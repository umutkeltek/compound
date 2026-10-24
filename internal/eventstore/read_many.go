package eventstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"compound/internal/home"
)

// ReadResult is one batch outcome for one requested reference. Exactly one of
// Record and Err is meaningful: Err is the SAME error Store.Read would have
// returned for that reference, and Record is the SAME record Store.Read would
// have returned. Errors stay per reference — one missing or corrupt item never
// erases another reference's result.
type ReadResult struct {
	Record Record
	Err    error
}

// errBatchDrift reports that the event store changed underneath a batch
// observation and the change persisted across the bounded retry. It is an
// explicit failure, never a silently stale success.
var errBatchDrift = errors.New("event store changed during batch resolution")

// ReadMany resolves many exact event references in ONE bounded, request-local
// observation of the event store.
//
// It is the batched form of Store.Read with identical per-reference
// semantics: every distinct input reference is a result key (including
// malformed ones), and each result is byte-for-byte what an independent
// Store.Read of that reference returns — same Record identity, same bytes,
// same diagnostic path, or the same error class and message.
//
// The difference is mechanical, not semantic: instead of one family
// directory enumeration and one sealed-index acquisition PER REFERENCE, the
// batch enumerates each needed family once and acquires each needed sealed
// index once, then resolves every requested id through the SAME point-read
// validation machinery (decode, hash, range and rebuild rules).
//
// ReadMany holds no state beyond the call: nothing is memoized on the Store,
// no verdict is cached, and no file is written. Read and ReadRegular are
// untouched and keep their native corruption, newest-occurrence, tail,
// migration and freshness behavior.
func (s *Store) ReadMany(refs []string) map[string]ReadResult {
	return s.readMany(refs, "")
}

// ReadManyRegular is the fail-closed rooted twin of ReadMany: it applies the
// same rooted/symlink rejection ReadRegular applies, to the same batch shape.
// A batch never routes a rooted read through a weaker non-rooted path.
func (s *Store) ReadManyRegular(refs []string, kind string) map[string]ReadResult {
	return s.readMany(refs, kind)
}

func (s *Store) readMany(refs []string, regularKind string) map[string]ReadResult {
	results := make(map[string]ReadResult, len(refs))
	if len(refs) == 0 {
		// Empty input performs no family access at all.
		return results
	}

	// Parse every reference exactly as Store.Read would. The ORIGINAL input
	// string is the result key; aliases that carry the same canonical id share
	// ONE resolution but keep every original key.
	ids := make([]string, 0, len(refs))
	keysForID := make(map[string][]string, len(refs))
	seenRaw := make(map[string]bool, len(refs))
	for _, raw := range refs {
		if seenRaw[raw] {
			continue
		}
		seenRaw[raw] = true
		ref, err := ParseReference(raw)
		if err != nil {
			results[raw] = ReadResult{Err: err}
			continue
		}
		id := ref.ID()
		if _, known := keysForID[id]; !known {
			ids = append(ids, id)
		}
		keysForID[id] = append(keysForID[id], raw)
	}
	if len(ids) == 0 {
		return results
	}

	stored, errs := s.engine.resolveBatch(ids, regularKind)
	for _, id := range ids {
		var res ReadResult
		if err, failed := errs[id]; failed {
			res.Err = err
		} else if rec, found := stored[id]; found {
			res.Record = Record{
				ID:             id,
				Reference:      Reference{id: id},
				Bytes:          rec.bytes,
				DiagnosticPath: rec.diagnosticPath,
			}
		} else {
			res.Err = fmt.Errorf("event %s was not resolved by the batch reader", id)
		}
		for _, raw := range keysForID[id] {
			results[raw] = res
		}
	}
	return results
}

// resolveBatch resolves canonical ids through one bounded observation of the
// event store, retrying ONCE when the store changes underneath the batch.
//
// A second, persistent change is reported as an explicit drift failure for the
// affected ids rather than as stale bytes presented as current.
func (e *engine) resolveBatch(ids []string, regularKind string) (map[string]storedRecord, map[string]error) {
	var stored map[string]storedRecord
	var errs map[string]error
	var drifted map[string]bool
	for attempt := 0; attempt < 2; attempt++ {
		stored, errs, drifted = e.resolveBatchAttempt(ids, regularKind)
		if len(drifted) == 0 {
			return stored, errs
		}
	}
	// The store kept changing across the bounded retry. Fail closed for the
	// ids we would otherwise have answered with possibly-stale bytes. Ids that
	// already carry a native error (corruption, missing) keep it: an explicit
	// failure is never staler than the fact it reports.
	for id := range stored {
		if !drifted[familyOf(nil, id)] {
			continue
		}
		delete(stored, id)
		errs[id] = fmt.Errorf("event %s: %w", id, errBatchDrift)
	}
	return stored, errs
}

// resolveBatchAttempt performs one full observation: every needed family is
// enumerated once, every needed sealed index is acquired once, and every
// requested id is resolved through the existing point-read rules.
func (e *engine) resolveBatchAttempt(ids []string, regularKind string) (map[string]storedRecord, map[string]error, map[string]bool) {
	stored := make(map[string]storedRecord, len(ids))
	errs := make(map[string]error)
	drifted := make(map[string]bool)

	pending := make(map[string]bool, len(ids))
	byFamily := make(map[string][]string, len(ids))
	for _, id := range ids {
		pending[id] = true
		family := familyOf(nil, id)
		byFamily[family] = append(byFamily[family], id)
	}
	families := make([]string, 0, len(byFamily))
	for family := range byFamily {
		families = append(families, family)
	}
	sort.Strings(families)

	for _, family := range families {
		famIDs := byFamily[family]
		observation, err := e.observeFamily(family)
		if err != nil {
			for _, id := range famIDs {
				if pending[id] {
					errs[id] = err
					delete(pending, id)
				}
			}
			continue
		}
		e.resolveFamily(observation, famIDs, pending, stored, errs, regularKind)
		if e.familyDrifted(observation) {
			drifted[family] = true
		}
	}

	// Everything still pending is absent from the segments. The loose layer is
	// consulted exactly as getByID consults it: only while the migration
	// completion gate is open, and fail-closed once it is closed. The gate is
	// read ONCE for the batch — one observation, not one stat per id.
	looseEnabled := e.legacyFallbackEnabled()
	for _, id := range ids {
		if !pending[id] {
			continue
		}
		if !looseEnabled {
			errs[id] = fmt.Errorf("event %s not found in segments (migration complete): %w", id, os.ErrNotExist)
			delete(pending, id)
			continue
		}
		legacyPath := filepath.Join(e.eventsDir, id+".json")
		var data []byte
		var err error
		if regularKind == "" {
			data, err = os.ReadFile(legacyPath)
		} else {
			data, err = home.ReadRootedRegularFile(e.home, legacyPath, regularKind)
		}
		if err != nil {
			errs[id] = err
			delete(pending, id)
			continue
		}
		stored[id] = storedRecord{id: id, bytes: data, diagnosticPath: legacyPath}
		delete(pending, id)
	}
	return stored, errs, drifted
}

// resolveFamily resolves one family's requested ids against that family's
// segments, newest segment first — the same order, and the same per-segment
// rules, that segmentFrame applies to a single id.
func (e *engine) resolveFamily(observation familyObservation, famIDs []string, pending map[string]bool, stored map[string]storedRecord, errs map[string]error, regularKind string) {
	for index, segment := range observation.segments {
		if index == 0 {
			// Hot (last-written) segment. The per-process memo keeps this a
			// single parse; the batch asks for it once per segment instead of
			// once per id.
			byID, err := e.hotSegmentFrames(segment.path, regularKind)
			if err != nil {
				failPendingFamily(famIDs, pending, errs, err)
				return
			}
			for _, id := range famIDs {
				if !pending[id] {
					continue
				}
				hit, found := byID[id]
				if !found {
					continue
				}
				stored[id] = storedRecord{
					id:             id,
					bytes:          append(append([]byte(nil), hit.Frame.Record...), '\n'),
					diagnosticPath: logicalReferencePrefix + id,
					fromSegment:    true,
				}
				delete(pending, id)
			}
			continue
		}

		// Sealed segment: ONE index acquisition serves every requested id.
		index, err := e.loadOrBuildIndex(observation.family, segment)
		if err != nil {
			failPendingFamily(famIDs, pending, errs, err)
			return
		}
		for _, id := range famIDs {
			if !pending[id] {
				continue
			}
			frame, found, err := e.resolveIndexedFrame(observation.family, segment, &index, id, regularKind)
			if err != nil {
				errs[id] = err
				delete(pending, id)
				continue
			}
			if !found {
				continue
			}
			stored[id] = storedRecord{
				id:             id,
				bytes:          append(append([]byte(nil), frame.Record...), '\n'),
				diagnosticPath: logicalReferencePrefix + id,
				fromSegment:    true,
			}
			delete(pending, id)
		}
	}
}

// hotSegmentFrames returns the hot segment's id→frame map for the batch. It
// mirrors segmentFrame exactly: the memoized non-rooted parse, or the rooted
// parse with its tolerable final torn frame. Within a segment the LAST
// occurrence of an id wins, matching the point reader's reverse scan.
func (e *engine) hotSegmentFrames(path string, regularKind string) (map[string]parsedFrame, error) {
	if regularKind == "" {
		notifyHotSegmentAcquire(path)
		return e.hotFrames(path)
	}
	notifyHotSegmentAcquire(path)
	notifyHotParse(path)
	frames, err := e.readSegment(path, true, regularKind)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]parsedFrame, len(frames))
	for i := len(frames) - 1; i >= 0; i-- {
		if _, seen := byID[frames[i].Frame.ID]; !seen {
			byID[frames[i].Frame.ID] = frames[i]
		}
	}
	return byID, nil
}

// failPendingFamily applies one segment-level failure to every still-unresolved
// id of that family, which is exactly what the point reader does: a segment
// that cannot be read fails every id that reaches it.
func failPendingFamily(famIDs []string, pending map[string]bool, errs map[string]error, err error) {
	for _, id := range famIDs {
		if pending[id] {
			errs[id] = err
			delete(pending, id)
		}
	}
}

// fileToken is the observable identity of one file at observation time.
type fileToken struct {
	path  string
	size  int64
	mtime int64
}

// familyObservation is the request-local identity of one family at the batch
// boundary: which segments existed, and what each of them looked like.
type familyObservation struct {
	family   string
	segments []segmentFile
	tokens   []fileToken
}

// observeFamily captures the family's segment inventory and each segment's
// stat identity. It is the batch's recheck anchor: if any of it moves, the
// observation is no longer current.
func (e *engine) observeFamily(family string) (familyObservation, error) {
	segments, err := e.familySegments(family)
	if err != nil {
		return familyObservation{}, err
	}
	observation := familyObservation{family: family, segments: segments}
	for _, segment := range segments {
		token, err := statToken(segment.path)
		if err != nil {
			return familyObservation{}, err
		}
		observation.tokens = append(observation.tokens, token)
	}
	return observation, nil
}

// familyDrifted reports whether the family changed since the batch boundary:
// a new/removed/reordered segment, or a segment whose bytes moved.
func (e *engine) familyDrifted(observation familyObservation) bool {
	segments, err := e.familySegments(observation.family)
	if err != nil {
		return true
	}
	if len(segments) != len(observation.segments) {
		return true
	}
	for index, segment := range segments {
		if segment.path != observation.segments[index].path {
			return true
		}
		token, err := statToken(segment.path)
		if err != nil {
			return true
		}
		if token != observation.tokens[index] {
			return true
		}
	}
	return false
}

func statToken(path string) (fileToken, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileToken{}, err
	}
	return fileToken{path: path, size: info.Size(), mtime: info.ModTime().UnixNano()}, nil
}

// hotSegmentObserver is a test-only seam fired once per hot-segment
// acquisition — the per-id hot-path cost the batch collapses to one
// acquisition per segment. It is nil in production (counter-seam precedent:
// segmentParseObserver in hotcache.go).
var hotSegmentObserver func(path string)

func notifyHotSegmentAcquire(path string) {
	if hotSegmentObserver != nil {
		hotSegmentObserver(path)
	}
}
