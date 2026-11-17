package corpus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

func TestRefreshMultipleRootsPreservesMirrorsAndDivergence(t *testing.T) {
	hp := t.TempDir()
	one := t.TempDir()
	two := t.TempDir()
	native := func(text string) string {
		return "{\"type\":\"user\",\"timestamp\":\"synthetic-time\",\"message\":{\"role\":\"user\",\"content\":" + strconvQuote(text) + "}}\n"
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

func TestRootInventoryDescendantCannotHoldRefreshPipeOpen(t *testing.T) {
	fixture := t.TempDir()
	script := filepath.Join(fixture, "inventory.sh")
	pidFile := filepath.Join(fixture, "descendant.pid")
	// WaitDelay bounds the caller, but the intentionally inherited-pipe fixture
	// must also observe its own bounded child exit before returning to a runner.
	// Signal 0 is read-only; no process is terminated and a recycled PID can
	// only make this cleanup fail closed, never signal unrelated work.
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			t.Error("invalid owned fixture descendant identity")
			return
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			err := syscall.Kill(pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				return
			}
			if time.Now().After(deadline) {
				t.Error("owned inventory fixture descendant did not exit")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	quotedPID := "'" + strings.ReplaceAll(pidFile, "'", "'\"'\"'") + "'"
	body := "#!/bin/sh\nsleep 1 &\nprintf '%s' \"$!\" > " + quotedPID + "\nprintf '%s' '{\"roots\":[]}'\n"

	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := SourceConfig{Format: "claude", RootsCommand: []string{script}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := cfg.approvedRoots(ctx); err == nil {
		t.Fatal("inherited pipe was accepted as a completed inventory")
	}
	if time.Since(started) > 900*time.Millisecond {
		t.Fatal("inherited stdout consumed the descendant lifetime")
	}
}
