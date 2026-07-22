package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"compound/internal/runattempt"
	"compound/internal/work"
)

var delegateCLITestNow = time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)

type delegateCLIFakeRunner struct {
	mu        sync.Mutex
	live      map[string]bool
	panes     map[string]work.SessionStatus
	creates   []delegateCLICreate
	keys      []delegateCLIKeys
	kills     []string
	createErr error
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
	return &delegateCLIFakeRunner{live: map[string]bool{}, panes: map[string]work.SessionStatus{}}
}

func (f *delegateCLIFakeRunner) Available() error { return nil }
func (f *delegateCLIFakeRunner) Has(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[name], nil
}
func (f *delegateCLIFakeRunner) SessionStatus(name string) (work.SessionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ps, ok := f.panes[name]; ok {
		return ps, nil
	}
	if f.live[name] {
		return work.SessionStatus{Exists: true, ExitCode: -1}, nil
	}
	return work.SessionStatus{Exists: false}, nil
}
func (f *delegateCLIFakeRunner) Create(name, dir, command string, args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.creates = append(f.creates, delegateCLICreate{name: name, dir: dir, command: command, args: append([]string(nil), args...)})
	f.live[name] = true
	return nil
}
func (f *delegateCLIFakeRunner) SendKeys(name, text string, enter bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.live[name] {
		return fmt.Errorf("session %s is dead", name)
	}
	f.keys = append(f.keys, delegateCLIKeys{name: name, text: text, enter: enter})
	return nil
}
func (f *delegateCLIFakeRunner) Kill(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, name)
	delete(f.live, name)
	delete(f.panes, name)
	return nil
}
func (f *delegateCLIFakeRunner) AttachArgv(name string) []string {
	return []string{"tmux", "attach-session", "-t", name}
}

func claimedDelegateCLIWork(t *testing.T, holder string) (*work.Store, string) {
	t.Helper()
	store, workID, _ := claimedDelegateCLIWorkAt(t, holder)
	return store, workID
}

func claimedDelegateCLIWorkAt(t *testing.T, holder string) (*work.Store, string, string) {
	t.Helper()
	home := t.TempDir()
	store, err := work.Open(home)
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
	return store, w.ID, home
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
	if !strings.Contains(out, "status=preview") || !strings.Contains(out, "lane=claude-minimax") || !strings.Contains(out, "capacity=ok") || !strings.Contains(out, "confirmation_required=true") {
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

func TestWorkDelegateStatusIsReachableAndWritesNothing(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID, home := claimedDelegateCLIWorkAt(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	spawnOut, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-zai", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("spawn code=%d stderr=%q", code, errOut)
	}
	var spawned work.DelegateResult
	if err := json.Unmarshal([]byte(spawnOut), &spawned); err != nil || spawned.Binding == nil {
		t.Fatalf("decode spawn: %+v %v", spawned, err)
	}
	runner.panes[spawned.Binding.Session] = work.SessionStatus{Exists: true, PaneDead: true, ExitCode: 1}
	before := snapshotFiles(t, home)
	out, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil, "status", workID, "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("status code=%d stderr=%q stdout=%q", code, errOut, out)
	}
	var observed work.DelegateStatusObservation
	if err := json.Unmarshal([]byte(out), &observed); err != nil {
		t.Fatalf("decode status: %v\n%s", err, out)
	}
	if !observed.Bound || !observed.PaneStatus.PaneDead || observed.RunAttemptID != spawned.RunAttemptID || observed.RunAttemptStatus != "running" {
		t.Fatalf("status observation = %+v", observed)
	}
	after := snapshotFiles(t, home)
	if !reflect.DeepEqual(before, after) || len(runner.kills) != 0 {
		t.Fatalf("status mutated state: files_equal=%v kills=%v", reflect.DeepEqual(before, after), runner.kills)
	}
}

func TestWorkDelegateSweepDryRunWritesAndKillsNothing(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID, home := claimedDelegateCLIWorkAt(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	spawnOut, _, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-minimax", "--json")
	if code != 0 {
		t.Fatalf("spawn code=%d", code)
	}
	var spawned work.DelegateResult
	if err := json.Unmarshal([]byte(spawnOut), &spawned); err != nil || spawned.Binding == nil {
		t.Fatalf("decode spawn: %+v %v", spawned, err)
	}
	runner.panes[spawned.Binding.Session] = work.SessionStatus{Exists: true, PaneDead: true, ExitCode: 0}
	before := snapshotFiles(t, home)
	out, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		"sweep", workID, "--dry-run", "--json")
	if code != 0 || errOut != "" || !strings.Contains(out, spawned.RunAttemptID) {
		t.Fatalf("dry-run code=%d stderr=%q stdout=%q", code, errOut, out)
	}
	if after := snapshotFiles(t, home); !reflect.DeepEqual(before, after) || len(runner.kills) != 0 {
		t.Fatalf("dry-run mutated state: files_equal=%v kills=%v", reflect.DeepEqual(before, after), runner.kills)
	}
}

func TestWorkDelegateSweepTerminalizesAndRemovesDeadPane(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID := claimedDelegateCLIWork(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	spawnOut, _, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-qwen", "--json")
	if code != 0 {
		t.Fatalf("spawn code=%d", code)
	}
	var spawned work.DelegateResult
	if err := json.Unmarshal([]byte(spawnOut), &spawned); err != nil || spawned.Binding == nil {
		t.Fatalf("decode spawn: %+v %v", spawned, err)
	}
	runner.panes[spawned.Binding.Session] = work.SessionStatus{Exists: true, PaneDead: true, ExitCode: 1}
	out, errOut, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		"sweep", workID, "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("sweep code=%d stderr=%q stdout=%q", code, errOut, out)
	}
	var swept work.DelegateSweepOutcome
	if err := json.Unmarshal([]byte(out), &swept); err != nil {
		t.Fatalf("decode sweep: %v\n%s", err, out)
	}
	if swept.RunAttemptID != spawned.RunAttemptID || swept.TerminalStatus != "failed" || len(runner.kills) != 1 {
		t.Fatalf("sweep=%+v kills=%v", swept, runner.kills)
	}
}

func TestRunAttemptSweepProbeUsesExactDelegateBinding(t *testing.T) {
	allowDelegateCLIGuard(t, false)
	store, workID, home := claimedDelegateCLIWorkAt(t, "orchestrator:test")
	runner := newDelegateCLIFakeRunner()
	spawnOut, _, code := runDelegateCLIForTest(t, "delegate", store, runner, nil,
		workID, "--harness", "claude-zai", "--json")
	if code != 0 {
		t.Fatalf("spawn code=%d", code)
	}
	var spawned work.DelegateResult
	if err := json.Unmarshal([]byte(spawnOut), &spawned); err != nil || spawned.Binding == nil {
		t.Fatalf("decode spawn: %+v %v", spawned, err)
	}
	all, err := runattempt.Open(home).List()
	if err != nil {
		t.Fatal(err)
	}
	var delegated, sibling runattempt.RunAttempt
	for _, ra := range all {
		switch ra.ID {
		case spawned.RunAttemptID:
			delegated = ra
		default:
			if ra.WorkID == workID {
				sibling = ra
			}
		}
	}
	probe := buildDelegateLivenessProbeWithRunner(home, runner)
	if live, err := probe(delegated); err != nil || !live {
		t.Fatalf("live exact delegate probe = %v, %v", live, err)
	}
	if live, err := probe(sibling); err != nil || live {
		t.Fatalf("sibling claim reservation probe = %v, %v", live, err)
	}
	runner.panes[spawned.Binding.Session] = work.SessionStatus{Exists: true, PaneDead: true, ExitCode: 0}
	if live, err := probe(delegated); err != nil || live {
		t.Fatalf("dead exact delegate probe = %v, %v", live, err)
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

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = string(body)
		return nil
	}); err != nil {
		t.Fatalf("snapshot files: %v", err)
	}
	return out
}
