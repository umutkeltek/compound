package eventstore

// Legacy migration (write-shape slice 4). Folds the loose one-record
// events/<id>.json files that predate the segment engine into framed segments
// through the NORMAL append path, records a verifiable manifest of what moved,
// archives every original (convert-don't-delete), and closes a fail-closed
// completion gate. After the gate closes the hot read/scan path drops the loose
// fallback entirely (see legacyFallbackEnabled): migrated ids resolve in
// segments, a reappearing loose file is a legacy-writer regression the doctor
// surfaces, and a corrupt segment record fails closed rather than silently
// falling back to the archived copy.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	migrationDirName    = "migration"
	manifestFileName    = "manifest.jsonl"
	planFileName        = "plan.json"
	completionFileName  = "completion.json"
	migrateLockName     = ".migrate.lock"
	conflictsDirName    = "conflicts"
	archiveTopDirName   = "archives"
	archiveGeneration   = "event-record-files-v0"
	archiveFilesDirName = "files"
	defaultMigrateBatch = 500

	planSchema       = "compound.eventstore.migration.plan.v0.1"
	manifestSchema   = "compound.eventstore.migration.manifest_row.v0.1"
	completionSchema = "compound.eventstore.migration.completion.v0.1"
	conflictSchema   = "compound.eventstore.migration.conflict.v0.1"

	statusMigrated       = "migrated"
	statusAlreadyPresent = "already_present"
	statusRejected       = "rejected"
)

// ErrMigrationConflict halts a run when a loose record and a segment record
// share an id but differ in content. Both bodies are preserved for the operator;
// the migration never guesses which one wins.
var ErrMigrationConflict = errors.New("event migration id conflict: same id, different content")

// MigrateMode selects the migration surface. Only Apply mutates state.
type MigrateMode int

const (
	ModeDryRun MigrateMode = iota
	ModeApply
	ModeVerify
)

// MigrateOptions parameterizes a single migration invocation. The two hooks are
// test-only fault injectors: returning a non-nil error simulates a process kill
// at a precise phase boundary for one id (segment committed / manifest written)
// so the resume-idempotency falsifiers exercise real crash windows.
type MigrateOptions struct {
	Mode           MigrateMode
	BatchSize      int
	beforeManifest func(id string) error
	beforeArchive  func(id string) error
}

// MigrateRejection names a loose file that could not be folded and why. A
// non-empty rejection set blocks completion until the operator resolves it.
type MigrateRejection struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// MigrateReport is the caller-facing outcome of one invocation.
type MigrateReport struct {
	Mode           string             `json:"mode"`
	Discovered     int                `json:"discovered"`
	Migrated       int                `json:"migrated"`
	AlreadyPresent int                `json:"already_present"`
	Rejected       int                `json:"rejected"`
	Archived       int                `json:"archived"`
	Conflicts      int                `json:"conflicts,omitempty"`
	ConflictIDs    []string           `json:"conflict_ids,omitempty"`
	PerFamily      map[string]int     `json:"per_family"`
	Rejections     []MigrateRejection `json:"rejections,omitempty"`
	Completion     CompletionReport   `json:"completion"`
}

// CompletionReport is the fail-closed gate. completion.json is written only when
// Complete is true. LegacyWriterActive is orthogonal to Complete: it reports a
// loose record that appeared AFTER the gate closed (a legacy-writer regression).
type CompletionReport struct {
	Schema             string         `json:"schema,omitempty"`
	Complete           bool           `json:"complete"`
	DiscoveredExpected int            `json:"legacy_discovered"`
	Migrated           int            `json:"migrated"`
	AlreadyPresent     int            `json:"already_present"`
	Rejected           int            `json:"rejected"`
	RemainingLoose     int            `json:"remaining_loose"`
	PerFamily          map[string]int `json:"per_family"`
	RollupDigest       string         `json:"rollup_digest"`
	ExpectedDigest     string         `json:"expected_digest"`
	Reasons            []string       `json:"reasons,omitempty"`
	LegacyWriterActive bool           `json:"legacy_writer_active"`
	CompletedAt        string         `json:"completed_at,omitempty"`
}

type manifestRow struct {
	Schema          string `json:"schema"`
	ID              string `json:"id"`
	SourceSHA256    string `json:"source_sha256"`
	CanonicalSHA256 string `json:"canonical_sha256,omitempty"`
	Family          string `json:"family,omitempty"`
	Segment         string `json:"segment,omitempty"`
	Offset          int64  `json:"offset"`
	SchemaVersion   string `json:"schema_version,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	RecordedAt      string `json:"recorded_at"`
}

type migratePlan struct {
	Schema          string         `json:"schema"`
	DiscoveredCount int            `json:"discovered_count"`
	PerFamily       map[string]int `json:"per_family"`
	ExpectedDigest  string         `json:"expected_digest"`
	CreatedAt       string         `json:"created_at"`
}

type conflictReport struct {
	Schema        string `json:"schema"`
	ID            string `json:"id"`
	LooseSHA256   string `json:"loose_canonical_sha256"`
	SegmentSHA256 string `json:"segment_canonical_sha256"`
	Segment       string `json:"segment"`
	DetectedAt    string `json:"detected_at"`
}

// segmentLocation is one id's authoritative physical coordinate + canonical hash
// as read from segment truth. It backs the O(1) presence/conflict decision so a
// bulk fold stays linear instead of re-scanning segments per record.
type segmentLocation struct {
	family  string
	relPath string
	offset  int64
	sha256  string
}

type looseFile struct {
	id   string
	name string
	path string
}

type migrator struct {
	store  *Store
	engine *engine
	opts   MigrateOptions
}

func (e *engine) migrationRoot() string  { return filepath.Join(e.eventsDir, migrationDirName) }
func (e *engine) manifestPath() string   { return filepath.Join(e.migrationRoot(), manifestFileName) }
func (e *engine) planPath() string       { return filepath.Join(e.migrationRoot(), planFileName) }
func (e *engine) completionPath() string { return filepath.Join(e.migrationRoot(), completionFileName) }

func (e *engine) archiveRoot() string {
	return filepath.Join(e.home, archiveTopDirName, archiveGeneration, archiveFilesDirName)
}

// migrationComplete reports whether the fail-closed completion gate has closed.
// A stat error other than not-exist is treated as "not complete" so the loose
// fallback stays live rather than dropping records on a transient fault.
func (e *engine) migrationComplete() bool {
	_, err := os.Stat(e.completionPath())
	return err == nil
}

func (e *engine) legacyFallbackEnabled() bool { return !e.migrationComplete() }

// buildSegmentPresence indexes every segment record by id → coordinate, matching
// getByID resolution (newest segment wins; within a segment the last frame wins).
// A corrupt sealed segment surfaces its error here, so every gate built on this
// index fails closed. Torn hot tails are tolerated exactly as the read path does.
func (e *engine) buildSegmentPresence() (map[string]segmentLocation, error) {
	presence := map[string]segmentLocation{}
	setBy := map[string]int{}
	families, err := e.segmentFamilies()
	if err != nil {
		return nil, err
	}
	for _, family := range families {
		segments, err := e.familySegments(family) // newest-first
		if err != nil {
			return nil, err
		}
		for index, segment := range segments {
			frames, err := parseSegment(segment.path, index == 0)
			if err != nil {
				return nil, err
			}
			rel, relErr := filepath.Rel(e.eventsDir, segment.path)
			if relErr != nil {
				rel = segment.path
			}
			for _, pf := range frames {
				id := pf.Frame.ID
				if prev, ok := setBy[id]; ok && prev < index {
					continue // a newer segment already owns this id
				}
				presence[id] = segmentLocation{family: family, relPath: rel, offset: pf.Offset, sha256: pf.Frame.SHA256}
				setBy[id] = index
			}
		}
	}
	return presence, nil
}

// Migrate folds the loose legacy records under one home. It is the single entry
// point behind `compound event-store migrate`.
func (s *Store) Migrate(opts MigrateOptions) (MigrateReport, error) {
	if s == nil || s.engine == nil {
		return MigrateReport{}, fmt.Errorf("event store requires a home")
	}
	m := &migrator{store: s, engine: s.engine, opts: opts}
	if m.opts.BatchSize <= 0 {
		m.opts.BatchSize = defaultMigrateBatch
	}
	switch opts.Mode {
	case ModeVerify:
		return m.verify()
	case ModeDryRun:
		return m.dryRun()
	case ModeApply:
		return m.apply()
	default:
		return MigrateReport{}, fmt.Errorf("unknown migrate mode %d", int(opts.Mode))
	}
}

// MigrationComplete reports whether this home's completion gate has closed.
func (s *Store) MigrationComplete() bool {
	if s == nil || s.engine == nil {
		return false
	}
	return s.engine.migrationComplete()
}

// LegacyWriterActive is the post-completion doctor/status check: it is true only
// when the gate has closed AND a loose events/<id>.json has reappeared, i.e. a
// legacy writer is still emitting one-record files after the cutover.
func (s *Store) LegacyWriterActive() (bool, error) {
	if s == nil || s.engine == nil {
		return false, fmt.Errorf("event store requires a home")
	}
	if !s.engine.migrationComplete() {
		return false, nil
	}
	m := &migrator{store: s, engine: s.engine}
	loose, err := m.enumerateLoose()
	if err != nil {
		return false, err
	}
	return len(loose) > 0, nil
}

func (m *migrator) now() string { return m.engine.clock().UTC().Format(time.RFC3339Nano) }

// enumerateLoose lists ONLY loose events/<id>.json record files. work.jsonl,
// hook-events.ndjson, traces.ndjson and the segments/index/migration/archives
// subdirectories are excluded by construction (subdirs and non-.json names),
// and dotfiles (in-flight atomic temps) are skipped.
func (m *migrator) enumerateLoose() ([]looseFile, error) {
	entries, err := os.ReadDir(m.engine.eventsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []looseFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		files = append(files, looseFile{id: id, name: name, path: filepath.Join(m.engine.eventsDir, name)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].id < files[j].id })
	return files, nil
}

func (m *migrator) apply() (MigrateReport, error) {
	if !m.engine.localFS {
		return MigrateReport{}, fmt.Errorf("legacy migration requires a local filesystem, detected %s", m.engine.fsType)
	}
	if err := m.store.Ensure(); err != nil {
		return MigrateReport{}, err
	}
	runLock, err := m.acquireRunLock()
	if err != nil {
		return MigrateReport{}, err
	}
	defer runLock.release()

	plan, err := m.loadOrCreatePlan()
	if err != nil {
		return MigrateReport{}, err
	}
	manifest, err := m.loadManifest()
	if err != nil {
		return MigrateReport{}, err
	}
	presence, err := m.engine.buildSegmentPresence()
	if err != nil {
		return MigrateReport{}, err
	}
	loose, err := m.enumerateLoose()
	if err != nil {
		return MigrateReport{}, err
	}

	report := MigrateReport{Mode: "apply", Discovered: plan.DiscoveredCount, PerFamily: map[string]int{}}
	for start := 0; start < len(loose); start += m.opts.BatchSize {
		end := min(start+m.opts.BatchSize, len(loose))
		if err := m.processBatch(loose[start:end], presence, manifest, &report); err != nil {
			if errors.Is(err, ErrMigrationConflict) {
				report.Conflicts++
			}
			return report, err
		}
	}

	// Re-derive presence from disk for the gate so completion proves segment
	// truth, not the in-memory projection of what this run appended.
	finalPresence, err := m.engine.buildSegmentPresence()
	if err != nil {
		return report, err
	}
	comp, err := m.evaluateCompletion(plan, finalPresence)
	if err != nil {
		return report, err
	}
	report.Completion = comp
	if comp.Complete && !m.engine.migrationComplete() {
		if err := m.writeCompletion(comp); err != nil {
			return report, err
		}
	}
	return report, nil
}

// processBatch holds the append writer lock for one bounded batch only (never
// the whole run) so live writers interleave between batches.
func (m *migrator) processBatch(batch []looseFile, presence map[string]segmentLocation, manifest map[string]manifestRow, report *MigrateReport) (retErr error) {
	lock, err := acquireAppendLock(m.engine.segRoot)
	if err != nil {
		return err
	}
	defer func() {
		if relErr := lock.release(); retErr == nil && relErr != nil {
			retErr = relErr
		}
	}()
	repaired := map[string]bool{}
	today := ingestionDay(m.engine.clock())
	for _, lf := range batch {
		if err := m.processRecord(lf, presence, manifest, repaired, today, report); err != nil {
			return err
		}
	}
	return nil
}

// processRecord folds one loose file. Ordering is segment-commit → manifest →
// archive, so every crash window is a resumable point: a segment-committed but
// manifest-absent record is seen already-present on retry and its manifest row
// is written; a manifest-present but unmoved original is archived on retry; an
// already-archived original is simply not re-enumerated.
func (m *migrator) processRecord(lf looseFile, presence map[string]segmentLocation, manifest map[string]manifestRow, repaired map[string]bool, today string, report *MigrateReport) error {
	data, err := os.ReadFile(lf.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // archived/removed by a prior run — nothing to do
		}
		return err
	}
	sourceSHA := recordHash(data)

	if regular, err := isRegularFile(lf.path); err != nil {
		return err
	} else if !regular {
		return m.recordRejected(lf, sourceSHA, manifest, report, "loose entry is not a regular file")
	}
	if _, err := ReferenceForID(lf.id); err != nil {
		return m.recordRejected(lf, sourceSHA, manifest, report, fmt.Sprintf("invalid event id: %v", err))
	}
	compact, err := canonical(data)
	if err != nil {
		return m.recordRejected(lf, sourceSHA, manifest, report, fmt.Sprintf("canonicalize: %v", err))
	}
	var header frameRecordHeader
	if err := json.Unmarshal(compact, &header); err != nil {
		return m.recordRejected(lf, sourceSHA, manifest, report, fmt.Sprintf("decode record header: %v", err))
	}
	if header.ID != lf.id {
		return m.recordRejected(lf, sourceSHA, manifest, report, fmt.Sprintf("embedded id %q does not match filename id %q", header.ID, lf.id))
	}
	canonSHA := recordHash(compact)
	family := familyOf(compact, lf.id)

	var segRel string
	var offset int64
	var status string
	if loc, ok := presence[lf.id]; ok {
		if loc.sha256 != canonSHA {
			return m.haltConflict(lf, data, canonSHA, loc)
		}
		segRel, offset, status = loc.relPath, loc.offset, statusAlreadyPresent
	} else {
		frame, _, err := buildFrame(lf.id, data)
		if err != nil {
			return m.recordRejected(lf, sourceSHA, manifest, report, fmt.Sprintf("build frame: %v", err))
		}
		line, err := encodeFrame(frame)
		if err != nil {
			return m.recordRejected(lf, sourceSHA, manifest, report, fmt.Sprintf("encode frame: %v", err))
		}
		if !repaired[family] {
			if last, exists, err := m.engine.lastWritten(family); err != nil {
				return err
			} else if exists {
				if err := m.engine.repairTail(last.path); err != nil {
					return err
				}
			}
			repaired[family] = true
		}
		rel, off, err := m.engine.appendFrameAt(family, today, line)
		if err != nil {
			return err
		}
		segRel, offset, status = rel, off, statusMigrated
		presence[lf.id] = segmentLocation{family: family, relPath: rel, offset: off, sha256: canonSHA}
	}

	row := manifestRow{
		Schema:          manifestSchema,
		ID:              lf.id,
		SourceSHA256:    sourceSHA,
		CanonicalSHA256: canonSHA,
		Family:          family,
		Segment:         segRel,
		Offset:          offset,
		SchemaVersion:   header.SchemaVersion,
		Status:          status,
		RecordedAt:      m.now(),
	}
	if existing, ok := manifest[lf.id]; !ok || existing.Status == statusRejected || existing.CanonicalSHA256 != canonSHA {
		if m.opts.beforeManifest != nil { // crash window (a): segment committed, manifest absent
			if err := m.opts.beforeManifest(lf.id); err != nil {
				return err
			}
		}
		if err := m.appendManifest(row); err != nil {
			return err
		}
		manifest[lf.id] = row
	}

	if m.opts.beforeArchive != nil { // crash window (b): manifest written, original not moved
		if err := m.opts.beforeArchive(lf.id); err != nil {
			return err
		}
	}
	if err := m.archiveLoose(lf, sourceSHA); err != nil {
		return err
	}

	switch status {
	case statusMigrated:
		report.Migrated++
		report.Archived++
		report.PerFamily[family]++
	case statusAlreadyPresent:
		report.AlreadyPresent++
		report.Archived++
		report.PerFamily[family]++
	}
	return nil
}

func (m *migrator) recordRejected(lf looseFile, sourceSHA string, manifest map[string]manifestRow, report *MigrateReport, reason string) error {
	row := manifestRow{
		Schema:       manifestSchema,
		ID:           lf.id,
		SourceSHA256: sourceSHA,
		Status:       statusRejected,
		Reason:       reason,
		RecordedAt:   m.now(),
	}
	if existing, ok := manifest[lf.id]; !ok || existing.Status != statusRejected || existing.Reason != reason {
		if err := m.appendManifest(row); err != nil {
			return err
		}
		manifest[lf.id] = row
	}
	report.Rejected++
	report.Rejections = append(report.Rejections, MigrateRejection{ID: lf.id, Reason: reason})
	// Rejected originals are deliberately left in place (not archived): they block
	// completion until the operator resolves them.
	return nil
}

// haltConflict preserves both bodies and stops the run. The loose original stays
// in place; the segment record and a conflict descriptor are copied beside it.
func (m *migrator) haltConflict(lf looseFile, looseData []byte, looseCanonSHA string, loc segmentLocation) error {
	dir := filepath.Join(m.engine.migrationRoot(), conflictsDirName, lf.id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(dir, "loose.json"), looseData); err != nil {
		return err
	}
	if hit, found, err := m.engine.segmentFrame(lf.id, ""); err == nil && found {
		if err := writeFileSync(filepath.Join(dir, "segment.json"), append(append([]byte(nil), hit.frame.Record...), '\n')); err != nil {
			return err
		}
	}
	detail := conflictReport{
		Schema:        conflictSchema,
		ID:            lf.id,
		LooseSHA256:   looseCanonSHA,
		SegmentSHA256: loc.sha256,
		Segment:       loc.relPath,
		DetectedAt:    m.now(),
	}
	payload, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(dir, "conflict.json"), append(payload, '\n')); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s (loose canonical %s vs segment %s); both bodies preserved at %s",
		ErrMigrationConflict, lf.id, looseCanonSHA, loc.sha256, dir)
}

// archiveLoose atomically renames the original into the sharded archive. It is
// idempotent across every crash window: a vanished original with a present
// archive is a completed move; an identical archive already in place removes the
// original; a different archive is a hard error.
func (m *migrator) archiveLoose(lf looseFile, sourceSHA string) error {
	shard := recordHash([]byte(lf.id))[:2]
	dir := filepath.Join(m.engine.archiveRoot(), shard)
	dest := filepath.Join(dir, lf.id+".json")
	if _, err := os.Stat(lf.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, derr := os.Stat(dest); derr == nil {
				return nil // already archived by a prior run
			}
			return fmt.Errorf("loose record %s vanished with no archive at %s", lf.id, dest)
		}
		return err
	}
	if existing, err := os.ReadFile(dest); err == nil {
		if recordHash(existing) == sourceSHA {
			return os.Remove(lf.path) // identical archive already present
		}
		return fmt.Errorf("archive %s already exists with different bytes", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Rename(lf.path, dest); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func (m *migrator) appendManifest(row manifestRow) error {
	if err := os.MkdirAll(m.engine.migrationRoot(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(m.engine.manifestPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := writeFull(f, data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (m *migrator) loadManifest() (map[string]manifestRow, error) {
	rows := map[string]manifestRow{}
	data, err := os.ReadFile(m.engine.manifestPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rows, nil
		}
		return nil, err
	}
	trailingNewline := len(data) > 0 && data[len(data)-1] == '\n'
	parts := strings.Split(string(data), "\n")
	lastNonEmpty := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			lastNonEmpty = i
			break
		}
	}
	for i, part := range parts {
		if part == "" {
			continue
		}
		var row manifestRow
		if err := json.Unmarshal([]byte(part), &row); err != nil {
			// A torn FINAL line (no trailing newline) is a crash mid-append and is
			// tolerated; a torn interior line is manifest corruption and surfaces.
			if i == lastNonEmpty && !trailingNewline {
				break
			}
			return nil, fmt.Errorf("event migration manifest line %d: %w", i+1, err)
		}
		if row.ID != "" {
			rows[row.ID] = row
		}
	}
	return rows, nil
}

func (m *migrator) loadOrCreatePlan() (migratePlan, error) {
	if plan, ok, err := m.loadPlan(); err != nil {
		return migratePlan{}, err
	} else if ok {
		return plan, nil
	}
	plan, err := m.computePlan()
	if err != nil {
		return migratePlan{}, err
	}
	if err := m.writePlanIfAbsent(plan); err != nil {
		return migratePlan{}, err
	}
	if stored, ok, rerr := m.loadPlan(); rerr == nil && ok {
		return stored, nil
	}
	return plan, nil
}

func (m *migrator) loadPlan() (migratePlan, bool, error) {
	data, err := os.ReadFile(m.engine.planPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return migratePlan{}, false, nil
		}
		return migratePlan{}, false, err
	}
	var plan migratePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return migratePlan{}, false, fmt.Errorf("decode migration plan: %w", err)
	}
	return plan, true, nil
}

// computePlan captures the immutable "what must end up migrated" contract from
// the full loose enumeration: discovered_count anchors the count gate and
// expected_digest anchors the sorted (id, canonical_sha256) rollup. Invalid /
// non-record files count toward discovered_count (they become rejected) but are
// excluded from the digest.
func (m *migrator) computePlan() (migratePlan, error) {
	loose, err := m.enumerateLoose()
	if err != nil {
		return migratePlan{}, err
	}
	perFamily := map[string]int{}
	var pairs []string
	for _, lf := range loose {
		data, err := os.ReadFile(lf.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return migratePlan{}, err
		}
		if regular, err := isRegularFile(lf.path); err != nil || !regular {
			continue
		}
		if _, err := ReferenceForID(lf.id); err != nil {
			continue
		}
		compact, err := canonical(data)
		if err != nil {
			continue
		}
		var header frameRecordHeader
		if err := json.Unmarshal(compact, &header); err != nil || header.ID != lf.id {
			continue
		}
		pairs = append(pairs, lf.id+" "+recordHash(compact))
		perFamily[familyOf(compact, lf.id)]++
	}
	sort.Strings(pairs)
	return migratePlan{
		Schema:          planSchema,
		DiscoveredCount: len(loose),
		PerFamily:       perFamily,
		ExpectedDigest:  recordHash([]byte(strings.Join(pairs, "\n"))),
		CreatedAt:       m.now(),
	}, nil
}

func (m *migrator) writePlanIfAbsent(plan migratePlan) error {
	if err := os.MkdirAll(m.engine.migrationRoot(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(m.engine.planPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil // a concurrent apply already wrote the plan
		}
		return err
	}
	if err := writeFull(f, data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDirectory(m.engine.migrationRoot())
}

// evaluateCompletion runs the fail-closed gate against durable state. It never
// writes. Every migrated/already-present manifest row must resolve to a segment
// record with a matching canonical hash (a missing or corrupt segment record
// refuses completion), the accounted count must equal the plan's discovered
// count, no loose files may remain, and the manifest rollup digest must equal
// the plan's expected digest.
func (m *migrator) evaluateCompletion(plan migratePlan, presence map[string]segmentLocation) (CompletionReport, error) {
	manifest, err := m.loadManifest()
	if err != nil {
		return CompletionReport{}, err
	}
	comp := CompletionReport{
		PerFamily:          map[string]int{},
		DiscoveredExpected: plan.DiscoveredCount,
		ExpectedDigest:     plan.ExpectedDigest,
	}
	var pairs []string
	for id, row := range manifest {
		switch row.Status {
		case statusRejected:
			comp.Rejected++
			continue
		case statusMigrated:
			comp.Migrated++
		case statusAlreadyPresent:
			comp.AlreadyPresent++
		default:
			comp.Reasons = append(comp.Reasons, fmt.Sprintf("manifest row %s has unknown status %q", id, row.Status))
			continue
		}
		comp.PerFamily[row.Family]++
		pairs = append(pairs, id+" "+row.CanonicalSHA256)
		loc, ok := presence[id]
		if !ok {
			comp.Reasons = append(comp.Reasons, fmt.Sprintf("migrated record %s is not present in any segment", id))
			continue
		}
		if loc.sha256 != row.CanonicalSHA256 {
			comp.Reasons = append(comp.Reasons, fmt.Sprintf("segment record %s canonical hash %s differs from manifest %s", id, loc.sha256, row.CanonicalSHA256))
		}
	}
	sort.Strings(pairs)
	comp.RollupDigest = recordHash([]byte(strings.Join(pairs, "\n")))

	loose, err := m.enumerateLoose()
	if err != nil {
		return CompletionReport{}, err
	}
	comp.RemainingLoose = len(loose)

	if comp.Rejected != 0 {
		comp.Reasons = append(comp.Reasons, fmt.Sprintf("%d rejected records must be resolved", comp.Rejected))
	}
	accounted := comp.Migrated + comp.AlreadyPresent + comp.Rejected
	if accounted != plan.DiscoveredCount {
		comp.Reasons = append(comp.Reasons, fmt.Sprintf("accounted %d != legacy_discovered %d", accounted, plan.DiscoveredCount))
	}
	if comp.RemainingLoose != 0 {
		comp.Reasons = append(comp.Reasons, fmt.Sprintf("%d loose record files remain unmigrated", comp.RemainingLoose))
	}
	if comp.RollupDigest != plan.ExpectedDigest {
		comp.Reasons = append(comp.Reasons, "manifest (id, canonical_sha256) rollup digest does not equal the plan digest")
	}
	for family, want := range plan.PerFamily {
		if comp.PerFamily[family] != want {
			comp.Reasons = append(comp.Reasons, fmt.Sprintf("family %s: manifest count %d != plan count %d", family, comp.PerFamily[family], want))
		}
	}
	for family, got := range comp.PerFamily {
		if _, ok := plan.PerFamily[family]; !ok {
			comp.Reasons = append(comp.Reasons, fmt.Sprintf("family %s: %d migrated records absent from plan", family, got))
		}
	}
	comp.Complete = len(comp.Reasons) == 0
	comp.LegacyWriterActive = m.engine.migrationComplete() && comp.RemainingLoose > 0
	if comp.Complete {
		comp.CompletedAt = m.now()
	}
	return comp, nil
}

func (m *migrator) verify() (MigrateReport, error) {
	plan, ok, err := m.loadPlan()
	if err != nil {
		return MigrateReport{}, err
	}
	presence, err := m.engine.buildSegmentPresence()
	if err != nil {
		return MigrateReport{}, err
	}
	comp, err := m.evaluateCompletion(plan, presence)
	if err != nil {
		return MigrateReport{}, err
	}
	if !ok {
		comp.Complete = false
		comp.Reasons = append([]string{"no migration plan found; run `compound event-store migrate --apply` first"}, comp.Reasons...)
	}
	return MigrateReport{
		Mode:           "verify",
		Discovered:     comp.DiscoveredExpected,
		Migrated:       comp.Migrated,
		AlreadyPresent: comp.AlreadyPresent,
		Rejected:       comp.Rejected,
		PerFamily:      comp.PerFamily,
		Completion:     comp,
	}, nil
}

func (m *migrator) dryRun() (MigrateReport, error) {
	loose, err := m.enumerateLoose()
	if err != nil {
		return MigrateReport{}, err
	}
	presence, err := m.engine.buildSegmentPresence()
	if err != nil {
		return MigrateReport{}, err
	}
	report := MigrateReport{Mode: "dry-run", Discovered: len(loose), PerFamily: map[string]int{}}
	for _, lf := range loose {
		data, err := os.ReadFile(lf.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return MigrateReport{}, err
		}
		reason := ""
		var compact []byte
		var header frameRecordHeader
		if regular, rerr := isRegularFile(lf.path); rerr != nil {
			return MigrateReport{}, rerr
		} else if !regular {
			reason = "loose entry is not a regular file"
		} else if _, err := ReferenceForID(lf.id); err != nil {
			reason = fmt.Sprintf("invalid event id: %v", err)
		} else if compact, err = canonical(data); err != nil {
			reason = fmt.Sprintf("canonicalize: %v", err)
		} else if err := json.Unmarshal(compact, &header); err != nil {
			reason = fmt.Sprintf("decode record header: %v", err)
		} else if header.ID != lf.id {
			reason = fmt.Sprintf("embedded id %q does not match filename id %q", header.ID, lf.id)
		}
		if reason != "" {
			report.Rejected++
			report.Rejections = append(report.Rejections, MigrateRejection{ID: lf.id, Reason: reason})
			continue
		}
		canonSHA := recordHash(compact)
		family := familyOf(compact, lf.id)
		if loc, ok := presence[lf.id]; ok {
			if loc.sha256 != canonSHA {
				report.Conflicts++
				report.ConflictIDs = append(report.ConflictIDs, lf.id)
				continue
			}
			report.AlreadyPresent++
			report.PerFamily[family]++
			continue
		}
		report.Migrated++
		report.PerFamily[family]++
	}
	return report, nil
}

func (m *migrator) writeCompletion(comp CompletionReport) error {
	if err := os.MkdirAll(m.engine.migrationRoot(), 0o755); err != nil {
		return err
	}
	comp.Schema = completionSchema
	data, err := json.MarshalIndent(comp, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(m.engine.completionPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if err := writeFull(f, data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDirectory(m.engine.migrationRoot())
}

// acquireRunLock guards against two concurrent migrations. It is a whole-run
// lock distinct from the per-batch append writer lock, so it never blocks live
// writers — only a second migrator.
func (m *migrator) acquireRunLock() (*appendLock, error) {
	if err := os.MkdirAll(m.engine.migrationRoot(), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(m.engine.migrationRoot(), migrateLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another event-store migration is already running: %w", err)
	}
	return &appendLock{file: file}, nil
}

func writeFileSync(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := writeFull(f, data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func isRegularFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}
