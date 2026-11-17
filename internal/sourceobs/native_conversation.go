package sourceobs

import (
	"bytes"
	"context"
	_ "embed"
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

//go:embed native_conversation.py
var nativeConversationReader string

// NativeConversationDatabase resolves a configured file below an approved
// directory. Account paths and native source discovery are never implicit.
func NativeConversationDatabase(root, format, database string) (string, error) {
	if format != "opencode" && format != "hermes" && format != "codex-history" {
		return "", fmt.Errorf("unsupported native conversation format")
	}
	if database == "" {
		database = "opencode.db"
		if format == "codex-history" {
			database = "thread_history_1.sqlite"
		}
		if format == "hermes" {
			database = "state.db"
		}
	}
	clean := filepath.Clean(database)
	if !filepath.IsAbs(root) || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("native database must be a relative file beneath the approved root")
	}
	path := filepath.Clean(root)
	for current := path; ; current = filepath.Dir(current) {
		st, err := os.Lstat(current)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("native source root unavailable or linked")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		path = filepath.Join(path, component)
		st, err := os.Lstat(path)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("native database unavailable or linked")
		}
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("native database must be a regular file")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("native database sidecar unavailable or linked")
		}
	}
	return path, nil
}

func nativeDatabaseFingerprint(path string) (string, error) {
	var parts []string
	for _, suffix := range []string{"", "-wal"} {
		st, err := os.Lstat(path + suffix)
		if os.IsNotExist(err) && suffix != "" {
			parts = append(parts, "absent")
			continue
		}
		if err != nil || !st.Mode().IsRegular() {
			return "", fmt.Errorf("native database identity unavailable")
		}
		parts = append(parts, sourceFingerprint(st))
	}
	return strings.TrimPrefix(hashBytes([]byte(strings.Join(parts, ":"))), "sha256:"), nil
}

type nativeSessionRecord struct {
	Kind         string  `json:"kind"`
	Table        string  `json:"table"`
	RowID        string  `json:"row_id"`
	SessionID    string  `json:"session_id"`
	Fingerprint  string  `json:"fingerprint"`
	Body         *string `json:"body"`
	Complete     bool    `json:"complete"`
	Pending      int     `json:"pending"`
	MetadataRows int     `json:"metadata_rows"`
	BodyRows     int     `json:"body_rows"`
	BodyBytes    int64   `json:"body_bytes"`
}

// CaptureNativeConversations takes one SQLite read transaction. Native metadata
// selects changed sessions; daily bounded full-row reconciliation catches edits
// with unchanged revisions. Per-session fingerprints are durable manifest cursors.
// Raw row versions are separately content-addressed custody-only items. A session
// projection is published only when its semantic bytes change.
// Missing sessions preserve owned current recall: absence does not invalidate
// past evidence. The raw original database and its independent backup stay intact.
func CaptureNativeConversations(ctx context.Context, homePath, family, root, format, database string, now time.Time) (CaptureBatchReport, error) {
	result := CaptureBatchReport{}
	if strings.TrimSpace(family) == "" {
		return result, fmt.Errorf("source family required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	path, err := NativeConversationDatabase(root, format, database)
	if err != nil {
		return result, err
	}
	before, err := os.Stat(path)
	if err != nil {
		return result, err
	}
	identity, err := nativeDatabaseFingerprint(path)
	if err != nil {
		return result, err
	}
	namespace := "native:" + format + ":" + filepath.ToSlash(path)
	existing, loadErr := LoadSourceManifest(homePath, family)
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return result, loadErr
	}
	current, previous := map[string]SourceManifestItem{}, map[string]string{}
	rowFingerprints := map[string]map[string]string{}
	projectionHashes := map[string]string{}
	expectedItems := map[string]SourceManifestItem{}
	inventoryKey := idFor("source_", family, namespace, "inventory")
	var checkpoint SourceManifestItem
	for _, item := range existing.Items {
		if item.SourceNamespace != namespace || item.VersionState == "superseded" {
			continue
		}
		if item.SourceKey == inventoryKey {
			checkpoint = item
			continue
		}
		parsed, parseErr := url.Parse(item.SourceRef)
		if parseErr != nil || parsed.Query().Get("session") == "" {
			return result, fmt.Errorf("native checkpoint has no session provenance")
		}
		sid := parsed.Query().Get("session")
		expectedItems[item.SourceKey] = item
		if rid := parsed.Query().Get("row"); rid != "" {
			table := parsed.Query().Get("table")
			if table == "" {
				return result, fmt.Errorf("native row checkpoint has no table provenance")
			}
			if rowFingerprints[sid] == nil {
				rowFingerprints[sid] = map[string]string{}
			}
			rowFingerprints[sid][table+"\x1f"+rid] = item.SourceFingerprint
			continue
		}
		current[sid], previous[sid] = item, item.SourceFingerprint
		projectionHashes[sid] = item.FileHash
	}
	epoch, previousEpoch := now.UTC().Unix()/86400, int64(-1)
	parts := strings.Split(checkpoint.SourceFingerprint, ":")
	if len(parts) == 3 && parts[0] == "native-db-v3" {
		previousEpoch, _ = strconv.ParseInt(parts[2], 10, 64)
	}
	if previousEpoch == epoch && len(parts) == 3 && parts[1] == identity {
		result.Unchanged, result.Missing = len(current), nativeMissingCount(checkpoint)
		result.SourceManifestReport = SourceManifestReport{ManifestID: existing.ID, ManifestPath: SourceManifestPath(homePath, family), AuthorityRef: SourceManifestAuthorityRef(family), ItemCount: len(existing.Items)}
		return result, ctx.Err()
	}
	request, _ := json.Marshal(map[string]any{"database": path, "format": format, "previous": previous, "rows": rowFingerprints, "projection_hashes": projectionHashes, "reconcile": previousEpoch != epoch, "reconcile_epoch": epoch})
	spool, err := newSourceManifestSnapshotBatch(128)
	if err != nil {
		return result, err
	}
	defer spool.Close()
	fragment := SourceManifest{SchemaVersion: SourceManifestSchemaVersion, ID: "srcman_" + safeManifestName(family), SourceFamily: family, AuthorityRef: SourceManifestAuthorityRef(family), Lifecycle: defaultSourceManifestLifecycle()}
	appendBody := func(sid, fingerprint, body string, inventory bool, table, rowID string) error {
		tables := "session,message,part,session_v2,session_message,session_part"
		if format == "hermes" {
			tables = "sessions,messages"
		}
		if inventory {
			tables = "inventory"
		}
		query := url.Values{"session": {sid}, "tables": {tables}}
		key := idFor("source_", family, namespace, "session", sid)
		if inventory {
			key = inventoryKey
		}
		if rowID != "" {
			query = url.Values{"session": {sid}, "table": {table}, "row": {rowID}}
			key = idFor("source_", family, namespace, "row", sid, table, rowID)
		}
		ref := (&url.URL{Scheme: "sqlite", Path: filepath.ToSlash(path), RawQuery: query.Encode()}).String()
		hash := hashBytes([]byte(body))
		temp, err := os.CreateTemp(spool.spoolDir, "native-session-*")
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(temp, body)
		closeErr := temp.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		spool.files = append(spool.files, sourceManifestFileSnapshot{path: ref, spoolPath: temp.Name(), fileHash: hash, bodyBytes: int64(len(body)), modTime: now, mode: 0600})
		pageSlug := ""
		if !inventory && rowID == "" {
			pageSlug = format + " conversation"
		}
		fragment.Items = append(fragment.Items, SourceManifestItem{PageSlug: pageSlug, ID: idFor("srcitem_", family, key, hash), SourceKey: key, SourceNamespace: namespace, SourceFingerprint: fingerprint, SourceKind: format + "_transcript_v1", SourceRef: ref, FileHash: hash, SnapshotTime: now.UTC().Format(time.RFC3339Nano), Privacy: "private", BodyPolicy: "source_in_place", EvidenceRefs: []string{"source:" + ref, hash}})
		return nil
	}
	metadataOnly, seen := []SourceManifestItem{}, map[string]bool{}
	seenRows := map[string]bool{}
	err = readNativeSessions(ctx, request, func(row nativeSessionRecord) error {
		if row.Complete {
			result.Pending = row.Pending
			return nil
		}
		if row.SessionID == "" || seen[row.SessionID] {
			return fmt.Errorf("invalid native session inventory")
		}
		if row.Kind == "row" {
			if err := validateNativeRowIdentity(format, row.Table, row.RowID); err != nil {
				return err
			}
			key := idFor("source_", family, namespace, "row", row.SessionID, row.Table, row.RowID)
			if seenRows[key] || row.Body == nil || row.Fingerprint != "native-row-v3:"+strings.TrimPrefix(hashBytes([]byte(*row.Body)), "sha256:") {
				return fmt.Errorf("invalid native row export")
			}
			seenRows[key] = true
			if err := appendBody(row.SessionID, row.Fingerprint, *row.Body, false, row.Table, row.RowID); err != nil {
				return err
			}
			result.NativeRowsCaptured++
			result.NativeRowBytesCaptured += int64(len(*row.Body))
			return nil
		}
		if row.Kind != "" {
			return fmt.Errorf("unsupported native export record")
		}
		seen[row.SessionID] = true
		result.NativeMetadataRows += row.MetadataRows
		result.NativeBodyRows += row.BodyRows
		result.NativeBodyBytes += row.BodyBytes
		if row.BodyRows > 0 {
			result.NativeSessionBodies++
		}
		if row.Body != nil {
			if row.Fingerprint == "" {
				return fmt.Errorf("native session fingerprint missing")
			}
			if err := appendBody(row.SessionID, row.Fingerprint, *row.Body, false, "", ""); err != nil {
				return err
			}
			result.Captured++
		} else if row.Fingerprint != "" {
			old, ok := current[row.SessionID]
			if !ok {
				return fmt.Errorf("native checkpoint body missing")
			}
			old.SourceFingerprint = row.Fingerprint
			metadataOnly = append(metadataOnly, old)
			result.Unchanged++
		} else if _, ok := current[row.SessionID]; ok {
			result.Unchanged++
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		return result, fmt.Errorf("native database replaced during read; retry")
	}
	for sid := range current {
		if !seen[sid] {
			result.Missing++
		}
	}
	if result.Pending == 0 {
		// A commit during capture changes this BEFORE-transaction identity, so
		// the following refresh rechecks it rather than skipping new rows.
		body := fmt.Sprintf("{\"type\":\"native_inventory\",\"missing_sessions\":%d}\n", result.Missing)
		if err := appendBody("", fmt.Sprintf("native-db-v3:%s:%d", identity, epoch), body, true, "", ""); err != nil {
			return result, err
		}
		fragment.Items[len(fragment.Items)-1].StructureOverlays = []SourceStructureOverlay{{Kind: "native_missing_sessions", Count: result.Missing}}
	}
	var report SourceManifestReport
	err = withSourceManifestAuthorityLock(homePath, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		latest, loadErr := LoadSourceManifest(homePath, family)
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return loadErr
		}
		var latestCheckpoint SourceManifestItem
		byKey := map[string]SourceManifestItem{}
		for _, item := range latest.Items {
			if item.VersionState != "superseded" {
				byKey[item.SourceKey] = item
				if item.SourceKey == inventoryKey {
					latestCheckpoint = item
				}
			}
		}
		if latestCheckpoint.SourceFingerprint != checkpoint.SourceFingerprint {
			return fmt.Errorf("native checkpoint advanced concurrently; retry")
		}
		for _, candidate := range append(append([]SourceManifestItem(nil), fragment.Items...), metadataOnly...) {
			if candidate.SourceKey == inventoryKey {
				continue
			}
			expected, had := expectedItems[candidate.SourceKey]
			actual, exists := byKey[candidate.SourceKey]
			if had != exists || had && (actual.ID != expected.ID || actual.SourceFingerprint != expected.SourceFingerprint) {
				return fmt.Errorf("native session checkpoint advanced concurrently; retry")
			}
		}
		owned, err := captureOwnedSourceManifest(homePath, fragment, spool.files, nil)
		if err != nil {
			return err
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
		report, err = recordSourceManifestUnlocked(homePath, owned)
		return err
	})
	result.SourceManifestReport = report
	return result, err
}

func nativeMissingCount(item SourceManifestItem) int {
	for _, overlay := range item.StructureOverlays {
		if overlay.Kind == "native_missing_sessions" {
			return overlay.Count
		}
	}
	return 0
}

func readNativeSessions(ctx context.Context, request []byte, consume func(nativeSessionRecord) error) error {
	python, err := exec.LookPath("python3")
	if err != nil {
		return fmt.Errorf("native SQLite adapter requires python3 with the sqlite3 standard library")
	}
	cmd := exec.CommandContext(ctx, python, "-I", "-c", nativeConversationReader)
	cmd.Stdin = bytes.NewReader(request)
	var stderr bytes.Buffer
	cmd.Stderr, cmd.WaitDelay = &stderr, time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("native SQLite reader could not start: %w", err)
	}
	decoder, complete := json.NewDecoder(stdout), false
	var readErr error
	for {
		var row nativeSessionRecord
		err := decoder.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			readErr = fmt.Errorf("invalid native SQLite reader output")
			break
		}
		if complete {
			readErr = fmt.Errorf("native inventory has trailing records")
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 512 {
			detail = "native SQLite reader failed"
		}
		return fmt.Errorf("native SQLite reader unavailable or unsupported: %s: %w", detail, waitErr)
	}
	if !complete {
		return fmt.Errorf("native SQLite inventory interrupted before completion")
	}
	return nil
}

// The source adapter uses actual native PKs, not invented row entities. A row
// snapshot stays a typed non-message record, so raw tools/reasoning never embed.
func validateNativeRowIdentity(format, table, rowID string) error {
	allowed := map[string]bool{"sessions": true, "messages": true}
	if format == "opencode" {
		allowed = map[string]bool{"session": true, "message": true, "part": true, "session_v2": true, "session_message": true, "session_part": true}
	}
	var parts []json.RawMessage
	want := 1
	if table == "session_part" {
		want = 2
	}
	if !allowed[table] || json.Unmarshal([]byte(rowID), &parts) != nil || len(parts) != want {
		return fmt.Errorf("invalid native row primary-key provenance")
	}
	return nil
}
