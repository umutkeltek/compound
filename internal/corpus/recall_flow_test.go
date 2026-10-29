package corpus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecallFlowCaptureCorrectionRestartAndCoverage(t *testing.T) {
	h, root := t.TempDir(), t.TempDir()
	lane := LaneConfig{Lane: "conversations", SourceFamily: "conversation_fixture", Privacy: "private",
		Embedder: EmbedderConfig{Provider: "local", Model: "fixture", Dims: 3},
		Source:   &SourceConfig{Root: root, Format: "codex"}}
	path := filepath.Join(root, "session.jsonl")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"We selected Cedar for the archive.\"}]}}\n")
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	r := Refresh(context.Background(), h, []LaneConfig{lane}, factory, time.Now())
	if r.Status != "ok" {
		t.Fatalf("refresh: %+v", r)
	}
	search := func(opts QueryOptions) RecallReport {
		return Search(context.Background(), h, []LaneConfig{lane}, factory, "archive decision", 5, opts)
	}
	hidden := search(QueryOptions{})
	if len(hidden.Evidence) != 1 || hidden.Evidence[0].Text != "" {
		t.Fatalf("privacy: %+v", hidden)
	}
	first := search(QueryOptions{IncludeText: true})
	if len(first.Evidence) != 1 || !strings.Contains(first.Evidence[0].Text, "Cedar") {
		t.Fatalf("initial: %+v", first)
	}
	oldID := first.Evidence[0].ItemID
	write("{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Correction: we now use Birch for the archive, replacing Cedar.\"}]}}\n")
	r = Refresh(context.Background(), h, []LaneConfig{lane}, factory, time.Now())
	if r.Status != "ok" {
		t.Fatalf("update: %+v", r)
	}
	latest := search(QueryOptions{IncludeText: true})
	if len(latest.Evidence) != 1 || latest.Evidence[0].ItemID == oldID || !strings.Contains(latest.Evidence[0].Text, "Birch") {
		t.Fatalf("latest: %+v", latest)
	}
	history := search(QueryOptions{IncludeText: true, IncludeHistory: true})
	if len(history.Evidence) != 2 {
		t.Fatalf("history: %+v", history)
	}
	// A new reader must use owned snapshots after loss of the original file,
	// while refresh reports loss instead of claiming up-to-date coverage.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	survived := search(QueryOptions{IncludeText: true})
	if len(survived.Evidence) != 1 {
		t.Fatalf("restart/custody: %+v", survived)
	}
	failed := Refresh(context.Background(), h, []LaneConfig{lane}, factory, time.Now())
	if failed.Status == "ok" {
		t.Fatalf("source loss hidden: %+v", failed)
	}
	missing := lane
	missing.Lane = "missing"
	missing.SourceFamily = "missing"
	partial := Search(context.Background(), h, []LaneConfig{lane, missing}, factory, "archive", 5, QueryOptions{IncludeText: true})
	if partial.Status != "degraded" || len(partial.Coverage) != 2 || len(partial.Evidence) == 0 {
		t.Fatalf("coverage: %+v", partial)
	}
}

func TestTranscriptRetrievalExcludesToolsAndLabelsSpeakers(t *testing.T) {
	body := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"Keep the archive private.\"}]}}\n" +
		"{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"tool_use\",\"input\":{\"secret\":\"TOOL_SECRET\"}},{\"type\":\"text\",\"text\":\"I will keep it private.\"}]}}\n"
	got, err := conversationText("claude", body)
	if err != nil || !strings.Contains(got, "user:") || !strings.Contains(got, "assistant:") || strings.Contains(got, "TOOL_SECRET") {
		t.Fatalf("%q %v", got, err)
	}
	if _, err = conversationText("codex", "{\"broken\":\n"); err == nil {
		t.Fatal("malformed input must fail")
	}
}

type cancelEmbedder struct{ identity string }

func (e cancelEmbedder) Identity() string { return e.identity }
func (e cancelEmbedder) Dim() int         { return 3 }
func (e cancelEmbedder) Embed(ctx context.Context, _ []string) ([][]float32, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestQueryPropagatesCancellation(t *testing.T) {
	h := t.TempDir()
	f := seedOwnedSnapshotLane(t, h, "Cancellation fixture")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := QueryWithOptions(ctx, h, f.lane, cancelEmbedder{f.lane.Embedder.Identity()}, "question", 1, QueryOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}
