package actuator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/eventstore"
	"compound/internal/policy"
)

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
