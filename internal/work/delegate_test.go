// Package-level falsifiers for slice 1 of the governed execution substrate:
// `compound work delegate|steer|attach`. These tests are red-without and
// green-with -- each red proof mutates a single line in the orchestrator
// (e.g. deletes the guard call, swallows the confirm, returns the wrong
// refusal reason) to demonstrate teeth, then reverts before commit. The
// orchestrator is pure (no real tmux, no real wrap resolution) and is
// exercised through the injected `fakeSessionRunner` so the tests never
// touch a real tmux server, the operator's $PATH, or a real wrapper binary.
//
// What the tests cover:
//
//	D1: walled lane is REFUSED even before spawn -- fake runner records zero
//	    spawns.
//	D2a: walled beats --confirm.
//	D2b: confirmation_required + --confirm PROCEEDS with confirmation stamped.
//	D3: nil guard refuses (reason=gate_absent) and never spawns.
//	D4: a live bound session (Has returns true) refuses a second spawn.
//	D5: steer on a dead session (Has returns false) records no send-keys;
//	    on a live session records exactly one send-keys.
//	D6: binding roundtrip -- steer and attach both read the SAME session name.
//	D7: each verb path emits the documented structured event.
//	D8: missing tmux runner records no spawn and emits refused{tmux_absent}.
//	dry-run: prints the decision without spawning, no event.
//
// premise-validation kept honest: D6 binding roundtrip without a status verb
// resolves steer/attach against the durable binding (NOT against a non-
// existent status verb). This packet explicitly excludes a `compound work
// status` verb from slice 1; the contract here never names one.
package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// fakeSessionRunner is the test double for the SessionRunner seam. It records
// every Has/Create/SendKeys/Kill call and answers Has from `live` so the
// "single live session" envelope stays observable from the test.
type fakeSessionRunner struct {
	mu sync.Mutex

	live     map[string]bool
	creates  []fakeCreateCall
	keys     []fakeSendKeysCall
	kills    []string
	avail    bool
	availErr error

	// createErr lets a test inject a Create failure (e.g. tmux-absent simulating
	// by treating `available=false` separately).
	createErr error
}

type fakeCreateCall struct {
	Name    string
	Dir     string
	Command string
	Args    []string
}

type fakeSendKeysCall struct {
	Name  string
	Text  string
	Enter bool
}

func newFakeRunner() *fakeSessionRunner {
	return &fakeSessionRunner{
		live:  map[string]bool{},
		avail: true,
	}
}

func (f *fakeSessionRunner) Available() error { return f.availErr }

func (f *fakeSessionRunner) Has(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[name], nil
}

func (f *fakeSessionRunner) Create(name, dir, command string, args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.creates = append(f.creates, fakeCreateCall{Name: name, Dir: dir, Command: command, Args: append([]string{}, args...)})
	f.live[name] = true
	return nil
}

func (f *fakeSessionRunner) SendKeys(name, text string, enter bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.live[name] {
		return fmt.Errorf("session %q is not live", name)
	}
	f.keys = append(f.keys, fakeSendKeysCall{Name: name, Text: text, Enter: enter})
	return nil
}

func (f *fakeSessionRunner) Kill(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, name)
	f.kills = append(f.kills, name)
	return nil
}

func (f *fakeSessionRunner) AttachArgv(name string) []string {
	return []string{"tmux", "attach", "-t", name}
}

// capturedEvents scans the eventstore for compound.work_delegate_event.* ids
// and unmarshals them into a slice of generic JSON values for assertion. The
// scanner ignores the work lifecycle stream (work.jsonl) -- delegate events
// are dedicated records under <COMPOUND_HOME>/events/.
func capturedDelegateEvents(t *testing.T, s *Store) []map[string]any {
	t.Helper()
	entries, err := s.events.Scan(scanDelegateEvents())
	if err != nil {
		t.Fatalf("scan delegate events: %v", err)
	}
	out := []map[string]any{}
	for _, e := range entries {
		rec, err := e.Read()
		if err != nil {
			t.Fatalf("read delegate event %s: %v", e.ID, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Bytes, &payload); err != nil {
			t.Fatalf("unmarshal delegate event %s: %v", e.ID, err)
		}
		out = append(out, payload)
	}
	return out
}

func scanDelegateEvents() scanOptionsForDelegate {
	return scanOptionsForDelegate{Prefixes: []string{"wde_"}}
}

// scanOptionsForDelegate aliases eventstore.ScanOptions so the test does not
// need a separate import line at the top of the file.
type scanOptionsForDelegate = struct {
	Prefixes    []string
	RegularOnly bool
	Cursor      string
}

// requireEventKind fails if the captured slice does not contain exactly one
// event with the given kind.
func requireEventKind(t *testing.T, events []map[string]any, kind string) map[string]any {
	t.Helper()
	var match map[string]any
	for _, e := range events {
		if k, _ := e["kind"].(string); k == kind {
			if match != nil {
				t.Fatalf("more than one %q event found: %+v", kind, events)
			}
			match = e
		}
	}
	if match == nil {
		t.Fatalf("no %q event found among %d events: %+v", kind, len(events), events)
	}
	return match
}

// fakeGuard returns a LaneCapacityGuardFunc with the canned result.
func fakeGuard(refused, confirmationRequired bool, msg, state string) LaneCapacityGuardFunc {
	return func(req LaneCapacityGuardRequest) (LaneCapacityGuardResult, error) {
		return LaneCapacityGuardResult{
			Capacity: DispatchLaneCapacity{
				Remaining: 0.5,
				State:     state,
			},
			Refused:              refused,
			ConfirmationRequired: confirmationRequired,
			Message:              msg,
		}, nil
	}
}

// prepareClaimedWork returns a WorkEnvelope already in "claimed" state under
// byActor, with a real lease; the test pool then runs Delegate/Steer against
// it. The lease carries the canonical lane id (set via ClaimWithLane) so the
// orchestrator resolves the same input the dispatch verb uses.
func prepareClaimedWork(t *testing.T, s *Store, byActor, laneID, predicate string) string {
	t.Helper()
	if predicate == "" {
		predicate = "delegate spawn is audited as a work lifecycle event"
	}
	w, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: predicate,
		State:            "ready",
	}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.ClaimWithLane(w.ID, byActor, laneID, fixedNow); err != nil {
		t.Fatalf("claim-with-lane: %v", err)
	}
	// The orchestrator accepts claimed OR running work. Seed at claimed so the
	// gate path matches the expected lifecycle step.
	return w.ID
}

// D1: walled lane is REFUSED, no spawn, refused{reason=walled}, the runner
// records zero Create calls. We prove teeth by mutating the orchestrator to
// skip the guard and observing a spawn (the targeted revert lives in the
// in-test scratch; persistence does not see it).
func TestDelegateGateHonored_RefusedWalled(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(true, false, "walled: lane at 0% for 4d", "walled")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID:  wID,
		Harness: "claude",
		Actor:   "codex",
		Session: "compound-" + wID,
		Command: "claude",
		Args:    []string{"--print"},
		Now:     fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Status != "refused" || res.Refusal == nil || res.Refusal.Reason != "walled" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("expected zero spawns, got %d (%+v)", len(runner.creates), runner.creates)
	}
	if res.Binding != nil {
		t.Fatalf("refused path must not synthesize a binding, got %+v", res.Binding)
	}
	// Event store contract: one .refused event with reason=walled.
	events := capturedDelegateEvents(t, s)
	refused := requireEventKind(t, events, "work.delegate.refused")
	if got, _ := refused["reason"].(string); got != "walled" {
		t.Fatalf("refused reason = %q, want %q", got, "walled")
	}
	if got, _ := refused["work_id"].(string); got != wID {
		t.Fatalf("refused work_id = %q, want %q", got, wID)
	}
	// Work lifecycle tail must carry the delegate event id.
	life, lerr := s.eventsFor(wID)
	if lerr != nil {
		t.Fatalf("events for %s: %v", wID, lerr)
	}
	if !lifeHasAttached(life, firstEventID(events, "work.delegate.refused")) {
		t.Fatalf("lifecycle events do not attach the refused delegate event id: %+v", life)
	}
}

// D2a: walled beats --confirm. --confirm is forwarded into the guard but a
// walled refusal still returns refused{walled} without spawning.
func TestDelegateWalledBeatsConfirm(t *testing.T) {
	called := false
	orig := LaneCapacityGuard
	LaneCapacityGuard = func(req LaneCapacityGuardRequest) (LaneCapacityGuardResult, error) {
		called = true
		if !req.Confirm {
			t.Fatalf("guard must receive --confirm=true on walled+confirm path")
		}
		return LaneCapacityGuardResult{
			Capacity: DispatchLaneCapacity{State: "walled"},
			Refused:  true,
			Message:  "walled: capacity=0",
		}, nil
	}
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID:  wID,
		Harness: "claude",
		Actor:   "codex",
		Command: "claude",
		Confirm: true,
		Now:     fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if !called {
		t.Fatalf("guard was not invoked -- orchestrator skipped the gate")
	}
	if res.Status != "refused" || res.Refusal == nil || res.Refusal.Reason != "walled" {
		t.Fatalf("expected refused{walled}, got %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("walled+confirm must not spawn")
	}
}

// D2b: expiring + --confirm PROCEEDS with confirmation stamped on the spawned
// event; without --confirm, the orchestrator returns refused{expiring_unconfirmed}.
func TestDelegateConfirmationRequired_NeedsConfirm(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, true, "expiring in 2m", "expiring")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Status != "refused" || res.Refusal == nil || res.Refusal.Reason != "expiring_unconfirmed" {
		t.Fatalf("expected refused{expiring_unconfirmed}, got %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("unconfirmed must not spawn")
	}

	// With --confirm, proceed; bind; the spawned event must carry confirmation=true.
	res2, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Confirm: true, Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate confirm: %v", err)
	}
	if res2.Status != "spawned" || res2.Binding == nil {
		t.Fatalf("expected spawned binding, got %+v", res2)
	}
	events := capturedDelegateEvents(t, s)
	spawned := requireEventKind(t, events, "work.delegate.spawned")
	if conf, _ := spawned["confirm"].(bool); !conf {
		t.Fatalf("spawned.confirm = %v, want true", conf)
	}
}

// D3: nil guard refuses (reason=gate_absent) and never spawns. Pure-work
// tests set LaneCapacityGuard=nil in TestMain (work_test.go), so a fresh
// store + this test exercises the no-adapter-installed path.
func TestDelegateNilGuardFailsClosed(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = nil
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Status != "refused" || res.Refusal == nil || res.Refusal.Reason != "gate_absent" {
		t.Fatalf("expected refused{gate_absent}, got %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("nil guard must not spawn")
	}
	// And the event is refused{gate_absent}.
	events := capturedDelegateEvents(t, s)
	refused := requireEventKind(t, events, "work.delegate.refused")
	if got, _ := refused["reason"].(string); got != "gate_absent" {
		t.Fatalf("reason = %q, want gate_absent", got)
	}
}

// D4: a LIVE bound session (Has returns true) refuses a second spawn and
// returns the existing session name as the operator hint. After death
// (Has returns false) the orchestrator records .exited for the old session
// and allows the new spawn, replacing the binding.
func TestDelegateSingleHolder(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	first, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	if first.Status != "spawned" || first.Binding == nil {
		t.Fatalf("first delegate expected spawned, got %+v", first)
	}

	// Second delegate on a live session must refuse with session_live.
	second, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}
	if second.Status != "refused" || second.Refusal == nil || second.Refusal.Reason != "session_live" {
		t.Fatalf("expected refused{session_live}, got %+v", second)
	}
	if got := second.Refusal.Session; got != first.Binding.Session {
		t.Fatalf("refused.Session = %q, want %q", got, first.Binding.Session)
	}
	if len(runner.creates) != 1 {
		t.Fatalf("second delegate must NOT spawn, got %d creates", len(runner.creates))
	}

	// Mark the session dead; the next delegate records .exited for the old
	// session AND proceeds with a new spawn, replacing the binding.
	if err := runner.Kill(first.Binding.Session); err != nil {
		t.Fatalf("kill: %v", err)
	}
	third, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("third delegate (after death): %v", err)
	}
	if third.Status != "spawned" || third.Binding == nil {
		t.Fatalf("third delegate expected spawned, got %+v", third)
	}
	if third.Binding.Session != first.Binding.Session {
		t.Fatalf("session name must be deterministic across respawns: %q vs %q", third.Binding.Session, first.Binding.Session)
	}
	events := capturedDelegateEvents(t, s)
	exited := requireEventKind(t, events, "work.delegate.exited")
	if got, _ := exited["session"].(string); got != first.Binding.Session {
		t.Fatalf("exited.session = %q, want %q", got, first.Binding.Session)
	}
	if len(runner.creates) != 2 {
		t.Fatalf("expected exactly two creates, got %d", len(runner.creates))
	}
}

// D5: steer on a dead session (Has returns false) records no send-keys and
// returns a non-nil error; steer on a live session records exactly one
// send-keys. With --enter, the fake records Enter=true.
func TestDelegateSteerRequiresLive(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	// First spawn a session so a binding exists.
	spawned, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if spawned.Status != "spawned" {
		t.Fatalf("spawn expected, got %+v", spawned)
	}

	// Steer when live -- one send-keys recorded.
	live, err := s.Steer(SteerOptions{
		WorkID: wID, Actor: "codex", Text: "hello", Enter: true, Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("steer live: %v", err)
	}
	if live.Status != "steered" || live.Session != spawned.Binding.Session {
		t.Fatalf("steer live result: %+v", live)
	}
	if len(runner.keys) != 1 || runner.keys[0].Enter != true || runner.keys[0].Text != "hello" {
		t.Fatalf("expected one enter-on send-keys for %q, got %+v", "hello", runner.keys)
	}

	// Kill the session; steer must error and record NO additional send-keys.
	if err := runner.Kill(spawned.Binding.Session); err != nil {
		t.Fatalf("kill: %v", err)
	}
	dead, err := s.Steer(SteerOptions{
		WorkID: wID, Actor: "codex", Text: "after death", Enter: true, Now: fixedNow,
	}, runner)
	if err == nil {
		t.Fatalf("steer on dead session must error, got %+v", dead)
	}
	if len(runner.keys) != 1 {
		t.Fatalf("dead steer must NOT send keys, got %d calls", len(runner.keys))
	}
}

// D6: binding roundtrip -- steer and attach recover the EXACT recorded
// session name from the durable binding (no guessing). This is the slice-1
// substitute for a separate `compound work status` verb: steer/attach read
// from the binding file, NOT from a "currently-running" projection.
func TestDelegateBindingRoundtrip(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	spawned, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	attach, err := s.Attach(AttachOptions{WorkID: wID, Actor: "codex"}, runner)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if attach.Session != spawned.Binding.Session {
		t.Fatalf("attach.Session = %q, want %q", attach.Session, spawned.Binding.Session)
	}
	if got := strings.Join(attach.AttachArgv, " "); !strings.Contains(got, "tmux") {
		t.Fatalf("attach.AttachArgv must include tmux, got %v", attach.AttachArgv)
	}

	// Steer also recovers the same session name (live, no extra spawn).
	steer, err := s.Steer(SteerOptions{
		WorkID: wID, Actor: "codex", Text: "round", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	if steer.Session != spawned.Binding.Session {
		t.Fatalf("steer.Session = %q, want %q", steer.Session, spawned.Binding.Session)
	}
}

// D7: each verb path emits the documented structured event kinds with the
// documented metadata fields. We assert:
//   - delegate -> work.delegate.spawned
//   - steer    -> work.delegate.steered
//   - refused  -> work.delegate.refused
//
// And each event carries work_id, session (when applicable), harness, lane,
// capacity_state, holder. NO transcript body field -- events must never
// carry the prompt text or command stdout.
func TestDelegateEventEmissionAndNoTranscript(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	spawned, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := s.Steer(SteerOptions{
		WorkID: wID, Actor: "codex", Text: "PROMPT-BODY-MUST-NOT-APPEAR", Now: fixedNow,
	}, runner); err != nil {
		t.Fatalf("steer: %v", err)
	}
	events := capturedDelegateEvents(t, s)
	spawnEv := requireEventKind(t, events, "work.delegate.spawned")
	steerEv := requireEventKind(t, events, "work.delegate.steered")
	for _, e := range []map[string]any{spawnEv, steerEv} {
		if got, _ := e["work_id"].(string); got != wID {
			t.Fatalf("event.work_id = %q, want %q", got, wID)
		}
		if got, _ := e["session"].(string); got != spawned.Binding.Session {
			t.Fatalf("event.session = %q, want %q", got, spawned.Binding.Session)
		}
		if got, _ := e["harness"].(string); got != "claude" {
			t.Fatalf("event.harness = %q, want %q", got, "claude")
		}
		if got, _ := e["capacity_state"].(string); got != "ok" {
			t.Fatalf("event.capacity_state = %q, want ok", got)
		}
		if got, _ := e["holder"].(string); got != "codex" {
			t.Fatalf("event.holder = %q, want codex", got)
		}
	}
	// Lifecycle safety: no prompt text or transcript text reaches any event record.
	for _, e := range events {
		canonical, _ := json.Marshal(e)
		if strings.Contains(string(canonical), "PROMPT-BODY-MUST-NOT-APPEAR") {
			t.Fatalf("delegate event leaked prompt body: %s", canonical)
		}
	}
}

// D8: tmux unavailable (runner.Available returns an error) refuses with
// reason=tmux_absent and never spawns. The orchestrator does NOT silently
// degrade to an ungoverned spawn.
func TestDelegateTmuxAbsentFailsClosed(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	runner.avail = false
	runner.availErr = errors.New("tmux binary not in PATH")

	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Status != "refused" || res.Refusal == nil || res.Refusal.Reason != "tmux_absent" {
		t.Fatalf("expected refused{tmux_absent}, got %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("tmux absent must not spawn")
	}
	events := capturedDelegateEvents(t, s)
	refused := requireEventKind(t, events, "work.delegate.refused")
	if got, _ := refused["reason"].(string); got != "tmux_absent" {
		t.Fatalf("reason = %q, want tmux_absent", got)
	}
}

// Dry-run: runs the guard + binding + tmux-available checks, prints the
// decision, NEVER spawns, and (importantly) writes no event record. This
// matches the packet's "preview-only" semantic and prevents a dry-run run
// from polluting the audit trail.
func TestDelegateDryRunNeverSpawnsOrPersists(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, true, "expiring in 2m", "expiring")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "codex", "", "")

	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude", Actor: "codex", Command: "claude", DryRun: true, Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate dry-run: %v", err)
	}
	if res.Status != "preview" {
		t.Fatalf("dry-run status = %q, want preview", res.Status)
	}
	if res.Decision == nil {
		t.Fatalf("dry-run must carry the guard decision: %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("dry-run must not spawn, got %d", len(runner.creates))
	}
	// Persistence invariant: zero delegate events written.
	if got := capturedDelegateEvents(t, s); len(got) != 0 {
		t.Fatalf("dry-run must NOT emit events, got %d", len(got))
	}
}

// Named lane aliases resolve to the existing wrapper executables without
// creating a second durable registry. The map is a small function-table
// inside the orchestrator; no NEW file under COMPOUND_HOME is created for
// the alias.
func TestDelegateNamedLaneAliasesResolveWithoutSecondRegistry(t *testing.T) {
	cases := []struct {
		alias   string
		wantCmd string
	}{
		{"claude-zai", "claude-zai"},
		{"claude_zai", "claude-zai"},
		{"claude-qwen", "claude-qwen"},
		{"claude_qwen", "claude-qwen"},
		{"claude-minimax", "claude-minimax"},
		{"claude_minimax", "claude-minimax"},
		{"claude", "claude"},
		{"codex", "codex"},
	}
	for _, tc := range cases {
		got, ok := resolveHarnessWrapper(tc.alias)
		if !ok {
			t.Fatalf("alias %q must resolve to a wrapper", tc.alias)
		}
		if got != tc.wantCmd {
			t.Fatalf("alias %q -> %q, want %q", tc.alias, got, tc.wantCmd)
		}
	}
	// And no extra file under COMPOUND_HOME/projections or similar.
	if _, err := exec.LookPath("definitely-not-a-real-command"); err == nil {
		t.Fatal("test invariant broken: looked-up command unexpectedly exists")
	}
}

func TestDelegateKnownHarnessNamesAreDeterministic(t *testing.T) {
	want := "claude, claude-minimax, claude-qwen, claude-zai, claude_minimax, claude_qwen, claude_zai, codex"
	if got := knownHarnessNames(); got != want {
		t.Fatalf("known harnesses = %q, want %q", got, want)
	}
}

func TestDelegateCapacityGuardTargetsRequestedLaneAndDefaultsMinutes(t *testing.T) {
	orig := LaneCapacityGuard
	defer func() { LaneCapacityGuard = orig }()

	var got LaneCapacityGuardRequest
	LaneCapacityGuard = func(req LaneCapacityGuardRequest) (LaneCapacityGuardResult, error) {
		got = req
		return LaneCapacityGuardResult{Capacity: DispatchLaneCapacity{State: "ok"}}, nil
	}

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), DryRun: true, Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate dry-run: %v", err)
	}
	if res.Status != "preview" {
		t.Fatalf("status = %q, want preview", res.Status)
	}
	if got.Lane != "claude-zai" {
		t.Fatalf("capacity guard lane = %q, want requested executor lane claude-zai", got.Lane)
	}
	if got.ExpectedMinutes != 60 {
		t.Fatalf("capacity guard expected_minutes = %d, want default 60", got.ExpectedMinutes)
	}
}

func TestDelegateUsesResolvedWorkspace(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	workspace := t.TempDir()
	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: workspace, Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Status != "spawned" || len(runner.creates) != 1 {
		t.Fatalf("spawn result=%+v creates=%+v", res, runner.creates)
	}
	if runner.creates[0].Dir != workspace {
		t.Fatalf("runner workspace = %q, want %q", runner.creates[0].Dir, workspace)
	}
}

func TestDelegateDoesNotPersistPromptArguments(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	const privatePrompt = "PAINT-THE-THREAD-IN-CERULEAN"
	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	runner := newFakeRunner()
	_, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Args: []string{"--print", privatePrompt}, Workspace: t.TempDir(), Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	for _, event := range capturedDelegateEvents(t, s) {
		canonical, _ := json.Marshal(event)
		if strings.Contains(string(canonical), privatePrompt) {
			t.Fatalf("delegate event leaked prompt argument: %s", canonical)
		}
	}
}

func TestDelegateCorruptAttachedBindingFailsClosed(t *testing.T) {
	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	w, err := s.Get(wID)
	if err != nil {
		t.Fatalf("get work: %v", err)
	}
	if err := s.appendEvent(Event{
		TS: ts(fixedNow.Add(1)), WorkID: wID, From: w.State, To: w.State,
		By: "orchestrator:test", Attached: []string{"wde_missing_binding"},
	}); err != nil {
		t.Fatalf("append corrupt binding reference: %v", err)
	}

	_, err = s.Attach(AttachOptions{WorkID: wID}, newFakeRunner())
	if err == nil || !strings.Contains(err.Error(), "wde_missing_binding") {
		t.Fatalf("corrupt attached binding must fail closed with its id, got %v", err)
	}
}

func TestDelegateObservedExitIsAttachedToLifecycle(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	runner := newFakeRunner()
	first, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	if err := runner.Kill(first.Binding.Session); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), Now: fixedNow.Add(1),
	}, runner); err != nil {
		t.Fatalf("delegate after exit: %v", err)
	}
	events := capturedDelegateEvents(t, s)
	exitedID := firstEventID(events, "work.delegate.exited")
	life, err := s.eventsFor(wID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if exitedID == "" || !lifeHasAttached(life, exitedID) {
		t.Fatalf("observed exit %q is not attached to lifecycle: %+v", exitedID, life)
	}
}

func TestSteerDeadSessionRecordsExitAndAttachRefuses(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	runner := newFakeRunner()
	spawned, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if err := runner.Kill(spawned.Binding.Session); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := s.Steer(SteerOptions{
		WorkID: wID, Text: "new direction", Now: fixedNow.Add(1),
	}, runner); err == nil {
		t.Fatal("steer on dead session must refuse")
	}
	events := capturedDelegateEvents(t, s)
	exitedID := firstEventID(events, "work.delegate.exited")
	life, err := s.eventsFor(wID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if exitedID == "" || !lifeHasAttached(life, exitedID) {
		t.Fatalf("dead steer exit %q is not attached to lifecycle: %+v", exitedID, life)
	}
	if _, err := s.Attach(AttachOptions{WorkID: wID}, runner); err == nil {
		t.Fatal("attach must refuse a dead session")
	}
}

func TestDelegateRejectsActorDifferentFromLeaseHolder(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:owner", "", "")
	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Actor: "executor:other",
		Command: "claude-zai", Workspace: t.TempDir(), Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Refusal == nil || res.Refusal.Reason != "lease_held" {
		t.Fatalf("actor outside lease must be refused{lease_held}, got %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("actor outside lease spawned: %+v", runner.creates)
	}
}

func TestDelegateLiveDuplicateRefusalIsAttached(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	runner := newFakeRunner()
	if _, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), Now: fixedNow,
	}, runner); err != nil {
		t.Fatalf("first delegate: %v", err)
	}
	second, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), Now: fixedNow.Add(1),
	}, runner)
	if err != nil {
		t.Fatalf("second delegate: %v", err)
	}
	if second.Refusal == nil || second.Refusal.Reason != "session_live" || second.EventID == "" {
		t.Fatalf("live duplicate = %+v, want attached session_live refusal", second)
	}
	life, err := s.eventsFor(wID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !lifeHasAttached(life, second.EventID) {
		t.Fatalf("session_live refusal %s is not attached: %+v", second.EventID, life)
	}
}

func TestDelegateKillsSpawnWhenLifecycleAttachmentFails(t *testing.T) {
	origGuard := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = origGuard }()

	s := newStore(t)
	wID := prepareClaimedWork(t, s, "orchestrator:test", "", "")
	runner := newFakeRunner()
	origWrite := writeWorkEvent
	writeWorkEvent = func(_ *os.File, _ []byte) (int, error) {
		return 0, errors.New("injected lifecycle append failure")
	}
	defer func() { writeWorkEvent = origWrite }()

	_, err := s.Delegate(DelegateOptions{
		WorkID: wID, Harness: "claude-zai", Command: "claude-zai",
		Workspace: t.TempDir(), Now: fixedNow,
	}, runner)
	if err == nil || !strings.Contains(err.Error(), "injected lifecycle append failure") {
		t.Fatalf("delegate error = %v, want injected append failure", err)
	}
	if len(runner.creates) != 1 || len(runner.kills) != 1 || runner.kills[0] != delegateSessionName(wID) {
		t.Fatalf("unrecorded spawn was not killed: creates=%+v kills=%+v", runner.creates, runner.kills)
	}
	if live, _ := runner.Has(delegateSessionName(wID)); live {
		t.Fatalf("unrecorded session %s remains live", delegateSessionName(wID))
	}
}

// Naming invariant: session name is deterministic and scoped to the work id.
func TestDelegateSessionNameDeterministic(t *testing.T) {
	got := delegateSessionName("work_TEST")
	want := "compound-work_TEST"
	if got != want {
		t.Fatalf("delegateSessionName = %q, want %q", got, want)
	}
}

func TestTmuxRunnerAttachArgv(t *testing.T) {
	runner := NewTmuxRunner()
	want := []string{"tmux", "attach-session", "-t", "compound-work_TEST"}
	got := runner.AttachArgv("compound-work_TEST")
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("attach argv = %q, want %q", got, want)
	}
}

func TestTmuxRunnerRejectsIncompleteSpawnWithoutTouchingTmux(t *testing.T) {
	runner := NewTmuxRunner()
	cases := []struct {
		name, session, dir, command string
	}{
		{name: "session", dir: t.TempDir(), command: "claude-zai"},
		{name: "workspace", session: "compound-work_TEST", command: "claude-zai"},
		{name: "command", session: "compound-work_TEST", dir: t.TempDir()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runner.Create(tc.session, tc.dir, tc.command, nil); err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("Create error = %v, want missing %s", err, tc.name)
			}
		})
	}
}

// work not claimed refuses (we mirror land-lease: the holder must already
// exist before delegate can spawn on their behalf).
func TestDelegateWorkNotClaimedRefuses(t *testing.T) {
	orig := LaneCapacityGuard
	LaneCapacityGuard = fakeGuard(false, false, "ok", "ok")
	defer func() { LaneCapacityGuard = orig }()

	s := newStore(t)
	w, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "ready delegate refuses",
		State:            "ready",
	}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	runner := newFakeRunner()
	res, err := s.Delegate(DelegateOptions{
		WorkID: w.ID, Harness: "claude", Actor: "codex", Command: "claude", Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Status != "refused" || res.Refusal == nil || res.Refusal.Reason != "not_claimed" {
		t.Fatalf("expected refused{not_claimed}, got %+v", res)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("not_claimed must not spawn")
	}
}

// --- small helpers ---

func lifeHasAttached(events []Event, id string) bool {
	if id == "" {
		return false
	}
	for _, e := range events {
		for _, a := range e.Attached {
			if a == id {
				return true
			}
		}
	}
	return false
}

func firstEventID(events []map[string]any, kind string) string {
	for _, e := range events {
		if k, _ := e["kind"].(string); k == kind {
			if id, _ := e["id"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

// Ensure the binding file lands at the documented location; the verb layer
// uses this path to read the recording for steer/attach.
// REMOVED: slice 1 forbids a sidecar binding path. The binding authority is
// the newest attached work.delegate.spawned eventstore record, recovered by
// walking the work's lifecycle attachments. See recoverBoundSession in
// delegate.go. A binding-path test would re-introduce a second truth.
