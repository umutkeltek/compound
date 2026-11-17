package sourceobs

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
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
	cursor := codexCurrentBody(t, hp, "metadata/thread-a")
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
	// A due reconcile verifies the large row even if an append selected its
	// range earlier in this capture.
	nativeSQL(t, db, `UPDATE thread_items SET item_json=replace(item_json,'xxx','yyy') WHERE item_id='large-tool'; INSERT INTO thread_items VALUES('thread-a','turn-a','tool-daily',6,1005,'{"type":"toolResult","output":"daily append"}','toolResult',6,NULL,NULL,'future'); UPDATE thread_history_projection_state SET next_rollout_ordinal=32;`)
	captureCodex(t, hp, root, "state_5.sqlite", now.Add(24*time.Hour))
	reconciled := codexCurrentBody(t, hp, `large/thread-a/thread_items/["turn-a","large-tool"]`)
	if !strings.Contains(reconciled, strings.Repeat("y", 10000)) || !strings.Contains(codexCurrentBody(t, hp, "pack/thread-a/thread_items/0"), hashBytes([]byte(reconciled))) {
		t.Fatal("append shortcut skipped due large-row reconciliation")
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
	if second.Pending != 0 || second.NativeBodyRows > 11*512+8 {
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

func TestCodexHistoryConcurrentCaptureCannotOverwriteNewGeneration(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema)
	now := time.Now().UTC()
	captureCodex(t, hp, root, "", now)
	nativeSQL(t, db, `UPDATE thread_items SET item_json='{"type":"agentMessage","text":"Concurrent archive generation"}',updated_at_ordinal=60 WHERE item_id='agent-native';`)
	prior := readCodexHistoryRecords
	defer func() { readCodexHistoryRecords = prior }()
	var winner []byte
	triggered := false
	readCodexHistoryRecords = func(ctx context.Context, request []byte, consume func(codexHistoryRecord) error) error {
		return prior(ctx, request, func(row codexHistoryRecord) error {
			if row.Complete && !triggered {
				triggered = true
				readCodexHistoryRecords = prior
				if _, err := CaptureCodexHistory(ctx, hp, "codex_history_fixture", root, "", "", now); err != nil {
					return err
				}
				var err error
				winner, err = os.ReadFile(SourceManifestPath(hp, "codex_history_fixture"))
				if err != nil {
					return err
				}
			}
			return consume(row)
		})
	}
	_, err := CaptureCodexHistory(context.Background(), hp, "codex_history_fixture", root, "", "", now)
	if err == nil || !strings.Contains(err.Error(), "advanced concurrently") {
		t.Fatalf("stale capture manufactured a new generation: %v", err)
	}
	after, err := os.ReadFile(SourceManifestPath(hp, "codex_history_fixture"))
	if err != nil || string(after) != string(winner) {
		t.Fatal("stale source publication overwrote winner")
	}
	if !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "Concurrent archive generation") {
		t.Fatal("winning native projection unavailable")
	}
}

func TestCodexHistoryWALCommitIsObservedWithoutNativeWrites(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	script := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
c.execute("PRAGMA journal_mode=WAL")
c.execute("PRAGMA wal_autocheckpoint=0")
c.execute("BEGIN")
c.execute("""INSERT INTO thread_items VALUES('thread-a','turn-a','wal-user',5,1005,'{"type":"userMessage","id":"wal-user","content":[{"type":"text","text":"WAL-only visible archive"}]}','userMessage',5,NULL,NULL,'native')""")
c.execute("UPDATE thread_history_projection_state SET next_rollout_ordinal=25")
print("uncommitted",flush=True)
sys.stdin.readline()
c.commit()
print("committed",flush=True)
sys.stdin.readline()
c.close()
`
	cmd := exec.CommandContext(ctx, python, "-I", "-u", "-c", script, db)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { fmt.Fprintln(stdin, "finish"); stdin.Close(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "uncommitted" {
		t.Fatal("native WAL fixture did not start")
	}
	now := time.Now().UTC()
	readHash := func(path string) string {
		t.Helper()
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return hashBytes(raw)
	}
	mainBefore, walBefore := readHash(db), readHash(db+"-wal")
	captureCodex(t, hp, root, "", now)
	if readHash(db) != mainBefore || readHash(db+"-wal") != walBefore {
		t.Fatal("read-only capture changed native DB/WAL")
	}
	if strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "WAL-only") {
		t.Fatal("uncommitted native rows entered custody")
	}
	fmt.Fprintln(stdin, "commit")
	if !scanner.Scan() || scanner.Text() != "committed" {
		t.Fatal("native WAL fixture did not commit")
	}
	if readHash(db) != mainBefore || readHash(db+"-wal") == walBefore {
		t.Fatal("fixture did not isolate a WAL-only commit")
	}
	walCommitted := readHash(db + "-wal")
	captured := captureCodex(t, hp, root, "", now)
	if captured.Captured != 1 || !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/0"), "WAL-only visible archive") {
		t.Fatalf("WAL-only change was skipped: %+v", captured)
	}
	if readHash(db) != mainBefore || readHash(db+"-wal") != walCommitted {
		t.Fatal("WAL-only capture wrote the source")
	}
	steady := captureCodex(t, hp, root, "", now)
	if steady.NativeBodyRows != 0 {
		t.Fatal("unchanged active WAL reread bodies")
	}
}

func TestCodexHistoryMalformedNativeTextDoesNotEchoPayload(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema)
	secret := "SyntheticPrivateNativeMarker"
	raw := append([]byte{0xff}, []byte(secret)...)
	nativeSQL(t, db, "UPDATE thread_items SET item_json=CAST(X'"+hex.EncodeToString(raw)+"' AS TEXT) WHERE item_id='user-native';")
	_, err := CaptureCodexHistory(context.Background(), hp, "codex_history_fixture", root, "", "", time.Now())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("native error leaked row data: %v", err)
	}
	if _, err := os.Stat(SourceManifestPath(hp, "codex_history_fixture")); !os.IsNotExist(err) {
		t.Fatal("malformed source advanced its checkpoint")
	}
}

// A tail observation must not stand in for custody of the intervening history.
func TestCodexHistoryFairBackfillAndRecentTail(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema+`
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<80)
INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
SELECT 'thread-a','turn-a','history-'||x,x*512,x,'{"type":"agentMessage","text":"history-'||x||'"}','agentMessage',x*512 FROM n;
INSERT INTO thread_history_projection_state VALUES('thread-z',10,2);
INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
VALUES('thread-z','turn-z','late-user',1,99999,'{"type":"userMessage","content":"Late thread visible promptly"}','userMessage',1);
`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	first := captureCodex(t, hp, root, "", now)
	if first.Pending == 0 || first.NativeBodyRows > 32*512+8 {
		t.Fatalf("unbounded or missing backlog: %+v", first)
	}
	if !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-z/thread_items/0"), "Late thread visible promptly") {
		t.Fatal("late thread starved behind early history")
	}
	assertContiguous := func() {
		t.Helper()
		var last int64 = -1
		packs := map[int64]bool{}
		for _, item := range codexManifest(t, hp).Items {
			if item.VersionState == "superseded" {
				continue
			}
			ref, _ := url.Parse(item.SourceRef)
			if ref.Query().Get("thread") != "thread-a" {
				continue
			}
			if ref.Query().Get("view") == "raw_pack" && ref.Query().Get("table") == "thread_items" {
				var bucket int64
				fmt.Sscan(ref.Query().Get("bucket"), &bucket)
				packs[bucket] = true
			}
			if ref.Query().Get("view_key") == "cursor/thread-a" {
				raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(item.SourceFingerprint, "codex-cursor-v1:"))
				if err != nil {
					t.Fatal(err)
				}
				var state struct {
					Tables map[string]struct {
						Last int64 `json:"last"`
					} `json:"tables"`
				}
				if err := json.Unmarshal(raw, &state); err != nil {
					t.Fatal(err)
				}
				last = state.Tables["thread_items"].Last
			}
		}
		if last >= 80*512 {
			t.Fatal("tail capture jumped over history")
		}
		for bucket := int64(1); bucket*512 <= last; bucket++ {
			if !packs[bucket] {
				t.Fatalf("watermark crossed missing pack %d", bucket)
			}
		}
	}
	assertContiguous()
	nativeSQL(t, db, `INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
 VALUES('thread-a','turn-a','fresh-tail',41984,999999,'{"type":"agentMessage","text":"Fresh tail during backfill"}','agentMessage',41984);
 UPDATE thread_history_projection_state SET next_rollout_ordinal=41985 WHERE thread_id='thread-a';`)
	second := captureCodex(t, hp, root, "", now)
	if second.Pending == 0 || second.NativeBodyRows > 32*512+8 {
		t.Fatalf("second bounded pass: %+v", second)
	}
	if !strings.Contains(codexCurrentBody(t, hp, "semantic/thread-a/thread_items/82"), "Fresh tail during backfill") {
		t.Fatal("new tail waited for history")
	}
	assertContiguous()
}

func TestCodexHistoryRoundRobinProgressUnderContinuousTails(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema+`
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<80)
INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
SELECT 'thread-a','turn-a','backlog-'||x,x*512,x,'{"type":"toolResult","output":"old history"}','toolResult',x*512 FROM n;
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<48)
INSERT INTO thread_history_projection_state SELECT printf('thread-z%02d',x),10,2 FROM n;
INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
SELECT thread_id,'turn-z','late',1,1,'{"type":"userMessage","content":"Cold thread must progress"}','userMessage',1
FROM thread_history_projection_state WHERE thread_id LIKE 'thread-z%';
`)
	nativeSQL(t, filepath.Join(root, "context.sqlite"), `CREATE TABLE threads(id TEXT PRIMARY KEY,title TEXT,cwd TEXT);
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<48)
INSERT INTO threads SELECT printf('thread-z%02d',x),'Cold thread context','/workspace/fixture' FROM n;`)
	now := time.Now().UTC()
	for pass := 0; pass < 4; pass++ {
		nativeSQL(t, db, fmt.Sprintf(`INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
VALUES('thread-a','turn-a','fresh-%d',%d,%d,'{"type":"agentMessage","text":"Continuously active thread"}','agentMessage',%d);
UPDATE thread_history_projection_state SET next_rollout_ordinal=%d WHERE thread_id='thread-a';`, pass, (82+pass)*512, 999999+pass, (82+pass)*512, (82+pass)*512+1))
		r := captureCodex(t, hp, root, "context.sqlite", now)
		if r.NativeBodyRows > 32*512+8 {
			t.Fatalf("pass %d exceeded bounded range work: %+v", pass, r)
		}
		visible, contexts := map[string]bool{}, map[string]bool{}
		for _, item := range codexManifest(t, hp).Items {
			if item.VersionState == "superseded" {
				continue
			}
			ref, _ := url.Parse(item.SourceRef)
			q := ref.Query()
			if q.Get("view") == "semantic" {
				visible[q.Get("thread")] = true
			}
			if q.Get("view") == "semantic_context" {
				contexts[q.Get("thread")] = true
			}
		}
		for sid := range contexts {
			if !visible[sid] {
				t.Fatalf("uncaptured thread %s generated context-only retrieval", sid)
			}
		}
	}
	for n := 1; n <= 48; n++ {
		if !strings.Contains(codexCurrentBody(t, hp, fmt.Sprintf("semantic/thread-z%02d/thread_items/0", n)), "Cold thread must progress") {
			t.Fatalf("cold thread %d starved under a continuous tail", n)
		}
		codexCurrentBody(t, hp, fmt.Sprintf("context/thread-z%02d", n))
	}
	if !strings.Contains(codexCurrentBody(t, hp, "pack/thread-a/thread_items/1"), "old history") {
		t.Fatal("tail priority starved contiguous history")
	}
}

func TestCodexHistoryByteBudgetAndDeferredMetadataPreserveProgress(t *testing.T) {
	// Exercise the exact production scheduler at KiB scale, with rows larger than
	// the priority share and an optional metadata row over the row limit.
	reader := codexHistoryReader
	codexHistoryReader = strings.NewReplacer("MAX_BYTES = 128 << 20", "MAX_BYTES = 128 << 10", "MAX_ROW = 64 << 20", "MAX_ROW = 64 << 10", "byte_limit = 32 << 20", "byte_limit = 32 << 10").Replace(reader)
	defer func() { codexHistoryReader = reader }()
	hp, root := t.TempDir(), nativeRoot(t)
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeSQL(t, db, codexHistorySchema+`
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<6)
INSERT INTO thread_history_projection_state SELECT printf('thread-z%02d',x),10,2 FROM n;
INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
SELECT thread_id,'turn-z','large-user',1,1,'{"type":"userMessage","content":"'||replace(hex(zeroblob(57344)),'00','x')||'"}','userMessage',1
FROM thread_history_projection_state WHERE thread_id LIKE 'thread-z%';
`)
	nativeSQL(t, filepath.Join(root, "state.sqlite"), `CREATE TABLE threads(id TEXT PRIMARY KEY,title TEXT,cwd TEXT,updated_at_ms INTEGER,unknown_payload BLOB);
INSERT INTO threads VALUES('thread-a','Tiny useful context','/workspace/fixture',1,zeroblob(65537));`)
	now := time.Now().UTC()
	for pass := 0; pass < 7; pass++ {
		nativeSQL(t, db, fmt.Sprintf(`INSERT INTO thread_items(thread_id,turn_id,item_id,rollout_ordinal,created_at_ms,item_json,item_type,updated_at_ordinal)
VALUES('thread-a','turn-a','hot-%d',%d,%d,'{"type":"agentMessage","text":"'||replace(hex(zeroblob(20480)),'00','h')||'"}','agentMessage',%d);
UPDATE thread_history_projection_state SET next_rollout_ordinal=%d WHERE thread_id='thread-a';`, pass, (pass+1)*512, 999999+pass, (pass+1)*512, (pass+1)*512+1))
		r := captureCodex(t, hp, root, "state.sqlite", now)
		if r.NativeBodyBytes > 128<<10 || r.Pending == 0 {
			t.Fatalf("pass %d lost bound/deferred metadata: %+v", pass, r)
		}
		if !strings.Contains(codexCurrentBody(t, hp, "cursor/thread-a"), "projection_state") {
			t.Fatal("deferred metadata discarded progress cursor")
		}
	}
	for n := 1; n <= 6; n++ {
		codexCurrentBody(t, hp, fmt.Sprintf("semantic/thread-z%02d/thread_items/0", n))
	}
}
