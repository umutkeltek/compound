package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/readiness"
	"compound/internal/work"
)

// TestRunHarnessReadinessUnreadyPrintsRepair covers the read-only readiness
// face R3B 2026-09-04 introduced: --harness without a work id must
// surface Ready=false, the readiness details, and per-category repair
// commands — same vocabulary the execute-time refusal uses, so an
// operator can ask "can this lane run anything right now?" without
// claiming work first.
func TestRunHarnessReadinessUnreadyPrintsRepair(t *testing.T) {
	homePath := initializedExecutionHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{
			Ready: false,
			Details: []string{
				"permission_posture: managed settings differ from authority",
				"blocking_hook_live: mutation-covering binding status=drifted",
				"instruction_pack: work has no context_pack_id",
			},
		}, nil
	})
	var out, errOut bytes.Buffer
	code := runHarnessReadiness([]string{"--harness", "claude", "--workspace", t.TempDir()}, &out, &errOut)
	if code == 0 {
		t.Fatalf("unready readiness face returned 0; expected non-zero:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, "status: unready") {
		t.Fatalf("readiness face did not print status: unready:\n%s", body)
	}
	for _, want := range []string{
		"permission_posture: managed settings differ from authority",
		"blocking_hook_live: mutation-covering binding status=drifted",
		"instruction_pack: work has no context_pack_id",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("readiness face omitted detail %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "compound harness readiness --harness claude") {
		t.Fatalf("readiness face did not include the self-referential repair line:\n%s", body)
	}
	for _, want := range []string{
		"compound harness permission-mode --harness claude",
		"compound hook install --harness claude",
		"compound harness instruction-pack --harness claude",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("readiness face did not include repair line %q:\n%s", want, body)
		}
	}
}

// TestRunHarnessReadinessJSONShape pins the JSON output the doctor
// harness section and dashboards consume. Every repair command line is
// a copy of the cmd-line, not a description.
func TestRunHarnessReadinessJSONShape(t *testing.T) {
	homePath := initializedExecutionHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{
			Ready:   false,
			Details: []string{"permission_posture: managed settings differ from authority"},
		}, nil
	})
	var out, errOut bytes.Buffer
	code := runHarnessReadiness([]string{"--harness", "claude", "--json"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("unready readiness JSON returned 0:\n%s", out.String())
	}
	var rep struct {
		Harness        string   `json:"harness"`
		Workspace      string   `json:"workspace,omitempty"`
		Ready          bool     `json:"ready"`
		Details        []string `json:"details"`
		RepairCommands []string `json:"repair_commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("decode readiness JSON: %v\n%s", err, out.String())
	}
	if rep.Harness != "claude" || rep.Ready {
		t.Fatalf("readiness JSON shape = %+v", rep)
	}
	if len(rep.Details) != 1 || rep.Details[0] != "permission_posture: managed settings differ from authority" {
		t.Fatalf("readiness JSON details = %v", rep.Details)
	}
	if !readinessRepairListHasPrefix(rep.RepairCommands, "compound harness permission-mode --harness claude") {
		t.Fatalf("readiness JSON repair_commands = %v", rep.RepairCommands)
	}
}

// TestRunHarnessReadinessReadyExitsZero pins that a Ready=true evaluation
// exits 0; the unready cases above exit 1 (with details + repair).
func TestRunHarnessReadinessReadyExitsZero(t *testing.T) {
	homePath := initializedExecutionHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{Ready: true}, nil
	})
	var out, errOut bytes.Buffer
	code := runHarnessReadiness([]string{"--harness", "claude"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("ready readiness face returned %d; want 0:\nstdout=%s\nstderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "status: ready") {
		t.Fatalf("ready readiness face did not print status: ready:\n%s", out.String())
	}
}

// TestRunHarnessReadinessUsageMissingHarness pins the usage guard; it
// keeps the F3 falsifier contract for the new verb.
func TestRunHarnessReadinessUsageMissingHarness(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runHarnessReadiness(nil, &out, &errOut)
	if code != 2 {
		t.Fatalf("usage guard returned %d; want 2:\nstderr=%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "usage: compound harness readiness") {
		t.Fatalf("usage guard stderr = %q; want usage line", errOut.String())
	}
}

// TestEvaluateForHarnessCallsRegisteredGuard makes the thin wrapper's
// contract explicit: it builds the request through the registered seam,
// so the cmd/compound face and the harness readiness face evaluate the
// SAME logic (and a missing guard fails closed).
func TestEvaluateForHarnessCallsRegisteredGuard(t *testing.T) {
	homePath := initializedExecutionHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	stubDelegateHarnessReadiness(t, func(req work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		if req.Harness != "claude" {
			t.Fatalf("EvaluateForHarness harness = %q; want claude", req.Harness)
		}
		return work.DelegateHarnessReadinessResult{Ready: true}, nil
	})
	result, err := readiness.EvaluateForHarness(homePath, "claude", t.TempDir(), "", cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready {
		t.Fatalf("EvaluateForHarness Ready=false; want true")
	}
}

// TestEvaluateForHarnessMissingGuard pins the fail-closed path so a
// forgotten init() in a test harness (or a future binary that forgets
// to install the seam) cannot silently report Ready=true.
func TestEvaluateForHarnessMissingGuard(t *testing.T) {
	original := work.DelegateHarnessReadiness
	work.DelegateHarnessReadiness = nil
	t.Cleanup(func() { work.DelegateHarnessReadiness = original })
	if _, err := readiness.EvaluateForHarness(t.TempDir(), "claude", "", "", cliTestNow); err == nil {
		t.Fatalf("missing readiness guard returned no error; want fail-closed")
	}
}

func readinessRepairListContains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func readinessRepairListHasPrefix(values []string, prefix string) bool {
	for _, v := range values {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// Suppress unused import warnings if all subcommand tests get trimmed.
var _ = exec.Command
var _ = filepath.Join
