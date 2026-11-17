package corpus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fairRefreshLane(t *testing.T, name string) LaneConfig {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("The archive uses Cedar for recoverable history."), 0600); err != nil {
		t.Fatal(err)
	}
	return LaneConfig{Lane: name, SourceFamily: name, Privacy: "private",
		Embedder: EmbedderConfig{Provider: "ollama", Model: "synthetic", Dims: 3},
		Source:   &SourceConfig{Root: root, Format: "markdown"}}
}

func TestRefreshBudgetDoesNotStarveLaterSource(t *testing.T) {
	home, now := t.TempDir(), time.Now()
	lanes := []LaneConfig{fairRefreshLane(t, "first"), fairRefreshLane(t, "second")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := Refresh(ctx, home, lanes, func(l LaneConfig) (Embedder, error) {
		cancel()
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}, nil
	}, now)
	if len(first.Sources) != 2 || first.Sources[1].Status != "cancelled" {
		t.Fatalf("budget did not stop second source: %+v", first)
	}
	if _, err := os.Stat(refreshPath(home, lanes[1])); !os.IsNotExist(err) {
		t.Fatalf("unattempted source got a receipt: %v", err)
	}
	next := Refresh(context.Background(), home, lanes, func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}, nil
	}, now.Add(time.Second))
	if next.Sources[0].Lane != "second" || next.Sources[0].IndexedAt == "" {
		t.Fatalf("next pass did not index skipped source first: %+v", next)
	}
	if lanes[0].Lane != "first" {
		t.Fatal("changed caller configuration order")
	}
}

func TestCancelledRefreshPreservesLastObservedCoverage(t *testing.T) {
	home, now := t.TempDir(), time.Now()
	lane := fairRefreshLane(t, "fixture")
	path := refreshPath(home, lane)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	previous := Coverage{Lane: lane.Lane, Status: "ok", CheckedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), IndexedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Items: 7}
	if err := writeJSONFile(path, previous); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Refresh(ctx, home, []LaneConfig{lane}, func(LaneConfig) (Embedder, error) { t.Fatal("cancelled source invoked backend"); return nil, nil }, now)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("cancelled source overwrote successful coverage")
	}
	if !r.Failed() || r.Sources[0].CheckedAt != "" {
		t.Fatalf("cancellation was presented as an observation: %+v", r)
	}
}

func TestInterruptedRefreshDoesNotMonopolizeNextPass(t *testing.T) {
	home, now := t.TempDir(), time.Now()
	lanes := []LaneConfig{fairRefreshLane(t, "first"), fairRefreshLane(t, "second")}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fixture interruption did not occur")
			}
		}()
		Refresh(context.Background(), home, lanes, func(LaneConfig) (Embedder, error) {
			panic("fixture interruption before final receipt")
		}, now)
	}()
	var prior Coverage
	if err := readJSONFile(refreshPath(home, lanes[0]), &prior); err != nil {
		t.Fatal(err)
	}
	if prior.Status != "refreshing" || prior.CheckedAt != "" || prior.LastAttemptAt == "" {
		t.Fatalf("start was not recorded separately from success: %+v", prior)
	}
	next := Refresh(context.Background(), home, lanes, func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}, nil
	}, now.Add(time.Second))
	if next.Sources[0].Lane != "second" || next.Sources[0].IndexedAt == "" {
		t.Fatalf("interrupted source monopolized next pass: %+v", next)
	}
}

type cutRefreshEmbedder struct{ *fakeEmbedder }

func (e *cutRefreshEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, context.DeadlineExceeded
}

func TestRefreshRetriesCutSourceBeforeCompletedTie(t *testing.T) {
	hp, now := t.TempDir(), time.Now().UTC()
	lanes := []LaneConfig{fairRefreshLane(t, "completed"), fairRefreshLane(t, "cut-short")}
	first := Refresh(context.Background(), hp, lanes, func(l LaneConfig) (Embedder, error) {
		base := &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}
		if l.Lane == "cut-short" {
			return &cutRefreshEmbedder{base}, nil
		}
		return base, nil
	}, now)
	if !first.Failed() || first.Sources[1].Status != "index_failed" {
		t.Fatalf("deadline lost its real failure status: %+v", first)
	}
	if first.Sources[0].LastAttemptAt != first.Sources[1].LastAttemptAt {
		t.Fatal("fixture must reproduce the shared pass timestamp")
	}
	next := Refresh(context.Background(), hp, lanes, func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}, nil
	}, now.Add(time.Second))
	if next.Failed() || next.Sources[0].Lane != "cut-short" || next.Sources[0].IndexedAt == "" {
		t.Fatalf("cut source did not recover before completed source: %+v", next)
	}
	if lanes[0].Lane != "completed" {
		t.Fatal("mutated caller order")
	}
}
