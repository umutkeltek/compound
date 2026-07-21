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
