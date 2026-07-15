package eventstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/home"
)

type storedRecord struct {
	id             string
	bytes          []byte
	diagnosticPath string
	fromSegment    bool
}

// segmentHit is a resolved segment record: the authoritative frame plus the
// physical coordinate the migration seam records in a manifest row. offset is
// only populated when the record is resolved by a full segment parse (hot
// segments and presence scans); an index-resolved sealed hit leaves it zero
// because the completion gate keys record identity on canonical hash, not offset.
type segmentHit struct {
	frame   Frame
	relPath string
	offset  int64
}

// segmentFrame resolves an id against segment truth ONLY (never the loose layer
// or the archive). A corrupt segment surfaces its error here so every read path
// fails closed rather than silently falling back to another representation.
func (e *engine) segmentFrame(id, regularKind string) (segmentHit, bool, error) {
	family := familyOf(nil, id)
	segments, err := e.familySegments(family)
	if err != nil {
		return segmentHit{}, false, err
	}
	for index, segment := range segments {
		rel, relErr := filepath.Rel(e.eventsDir, segment.path)
		if relErr != nil {
			rel = segment.path
		}
		if index > 0 {
			frame, found, err := e.readIndexedFrame(family, segment, id, regularKind)
			if err != nil {
				return segmentHit{}, false, err
			}
			if found {
				return segmentHit{frame: frame, relPath: rel}, true, nil
			}
			continue
		}

		// Hot (last-written) segment: resolve through the per-process memo so a
		// projection's thousands of point reads cost one parse, not one parse
		// each (the post-migration quadratic — see hotcache.go). regularKind
		// reads keep the direct rooted-validation parse.
		if regularKind == "" {
			byID, err := e.hotFrames(segment.path)
			if err != nil {
				return segmentHit{}, false, err
			}
			if hit, ok := byID[id]; ok {
				return segmentHit{frame: hit.Frame, relPath: rel, offset: hit.Offset}, true, nil
			}
			continue
		}
		frames, err := e.readSegment(segment.path, true, regularKind)
		if err != nil {
			return segmentHit{}, false, err
		}
		for i := len(frames) - 1; i >= 0; i-- {
			if frames[i].Frame.ID == id {
				return segmentHit{frame: frames[i].Frame, relPath: rel, offset: frames[i].Offset}, true, nil
			}
		}
	}
	return segmentHit{}, false, nil
}

func (e *engine) getByID(id, regularKind string) (storedRecord, error) {
	hit, found, err := e.segmentFrame(id, regularKind)
	if err != nil {
		return storedRecord{}, err
	}
	if found {
		return storedRecord{
			id:             id,
			bytes:          append(append([]byte(nil), hit.frame.Record...), '\n'),
			diagnosticPath: logicalReferencePrefix + id,
			fromSegment:    true,
		}, nil
	}
	// After the migration completion gate closes, the loose layer is dropped from
	// the hot read path: a migrated id resolves in a segment above, and anything
	// not in a segment is genuinely absent (fail closed, never the archive copy).
	if !e.legacyFallbackEnabled() {
		return storedRecord{}, fmt.Errorf("event %s not found in segments (migration complete): %w", id, os.ErrNotExist)
	}
	legacyPath := filepath.Join(e.eventsDir, id+".json")
	var data []byte
	if regularKind == "" {
		data, err = os.ReadFile(legacyPath)
	} else {
		data, err = home.ReadRootedRegularFile(e.home, legacyPath, regularKind)
	}
	if err != nil {
		return storedRecord{}, err
	}
	return storedRecord{id: id, bytes: data, diagnosticPath: legacyPath}, nil
}

func (e *engine) readSegment(path string, tolerateFinalTorn bool, regularKind string) ([]parsedFrame, error) {
	if regularKind == "" {
		return parseSegment(path, tolerateFinalTorn)
	}
	data, err := home.ReadRootedRegularFile(e.home, path, regularKind+" segment")
	if err != nil {
		return nil, err
	}
	return parseSegmentBytes(path, data, tolerateFinalTorn)
}

// invalidLooseEntry is a loose events/ file whose name is not a valid record
// reference. scanAll reports these instead of skipping them: whether an
// invalid name is ignorable depends on the caller's prefix filter (Scan fails
// closed when one MATCHES the requested prefixes), and the engine does not
// know the prefixes.
type invalidLooseEntry struct {
	name string
	err  error
}

func (e *engine) scanAll(regularOnly bool) (map[string]storedRecord, []invalidLooseEntry, error) {
	records := map[string]storedRecord{}
	var invalid []invalidLooseEntry

	if familyEntries, err := os.ReadDir(e.segRoot); err == nil {
		families := make([]string, 0, len(familyEntries))
		for _, entry := range familyEntries {
			if entry.IsDir() {
				families = append(families, entry.Name())
			}
		}
		sort.Strings(families)
		for _, family := range families {
			segments, err := e.familySegments(family)
			if err != nil {
				return nil, nil, err
			}
			for index, segment := range segments {
				frames, err := e.readSegment(segment.path, index == 0, regularKindForScan(regularOnly))
				if err != nil {
					return nil, nil, err
				}
				for i := len(frames) - 1; i >= 0; i-- {
					id := frames[i].Frame.ID
					if _, exists := records[id]; exists {
						continue
					}
					records[id] = storedRecord{
						id:             id,
						bytes:          append(append([]byte(nil), frames[i].Frame.Record...), '\n'),
						diagnosticPath: logicalReferencePrefix + id,
						fromSegment:    true,
					}
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}

	// Loose records are unioned only while the migration completion gate is open.
	// Once completion.json exists the scan is segment-authoritative: the loose
	// readdir is skipped entirely (no O(records) directory of point files to walk),
	// and a post-completion loose file is a legacy-writer regression for the
	// doctor, not scan authority. A missing events dir before completion still
	// surfaces os.ErrNotExist here, preserving the read-only Open contract.
	if e.legacyFallbackEnabled() {
		dirEntries, err := os.ReadDir(e.eventsDir)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range dirEntries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			if regularOnly && !entry.Type().IsRegular() {
				continue
			}
			id := strings.TrimSuffix(name, ".json")
			if _, exists := records[id]; exists {
				continue
			}
			if _, err := ReferenceForID(id); err != nil {
				invalid = append(invalid, invalidLooseEntry{name: name, err: err})
				continue
			}
			records[id] = storedRecord{id: id, diagnosticPath: filepath.Join(e.eventsDir, name)}
		}
	}
	return records, invalid, nil
}

func regularKindForScan(regularOnly bool) string {
	if regularOnly {
		return "event record"
	}
	return ""
}

func (e *engine) remove(id string) (retErr error) {
	if !e.localFS {
		return os.Remove(filepath.Join(e.eventsDir, id+".json"))
	}
	lock, err := acquireAppendLock(e.segRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.Remove(filepath.Join(e.eventsDir, id+".json"))
		}
		return err
	}
	defer func() {
		if releaseErr := lock.release(); retErr == nil && releaseErr != nil {
			retErr = releaseErr
		}
	}()

	families, err := e.segmentFamilies()
	if err != nil {
		return err
	}
	preferred := familyOf(nil, id)
	sort.SliceStable(families, func(i, j int) bool {
		return families[i] == preferred && families[j] != preferred
	})
	for _, family := range families {
		last, exists, err := e.lastWritten(family)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := e.repairTail(last.path); err != nil {
			return err
		}
		frames, err := parseSegment(last.path, false)
		if err != nil {
			return err
		}
		for i := len(frames) - 1; i >= 0; i-- {
			if frames[i].Frame.ID != id {
				continue
			}
			if i != len(frames)-1 {
				return fmt.Errorf("cannot remove non-tail event %s from append-only segment", id)
			}
			return truncateAndSync(last.path, frames[i].Offset, true)
		}
	}
	return os.Remove(filepath.Join(e.eventsDir, id+".json"))
}

func (e *engine) segmentFamilies() ([]string, error) {
	entries, err := os.ReadDir(e.segRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var families []string
	for _, entry := range entries {
		if entry.IsDir() {
			families = append(families, entry.Name())
		}
	}
	sort.Strings(families)
	return families, nil
}
