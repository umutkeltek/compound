package corpus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

func TestRefreshMultipleRootsPreservesMirrorsAndDivergence(t *testing.T) {
	hp := t.TempDir()
	one := t.TempDir()
	two := t.TempDir()
	native := func(text string) string {
		return "{\"type\":\"user\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":" + strconvQuote(text) + "}}\n"
	}
	writeRaw(t, filepath.Join(one, "session.jsonl"), native("The synthetic comet archive has violet labels."))
	writeRaw(t, filepath.Join(two, "session.jsonl"), native("The synthetic comet archive has violet labels."))
	lane := LaneConfig{Lane: "multi-root", SourceFamily: "multi-root", Privacy: "private", Source: &SourceConfig{Root: one, Roots: []string{two}, Format: "claude"}, Embedder: EmbedderConfig{Provider: "local", Model: "synthetic", Dims: 3, Bits: "f32"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: 3}, nil
	}
	report := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	if report.Failed() {
		t.Fatalf("%+v", report)
	}
	m, err := readManifest(IndexDir(hp, lane))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Items) != 2 || m.Items[0].SourceKey == m.Items[1].SourceKey {
		t.Fatalf("account roots collided: %+v", m.Items)
	}
	total := len(manifestChunkHashes(m))
	if report.Sources[0].NewEmbeddedChunks >= total {
		t.Fatal("mirrored account history embedded redundantly")
	}
	writeRaw(t, filepath.Join(two, "session.jsonl"), native("The independent branch now records amber labels."))
	report = Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	if report.Failed() {
		t.Fatalf("%+v", report)
	}
	e, _ := factory(lane)
	q, err := QueryWithOptions(context.Background(), hp, lane, e, "amber", 20, QueryOptions{IncludeText: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range q.Hits {
		found = found || strings.Contains(h.Text, "amber")
	}
	if !found {
		t.Fatal("previously excluded account is not retrievable")
	}
	if b, err := os.ReadFile(filepath.Join(one, "session.jsonl")); err != nil || strings.Contains(string(b), "amber") {
		t.Fatal("ingestion changed native original")
	}
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func TestRootInventoryIsExplicitBoundedAndReconciled(t *testing.T) {
	root := t.TempDir()
	inventory, _ := json.Marshal(map[string]any{"roots": []string{root}})
	script := filepath.Join(t.TempDir(), "inventory.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '"+string(inventory)+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := SourceConfig{Format: "claude", RootsCommand: []string{script}}
	roots, err := cfg.approvedRoots(context.Background())
	if err != nil || len(roots) != 1 || roots[0] != root {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '{\"roots\":[\"relative\"]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.approvedRoots(context.Background()); err == nil {
		t.Fatal("relative inventory root accepted")
	}
}
func TestSourceDeletionPreservesOwnedSearchAndReportsMissing(t *testing.T) {
	hp, root, lane := incrementalFixture(t, 1)
	lane.Source = &SourceConfig{Root: root, Format: "markdown"}
	runIncremental(t, hp, lane)
	if err := os.Remove(filepath.Join(root, "session-000.md")); err != nil {
		t.Fatal(err)
	}
	capture, err := sourceobs.CaptureApprovedDirectoryBatch(context.Background(), hp, lane.SourceFamily, root, "markdown", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if capture.Missing != 1 {
		t.Fatalf("deletion not reported: %+v", capture)
	}
	r := runIncremental(t, hp, lane)
	if r.Items != 1 || !r.Unchanged {
		t.Fatalf("source deletion discarded custodied knowledge: %+v", r)
	}
}
