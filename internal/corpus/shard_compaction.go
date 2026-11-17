package corpus

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

// This is configuration, not an attestation that old readers are gone. Enabling
// requires the existing Work Action/Receipt and RuntimeBinding/native readback
// path plus an independent consumer census. Unknown/old readers keep it false.
type ShardCompactionConfig struct {
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

type ShardCompactionReport struct {
	Protocol                     string `json:"protocol"`
	Status                       string `json:"status"`
	ScannedFiles                 int    `json:"scanned_files"`
	ReadBytes                    int64  `json:"read_bytes"`
	ReclaimedFiles               int    `json:"reclaimed_files"`
	ReclaimedPayloadBytes        int64  `json:"reclaimed_payload_bytes"`
	ReclaimedAllocatedBytes      int64  `json:"reclaimed_allocated_bytes"`
	RetainedUniqueFiles          int    `json:"retained_unique_files"`
	RetainedDifferentVectorFiles int    `json:"retained_different_vector_files"`
	RetainedUnverifiedFiles      int    `json:"retained_unverified_files"`
	Deferred                     bool   `json:"deferred"`
	FreeBytesBefore              int64  `json:"free_bytes_before,omitempty"`
	FreeBytesAfter               int64  `json:"free_bytes_after,omitempty"`
	Error                        string `json:"error,omitempty"`
}

const (
	shardCompactMaxScanned = 512
	shardCompactMaxRemoved = 16
	shardCompactReadBudget = int64(64 << 20)
	shardCompactTimeBudget = 150 * time.Millisecond
)

// Called only by the existing index writer, after successful complete durable
// publication (including unchanged complete generations). No source body,
// manifest, vectors.json rollback file, or vector entry is removed.
func compactRedundantShards(ctx context.Context, dir string, manifest Manifest, dims int, config ShardCompactionConfig) ShardCompactionReport {
	r := ShardCompactionReport{Protocol: shardReaderProtocol, Status: "disabled"}
	if !config.Enabled {
		return r
	}
	if ctx.Err() != nil {
		r.Status = "cancelled"
		r.Deferred = true
		return r
	}
	if manifest.VectorShards == nil || manifest.VectorPending != 0 || manifest.VectorError != "" {
		r.Status = "incomplete_or_legacy"
		return r
	}
	release, busy, err := tryShardCollection(dir)
	if busy {
		r.Status = "readers_busy"
		r.Deferred = true
		return r
	}
	if err != nil {
		r.Status = "gate_error"
		r.Error = err.Error()
		return r
	}
	defer release()
	committed, e := readManifest(dir)
	if e != nil || !reflect.DeepEqual(committed.VectorShards, manifest.VectorShards) || committed.VectorPending != 0 || committed.VectorError != "" || committed.ModelDigest != manifest.ModelDigest || committed.EmbedderIdentity != manifest.EmbedderIdentity {
		r.Status = "uncommitted_manifest"
		if e != nil {
			r.Error = e.Error()
		}
		return r
	}
	r.Status = "complete"
	r.FreeBytesBefore = shardFreeBytes(dir)
	started := time.Now()
	path := filepath.Join(dir, "vector-shards")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		r.FreeBytesAfter = shardFreeBytes(dir)
		return r
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		r.Status = "unsafe_directory"
		return r
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		r.Status = "directory_error"
		r.Error = err.Error()
		return r
	}
	defer root.Close()
	list, err := root.Open(".")
	if err != nil {
		r.Status = "directory_error"
		r.Error = err.Error()
		return r
	}
	defer list.Close()
	refs := map[string]bool{}
	for _, ref := range manifest.VectorShards {
		refs[ref] = true
	}
	current := map[string]map[string][]float32{}
	finish := func() ShardCompactionReport {
		if r.ReclaimedFiles > 0 {
			if e := syncCorpusDirectory(path); e != nil {
				r.Status = "sync_error"
				r.Error = e.Error()
			}
		}
		r.FreeBytesAfter = shardFreeBytes(dir)
		return r
	}
	for r.ScannedFiles < shardCompactMaxScanned && r.ReclaimedFiles < shardCompactMaxRemoved {
		if ctx.Err() != nil || time.Since(started) >= shardCompactTimeBudget {
			r.Deferred = true
			break
		}
		entries, e := list.ReadDir(1)
		if e != nil && len(entries) == 0 {
			if e != io.EOF {
				r.Status = "scan_error"
				r.Error = e.Error()
			}
			break
		}
		if len(entries) == 0 {
			break
		}
		entry := entries[0]
		r.ScannedFiles++
		name := entry.Name()
		if !strings.HasSuffix(name, ".bin") {
			continue
		}
		ref := strings.TrimSuffix(name, ".bin")
		if _, e := vectorShardPath(dir, ref); e != nil || refs[ref] {
			continue
		}
		before, e := root.Lstat(name)
		if e != nil || !before.Mode().IsRegular() {
			r.RetainedUnverifiedFiles++
			continue
		}
		if before.Size() > shardCompactReadBudget-r.ReadBytes {
			r.Deferred = true
			continue
		}
		candidate, n, e := readVectorShard(dir, ref, dims)
		r.ReadBytes += n
		if e != nil {
			r.RetainedUnverifiedFiles++
			continue
		}
		unique, different, unverified := false, false, false
		for hash, vec := range candidate {
			prefix, e := vectorPrefix(hash)
			if e != nil {
				unverified = true
				break
			}
			currentRef := manifest.VectorShards[prefix]
			if currentRef == "" {
				unique = true
				break
			}
			values, loaded := current[currentRef]
			if !loaded {
				p, e := vectorShardPath(dir, currentRef)
				if e != nil {
					unverified = true
					break
				}
				st, e := os.Lstat(p)
				if e != nil || !st.Mode().IsRegular() {
					unverified = true
					break
				}
				if st.Size() > shardCompactReadBudget-r.ReadBytes {
					r.Deferred = true
					unverified = true
					break
				}
				values, n, e = readVectorShard(dir, currentRef, dims)
				r.ReadBytes += n
				if e != nil {
					unverified = true
					break
				}
				current[currentRef] = values
			}
			preserved, ok := values[hash]
			if !ok {
				unique = true
				break
			}
			if !sameVectorBits(vec, preserved) {
				different = true
				break
			}
		}
		switch {
		case unique:
			r.RetainedUniqueFiles++
			continue
		case different:
			r.RetainedDifferentVectorFiles++
			continue
		case unverified:
			r.RetainedUnverifiedFiles++
			continue
		}
		// Revalidate names and directory identity immediately before removal.
		after, e := root.Lstat(name)
		namedDir, de := os.Lstat(path)
		if e != nil || de != nil || !os.SameFile(info, namedDir) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			r.RetainedUnverifiedFiles++
			continue
		}
		if e := root.Remove(name); e != nil {
			r.RetainedUnverifiedFiles++
			r.Error = e.Error()
			continue
		}
		r.ReclaimedFiles++
		r.ReclaimedPayloadBytes += before.Size()
		r.ReclaimedAllocatedBytes += shardAllocatedBytes(before)
	}
	if r.ScannedFiles >= shardCompactMaxScanned || r.ReclaimedFiles >= shardCompactMaxRemoved {
		r.Deferred = true
	}
	return finish()
}

func sameVectorBits(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i, x := range a {
		y := b[i]
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) || math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) || math.Float32bits(x) != math.Float32bits(y) {
			return false
		}
	}
	return true
}

func shardAllocatedBytes(info os.FileInfo) int64 {
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.Struct {
		f := v.FieldByName("Blocks")
		if f.IsValid() && f.CanInt() {
			return f.Int() * 512
		}
	}
	return 0
}

func shardFreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
