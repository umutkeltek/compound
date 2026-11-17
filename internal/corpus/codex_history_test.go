package corpus

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

func TestCodexHistoryDBOnlyRecallAndToolPacks(t *testing.T) {
	hp := t.TempDir()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "thread_history_1.sqlite")
	nativeCorpusSQL(t, path, `
CREATE TABLE thread_history_projection_state(thread_id TEXT PRIMARY KEY,next_rollout_byte_offset INTEGER NOT NULL,next_rollout_ordinal INTEGER NOT NULL);
CREATE TABLE thread_turns(thread_id TEXT,turn_id TEXT,rollout_ordinal INTEGER,status TEXT,error_json TEXT,PRIMARY KEY(thread_id,turn_id));
CREATE UNIQUE INDEX idx_thread_turns_page ON thread_turns(thread_id,rollout_ordinal);
CREATE TABLE thread_items(thread_id TEXT,turn_id TEXT,item_id TEXT,rollout_ordinal INTEGER,created_at_ms INTEGER,item_json TEXT,item_type TEXT,updated_at_ordinal INTEGER,PRIMARY KEY(thread_id,turn_id,item_id));
CREATE UNIQUE INDEX idx_thread_items_page ON thread_items(thread_id,rollout_ordinal);
CREATE INDEX idx_thread_items_updated_page ON thread_items(thread_id,updated_at_ordinal);
CREATE TABLE thread_realtime_items(thread_id TEXT,item_id TEXT,rollout_ordinal INTEGER,created_at_ms INTEGER,item_type TEXT,item_json TEXT,PRIMARY KEY(thread_id,item_id));
CREATE UNIQUE INDEX idx_thread_realtime_items_page ON thread_realtime_items(thread_id,rollout_ordinal);
INSERT INTO thread_history_projection_state VALUES('db-only-thread',100,10);
INSERT INTO thread_turns VALUES('db-only-thread','native-turn',0,'completed',NULL);
INSERT INTO thread_items VALUES('db-only-thread','native-turn','native-user',1,1000,'{"type":"userMessage","id":"native-user","clientId":"client-fixture","content":[{"type":"text","text":"Cedar DB-only archive decision"}]}','userMessage',1);
INSERT INTO thread_items VALUES('db-only-thread','native-turn','native-agent',2,1001,'{"type":"agentMessage","id":"native-agent","text":"The maple answer is preserved","phase":"final","memoryCitation":null,"delivery":"direct","questions":[]}','agentMessage',2);
INSERT INTO thread_items VALUES('db-only-thread','native-turn','native-tool',3,1002,'{"type":"toolResult","output":"HiddenToolNeverSemantic"}','toolResult',3);
INSERT INTO thread_items VALUES('db-only-thread','native-turn','native-reasoning',4,1003,'{"type":"reasoning","text":"HiddenReasoningNeverSemantic"}','reasoning',4);
`)
	lane := LaneConfig{Lane: "native-codex-history", SourceFamily: "native_codex_history_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3}, Source: &SourceConfig{Root: root, Format: "codex-history"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	refresh := func() RefreshReport { return Refresh(context.Background(), hp, []LaneConfig{lane}, factory, now) }
	first := refresh()
	if first.Failed() || first.Status != "ok" || first.Sources[0].NativeSessionBodies != 1 {
		t.Fatalf("DB-only capture not wired: %+v", first)
	}
	search := func() RecallReport {
		return Search(context.Background(), hp, []LaneConfig{lane}, factory, "archive", 20, QueryOptions{IncludeText: true})
	}
	got := search()
	if len(got.Evidence) != 1 || !strings.Contains(got.Evidence[0].Text, "Cedar DB-only archive decision") || !strings.Contains(got.Evidence[0].Text, "native-user") || !strings.Contains(got.Evidence[0].Text, "native-turn") || strings.Contains(got.Evidence[0].Text, "Hidden") {
		t.Fatalf("DB-only visible/native attribution lost: %+v", got)
	}
	m, err := sourceobs.LoadSourceManifest(hp, lane.SourceFamily)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range m.Items {
		if item.ID == got.Evidence[0].ItemID {
			ref, e := url.Parse(item.SourceRef)
			if e != nil {
				t.Fatal(e)
			}
			found = ref.Scheme == "sqlite" && ref.Path == filepath.ToSlash(path) && ref.Query().Get("thread") == "db-only-thread" && ref.Query().Get("table") == "thread_items"
		}
	}
	if !found {
		t.Fatal("native sqlite/thread provenance missing")
	}
	unchanged := refresh()
	if unchanged.Failed() || unchanged.Sources[0].NativeBodyRows != 0 || unchanged.Sources[0].BodyBytesRead != 0 || unchanged.Sources[0].NewEmbeddedChunks != 0 {
		t.Fatalf("unchanged history read or embedded bodies: %+v", unchanged)
	}
	nativeCorpusSQL(t, path, `INSERT INTO thread_items VALUES('db-only-thread','native-turn','tool-next',5,1004,'{"type":"toolResult","output":"Hidden appended tool"}','toolResult',5);UPDATE thread_history_projection_state SET next_rollout_ordinal=11;`)
	tool := refresh()
	if tool.Failed() || tool.Sources[0].NewEmbeddedChunks != 0 || tool.Sources[0].CapturedFiles != 0 || tool.Sources[0].IndexBytesWritten != 0 || tool.Sources[0].BodyBytesRead != 0 {
		t.Fatalf("tool append changed semantic packs: %+v", tool)
	}
	if next := search(); len(next.Evidence) != 1 || next.Evidence[0].ItemID != got.Evidence[0].ItemID {
		t.Fatalf("tool append changed source identity: %+v", next)
	}
	nativeCorpusSQL(t, path, `INSERT INTO thread_items VALUES('db-only-thread','native-turn','agent-next',6,1005,'{"type":"agentMessage","id":"agent-next","text":"Birch newly captured information","phase":"final"}','agentMessage',6);UPDATE thread_history_projection_state SET next_rollout_ordinal=12;`)
	visible := refresh()
	if visible.Failed() || visible.Sources[0].NewEmbeddedChunks != 1 {
		t.Fatalf("new visible message not indexed: %+v", visible)
	}
	if next := search(); len(next.Evidence) != 1 || !strings.Contains(next.Evidence[0].Text, "Birch newly captured information") || !strings.Contains(next.Evidence[0].Text, "agent-next") {
		t.Fatalf("new captured info unavailable: %+v", next)
	}
	for _, kind := range []string{"native_pack", "native_row", "native_thread", "native_inventory"} {
		text, err := conversationText("codex-history", `{"type":"`+kind+`","row":{"item_json":"Hidden raw payload"}}`)
		if err != nil || text != "" {
			t.Fatalf("raw %s became semantic: %q %v", kind, text, err)
		}
	}
}

func TestCodexHistoryStateOnlyThreadsDoNotEmbedOrRewriteRetrieval(t *testing.T) {
	hp := t.TempDir()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, "thread_history_1.sqlite")
	nativeCorpusSQL(t, db, `
CREATE TABLE thread_history_projection_state(thread_id TEXT PRIMARY KEY,next_rollout_byte_offset INTEGER,next_rollout_ordinal INTEGER);
CREATE TABLE thread_turns(thread_id TEXT,turn_id TEXT,rollout_ordinal INTEGER,status TEXT,error_json TEXT);
CREATE TABLE thread_items(thread_id TEXT,turn_id TEXT,item_id TEXT,rollout_ordinal INTEGER,created_at_ms INTEGER,item_json TEXT,item_type TEXT,updated_at_ordinal INTEGER);
CREATE TABLE thread_realtime_items(thread_id TEXT,item_id TEXT,rollout_ordinal INTEGER,created_at_ms INTEGER,item_type TEXT,item_json TEXT);
CREATE UNIQUE INDEX idx_thread_items_page ON thread_items(thread_id,rollout_ordinal);
CREATE INDEX idx_thread_items_updated_page ON thread_items(thread_id,updated_at_ordinal);
INSERT INTO thread_history_projection_state VALUES('metadata-only',0,0);
`)
	meta := filepath.Join(root, "metadata.sqlite")
	nativeCorpusSQL(t, meta, `CREATE TABLE threads(id TEXT PRIMARY KEY,title TEXT,cwd TEXT,model TEXT,updated_at_ms INTEGER); INSERT INTO threads VALUES('metadata-only','A raw metadata title','/workspace/fixture','fixture',1);`)
	lane := LaneConfig{Lane: "state-only-native", SourceFamily: "state_only_native_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3}, Source: &SourceConfig{Root: root, Format: "codex-history", StateDatabase: "metadata.sqlite"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	now := time.Now().UTC()
	first := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, now)
	if first.Failed() || first.Sources[0].NewEmbeddedChunks != 0 || first.Sources[0].BodyBytesRead != 0 {
		t.Fatalf("empty native history generated retrieval work: %+v", first)
	}
	m, err := sourceobs.LoadSourceManifest(hp, lane.SourceFamily)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Items) != 2 {
		t.Fatalf("native metadata was discarded or context-only artifact generated: %+v", m)
	}
	for _, item := range m.Items {
		if item.PageSlug != "" || item.BodyRef == "" {
			t.Fatalf("metadata escaped raw custody: %+v", item)
		}
	}
	nativeCorpusSQL(t, meta, "UPDATE threads SET updated_at_ms=2;")
	second := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, now)
	c := second.Sources[0]
	if second.Failed() || c.NewEmbeddedChunks != 0 || c.BodyBytesRead != 0 || c.IndexBytesWritten != 0 || !c.IndexUnchanged {
		t.Fatalf("raw metadata revision rewrote retrieval: %+v", c)
	}
}
