package home

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"

	"gopkg.in/yaml.v3"
)

const (
	ArchiveSchemaVersion     = "compound.home_archive.v0.1"
	ArchiveManifestName      = "compound-home-archive.json"
	PublicFixtureSummaryName = "compound-home-public-fixture.json"

	ArchiveKindBackup        = "backup"
	ArchiveKindPublicFixture = "public_fixture"
	ArchiveKindOperational   = "operational_history"
)

var backupExcludedRoots = []string{"exports", "indexes", "lock", "projections", "tmp"}

// ArchiveEntry records one checksummed file inside a home archive.
type ArchiveEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ArchiveManifest is stored inside every home archive.
type ArchiveManifest struct {
	SchemaVersion     string         `json:"schema_version"`
	Kind              string         `json:"kind"`
	CreatedAt         string         `json:"created_at"`
	SourceHome        string         `json:"source_home,omitempty"`
	HomeSchemaVersion string         `json:"home_schema_version,omitempty"`
	ExcludedRoots     []string       `json:"excluded_roots,omitempty"`
	Entries           []ArchiveEntry `json:"entries"`
}

type ArchiveReport struct {
	Kind        string
	ArchivePath string
	Files       int
	Bytes       int64
	Skipped     []string
}

type VerifyReport struct {
	Kind        string
	ArchivePath string
	Files       int
	Bytes       int64
	OK          bool
}

type RestoreReport struct {
	ArchivePath string
	TargetHome  string
	Kind        string
	Files       int
	Bytes       int64
}

type CompactOptions struct {
	DryRun      bool
	Rebuildable bool
}

type CompactReport struct {
	Home         string
	DryRun       bool
	ClearedDirs  []string
	RemovedFiles int
	RemovedBytes int64
}

const (
	OperationalArchiveSchemaVersion = "compound.operational_archive.v0.1"
	OperationalArchiveIndexRel      = "archives/operational/index.jsonl"
	OperationalArchiveFilesRel      = "archives/operational/files"
)

type OperationalArchiveOptions struct {
	DryRun bool
	Kinds  []string
	Paths  []string
}

type OperationalRestoreOptions struct {
	DryRun bool
	All    bool
	IDs    []string
}

type OperationalArchiveEntry struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Operation     string `json:"operation"`
	CreatedAt     string `json:"created_at"`
	Kind          string `json:"kind"`
	Status        string `json:"status,omitempty"`
	OriginalPath  string `json:"original_path"`
	ArchivePath   string `json:"archive_path"`
	Size          int64  `json:"size,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
}

type OperationalArchiveReport struct {
	SchemaVersion string                    `json:"schema_version"`
	Home          string                    `json:"home"`
	DryRun        bool                      `json:"dry_run"`
	ArchivedAt    string                    `json:"archived_at,omitempty"`
	RestoredAt    string                    `json:"restored_at,omitempty"`
	ArchivedCount int                       `json:"archived_count,omitempty"`
	RestoredCount int                       `json:"restored_count,omitempty"`
	Counts        map[string]int            `json:"counts,omitempty"`
	Entries       []OperationalArchiveEntry `json:"entries"`
}

type operationalCandidate struct {
	Kind         string
	Status       string
	OriginalPath string
}

// Backup writes a durable COMPOUND_HOME archive. Transient and rebuildable roots
// are excluded so backup size tracks owned evidence, not cache mass.
func Backup(homePath, targetPath string, now time.Time) (ArchiveReport, error) {
	homePath, targetPath, err := normalizeArchiveInputs(homePath, targetPath, ArchiveKindBackup, now)
	if err != nil {
		return ArchiveReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return ArchiveReport{}, err
	}
	report, err := writeHomeArchive(homePath, targetPath, ArchiveKindBackup, backupExcludedRoots, now, includeBackupPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	return report, nil
}

// ExportPublicFixture writes a redacted fixture archive: habitat shape and
// sanitized manifest only, no runtime ledger/events/context/private files.
func ExportPublicFixture(homePath, targetPath string, now time.Time) (ArchiveReport, error) {
	homePath, targetPath, err := normalizeArchiveInputs(homePath, targetPath, ArchiveKindPublicFixture, now)
	if err != nil {
		return ArchiveReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return ArchiveReport{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ArchiveReport{}, err
	}
	f, err := os.Create(targetPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	entries := []ArchiveEntry{}
	var bytesWritten int64
	for _, dir := range Dirs {
		if err := writeDirEntry(tw, filepath.ToSlash(dir)); err != nil {
			return ArchiveReport{}, err
		}
	}
	fixtureManifest := Manifest{
		SchemaVersion: SchemaVersion,
		HomeKind:      ArchiveKindPublicFixture,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		ActiveModules: []string{},
	}
	manifestBytes, err := yaml.Marshal(fixtureManifest)
	if err != nil {
		return ArchiveReport{}, err
	}
	entry, err := writeBytesEntry(tw, ManifestName, manifestBytes, 0o644)
	if err != nil {
		return ArchiveReport{}, err
	}
	entries = append(entries, entry)
	bytesWritten += entry.Size

	summaryBytes, err := json.MarshalIndent(map[string]any{
		"schema_version":    "compound.home_public_fixture.v0.1",
		"created_at":        now.UTC().Format(time.RFC3339),
		"home_schema":       SchemaVersion,
		"dirs":              Dirs,
		"private_data_note": "runtime files are intentionally omitted; this archive is safe as a public fixture shape",
	}, "", "  ")
	if err != nil {
		return ArchiveReport{}, err
	}
	entry, err = writeBytesEntry(tw, PublicFixtureSummaryName, summaryBytes, 0o644)
	if err != nil {
		return ArchiveReport{}, err
	}
	entries = append(entries, entry)
	bytesWritten += entry.Size

	archiveManifest := ArchiveManifest{
		SchemaVersion:     ArchiveSchemaVersion,
		Kind:              ArchiveKindPublicFixture,
		CreatedAt:         now.UTC().Format(time.RFC3339),
		SourceHome:        homePath,
		HomeSchemaVersion: SchemaVersion,
		ExcludedRoots:     []string{"all runtime files"},
		Entries:           sortedArchiveEntries(entries),
	}
	if err := writeArchiveManifest(tw, archiveManifest); err != nil {
		return ArchiveReport{}, err
	}
	return ArchiveReport{
		Kind:        ArchiveKindPublicFixture,
		ArchivePath: targetPath,
		Files:       len(entries),
		Bytes:       bytesWritten,
		Skipped:     archiveManifest.ExcludedRoots,
	}, nil
}

// VerifyArchive checks the archive manifest and file checksums.
func VerifyArchive(archivePath string) (VerifyReport, error) {
	manifest, actual, err := readArchive(archivePath, false, "")
	if err != nil {
		return VerifyReport{}, err
	}
	expected := map[string]ArchiveEntry{}
	for _, entry := range manifest.Entries {
		expected[entry.Path] = entry
	}
	if len(expected) != len(actual) {
		return VerifyReport{}, fmt.Errorf("archive entry count mismatch: manifest=%d actual=%d", len(expected), len(actual))
	}
	var bytesTotal int64
	for path, want := range expected {
		got, ok := actual[path]
		if !ok {
			return VerifyReport{}, fmt.Errorf("archive missing %s", path)
		}
		if got.Size != want.Size || got.SHA256 != want.SHA256 {
			return VerifyReport{}, fmt.Errorf("archive checksum mismatch for %s", path)
		}
		bytesTotal += got.Size
	}
	return VerifyReport{
		Kind:        manifest.Kind,
		ArchivePath: archivePath,
		Files:       len(actual),
		Bytes:       bytesTotal,
		OK:          true,
	}, nil
}

// RestoreArchive extracts a verified archive into an empty target home.
func RestoreArchive(archivePath, targetHome string, now time.Time) (RestoreReport, error) {
	if strings.TrimSpace(targetHome) == "" {
		return RestoreReport{}, fmt.Errorf("target home required")
	}
	targetHome, err := filepath.Abs(expandUser(targetHome))
	if err != nil {
		return RestoreReport{}, err
	}
	if ok, err := dirEmptyOrMissing(targetHome); err != nil {
		return RestoreReport{}, err
	} else if !ok {
		return RestoreReport{}, fmt.Errorf("target home %s is not empty", targetHome)
	}
	verify, err := VerifyArchive(archivePath)
	if err != nil {
		return RestoreReport{}, err
	}
	if _, _, err := readArchive(archivePath, true, targetHome); err != nil {
		return RestoreReport{}, err
	}
	if err := Init(targetHome, "restored", now); err != nil {
		return RestoreReport{}, err
	}
	if !Doctor(targetHome).OK {
		return RestoreReport{}, fmt.Errorf("restored home failed doctor")
	}
	return RestoreReport{
		ArchivePath: archivePath,
		TargetHome:  targetHome,
		Kind:        verify.Kind,
		Files:       verify.Files,
		Bytes:       verify.Bytes,
	}, nil
}

// Compact clears owned transient dirs. With Rebuildable it also clears cache-like
// indexes/projections that can be regenerated from ledger/events.
func Compact(homePath string, opts CompactOptions) (CompactReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return CompactReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return CompactReport{}, err
	}
	dirs := []string{"lock", "tmp"}
	if opts.Rebuildable {
		dirs = append(dirs, "indexes", "projections")
	}
	report := CompactReport{Home: homePath, DryRun: opts.DryRun, ClearedDirs: append([]string(nil), dirs...)}
	for _, dir := range dirs {
		full := filepath.Join(homePath, dir)
		if !isUnderHome(homePath, full) {
			return CompactReport{}, fmt.Errorf("refusing compact path outside home: %s", full)
		}
		files, bytes, err := measureTree(full)
		if err != nil {
			return CompactReport{}, err
		}
		report.RemovedFiles += files
		report.RemovedBytes += bytes
		if opts.DryRun {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return CompactReport{}, err
		}
		if err := os.MkdirAll(full, 0o755); err != nil {
			return CompactReport{}, err
		}
	}
	return report, nil
}

func OperationalArchiveIndexPath(homePath string) string {
	return filepath.Join(homePath, OperationalArchiveIndexRel)
}

func OperationalArchiveFilesRoot(homePath string) string {
	return filepath.Join(homePath, OperationalArchiveFilesRel)
}

func LoadOperationalArchiveIndex(homePath string) ([]OperationalArchiveEntry, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(OperationalArchiveIndexPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	active := map[string]OperationalArchiveEntry{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry OperationalArchiveEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse operational archive index line %d: %w", i+1, err)
		}
		if entry.SchemaVersion != OperationalArchiveSchemaVersion {
			return nil, fmt.Errorf("operational archive index line %d schema_version must be %s, got %q", i+1, OperationalArchiveSchemaVersion, entry.SchemaVersion)
		}
		switch entry.Operation {
		case "archived":
			active[entry.ID] = entry
		case "restored":
			delete(active, entry.ID)
		default:
			return nil, fmt.Errorf("operational archive index line %d has unknown operation %q", i+1, entry.Operation)
		}
	}
	out := make([]OperationalArchiveEntry, 0, len(active))
	for _, entry := range active {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].OriginalPath < out[j].OriginalPath
	})
	return out, nil
}

func ArchiveOperationalHistory(homePath string, opts OperationalArchiveOptions, now time.Time) (OperationalArchiveReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return OperationalArchiveReport{}, err
	}
	report := OperationalArchiveReport{
		SchemaVersion: OperationalArchiveSchemaVersion,
		Home:          homePath,
		DryRun:        opts.DryRun,
		ArchivedAt:    now.UTC().Format(time.RFC3339),
		Counts:        map[string]int{},
	}
	active, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	alreadyArchived := map[string]bool{}
	for _, entry := range active {
		alreadyArchived[entry.OriginalPath] = true
	}
	candidates, err := operationalArchiveCandidates(homePath, opts, now)
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	for _, cand := range candidates {
		if alreadyArchived[cand.OriginalPath] {
			continue
		}
		entry, err := archiveOperationalFile(homePath, cand, now, opts.DryRun)
		if err != nil {
			return OperationalArchiveReport{}, err
		}
		report.Entries = append(report.Entries, entry)
		report.Counts[entry.Kind]++
	}
	report.ArchivedCount = len(report.Entries)
	if !opts.DryRun && len(report.Entries) > 0 {
		if err := appendOperationalArchiveEntries(homePath, report.Entries); err != nil {
			return OperationalArchiveReport{}, err
		}
	}
	return report, nil
}

func RestoreOperationalArchive(homePath string, opts OperationalRestoreOptions, now time.Time) (OperationalArchiveReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return OperationalArchiveReport{}, err
	}
	active, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	selected := selectOperationalArchiveEntries(active, opts)
	if len(selected) == 0 && !opts.All && len(opts.IDs) == 0 {
		return OperationalArchiveReport{}, fmt.Errorf("restore requires --all or at least one --id")
	}
	report := OperationalArchiveReport{
		SchemaVersion: OperationalArchiveSchemaVersion,
		Home:          homePath,
		DryRun:        opts.DryRun,
		RestoredAt:    now.UTC().Format(time.RFC3339),
		Counts:        map[string]int{},
	}
	for _, entry := range selected {
		restored, err := restoreOperationalFile(homePath, entry, now, opts.DryRun)
		if err != nil {
			return OperationalArchiveReport{}, err
		}
		report.Entries = append(report.Entries, restored)
		report.Counts[restored.Kind]++
	}
	report.RestoredCount = len(report.Entries)
	if !opts.DryRun && len(report.Entries) > 0 {
		if err := appendOperationalArchiveEntries(homePath, report.Entries); err != nil {
			return OperationalArchiveReport{}, err
		}
	}
	return report, nil
}

func operationalArchiveCandidates(homePath string, opts OperationalArchiveOptions, now time.Time) ([]operationalCandidate, error) {
	kinds, err := operationalArchiveKinds(opts.Kinds)
	if err != nil {
		return nil, err
	}
	paths, err := operationalArchivePathFilter(opts.Paths)
	if err != nil {
		return nil, err
	}
	var out []operationalCandidate
	if kinds["work"] {
		candidates, err := terminalWorkCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["lease"] {
		candidates, err := staleLeaseCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["review"] {
		candidates, err := terminalReviewCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["comparison"] {
		candidates, err := terminalComparisonCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["create-plan"] {
		candidates, err := staleCreatePlanCandidates(homePath, now)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
		if len(paths) > 0 {
			candidates, err := explicitCreatePlanCandidates(homePath, paths, now)
			if err != nil {
				return nil, err
			}
			out = append(out, candidates...)
		}
	}
	if kinds["memory-candidate"] {
		candidates, err := terminalMemoryCandidateCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if len(paths) > 0 {
		filtered := out[:0]
		for _, candidate := range out {
			if paths[candidate.OriginalPath] {
				filtered = append(filtered, candidate)
			}
		}
		out = filtered
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OriginalPath < out[j].OriginalPath
	})
	return out, nil
}

func operationalArchiveKinds(values []string) (map[string]bool, error) {
	// memory-candidate is opt-in only (landfill compaction); not in the default
	// archive-history mix so a routine work/lease sweep never sweeps the inbox.
	allowed := map[string]bool{"work": true, "lease": true, "review": true, "comparison": true, "create-plan": true, "memory-candidate": true}
	out := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				if !allowed[part] {
					return nil, fmt.Errorf("unknown operational archive kind %q", part)
				}
				out[part] = true
			}
		}
	}
	if len(out) == 0 {
		out["work"] = true
		out["lease"] = true
		out["review"] = true
		out["comparison"] = true
		out["create-plan"] = true
	}
	return out, nil
}

// terminalMemoryCandidateCandidates selects memory inbox rows that are fully
// terminal for landfill compaction: no_promote, rejected, promoted.
// Parked/proposed stay hot (corroboration / operator gate). Claims ledger is
// never touched — provenance stays in ledger/claims.jsonl.
func terminalMemoryCandidateCandidates(homePath string) ([]operationalCandidate, error) {
	dirName := candidates.InboxDir(candidates.Memory)
	if dirName == "" {
		return nil, nil
	}
	dir := filepath.Join(homePath, dirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(dirName, entry.Name()))
		status, err := jsonStringField(filepath.Join(homePath, filepath.FromSlash(rel)), "state")
		if err != nil {
			return nil, err
		}
		if landfillMemoryCandidateStatus(status) {
			out = append(out, operationalCandidate{Kind: "memory-candidate", Status: status, OriginalPath: rel})
		}
	}
	return out, nil
}

func landfillMemoryCandidateStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "no_promote", "rejected", "promoted":
		return true
	default:
		return false
	}
}

func operationalArchivePathFilter(values []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = filepath.ToSlash(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			part = strings.TrimPrefix(part, "./")
			if err := validateArchivePath(part); err != nil {
				return nil, err
			}
			out[part] = true
		}
	}
	return out, nil
}

func terminalWorkCandidates(homePath string) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "work", "work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("work", "work", entry.Name()))
		status, err := jsonStringField(filepath.Join(homePath, filepath.FromSlash(rel)), "state")
		if err != nil {
			return nil, err
		}
		if terminalWorkStatus(status) {
			out = append(out, operationalCandidate{Kind: "work", Status: status, OriginalPath: rel})
		}
	}
	return out, nil
}

// staleLeaseCandidates finds work/leases/*.json whose work_id no longer maps to
// a live WorkEnvelope: the hot work is terminal, already archived, or missing
// entirely (an orphan). Leases for live (non-terminal, hot) work are never
// candidates. Claim leases are not otherwise swept on close or archive, so this
// is the continuous cleanup path for the lease lifecycle.
func staleLeaseCandidates(homePath string) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "work", "leases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var archivedWork map[string]bool
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		workID := strings.TrimSuffix(entry.Name(), ".json")
		rel := filepath.ToSlash(filepath.Join("work", "leases", entry.Name()))
		state, err := jsonStringField(filepath.Join(homePath, "work", "work", workID+".json"), "state")
		switch {
		case err == nil && !terminalWorkStatus(state):
			continue // live work: its lease must survive the sweep
		case err == nil:
			out = append(out, operationalCandidate{Kind: "lease", Status: state, OriginalPath: rel})
		case os.IsNotExist(err):
			if archivedWork == nil {
				ids, idsErr := archivedWorkIDs(homePath)
				if idsErr != nil {
					return nil, idsErr
				}
				archivedWork = ids
			}
			status := "orphaned"
			if archivedWork[workID] {
				status = "work_archived"
			}
			out = append(out, operationalCandidate{Kind: "lease", Status: status, OriginalPath: rel})
		default:
			return nil, err
		}
	}
	return out, nil
}

const staleCreatePlanAfter = 24 * time.Hour

func explicitCreatePlanCandidates(homePath string, paths map[string]bool, now time.Time) ([]operationalCandidate, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var out []operationalCandidate
	for rel := range paths {
		if !strings.HasPrefix(rel, "create-plans/") || filepath.Ext(rel) != ".json" {
			continue
		}
		candidate, ok, err := explicitCreatePlanCandidate(homePath, rel, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, candidate)
		}
	}
	return out, nil
}

func explicitCreatePlanCandidate(homePath, rel string, now time.Time) (operationalCandidate, bool, error) {
	path := filepath.Join(homePath, filepath.FromSlash(rel))
	var plan struct {
		SchemaVersion string `json:"schema_version"`
		ID            string `json:"id"`
		GeneratedAt   string `json:"generated_at"`
		Status        string `json:"status"`
		WorkID        string `json:"work_id"`
		Policy        struct {
			Allowed bool `json:"allowed"`
		} `json:"policy"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return operationalCandidate{}, false, nil
		}
		return operationalCandidate{}, false, err
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return operationalCandidate{}, false, fmt.Errorf("parse create-plan %s: %w", path, err)
	}
	if plan.SchemaVersion != "compound.work_create_plan.v0.1" ||
		strings.TrimSpace(plan.Status) != "create_policy_allowed" ||
		!plan.Policy.Allowed ||
		strings.TrimSpace(plan.ID) == "" ||
		strings.TrimSpace(plan.WorkID) == "" {
		return operationalCandidate{}, false, nil
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "work", plan.WorkID+".json")); err == nil {
		return operationalCandidate{}, false, nil
	} else if !os.IsNotExist(err) {
		return operationalCandidate{}, false, err
	}
	generatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(plan.GeneratedAt))
	if err != nil {
		return operationalCandidate{}, false, fmt.Errorf("parse create-plan generated_at %s: %w", path, err)
	}
	if now.UTC().Sub(generatedAt.UTC()) >= staleCreatePlanAfter {
		return operationalCandidate{}, false, nil
	}
	return operationalCandidate{Kind: "create-plan", Status: "explicit_unadmitted", OriginalPath: rel}, true, nil
}

func staleCreatePlanCandidates(homePath string, now time.Time) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "create-plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var plan struct {
			SchemaVersion string `json:"schema_version"`
			ID            string `json:"id"`
			GeneratedAt   string `json:"generated_at"`
			Status        string `json:"status"`
			WorkID        string `json:"work_id"`
			Policy        struct {
				Allowed bool `json:"allowed"`
			} `json:"policy"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &plan); err != nil {
			return nil, fmt.Errorf("parse create-plan %s: %w", path, err)
		}
		if plan.SchemaVersion != "compound.work_create_plan.v0.1" ||
			strings.TrimSpace(plan.Status) != "create_policy_allowed" ||
			!plan.Policy.Allowed ||
			strings.TrimSpace(plan.ID) == "" ||
			strings.TrimSpace(plan.WorkID) == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(homePath, "work", "work", plan.WorkID+".json")); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		generatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(plan.GeneratedAt))
		if err != nil {
			return nil, fmt.Errorf("parse create-plan generated_at %s: %w", path, err)
		}
		if now.UTC().Sub(generatedAt.UTC()) < staleCreatePlanAfter {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("create-plans", entry.Name()))
		out = append(out, operationalCandidate{Kind: "create-plan", Status: "stale_unadmitted", OriginalPath: rel})
	}
	return out, nil
}

// archivedWorkIDs lists work_ids whose WorkEnvelope already moved into the
// operational archive — their leftover leases are stale by definition.
func archivedWorkIDs(homePath string) (map[string]bool, error) {
	entries, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind != "work" || !strings.HasPrefix(entry.OriginalPath, "work/work/") || !strings.HasSuffix(entry.OriginalPath, ".json") {
			continue
		}
		out[strings.TrimSuffix(strings.TrimPrefix(entry.OriginalPath, "work/work/"), ".json")] = true
	}
	return out, nil
}

// OperationalHygieneReport is the swept-state doctor view: terminal work must
// not sit hot in work/work, and work/leases must only hold leases for live
// work. Both lists reuse the sweep's own candidate predicates, so the doctor
// and `home archive-history` can never disagree about what counts as dirty.
type OperationalHygieneReport struct {
	Home            string
	TerminalHotWork []string // hot work/work envelopes in a terminal state
	StaleLeases     []string // leases without a live (non-terminal, hot) work item
	OK              bool
}

// OperationalHygiene inspects the hot work surfaces without mutating anything.
// It deliberately does not require a healthy home: it feeds Doctor, and the
// archive sweep that remedies a dirty home must still be allowed to run.
func OperationalHygiene(homePath string) (OperationalHygieneReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return OperationalHygieneReport{}, err
	}
	report := OperationalHygieneReport{Home: homePath}
	workCandidates, err := terminalWorkCandidates(homePath)
	if err != nil {
		return report, err
	}
	for _, cand := range workCandidates {
		report.TerminalHotWork = append(report.TerminalHotWork, cand.OriginalPath)
	}
	leaseCandidates, err := staleLeaseCandidates(homePath)
	if err != nil {
		return report, err
	}
	for _, cand := range leaseCandidates {
		report.StaleLeases = append(report.StaleLeases, cand.OriginalPath)
	}
	report.OK = len(report.TerminalHotWork) == 0 && len(report.StaleLeases) == 0
	return report, nil
}

// hygieneChecks folds OperationalHygiene into doctor CheckItems. Hygiene is
// reported, not fatal: Report.OK must keep gating on manifest+dirs only,
// because the archive sweep that cleans a dirty home refuses to run on an
// unhealthy home — gating OK on hygiene would deadlock the remedy. A scan
// error reads as not-OK (dirty until proven swept); Doctor has no error path.
func hygieneChecks(homePath string) ([]CheckItem, bool) {
	hygiene, err := OperationalHygiene(homePath)
	if err != nil {
		return []CheckItem{{Name: "hygiene:scan", OK: false}}, false
	}
	return []CheckItem{
		{Name: "hygiene:no-terminal-work-hot", OK: len(hygiene.TerminalHotWork) == 0},
		{Name: "hygiene:no-stale-leases", OK: len(hygiene.StaleLeases) == 0},
	}, hygiene.OK
}

func terminalReviewCandidates(homePath string) ([]operationalCandidate, error) {
	root := filepath.Join(homePath, "reviews")
	var out []operationalCandidate
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" || !operationalCoreReviewJSON(path) {
			return nil
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		status, err := jsonFirstStringField(path, "status", "verdict", "result")
		if err != nil {
			return err
		}
		operatorPending, err := operatorPendingReviewDecision(path)
		if err != nil {
			return err
		}
		if operatorPending && !hasOperatorDispositionSidecar(path) {
			return nil
		}
		derivedStatus, derivedTerminal, err := derivedTerminalReviewStatus(homePath, path)
		if err != nil {
			return err
		}
		if derivedTerminal {
			status = firstNonEmptyString(status, derivedStatus)
		}
		if terminalReviewStatus(status) || derivedTerminal || hasTerminalReviewSidecar(path) {
			status = firstNonEmptyString(status, "recorded")
			out = append(out, operationalCandidate{Kind: "review", Status: status, OriginalPath: rel})
			siblings, err := operationalReviewSiblingPaths(homePath, path)
			if err != nil {
				return err
			}
			for _, sibling := range siblings {
				out = append(out, operationalCandidate{Kind: "review", Status: status, OriginalPath: sibling})
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

func terminalComparisonCandidates(homePath string) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "inbox", "comparisons")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := jsonStringField(path, "state")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(state) != "judged" {
			continue
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return nil, err
		}
		out = append(out, operationalCandidate{
			Kind:         "comparison",
			Status:       state,
			OriginalPath: filepath.ToSlash(rel),
		})
	}
	return out, nil
}

func derivedTerminalReviewStatus(homePath, path string) (string, bool, error) {
	var payload map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false, err
	}
	if jsonMapString(payload, "schema_version") != "compound.panel_verdict.v0.1" {
		return "", false, nil
	}
	target, ok := payload["target"].(map[string]any)
	if !ok {
		return "", false, nil
	}
	switch jsonMapString(target, "kind") {
	case "comparison":
		ref := jsonMapString(target, "ref")
		if ref == "" {
			return "", false, nil
		}
		judged, err := comparisonCandidateTerminal(homePath, ref)
		if err != nil {
			return "", false, err
		}
		if judged {
			return "applied", true, nil
		}
	case "memory_candidate":
		ref := jsonMapString(target, "ref")
		if ref == "" {
			return "", false, nil
		}
		terminal, err := candidateTerminal(homePath, candidates.Memory, ref)
		if err != nil {
			return "", false, err
		}
		if terminal {
			return "applied", true, nil
		}
	case "capability_observation":
		ref := jsonMapString(target, "ref")
		if ref == "" {
			return "", false, nil
		}
		terminal, err := candidateTerminal(homePath, candidates.CapabilityObs, ref)
		if err != nil {
			return "", false, err
		}
		if terminal {
			return "applied", true, nil
		}
	}
	return "", false, nil
}

func comparisonCandidateTerminal(homePath, ref string) (bool, error) {
	rel := filepath.ToSlash(filepath.Join("inbox", "comparisons", ref+".json"))
	path := filepath.Join(homePath, filepath.FromSlash(rel))
	if state, err := jsonStringField(path, "state"); err == nil {
		return strings.TrimSpace(state) == "judged", nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	entries, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Kind == "comparison" && entry.OriginalPath == rel && entry.Status == "judged" {
			return true, nil
		}
	}
	return false, nil
}

func candidateTerminal(homePath string, kind candidates.Kind, ref string) (bool, error) {
	dir := candidates.InboxDir(kind)
	if dir == "" {
		return false, nil
	}
	path := filepath.Join(homePath, dir, ref+".json")
	status, err := jsonFirstStringField(path, "state", "verdict", "status", "result")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	switch strings.TrimSpace(status) {
	case "park", "parked", "promote", "promoted", "reject", "rejected", "no_promote", "operationalize", "operationalized":
		return true, nil
	default:
		return false, nil
	}
}

func archiveOperationalFile(homePath string, cand operationalCandidate, now time.Time, dryRun bool) (OperationalArchiveEntry, error) {
	rel := filepath.ToSlash(cand.OriginalPath)
	if err := validateArchivePath(rel); err != nil {
		return OperationalArchiveEntry{}, err
	}
	source := filepath.Join(homePath, filepath.FromSlash(rel))
	info, err := os.Stat(source)
	if err != nil {
		return OperationalArchiveEntry{}, err
	}
	if info.IsDir() {
		return OperationalArchiveEntry{}, fmt.Errorf("operational archive source must be file: %s", rel)
	}
	hash, err := fileSHA256(source)
	if err != nil {
		return OperationalArchiveEntry{}, err
	}
	archiveRel := filepath.ToSlash(filepath.Join(OperationalArchiveFilesRel, rel))
	target := filepath.Join(homePath, filepath.FromSlash(archiveRel))
	entry := OperationalArchiveEntry{
		SchemaVersion: OperationalArchiveSchemaVersion,
		ID:            operationalArchiveID(rel),
		Operation:     "archived",
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Kind:          cand.Kind,
		Status:        cand.Status,
		OriginalPath:  rel,
		ArchivePath:   archiveRel,
		Size:          info.Size(),
		SHA256:        hash,
	}
	if dryRun {
		return entry, nil
	}
	if _, err := os.Stat(target); err == nil {
		return OperationalArchiveEntry{}, fmt.Errorf("operational archive target already exists: %s", archiveRel)
	} else if !os.IsNotExist(err) {
		return OperationalArchiveEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return OperationalArchiveEntry{}, err
	}
	if err := os.Rename(source, target); err != nil {
		return OperationalArchiveEntry{}, err
	}
	return entry, nil
}

func restoreOperationalFile(homePath string, entry OperationalArchiveEntry, now time.Time, dryRun bool) (OperationalArchiveEntry, error) {
	source := filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath))
	target := filepath.Join(homePath, filepath.FromSlash(entry.OriginalPath))
	if !isUnderHome(homePath, source) || !isUnderHome(homePath, target) {
		return OperationalArchiveEntry{}, fmt.Errorf("operational archive restore path escapes home: %s", entry.ID)
	}
	if _, err := os.Stat(source); err != nil {
		return OperationalArchiveEntry{}, err
	}
	restored := entry
	restored.Operation = "restored"
	restored.CreatedAt = now.UTC().Format(time.RFC3339)
	if dryRun {
		return restored, nil
	}
	if _, err := os.Stat(target); err == nil {
		return OperationalArchiveEntry{}, fmt.Errorf("operational archive restore target already exists: %s", entry.OriginalPath)
	} else if !os.IsNotExist(err) {
		return OperationalArchiveEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return OperationalArchiveEntry{}, err
	}
	if err := os.Rename(source, target); err != nil {
		return OperationalArchiveEntry{}, err
	}
	return restored, nil
}

func selectOperationalArchiveEntries(entries []OperationalArchiveEntry, opts OperationalRestoreOptions) []OperationalArchiveEntry {
	if opts.All {
		return append([]OperationalArchiveEntry(nil), entries...)
	}
	wanted := map[string]bool{}
	for _, raw := range opts.IDs {
		for _, id := range strings.Split(raw, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				wanted[id] = true
			}
		}
	}
	var out []OperationalArchiveEntry
	for _, entry := range entries {
		if wanted[entry.ID] {
			out = append(out, entry)
		}
	}
	return out
}

func appendOperationalArchiveEntries(homePath string, entries []OperationalArchiveEntry) error {
	path := OperationalArchiveIndexPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func writeHomeArchive(homePath, targetPath, kind string, excluded []string, now time.Time, include func(string, fs.FileInfo) bool) (ArchiveReport, error) {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ArchiveReport{}, err
	}
	f, err := os.Create(targetPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	entries := []ArchiveEntry{}
	var bytesWritten int64
	err = filepath.WalkDir(homePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == homePath {
			return nil
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !include(rel, info) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return writeDirEntry(tw, rel)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		entry, err := writeFileEntry(tw, path, rel, info)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		bytesWritten += entry.Size
		return nil
	})
	if err != nil {
		return ArchiveReport{}, err
	}
	archiveManifest := ArchiveManifest{
		SchemaVersion:     ArchiveSchemaVersion,
		Kind:              kind,
		CreatedAt:         now.UTC().Format(time.RFC3339),
		SourceHome:        homePath,
		HomeSchemaVersion: SchemaVersion,
		ExcludedRoots:     append([]string(nil), excluded...),
		Entries:           sortedArchiveEntries(entries),
	}
	if err := writeArchiveManifest(tw, archiveManifest); err != nil {
		return ArchiveReport{}, err
	}
	return ArchiveReport{
		Kind:        kind,
		ArchivePath: targetPath,
		Files:       len(entries),
		Bytes:       bytesWritten,
		Skipped:     archiveManifest.ExcludedRoots,
	}, nil
}

func includeBackupPath(rel string, _ fs.FileInfo) bool {
	top := strings.Split(rel, "/")[0]
	for _, excluded := range backupExcludedRoots {
		if top == excluded {
			return false
		}
	}
	return true
}

func writeFileEntry(tw *tar.Writer, path, rel string, info fs.FileInfo) (ArchiveEntry, error) {
	if err := validateArchivePath(rel); err != nil {
		return ArchiveEntry{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ArchiveEntry{}, err
	}
	defer f.Close()
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return ArchiveEntry{}, err
	}
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return ArchiveEntry{}, err
	}
	sum := sha256.New()
	n, err := io.Copy(tw, io.TeeReader(f, sum))
	if err != nil {
		return ArchiveEntry{}, err
	}
	return ArchiveEntry{Path: rel, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

func writeBytesEntry(tw *tar.Writer, rel string, data []byte, mode int64) (ArchiveEntry, error) {
	if err := validateArchivePath(rel); err != nil {
		return ArchiveEntry{}, err
	}
	hdr := &tar.Header{Name: rel, Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := tw.WriteHeader(hdr); err != nil {
		return ArchiveEntry{}, err
	}
	if _, err := tw.Write(data); err != nil {
		return ArchiveEntry{}, err
	}
	sum := sha256.Sum256(data)
	return ArchiveEntry{Path: rel, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func writeDirEntry(tw *tar.Writer, rel string) error {
	if err := validateArchivePath(rel); err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return nil
	}
	return tw.WriteHeader(&tar.Header{Name: strings.TrimSuffix(rel, "/") + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: time.Unix(0, 0).UTC()})
}

func writeArchiveManifest(tw *tar.Writer, manifest ArchiveManifest) error {
	manifest.Entries = sortedArchiveEntries(manifest.Entries)
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	_, err = writeBytesEntry(tw, ArchiveManifestName, b, 0o644)
	return err
}

func readArchive(archivePath string, restore bool, targetHome string) (ArchiveManifest, map[string]ArchiveEntry, error) {
	archivePath, err := filepath.Abs(expandUser(archivePath))
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return ArchiveManifest{}, nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	actual := map[string]ArchiveEntry{}
	var manifest ArchiveManifest
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ArchiveManifest{}, nil, err
		}
		name := filepath.ToSlash(hdr.Name)
		if err := validateArchivePath(name); err != nil {
			return ArchiveManifest{}, nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if restore {
				if err := os.MkdirAll(filepath.Join(targetHome, filepath.FromSlash(name)), fs.FileMode(hdr.Mode)); err != nil {
					return ArchiveManifest{}, nil, err
				}
			}
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return ArchiveManifest{}, nil, fmt.Errorf("archive contains unsupported entry %s", name)
		}
		if name == ArchiveManifestName {
			data, err := io.ReadAll(tr)
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return ArchiveManifest{}, nil, fmt.Errorf("parse archive manifest: %w", err)
			}
			continue
		}
		sum := sha256.New()
		var n int64
		if restore {
			target := filepath.Join(targetHome, filepath.FromSlash(name))
			if !isUnderHome(targetHome, target) {
				return ArchiveManifest{}, nil, fmt.Errorf("archive entry escapes target: %s", name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return ArchiveManifest{}, nil, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(hdr.Mode))
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			n, err = io.Copy(out, io.TeeReader(tr, sum))
			closeErr := out.Close()
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			if closeErr != nil {
				return ArchiveManifest{}, nil, closeErr
			}
		} else {
			n, err = io.Copy(sum, tr)
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
		}
		actual[name] = ArchiveEntry{Path: name, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	}
	if manifest.SchemaVersion != ArchiveSchemaVersion {
		return ArchiveManifest{}, nil, fmt.Errorf("archive manifest schema_version must be %s, got %q", ArchiveSchemaVersion, manifest.SchemaVersion)
	}
	return manifest, actual, nil
}

func normalizeArchiveInputs(homePath, targetPath, kind string, now time.Time) (string, string, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(targetPath) == "" {
		targetPath = defaultArchivePath(homePath, kind, now)
	}
	targetPath, err = filepath.Abs(expandUser(targetPath))
	if err != nil {
		return "", "", err
	}
	return homePath, targetPath, nil
}

func defaultArchivePath(homePath, kind string, now time.Time) string {
	prefix := "compound-home"
	if kind == ArchiveKindPublicFixture {
		prefix = "compound-home-public-fixture"
	}
	return filepath.Join(homePath, "exports", fmt.Sprintf("%s-%s.tar.gz", prefix, now.UTC().Format("20060102T150405Z")))
}

func requireHealthyHome(homePath string) error {
	r := Doctor(homePath)
	if !r.OK {
		return fmt.Errorf("home doctor failed for %s", homePath)
	}
	return nil
}

func validateArchivePath(name string) error {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid archive path %q", name)
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean != name && !strings.HasSuffix(name, "/") {
		return fmt.Errorf("invalid archive path %q", name)
	}
	for _, part := range strings.Split(strings.TrimSuffix(clean, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid archive path %q", name)
		}
	}
	return nil
}

func terminalWorkStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func terminalReviewStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "ok", "applied", "resolved", "accepted", "recorded", "candidate_recorded":
		return true
	default:
		return false
	}
}

func operationalCoreReviewJSON(path string) bool {
	name := filepath.Base(path)
	for _, suffix := range []string{".apply.json", ".decision.json", ".reviewed.json"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

func hasTerminalReviewSidecar(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	dir := filepath.Dir(path)
	for _, suffix := range []string{".reviewed.json", ".apply.json", ".decision.json"} {
		if _, err := os.Stat(filepath.Join(dir, stem+suffix)); err == nil {
			return true
		}
	}
	return false
}

func operatorPendingReviewDecision(path string) (bool, error) {
	var payload map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, err
	}
	if operatorPendingDecision(jsonMapString(payload, "status")) ||
		operatorPendingDecision(jsonMapString(payload, "verdict")) ||
		operatorPendingDecision(jsonMapString(payload, "result")) {
		return true, nil
	}
	synthesis, ok := payload["synthesis"].(map[string]any)
	if !ok {
		return false, nil
	}
	return operatorPendingDecision(jsonMapString(synthesis, "decision")), nil
}

func operatorPendingDecision(decision string) bool {
	switch strings.TrimSpace(decision) {
	case "needs_human", "needs_trial":
		return true
	default:
		return false
	}
}

func hasOperatorDispositionSidecar(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	sidecarPath := filepath.Join(filepath.Dir(path), stem+".reviewed.json")
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		return false
	}
	var sidecar map[string]any
	if err := json.Unmarshal(data, &sidecar); err != nil {
		return false
	}
	return strings.TrimSpace(jsonMapString(sidecar, "reviewed_by")) != ""
}

func operationalReviewSiblingPaths(homePath, corePath string) ([]string, error) {
	stem := strings.TrimSuffix(filepath.Base(corePath), ".json")
	dir := filepath.Dir(corePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, stem+".") {
			continue
		}
		path := filepath.Join(dir, name)
		if path == corePath {
			continue
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}

func jsonStringField(path, key string) (string, error) {
	return jsonFirstStringField(path, key)
}

func jsonFirstStringField(path string, keys ...string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", nil
}

func jsonMapString(payload map[string]any, key string) string {
	if value, ok := payload[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func operationalArchiveID(originalPath string) string {
	sum := sha256.Sum256([]byte(filepath.ToSlash(originalPath)))
	return "oparch_" + hex.EncodeToString(sum[:])[:12]
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func sortedArchiveEntries(entries []ArchiveEntry) []ArchiveEntry {
	out := append([]ArchiveEntry(nil), entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func dirEmptyOrMissing(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func measureTree(root string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	return files, bytes, err
}

func isUnderHome(homePath, path string) bool {
	homePath = filepath.Clean(homePath)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(homePath, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func expandUser(path string) string {
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
