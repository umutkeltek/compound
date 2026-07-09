package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/bench"
	"compound/internal/capgraph"
)

// TestRunBenchConvertWritesDeterministicLoadableSuite exercises the convert CLI end-to-end on
// SYNTHETIC input: it writes a byte-stable suite that bench.LoadSuite accepts, prints the
// drop-accounting to stdout, and produces byte-identical output on a rerun.
func TestRunBenchConvertWritesDeterministicLoadableSuite(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "acceptance.json")
	syn := `{"schema_version":1,"class_cases":[{"prompt":"c","expected_class":"x"}],` +
		`"cases":[{"id":"case-b","prompt":"b prompt","selected_rule":"r","required_top_n":{"n":5,"ids":["node:beta"]},"required_policy_for":["node:beta"],"forbidden_top_n_contains":{"n":3,"contains":["zeta"]}},` +
		`{"id":"case-a","prompt":"a prompt","selected_rule":"r","required_anywhere":["node:alpha"]}]}`
	if err := os.WriteFile(inPath, []byte(syn), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "suite.json")

	var out, errBuf bytes.Buffer
	if code := runBench([]string{"convert", "acceptance", "--in", inPath, "--out", outPath, "--name", "syn"}, &out, &errBuf); code != 0 {
		t.Fatalf("convert exit=%d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "required_policy_for") || !strings.Contains(out.String(), "class_cases") {
		t.Fatalf("convert summary missing drop-accounting:\n%s", out.String())
	}
	suite, err := bench.LoadSuite(outPath)
	if err != nil {
		t.Fatalf("converted suite does not load: %v", err)
	}
	if len(suite.Cases) != 2 {
		t.Fatalf("converted cases = %d, want 2", len(suite.Cases))
	}

	first, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"convert", "acceptance", "--in", inPath, "--out", outPath, "--name", "syn"}, &out, &errBuf); code != 0 {
		t.Fatalf("convert rerun exit=%d stderr=%s", code, errBuf.String())
	}
	second, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("convert output not byte-identical across runs")
	}
}

// TestRunBenchConvertIDMapDefaultsToV2 exercises the CLI --id-map surface end-to-end: the default
// (no --id-map flag) applies v2, an explicit --id-map v1 applies only the original three shapes
// (no v2 literal overrides), and both print their applied version in the convert summary.
func TestRunBenchConvertIDMapDefaultsToV2(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "acceptance.json")
	syn := `{"schema_version":1,"class_cases":[],` +
		`"cases":[{"id":"case-mcporter","prompt":"mcporter prompt","required_anywhere":["tool.mcporter"]}]}`
	if err := os.WriteFile(inPath, []byte(syn), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default (no --id-map): v2 fires, tool.mcporter -> adapter:mcporter (single target, not the pair).
	defaultOut := filepath.Join(dir, "default.json")
	var out, errBuf bytes.Buffer
	if code := runBench([]string{"convert", "acceptance", "--in", inPath, "--out", defaultOut, "--name", "syn"}, &out, &errBuf); code != 0 {
		t.Fatalf("convert default exit=%d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "id-map: v2") {
		t.Fatalf("convert summary = %q, want id-map: v2 (default)", out.String())
	}
	defaultSuite, err := bench.LoadSuite(defaultOut)
	if err != nil {
		t.Fatalf("default suite does not load: %v", err)
	}
	if got := defaultSuite.Cases[0].Expected; len(got) != 1 || got[0] != "adapter:mcporter" {
		t.Fatalf("default (v2) case-mcporter expected = %v, want [adapter:mcporter]", got)
	}

	// Explicit --id-map v1: only the generic tool-pair rule fires, no v2 override.
	v1Out := filepath.Join(dir, "v1.json")
	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"convert", "acceptance", "--in", inPath, "--out", v1Out, "--name", "syn", "--id-map", "v1"}, &out, &errBuf); code != 0 {
		t.Fatalf("convert v1 exit=%d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "id-map: v1") {
		t.Fatalf("convert summary = %q, want id-map: v1", out.String())
	}
	v1Suite, err := bench.LoadSuite(v1Out)
	if err != nil {
		t.Fatalf("v1 suite does not load: %v", err)
	}
	if got := v1Suite.Cases[0].Expected; len(got) != 2 || got[0] != "mcp:mcporter" || got[1] != "tool:mcporter" {
		t.Fatalf("v1 case-mcporter expected = %v, want [mcp:mcporter tool:mcporter] (v2 override must not leak into v1)", got)
	}
}

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

// TestRunBenchNoActionableStampsReport: --no-actionable marks the report non-actionable so the
// director will not mine a synthetic/diagnostic run, and --suite-kind propagates to the report
// label. Without either flag the field stays absent (nil => actionable back-compat).
func TestRunBenchNoActionableStampsReport(t *testing.T) {
	home := t.TempDir()
	seedBenchGraph(t, home)
	suite := filepath.Join("..", "..", "fixtures", "bench", "capability-suite.json")

	var out, errBuf bytes.Buffer
	if code := runBench([]string{"run", "--suite", suite, "--home", home, "--json"}, &out, &errBuf); code != 0 {
		t.Fatalf("default exit=%d stderr=%s", code, errBuf.String())
	}
	var def bench.Report
	if err := json.Unmarshal(out.Bytes(), &def); err != nil {
		t.Fatalf("parse default report json: %v\n%s", err, out.String())
	}
	if !def.IsActionable() || def.SuiteKind != "" {
		t.Fatalf("default run must be actionable with no suite_kind (back-compat): actionable=%v kind=%q", def.Actionable, def.SuiteKind)
	}

	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"run", "--suite", suite, "--home", home, "--json", "--no-actionable", "--suite-kind", "synthetic_fixture"}, &out, &errBuf); code != 0 {
		t.Fatalf("--no-actionable exit=%d stderr=%s", code, errBuf.String())
	}
	var na bench.Report
	if err := json.Unmarshal(out.Bytes(), &na); err != nil {
		t.Fatalf("parse no-actionable report json: %v\n%s", err, out.String())
	}
	if na.IsActionable() {
		t.Fatalf("--no-actionable must stamp actionable=false, got %v", na.Actionable)
	}
	if na.SuiteKind != "synthetic_fixture" {
		t.Fatalf("--suite-kind must propagate to report, got %q", na.SuiteKind)
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

// TestRunBenchCompareSuiteMismatchGate is the apples-to-oranges falsifier: comparing two DIFFERENT
// suites (equal metrics, no regression) must exit nonzero and report accept=false — a gate that can
// accept a mismatched comparison is not trustworthy. --allow-suite-mismatch records the delta past
// the gate (exit 0) but Accept still reports the semantic truth (false).
func TestRunBenchCompareSuiteMismatchGate(t *testing.T) {
	dir := t.TempDir()
	before := bench.Report{
		Suite:   "capability-suite",
		Splits:  map[string]bench.Metrics{bench.SplitHeldIn: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1}, bench.SplitHeldOut: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1}},
		Overall: bench.Metrics{Cases: 10, Hit1: 1, Hit5: 1, MRR: 1},
	}
	after := before              // identical metrics...
	after.Suite = "memory-suite" // ...but a DIFFERENT suite
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	for path, r := range map[string]bench.Report{beforePath: before, afterPath: after} {
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out, errBuf bytes.Buffer
	if code := runBench([]string{"compare", "--before", beforePath, "--after", afterPath, "--json"}, &out, &errBuf); code == 0 {
		t.Fatalf("expected nonzero exit on suite mismatch with equal metrics, got 0\nstderr=%s", errBuf.String())
	}
	var cmp bench.CompareReport
	if err := json.Unmarshal(out.Bytes(), &cmp); err != nil {
		t.Fatalf("decode compare json: %v\nraw=%s", err, out.String())
	}
	if !cmp.SuiteMismatch {
		t.Fatalf("expected suite_mismatch=true, got false")
	}
	if cmp.Accept {
		t.Fatalf("suite mismatch must force accept=false, got true")
	}
	if cmp.Regressed {
		t.Fatalf("equal metrics must not report regressed=true: %+v", cmp)
	}

	// Escape hatch: the operator may record the delta past the gate, but Accept stays false.
	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"compare", "--before", beforePath, "--after", afterPath, "--json", "--allow-suite-mismatch"}, &out, &errBuf); code != 0 {
		t.Fatalf("expected exit 0 with --allow-suite-mismatch, got %d\nstderr=%s", code, errBuf.String())
	}
	if err := json.Unmarshal(out.Bytes(), &cmp); err != nil {
		t.Fatalf("decode compare json (allow): %v\nraw=%s", err, out.String())
	}
	if !cmp.SuiteMismatch || cmp.Accept {
		t.Fatalf("--allow-suite-mismatch must keep suite_mismatch=true/accept=false, got mismatch=%v accept=%v", cmp.SuiteMismatch, cmp.Accept)
	}
}
