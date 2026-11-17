package corpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"compound/internal/sourceobs"
)

// The manifest is the commit point. Immutable hash-addressed vector shards are
// published first; interrupted writes cannot change an already visible index.
// Legacy vectors.json stays available for rollback and is migrated only once.
const vectorShardMagic = "CMPVEC01"

// Injectable only at the filesystem durability boundary: a failed directory
// sync must prevent a manifest from acknowledging new vector storage.
var syncCorpusDirectory = func(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type incrementalStats struct {
	BodyBytesRead     int64
	IndexBytesRead    int64
	IndexBytesWritten int64
	ReusedItems       int
	PreparedItems     int
}

func lockCorpusIndex(ctx context.Context, dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".refresh.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if err := ensureShardAccessGate(dir); err != nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
				return nil, err
			}
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func preparationKey(item sourceobs.SourceManifestItem) string {
	// Include every input used by body resolution and the embedding prefix.
	// Source paths themselves are provenance; only their title affects embedding.
	meta := struct {
		Hash, Ref, Policy, Kind, Title, Entities string
		Bytes                                    int64
	}{item.FileHash, item.BodyRef, item.BodyPolicy, item.SourceKind, buildPrefix(item, ""), prefixEntities(item.CompoundEntityRefs), item.BodyBytes}
	raw, _ := json.Marshal(meta)
	return string(raw)
}

func prepareIncremental(ctx context.Context, hp string, lane LaneConfig, old Manifest, rebuild bool, stats *incrementalStats) (preparedIndex, error) {
	source, err := sourceobs.LoadSourceManifest(hp, lane.SourceFamily)
	if err != nil {
		return preparedIndex{}, err
	}
	generation := lane.Generation
	if generation == "" && source.FamilyMeta != nil {
		generation = strings.TrimSpace(source.FamilyMeta.GenerationLabel)
	}
	out := Manifest{SchemaVersion: ManifestSchemaVersion, Lane: lane.Lane, SourceFamily: lane.SourceFamily,
		SourceManifestRef: source.AuthorityRef, EmbedderIdentity: lane.Embedder.Identity(), PrefixSchemaVersion: PrefixSchemaVersion,
		GenerationLabel: generation, Chunk: lane.Chunk, EmptyItems: map[string]bool{}}
	cache := map[string]sourceobs.SourceManifestItem{}
	compatible := !rebuild && old.PrefixSchemaVersion == PrefixSchemaVersion && old.Chunk == lane.Chunk &&
		old.EmbedderIdentity == lane.Embedder.Identity() && old.GenerationLabel == generation
	if compatible {
		for _, item := range old.Items {
			// Empty normalized transcripts must be explicitly attested; a legacy
			// missing chunk record is not an ingestion checkpoint.
			if len(item.Chunks) > 0 || old.EmptyItems[item.ID] {
				cache[preparationKey(item)] = item
			}
		}
	}
	var chunks []Chunk
	for _, item := range source.Items {
		if err := ctx.Err(); err != nil {
			return preparedIndex{}, err
		}
		if lane.Generation != "" && strings.TrimSpace(item.CorpusGeneration) != lane.Generation {
			continue
		}
		if err := validateCurrentItemAccess(lane, item); err != nil {
			return preparedIndex{}, err
		}
		key := preparationKey(item)
		prior, reuse := cache[key]
		// In-place bodies are mutable and have no immutable custody identity.
		if item.BodyPolicy != "owned_snapshot" {
			reuse = false
		}
		copyItem := item
		if reuse {
			copyItem.Chunks = append([]sourceobs.SourceManifestChunk(nil), prior.Chunks...)
			copyItem.PageHash = prior.PageHash
			if len(prior.Chunks) == 0 {
				out.EmptyItems[item.ID] = true
			}
			for _, c := range prior.Chunks {
				chunks = append(chunks, Chunk{ID: buildChunkID(item.ID, c.Ordinal), ItemID: item.ID, Ordinal: c.Ordinal, ChunkHash: c.ChunkHash})
			}
			stats.ReusedItems++
		} else {
			body, err := readItemBody(hp, item)
			if err != nil {
				return preparedIndex{}, err
			}
			stats.BodyBytesRead += int64(len(body))
			stats.PreparedItems++
			itemChunks, err := chunkItemContext(ctx, item, body, lane.Chunk)
			if err != nil {
				return preparedIndex{}, fmt.Errorf("prepare item %s: %w", item.ID, err)
			}
			copyItem.Chunks = make([]sourceobs.SourceManifestChunk, 0, len(itemChunks))
			for _, c := range itemChunks {
				copyItem.Chunks = append(copyItem.Chunks, sourceobs.SourceManifestChunk{Ordinal: c.Ordinal, ChunkHash: c.ChunkHash})
				chunks = append(chunks, c)
			}
			if len(itemChunks) == 0 {
				out.EmptyItems[item.ID] = true
			}
			copyItem.PageHash = sourceobs.HashGbrainPageFromChunks(copyItem.Chunks)
			cache[key] = copyItem
		}
		copyItem.Embedding = &sourceobs.SourceManifestEmbedding{Provider: lane.Embedder.Provider, Model: lane.Embedder.Model, Dims: lane.Embedder.Dims}
		if copyItem.CorpusGeneration == "" {
			copyItem.CorpusGeneration = generation
		}
		out.Items = append(out.Items, copyItem)
	}
	hashes := map[string]string{}
	for _, c := range chunks {
		hashes[c.ID] = c.ChunkHash
	}
	return preparedIndex{identity: IdentityFile{SchemaVersion: IdentitySchemaVersion, Lane: lane.Lane, SourceFamily: lane.SourceFamily,
		EmbedderIdentity: lane.Embedder.Identity(), PrefixSchemaVersion: PrefixSchemaVersion, Chunk: lane.Chunk, GenerationLabel: generation, SourceManifestRef: source.AuthorityRef},
		manifest: out, chunks: chunks, hashes: hashes}, nil
}

func indexIncremental(ctx context.Context, hp string, lane LaneConfig, embedder Embedder, opts IndexOptions) (IndexReport, error) {
	started := time.Now()
	dir := IndexDir(hp, lane)
	unlock, err := lockCorpusIndex(ctx, dir)
	if err != nil {
		return IndexReport{}, err
	}
	defer unlock()
	if err := refuseMixedIdentity(dir, lane.Embedder.Identity()); err != nil {
		return IndexReport{}, err
	}
	old, err := readManifest(dir)
	if err != nil && !os.IsNotExist(err) {
		return IndexReport{}, err
	}
	if err := checkIndexModelDigest(dir, lane, old, opts.Rebuild, false); err != nil {
		return IndexReport{}, err
	}
	stats := incrementalStats{}
	prepared, err := prepareIncremental(ctx, hp, lane, old, opts.Rebuild, &stats)
	if err != nil {
		return IndexReport{}, err
	}
	if err := verifyModelDigest(ctx, lane.Embedder); err != nil {
		return IndexReport{}, err
	}
	binding := modelDigestBinding(old, lane.Embedder.ModelDigest, opts.Rebuild)
	prepared.identity.ModelDigest = lane.Embedder.ModelDigest
	prepared.manifest.ModelDigest = lane.Embedder.ModelDigest
	prepared.identity.ModelDigestBinding = binding
	prepared.manifest.ModelDigestBinding = binding
	report := IndexReport{SchemaVersion: ManifestSchemaVersion, Lane: lane.Lane, IndexDir: dir, EmbedderIdentity: lane.Embedder.Identity(),
		Items: len(prepared.manifest.Items), TotalChunks: len(prepared.chunks), Rebuild: opts.Rebuild}
	report.Added, report.Changed, report.Removed = stalenessCounts(old, prepared.manifest)
	finish := func() {
		report.BodyBytesRead = stats.BodyBytesRead
		report.IndexBytesRead = stats.IndexBytesRead
		report.IndexBytesWritten = stats.IndexBytesWritten
		report.PreparedItems = stats.PreparedItems
		report.ReusedItems = stats.ReusedItems
		report.WallMilliseconds = time.Since(started).Milliseconds()
	}
	// Metadata inventory is reconciled on every pass. No vector or body reads
	// and no index writes are needed when the committed generation is complete.
	var progress ProgressFile
	if !opts.Rebuild && old.ModelDigest == prepared.manifest.ModelDigest && old.ModelDigestBinding == binding && old.VectorShards != nil && old.VectorPending == 0 && old.VectorError == "" && reflect.DeepEqual(old.Items, prepared.manifest.Items) &&
		(len(old.EmptyItems) == 0 && len(prepared.manifest.EmptyItems) == 0 || reflect.DeepEqual(old.EmptyItems, prepared.manifest.EmptyItems)) && old.Chunk == lane.Chunk &&
		old.PrefixSchemaVersion == PrefixSchemaVersion && readJSONFile(filepath.Join(dir, "progress.json"), &progress) == nil &&
		progress.PendingChunks == 0 && progress.TotalChunks == len(prepared.chunks) && progress.Error == "" {
		if err := statVectorShards(dir, old.VectorShards); err != nil {
			return report, err
		}
		report.ReusedChunks = len(prepared.chunks)
		report.EmbeddedChunks = len(prepared.chunks)
		report.Unchanged = true
		report.ShardCompaction = compactRedundantShards(ctx, dir, old, lane.Embedder.Dims, lane.ShardCompaction)
		finish()
		return report, nil
	}
	// Each shard has at most 1/256 of the vector hash space. Only changed
	// shards are replaced, and duplicates address the same semantic vector.
	shards := map[string]map[string][]float32{}
	refs := map[string]string{}
	for k, v := range old.VectorShards {
		refs[k] = v
	}
	known := map[string]bool{}
	if !opts.Rebuild {
		if old.VectorShards != nil {
			if old.VectorPending == 0 && old.VectorError == "" {
				if err := statVectorShards(dir, refs); err != nil {
					return report, err
				}
				for _, hash := range manifestChunkHashes(old) {
					prefix, e := vectorPrefix(hash)
					if e != nil {
						return report, e
					}
					if refs[prefix] == "" {
						return report, fmt.Errorf("committed vector partition missing")
					}
					known[hash] = true
				}
			} else {
				for prefix, ref := range old.VectorShards {
					values, n, err := readVectorShard(dir, ref, lane.Embedder.Dims)
					if err != nil {
						return report, err
					}
					shards[prefix] = values
					stats.IndexBytesRead += n
				}
			}
		} else if len(old.Items) > 0 {
			vectors, err := readVectors(dir)
			if err != nil {
				return report, err
			}
			if st, e := os.Stat(filepath.Join(dir, "vectors.json")); e == nil {
				stats.IndexBytesRead += st.Size()
			}
			for id, hash := range manifestChunkHashes(old) {
				vec := vectors.Vectors[id]
				if len(vec) != lane.Embedder.Dims {
					continue
				}
				normalized, e := normalizeVector(vec)
				if e != nil {
					continue
				}
				prefix, e := vectorPrefix(hash)
				if e != nil {
					return report, e
				}
				if shards[prefix] == nil {
					shards[prefix] = map[string][]float32{}
				}
				shards[prefix][hash] = normalized
			}
		}
	} else {
		refs = map[string]string{}
	}
	dirty := map[string]bool{}
	if old.VectorShards == nil || opts.Rebuild {
		for prefix := range shards {
			dirty[prefix] = true
		}
	}
	for _, values := range shards {
		for hash := range values {
			known[hash] = true
		}
	}
	var pending []Chunk
	hydrated := map[string][]Chunk{}
	seen := map[string]bool{}
	for _, chunk := range prepared.chunks {
		if known[chunk.ChunkHash] {
			report.ReusedChunks++
			continue
		}
		if seen[chunk.ChunkHash] {
			continue
		}
		seen[chunk.ChunkHash] = true
		// Cached metadata can come from an interrupted legacy partial build.
		// Rehydrate only that item's body when its vector is actually missing.
		if chunk.EmbedText == "" {
			for _, item := range prepared.manifest.Items {
				if item.ID == chunk.ItemID {
					cc, ok := hydrated[item.ID]
					if !ok {
						body, e := readItemBody(hp, item)
						if e != nil {
							return report, e
						}
						stats.BodyBytesRead += int64(len(body))
						cc, e = chunkItemContext(ctx, item, body, lane.Chunk)
						if e != nil {
							return report, e
						}
						hydrated[item.ID] = cc
					}
					if chunk.Ordinal >= len(cc) || cc[chunk.Ordinal].ChunkHash != chunk.ChunkHash {
						return report, fmt.Errorf("cached chunk identity drift")
					}
					chunk = cc[chunk.Ordinal]
					break
				}
			}
		}
		pending = append(pending, chunk)
	}
	persist := func(errText string) error {
		for prefix := range dirty {
			ref, n, e := writeVectorShard(dir, shards[prefix], lane.Embedder.Dims)
			if e != nil {
				return e
			}
			refs[prefix] = ref
			stats.IndexBytesWritten += n
			delete(dirty, prefix)
		}
		if len(prepared.chunks) == 0 {
			refs = map[string]string{}
		}
		prepared.manifest.VectorShards = refs
		current := map[string][]float32{}
		for _, c := range prepared.chunks {
			if known[c.ChunkHash] {
				current[c.ID] = nil
			}
		}
		prepared.manifest.VectorPending = len(prepared.chunks) - len(current)
		prepared.manifest.VectorError = errText
		// identity is configuration, manifest atomically commits the shard set.
		for _, entry := range []struct {
			name  string
			value any
		}{{"identity.json", prepared.identity}, {"manifest.json", prepared.manifest}, {"progress.json", progressFor(lane, len(prepared.chunks), current, errText)}} {
			raw, e := json.MarshalIndent(entry.value, "", "  ")
			if e != nil {
				return e
			}
			if e = writeJSONFile(filepath.Join(dir, entry.name), entry.value); e != nil {
				return e
			}
			stats.IndexBytesWritten += int64(len(raw) + 1)
		}
		report.EmbeddedChunks = len(current)
		return nil
	}
	for start := 0; start < len(pending); start += embedBatchSize {
		if err := ctx.Err(); err != nil {
			_ = persist(err.Error())
			finish()
			return report, err
		}
		end := start + embedBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		texts := make([]string, end-start)
		for i, c := range pending[start:end] {
			texts[i] = c.EmbedText
		}
		vectors, e := embedder.Embed(ctx, texts)
		if e == nil && len(vectors) != len(texts) {
			e = fmt.Errorf("embedder vector count mismatch")
		}
		if e != nil {
			_ = persist(e.Error())
			finish()
			return report, e
		}
		for i, vec := range vectors {
			normalized, e := normalizeVector(vec)
			if e != nil || len(vec) != lane.Embedder.Dims {
				if e == nil {
					e = fmt.Errorf("embedder dimension mismatch")
				}
				_ = persist(e.Error())
				finish()
				return report, e
			}
			hash := pending[start+i].ChunkHash
			prefix, e := vectorPrefix(hash)
			if e != nil {
				return report, e
			}
			if shards[prefix] == nil {
				if ref := refs[prefix]; ref != "" {
					values, n, e := readVectorShard(dir, ref, lane.Embedder.Dims)
					if e != nil {
						return report, e
					}
					shards[prefix] = values
					stats.IndexBytesRead += n
				} else {
					shards[prefix] = map[string][]float32{}
				}
			}
			shards[prefix][hash] = normalized
			known[hash] = true
			dirty[prefix] = true
			report.NewEmbeddedChunks++
		}
		if err := persist(""); err != nil {
			finish()
			return report, err
		}
	}
	if len(pending) == 0 {
		if err := persist(""); err != nil {
			finish()
			return report, err
		}
	}
	report.ShardCompaction = compactRedundantShards(ctx, dir, prepared.manifest, lane.Embedder.Dims, lane.ShardCompaction)
	finish()
	return report, nil
}

func vectorPrefix(hash string) (string, error) {
	raw := strings.TrimPrefix(hash, "sha256:")
	if len(raw) != 64 {
		return "", fmt.Errorf("invalid vector hash")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", err
	}
	return raw[:2], nil
}
func statVectorShards(dir string, refs map[string]string) error {
	for _, ref := range refs {
		path, err := vectorShardPath(dir, ref)
		if err != nil {
			return err
		}
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("vector shard is not a regular file")
		}
	}
	return nil
}
func vectorShardPath(dir, ref string) (string, error) {
	if len(ref) != 64 {
		return "", fmt.Errorf("invalid vector shard reference")
	}
	if _, err := hex.DecodeString(ref); err != nil {
		return "", err
	}
	return filepath.Join(dir, "vector-shards", ref+".bin"), nil
}
func writeVectorShard(dir string, values map[string][]float32, dims int) (string, int64, error) {
	var body bytes.Buffer
	body.WriteString(vectorShardMagic)
	_ = binary.Write(&body, binary.LittleEndian, uint32(dims))
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(values)))
	keys := make([]string, 0, len(values))
	for hash := range values {
		keys = append(keys, hash)
	}
	sort.Strings(keys)
	for _, hash := range keys {
		raw, err := hex.DecodeString(strings.TrimPrefix(hash, "sha256:"))
		if err != nil || len(raw) != 32 {
			return "", 0, fmt.Errorf("invalid chunk hash")
		}
		if len(values[hash]) != dims {
			return "", 0, fmt.Errorf("invalid shard dimensions")
		}
		body.Write(raw)
		_ = binary.Write(&body, binary.LittleEndian, values[hash])
	}
	digest := sha256.Sum256(body.Bytes())
	ref := hex.EncodeToString(digest[:])
	path, _ := vectorShardPath(dir, ref)
	if st, err := os.Stat(path); err == nil && st.Size() == int64(body.Len()) {
		existing, err := os.ReadFile(path)
		if err != nil {
			return "", 0, err
		}
		if sha256.Sum256(existing) != digest {
			return "", 0, fmt.Errorf("existing vector shard checksum mismatch")
		}
		if err := syncCorpusDirectory(filepath.Dir(path)); err != nil {
			return "", 0, err
		}
		if err := syncCorpusDirectory(dir); err != nil {
			return "", 0, err
		}
		return ref, 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vector-*")
	if err != nil {
		return "", 0, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(body.Bytes()); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, err
	}
	if err = os.Rename(name, path); err != nil {
		return "", 0, err
	}
	if err := syncCorpusDirectory(filepath.Dir(path)); err != nil {
		return "", 0, err
	}
	if err := syncCorpusDirectory(dir); err != nil {
		return "", 0, err
	}
	return ref, int64(body.Len()), nil
}
func readVectorShard(dir, ref string, dims int) (map[string][]float32, int64, error) {
	path, err := vectorShardPath(dir, ref)
	if err != nil {
		return nil, 0, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, int64(len(raw)), err
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != ref {
		return nil, int64(len(raw)), fmt.Errorf("vector shard checksum mismatch")
	}
	if len(raw) < 16 || string(raw[:8]) != vectorShardMagic || int(binary.LittleEndian.Uint32(raw[8:12])) != dims {
		return nil, int64(len(raw)), fmt.Errorf("vector shard identity mismatch")
	}
	count := int(binary.LittleEndian.Uint32(raw[12:16]))
	stride := 32 + 4*dims
	if dims <= 0 || dims > 65536 || stride < 32 || count != (len(raw)-16)/stride || 16+count*stride != len(raw) {
		return nil, int64(len(raw)), fmt.Errorf("vector shard length mismatch")
	}
	values := make(map[string][]float32, count)
	reader := bytes.NewReader(raw[16:])
	for i := 0; i < count; i++ {
		hash := make([]byte, 32)
		if _, err := io.ReadFull(reader, hash); err != nil {
			return nil, int64(len(raw)), err
		}
		vec := make([]float32, dims)
		if err := binary.Read(reader, binary.LittleEndian, vec); err != nil {
			return nil, int64(len(raw)), err
		}
		values["sha256:"+hex.EncodeToString(hash)] = vec
	}
	return values, int64(len(raw)), nil
}

var readShardForSnapshot = readVectorShard

func readShardedVectors(dir string, manifest Manifest) (VectorFile, error) {
	byHash := map[string][]float32{}
	// The embedder's identity includes dimensions, but the manifest also
	// carries the explicit embedding record on each item.
	dims := 0
	for _, item := range manifest.Items {
		if item.Embedding != nil {
			dims = item.Embedding.Dims
			break
		}
	}
	if dims == 0 && len(manifest.VectorShards) > 0 {
		return VectorFile{}, fmt.Errorf("vector shard dimensions missing")
	}
	for _, ref := range manifest.VectorShards {
		values, _, err := readShardForSnapshot(dir, ref, dims)
		if err != nil {
			return VectorFile{}, err
		}
		for hash, vec := range values {
			byHash[hash] = vec
		}
	}
	out := VectorFile{Vectors: map[string][]float32{}}
	for id, hash := range manifestChunkHashes(manifest) {
		if vec := byHash[hash]; vec != nil {
			out.Vectors[id] = vec
		}
	}
	return out, nil
}
