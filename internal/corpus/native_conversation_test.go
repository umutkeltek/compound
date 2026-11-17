package corpus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

func TestOMPConversationProjectionAndRefresh(t *testing.T) {
	body := `{"type":"session","id":"fixture"}
{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"Cedar archive decision"}}
{"type":"message","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Hidden reasoning"},{"type":"text","text":"Birch archive response"},{"type":"toolCall","name":"fixture","arguments":{"text":"Hidden tool"}}]}}
{"type":"message","message":{"role":"toolResult","content":[{"type":"text","text":"Hidden result"}]}}
{"type":"message","message":{"role":"system","content":"Hidden prompt"}}
`
	projected, err := conversationText("omp", body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projected, "Cedar archive") || !strings.Contains(projected, "Birch archive") || strings.Contains(projected, "Hidden") {
		t.Fatalf("projection: %s", projected)
	}
	hp, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "session.jsonl"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	lane := LaneConfig{Lane: "native-omp", SourceFamily: "native_omp_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3}, Source: &SourceConfig{Root: root, Format: "omp"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	r := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	if r.Failed() || r.Sources[0].CapturedFiles != 1 {
		t.Fatalf("refresh: %+v", r)
	}
	found := Search(context.Background(), hp, []LaneConfig{lane}, factory, "archive", 5, QueryOptions{IncludeText: true})
	if len(found.Evidence) != 1 || !strings.Contains(found.Evidence[0].Text, "Cedar archive") {
		t.Fatalf("recall: %+v", found)
	}
}

func nativeCorpusSQL(t *testing.T, path, script string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python SQLite fixture unavailable")
	}
	cmd := exec.Command(python, "-I", "-c", "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.executescript(sys.argv[2]); c.commit(); c.close()", path, script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
}
func TestNativeHermesRefreshRetrievalCorrectionAndMissingHistory(t *testing.T) {
	hp := t.TempDir()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.db")
	nativeCorpusSQL(t, path, `
CREATE TABLE sessions(id TEXT PRIMARY KEY,source TEXT,started_at REAL,runtime_revision INTEGER);
CREATE TABLE messages(id INTEGER PRIMARY KEY,session_id TEXT,role TEXT,content TEXT,timestamp REAL,active INTEGER DEFAULT 1,compacted INTEGER DEFAULT 0,_compressed_summary INTEGER DEFAULT 0,reasoning TEXT);
INSERT INTO sessions VALUES('fixture-session','cli',1,1);
INSERT INTO messages(id,session_id,role,content,timestamp,active,compacted,reasoning) VALUES(1,'fixture-session','user','Cedar archive decision',1,0,1,'HiddenReasoning');
INSERT INTO messages(id,session_id,role,content,timestamp,_compressed_summary) VALUES(2,'fixture-session','assistant','HiddenSummary',2,1),(3,'fixture-session','system','HiddenPrompt',3,0),(4,'fixture-session','tool','HiddenTool',4,0);
`)
	lane := LaneConfig{Lane: "native-hermes", SourceFamily: "native_hermes_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3}, Source: &SourceConfig{Root: root, Format: "hermes"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	refresh := func() RefreshReport { return Refresh(context.Background(), hp, []LaneConfig{lane}, factory, now) }
	first := refresh()
	if first.Failed() || first.Sources[0].NativeSessionBodies != 1 {
		t.Fatalf("first refresh: %+v", first)
	}
	search := func(history bool) RecallReport {
		return Search(context.Background(), hp, []LaneConfig{lane}, factory, "archive", 10, QueryOptions{IncludeText: true, IncludeHistory: history})
	}
	found := search(false)
	if len(found.Evidence) != 1 || !strings.Contains(found.Evidence[0].Text, "Cedar archive") || strings.Contains(found.Evidence[0].Text, "Hidden") {
		t.Fatalf("semantic text: %+v", found)
	}
	initialManifest, err := sourceobs.LoadSourceManifest(hp, lane.SourceFamily)
	if err != nil {
		t.Fatal(err)
	}
	provenanceFound := false
	for _, item := range initialManifest.Items {
		if item.ID == found.Evidence[0].ItemID && strings.Contains(item.SourceRef, "session=fixture-session") && strings.Contains(item.SourceRef, "messages") {
			provenanceFound = true
		}
	}
	if !provenanceFound {
		t.Fatalf("retrieval source identity lost: %+v", found.Evidence[0])
	}
	oldID := found.Evidence[0].ItemID
	nativeCorpusSQL(t, path, "UPDATE messages SET content='Birch archive correction',timestamp=5 WHERE id=1; UPDATE sessions SET runtime_revision=2;")
	second := refresh()
	if second.Failed() || second.Sources[0].CapturedFiles != 1 {
		t.Fatalf("correction: %+v", second)
	}
	current := search(false)
	if len(current.Evidence) != 1 || current.Evidence[0].ItemID == oldID || !strings.Contains(current.Evidence[0].Text, "Birch archive") {
		t.Fatalf("current correction: %+v", current)
	}
	if history := search(true); len(history.Evidence) != 2 {
		t.Fatalf("owned history: %+v", history)
	}
	nativeCorpusSQL(t, path, "DELETE FROM messages; DELETE FROM sessions;")
	missing := refresh()
	if missing.Failed() || missing.Sources[0].MissingFiles != 1 || missing.Sources[0].Status != "capture_incomplete" {
		t.Fatalf("missing coverage: %+v", missing)
	}
	if got := search(false); len(got.Evidence) != 1 || !strings.Contains(got.Evidence[0].Text, "Birch archive") {
		t.Fatalf("source absence changed knowledge: %+v", got)
	}
	if got := search(true); len(got.Evidence) != 2 {
		t.Fatalf("missing history: %+v", got)
	}
	manifest, err := sourceobs.LoadSourceManifest(hp, lane.SourceFamily)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range manifest.Items {
		if item.SourceKind != "hermes_transcript_v1" || item.BodyPolicy != "owned_snapshot" {
			t.Fatalf("unexpected ownership: %+v", item)
		}
	}
}

func TestNativeRefreshUnsupportedSchemaIsActionableFailure(t *testing.T) {
	hp := t.TempDir()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nativeCorpusSQL(t, filepath.Join(root, "opencode.db"), "CREATE TABLE unrelated(id INTEGER);")
	lane := LaneConfig{Lane: "native-invalid", SourceFamily: "native_invalid_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3}, Source: &SourceConfig{Root: root, Format: "opencode"}}
	called := false
	r := Refresh(context.Background(), hp, []LaneConfig{lane}, func(l LaneConfig) (Embedder, error) { called = true; return nil, nil }, time.Now())
	if !r.Failed() || called || r.Sources[0].Status != "capture_failed" || !strings.Contains(r.Sources[0].Error, "schema") {
		t.Fatalf("schema failure concealed: %+v", r)
	}
}

func TestNativeRawOnlyAppendReusesSemanticProjectionAndVectors(t *testing.T) {
	hp := t.TempDir()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.db")
	nativeCorpusSQL(t, path, `
CREATE TABLE sessions(id TEXT PRIMARY KEY,source TEXT,started_at REAL,runtime_revision INTEGER);
CREATE TABLE messages(id INTEGER PRIMARY KEY,session_id TEXT,role TEXT,content TEXT,timestamp REAL,active INTEGER DEFAULT 1,compacted INTEGER DEFAULT 0,_compressed_summary INTEGER DEFAULT 0);
INSERT INTO sessions VALUES('stable','cli',1,1);
INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(1,'stable','user','Cedar archive decision',1),(2,'stable','tool','Original hidden tool',2);
`)
	lane := LaneConfig{Lane: "native-stable", SourceFamily: "native_stable_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3}, Source: &SourceConfig{Root: root, Format: "hermes"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	now := time.Now()
	first := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, now)
	if first.Failed() {
		t.Fatalf("initial: %+v", first)
	}
	nativeCorpusSQL(t, path, "UPDATE sessions SET runtime_revision=2; INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(3,'stable','tool','Another hidden tool',3);")
	second := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, now)
	if second.Failed() {
		t.Fatalf("raw update: %+v", second)
	}
	if second.Sources[0].NewEmbeddedChunks != 0 {
		t.Fatalf("raw row append changed existing semantic chunk hashes: new_embedded_chunks=%d prepared_items=%d reused_items=%d", second.Sources[0].NewEmbeddedChunks, second.Sources[0].PreparedItems, second.Sources[0].ReusedItems)
	}
}
