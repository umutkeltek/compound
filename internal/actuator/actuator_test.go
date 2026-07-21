package actuator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/eventstore"
	"compound/internal/harness"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

// stubClaudeAdapter is a hermetic stand-in for the real subprocess-spawning claude
// adapter, so a Run test can reach the post-dispatch consumption record without
// spawning `claude`. It is injected through the buildAdapterRegistry seam.
type stubClaudeAdapter struct{ ran bool }

func (a *stubClaudeAdapter) Name() string { return "claude" }

func (a *stubClaudeAdapter) Run(_ context.Context, in harness.RunInput) (harness.RunOutput, error) {
	a.ran = true
	return harness.RunOutput{
		RunAttempt: runattempt.RunAttempt{ID: "ratt_stub", WorkID: in.WorkID, Harness: in.Harness, Status: "succeeded"},
		ExitCode:   0,
	}, nil
}

// T5 (F10): the switchback instruction write is GOVERNED, not a raw os.WriteFile.
// Under the default policy a workspace instruction file is local_reversible/easy, so
// it flows AUTO through act.Execute(FileArtifactAdapter) and carries a receipt +
// transition recorded in the canonical event store. (When policy holds it instead,
// the step is honestly Held with the target path in reasons — never a silent raw
// write.)
func TestSwitchbackInstructionStepIsGoverned(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	cfg := policy.DefaultConfig()
	profile := ProjectProfile{
		SchemaVersion: ProfileSchemaVersion,
		ID:            "prof_instr",
		Slug:          "demo",
		Workspace:     workspace,
		Routing:       RoutingSpec{Harness: "claude"},
		Instructions: []InstructionSurface{
			{Name: "AGENTS.md", Path: "AGENTS.md", Body: "# demo agents contract\n"},
		},
		CreatedAt: testNow.Format(time.RFC3339),
	}

	res, err := Switchback(home, profile, "work_switch", cfg, testNow)
	if err != nil {
		t.Fatalf("switchback: %v", err)
	}

	var instr *StepResult
	for i := range res.Steps {
		if res.Steps[i].Kind == StepInstructions {
			instr = &res.Steps[i]
		}
	}
	if instr == nil {
		t.Fatal("no instruction step in switchback result")
	}
	if instr.Held {
		t.Fatalf("instruction step held unexpectedly: %v", instr.Reasons)
	}
	if instr.ReceiptRef == "" {
		t.Fatalf("instruction write must be governed with a receipt, got none (reasons=%v)", instr.Reasons)
	}

	target := filepath.Join(workspace, "AGENTS.md")
	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("instruction file not written: %v", rerr)
	}
	if string(got) != "# demo agents contract\n" {
		t.Fatalf("instruction body wrong: %q", got)
	}

	events, err := eventstore.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.Read(instr.ReceiptRef); err != nil {
		t.Fatalf("instruction receipt must resolve through the event store: %v", err)
	}
	if _, err := events.Read(instr.TransitionRef); err != nil {
		t.Fatalf("instruction transition must resolve through the event store: %v", err)
	}
}

// T1 (F8): an arbitrary harness command (os-harness-entrypoints) must be classified
// so the default policy outcome is HOLD and Route never auto-executes it — derived
// from what is run, not a constant local_reversible label.
func TestRunIntentHoldsArbitraryHarnessCommand(t *testing.T) {
	in := RunInput{WorkID: "work_1", Harness: "os-harness-entrypoints", Command: "true", Args: []string{"--flag"}}
	intent := runIntent(in, testNow)
	if intent.MutationClass != "external_side_effect" {
		t.Fatalf("arbitrary harness run must be classified external_side_effect, got %q", intent.MutationClass)
	}
	pd := policy.Evaluate(intent, policy.DefaultConfig(), testNow)
	if pd.Outcome != "hold" {
		t.Fatalf("default policy for an arbitrary harness run must be hold, got %q", pd.Outcome)
	}
	d := Route(intent, pd.Outcome, openHeadroom())
	if d.Mode != ModeHold {
		t.Fatalf("Route must HOLD an arbitrary harness run, got %s (reasons=%v)", d.Mode, d.Reasons)
	}
}

// T1 (F8, end-to-end): Run of an arbitrary harness command must HOLD without
// dispatching — no subprocess, no RunAttempt minted — and persist a held-move card.
func TestRunHoldsArbitraryCommandWithoutDispatch(t *testing.T) {
	home := t.TempDir()
	res, err := Run(home, RunInput{
		WorkID:  "work_run",
		Harness: "os-harness-entrypoints",
		Command: "true",
	}, policy.DefaultConfig(), testNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Held || res.Executed {
		t.Fatalf("arbitrary harness run must be HELD, not executed: %+v", res)
	}
	if res.RunAttemptID != "" {
		t.Fatalf("held run must not mint a RunAttempt, got %q", res.RunAttemptID)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "run_attempts")); len(entries) != 0 {
		t.Fatalf("held run must not spawn a subprocess / mint a RunAttempt; found %d run_attempt entries", len(entries))
	}
	if res.HeldMoveID == "" {
		t.Fatal("held run must persist a human-gated held-move card")
	}
}

// T2 (F8, regression guard): a claude run in plan mode (and empty→plan default) stays
// local_reversible/easy and routes AUTO — plan mode is read-only by contract.
func TestRunIntentClaudePlanStaysAuto(t *testing.T) {
	for _, mode := range []string{"", "plan"} {
		in := RunInput{WorkID: "work_2", Harness: "claude", PermissionMode: mode, Prompt: "summarize"}
		intent := runIntent(in, testNow)
		if intent.MutationClass != "local_reversible" || intent.Reversibility != "easy" {
			t.Fatalf("claude+%q must stay local_reversible/easy, got %q/%q", mode, intent.MutationClass, intent.Reversibility)
		}
		pd := policy.Evaluate(intent, policy.DefaultConfig(), testNow)
		d := Route(intent, pd.Outcome, openHeadroom())
		if d.Mode != ModeAuto {
			t.Fatalf("claude+%q (plan) must route AUTO, got %s (reasons=%v)", mode, d.Mode, d.Reasons)
		}
	}
}

// F8: a claude run in a WRITE posture (acceptEdits/bypass/default) is not read-only,
// so it must be held like any other arbitrary run — plan is the ONLY auto-safe mode.
func TestRunIntentClaudeNonPlanHolds(t *testing.T) {
	for _, mode := range []string{"acceptEdits", "bypass", "default"} {
		in := RunInput{WorkID: "work_3", Harness: "claude", PermissionMode: mode, Prompt: "edit files"}
		intent := runIntent(in, testNow)
		if intent.MutationClass != "external_side_effect" {
			t.Fatalf("claude+%q must be held (external_side_effect), got %q", mode, intent.MutationClass)
		}
		pd := policy.Evaluate(intent, policy.DefaultConfig(), testNow)
		d := Route(intent, pd.Outcome, openHeadroom())
		if d.Mode != ModeHold {
			t.Fatalf("claude+%q must HOLD, got %s", mode, d.Mode)
		}
	}
}

// T4 (F9.2): a corrupt row in the consumption ledger must make Used ERROR naming the
// line, and the budget gate must treat that as BLOCKING (hold) — never silently skip
// the row and under-count, and never hard-error out of the plan.
func TestCorruptLedgerErrorsAndHoldsBudget(t *testing.T) {
	home := t.TempDir()
	l := OpenLedger(home)
	// Row 1: a valid consumption row (so the file + dir exist).
	if err := l.Record(Consumption{Channel: "*", At: testNow.Format(time.RFC3339), Actions: 1}); err != nil {
		t.Fatalf("record valid row: %v", err)
	}
	// Row 2: a corrupt, non-JSON row appended directly.
	f, err := os.OpenFile(filepath.Join(home, "actuator", "budget", "consumption.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{ this is not valid json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Used errors, naming the corrupt line.
	if _, uerr := l.Used("", "*", testNow.Add(-time.Hour)); uerr == nil {
		t.Fatal("Used must error on a corrupt ledger row, not silently skip it")
	} else if !strings.Contains(uerr.Error(), "line 2") {
		t.Fatalf("Used error must name the corrupt line number, got: %v", uerr)
	}

	// The budget gate converts the unreadable ledger into a HOLD, not zero usage,
	// and not a hard error that aborts the plan.
	h, cerr := l.Check(DefaultBudget(testNow), "", "*", testNow)
	if cerr != nil {
		t.Fatalf("Check must convert the ledger error into a hold, not propagate a hard error: %v", cerr)
	}
	if h.Allowed {
		t.Fatal("a corrupt budget ledger must HOLD (not allowed), never be treated as zero usage")
	}

	// End-to-end: an otherwise-auto move is HELD on the corrupt ledger.
	intent := runIntent(RunInput{WorkID: "w", Harness: "claude", PermissionMode: "plan"}, testNow)
	plan, perr := EvaluatePlan(home, intent, "", "", policy.DefaultConfig(), testNow)
	if perr != nil {
		t.Fatalf("EvaluatePlan must not hard-error on a corrupt ledger: %v", perr)
	}
	if plan.Decision.Mode != ModeHold {
		t.Fatalf("an otherwise-auto move on a corrupt ledger must be HELD, got %s (reasons=%v)", plan.Decision.Mode, plan.Decision.Reasons)
	}
}

// T3 (F9.1): when a run executed but recording its budget consumption fails, Run must
// return an error naming the consumption failure — and still report the run happened
// (Executed=true) so the operator knows it did NOT get rolled back. The budget dir is
// made read-only so LoadBudget/Used (reads) succeed but Record (a write) fails,
// isolating the record failure.
func TestRunSurfacesConsumptionRecordFailure(t *testing.T) {
	home := t.TempDir()
	budgetDir := filepath.Join(home, "actuator", "budget")
	if err := os.MkdirAll(budgetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(budgetDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(budgetDir, 0o755) })

	stub := &stubClaudeAdapter{}
	orig := buildAdapterRegistry
	buildAdapterRegistry = func(h string) *harness.AdapterRegistry {
		reg := harness.NewAdapterRegistry(runattempt.Open(h))
		if err := reg.RegisterAdapter(stub); err != nil {
			t.Fatalf("register stub adapter: %v", err)
		}
		return reg
	}
	defer func() { buildAdapterRegistry = orig }()

	res, err := Run(home, RunInput{
		WorkID:         "work_rec",
		Harness:        "claude",
		PermissionMode: "plan",
		Prompt:         "hello",
	}, policy.DefaultConfig(), testNow)

	if !stub.ran {
		t.Fatal("precondition: the auto run must have dispatched the stub adapter")
	}
	if !res.Executed {
		t.Fatalf("the run DID execute; result must report Executed=true: %+v", res)
	}
	if err == nil {
		t.Fatal("a failed consumption record must surface as an error, not be silently discarded")
	}
	if !strings.Contains(err.Error(), "consumption") {
		t.Fatalf("error must name the consumption-record failure, got: %v", err)
	}
	if res.RunAttemptID == "" || !strings.Contains(err.Error(), res.RunAttemptID) {
		t.Fatalf("error must report the run happened (run_attempt id %q), got: %v", res.RunAttemptID, err)
	}
}
