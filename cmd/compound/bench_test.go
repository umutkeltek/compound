package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"compound/internal/bench"
	"compound/internal/capgraph"
	"compound/internal/home"
	"compound/internal/work"
)

// TestRunBenchConvertWritesDeterministicLoadableSuite exercises the convert CLI end-to-end on
// SYNTHETIC input: it writes a byte-stable suite that bench.LoadSuite accepts, prints the
// drop-accounting to stdout, and produces byte-identical output on a rerun.
func TestRunBenchConvertWritesDeterministicLoadableSuite(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestRunBenchNoActionableArtifactRoundTrips is the loader-input half of the loop: the report the
// director's loader actually reads is the on-disk artifact under reviews/bench (NOT the --json
// stdout). A real `bench run --no-actionable` must persist actionable=false into that artifact so
// loadLatestBenchReports skips it; a default run must leave it absent (nil => actionable).
func TestRunBenchNoActionableArtifactRoundTrips(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	seedBenchGraph(t, home)
	suite := filepath.Join("..", "..", "fixtures", "bench", "capability-suite.json")

	// Default run: artifact on disk has no actionable field (nil => actionable).
	var out, errBuf bytes.Buffer
	if code := runBench([]string{"run", "--suite", suite, "--home", home, "--json"}, &out, &errBuf); code != 0 {
		t.Fatalf("default exit=%d stderr=%s", code, errBuf.String())
	}
	defArtifact := readSingleBenchArtifact(t, home)
	if !defArtifact.IsActionable() {
		t.Fatalf("default artifact must be actionable (nil => true), got %v", defArtifact.Actionable)
	}

	// --no-actionable: the SAME artifact path the director reads carries actionable=false.
	naHome := t.TempDir()
	seedBenchGraph(t, naHome)
	out.Reset()
	errBuf.Reset()
	if code := runBench([]string{"run", "--suite", suite, "--home", naHome, "--json", "--no-actionable"}, &out, &errBuf); code != 0 {
		t.Fatalf("--no-actionable exit=%d stderr=%s", code, errBuf.String())
	}
	naArtifact := readSingleBenchArtifact(t, naHome)
	if naArtifact.IsActionable() {
		t.Fatalf("non-actionable artifact must round-trip actionable=false through the loader input, got %v", naArtifact.Actionable)
	}
}

// readSingleBenchArtifact reads the one reviews/bench/*.json artifact a `bench run` writes, the
// exact file loadLatestBenchReports scans — proving the field survives the real write→read path.
func readSingleBenchArtifact(t *testing.T, home string) bench.Report {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "reviews", "bench"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one bench artifact, err=%v entries=%d", err, len(entries))
	}
	data, err := os.ReadFile(filepath.Join(home, "reviews", "bench", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var r bench.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	return r
}

// TestBenchCompareProofTokenFlowsToHarnessEditClose is the end-to-end loop proof: a real
// `bench compare --work` ACCEPT records immutable evidence and emits a proof_ref, and that EXACT
// ref, fed verbatim into the harness-edit close, is what stamps SecondOrder.ShouldHarden=true.
// A rejected compare emits no ref. A label, or a comparison of something else, refuses the close.
func TestBenchCompareProofTokenFlowsToHarnessEditClose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	homePath := t.TempDir()
	base := benchCompareFixture(t, true)
	edited := base
	edited.BuildSHA = "build-after-edit"
	regressed := benchCompareFixture(t, false)
	otherSuite := base
	otherSuite.Suite = "another-suite"
	beforePath := filepath.Join(dir, "before.json")
	afterOK := filepath.Join(dir, "ok.json")
	afterBad := filepath.Join(dir, "bad.json")
	afterOtherSuite := filepath.Join(dir, "other.json")
	for path, r := range map[string]bench.Report{beforePath: base, afterOK: edited, afterBad: regressed, afterOtherSuite: otherSuite} {
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	startEdit := func(predicate string) string {
		t.Helper()
		w, err := store.Create(work.CreateInput{Owner: "director", SuccessPredicate: predicate, TargetSurface: "capability_edges"}, now)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := store.Claim(w.ID, "codex", now); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, now); err != nil {
			t.Fatalf("advance: %v", err)
		}
		return w.ID
	}
	compare := func(wantOK bool, args ...string) bench.CompareReport {
		t.Helper()
		var out, errBuf bytes.Buffer
		code := runBench(append([]string{"compare", "--before", beforePath, "--json"}, args...), &out, &errBuf)
		if (code == 0) != wantOK {
			t.Fatalf("compare %v exit=%d stderr=%s", args, code, errBuf.String())
		}
		var report bench.CompareReport
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatalf("decode compare: %v", err)
		}
		return report
	}
	closeEdit := func(id string, refs ...string) (work.CloseResult, error) {
		t.Helper()
		return store.Close(id, work.CloseInput{
			Outcome:   "completed",
			MoveType:  "control_plane_repair",
			Summary:   "harness edit applied",
			Changed:   true,
			ProofRefs: append([]string{"gate:harness-edit-accepted"}, refs...),
		}, now)
	}
	records := func() int {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join(homePath, "work", "bench-compare"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return len(entries)
	}

	// ACCEPT with --work: exits 0, records evidence, emits the ref. The ref hardens this work.
	accepted := startEdit("apply accepted harness edit")
	acc := compare(true, "--after", afterOK, "--work", accepted, "--home", homePath)
	if !acc.Accept || acc.ProofRef == "" {
		t.Fatalf("accept must emit proof_ref, got accept=%v proof_ref=%q", acc.Accept, acc.ProofRef)
	}
	if got := records(); got != 1 {
		t.Fatalf("an accepted comparison must leave one record, got %d", got)
	}
	res, err := closeEdit(accepted, acc.ProofRef)
	if err != nil {
		t.Fatalf("close citing the recorded ref: %v", err)
	}
	if !res.Transition.SecondOrder.ShouldHarden {
		t.Fatal("the verbatim recorded proof ref must stamp should_harden=true")
	}

	// REJECT with --work: exits nonzero, keeps the evidence, emits nothing to cite.
	rejected := startEdit("apply regressed harness edit")
	if rej := compare(false, "--after", afterBad, "--work", rejected, "--home", homePath); rej.ProofRef != "" {
		t.Fatalf("rejected compare must NOT emit proof_ref, got %q", rej.ProofRef)
	}
	if got := records(); got != 2 {
		t.Fatalf("a rejected comparison must also leave a record, got %d", got)
	}

	// A candidate cannot certify itself after being rejected: both sides are the same report.
	if self := compare(false, "--before", afterBad, "--after", afterBad, "--work", rejected, "--home", homePath); self.ProofRef != "" {
		t.Fatalf("a self-comparison must NOT emit proof_ref, got %q", self.ProofRef)
	}

	// Incomparable reports carry no verdict worth recording, even with --work.
	if mismatch := compare(false, "--after", afterOtherSuite, "--work", rejected, "--home", homePath); mismatch.ProofRef != "" {
		t.Fatalf("a suite mismatch must NOT emit proof_ref, got %q", mismatch.ProofRef)
	}
	if got := records(); got != 2 {
		t.Fatalf("refused and incomparable comparisons must leave no record, got %d", got)
	}

	// ACCEPT without --work: still a verdict, but nothing a close can cite.
	if advisory := compare(true, "--after", afterOK); !advisory.Accept || advisory.ProofRef != "" {
		t.Fatalf("an unrecorded accept must emit no proof_ref, got accept=%v proof_ref=%q", advisory.Accept, advisory.ProofRef)
	}

	// The old suite label contains the right words and names no evidence. The close refuses it.
	if _, err := closeEdit(rejected, "bench_compare_accept:"+base.Suite); err == nil {
		t.Fatal("a suite label must refuse the close")
	}

	// A ref recorded for one work does not prove another.
	unrelated := startEdit("apply a different harness edit")
	if _, err := closeEdit(unrelated, acc.ProofRef); err == nil {
		t.Fatal("a comparison of another work must refuse the close")
	}

	// A completed close with NO comparison ref completes, unhardened.
	uncited := startEdit("apply harness edit")
	if res, err := closeEdit(uncited); err != nil || res.Transition.SecondOrder.ShouldHarden {
		t.Fatalf("a close without a comparison must complete unhardened: %v", err)
	}

	// A work with no target surface has nothing a comparison can prove.
	ordinary, err := store.Create(work.CreateInput{Owner: "director", SuccessPredicate: "ordinary work"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if code := runBench([]string{"compare", "--before", beforePath, "--after", afterOK, "--work", ordinary.ID, "--home", homePath, "--json"}, io.Discard, io.Discard); code == 0 {
		t.Fatal("compare --work on ordinary work must refuse")
	}
}

// A comparison of an unknown work must fail without creating a home for it.
func TestBenchCompareWorkOnMissingWorkLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := benchCompareFixture(t, true)
	edited := base
	edited.BuildSHA = "build-after-edit"
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	for path, r := range map[string]bench.Report{beforePath: base, afterPath: edited} {
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	missingHome := filepath.Join(dir, "no-such-home")
	if code := runBench([]string{"compare", "--before", beforePath, "--after", afterPath, "--work", "work_missing", "--home", missingHome, "--json"}, io.Discard, io.Discard); code == 0 {
		t.Fatal("compare --work on a missing work must fail")
	}
	if _, err := os.Stat(missingHome); !os.IsNotExist(err) {
		t.Fatalf("a failed comparison must not create a home: %v", err)
	}
}

// TestRunBenchAbsentGate is the fail-loud falsifier: an expected id absent from the graph makes
// `bench run` exit nonzero UNLESS --allow-absent is passed.
func TestRunBenchAbsentGate(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	dir := t.TempDir()
	before := benchCompareFixture(t, true)
	regressed := benchCompareFixture(t, false)
	improved := before

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
	t.Parallel()
	dir := t.TempDir()
	before := benchCompareFixture(t, true)
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

func benchCompareFixture(t *testing.T, heldOutHit bool) bench.Report {
	t.Helper()
	home := t.TempDir()
	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	trigger := "beta"
	if !heldOutHit {
		trigger = "unmatched"
	}
	for _, n := range []capgraph.Node{
		{ID: "skill:alpha", Kind: "skill", Name: "Alpha", Triggers: []string{"alpha"}},
		{ID: "skill:beta", Kind: "skill", Name: "Beta", Triggers: []string{trigger}},
	} {
		if err := store.Add(n); err != nil {
			t.Fatal(err)
		}
	}
	r, err := bench.Run(bench.Options{Home: home, Suite: bench.Suite{
		Suite: "capability-suite",
		Cases: []bench.Case{
			{ID: "in", Kind: bench.KindCapability, Task: "alpha", Expected: []string{"skill:alpha"}, Split: bench.SplitHeldIn, Lane: "exact"},
			{ID: "out", Kind: bench.KindCapability, Task: "beta", Expected: []string{"skill:beta"}, Split: bench.SplitHeldOut, Lane: "exact"},
		},
	}, Now: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Certification failures must reach the real command exit status and both output modes,
// including the explicit suite-label escape hatch.
func TestRunBenchCompareCertificationGate(t *testing.T) {
	valid := benchCompareFixture(t, true)
	variants := map[string]bench.Report{"empty": {}, "legacy": valid, "changed-inputs": valid}
	legacy := variants["legacy"]
	legacy.InputDigest = ""
	variants["legacy"] = legacy
	changed := variants["changed-inputs"]
	changed.InputDigest = strings.Repeat("a", 64)
	variants["changed-inputs"] = changed
	for name, r := range variants {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			beforePath, afterPath := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
			for path, report := range map[string]bench.Report{beforePath: valid, afterPath: r} {
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, jsonOut := range []bool{false, true} {
				args := []string{"compare", "--before", beforePath, "--after", afterPath, "--allow-suite-mismatch"}
				if jsonOut {
					args = append(args, "--json")
				}
				var out, errBuf bytes.Buffer
				if code := runBench(args, &out, &errBuf); code == 0 {
					t.Fatalf("invalid compare exited zero: %s", out.String())
				}
				if errBuf.Len() == 0 {
					t.Fatal("rejection lacks diagnostic")
				}
				if jsonOut {
					var c bench.CompareReport
					if err := json.Unmarshal(out.Bytes(), &c); err != nil {
						t.Fatal(err)
					}
					if c.Accept || c.ProofRef != "" {
						t.Fatalf("invalid input certified: %+v", c)
					}
					if len(c.InputErrors) == 0 && !c.InputMismatch {
						t.Fatalf("missing certification reason: %+v", c)
					}
				} else if !strings.Contains(out.String(), "REJECT") || strings.Contains(out.String(), "proof:") {
					t.Fatalf("misleading summary: %s", out.String())
				}
			}
		})
	}
}

func TestRunBenchCompareRejectsTruncatedDeclaredContract(t *testing.T) {
	home := t.TempDir()
	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(capgraph.Node{ID: "skill:alpha", Kind: "skill", Name: "Alpha", Triggers: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	suite := bench.Suite{Suite: "declared-coverage"}
	for _, split := range []string{bench.SplitHeldIn, bench.SplitHeldOut} {
		for _, lane := range []string{"exact", "casual"} {
			suite.Cases = append(suite.Cases, bench.Case{ID: split + "-" + lane, Kind: bench.KindCapability, Task: "alpha", Expected: []string{"skill:alpha"}, Split: split, Lane: lane})
		}
	}
	full, err := bench.Run(bench.Options{Home: home, Suite: suite, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	suite.Cases = []bench.Case{suite.Cases[0], suite.Cases[2]}
	short, err := bench.Run(bench.Options{Home: home, Suite: suite, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	short.InputDigest = full.InputDigest
	fullBytes, _ := json.Marshal(full)
	var fullMap map[string]json.RawMessage
	if err := json.Unmarshal(fullBytes, &fullMap); err != nil {
		t.Fatal(err)
	}
	shortBytes, _ := json.Marshal(short)
	var shortMap map[string]json.RawMessage
	if err := json.Unmarshal(shortBytes, &shortMap); err != nil {
		t.Fatal(err)
	}
	if contract, ok := fullMap["evaluation_contract"]; ok {
		shortMap["evaluation_contract"] = contract
	}
	shortBytes, err = json.Marshal(shortMap)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	before, after := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	writePair := func(data []byte) {
		t.Helper()
		for _, path := range []string{before, after} {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	writePair(fullBytes)
	var out, errOut bytes.Buffer
	args := []string{"compare", "--before", before, "--after", after}
	if code := runBench(args, &out, &errOut); code != 0 {
		t.Fatalf("valid full control refused: %d %s", code, errOut.String())
	}
	writePair(shortBytes)
	for _, jsonOut := range []bool{false, true} {
		out.Reset()
		errOut.Reset()
		cmd := append(append([]string{}, args...), "--allow-suite-mismatch")
		if jsonOut {
			cmd = append(cmd, "--json")
		}
		if code := runBench(cmd, &out, &errOut); code == 0 {
			t.Fatalf("truncated declared contract certified by CLI: %s", out.String())
		}
		if jsonOut {
			var c bench.CompareReport
			if err := json.Unmarshal(out.Bytes(), &c); err != nil {
				t.Fatal(err)
			}
			if c.Accept || c.ProofRef != "" || len(c.InputErrors) == 0 {
				t.Fatalf("invalid evidence certified: %+v", c)
			}
		} else if !strings.Contains(out.String(), "REJECT") {
			t.Fatalf("missing REJECT: %s", out.String())
		}
	}
}

// TestWriteBenchReportNeverReplacesEarlierEvidence: a self-improvement comparison needs its
// baseline report after the treatment run. Two same-suite reports stamped at one instant, a
// concurrent burst of writers, and two suites whose names slug to the same text must each keep
// their own file with their original bytes; rewriting byte-identical bytes stays one file.
func TestWriteBenchReportNeverReplacesEarlierEvidence(t *testing.T) {
	t.Parallel()
	homePath := t.TempDir()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	report := func(suite, build string) bench.Report {
		return bench.Report{Schema: bench.SchemaVersion, Suite: suite, BuildSHA: build, Timestamp: now.Format(time.RFC3339)}
	}
	reports := []bench.Report{report("capability", "baseline"), report("capability", "treatment"), report("Foo Bar", "x"), report("foo-bar", "x")}
	for i := range 8 {
		reports = append(reports, report("capability", fmt.Sprintf("concurrent-%d", i)))
	}
	paths := make([]string, len(reports))
	errs := make([]error, len(reports))
	var wg sync.WaitGroup
	for i, r := range reports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i], errs[i] = writeBenchReport(homePath, r, now)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, r := range reports {
		if errs[i] != nil {
			t.Fatalf("write %s/%s: %v", r.Suite, r.BuildSHA, errs[i])
		}
		if seen[paths[i]] {
			t.Fatalf("report %s/%s reused path %s", r.Suite, r.BuildSHA, paths[i])
		}
		seen[paths[i]] = true
		want, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(paths[i]); err != nil || !bytes.Equal(got, append(want, '\n')) {
			t.Fatalf("report %s/%s bytes changed at %s: err=%v\n%s", r.Suite, r.BuildSHA, paths[i], err, got)
		}
	}
	again, err := writeBenchReport(homePath, reports[0], now)
	if err != nil || again != paths[0] {
		t.Fatalf("identical rewrite = %q, %v; want %q", again, err, paths[0])
	}
	if entries, err := os.ReadDir(filepath.Join(homePath, "reviews", "bench")); err != nil || len(entries) != len(reports) {
		t.Fatalf("reviews/bench holds %d entries (err=%v), want %d", len(entries), err, len(reports))
	}
}

func TestWriteBenchReportPreservesPersistenceErrors(t *testing.T) {
	t.Parallel()
	homePath := t.TempDir()
	report := bench.Report{Schema: bench.SchemaVersion, Suite: "synthetic-durability"}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var written string
	write := func(root, path, kind string, data []byte, mode os.FileMode) error {
		if err := home.WriteRootedNewFileDurable(root, path, kind, data, mode); err != nil {
			return err
		}
		written = path
		return fmt.Errorf("final directory sync: %w", syscall.EIO)
	}
	path, err := writeBenchReportWithWriter(homePath, report, now, write)
	if written == "" {
		t.Fatal("fault was not reached after writing the report")
	}
	data, readErr := os.ReadFile(written)
	if readErr != nil || len(data) == 0 {
		t.Fatalf("readable leftover = %q, %v", data, readErr)
	}
	if path != "" || !errors.Is(err, syscall.EIO) {
		t.Fatalf("visible bytes masked persistence failure: path=%q error=%v", path, err)
	}
}

func TestWriteBenchReportRejectsSymlinkRetries(t *testing.T) {
	t.Parallel()
	for _, ancestor := range []bool{false, true} {
		t.Run(fmt.Sprintf("ancestor=%v", ancestor), func(t *testing.T) {
			homePath := t.TempDir()
			report := bench.Report{Schema: bench.SchemaVersion, Suite: "synthetic-custody"}
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			path, err := writeBenchReport(homePath, report, now)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "preserved")
			source := path
			if ancestor {
				source = filepath.Dir(path)
			}
			if err := os.Rename(source, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, source); err != nil {
				t.Fatal(err)
			}
			got, err := writeBenchReport(homePath, report, now)
			if got != "" || err == nil {
				t.Errorf("matching bytes bypassed custody: path=%q error=%v", got, err)
			}
			if ancestor {
				target = filepath.Join(target, filepath.Base(path))
			}
			after, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, data) || !os.SameFile(before, after) {
				t.Fatalf("original evidence changed: error=%v", err)
			}
		})
	}
}

func TestWriteBenchReportConflictingEvidenceAndConcurrentRetries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	report := bench.Report{Schema: bench.SchemaVersion, Suite: "synthetic-retries"}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	path, err := writeBenchReport(root, report, now)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := writeBenchReport(root, report, now)
			if err != nil || got != path {
				t.Errorf("identical concurrent retry = %q, %v", got, err)
			}
		}()
	}
	wg.Wait()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("concurrent retries replaced original inode")
	}
	different := []byte("synthetic conflicting evidence\n")
	if err := os.WriteFile(path, different, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := writeBenchReport(root, report, now)
	if got != "" || err == nil {
		t.Fatalf("conflicting evidence accepted: %q, %v", got, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, different) {
		t.Fatalf("conflicting original was overwritten: %q, %v", actual, err)
	}
}

func TestRunBenchPersistenceFailureDoesNotPrintSuccess(t *testing.T) {
	t.Parallel()
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", jsonOutput), func(t *testing.T) {
			root := t.TempDir()
			seedBenchGraph(t, root)
			args := []string{"run", "--suite", filepath.Join("..", "..", "fixtures", "bench", "capability-suite.json"), "--home", root, "--no-actionable"}
			if jsonOutput {
				args = append(args, "--json")
			}
			var out, errs bytes.Buffer
			if code := runBench(args, &out, &errs); code != 0 || out.Len() == 0 {
				t.Fatalf("valid control exit=%d out=%s err=%s", code, &out, &errs)
			}
			parent := filepath.Join(root, "reviews", "bench")
			saved := filepath.Join(t.TempDir(), "preserved-reports")
			if err := os.Rename(parent, saved); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(saved)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("valid control produced %d reports", len(entries))
			}
			originalPath := filepath.Join(saved, entries[0].Name())
			original, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(saved, parent); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			errs.Reset()
			if code := runBench(args, &out, &errs); code == 0 || out.Len() != 0 || !strings.Contains(errs.String(), "symlink") {
				t.Fatalf("persistence refusal exit=%d out=%s err=%s", code, &out, &errs)
			}
			entries, err = os.ReadDir(saved)
			actual, readErr := os.ReadFile(originalPath)
			if err != nil || readErr != nil || len(entries) != 1 || !bytes.Equal(actual, original) {
				t.Fatalf("original reports changed: %v %v", err, readErr)
			}
		})
	}
}
