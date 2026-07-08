package bench

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"compound/internal/capgraph"
)

// fixtureNode is a seed node; ids MUST match fixtures/bench/capability-suite.json so the
// committed suite scores real hits against the graph these tests build.
type fixtureNode struct {
	id       string
	kind     string
	name     string
	triggers []string
	retired  bool
}

func fixtureNodes() []fixtureNode {
	return []fixtureNode{
		{"skill:pricing", "skill", "Pricing", []string{"pricing", "paywall", "monetization"}, false},
		{"skill:paywall", "skill", "Paywall Design", []string{"paywall", "upgrade", "subscription"}, false},
		{"tool:revenuecat", "tool", "RevenueCat", []string{"subscription", "receipt", "entitlement"}, false},
		{"lens:reasoning", "lens", "Reasoning Lenses", []string{"inversion", "tradeoff", "decision"}, false},
		{"procedure:migration", "procedure", "DB Migration", []string{"migration", "schema", "rollback"}, false},
		{"skill:security-review", "skill", "Security Review", []string{"security", "auth", "injection"}, false},
		{"tool:agent-browser", "tool", "Agent Browser", []string{"browser", "automation", "scrape"}, false},
		{"skill:ios-concurrency", "skill", "Swift Concurrency", []string{"swift", "concurrency", "actor"}, false},
		{"procedure:release", "procedure", "App Store Release", []string{"release", "submission", "store"}, false},
		{"lens:cost", "lens", "Unit Economics", []string{"margin", "cac", "pricing"}, false},
	}
}

func seedGraph(t *testing.T, home string, nodes []fixtureNode) {
	t.Helper()
	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatalf("open capgraph: %v", err)
	}
	for _, n := range nodes {
		if err := store.Add(capgraph.Node{ID: n.id, Kind: n.kind, Name: n.name, Triggers: n.triggers, Retired: n.retired}); err != nil {
			t.Fatalf("add %s: %v", n.id, err)
		}
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) }

// TestRunSeparatesAbsentFromRankedMiss is the CRITICAL-distinction falsifier: an expected id
// that isn't in the graph at all (absent_from_graph) must be reported distinctly from an
// expected id that IS present but the engine failed to rank (a ranked miss). A scorer that only
// measures hit@k conflates the two and reports both as rank 0 — this test fails red on that.
func TestRunSeparatesAbsentFromRankedMiss(t *testing.T) {
	home := t.TempDir()
	seedGraph(t, home, fixtureNodes())

	suite := Suite{
		Suite: "distinction",
		Cases: []Case{
			// present AND ranked: skill:pricing tops "pricing".
			{ID: "hit", Kind: KindCapability, Task: "pricing paywall", Expected: []string{"skill:pricing"}, Split: SplitHeldIn, Lane: "exact"},
			// present but UNRANKED for this task: skill:pricing exists, "migration schema" won't surface it -> ranked miss, NOT absent.
			{ID: "ranked-miss", Kind: KindCapability, Task: "migration schema rollback", Expected: []string{"skill:pricing"}, Split: SplitHeldIn, Lane: "graph"},
			// ABSENT: skill:ghost was never added -> parity gap.
			{ID: "absent", Kind: KindCapability, Task: "pricing", Expected: []string{"skill:ghost"}, Split: SplitHeldOut, Lane: "exact"},
		},
	}
	report, err := Run(Options{Home: home, Suite: suite, BuildSHA: "test", Now: fixedNow()})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := report.AbsentFromGraph; !reflect.DeepEqual(got, []string{"skill:ghost"}) {
		t.Fatalf("absent_from_graph = %v, want [skill:ghost] only (ranked miss must NOT be counted absent)", got)
	}
	byID := map[string]CaseResult{}
	for _, c := range report.Cases {
		byID[c.ID] = c
	}
	if er := byID["hit"].Expected[0]; er.AbsentFromGraph || !er.Hit || er.Rank != 1 {
		t.Fatalf("hit case expected = %+v, want present ranked #1", er)
	}
	if er := byID["ranked-miss"].Expected[0]; er.AbsentFromGraph || er.Hit || er.Rank != 0 {
		t.Fatalf("ranked-miss case expected = %+v, want present, unranked, NOT absent", er)
	}
	if er := byID["absent"].Expected[0]; !er.AbsentFromGraph || er.Hit {
		t.Fatalf("absent case expected = %+v, want absent_from_graph=true", er)
	}
}

// TestRunDeterministic falsifies nondeterminism: two runs over the same fixture home must
// produce byte-identical metrics, per-case results, absent list, and graph hash. The suite
// carries MULTIPLE absent ids so the absent-list ordering (sortedKeys) is exercised — Go
// randomizes map iteration, so an unsorted absent list would differ across the two runs.
func TestRunDeterministic(t *testing.T) {
	home := t.TempDir()
	seedGraph(t, home, fixtureNodes())
	suite := Suite{
		Suite: "determinism",
		Cases: []Case{
			{ID: "hit-1", Kind: KindCapability, Task: "pricing paywall", Expected: []string{"skill:pricing"}, Split: SplitHeldIn, Lane: "exact"},
			{ID: "hit-2", Kind: KindCapability, Task: "migration schema rollback", Expected: []string{"procedure:migration"}, Split: SplitHeldOut, Lane: "exact"},
			{ID: "absent-a", Kind: KindCapability, Task: "pricing", Expected: []string{"skill:zeta-ghost"}, Split: SplitHeldIn, Lane: "graph"},
			{ID: "absent-b", Kind: KindCapability, Task: "pricing", Expected: []string{"skill:alpha-ghost"}, Split: SplitHeldOut, Lane: "graph"},
			{ID: "absent-c", Kind: KindCapability, Task: "pricing", Expected: []string{"skill:mid-ghost"}, Split: SplitHeldIn, Lane: "graph"},
		},
	}
	a, err := Run(Options{Home: home, Suite: suite, BuildSHA: "test", Now: fixedNow()})
	if err != nil {
		t.Fatalf("run a: %v", err)
	}
	b, err := Run(Options{Home: home, Suite: suite, BuildSHA: "test", Now: fixedNow()})
	if err != nil {
		t.Fatalf("run b: %v", err)
	}
	if !reflect.DeepEqual(a.Splits, b.Splits) {
		t.Fatalf("splits differ:\n a=%+v\n b=%+v", a.Splits, b.Splits)
	}
	if !reflect.DeepEqual(a.Overall, b.Overall) {
		t.Fatalf("overall differ:\n a=%+v\n b=%+v", a.Overall, b.Overall)
	}
	if !reflect.DeepEqual(a.Cases, b.Cases) {
		t.Fatalf("cases differ")
	}
	if !reflect.DeepEqual(a.AbsentFromGraph, b.AbsentFromGraph) {
		t.Fatalf("absent differ: %v vs %v", a.AbsentFromGraph, b.AbsentFromGraph)
	}
	if a.GraphHash != b.GraphHash {
		t.Fatalf("graph hash differ: %s vs %s", a.GraphHash, b.GraphHash)
	}
}

// TestCommittedFixtureScoresRealHits proves the public fixture is not vacuous: over the graph
// its ids name, every case hits at rank 1 and nothing is absent.
func TestCommittedFixtureScoresRealHits(t *testing.T) {
	home := t.TempDir()
	seedGraph(t, home, fixtureNodes())
	suite, err := LoadSuite(filepath.Join("..", "..", "fixtures", "bench", "capability-suite.json"))
	if err != nil {
		t.Fatalf("load suite: %v", err)
	}
	report, err := Run(Options{Home: home, Suite: suite, BuildSHA: "test", Now: fixedNow()})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.AbsentFromGraph) != 0 {
		t.Fatalf("absent = %v, want none", report.AbsentFromGraph)
	}
	if report.Overall.Hit1 != 1.0 || report.Overall.MRR != 1.0 {
		t.Fatalf("overall = %+v, want hit@1=1.0 mrr=1.0", report.Overall)
	}
	if report.Splits[SplitHeldIn].Cases != 5 || report.Splits[SplitHeldOut].Cases != 5 {
		t.Fatalf("split case counts = in:%d out:%d, want 5/5", report.Splits[SplitHeldIn].Cases, report.Splits[SplitHeldOut].Cases)
	}
}

// TestCompareFlagsHeldOutRegression falsifies the accept rule: a fabricated regression on
// held_out ALONE (held_in unchanged) must be rejected with held_out named.
func TestCompareFlagsHeldOutRegression(t *testing.T) {
	before := Report{
		Suite:   "capability-suite",
		Splits:  map[string]Metrics{SplitHeldIn: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1}, SplitHeldOut: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1}},
		Overall: Metrics{Cases: 10, Hit1: 1, Hit5: 1, MRR: 1},
	}
	after := before
	after.Splits = map[string]Metrics{
		SplitHeldIn:  {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1},        // unchanged
		SplitHeldOut: {Cases: 5, Hit1: 0.8, Hit5: 0.8, MRR: 0.75}, // regressed
	}
	cmp := Compare(before, after)
	if !cmp.Regressed || cmp.Accept {
		t.Fatalf("expected regression rejected, got regressed=%v accept=%v", cmp.Regressed, cmp.Accept)
	}
	if !reflect.DeepEqual(cmp.RegressedSplit, []string{SplitHeldOut}) {
		t.Fatalf("regressed splits = %v, want [held_out]", cmp.RegressedSplit)
	}

	// No-regression control (held_out improves) must accept.
	improve := before
	improve.Splits = map[string]Metrics{
		SplitHeldIn:  {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1},
		SplitHeldOut: {Cases: 5, Hit1: 1, Hit5: 1, MRR: 1},
	}
	if cmp := Compare(before, improve); cmp.Regressed || !cmp.Accept {
		t.Fatalf("no-regression compare rejected: regressed=%v accept=%v", cmp.Regressed, cmp.Accept)
	}
}

// TestMemoryCaseSkippedWhenNoLedger proves graceful degradation: a memory case against a home
// with no memory ledger is marked skipped (with a reason), never a failed run.
func TestMemoryCaseSkippedWhenNoLedger(t *testing.T) {
	home := t.TempDir()
	seedGraph(t, home, fixtureNodes())
	suite := Suite{
		Suite: "mem",
		Cases: []Case{
			{ID: "mem-1", Kind: KindMemory, Task: "some recall task", Expected: []string{"claim-x"}, Split: SplitHeldIn, Lane: "exact"},
		},
	}
	report, err := Run(Options{Home: home, Suite: suite, BuildSHA: "test", Now: fixedNow()})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.SkippedCount != 1 || report.ScoredCount != 0 {
		t.Fatalf("skipped=%d scored=%d, want 1/0", report.SkippedCount, report.ScoredCount)
	}
	if len(report.Cases) != 1 || !report.Cases[0].Skipped || report.Cases[0].SkipReason == "" {
		t.Fatalf("case = %+v, want skipped with reason", report.Cases[0])
	}
	if len(report.AbsentFromGraph) != 0 {
		t.Fatalf("skipped memory case must not contribute absent ids, got %v", report.AbsentFromGraph)
	}
}
