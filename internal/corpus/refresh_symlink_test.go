package corpus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomaticRefreshRefusesLinkedTranscriptWithoutBlockingRegularHistory(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(body, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "regular.jsonl"), "We chose Cedar archive.")
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	write(outside, "OUTSIDE_PRIVATE_TRANSCRIPT")
	if err := os.Symlink(outside, filepath.Join(root, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	lane := LaneConfig{Lane: "dialogue", SourceFamily: "dialogue", Privacy: "private",
		Embedder: EmbedderConfig{Provider: "ollama", Model: "synthetic", Dims: 3},
		Source:   &SourceConfig{Root: root, Format: "claude"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}, nil
	}
	refreshed := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	if refreshed.Status != "degraded" || len(refreshed.Sources) != 1 || refreshed.Sources[0].Status != "capture_incomplete" ||
		refreshed.Sources[0].Items != 1 || refreshed.Sources[0].Chunks != 1 {
		t.Fatalf("regular history blocked or partial coverage hidden: %+v", refreshed)
	}
	checkRefusal := func(c Coverage) {
		t.Helper()
		body, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var public map[string]any
		if err = json.Unmarshal(body, &public); err != nil {
			t.Fatal(err)
		}
		if public["refused_symlinks"] != float64(1) {
			t.Fatalf("missing visible symlink refusal: %s", body)
		}
	}
	checkRefusal(refreshed.Sources[0])
	if refreshed.Failed() {
		t.Fatalf("a refused symlink is a visible limit, not a failed refresh: %+v", refreshed)
	}
	found := Search(context.Background(), hp, []LaneConfig{lane}, factory, "Which archive did we choose?", 5, QueryOptions{IncludeText: true})
	if len(found.Evidence) != 1 || !strings.Contains(found.Evidence[0].Text, "Cedar") ||
		strings.Contains(found.Evidence[0].Text, "OUTSIDE_PRIVATE_TRANSCRIPT") {
		t.Fatalf("regular recall missing or symlink followed: %+v", found.Evidence)
	}
	checkRefusal(found.Coverage[0])
	failed := Refresh(context.Background(), hp, []LaneConfig{lane}, func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 2}, nil
	}, time.Now())
	if failed.Sources[0].Status != "index_failed" {
		t.Fatalf("refused-link reporting masked indexing failure: %+v", failed)
	}
	checkRefusal(failed.Sources[0])
}
