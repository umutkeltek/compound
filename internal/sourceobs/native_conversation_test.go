package sourceobs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

const hermesNativeSchema = `
CREATE TABLE sessions(id TEXT PRIMARY KEY,source TEXT,started_at REAL,runtime_revision INTEGER DEFAULT 1,title TEXT);
CREATE TABLE messages(id INTEGER PRIMARY KEY,session_id TEXT,role TEXT,content TEXT,timestamp REAL,active INTEGER DEFAULT 1,compacted INTEGER DEFAULT 0,_compressed_summary INTEGER DEFAULT 0,reasoning TEXT,tool_calls TEXT);
CREATE INDEX messages_session ON messages(session_id);
`

func nativePython(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 SQLite fixture backend unavailable")
	}
	return path
}
func nativeSQL(t *testing.T, path, script string) {
	t.Helper()
	cmd := exec.Command(nativePython(t), "-I", "-c", "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.executescript(sys.argv[2]); c.commit(); c.close()", path, script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("SQLite fixture: %v: %s", err, out)
	}
}
func nativeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func captureNative(t *testing.T, hp, root, format string, now time.Time) CaptureBatchReport {
	t.Helper()
	r, err := CaptureNativeConversations(context.Background(), hp, "native_fixture", root, format, "", now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func nativeManifest(t *testing.T, hp string) SourceManifest {
	t.Helper()
	m, err := LoadSourceManifest(hp, "native_fixture")
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func nativeSessionItems(t *testing.T, hp, sid string) []SourceManifestItem {
	t.Helper()
	var out []SourceManifestItem
	for _, item := range nativeManifest(t, hp).Items {
		if strings.Contains(item.SourceRef, "session="+sid+"&") && !strings.Contains(item.SourceRef, "row=") {
			out = append(out, item)
		}
	}
	return out
}
func nativeBody(t *testing.T, hp string, item SourceManifestItem) string {
	t.Helper()
	store, err := home.NewArtifactStore(hp)
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Get(item.BodyRef)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func nativeProjectedMessages(t *testing.T, body string) []string {
	t.Helper()
	var messages []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		var row struct {
			Type    string
			Message struct{ Content string }
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.Type == "message" {
			messages = append(messages, row.Message.Content)
		}
	}
	return messages
}

func TestNativeHermesIncrementalReconciliationAndCustody(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+`
INSERT INTO sessions(id,source,started_at,title) VALUES('s1','cli',1,'First'),('s2','cli',2,'Second');
INSERT INTO messages(id,session_id,role,content,timestamp,active,compacted,reasoning,tool_calls) VALUES(1,'s1','user','Cedar archive',1,0,1,'Reasoning retained','Tool payload retained'),(2,'s2','assistant','Birch archive',2,1,0,NULL,NULL);
INSERT INTO messages(id,session_id,role,content,timestamp,_compressed_summary) VALUES(3,'s1','assistant','GeneratedSummarySecret',3,1);
`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	before, _ := os.ReadFile(path)
	first := captureNative(t, hp, root, "hermes", now)
	if first.Captured != 2 || first.NativeSessionBodies != 2 || first.NativeBodyRows != 5 {
		t.Fatalf("initial: %+v", first)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("source database bytes changed")
	}
	items := nativeSessionItems(t, hp, "s1")
	body := nativeBody(t, hp, items[0])
	rawBodies := nativeRawBodies(t, hp, "s1")
	if !strings.Contains(rawBodies, "Reasoning retained") || !strings.Contains(rawBodies, "GeneratedSummarySecret") || !strings.Contains(rawBodies, `"compacted":1`) {
		t.Fatal("original rows/flags missing from custody")
	}
	if got := nativeProjectedMessages(t, body); len(got) != 1 || got[0] != "Cedar archive" {
		t.Fatalf("projection: %v", got)
	}
	unchanged := captureNative(t, hp, root, "hermes", now.Add(time.Minute))
	if unchanged.NativeBodyRows != 0 || unchanged.NativeMetadataRows != 0 || unchanged.Captured != 0 {
		t.Fatalf("unchanged read bodies: %+v", unchanged)
	}

	nativeSQL(t, path, "UPDATE messages SET content='Maple archive',timestamp=4 WHERE id=1; UPDATE sessions SET runtime_revision=2 WHERE id='s1';")
	change := captureNative(t, hp, root, "hermes", now.Add(2*time.Minute))
	if change.Captured != 1 || change.NativeSessionBodies != 1 || change.NativeBodyRows != 3 || change.NativeMetadataRows == 0 {
		t.Fatalf("one change must fetch one body: %+v", change)
	}
	items = nativeSessionItems(t, hp, "s1")
	if len(items) != 2 {
		t.Fatalf("versions: %+v", items)
	}
	oldOwned := false
	for _, item := range items {
		if item.VersionState == "superseded" && strings.Contains(nativeBody(t, hp, item), "Cedar archive") {
			oldOwned = true
		}
	}
	if !oldOwned {
		t.Fatal("correction deleted historical custody")
	}

	// A source content correction that does not advance native metadata is found
	// by the next daily full-row reconciliation, with the limitation explicit.
	nativeSQL(t, path, "UPDATE messages SET content='Elm archive' WHERE id=1;")
	fast := captureNative(t, hp, root, "hermes", now.Add(3*time.Minute))
	if fast.NativeBodyRows != 0 {
		t.Fatalf("metadata path selected unchanged bodies: %+v", fast)
	}
	full := captureNative(t, hp, root, "hermes", now.Add(24*time.Hour))
	if full.NativeSessionBodies != 2 || full.Captured != 1 {
		t.Fatalf("authoritative reconcile: %+v", full)
	}

	nativeSQL(t, path, "DELETE FROM messages WHERE session_id='s1'; DELETE FROM sessions WHERE id='s1';")
	deleted := captureNative(t, hp, root, "hermes", now.Add(24*time.Hour+time.Minute))
	if deleted.Missing != 1 || deleted.NativeSessionBodies != 0 {
		t.Fatalf("missing session: %+v", deleted)
	}
	found := false
	for _, item := range nativeSessionItems(t, hp, "s1") {
		if item.VersionState != "superseded" && strings.Contains(nativeBody(t, hp, item), "Elm archive") {
			found = true
		}
	}
	if !found {
		t.Fatal("source absence invalidated owned current evidence")
	}
}

func TestNativeBoundedBackfillAndDailyReconciliationResume(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+`
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<5)
INSERT INTO sessions(id,source,started_at) SELECT printf('s%03d',i),'cli',i FROM n;
INSERT INTO messages(id,session_id,role,content,timestamp) SELECT rowid,id,'user','Synthetic turn ' || id,started_at FROM sessions;
`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	for i, count := range []int{2, 2, 1} {
		pass := captureNative(t, hp, root, "hermes", now)
		pending := []int{3, 1, 0}[i]
		if pass.Captured != count || pass.NativeSessionBodies != count || pass.NativeBodyRows != 2*count || pass.Pending != pending {
			t.Fatalf("backfill pass %d: %+v", i, pass)
		}
	}
	for i := 1; i <= 5; i++ {
		sid := fmt.Sprintf("s%03d", i)
		items := nativeSessionItems(t, hp, sid)
		if len(items) != 1 {
			t.Fatalf("session %s duplicated or lost: %+v", sid, items)
		}
		if got := nativeProjectedMessages(t, nativeBody(t, hp, items[0])); len(got) != 1 || got[0] != "Synthetic turn "+sid {
			t.Fatalf("session %s projection: %v", sid, got)
		}
		if rows := nativeRawBodies(t, hp, sid); !strings.Contains(rows, `"table":"sessions"`) || !strings.Contains(rows, `"table":"messages"`) || !strings.Contains(rows, "Synthetic turn "+sid) {
			t.Fatalf("session %s lost original rows", sid)
		}
	}
	before := nativeManifest(t, hp)
	for i, count := range []int{2, 2, 1} {
		pass := captureNative(t, hp, root, "hermes", now.Add(24*time.Hour))
		pending := []int{3, 1, 0}[i]
		if pass.NativeSessionBodies != count || pass.NativeBodyRows != 2*count || pass.Captured != 0 || pass.Pending != pending {
			t.Fatalf("daily reconciliation pass %d: %+v", i, pass)
		}
	}
	after := nativeManifest(t, hp)
	bodies := map[string]string{}
	for _, item := range after.Items {
		if strings.Contains(item.SourceRef, "session=") {
			bodies[item.ID] = item.BodyRef
		}
	}
	for _, item := range before.Items {
		if strings.Contains(item.SourceRef, "session=") && bodies[item.ID] != item.BodyRef {
			t.Fatalf("daily reconciliation lost owned body %s", item.ID)
		}
	}
	done := captureNative(t, hp, root, "hermes", now.Add(24*time.Hour))
	if done.NativeBodyRows != 0 || done.NativeSessionBodies != 0 || done.Pending != 0 {
		t.Fatalf("completed reconcile repeated: %+v", done)
	}
}

func TestNativeBoundedBackfillAndReconciliationCrossUTCPeriods(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+`
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<5)
INSERT INTO sessions(id,source,started_at) SELECT printf('s%03d',i),'cli',i FROM n;
INSERT INTO messages(id,session_id,role,content,timestamp) SELECT rowid,id,'user','Synthetic turn ' || id,started_at FROM sessions;
`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	// Uncaptured sessions must receive the budget even when every pass starts
	// a new daily reconciliation. Otherwise the same prefix can starve the tail.
	for day, captured := range []int{2, 2, 1} {
		pass := captureNative(t, hp, root, "hermes", now.Add(time.Duration(day)*24*time.Hour))
		if pass.Captured != captured || pass.NativeSessionBodies != 2 || pass.Pending != 3 {
			t.Fatalf("cross-day backfill %d: %+v", day, pass)
		}
	}
	for i := 1; i <= 5; i++ {
		sid := fmt.Sprintf("s%03d", i)
		items := nativeSessionItems(t, hp, sid)
		if len(items) != 1 {
			t.Fatalf("cross-day coverage for %s: %+v", sid, items)
		}
		if got := nativeProjectedMessages(t, nativeBody(t, hp, items[0])); len(got) != 1 || got[0] != "Synthetic turn "+sid {
			t.Fatalf("cross-day projection for %s: %v", sid, got)
		}
	}
	for i, pending := range []int{1, 0} {
		pass := captureNative(t, hp, root, "hermes", now.Add(48*time.Hour))
		if pass.Captured != 0 || pass.NativeSessionBodies != 2-i || pass.Pending != pending {
			t.Fatalf("finish cross-day checkpoint %d: %+v", i, pass)
		}
	}
	// Once backfill finishes, older checkpoint epochs must also precede newer
	// ones across day changes, so daily reconciliation eventually reads the tail.
	for day := 3; day <= 5; day++ {
		pass := captureNative(t, hp, root, "hermes", now.Add(time.Duration(day)*24*time.Hour))
		if pass.Captured != 0 || pass.NativeSessionBodies != 2 {
			t.Fatalf("cross-day reconciliation %d: %+v", day, pass)
		}
	}
	minEpoch := now.Add(72*time.Hour).Unix() / 86400
	for i := 1; i <= 5; i++ {
		sid := fmt.Sprintf("s%03d", i)
		items := nativeSessionItems(t, hp, sid)
		if len(items) != 1 {
			t.Fatalf("reconciliation duplicated session %s: %+v", sid, items)
		}
		parts := strings.Split(items[0].SourceFingerprint, ":")
		epoch, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		if err != nil || epoch < minEpoch {
			t.Fatalf("session %s starved across UTC periods: %s", sid, items[0].SourceFingerprint)
		}
	}
}

const opencodeNativeSchema = `
CREATE TABLE session(id TEXT PRIMARY KEY,time_updated INTEGER);
CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,time_created INTEGER,time_updated INTEGER,data TEXT);
CREATE TABLE part(id TEXT PRIMARY KEY,message_id TEXT,session_id TEXT,time_created INTEGER,time_updated INTEGER,data TEXT);
CREATE TABLE session_v2(id TEXT PRIMARY KEY,time_updated INTEGER);
CREATE TABLE session_message(id TEXT PRIMARY KEY,session_id TEXT,type TEXT,seq INTEGER,time_created INTEGER,time_updated INTEGER,data TEXT);
CREATE TABLE session_part(message_id TEXT,session_id TEXT,ordinal INTEGER,type TEXT,time_created INTEGER,time_updated INTEGER,data TEXT);
INSERT INTO session VALUES('mirror',1);
INSERT INTO session_v2 VALUES('mirror',1);
INSERT INTO message VALUES('old-user','mirror',1,1,'{"role":"user"}');
INSERT INTO part VALUES('p','old-user','mirror',1,1,'{"type":"text","text":"Cedar archive"}');
INSERT INTO session_message VALUES('new-user','mirror','user',1,1,1,'{"text":"Cedar archive"}');
INSERT INTO session_message VALUES('answer','mirror','assistant',2,2,2,'{"content":[{"type":"text","text":"Birch response"},{"type":"reasoning","text":"Hidden reasoning"},{"type":"tool","text":"Hidden tool"}]}');
`

func TestNativeOpenCodeBothSchemasDedupeMirrorsRetainRows(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "opencode.db")
	nativeSQL(t, path, opencodeNativeSchema)
	r := captureNative(t, hp, root, "opencode", time.Now())
	if r.Captured != 1 {
		t.Fatalf("capture: %+v", r)
	}
	items := nativeSessionItems(t, hp, "mirror")
	body := nativeBody(t, hp, items[0])
	messages := nativeProjectedMessages(t, body)
	if len(messages) != 2 || messages[0] != "Cedar archive" || messages[1] != "Birch response" {
		t.Fatalf("mirror/text projection: %v", messages)
	}
	for _, table := range []string{`"table":"session"`, `"table":"session_v2"`, `"table":"message"`, `"table":"session_message"`, `"table":"part"`} {
		if !strings.Contains(nativeRawBodies(t, hp, "mirror"), table) {
			t.Fatalf("lost table provenance: %s", table)
		}
	}
	nativeSQL(t, path, "INSERT INTO message VALUES('old-user-second','mirror',3,3,'{\"role\":\"user\"}'); INSERT INTO part VALUES('p2','old-user-second','mirror',3,3,'{\"type\":\"text\",\"text\":\"Cedar archive\"}');")
	captureNative(t, hp, root, "opencode", time.Now())
	repeated := false
	for _, item := range nativeSessionItems(t, hp, "mirror") {
		if item.VersionState == "superseded" {
			continue
		}
		projected := nativeProjectedMessages(t, nativeBody(t, hp, item))
		if len(projected) == 3 && strings.Count(strings.Join(projected, "|"), "Cedar archive") == 2 {
			repeated = true
		}
	}
	if !repeated {
		t.Fatal("mirror dedupe removed an original repeated turn")
	}
	nativeSQL(t, path, "INSERT INTO session_part VALUES('answer','mirror',1,'text',2,3,'{\"text\":\"Additional part\"}'); UPDATE session_message SET data='{}',time_updated=3 WHERE id='answer';")
	update := captureNative(t, hp, root, "opencode", time.Now())
	if update.Captured != 1 {
		t.Fatalf("v2 part update: %+v", update)
	}
	for _, item := range nativeSessionItems(t, hp, "mirror") {
		if item.VersionState != "superseded" && !strings.Contains(strings.Join(nativeProjectedMessages(t, nativeBody(t, hp, item)), "|"), "Additional part") {
			t.Fatal("v2 ordinal parts not consumed")
		}
	}
}

func TestNativeInterruptedReaderDoesNotAdvanceCheckpoint(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+"INSERT INTO sessions(id,source,started_at) VALUES('s1','cli',1); INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(1,'s1','user','Cedar',1);")
	now := time.Now()
	captureNative(t, hp, root, "hermes", now)
	before, err := os.ReadFile(SourceManifestPath(hp, "native_fixture"))
	if err != nil {
		t.Fatal(err)
	}
	nativeSQL(t, path, "UPDATE messages SET content='Birch',timestamp=2 WHERE id=1;")
	backend := t.TempDir()
	// Deliberately simulate a subprocess that exported a changed body but died
	// before the complete inventory record. No partial checkpoint may publish.
	script := "#!/bin/sh\nprintf '%s\\n' '{\"session_id\":\"s1\",\"fingerprint\":\"interrupted\",\"body\":\"{\\\"type\\\":\\\"native_session\\\"}\\n\"}'\n"
	if err := os.WriteFile(filepath.Join(backend, "python3"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", backend)
	if _, err := CaptureNativeConversations(context.Background(), hp, "native_fixture", root, "hermes", "", now); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("interruption: %v", err)
	}
	after, _ := os.ReadFile(SourceManifestPath(hp, "native_fixture"))
	if string(before) != string(after) {
		t.Fatal("interrupted read published checkpoint")
	}
	t.Setenv("PATH", originalPath)
	retry := captureNative(t, hp, root, "hermes", now)
	if retry.Captured != 1 || retry.NativeSessionBodies != 1 {
		t.Fatalf("retry: %+v", retry)
	}
}

func TestNativeRefusesUnsupportedSchemaBackendAndLinks(t *testing.T) {
	root := nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, "CREATE TABLE unrelated(id INTEGER);")
	if _, err := CaptureNativeConversations(context.Background(), t.TempDir(), "native_fixture", root, "hermes", "", time.Now()); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("unsupported schema: %v", err)
	}
	t.Run("backend", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := CaptureNativeConversations(context.Background(), t.TempDir(), "native_fixture", root, "hermes", "", time.Now()); err == nil || !strings.Contains(err.Error(), "python3") {
			t.Fatalf("backend: %v", err)
		}
	})
	for _, rel := range []string{"../state.db", path, "."} {
		if _, err := NativeConversationDatabase(root, "hermes", rel); err == nil {
			t.Fatalf("allowed escaping database %q", rel)
		}
	}
	if err := os.Symlink(path, filepath.Join(root, "alias.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeConversationDatabase(root, "hermes", "alias.db"); err == nil {
		t.Fatal("linked database allowed")
	}
	if err := os.Symlink(path, path+"-wal"); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeConversationDatabase(root, "hermes", ""); err == nil {
		t.Fatal("linked WAL allowed")
	}
}

func TestNativeWALOnlyCommitTriggersIncrementalRead(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
c.execute("PRAGMA journal_mode=WAL")
c.execute("PRAGMA wal_autocheckpoint=0")
c.executescript(sys.argv[2])
c.commit()
print("ready",flush=True)
for sql in sys.stdin:
    c.executescript(sql)
    c.commit()
    print("committed",flush=True)
`
	cmd := exec.CommandContext(ctx, nativePython(t), "-I", "-c", script, path, hermesNativeSchema+"INSERT INTO sessions(id,source,started_at) VALUES('wal-session','cli',1); INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(1,'wal-session','user','Cedar',1);")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatalf("writer not ready: %s", scanner.Text())
	}
	before, _ := os.ReadFile(path)
	now := time.Now()
	first := captureNative(t, hp, root, "hermes", now)
	if first.Captured != 1 {
		t.Fatalf("WAL initial: %+v", first)
	}
	if _, err := fmt.Fprintln(input, "UPDATE messages SET content='Birch',timestamp=2 WHERE id=1;"); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() || scanner.Text() != "committed" {
		t.Fatal("writer did not commit")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("fixture commit did not stay WAL-only")
	}
	updated := captureNative(t, hp, root, "hermes", now)
	if updated.Captured != 1 || updated.NativeSessionBodies != 1 {
		t.Fatalf("WAL update missed: %+v", updated)
	}
}

func TestNativeMalformedSourceDoesNotEchoPrivateFields(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "opencode.db")
	nativeSQL(t, path, opencodeNativeSchema+"UPDATE session_message SET time_created='SensitiveTimestampPayload' WHERE id='answer';")
	_, err := CaptureNativeConversations(context.Background(), hp, "native_fixture", root, "opencode", "", time.Now())
	if err == nil || strings.Contains(err.Error(), "SensitiveTimestampPayload") || !strings.Contains(err.Error(), "source row encoding/schema") {
		t.Fatalf("private field reached diagnostics: %v", err)
	}
	if _, err := LoadSourceManifest(hp, "native_fixture"); !os.IsNotExist(err) {
		t.Fatalf("invalid native source published manifest: %v", err)
	}
}

func TestNativePublicationRefusesNewerSessionCheckpoint(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+"INSERT INTO sessions(id,source,started_at) VALUES('s1','cli',1); INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(1,'s1','user','Cedar',1);")
	now := time.Now()
	captureNative(t, hp, root, "hermes", now)
	nativeSQL(t, path, "UPDATE messages SET content='Birch',timestamp=2 WHERE id=1;")
	newer := nativeManifest(t, hp)
	for i := range newer.Items {
		if strings.Contains(newer.Items[i].SourceRef, "session=s1&") {
			newer.Items[i].SourceFingerprint = "native-session-v2:competing"
		}
	}
	body, err := json.MarshalIndent(newer, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	competing := filepath.Join(t.TempDir(), "competing.json")
	if err := os.WriteFile(competing, body, 0600); err != nil {
		t.Fatal(err)
	}
	backend := t.TempDir()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// Deliberately inject a valid newer authority record after the initial
	// read. A cooperating writer would normally use the manifest lock.
	script := "#!/bin/sh\n/bin/cp " + quote(competing) + " " + quote(SourceManifestPath(hp, "native_fixture")) + "\nprintf '%s\\n' '{\"session_id\":\"s1\",\"fingerprint\":\"candidate\",\"body\":\"{\\\"type\\\":\\\"native_session\\\"}\\n\"}' '{\"complete\":true,\"pending\":1}'\n"
	if err := os.WriteFile(filepath.Join(backend, "python3"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	previousPath := os.Getenv("PATH")
	t.Setenv("PATH", backend)
	_, err = CaptureNativeConversations(context.Background(), hp, "native_fixture", root, "hermes", "", now)
	if err == nil || !strings.Contains(err.Error(), "session checkpoint advanced concurrently") {
		t.Fatalf("newer generation replaced: %v", err)
	}
	actual, err := os.ReadFile(SourceManifestPath(hp, "native_fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(body) {
		t.Fatal("newer authority bytes changed")
	}
	t.Setenv("PATH", previousPath)
	retry := captureNative(t, hp, root, "hermes", now)
	if retry.Captured != 1 {
		t.Fatalf("retry after competing publication: %+v", retry)
	}
}
