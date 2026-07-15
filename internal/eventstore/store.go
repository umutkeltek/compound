// Package eventstore is the sole physical owner of COMPOUND_HOME/events record
// layout. The current engine intentionally remains one loose JSON file per
// event; callers consume logical references and Store operations so a later
// engine swap does not reopen repository-wide path construction.
package eventstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"compound/internal/home"
)

const (
	eventsDirName              = "events"
	workLogName                = "work.jsonl"
	hookEventsStreamName       = "hook-events.ndjson"
	tracesStreamName           = "traces.ndjson"
	retrievalTrafficStreamName = "retrieval-traffic.ndjson"
)

// Store owns event records and the named compatibility streams under one home.
// Open validates configuration only; read-only callers do not create state.
type Store struct {
	home string
}

// Record is one event read through the seam. DiagnosticPath is intentionally
// available only after resolving an actual record; it is not a path builder.
type Record struct {
	ID             string
	Reference      Reference
	Bytes          []byte
	DiagnosticPath string
}

// PutResult reports whether a writer created/published a record. A skipped
// if-absent write returns Created=false and the same logical identity.
type PutResult struct {
	Reference      Reference
	Created        bool
	DiagnosticPath string
}

// AtomicOptions preserves the existing durability and injected-failure seams
// used by work and memory writers without exposing loose object paths.
type AtomicOptions struct {
	SyncDirectory bool
	Write         func(*os.File, []byte) error
	BeforePublish func() error
	AfterPublish  func() error
}

// ScanOptions controls filename filtering before any matching entry is opened.
type ScanOptions struct {
	Prefixes    []string
	RegularOnly bool
	// Cursor is reserved for the engine-independent scan contract. The loose
	// engine returns a complete deterministic scan when it is empty; segmented
	// engines may issue and consume opaque cursors without changing callers.
	Cursor string
}

// Entry is a lazily readable event discovered by Scan.
type Entry struct {
	ID        string
	Name      string
	Reference Reference
	store     *Store
}

func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	return &Store{home: filepath.Clean(homePath)}, nil
}

// Ensure creates the current physical event root for a writer.
func (s *Store) Ensure() error {
	if s == nil || strings.TrimSpace(s.home) == "" {
		return fmt.Errorf("event store requires a home")
	}
	return os.MkdirAll(s.rootPath(), 0o755)
}

// Preflight proves the current loose store can create and remove a file. Probe
// cleanup remains best-effort, matching the governed-act preflight contract.
func (s *Store) Preflight() error {
	if err := s.Ensure(); err != nil {
		return err
	}
	probe, err := os.CreateTemp(s.rootPath(), ".eventstore-preflight-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

func (s *Store) rootPath() string { return filepath.Join(s.home, eventsDirName) }

func (s *Store) recordPath(ref Reference) string {
	return filepath.Join(s.rootPath(), ref.ID()+".json")
}

func (s *Store) resolve(raw string) (Reference, string, error) {
	ref, err := ParseReference(raw)
	if err != nil {
		return Reference{}, "", err
	}
	return ref, s.recordPath(ref), nil
}

// Read resolves a bare, logical, or historical reference through the current
// loose engine.
func (s *Store) Read(raw string) (Record, error) {
	ref, path, err := s.resolve(raw)
	if err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	return Record{ID: ref.ID(), Reference: ref, Bytes: data, DiagnosticPath: path}, nil
}

// ReadRegular is the fail-closed rooted variant used where a symlinked record
// or events ancestor must never qualify as authority evidence.
func (s *Store) ReadRegular(raw, kind string) (Record, error) {
	ref, path, err := s.resolve(raw)
	if err != nil {
		return Record{}, err
	}
	data, err := home.ReadRootedRegularFile(s.home, path, kind)
	if err != nil {
		return Record{}, err
	}
	return Record{ID: ref.ID(), Reference: ref, Bytes: data, DiagnosticPath: path}, nil
}

func (s *Store) Exists(raw string) (bool, error) {
	_, path, err := s.resolve(raw)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func (s *Store) Remove(raw string) error {
	_, path, err := s.resolve(raw)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// PutJSON preserves the existing overwrite mode and caller-selected indent.
func (s *Store) PutJSON(id string, value any, indent string) (PutResult, error) {
	ref, err := ReferenceForID(id)
	if err != nil {
		return PutResult{}, err
	}
	data, err := marshalJSONLine(value, indent)
	if err != nil {
		return PutResult{}, err
	}
	if err := s.Ensure(); err != nil {
		return PutResult{}, err
	}
	path := s.recordPath(ref)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return PutResult{}, err
	}
	return putResult(ref, path, true), nil
}

// PutJSONExclusive preserves O_EXCL publication and removes a partial file on
// write or close failure.
func (s *Store) PutJSONExclusive(id string, value any, indent string) (PutResult, error) {
	ref, err := ReferenceForID(id)
	if err != nil {
		return PutResult{}, err
	}
	data, err := marshalJSONLine(value, indent)
	if err != nil {
		return PutResult{}, err
	}
	if err := s.Ensure(); err != nil {
		return PutResult{}, err
	}
	path := s.recordPath(ref)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return PutResult{}, err
	}
	// Preserve the review-artifact writer's historical single Write call. In
	// particular, a nil-error short write is not retried in this parity slice.
	if err := writeOnce(file, data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return PutResult{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return PutResult{}, err
	}
	return putResult(ref, path, true), nil
}

// PutJSONIfAbsent preserves trace import's current stat-then-write behavior:
// an existing id is a silent skip, not an ErrIDConflict.
func (s *Store) PutJSONIfAbsent(id string, value any, indent string) (PutResult, error) {
	ref, err := ReferenceForID(id)
	if err != nil {
		return PutResult{}, err
	}
	path := s.recordPath(ref)
	if _, err := os.Stat(path); err == nil {
		return putResult(ref, path, false), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return PutResult{}, err
	}
	return s.PutJSON(id, value, indent)
}

// PutJSONAtomic publishes a synced sibling temp by rename. Callers select
// directory sync and optional injected boundaries to preserve their contract.
func (s *Store) PutJSONAtomic(id string, value any, indent string, opts AtomicOptions) (PutResult, error) {
	ref, err := ReferenceForID(id)
	if err != nil {
		return PutResult{}, err
	}
	data, err := marshalJSONLine(value, indent)
	if err != nil {
		return PutResult{}, err
	}
	if err := s.Ensure(); err != nil {
		return PutResult{}, err
	}
	path := s.recordPath(ref)
	tmp, err := os.CreateTemp(s.rootPath(), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return PutResult{}, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return PutResult{}, err
	}
	writer := opts.Write
	if writer == nil {
		writer = writeFull
	}
	if err := writer(tmp, data); err != nil {
		return PutResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		return PutResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return PutResult{}, err
	}
	if opts.BeforePublish != nil {
		if err := opts.BeforePublish(); err != nil {
			return PutResult{}, err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return PutResult{}, err
	}
	if opts.SyncDirectory {
		if err := syncDirectory(s.rootPath()); err != nil {
			return PutResult{}, err
		}
	}
	if opts.AfterPublish != nil {
		if err := opts.AfterPublish(); err != nil {
			return PutResult{}, err
		}
	}
	return putResult(ref, path, true), nil
}

func putResult(ref Reference, path string, created bool) PutResult {
	return PutResult{Reference: ref, Created: created, DiagnosticPath: path}
}

func marshalJSONLine(value any, indent string) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", indent)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeFull(file *os.File, data []byte) error {
	for len(data) > 0 {
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func writeOnce(writer io.Writer, data []byte) error {
	_, err := writer.Write(data)
	return err
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	syncErr := dir.Sync()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return nil
}

// Scan returns matching loose records in deterministic filename order. It
// never opens them; each caller retains its fail-fast or warning policy via
// Entry.Read.
func (s *Store) Scan(opts ScanOptions) ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.rootPath())
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if opts.RegularOnly && !dirEntry.Type().IsRegular() {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !matchesPrefix(id, opts.Prefixes) {
			continue
		}
		ref, err := ReferenceForID(id)
		if err != nil {
			return nil, fmt.Errorf("invalid event scan entry %q: %w", name, err)
		}
		entries = append(entries, Entry{
			ID:        id,
			Name:      name,
			Reference: ref,
			store:     s,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func matchesPrefix(id string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func (e Entry) Read() (Record, error) {
	if e.store == nil {
		return Record{}, fmt.Errorf("event scan entry has no store")
	}
	return e.store.Read(e.ID)
}

func (s *Store) Count(opts ScanOptions) (int, error) {
	entries, err := s.Scan(opts)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// Named compatibility streams remain physically separate from event records.
// Their paths stay private so callers cannot accidentally become a second
// physical owner while the work lifecycle retains its distinct semantics.
func (s *Store) workLogPath() string { return filepath.Join(s.rootPath(), workLogName) }

func (s *Store) workLogLockPath() string { return s.workLogPath() + ".lock" }

func (s *Store) hookEventsPath() string {
	return filepath.Join(s.rootPath(), hookEventsStreamName)
}

func (s *Store) tracesPath() string { return filepath.Join(s.rootPath(), tracesStreamName) }

func (s *Store) retrievalTrafficPath() string {
	return filepath.Join(s.rootPath(), retrievalTrafficStreamName)
}

// WithWorkLogLock serializes authoritative lifecycle readers and writers on
// the stable sidecar lock. Autonomy admission remains an outer concern of the
// work package, preserving admission -> work-log lock ordering.
func (s *Store) WithWorkLogLock(fn func() error) error {
	if fn == nil {
		return fmt.Errorf("work log lock callback required")
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.workLogLockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

// AppendWorkLog performs one caller-supplied JSONL append and syncs the file.
// write is injectable so work's existing short-write and crash falsifiers keep
// observing the exact publication boundary without owning the physical path.
func (s *Store) AppendWorkLog(data []byte, write func(io.Writer, []byte) (int, error)) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	file, err := os.OpenFile(s.workLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if write == nil {
		write = func(w io.Writer, data []byte) (int, error) { return w.Write(data) }
	}
	n, err := write(file, data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	return file.Sync()
}

// WorkLogReplaceOptions preserves work's controlled append failpoints. Prefix
// bytes are copied completely before WriteSuffix writes the new event.
type WorkLogReplaceOptions struct {
	WriteSuffix   func(io.Writer, []byte) (int, error)
	BeforePublish func() error
	AfterPublish  func() error
}

// ReplaceWorkLog atomically publishes prefix+suffix via a synced sibling temp,
// rename, and directory sync. The caller is responsible for holding the work
// log lock and validating prefix before invoking this operation.
func (s *Store) ReplaceWorkLog(prefix, suffix []byte, opts WorkLogReplaceOptions) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	path := s.workLogPath()
	tmp, err := os.CreateTemp(s.rootPath(), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := writeFull(tmp, prefix); err != nil {
		return err
	}
	writeSuffix := opts.WriteSuffix
	if writeSuffix == nil {
		writeSuffix = func(w io.Writer, data []byte) (int, error) { return w.Write(data) }
	}
	n, err := writeSuffix(tmp, suffix)
	if err == nil && n != len(suffix) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if opts.BeforePublish != nil {
		if err := opts.BeforePublish(); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := syncDirectory(s.rootPath()); err != nil {
		return err
	}
	if opts.AfterPublish != nil {
		return opts.AfterPublish()
	}
	return nil
}

func (s *Store) ReadWorkLog() ([]byte, error) { return os.ReadFile(s.workLogPath()) }

func (s *Store) ReadWorkLogRegular(kind string) ([]byte, error) {
	return home.ReadRootedRegularFile(s.home, s.workLogPath(), kind)
}

// OpenWorkLog returns an O_RDONLY handle for scanners. It never creates the
// stream and never takes the authoritative writer lock.
func (s *Store) OpenWorkLog() (*os.File, error) { return os.Open(s.workLogPath()) }

func (s *Store) WorkLogSize() (int64, error) {
	info, err := os.Stat(s.workLogPath())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Store) ReadWorkLogAt(off, length int64) ([]byte, error) {
	if off < 0 || length < 0 {
		return nil, fmt.Errorf("work log read range must be non-negative")
	}
	if length == 0 {
		return nil, nil
	}
	file, err := s.OpenWorkLog()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buf := make([]byte, length)
	n, err := file.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

func (s *Store) OpenHookEvents() (*os.File, error) { return os.Open(s.hookEventsPath()) }

func (s *Store) HookEventsExists() (bool, error) {
	return regularFileExists(s.hookEventsPath())
}

func (s *Store) OpenTraces() (*os.File, error) { return os.Open(s.tracesPath()) }

func (s *Store) OpenRetrievalTraffic() (*os.File, error) {
	return os.Open(s.retrievalTrafficPath())
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}

func (s *Store) AppendHookEvent(value any) error {
	return s.appendJSONL(s.hookEventsPath(), value)
}

func (s *Store) AppendTrace(value any) error { return s.appendJSONL(s.tracesPath(), value) }

func (s *Store) AppendRetrievalTraffic(value any) error {
	return s.appendJSONL(s.retrievalTrafficPath(), value)
}

func (s *Store) appendJSONL(path string, value any) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}
