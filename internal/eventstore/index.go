package eventstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"compound/internal/home"
)

const (
	indexDirName = "index"
	indexVersion = 1
)

type segmentIndex struct {
	IndexVersion int                   `json:"index_version"`
	Segment      segmentIndexSource    `json:"segment"`
	Entries      map[string]indexEntry `json:"entries"`
}

type segmentIndexSource struct {
	Name             string `json:"name"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

type indexEntry struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

type cachedSourceHash struct {
	size             int64
	modifiedUnixNano int64
	hash             string
}

// segmentIndexCache memoizes the fully-loaded, validated sidecar index of a
// SEALED segment for the lifetime of one process, keyed by the index-file path.
// Without it every point read of a sealed record re-opened and re-JSON-parsed
// the whole .idx file (loadOrBuildIndex). After the 03:00 UTC day seal, a
// projection or close path that Gets tens of thousands of sealed records each
// re-parsed a multi-MB index — the sealed twin of the hot-segment reparse fixed
// in 6aa94840/0cd766b2 (observed live 2026-07-16: ConsumeReceipts CPU-bound in
// loadOrBuildIndex).
//
// Stat-guarded on BOTH files: the index file (size+mtime, so a rebuild or an
// `rm -rf index/` recovery re-parse invalidates the memo) AND the sealed
// segment (size+mtime, so the existing drift contract still re-validates the
// moment the segment underneath moves). A memo hit skips the read, the JSON
// parse, and the O(entries) range revalidation; a miss falls through to the
// unchanged load/validate/rebuild path. The index stays cache, never authority.
type segmentIndexCache struct {
	mu      sync.Mutex
	entries map[string]indexCacheEntry
}

// Package-level for the same reason as sharedHotCache: consumers open a fresh
// Store per operation, and an engine-scoped memo would reload+revalidate every
// sealed segment's sidecar index on every one of their calls. Keys are
// absolute index paths; the double stat guard (index file + sealed segment)
// carries correctness, not cache scope.
var sharedIndexCache segmentIndexCache

type indexCacheEntry struct {
	idxSize  int64
	idxMtime time.Time
	segSize  int64
	segMtime time.Time
	index    segmentIndex
}

// indexParseObserver is a test-only seam fired on every FULL read+parse of a
// sealed segment's sidecar index — the exact operation the memo eliminates. It
// is nil in production and never surfaces in the public API; a falsifier
// installs it to prove a projection's thousands of sealed Gets collapse to one
// index parse per segment (counter-seam precedent: segmentParseObserver,
// work_dde825bbd7a5).
var indexParseObserver func(path string)

func notifyIndexParse(path string) {
	if indexParseObserver != nil {
		indexParseObserver(path)
	}
}

func (e *engine) indexPath(family string, segment segmentFile) string {
	stem := segment.day
	if segment.seq > 0 {
		stem += "." + strconv.Itoa(segment.seq)
	}
	return filepath.Join(e.indexRoot, family, stem+".idx")
}

func (e *engine) readIndexedFrame(family string, segment segmentFile, id, regularKind string) (Frame, bool, error) {
	index, err := e.loadOrBuildIndex(family, segment)
	if err != nil {
		return Frame{}, false, err
	}
	entry, found := index.Entries[id]
	if !found {
		return Frame{}, false, nil
	}
	frame, err := e.readFrameAt(segment.path, entry, regularKind)
	if err == nil && frame.ID == id {
		return frame, true, nil
	}

	reason := "indexed frame validation failed"
	if err == nil {
		reason = fmt.Sprintf("validated hit id %q differs from index id %q", frame.ID, id)
	} else {
		reason = fmt.Sprintf("indexed frame at offset %d is invalid: %v", entry.Offset, err)
	}
	index, err = e.rebuildIndex(family, segment, reason)
	if err != nil {
		return Frame{}, false, err
	}
	entry, found = index.Entries[id]
	if !found {
		return Frame{}, false, nil
	}
	frame, err = e.readFrameAt(segment.path, entry, regularKind)
	if err != nil {
		return Frame{}, false, corruptionAt(segment.path, nil, 0, entry.Offset, err)
	}
	if frame.ID != id {
		return Frame{}, false, fmt.Errorf("rebuilt event index %s maps %q to frame %q", e.indexPath(family, segment), id, frame.ID)
	}
	return frame, true, nil
}

func (e *engine) loadOrBuildIndex(family string, segment segmentFile) (segmentIndex, error) {
	path := e.indexPath(family, segment)
	if index, ok := e.cachedIndex(path, segment.path); ok {
		return index, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return e.rebuildIndex(family, segment, "index missing")
	}
	if err != nil {
		return segmentIndex{}, err
	}
	notifyIndexParse(path)
	var index segmentIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return e.rebuildIndex(family, segment, fmt.Sprintf("index decode failed: %v", err))
	}
	if reason, err := e.validateLoadedIndex(segment, index); err != nil {
		return segmentIndex{}, err
	} else if reason != "" {
		return e.rebuildIndex(family, segment, reason)
	}
	e.rememberIndex(path, segment.path, index)
	return index, nil
}

// cachedIndex returns the memoized validated index for idxPath when both the
// index file and its sealed segment are byte-for-byte stat-identical to the
// snapshot taken at memo time. Stat happens before the lock (mirroring
// hotFrames) so a slow syscall never serializes readers; the returned index's
// Entries map is never mutated in place, so serving it after releasing the lock
// is race-free even against a concurrent rememberIndex overwrite.
func (e *engine) cachedIndex(idxPath, segPath string) (segmentIndex, bool) {
	idxInfo, err := os.Stat(idxPath)
	if err != nil {
		return segmentIndex{}, false
	}
	segInfo, err := os.Stat(segPath)
	if err != nil {
		return segmentIndex{}, false
	}
	sharedIndexCache.mu.Lock()
	defer sharedIndexCache.mu.Unlock()
	entry, ok := sharedIndexCache.entries[idxPath]
	if ok &&
		entry.idxSize == idxInfo.Size() && entry.idxMtime.Equal(idxInfo.ModTime()) &&
		entry.segSize == segInfo.Size() && entry.segMtime.Equal(segInfo.ModTime()) {
		return entry.index, true
	}
	return segmentIndex{}, false
}

// rememberIndex snapshots the index and segment stats and memoizes the parsed
// index under idxPath. A stat failure between the load and here simply skips the
// memo (the next Get re-loads) rather than caching an unverifiable entry.
func (e *engine) rememberIndex(idxPath, segPath string, index segmentIndex) {
	idxInfo, err := os.Stat(idxPath)
	if err != nil {
		return
	}
	segInfo, err := os.Stat(segPath)
	if err != nil {
		return
	}
	sharedIndexCache.mu.Lock()
	defer sharedIndexCache.mu.Unlock()
	if sharedIndexCache.entries == nil {
		sharedIndexCache.entries = map[string]indexCacheEntry{}
	}
	sharedIndexCache.entries[idxPath] = indexCacheEntry{
		idxSize:  idxInfo.Size(),
		idxMtime: idxInfo.ModTime(),
		segSize:  segInfo.Size(),
		segMtime: segInfo.ModTime(),
		index:    index,
	}
}

func (e *engine) validateLoadedIndex(segment segmentFile, index segmentIndex) (string, error) {
	if index.IndexVersion != indexVersion {
		return fmt.Sprintf("index version %d differs from %d", index.IndexVersion, indexVersion), nil
	}
	if index.Segment.Name != filepath.Base(segment.path) {
		return fmt.Sprintf("source name %q differs from %q", index.Segment.Name, filepath.Base(segment.path)), nil
	}
	if _, err := decodeSHA256(index.Segment.SHA256); err != nil {
		return fmt.Sprintf("source hash is invalid: %v", err), nil
	}
	if index.Entries == nil {
		return "entry map is missing", nil
	}
	info, err := os.Stat(segment.path)
	if err != nil {
		return "", err
	}
	if index.Segment.Size != info.Size() {
		return fmt.Sprintf("source size drift: index=%d segment=%d", index.Segment.Size, info.Size()), nil
	}
	for id, entry := range index.Entries {
		if entry.Offset < 0 || entry.Length <= 0 || entry.Offset > info.Size()-entry.Length {
			return fmt.Sprintf("entry %q has invalid range offset=%d length=%d", id, entry.Offset, entry.Length), nil
		}
	}
	actualHash, err := e.sourceHash(segment.path, info)
	if err != nil {
		return "", err
	}
	if index.Segment.SHA256 != actualHash {
		return fmt.Sprintf("source hash drift: index=%s segment=%s", index.Segment.SHA256, actualHash), nil
	}
	if index.Segment.ModifiedUnixNano != info.ModTime().UnixNano() {
		return fmt.Sprintf("source modification time drift: index=%d segment=%d", index.Segment.ModifiedUnixNano, info.ModTime().UnixNano()), nil
	}
	return "", nil
}

func (e *engine) rebuildIndex(family string, segment segmentFile, reason string) (segmentIndex, error) {
	data, err := os.ReadFile(segment.path)
	if err != nil {
		return segmentIndex{}, err
	}
	frames, err := parseSegmentBytes(segment.path, data, false)
	if err != nil {
		return segmentIndex{}, err
	}
	info, err := os.Stat(segment.path)
	if err != nil {
		return segmentIndex{}, err
	}
	if info.Size() != int64(len(data)) {
		return segmentIndex{}, fmt.Errorf("source segment changed while rebuilding index: %s", segment.path)
	}
	entries := make(map[string]indexEntry, len(frames))
	for _, frame := range frames {
		entries[frame.Frame.ID] = indexEntry{Offset: frame.Offset, Length: frame.End - frame.Offset}
	}
	hash := contentHash(data)
	index := segmentIndex{
		IndexVersion: indexVersion,
		Segment: segmentIndexSource{
			Name:             filepath.Base(segment.path),
			Size:             int64(len(data)),
			SHA256:           hash,
			ModifiedUnixNano: info.ModTime().UnixNano(),
		},
		Entries: entries,
	}
	e.rememberSourceHash(segment.path, info, hash)
	path := e.indexPath(family, segment)
	if err := writeIndex(path, index); err != nil {
		return segmentIndex{}, err
	}
	e.logf("eventstore: rebuilt index %s from %s (%s)", path, segment.path, reason)
	return index, nil
}

func writeIndex(path string, index segmentIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := writeFull(tmp, data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func (e *engine) readFrameAt(path string, entry indexEntry, regularKind string) (Frame, error) {
	var data []byte
	if regularKind == "" {
		file, err := os.Open(path)
		if err != nil {
			return Frame{}, err
		}
		defer file.Close()
		data = make([]byte, entry.Length)
		n, err := file.ReadAt(data, entry.Offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return Frame{}, err
		}
		if n != len(data) {
			return Frame{}, io.ErrUnexpectedEOF
		}
	} else {
		whole, err := home.ReadRootedRegularFile(e.home, path, regularKind+" segment")
		if err != nil {
			return Frame{}, err
		}
		if entry.Offset < 0 || entry.Length <= 0 || entry.Offset > int64(len(whole))-entry.Length {
			return Frame{}, io.ErrUnexpectedEOF
		}
		data = append([]byte(nil), whole[entry.Offset:entry.Offset+entry.Length]...)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return Frame{}, &FrameInvalidError{Reason: "indexed frame is not newline terminated"}
	}
	line := data[:len(data)-1]
	if bytes.ContainsRune(line, '\n') {
		return Frame{}, &FrameInvalidError{Reason: "indexed range contains multiple frames"}
	}
	return decodeFrame(line)
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (e *engine) sourceHash(path string, info os.FileInfo) (string, error) {
	e.sourceHashMu.Lock()
	cached, ok := e.sourceHashes[path]
	e.sourceHashMu.Unlock()
	if ok && cached.size == info.Size() && cached.modifiedUnixNano == info.ModTime().UnixNano() {
		return cached.hash, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	after, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if after.Size() != info.Size() || after.ModTime().UnixNano() != info.ModTime().UnixNano() || int64(len(data)) != info.Size() {
		return "", fmt.Errorf("source segment changed while hashing index provenance: %s", path)
	}
	hash := contentHash(data)
	e.rememberSourceHash(path, info, hash)
	return hash, nil
}

func (e *engine) rememberSourceHash(path string, info os.FileInfo, hash string) {
	e.sourceHashMu.Lock()
	defer e.sourceHashMu.Unlock()
	if e.sourceHashes == nil {
		e.sourceHashes = make(map[string]cachedSourceHash)
	}
	e.sourceHashes[path] = cachedSourceHash{
		size:             info.Size(),
		modifiedUnixNano: info.ModTime().UnixNano(),
		hash:             hash,
	}
}

func decodeSHA256(value string) ([]byte, error) {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return nil, fmt.Errorf("want %d lowercase hex characters", sha256.Size*2)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}
