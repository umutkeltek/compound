package sourceobs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const codexHistorySchema = `
CREATE TABLE thread_history_projection_state(thread_id TEXT PRIMARY KEY,next_rollout_byte_offset INTEGER NOT NULL,next_rollout_ordinal INTEGER NOT NULL);
CREATE TABLE thread_turns(thread_id TEXT NOT NULL,turn_id TEXT NOT NULL,rollout_ordinal INTEGER NOT NULL,status TEXT NOT NULL,error_json TEXT,started_at INTEGER,completed_at INTEGER,duration_ms INTEGER,first_user_item_id TEXT,final_agent_item_id TEXT,rollout_byte_offset INTEGER,rollout_end_ordinal INTEGER,rollout_end_byte_offset INTEGER,root_turn_id TEXT,PRIMARY KEY(thread_id,turn_id));
CREATE UNIQUE INDEX idx_thread_turns_page ON thread_turns(thread_id,rollout_ordinal);
CREATE TABLE thread_items(thread_id TEXT NOT NULL,turn_id TEXT NOT NULL,item_id TEXT NOT NULL,rollout_ordinal INTEGER NOT NULL,created_at_ms INTEGER NOT NULL,item_json TEXT NOT NULL,item_type TEXT NOT NULL DEFAULT '',updated_at_ordinal INTEGER NOT NULL DEFAULT 0,started_at_ms INTEGER,completed_at_ms INTEGER,extra_native TEXT,PRIMARY KEY(thread_id,turn_id,item_id));
CREATE UNIQUE INDEX idx_thread_items_page ON thread_items(thread_id,rollout_ordinal);
CREATE INDEX idx_thread_items_updated_page ON thread_items(thread_id,updated_at_ordinal);
CREATE TABLE thread_realtime_items(thread_id TEXT NOT NULL,item_id TEXT NOT NULL,rollout_ordinal INTEGER NOT NULL,created_at_ms INTEGER NOT NULL,item_type TEXT NOT NULL,item_json TEXT NOT NULL,PRIMARY KEY(thread_id,item_id));
CREATE UNIQUE INDEX idx_thread_realtime_items_page ON thread_realtime_items(thread_id,rollout_ordinal);
INSERT INTO thread_history_projection_state VALUES('thread-a',500,20);
INSERT INTO thread_turns(thread_id,turn_id,rollout_ordinal,status,error_json,rollout_end_ordinal) VALUES('thread-a','turn-a',0,'completed','{"future":{"native":true}}',20);
INSERT INTO thread_items VALUES('thread-a','turn-a','user-native',1,1000,'{ "type":"userMessage", "id":"user-native","clientId":"client-a", "content":[{"type":"text","text":"Cedar archive plan"}] }','userMessage',1,NULL,NULL,'future preserved');
INSERT INTO thread_items VALUES('thread-a','turn-a','agent-native',2,1001,'{"type":"agentMessage","id":"agent-native","text":"Keep the cedar archive","phase":"final","memoryCitation":{"source":"fixture"},"delivery":"direct","questions":[]}','agentMessage',2,NULL,1002,'future preserved');
`

func captureCodex(t *testing.T, hp, root, meta string, now time.Time) CaptureBatchReport {
	t.Helper()
	r, err := CaptureCodexHistory(context.Background(), hp, "codex_history_fixture", root, "", meta, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func codexManifest(t *testing.T, hp string) SourceManifest {
	t.Helper()
	m, err := LoadSourceManifest(hp, "codex_history_fixture")
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func codexCurrentBody(t *testing.T, hp, key string) string {
	t.Helper()
	for _, item := range codexManifest(t, hp).Items {
		ref, err := url.Parse(item.SourceRef)
		if err != nil {
			t.Fatal(err)
		}
		if item.VersionState != "superseded" && ref.Query().Get("view_key") == key {
			return nativeBody(t, hp, item)
		}
	}
	t.Fatalf("missing Codex view %s", key)
	return ""
}
func TestCodexHistoryPacksPreserveExactRowsAndBoundToolAppend(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema+`
INSERT INTO thread_items VALUES('thread-a','turn-a','large-tool',3,1002,'{"type":"toolResult","id":"large-tool","output":"'||replace(hex(zeroblob(1048576)),'00','x')||'"}','toolResult',3,1000,1002,'all fields retained');
INSERT INTO thread_items VALUES('thread-a','turn-a','reasoning',4,1003,'{"type":"reasoning","text":"Hidden reasoning sentinel"}','reasoning',4,NULL,NULL,'private');
`)
	nativeSQL(t, filepath.Join(root, "state_5.sqlite"), `CREATE TABLE threads(id TEXT PRIMARY KEY,cwd TEXT,title TEXT,model TEXT,source TEXT,updated_at_ms INTEGER,history_mode TEXT,creator_account_id TEXT,unknown_blob BLOB);INSERT INTO threads VALUES('thread-a','/workspace/fixture','Cedar archive','fixture-model','cli',1,'paginated','synthetic-account',X'0001ff');`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	first := captureCodex(t, hp, root, "state_5.sqlite", now)
	if first.Pending != 0 || first.Captured != 2 || first.NativeRowsCaptured < 6 {
		t.Fatalf("initial pack capture: %+v", first)
	}
	if got := len(codexManifest(t, hp).Items); got > 12 {
		t.Fatalf("per-row source explosion: %d", got)
	}
	large := codexCurrentBody(t, hp, `large/thread-a/thread_items/["turn-a","large-tool"]`)
	if !strings.Contains(large, strings.Repeat("x", 1048576)) || !strings.Contains(large, `"extra_native":"all fields retained"`) {
		t.Fatal("large native row lost fields or exact payload")
	}
	raw := codexCurrentBody(t, hp, "pack/thread-a/thread_items/0")
	if strings.Contains(raw, strings.Repeat("x", 65536)) || !strings.Contains(raw, `"native_row_key"`) || !strings.Contains(raw, `{ \"type\":\"userMessage\"`) {
		t.Fatal("raw pack did not preserve original item_json or separate large row")
	}
	cursor := codexCurrentBody(t, hp, "cursor/thread-a")
	if !strings.Contains(cursor, `"creator_account_id":"synthetic-account"`) || !strings.Contains(cursor, `"base64":"AAH/"`) {
		t.Fatal("optional native metadata was not lossless")
	}
	before, files := nativeArtifactTotals(t, hp)
	unchanged := captureCodex(t, hp, root, "state_5.sqlite", now)
	after, filesAfter := nativeArtifactTotals(t, hp)
	if unchanged.NativeBodyRows != 0 || before != after || files != filesAfter {
		t.Fatalf("unchanged DB/WAL read or wrote bodies: %+v %d %d", unchanged, before, after)
	}
	semantic := codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0")
	nativeSQL(t, db, `INSERT INTO thread_items VALUES('thread-a','turn-a','tool-next',5,1004,'{"type":"toolResult","output":"small tool append"}','toolResult',5,NULL,NULL,'future');UPDATE thread_history_projection_state SET next_rollout_ordinal=21;`)
	appended := captureCodex(t, hp, root, "state_5.sqlite", now)
	appendedBytes, appendedFiles := nativeArtifactTotals(t, hp)
	if appended.Captured != 0 || appended.NativeBodyBytes > 32<<10 || appendedBytes-after > 32<<10 || appendedFiles-filesAfter > 4 {
		t.Fatalf("tool append recopied large or semantic body: %+v bytes=%d files=%d", appended, appendedBytes-after, appendedFiles-filesAfter)
	}
	if semantic != codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0") {
		t.Fatal("tool append renumbered semantic native IDs")
	}
	nativeSQL(t, db, `UPDATE thread_items SET item_json='{"type":"agentMessage","id":"agent-native","text":"Revised cedar archive","phase":"final"}',updated_at_ordinal=30 WHERE item_id='agent-native';UPDATE thread_history_projection_state SET next_rollout_ordinal=31;`)
	changed := captureCodex(t, hp, root, "state_5.sqlite", now)
	if changed.Captured != 1 || !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "Revised cedar archive") {
		t.Fatalf("native earlier-row update lost: %+v", changed)
	}
}

func TestCodexHistoryScaleBackfillAndDailyReconcileResume(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema+`WITH RECURSIVE n(x) AS (VALUES(10) UNION ALL SELECT x+1 FROM n WHERE x<20489) INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal) SELECT 'thread-a','turn-a','tool-'||x,x,x,'{"type":"toolResult","output":"small native tool"}','toolResult',x FROM n; UPDATE thread_history_projection_state SET next_rollout_ordinal=20490;`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	first := captureCodex(t, hp, root, "", now)
	if first.Pending == 0 || first.NativeBodyRows > 32*512+8 {
		t.Fatalf("backfill was not bounded: %+v", first)
	}
	second := captureCodex(t, hp, root, "", now)
	if second.Pending != 0 || second.NativeBodyRows > 10*512+8 {
		t.Fatalf("backfill did not resume: %+v", second)
	}
	if count := len(codexManifest(t, hp).Items); count > 100 {
		t.Fatalf("20482 native rows became %d manifest entries", count)
	}
	before, files := nativeArtifactTotals(t, hp)
	steady := captureCodex(t, hp, root, "", now)
	if steady.NativeBodyRows != 0 {
		t.Fatal("unchanged full backfill reread bodies")
	}
	// Same-width, same-update-ordinal changes require body reconciliation.
	nativeSQL(t, db, `UPDATE thread_items SET item_json='{"type":"userMessage","id":"user-native","content":[{"type":"text","text":"Maple archive plan"}]}' WHERE item_id='user-native';`)
	daily := captureCodex(t, hp, root, "", now.Add(24*time.Hour))
	if daily.Pending == 0 || daily.NativeBodyRows > 32*512+8 {
		t.Fatalf("daily full reconcile was not resumable: %+v", daily)
	}
	done := captureCodex(t, hp, root, "", now.Add(24*time.Hour))
	if done.Pending != 0 || !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "Maple archive plan") {
		t.Fatalf("daily body change was lost: %+v", done)
	}
	after, filesAfter := nativeArtifactTotals(t, hp)
	if after-before > 1<<20 || filesAfter-files > 6 {
		t.Fatalf("unchanged reconciliation duplicated raw history: bytes=%d files=%d", after-before, filesAfter-files)
	}
}

func TestCodexHistoryInterruptedPublicationAndDeletesPreserveCustody(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	captureCodex(t, hp, root, "", now)
	old, err := os.ReadFile(SourceManifestPath(hp, "codex_history_fixture"))
	if err != nil {
		t.Fatal(err)
	}
	nativeSQL(t, db, `UPDATE thread_items SET item_json='{"type":"agentMessage","text":"Interrupted new archive text"}',updated_at_ordinal=50 WHERE item_id='agent-native';`)
	prior := readCodexHistoryRecords
	readCodexHistoryRecords = func(ctx context.Context, request []byte, consume func(codexHistoryRecord) error) error {
		return prior(ctx, request, func(row codexHistoryRecord) error {
			if row.Complete {
				return errors.New("injected interruption before durable checkpoint")
			}
			return consume(row)
		})
	}
	_, err = CaptureCodexHistory(context.Background(), hp, "codex_history_fixture", root, "", "", now)
	readCodexHistoryRecords = prior
	if err == nil {
		t.Fatal("interrupted export published")
	}
	after, err := os.ReadFile(SourceManifestPath(hp, "codex_history_fixture"))
	if err != nil || string(after) != string(old) {
		t.Fatal("interruption advanced durable cursor")
	}
	captureCodex(t, hp, root, "", now)
	if !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "Interrupted new archive text") {
		t.Fatal("retry skipped interrupted native change")
	}
	nativeSQL(t, db, `DELETE FROM thread_items WHERE item_id='user-native';`)
	captureCodex(t, hp, root, "", now.Add(24*time.Hour))
	if strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "Cedar archive plan") {
		t.Fatal("deleted native item remained in current semantic view")
	}
	retained := false
	for _, item := range codexManifest(t, hp).Items {
		if item.VersionState == "superseded" && strings.Contains(nativeBody(t, hp, item), "Cedar archive plan") {
			retained = true
		}
	}
	if !retained {
		t.Fatal("native row deletion destroyed historical owned evidence")
	}
	nativeSQL(t, db, `DELETE FROM thread_history_projection_state WHERE thread_id='thread-a';`)
	missing := captureCodex(t, hp, root, "", now.Add(24*time.Hour))
	if missing.Missing != 1 || codexCurrentBody(t, hp, "cursor/thread-a") == "" {
		t.Fatalf("thread disappearance erased custody: %+v", missing)
	}
}

func TestCodexHistoryMirrorsShareBodiesAndRejectUnsafeMetadataPaths(t *testing.T) {
	hp, a, b := t.TempDir(), nativeRoot(t), nativeRoot(t)
	path := filepath.Join(a, "thread_history_1.sqlite")
	nativeSQL(t, path, codexHistorySchema)
	now := time.Now().UTC()
	captureCodex(t, hp, a, "", now)
	before, files := nativeArtifactTotals(t, hp)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(b, "thread_history_1.sqlite"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	captureCodex(t, hp, b, "", now)
	after, filesAfter := nativeArtifactTotals(t, hp)
	if before != after || files != filesAfter {
		t.Fatalf("mirrored source duplicated owned bodies: %d/%d %d/%d", before, after, files, filesAfter)
	}
	_, err = CaptureCodexHistory(context.Background(), hp, "codex_history_fixture", a, "", "../state.sqlite", now)
	if err == nil {
		t.Fatal("metadata path escaped approved root")
	}
	m := codexManifest(t, hp)
	aliases := map[string]bool{}
	for _, item := range m.Items {
		if item.PageSlug != "" {
			ref, _ := url.Parse(item.SourceRef)
			aliases[ref.Path] = true
		}
	}
	if len(aliases) != 2 {
		t.Fatal(fmt.Sprintf("mirror source attribution lost: %+v", aliases))
	}
}
