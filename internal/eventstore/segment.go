package eventstore

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	segmentsDirName       = "segments"
	maxBytes        int64 = 64 << 20
)

type engineConfig struct {
	clock           func() time.Time
	probe           fsProbeFunc
	logf            func(string, ...any)
	segmentMaxBytes int64
}

type engine struct {
	home            string
	eventsDir       string
	segRoot         string
	indexRoot       string
	segmentMaxBytes int64
	clock           func() time.Time
	logf            func(string, ...any)
	localFS         bool
	fsType          string
	warnOnce        sync.Once
	sourceHashMu    sync.Mutex
	sourceHashes    map[string]cachedSourceHash
}

type putMode uint8

const (
	putIdempotent putMode = iota
	putExclusive
	putIfAbsent
	putAtomic
)

type putOutcome struct {
	created        bool
	diagnosticPath string
}

type idPresence uint8

const (
	idAbsent idPresence = iota
	idAlreadyPresent
	idConflict
)

var ErrIDConflict = errors.New("event id already exists with different content")

func newEngine(homePath string, config engineConfig) (*engine, error) {
	clock := config.clock
	if clock == nil {
		clock = time.Now
	}
	probe := config.probe
	if probe == nil {
		probe = checkLocalFS
	}
	logf := config.logf
	if logf == nil {
		logf = log.Printf
	}
	limit := config.segmentMaxBytes
	if limit <= 0 {
		limit = maxBytes
	}
	// Prefer the existing events directory when it is a distinct mount point;
	// otherwise walk to the nearest existing ancestor without creating state.
	probePath, err := nearestExistingPath(filepath.Join(homePath, eventsDirName))
	if err != nil {
		return nil, err
	}
	fsType, local, err := probe(probePath)
	if err != nil {
		return nil, fmt.Errorf("probe event store filesystem: %w", err)
	}
	if strings.TrimSpace(fsType) == "" {
		return nil, fmt.Errorf("probe event store filesystem: empty filesystem type")
	}
	return &engine{
		home:            homePath,
		eventsDir:       filepath.Join(homePath, eventsDirName),
		segRoot:         filepath.Join(homePath, eventsDirName, segmentsDirName),
		indexRoot:       filepath.Join(homePath, eventsDirName, indexDirName),
		segmentMaxBytes: limit,
		clock:           clock,
		logf:            logf,
		localFS:         local,
		fsType:          fsType,
	}, nil
}

func nearestExistingPath(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		if _, err := os.Stat(current); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		current = parent
	}
}

func ingestionDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

func segmentPath(segRoot, family, day string, seq int) string {
	name := day + ".jsonl"
	if seq > 0 {
		name = day + "." + strconv.Itoa(seq) + ".jsonl"
	}
	return filepath.Join(segRoot, family, name)
}

type segmentFile struct {
	path string
	day  string
	seq  int
}

func (e *engine) familySegments(family string) ([]segmentFile, error) {
	dir := filepath.Join(e.segRoot, family)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	segments := make([]segmentFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		day, seq, ok := parseSegmentName(entry.Name())
		if !ok {
			continue
		}
		segments = append(segments, segmentFile{path: filepath.Join(dir, entry.Name()), day: day, seq: seq})
	}
	sort.Slice(segments, func(i, j int) bool {
		if segments[i].day == segments[j].day {
			return segments[i].seq > segments[j].seq
		}
		return segments[i].day > segments[j].day
	})
	return segments, nil
}

func parseSegmentName(name string) (string, int, bool) {
	if !strings.HasSuffix(name, ".jsonl") {
		return "", 0, false
	}
	stem := strings.TrimSuffix(name, ".jsonl")
	day := stem
	seq := 0
	if dot := strings.LastIndexByte(stem, '.'); dot >= 0 {
		day = stem[:dot]
		parsed, err := strconv.Atoi(stem[dot+1:])
		if err != nil || parsed <= 0 {
			return "", 0, false
		}
		seq = parsed
	}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return "", 0, false
	}
	return day, seq, true
}

func (e *engine) lastWritten(family string) (segmentFile, bool, error) {
	segments, err := e.familySegments(family)
	if err != nil {
		return segmentFile{}, false, err
	}
	if len(segments) == 0 {
		return segmentFile{}, false, nil
	}
	return segments[0], true, nil
}

func (e *engine) appendTarget(family, today string) (string, bool, error) {
	dir := filepath.Join(e.segRoot, family)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	segments, err := e.familySegments(family)
	if err != nil {
		return "", false, err
	}
	seq := 0
	foundToday := false
	for _, segment := range segments {
		if segment.day == today {
			seq = segment.seq
			foundToday = true
			break
		}
	}
	if foundToday {
		current := segmentPath(e.segRoot, family, today, seq)
		info, err := os.Stat(current)
		if err != nil {
			return "", false, err
		}
		if info.Size() >= e.segmentMaxBytes {
			seq++
			foundToday = false
		}
	}
	path := segmentPath(e.segRoot, family, today, seq)
	if foundToday {
		return path, false, nil
	}
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	return path, true, nil
}

func (e *engine) put(id string, record []byte, mode putMode, opts AtomicOptions) (outcome putOutcome, retErr error) {
	frame, _, err := buildFrame(id, record)
	if err != nil {
		return putOutcome{}, err
	}
	line, err := encodeFrame(frame)
	if err != nil {
		return putOutcome{}, err
	}
	if !e.localFS {
		if os.Getenv(requireLocalEnv) == "1" {
			return putOutcome{}, nonLocalFSError(e.fsType)
		}
		e.warnOnce.Do(func() {
			e.logf("eventstore: WARNING non-local filesystem %s; degrading to legacy loose writes", e.fsType)
		})
		return e.legacyPut(frame, record, mode, opts)
	}

	lock, err := acquireAppendLock(e.segRoot)
	if err != nil {
		return putOutcome{}, err
	}
	defer func() {
		if releaseErr := lock.release(); retErr == nil && releaseErr != nil {
			retErr = releaseErr
		}
	}()

	last, exists, err := e.lastWritten(frame.Family)
	if err != nil {
		return putOutcome{}, err
	}
	if exists {
		if err := e.repairTail(last.path); err != nil {
			return putOutcome{}, err
		}
	}
	today := ingestionDay(e.clock())
	presence, existingPath, err := e.checkID(frame.Family, id, frame.SHA256)
	if err != nil {
		return putOutcome{}, err
	}
	if presence != idAbsent {
		switch mode {
		case putExclusive:
			return putOutcome{}, fmt.Errorf("%s: %w", id, os.ErrExist)
		case putIfAbsent:
			return putOutcome{created: false, diagnosticPath: existingPath}, nil
		default:
			if presence == idConflict {
				return putOutcome{}, fmt.Errorf("%w: %s", ErrIDConflict, id)
			}
			return putOutcome{created: false, diagnosticPath: existingPath}, nil
		}
	}

	if opts.Write != nil {
		if err := e.exerciseAtomicWriter(opts.Write, line); err != nil {
			return putOutcome{}, err
		}
	}
	if opts.BeforePublish != nil {
		if err := opts.BeforePublish(); err != nil {
			return putOutcome{}, err
		}
	}
	if _, _, err := e.appendFrameAt(frame.Family, today, line); err != nil {
		return putOutcome{}, err
	}
	outcome = putOutcome{created: true, diagnosticPath: logicalReferencePrefix + id}
	if opts.AfterPublish != nil {
		if err := opts.AfterPublish(); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

// appendFrameAt appends one encoded frame line to the family's current segment.
// The caller must already hold the append lock and must have repaired the target
// family's tail. It returns the segment path relative to the events dir and the
// byte offset the frame begins at, so the migration seam can record a precise
// {segment, offset} manifest coordinate for every folded record.
func (e *engine) appendFrameAt(family, today string, line []byte) (relPath string, offset int64, err error) {
	path, created, err := e.appendTarget(family, today)
	if err != nil {
		return "", 0, err
	}
	flags := os.O_WRONLY | os.O_APPEND
	if created {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return "", 0, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if created {
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return "", 0, err
		}
	}
	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return "", 0, err
	}
	if err := writeFull(file, line); err != nil {
		truncateErr := file.Truncate(start)
		syncErr := file.Sync()
		if truncateErr != nil {
			return "", 0, fmt.Errorf("append event frame: %v; rollback truncate: %w", err, truncateErr)
		}
		if syncErr != nil {
			return "", 0, fmt.Errorf("append event frame: %v; rollback sync: %w", err, syncErr)
		}
		return "", 0, err
	}
	if err := file.Sync(); err != nil {
		return "", 0, err
	}
	if err := file.Close(); err != nil {
		return "", 0, err
	}
	closed = true
	rel, relErr := filepath.Rel(e.eventsDir, path)
	if relErr != nil {
		rel = path
	}
	return rel, start, nil
}

func (e *engine) exerciseAtomicWriter(writer func(*os.File, []byte) error, line []byte) error {
	tmp, err := os.CreateTemp(e.segRoot, ".eventstore-atomic-write-*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(path)
	}()
	if err := writer(tmp, line); err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	written, err := io.ReadAll(tmp)
	if err != nil {
		return err
	}
	if string(written) != string(line) {
		return io.ErrShortWrite
	}
	return nil
}

func (e *engine) checkID(family, id, hash string) (idPresence, string, error) {
	// A loose record is authoritative for its id only until the migration
	// completion gate closes. Once completion.json exists the loose layer is no
	// longer consulted on the write path: migrated ids live in segments and any
	// reappearing loose file is a legacy-writer regression for the doctor, not
	// silent authority.
	if e.legacyFallbackEnabled() {
		legacyPath := filepath.Join(e.eventsDir, id+".json")
		if data, err := os.ReadFile(legacyPath); err == nil {
			compact, compactErr := canonical(data)
			if compactErr != nil {
				return idAbsent, "", corruptionAt(legacyPath, data, 1, 0, compactErr)
			}
			if recordHash(compact) == hash {
				return idAlreadyPresent, legacyPath, nil
			}
			return idConflict, legacyPath, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return idAbsent, "", err
		}
	}

	segments, err := e.familySegments(family)
	if err != nil {
		return idAbsent, "", err
	}
	presence := idAbsent
	for index, segment := range segments {
		if index > 0 {
			frame, found, err := e.readIndexedFrame(family, segment, id, "")
			if err != nil {
				return idAbsent, "", err
			}
			if !found {
				continue
			}
			if frame.SHA256 != hash {
				return idConflict, logicalReferencePrefix + id, nil
			}
			presence = idAlreadyPresent
			continue
		}

		frames, err := parseSegment(segment.path, false)
		if err != nil {
			return idAbsent, "", err
		}
		for i := len(frames) - 1; i >= 0; i-- {
			if frames[i].Frame.ID != id {
				continue
			}
			if frames[i].Frame.SHA256 != hash {
				return idConflict, logicalReferencePrefix + id, nil
			}
			presence = idAlreadyPresent
		}
	}
	if presence == idAlreadyPresent {
		return presence, logicalReferencePrefix + id, nil
	}
	return idAbsent, "", nil
}

func (e *engine) legacyPut(frame Frame, record []byte, mode putMode, opts AtomicOptions) (putOutcome, error) {
	if err := os.MkdirAll(e.eventsDir, 0o755); err != nil {
		return putOutcome{}, err
	}
	path := filepath.Join(e.eventsDir, frame.ID+".json")
	if existing, err := os.ReadFile(path); err == nil {
		switch mode {
		case putExclusive:
			return putOutcome{}, fmt.Errorf("%s: %w", frame.ID, os.ErrExist)
		case putIfAbsent:
			return putOutcome{diagnosticPath: path}, nil
		}
		compact, compactErr := canonical(existing)
		if compactErr != nil {
			return putOutcome{}, corruptionAt(path, existing, 1, 0, compactErr)
		}
		if recordHash(compact) == frame.SHA256 {
			return putOutcome{diagnosticPath: path}, nil
		}
		return putOutcome{}, fmt.Errorf("%w: %s", ErrIDConflict, frame.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return putOutcome{}, err
	}

	if mode == putAtomic {
		return e.legacyAtomicPut(frame.ID, path, record, opts)
	}
	if opts.BeforePublish != nil {
		if err := opts.BeforePublish(); err != nil {
			return putOutcome{}, err
		}
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if mode == putExclusive {
		flags = os.O_CREATE | os.O_EXCL | os.O_WRONLY
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return putOutcome{}, err
	}
	if err := writeFull(file, record); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return putOutcome{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return putOutcome{}, err
	}
	if err := file.Close(); err != nil {
		return putOutcome{}, err
	}
	if err := syncDirectory(e.eventsDir); err != nil {
		return putOutcome{}, err
	}
	outcome := putOutcome{created: true, diagnosticPath: path}
	if opts.AfterPublish != nil {
		if err := opts.AfterPublish(); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

func (e *engine) legacyAtomicPut(id, path string, record []byte, opts AtomicOptions) (putOutcome, error) {
	tmp, err := os.CreateTemp(e.eventsDir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return putOutcome{}, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return putOutcome{}, err
	}
	writer := opts.Write
	if writer == nil {
		writer = writeFull
	}
	if err := writer(tmp, record); err != nil {
		return putOutcome{}, err
	}
	if err := tmp.Sync(); err != nil {
		return putOutcome{}, err
	}
	if err := tmp.Close(); err != nil {
		return putOutcome{}, err
	}
	if opts.BeforePublish != nil {
		if err := opts.BeforePublish(); err != nil {
			return putOutcome{}, err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return putOutcome{}, err
	}
	if opts.SyncDirectory {
		if err := syncDirectory(e.eventsDir); err != nil {
			return putOutcome{}, err
		}
	}
	outcome := putOutcome{created: true, diagnosticPath: path}
	if opts.AfterPublish != nil {
		if err := opts.AfterPublish(); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

func (e *engine) preflight() error {
	if !e.localFS && os.Getenv(requireLocalEnv) == "1" {
		return nonLocalFSError(e.fsType)
	}
	dir := e.segRoot
	if !e.localFS {
		dir = e.eventsDir
		e.warnOnce.Do(func() {
			e.logf("eventstore: WARNING non-local filesystem %s; degrading to legacy loose writes", e.fsType)
		})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if e.localFS {
		lock, err := acquireAppendLock(e.segRoot)
		if err != nil {
			return err
		}
		defer lock.release()
	}
	probe, err := os.CreateTemp(dir, ".eventstore-preflight-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	if err := probe.Sync(); err != nil {
		_ = probe.Close()
		_ = os.Remove(name)
		return err
	}
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
