package eventstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// It is the batched form of Store.Read: every distinct input reference is a
// result key (including malformed ones), and each result is what an
// independent Store.Read of that reference returns — same Record identity,
// same bytes, same diagnostic path, or the same error class and message. The
// one deliberate difference is an EXPLICIT drift failure: when the store keeps
// moving across the bounded retry, every requested id of the drifted family
// reports errBatchDrift instead of an answer that might be stale.
//
// The difference from per-reference reads is mechanical, not semantic: the
// batch resolves each needed family's segment inventory ONCE and acquires each
// consulted sealed index ONCE, then resolves every requested id through the
// SAME point-read validation machinery (decode, hash, range and rebuild rules)
// the point reader uses.
//
// ReadMany holds no state beyond the call: no verdict is cached, no new
// persistence is added, and no request-local fact outlives the call. It does
// NOT write a file of its own; the reused owner machinery it calls (index
// rebuild/verification) writes exactly what the point reader writes for the
// same segment, because it IS the point reader's machinery. Read and
// ReadRegular are untouched and keep their native corruption, rooted
// rejection, newest-occurrence, tail, migration and freshness behavior.
func (s *Store) ReadMany(refs []string) map[string]ReadResult {
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

	stored, errs := s.engine.resolveBatch(ids)
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
// A second, persistent change is reported as an explicit drift failure for
// EVERY requested id of the drifted family — including ids that were absent
// or already carrying an error on the first attempt. A stale "not found" or a
// stale corruption verdict is exactly the answer the drift exists to prevent.
func (e *engine) resolveBatch(ids []string) (map[string]storedRecord, map[string]error) {
	var stored map[string]storedRecord
	var errs map[string]error
	var drifted map[string]bool
	for attempt := 0; attempt < 2; attempt++ {
		stored, errs, drifted = e.resolveBatchAttempt(ids)
		if len(drifted) == 0 {
			return stored, errs
		}
	}
	// The store kept changing across the bounded retry. Fail closed for every
	// id of every drifted family: we can no longer vouch for any answer we
	// would give about that family.
	byFamily := map[string][]string{}
	for _, id := range ids {
		family := familyOf(nil, id)
		byFamily[family] = append(byFamily[family], id)
	}
	for family := range drifted {
		for _, id := range byFamily[family] {
			delete(stored, id)
			errs[id] = fmt.Errorf("event %s: %w", id, errBatchDrift)
		}
	}
	return stored, errs
}

// resolveBatchAttempt performs one full observation: every needed family is
// enumerated once for resolution, every CONSULTED sealed index is acquired
// once, and every requested id is resolved through the existing point-read
// rules. A final recheck of the consulted files decides whether the
// observation is still current.
func (e *engine) resolveBatchAttempt(ids []string) (map[string]storedRecord, map[string]error, map[string]bool) {
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
		// ONE resolution inventory per family, shared by every requested id of
		// that family. The inventory is a name list only: no file is stat'ed
		// here, so an irrelevant older segment that cannot be stat'ed (a
		// dangling symlink with a valid segment name) cannot break a request
		// that resolves in the newest segment.
		inventory, err := e.familySegments(family)
		if err != nil {
			for _, id := range famIDs {
				if pending[id] {
					errs[id] = err
					delete(pending, id)
				}
			}
			continue
		}
		observation := &familyObservation{family: family, inventory: inventory}
		e.resolveFamily(observation, famIDs, pending, stored, errs)
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
		data, err := os.ReadFile(legacyPath)
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
//
// The walk STOPS as soon as every requested id of the family is resolved:
// older segments are then irrelevant to this request and are never acquired.
func (e *engine) resolveFamily(observation *familyObservation, famIDs []string, pending map[string]bool, stored map[string]storedRecord, errs map[string]error) {
	for index, segment := range observation.inventory {
		if !anyPending(famIDs, pending) {
			// Every requested id of this family is resolved. Older segments are
			// irrelevant here: stop before acquiring their indexes.
			return
		}
		// The stat identity is captured only for CONSULTED segments, and only
		// to detect movement during the consultation.
		preToken, preErr := statToken(segment.path)

		if index == 0 {
			// Hot (last-written) segment. The per-process memo keeps this a
			// single parse; the batch asks for it once per segment instead of
			// once per id.
			byID, err := e.hotSegmentFrames(segment.path)
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
			observation.noteSegment(segment.path, preToken, preErr)
			continue
		}

		// Sealed segment: ONE index acquisition serves every requested id.
		idxPath := e.indexPath(observation.family, segment)
		preIndex, preIndexErr := statToken(idxPath)
		segIndex, rebuilt, err := e.loadOrBuildIndexTracked(observation.family, segment)
		if err != nil {
			failPendingFamily(famIDs, pending, errs, err)
			return
		}
		for _, id := range famIDs {
			if !pending[id] {
				continue
			}
			frame, found, idRebuilt, err := e.resolveIndexedFrameTracked(observation.family, segment, &segIndex, id, "")
			if idRebuilt {
				rebuilt = true
			}
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
		observation.noteSegment(segment.path, preToken, preErr)
		observation.noteIndex(idxPath, preIndex, preIndexErr, rebuilt)
	}
}

// anyPending reports whether any of the family's requested ids is still
// unresolved.
func anyPending(famIDs []string, pending map[string]bool) bool {
	for _, id := range famIDs {
		if pending[id] {
			return true
		}
	}
	return false
}

// hotSegmentFrames returns the hot segment's id→frame map for the batch. It
// mirrors segmentFrame exactly: the memoized non-rooted parse. Within a
// segment the LAST occurrence of an id wins, matching the point reader's
// reverse scan.
func (e *engine) hotSegmentFrames(path string) (map[string]parsedFrame, error) {
	notifyHotSegmentAcquire(path)
	return e.hotFrames(path)
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
// boundary: the segment inventory the batch resolved against, plus the stat
// identity of the segments and indexes the batch ACTUALLY consulted.
type familyObservation struct {
	family    string
	inventory []segmentFile
	consulted []consultedFile
	moved     bool
}

// consultedFile is the observed identity of one file the batch consulted. Only
// consulted files are stat'ed and rechecked: an irrelevant older segment is
// never stat'ed, so it can never fail the batch.
type consultedFile struct {
	path  string
	token fileToken
}

// noteSegment records the identity of a consulted segment AFTER the
// consultation. A segment that moved while we read it marks the family as
// moved: the bytes we served may no longer be current.
func (observation *familyObservation) noteSegment(path string, pre fileToken, preErr error) {
	post, err := statToken(path)
	if err != nil {
		observation.moved = true
		return
	}
	if preErr == nil && pre != post {
		observation.moved = true
	}
	observation.consulted = append(observation.consulted, consultedFile{path: path, token: post})
}

// noteIndex records the identity of a consulted sidecar index AFTER the
// consultation. An index this batch rebuilt is the OWNER's legitimate write —
// the same write the point reader performs for the same segment — so it is
// accepted as the current identity. An index that was REPLACED while we
// consulted it (and that we did not rebuild ourselves) marks the family as
// moved.
func (observation *familyObservation) noteIndex(path string, pre fileToken, preErr error, rebuilt bool) {
	post, err := statToken(path)
	if err != nil {
		observation.moved = true
		return
	}
	if preErr == nil && !rebuilt && pre != post {
		observation.moved = true
	}
	observation.consulted = append(observation.consulted, consultedFile{path: path, token: post})
}

// familyDrifted reports whether the family's observation is no longer current:
// the segment inventory changed, a consulted segment or consulted index moved,
// or a consulted file moved while it was being read.
func (e *engine) familyDrifted(observation *familyObservation) bool {
	if observation.moved {
		return true
	}
	// The final recheck: the family's inventory must still be the same set of
	// segments (a publication, append-driven rollover or removal changes it).
	segments, err := e.familySegments(observation.family)
	if err != nil {
		return true
	}
	if len(segments) != len(observation.inventory) {
		return true
	}
	for index, segment := range segments {
		if segment.path != observation.inventory[index].path {
			return true
		}
	}
	// Only CONSULTED files are rechecked, using the same stat trust rule the
	// owner uses elsewhere (size + mtime).
	for _, consulted := range observation.consulted {
		token, err := statToken(consulted.path)
		if err != nil {
			return true
		}
		if token != consulted.token {
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
