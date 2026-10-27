package authority

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"compound/internal/work"
)

func completedLaunchdHandoffPredecessor(t *testing.T) *adversarialLiveFixture {
	t.Helper()
	f := completedLaunchdPredecessor(t)
	p, err := launchdSuccessorPlan(t, f, f.plan, true)
	if err != nil {
		t.Fatal(err)
	}
	recordLaunchdSuccessor(t, f, p)
	result, err := f.apply()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Phases) != 4 {
		t.Fatalf("predecessor did not complete its native handoff: %+v", result)
	}
	f.now = f.now.Add(time.Second)
	if _, err := f.store.Advance(p.WorkID, work.AdvanceInput{
		To: "verifying", Actor: p.Actor,
	}, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Close(p.WorkID, work.CloseInput{
		Outcome: "completed", MoveType: "control_plane_repair", Actor: p.Actor,
		Changed: true, Summary: "synthetic handoff repair verified",
		ProofRefs: []string{result.Phases[len(result.Phases)-1].ReceiptID},
	}, f.now); err != nil {
		t.Fatal(err)
	}
	// Model the separately authorized quiescence required for another successor.
	f.disabled, f.loaded = true, false
	return f
}

func useLaunchdForeignBaseline(t *testing.T, f *adversarialLiveFixture, foreign map[string]bool) {
	t.Helper()
	base := f.f.env.Run
	f.f.env.Run = func(ctx context.Context, exe string, args ...string) (LaunchdCommandResult, error) {
		result, err := base(ctx, exe, args...)
		if err == nil && result.ExitCode == 0 && exe == "/bin/launchctl" && len(args) > 0 && args[0] == "print-disabled" {
			var output strings.Builder
			fmt.Fprintln(&output, "disabled services = {")
			fmt.Fprintf(&output, "%q => %t\n", f.plan.Label, f.disabled)
			keys := make([]string, 0, len(foreign))
			for label := range foreign {
				keys = append(keys, label)
			}
			sort.Strings(keys)
			for _, label := range keys {
				fmt.Fprintf(&output, "%q => %t\n", label, foreign[label])
			}
			fmt.Fprintln(&output, "}")
			result.Stdout = output.String()
		}
		return result, err
	}
}

func TestLaunchdTerminalHandoffAcceptsIndependentForeignBaseline(t *testing.T) {
	f := completedLaunchdHandoffPredecessor(t)
	old := f.plan
	history := snapshotLaunchdHistory(t, f, old)
	current := map[string]bool{
		"org.example.unrelated": false,
		"org.example.added":     false,
	}
	useLaunchdForeignBaseline(t, f, current)

	p, err := launchdSuccessorPlan(t, f, old, true)
	if err != nil {
		t.Fatalf("authentic predecessor refused after unrelated between-transaction changes: %v", err)
	}
	if !reflect.DeepEqual(p.Runtime.ForeignOverrides, current) {
		t.Fatalf("successor did not seal its fresh foreign baseline: %+v", p.Runtime.ForeignOverrides)
	}
	recordLaunchdSuccessor(t, f, p)
	if _, err := f.apply(); err != nil {
		t.Fatalf("successor could not apply against its independently sealed baseline: %v", err)
	}
	for name, want := range history {
		path, err := launchdArtifactPath(f.home, name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("historical evidence changed: %s: %v", name, err)
		}
	}
}

func TestLaunchdTerminalHandoffRejectsForgedHistoricalForeignBaseline(t *testing.T) {
	f := completedLaunchdHandoffPredecessor(t)
	old := f.plan
	current := map[string]bool{
		"org.example.unrelated": false,
		"org.example.added":     false,
	}
	useLaunchdForeignBaseline(t, f, current)

	var terminal LaunchdRepairResult
	if err := readLaunchdArtifact(f.home, old.ID+".apply.terminal.json", &terminal); err != nil {
		t.Fatal(err)
	}
	// Forge only the terminal observation to agree with today's state. Its
	// historical plan and authentic phase receipts still bind the old baseline.
	terminal.Runtime.ForeignOverrides = map[string]bool{
		"org.example.unrelated": false,
		"org.example.added":     false,
	}
	path, err := launchdArtifactPath(f.home, old.ID+".apply.terminal.json")
	if err != nil {
		t.Fatal(err)
	}
	adversarialWriteJSON(t, path, terminal)
	beforeEffects := len(f.effects)
	if _, err := launchdSuccessorPlan(t, f, old, true); err == nil {
		t.Fatal("terminal rewritten to today's foreign baseline was accepted as historical evidence")
	}
	if len(f.effects) != beforeEffects {
		t.Fatal("rejected historical terminal caused an OS effect")
	}
}

func TestLaunchdTerminalHandoffRejectsCurrentForeignDrift(t *testing.T) {
	for _, direction := range []string{"apply", "rollback"} {
		t.Run(direction, func(t *testing.T) {
			f := completedLaunchdHandoffPredecessor(t)
			old := f.plan
			current := map[string]bool{
				"org.example.unrelated": false,
				"org.example.added":     false,
			}
			useLaunchdForeignBaseline(t, f, current)
			p, err := launchdSuccessorPlan(t, f, old, true)
			if err != nil {
				t.Fatal(err)
			}
			recordLaunchdSuccessor(t, f, p)
			review := f.review
			wantOwner := old.ID
			if direction == "rollback" {
				if _, err := f.apply(); err != nil {
					t.Fatal(err)
				}
				review = f.sealReview(t, true)
				wantOwner = p.ID
			}

			beforeBytes, err := os.ReadFile(p.PlistPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeEffects, beforeLoaded, beforeDisabled := len(f.effects), f.loaded, f.disabled
			current["org.example.added"] = true
			if direction == "apply" {
				_, err = f.apply()
			} else {
				_, err = RollbackLaunchdRepair(context.Background(), f.home, p.ID, review.ID, p.Actor, f.f.env)
			}
			if err == nil {
				t.Fatalf("%s accepted drift after the successor sealed its own foreign baseline", direction)
			}
			afterBytes, readErr := os.ReadFile(p.PlistPath)
			if readErr != nil || !bytesEqual(beforeBytes, afterBytes) ||
				len(f.effects) != beforeEffects || f.loaded != beforeLoaded || f.disabled != beforeDisabled {
				t.Fatalf("refused %s changed OS state: %v", direction, readErr)
			}
			owner, readErr := readLaunchdOwner(f.home, p)
			if readErr != nil || !bytesEqual(owner, launchdOwnerBytes(wantOwner)) {
				t.Fatalf("refused %s changed ownership: %s %v", direction, owner, readErr)
			}
		})
	}
}
