package sourceobs

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed codex_history.py
var codexHistoryReader string

type codexHistoryRecord struct {
	NextThread    string   `json:"next_thread"`
	SessionBodies int      `json:"session_bodies"`
	Key           string   `json:"key"`
	ThreadID      string   `json:"thread_id"`
	Kind          string   `json:"kind"`
	Table         string   `json:"table"`
	Bucket        *int64   `json:"bucket"`
	Body          string   `json:"body"`
	Fingerprint   string   `json:"fingerprint"`
	Rows          int      `json:"rows"`
	Complete      bool     `json:"complete"`
	Pending       int      `json:"pending"`
	Deferred      int      `json:"deferred"`
	SeenThreads   []string `json:"seen_threads"`
	MetadataRows  int      `json:"metadata_rows"`
	BodyRows      int      `json:"body_rows"`
	BodyBytes     int64    `json:"body_bytes"`
}

// CaptureCodexHistory takes a read-only native SQLite transaction. Stable ordinal
// packs bound raw custody growth; large rows have independent source keys. Cursor
// metadata lives on the existing source manifest and advances with its bodies.
func CaptureCodexHistory(ctx context.Context, homePath, family, root, database, metadataDatabase string, now time.Time) (CaptureBatchReport, error) {
	result := CaptureBatchReport{}
	if strings.TrimSpace(family) == "" {
		return result, fmt.Errorf("source family required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	path, err := NativeConversationDatabase(root, "codex-history", database)
	if err != nil {
		return result, err
	}
	metaPath := ""
	if metadataDatabase != "" {
		metaPath, err = NativeConversationDatabase(root, "codex-history", metadataDatabase)
		if err != nil {
			return result, err
		}
	}
	var metadataBefore os.FileInfo
	if metaPath != "" {
		metadataBefore, err = os.Stat(metaPath)
		if err != nil {
			return result, err
		}
	}
	before, err := os.Stat(path)
	if err != nil {
		return result, err
	}
	identity, err := nativeDatabaseFingerprint(path)
	if err != nil {
		return result, err
	}
	if metaPath != "" {
		metaID, e := nativeDatabaseFingerprint(metaPath)
		if e != nil {
			return result, e
		}
		identity = strings.TrimPrefix(hashBytes([]byte(identity+":"+metaID)), "sha256:")
	}
	namespace := "native:codex-history:" + filepath.ToSlash(path)
	inventoryKey := idFor("source_", family, namespace, "inventory")
	existing, err := LoadSourceManifest(homePath, family)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	current := map[string]SourceManifestItem{}
	previous := map[string]map[string]string{}
	states := map[string]json.RawMessage{}
	known := map[string]map[string][]int64{}
	threads := map[string]bool{}
	var checkpoint SourceManifestItem
	for _, item := range existing.Items {
		if item.SourceNamespace != namespace || item.VersionState == "superseded" {
			continue
		}
		if item.SourceKey == inventoryKey {
			checkpoint = item
			current["inventory"] = item
			continue
		}
		ref, e := url.Parse(item.SourceRef)
		if e != nil {
			return result, fmt.Errorf("Codex checkpoint provenance invalid")
		}
		key, sid := ref.Query().Get("view_key"), ref.Query().Get("thread")
		if key == "" || sid == "" {
			return result, fmt.Errorf("Codex checkpoint has no native identity")
		}
		current[key] = item
		previous[key] = map[string]string{"hash": item.FileHash, "fingerprint": item.SourceFingerprint}
		if strings.HasPrefix(key, "cursor/") {
			encoded := strings.TrimPrefix(item.SourceFingerprint, "codex-cursor-v1:")
			body, e := base64.RawURLEncoding.DecodeString(encoded)
			if e != nil || !json.Valid(body) {
				return result, fmt.Errorf("Codex cursor invalid")
			}
			states[sid] = body
			threads[sid] = true
		}
		if ref.Query().Get("view") == "raw_pack" {
			bucket, e := strconv.ParseInt(ref.Query().Get("bucket"), 10, 64)
			if e != nil {
				return result, fmt.Errorf("Codex pack cursor invalid")
			}
			if known[sid] == nil {
				known[sid] = map[string][]int64{}
			}
			table := ref.Query().Get("table")
			known[sid][table] = append(known[sid][table], bucket)
		}
	}
	epoch := now.UTC().Unix() / 86400
	nextThread := ""
	if strings.HasPrefix(checkpoint.SourceFingerprint, "codex-db-v2:") {
		encoded := checkpoint.SourceFingerprint[strings.LastIndex(checkpoint.SourceFingerprint, ":")+1:]
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return result, fmt.Errorf("Codex inventory rotation invalid")
		}
		nextThread = string(decoded)
	}
	expected := fmt.Sprintf("codex-db-v2:%s:%d:complete:", identity, epoch)
	if strings.HasPrefix(checkpoint.SourceFingerprint, expected) {
		result.Unchanged = len(threads)
		result.Missing = nativeMissingCount(checkpoint)
		result.SourceManifestReport = SourceManifestReport{ManifestID: existing.ID, ManifestPath: SourceManifestPath(homePath, family), AuthorityRef: SourceManifestAuthorityRef(family), ItemCount: len(existing.Items)}
		return result, ctx.Err()
	}
	request, _ := json.Marshal(map[string]any{"next_thread": nextThread, "database": path, "metadata_database": metaPath, "previous": previous, "states": states, "known_packs": known, "epoch": epoch})
	spool, err := newSourceManifestSnapshotBatch(128)
	if err != nil {
		return result, err
	}
	defer spool.Close()
	fragment := SourceManifest{SchemaVersion: SourceManifestSchemaVersion, ID: "srcman_" + safeManifestName(family), SourceFamily: family, AuthorityRef: SourceManifestAuthorityRef(family), Lifecycle: defaultSourceManifestLifecycle()}
	var metadataOnly []SourceManifestItem
	seenKeys := map[string]bool{}
	var complete codexHistoryRecord
	appendBody := func(row codexHistoryRecord) error {
		if row.Key == "" || seenKeys[row.Key] {
			return fmt.Errorf("Codex export repeats source identity")
		}
		seenKeys[row.Key] = true
		key := idFor("source_", family, namespace, row.Key)
		query := url.Values{"thread": {row.ThreadID}, "view_key": {row.Key}, "view": {row.Kind}, "table": {row.Table}}
		if row.Bucket != nil {
			query.Set("bucket", strconv.FormatInt(*row.Bucket, 10))
			query.Set("ordinal_start", strconv.FormatInt(*row.Bucket*512, 10))
			query.Set("ordinal_end", strconv.FormatInt((*row.Bucket+1)*512, 10))
		}
		if metaPath != "" {
			query.Set("metadata_database", filepath.ToSlash(metaPath))
		}
		sourcePath := path
		if row.Table == "threads" && metaPath != "" {
			sourcePath = metaPath
			query.Set("history_database", filepath.ToSlash(path))
		}
		ref := (&url.URL{Scheme: "sqlite", Path: filepath.ToSlash(sourcePath), RawQuery: query.Encode()}).String()
		hash := hashBytes([]byte(row.Body))
		if old, ok := current[row.Key]; ok && old.FileHash == hash {
			if old.SourceFingerprint != row.Fingerprint {
				old.SourceFingerprint = row.Fingerprint
				metadataOnly = append(metadataOnly, old)
			}
			result.Unchanged++
			return nil
		}
		temp, e := os.CreateTemp(spool.spoolDir, "codex-history-*")
		if e != nil {
			return e
		}
		_, writeErr := io.WriteString(temp, row.Body)
		closeErr := temp.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		spool.files = append(spool.files, sourceManifestFileSnapshot{path: ref, spoolPath: temp.Name(), fileHash: hash, bodyBytes: int64(len(row.Body)), modTime: now, mode: 0600})
		slug := ""
		if row.Kind == "semantic" || row.Kind == "semantic_context" {
			slug = "codex history conversation"
			result.Captured++
		} else if row.Kind != "inventory" {
			result.NativeRowsCaptured += row.Rows
			result.NativeRowBytesCaptured += int64(len(row.Body))
		}
		fragment.Items = append(fragment.Items, SourceManifestItem{VersionState: "current", ID: idFor("srcitem_", family, key, hash), SourceKey: key, SourceNamespace: namespace, SourceFingerprint: row.Fingerprint, SourceKind: "codex-history_transcript_v1", SourceRef: ref, FileHash: hash, SnapshotTime: now.UTC().Format(time.RFC3339Nano), Privacy: "private", BodyPolicy: "source_in_place", PageSlug: slug, EvidenceRefs: []string{"source:" + ref, hash}})
		return nil
	}
	err = readCodexHistoryRecords(ctx, request, func(row codexHistoryRecord) error {
		if row.Complete {
			complete = row
			return nil
		}
		if row.ThreadID == "" || row.Fingerprint == "" {
			return fmt.Errorf("Codex export missing native provenance")
		}
		switch row.Kind {
		case "raw_pack", "raw_row", "semantic", "semantic_context", "cursor":
		default:
			return fmt.Errorf("invalid Codex export kind")
		}
		if row.Rows < 0 || len(row.Body) > 256<<20 {
			return fmt.Errorf("Codex export exceeds bounded record")
		}
		return appendBody(row)
	})
	if err != nil {
		return result, err
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		return result, fmt.Errorf("Codex database replaced during read; retry")
	}
	if metaPath != "" {
		metadataAfter, e := os.Stat(metaPath)
		if e != nil || !os.SameFile(metadataBefore, metadataAfter) {
			return result, fmt.Errorf("Codex metadata database replaced during read; retry")
		}
	}
	seen := map[string]bool{}
	for _, sid := range complete.SeenThreads {
		seen[sid] = true
	}
	for sid := range threads {
		if !seen[sid] {
			result.Missing++
		}
	}
	result.Pending, result.Deferred = complete.Pending, complete.Deferred
	result.NativeMetadataRows, result.NativeBodyRows, result.NativeBodyBytes = complete.MetadataRows, complete.BodyRows, complete.BodyBytes
	result.NativeSessionBodies = complete.SessionBodies
	status := "complete"
	if result.Pending > 0 || result.Deferred > 0 {
		status = "pending"
	}
	inventoryBytes, _ := json.Marshal(map[string]any{"type": "native_inventory", "format": "codex-history", "missing_sessions": result.Missing, "pending": result.Pending, "deferred": result.Deferred, "next_thread": complete.NextThread})
	inventoryBody := string(inventoryBytes) + "\n"
	inventoryRow := codexHistoryRecord{Key: "inventory", Kind: "inventory", Table: "inventory", Body: inventoryBody, Fingerprint: fmt.Sprintf("codex-db-v2:%s:%d:%s:%s", identity, epoch, status, base64.RawURLEncoding.EncodeToString([]byte(complete.NextThread)))}
	// The inventory uses a stable family key, not a fictitious native thread.
	if err := appendBody(inventoryRow); err != nil {
		return result, err
	}
	for i := range fragment.Items {
		if fragment.Items[i].SourceKey == inventoryKey {
			fragment.Items[i].StructureOverlays = []SourceStructureOverlay{{Kind: "native_missing_sessions", Count: result.Missing}}
		}
	}
	for i := range metadataOnly {
		if metadataOnly[i].SourceKey == inventoryKey {
			metadataOnly[i].StructureOverlays = []SourceStructureOverlay{{Kind: "native_missing_sessions", Count: result.Missing}}
		}
	}

	var report SourceManifestReport
	err = withSourceManifestAuthorityLock(homePath, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		latest, e := LoadSourceManifest(homePath, family)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		byKey := map[string]SourceManifestItem{}
		var latestCheckpoint SourceManifestItem
		for _, item := range latest.Items {
			if item.VersionState != "superseded" {
				byKey[item.SourceKey] = item
				if item.SourceKey == inventoryKey {
					latestCheckpoint = item
				}
			}
		}
		if latestCheckpoint.ID != checkpoint.ID || latestCheckpoint.SourceFingerprint != checkpoint.SourceFingerprint {
			return fmt.Errorf("Codex checkpoint advanced concurrently; retry")
		}
		for _, candidate := range append(append([]SourceManifestItem(nil), fragment.Items...), metadataOnly...) {
			if candidate.SourceKey == inventoryKey {
				continue
			}
			ref, _ := url.Parse(candidate.SourceRef)
			old, had := current[ref.Query().Get("view_key")]
			actual, has := byKey[candidate.SourceKey]
			if had != has || had && (old.ID != actual.ID || old.SourceFingerprint != actual.SourceFingerprint) {
				return fmt.Errorf("Codex source checkpoint advanced concurrently; retry")
			}
		}
		owned, e := captureOwnedSourceManifest(homePath, fragment, spool.files, nil)
		if e != nil {
			return e
		}
		owned.Items = append(owned.Items, metadataOnly...)
		for i := range owned.Items {
			item := &owned.Items[i]
			if prior, ok := byKey[item.SourceKey]; ok {
				if prior.FileHash == item.FileHash {
					item.ID = prior.ID
				} else {
					item.ID = idFor("srcitem_", family, item.SourceKey, item.FileHash, prior.ID)
				}
			}
		}
		report, e = recordSourceManifestUnlocked(homePath, owned)
		return e
	})
	result.SourceManifestReport = report
	return result, err
}

var readCodexHistoryRecords = readCodexHistory

func readCodexHistory(ctx context.Context, request []byte, consume func(codexHistoryRecord) error) error {
	python, err := exec.LookPath("python3")
	if err != nil {
		return fmt.Errorf("Codex history adapter requires python3 sqlite3")
	}
	cmd := exec.CommandContext(ctx, python, "-I", "-c", codexHistoryReader)
	cmd.Stdin = bytes.NewReader(request)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	decoder, complete := json.NewDecoder(stdout), false
	var readErr error
	for {
		var row codexHistoryRecord
		err := decoder.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			readErr = fmt.Errorf("invalid Codex history export")
			break
		}
		if complete {
			readErr = fmt.Errorf("trailing Codex history export")
			break
		}
		if err := consume(row); err != nil {
			readErr = err
			break
		}
		complete = row.Complete
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 512 {
			detail = "reader failed"
		}
		return fmt.Errorf("Codex history unavailable or unsupported: %s: %w", detail, waitErr)
	}
	if !complete {
		return fmt.Errorf("Codex history interrupted before checkpoint")
	}
	return nil
}
