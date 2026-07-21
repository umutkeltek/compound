package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"compound/internal/work"
)

var delegateCLITestNow = time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)

type delegateCLIFakeRunner struct {
	live    map[string]bool
	creates []delegateCLICreate
	keys    []delegateCLIKeys
}

type delegateCLICreate struct {
	name, dir, command string
	args               []string
}

type delegateCLIKeys struct {
	name, text string
	enter      bool
}

func newDelegateCLIFakeRunner() *delegateCLIFakeRunner {
	return &delegateCLIFakeRunner{live: map[string]bool{}}
}

func (f *delegateCLIFakeRunner) Available() error { return nil }
func (f *delegateCLIFakeRunner) Has(name string) (bool, error) {
	return f.live[name], nil
}
func (f *delegateCLIFakeRunner) Create(name, dir, command string, args []string) error {
	f.creates = append(f.creates, delegateCLICreate{name: name, dir: dir, command: command, args: append([]string(nil), args...)})
	f.live[name] = true
	return nil
}
func (f *delegateCLIFakeRunner) SendKeys(name, text string, enter bool) error {
	if !f.live[name] {
		return fmt.Errorf("session %s is dead", name)
	}
	f.keys = append(f.keys, delegateCLIKeys{name: name, text: text, enter: enter})
	return nil
}
func (f *delegateCLIFakeRunner) Kill(name string) error {
	delete(f.live, name)
	return nil
}
func (f *delegateCLIFakeRunner) AttachArgv(name string) []string {
	return []string{"tmux", "attach-session", "-t", name}
}

func claimedDelegateCLIWork(t *testing.T, holder string) (*work.Store, string) {
	t.Helper()
	store, err := work.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{
		Owner: holder, SuccessPredicate: "governed session remains observable and steerable", State: "ready",
	}, delegateCLITestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, holder, delegateCLITestNow); err != nil {
		t.Fatal(err)
	}
	return store, w.ID
}

func allowDelegateCLIGuard(t *testing.T, confirmationRequired bool) {
	t.Helper()
	orig := work.LaneCapacityGuard
	work.LaneCapacityGuard = func(req work.LaneCapacityGuardRequest) (work.LaneCapacityGuardResult, error) {
		return work.LaneCapacityGuardResult{
			Capacity:             work.DispatchLaneCapacity{State: "ok", Remaining: 0.8},
			ConfirmationRequired: confirmationRequired,
			Message:              "lane has headroom",
		}, nil
	}
	t.Cleanup(func() { work.LaneCapacityGuard = orig })
}

func runDelegateCLIForTest(t *testing.T, sub string, store *work.Store, runner work.SessionRunner, attach func([]string) error, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWorkDelegateCLI(sub, args, store, runner, attach, delegateCLITestNow, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestWorkDelegateCLIPromptUsesNamedWrapperWithoutPersistingSpeech(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID := claimedDelegateCLIWork(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	workspace := t.TempDir()
	const speech = "let it move like tidewater around the hard edges"
	out, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude_zai", "--prompt", speech, "--workspace", workspace, "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, errOut, out)
	}
	if len(runner.creates) != 1 {
		t.Fatalf("creates = %+v", runner.creates)
	}
	created := runner.creates[0]
	if created.command != "claude-zai" || created.dir != workspace || len(created.args) != 1 || created.args[0] != speech {
		t.Fatalf("create = %+v", created)
	}
	if strings.Contains(out, speech) || strings.Contains(errOut, speech) {
		t.Fatalf("CLI output persisted expressive prompt: stdout=%q stderr=%q", out, errOut)
	}
	var result work.DelegateResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if result.Status != "spawned" || result.Binding == nil || result.Binding.Harness != "claude-zai" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWorkDelegateCLIRawCommandAfterDashDash(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID := claimedDelegateCLIWork(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	_, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-qwen", "--", "worker-tool", "--mode", "gentle")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if len(runner.creates) != 1 || runner.creates[0].command != "worker-tool" || strings.Join(runner.creates[0].args, " ") != "--mode gentle" {
		t.Fatalf("raw create = %+v", runner.creates)
	}
}

func TestWorkDelegateCLIDryRunPrintsGuardDecisionAndDoesNotSpawn(t *testing.T) {
	allowDelegateCLIGuard(t, true)
	store, workID := claimedDelegateCLIWork(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	out, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-minimax", "--dry-run")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "status=preview") || !strings.Contains(out, "capacity=ok") || !strings.Contains(out, "confirmation_required=true") {
		t.Fatalf("dry-run hid guard decision: %q", out)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("dry-run spawned: %+v", runner.creates)
	}
}

func TestWorkDelegateCLISteerAndAttachUseRecoveredSession(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID := claimedDelegateCLIWork(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	if _, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-zai"); code != 0 {
		t.Fatalf("delegate code=%d stderr=%q", code, errOut)
	}

	const direction = "follow the surprising thread"
	if _, errOut, code := runDelegateCLIForTest(t, "steer", store, runner, nil,
		workID, "--text", direction, "--enter"); code != 0 {
		t.Fatalf("steer code=%d stderr=%q", code, errOut)
	}
	if len(runner.keys) != 1 || runner.keys[0].text != direction || !runner.keys[0].enter {
		t.Fatalf("steer calls = %+v", runner.keys)
	}

	var attached []string
	attach := func(argv []string) error {
		attached = append([]string(nil), argv...)
		return nil
	}
	if _, errOut, code := runDelegateCLIForTest(t, "attach", store, runner, attach, workID); code != 0 {
		t.Fatalf("attach code=%d stderr=%q", code, errOut)
	}
	wantSession := "compound-" + workID
	if strings.Join(attached, " ") != "tmux attach-session -t "+wantSession {
		t.Fatalf("attach argv = %q", attached)
	}
}

func TestWorkDelegateCLIUsageErrors(t *testing.T) {
	store, workID := claimedDelegateCLIWork(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	cases := []struct {
		sub  string
		args []string
	}{
		{sub: "delegate", args: []string{workID}},
		{sub: "delegate", args: []string{workID, "--harness", "claude-zai", "--prompt", "x", "--", "worker"}},
		{sub: "delegate", args: []string{workID, "--harness", "claude-zai", "--expected-minutes", "never"}},
		{sub: "steer", args: []string{workID}},
		{sub: "attach", args: []string{workID, "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.sub+strings.Join(tc.args, "_"), func(t *testing.T) {
			_, errOut, code := runDelegateCLIForTest(t, tc.sub, store, runner, nil, tc.args...)
			if code != 2 || !strings.Contains(errOut, "usage: compound work "+tc.sub) {
				t.Fatalf("code=%d stderr=%q", code, errOut)
			}
		})
	}
}

func TestWorkDelegateHelpInventory(t *testing.T) {
	for _, verb := range []string{"delegate", "steer", "attach"} {
		if !strings.Contains(workUsage(""), verb) || !strings.Contains(workUsage(verb), "usage: compound work "+verb) {
			t.Fatalf("work help does not expose %s: root=%q sub=%q", verb, workUsage(""), workUsage(verb))
		}
	}
}
