package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"compound/internal/bench"
	"compound/internal/capgraph"
)

func seedBenchGraph(t *testing.T, home string) {
	t.Helper()
	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatalf("open capgraph: %v", err)
	}
	nodes := []capgraph.Node{
		{ID: "skill:pricing", Kind: "skill", Name: "Pricing", Triggers: []string{"pricing", "paywall", "monetization"}},
		{ID: "skill:paywall", Kind: "skill", Name: "Paywall Design", Triggers: []string{"paywall", "upgrade", "subscription"}},
		{ID: "tool:revenuecat", Kind: "tool", Name: "RevenueCat", Triggers: []string{"subscription", "receipt", "entitlement"}},
		{ID: "lens:reasoning", Kind: "lens", Name: "Reasoning Lenses", Triggers: []string{"inversion", "tradeoff", "decision"}},
		{ID: "procedure:migration", Kind: "procedure", Name: "DB Migration", Triggers: []string{"migration", "schema", "rollback"}},
		{ID: "skill:security-review", Kind: "skill", Name: "Security Review", Triggers: []string{"security", "auth", "injection"}},
		{ID: "tool:agent-browser", Kind: "tool", Name: "Agent Browser", Triggers: []string{"browser", "automation", "scrape"}},
		{ID: "skill:ios-concurrency", Kind: "skill", Name: "Swift Concurrency", Triggers: []string{"swift", "concurrency", "actor"}},
		{ID: "procedure:release", Kind: "procedure", Name: "App Store Release", Triggers: []string{"release", "submission", "store"}},
		{ID: "lens:cost", Kind: "lens", Name: "Unit Economics", Triggers: []string{"margin", "cac", "pricing"}},
	}
	for _, n := range nodes {
		if err := store.Add(n); err != nil {
			t.Fatalf("add %s: %v", n.ID, err)
		}
	}
}

func writeSuite(t *testing.T, path string, s bench.Suite) {
	t.Helper()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunBenchHappyPathExitsZeroAndWritesArtifact: the committed public fixture over a matching
// graph exits 0, emits parseable --json, and persists a report under COMPOUND_HOME/reviews/bench.
func TestRunBenchHappyPathExitsZeroAndWritesArtifact(t *testing.T) {
	home := t.TempDir()
	seedBenchGraph(t, home)
	var out, errBuf bytes.Buffer
	code := runBench([]string{"run", "--suite", filepath.Join("..", "..", "fixtures", "bench", "capability-suite.json"), "--home", home, "--json"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errBuf.String())
	}
	var report bench.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("parse report json: %v\n%s", err, out.String())
	}
	if report.Overall.Hit1 != 1.0 {
		t.Fatalf("overall hit@1 = %v, want 1.0", report.Overall.Hit1)
	}
	entries, err := os.ReadDir(filepath.Join(home, "reviews", "bench"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one report artifact under reviews/bench, err=%v entries=%v", err, entries)
	}
}

// TestRunBenchAbsentGate is the fail-loud falsifier: an expected id absent from the graph makes
// `bench run` exit nonzero UNLESS --allow-absent is passed.
func TestRunBenchAbsentGate(t *testing.T) {
	home := t.TempDir()
	seedBenchGraph(t, home)
	suitePath := filepath.Join(t.TempDir(), "absent-suite.json")
	writeSuite(t, suitePath, bench.Suite{
		Suite: "absent-suite",
		Cases: []bench.Case{
			{ID: "present", Kind: bench.KindCapability, Task: "pricing paywall", Expected: []string{"skill:pricing"}, Split: bench.SplitHeldIn, Lane: "exact"},
			{ID: "absent", Kind: bench.KindCapability, Task: "pricing", Expected: []string{"skill:ghost"}, Split: bench.SplitHeldOut, Lane: "exact"},
		},
	})

	var out, errBuf bytes.Buffer
	if code := runBench([]string{"run", "--suite", suitePath, "--home", home, "--json"}, &out, &errBuf); code == 0 {
		t.Fatalf("expected nonzero exit on absent must-hit id, got 0\nstderr=%s", errBuf.String())
	}

	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"run", "--suite", suitePath, "--home", home, "--json", "--allow-absent"}, &out, &errBuf); code != 0 {
		t.Fatalf("expected exit 0 with --allow-absent, got %d\nstderr=%s", code, errBuf.String())
	}
}

// TestRunBenchCompareRegressionGate is the compare falsifier: a fabricated held_out-only
// regression makes `bench compare` exit nonzero; an improvement exits zero.
func TestRunBenchCompareRegressionGate(t *testing.T) {
	dir := t.TempDir()
	before := bench.Report{
		Suite:   "capability-suite",
		Splits:  map[string]bench.Metrics{bench.SplitHeldIn: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1}, bench.SplitHeldOut: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1}},
		Overall: bench.Metrics{Cases: 10, Hit1: 1, Hit5: 1, MRR: 1},
	}
	regressed := before
	regressed.Splits = map[string]bench.Metrics{
		bench.SplitHeldIn:  {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1},
		bench.SplitHeldOut: {Cases: 5, Hit1: 0.6, Hit5: 0.6, MRR: 0.5},
	}
	improved := before
	improved.Splits = map[string]bench.Metrics{
		bench.SplitHeldIn:  {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1},
		bench.SplitHeldOut: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1},
	}

	beforePath := filepath.Join(dir, "before.json")
	regressedPath := filepath.Join(dir, "regressed.json")
	improvedPath := filepath.Join(dir, "improved.json")
	for path, r := range map[string]bench.Report{beforePath: before, regressedPath: regressed, improvedPath: improved} {
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out, errBuf bytes.Buffer
	if code := runBench([]string{"compare", "--before", beforePath, "--after", regressedPath, "--json"}, &out, &errBuf); code == 0 {
		t.Fatalf("expected nonzero exit on held_out regression, got 0")
	}
	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"compare", "--before", beforePath, "--after", improvedPath, "--json"}, &out, &errBuf); code != 0 {
		t.Fatalf("expected exit 0 on no regression, got %d\nstderr=%s", code, errBuf.String())
	}
}
