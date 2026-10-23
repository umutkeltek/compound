package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/lanebeliefs"
	"compound/internal/routeevidence"
	"compound/internal/work"
)

const capabilityClassCLISubEnv = "COMPOUND_CAPABILITY_CLASS_CLI_SUB_ARGS"

// TestCapabilityClassCLISubprocess is the re-exec target for the tests below: it runs ONE CLI
// invocation (args joined by \x1f) so a path that os.Exits — a usage refusal — is observed by its
// exit code instead of killing the parent test binary. It is a no-op in a normal run.
func TestCapabilityClassCLISubprocess(t *testing.T) {
	raw := os.Getenv(capabilityClassCLISubEnv)
	if raw == "" {
		t.Skip("subprocess target only")
	}
	args := strings.Split(raw, "\x1f")
	switch args[0] {
	case "work":
		cmdWork(args[1:])
	case "lanebeliefs":
		cmdLaneBeliefs(args[1:])
	default:
		t.Fatalf("unknown subprocess family %q", args[0])
	}
}

func runCapabilityClassCLI(t *testing.T, homePath string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCapabilityClassCLISubprocess$")
	cmd.Env = append(os.Environ(), capabilityClassCLISubEnv+"="+strings.Join(args, "\x1f"), "COMPOUND_HOME="+homePath)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(output), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run subprocess %v: %v", args, err)
	}
	return string(output), 0
}

func runningWorkForCapabilityClass(t *testing.T, store *work.Store, predicate string) string {
	t.Helper()
	w, err := store.Create(work.CreateInput{Owner: "lane-beliefs", SuccessPredicate: predicate}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func closeReceiptJSON(t *testing.T, out string) map[string]json.RawMessage {
	t.Helper()
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("close output missing JSON:\n%s", out)
	}
	var result struct {
		Receipt map[string]json.RawMessage `json:"receipt"`
	}
	dec := json.NewDecoder(strings.NewReader(out[start:]))
	if err := dec.Decode(&result); err != nil {
		t.Fatalf("parse close output: %v\n%s", err, out)
	}
	if result.Receipt == nil {
		t.Fatalf("close output has no receipt:\n%s", out)
	}
	return result.Receipt
}

// `work close --capability-class build-implement` seals the class on the receipt; a close without
// the flag writes no capability_class key (no guessing from the predicate's "build and implement").
// Reds against a close door that does not accept or does not forward the flag.
func TestWorkCloseCapabilityClassFlagWritesReceiptField(t *testing.T) {
	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}

	declared := runningWorkForCapabilityClass(t, store, "close declares build-implement")
	out, code := runCapabilityClassCLI(t, homePath, "work", "close", declared, "--outcome", "completed", "--move-type", "audit_only", "--by", "codex", "--capability-class", "build-implement")
	if code != 0 {
		t.Fatalf("close --capability-class exit=%d, want 0:\n%s", code, out)
	}
	if got := string(closeReceiptJSON(t, out)["capability_class"]); got != `"build-implement"` {
		t.Fatalf("receipt capability_class = %s, want \"build-implement\":\n%s", got, out)
	}

	undeclared := runningWorkForCapabilityClass(t, store, "build and implement without a declared class")
	out, code = runCapabilityClassCLI(t, homePath, "work", "close", undeclared, "--outcome", "completed", "--move-type", "audit_only", "--by", "codex")
	if code != 0 {
		t.Fatalf("close without --capability-class exit=%d, want 0:\n%s", code, out)
	}
	if got, ok := closeReceiptJSON(t, out)["capability_class"]; ok {
		t.Fatalf("close without the flag wrote capability_class=%s; absence must stay absent", got)
	}
}

// A class outside the known vocabulary is refused with exit 2 before any close: the message names
// the bad value and lists the valid classes, and the work stays running. Reds against a close door
// that accepts any slug (a typo would open a silent new posterior cell).
func TestWorkCloseRefusesUnknownCapabilityClass(t *testing.T) {
	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	id := runningWorkForCapabilityClass(t, store, "unknown class is refused")
	out, code := runCapabilityClassCLI(t, homePath, "work", "close", id, "--outcome", "completed", "--move-type", "audit_only", "--by", "codex", "--capability-class", "banana-split")
	if code != 2 {
		t.Fatalf("unknown class exit=%d, want 2:\n%s", code, out)
	}
	for _, want := range []string{"--capability-class", "banana-split", "build-implement", "judge-critique", "classify-extract"} {
		if !strings.Contains(out, want) {
			t.Fatalf("refusal must name %q (bad value + valid classes):\n%s", want, out)
		}
	}
	got, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "running" {
		t.Fatalf("refused close moved work to %q, want running", got.State)
	}
}

// writeCLIClassReceiptEvent writes one terminal receipt event declaring capability_class on the
// wire, the persisted shape the fold reads.
func writeCLIClassReceiptEvent(t *testing.T, homePath, receiptID, workID, laneID, class, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(homePath, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	created := cliTestNow.Format("2006-01-02T15:04:05Z07:00")
	receipt := envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            receiptID,
			CreatedAt:     created,
			Producer:      envelope.Producer{Kind: "test", Name: "fixture", LaneID: laneID},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:    to,
		MoveType:   "audit_only",
		StateDelta: envelope.StateDelta{FilesChanged: []string{}},
		Cost:       envelope.ReceiptCost{ModelCalls: []string{}},
		Skills:     envelope.ReceiptSkills{Candidate: []string{}},
		Memory:     envelope.ReceiptMemory{Candidates: []string{}},
		Next:       envelope.ReceiptNext{SuggestedWork: []string{}},
	}
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	obj["capability_class"] = class
	writeCLIJSONFile(t, filepath.Join(homePath, "events", receiptID+".json"), obj)
	line, err := json.Marshal(map[string]any{"ts": created, "work_id": workID, "from": "running", "to": to, "attached": []string{receiptID}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(homePath, "events", "work.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

// `lanebeliefs posterior --lane L --class C --json` resolves the short lane id through the capgraph
// door and prints the existing CompletionPosteriorReport for that one (lane, class) cell. Reds
// against a missing read face (a scheduler would have to reimplement the posterior).
func TestLaneBeliefsPosteriorJSONShape(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	seedCLILaneNode(t, homePath, "fixture/judge")
	nodeID, _ := capgraph.AgentLaneNodeIDFromShortLaneID("fixture/judge")
	for i := 0; i < lanebeliefs.MIN_LANE_RECEIPTS; i++ {
		to := "completed"
		if i == 0 {
			to = "failed"
		}
		writeCLIClassReceiptEvent(t, homePath, fmt.Sprintf("rec_post_%02d", i), fmt.Sprintf("work_post_%02d", i), "fixture/judge", "build-implement", to)
	}
	if _, err := lanebeliefs.Fold(homePath, lanebeliefs.FoldOptions{Now: cliTestNow}); err != nil {
		t.Fatal(err)
	}

	out, code := runCapabilityClassCLI(t, homePath, "lanebeliefs", "posterior", "--lane", "fixture/judge", "--class", "build-implement", "--json", "--home", homePath)
	if code != 0 {
		t.Fatalf("posterior exit=%d, want 0:\n%s", code, out)
	}
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("posterior output missing JSON:\n%s", out)
	}
	var report lanebeliefs.CompletionPosteriorReport
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&report); err != nil {
		t.Fatalf("parse posterior: %v\n%s", err, out)
	}
	if report.SchemaVersion != "compound.lanebeliefs.completion_posterior.v0.1" || report.LaneID != nodeID || report.CapabilityClass != "build-implement" {
		t.Fatalf("posterior identity = %s/%s/%s, want completion_posterior.v0.1/%s/build-implement", report.SchemaVersion, report.LaneID, report.CapabilityClass, nodeID)
	}
	n := lanebeliefs.MIN_LANE_RECEIPTS
	if report.Attempts != n || report.Completions != n-1 || report.Failures != 1 || !report.MeetsFloor || report.PriorOnly {
		t.Fatalf("posterior counts = %+v, want %d attempts / %d completions / 1 failure, floor met", report, n, n-1)
	}
	want := float64(lanebeliefs.BETA_PRIOR_ALPHA+n-1) / float64(lanebeliefs.BETA_PRIOR_ALPHA+lanebeliefs.BETA_PRIOR_BETA+n)
	if diff := report.PosteriorMean - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("posterior mean = %v, want %v", report.PosteriorMean, want)
	}
}

// An unresolvable lane is exit 2 with a clear message (zero evidence is never priced as prior);
// so are a missing --lane and a class outside the known vocabulary. Reds against a read face that
// silently answers prior-only for a typo.
func TestLaneBeliefsPosteriorRefusesUnresolvableLaneAndUnknownClass(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	seedCLILaneNode(t, homePath, "fixture/judge")

	out, code := runCapabilityClassCLI(t, homePath, "lanebeliefs", "posterior", "--lane", "nobody/unseeded", "--class", "build-implement", "--json", "--home", homePath)
	if code != 2 || !strings.Contains(out, "nobody/unseeded") || !strings.Contains(out, "does not resolve") {
		t.Fatalf("unresolvable lane: exit=%d, want 2 naming the lane and 'does not resolve':\n%s", code, out)
	}
	out, code = runCapabilityClassCLI(t, homePath, "lanebeliefs", "posterior", "--lane", "fixture/judge", "--class", "banana-split", "--home", homePath)
	if code != 2 || !strings.Contains(out, "banana-split") || !strings.Contains(out, "build-implement") {
		t.Fatalf("unknown class: exit=%d, want 2 naming the class and the valid set:\n%s", code, out)
	}
	out, code = runCapabilityClassCLI(t, homePath, "lanebeliefs", "posterior", "--class", "build-implement", "--home", homePath)
	if code != 2 || !strings.Contains(out, "--lane") {
		t.Fatalf("missing --lane: exit=%d, want 2 naming --lane:\n%s", code, out)
	}
}

// routeevidence.KnownCapabilityClasses is the ONE vocabulary both doors check against. It covers
// every class the task classifier emits (a hand-copied list that drifted would refuse a real
// class at close), is sorted, deduplicated and a fresh slice, and adds a home's slug-shaped
// routing_defaults classes while leaving out a dotted policy capability the Receipt cannot carry.
func TestKnownCapabilityClassesCoversClassifierAndRoutingDefaults(t *testing.T) {
	base := routeevidence.KnownCapabilityClasses("")
	for _, task := range []string{
		"bulk rename the files", "add a regression test", "translate the strings", "repair the oauth refresh token",
		"tune the compound context pack", "polish the frontend", "extract the metrics", "draft the release email",
		"review the risk", "plan the roadmap", "ship the release", "implement the feature", "zzz no rule matches",
	} {
		class := routeevidence.ClassifyCapabilityTask(task).CapabilityClass
		if !slices.Contains(base, class) {
			t.Fatalf("classifier emits %q for %q but the known vocabulary %v lacks it", class, task, base)
		}
	}
	for i := 1; i < len(base); i++ {
		if base[i-1] >= base[i] {
			t.Fatalf("known classes not sorted+deduplicated: %v", base)
		}
	}
	base[0] = "mutated"
	if routeevidence.KnownCapabilityClasses("")[0] == "mutated" {
		t.Fatal("KnownCapabilityClasses must return a fresh slice")
	}

	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	src, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.TrimRight(string(src), "\n") + "\n  smoke-only-class:\n    - auto/extract\n  work.continue.unattended:\n    - auto/extract\n"
	manifestPath := filepath.Join(t.TempDir(), "model-capabilities.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() { cmdRoute([]string{"import-model-capabilities", manifestPath}) })
	withHome := routeevidence.KnownCapabilityClasses(homePath)
	if !slices.Contains(withHome, "smoke-only-class") {
		t.Fatalf("home routing_defaults class missing from vocabulary: %v", withHome)
	}
	if slices.Contains(withHome, "work.continue.unattended") {
		t.Fatalf("dotted policy capability leaked into the receipt class vocabulary: %v", withHome)
	}
	if len(withHome) != len(routeevidence.KnownCapabilityClasses(""))+1 {
		t.Fatalf("home vocabulary = %v, want the classifier set plus exactly smoke-only-class", withHome)
	}
}

// The whole slice, alive: a real `work close --lane L --capability-class C --move-type audit_only`
// (the product loop's close shape) → `lanebeliefs fold` → `lanebeliefs posterior` reads one attempt
// in that (lane, class) cell. Reds if any link drops the lane, the class, or the audit_only trial.
func TestWorkCloseLaneAndClassFeedThePosteriorEndToEnd(t *testing.T) {
	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	writeWorkLaneFixture(t, "h1/fast")
	nodeID, _ := capgraph.AgentLaneNodeIDFromShortLaneID("h1/fast")
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	id := runningWorkForCapabilityClass(t, store, "lane and class feed the posterior")
	out, code := runCapabilityClassCLI(t, homePath, "work", "close", id, "--outcome", "completed", "--move-type", "audit_only", "--by", "codex", "--lane", "h1/fast", "--capability-class", "classify-extract")
	if code != 0 {
		t.Fatalf("close --lane --capability-class exit=%d:\n%s", code, out)
	}
	var producer envelope.Producer
	if err := json.Unmarshal(closeReceiptJSON(t, out)["producer"], &producer); err != nil {
		t.Fatal(err)
	}
	if producer.LaneID != nodeID {
		t.Fatalf("receipt producer.lane_id = %q, want %q", producer.LaneID, nodeID)
	}

	if _, err := lanebeliefs.Fold(homePath, lanebeliefs.FoldOptions{Now: cliTestNow}); err != nil {
		t.Fatal(err)
	}
	out, code = runCapabilityClassCLI(t, homePath, "lanebeliefs", "posterior", "--lane", "h1/fast", "--class", "classify-extract", "--json")
	if code != 0 {
		t.Fatalf("posterior exit=%d:\n%s", code, out)
	}
	var report lanebeliefs.CompletionPosteriorReport
	if err := json.NewDecoder(strings.NewReader(out[strings.Index(out, "{"):])).Decode(&report); err != nil {
		t.Fatalf("parse posterior: %v\n%s", err, out)
	}
	if report.LaneID != nodeID || report.Attempts != 1 || report.Completions != 1 || report.PriorOnly {
		t.Fatalf("posterior after one declared audit_only close = %+v, want 1/1 in %s", report, nodeID)
	}
	// The "unknown" bucket stays readable and empty: the declared class never leaks into it.
	out, code = runCapabilityClassCLI(t, homePath, "lanebeliefs", "posterior", "--lane", "h1/fast", "--class", "unknown", "--json")
	if code != 0 || !strings.Contains(out, `"attempts": 0`) {
		t.Fatalf("unknown bucket read: exit=%d, want 0 with zero attempts:\n%s", code, out)
	}
}
