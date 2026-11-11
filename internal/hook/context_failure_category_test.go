package hook

// A live context build can fail open three different ways, and the stored hook record
// must say which one, and how long it took, without carrying the error text:
//
//	build_error      construction returned an error
//	late_completion  construction finished, but after its budget
//	budget_timeout   the budget timer fired while construction was still running
//
// The category is stored on the hook record beside breaker_tripped (not on the
// envelope); the elapsed time is the event's existing duration_ms.
//
// Before this, all three reached contextBreakerResult the same way and the stored record
// kept neither the cause nor the elapsed time (duration_ms was always 0), so a failure
// could not be read back afterwards.
//
// Every test here drives ONE shared injected clock from the hooks (Deps.After,
// Deps.BeforeContextBuild). The build runs in a goroutine, so a "return the next value"
// clock would be racy; nothing here compares wall-clock time.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/eventstore"
)

// sharedClock is one injected clock value that every reader sees, safe across the
// build goroutine and the hook's own goroutine.
type sharedClock struct{ offset atomic.Int64 }

func (c *sharedClock) now() time.Time { return fixedNow.Add(time.Duration(c.offset.Load())) }

func (c *sharedClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

// neverFires is a budget timer that never fires, so only the injected clock decides.
func neverFires(time.Duration) <-chan time.Time { return make(chan time.Time) }

func liveContextFailureDeps(t *testing.T, clock *sharedClock) (Deps, time.Duration) {
	t.Helper()
	t.Setenv(SessionPackEnv, "full") // the breaker guards the pack build only the full start runs
	deps := baseDeps()
	deps.HomePath = t.TempDir()
	deps.Now = clock.now
	deps.After = neverFires
	budget := time.Duration(deps.Registry.InjectionBudgetMS("codex")) * time.Millisecond
	if budget <= 0 {
		t.Fatalf("precondition: codex needs a positive injection budget, got %s", budget)
	}
	return deps, budget
}

// readStoredHookEvent reads the hook event back from the event store, the record every
// later reader consumes, not the in-memory Result.
func readStoredHookEvent(t *testing.T, homePath, id string) (hookEventRecord, []byte) {
	t.Helper()
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.Read(id)
	if err != nil {
		t.Fatalf("hook record %s must be stored: %v", id, err)
	}
	var stored hookEventRecord
	if err := json.Unmarshal(rec.Bytes, &stored); err != nil {
		t.Fatalf("decode stored hook record: %v\n%s", err, rec.Bytes)
	}
	return stored, rec.Bytes
}

func assertContextFailureEvent(t *testing.T, res Result, homePath, wantCategory string, wantElapsed time.Duration) []byte {
	t.Helper()
	if !res.BreakerTripped || res.Event.Status != "fail_open" || res.Event.FailOpenReason != envelope.FailOpenContextUnavailable {
		t.Fatalf("precondition: want a tripped context_unavailable fail-open, got breaker=%v status=%q reason=%q",
			res.BreakerTripped, res.Event.Status, res.Event.FailOpenReason)
	}
	wantMS := int(wantElapsed.Milliseconds())
	if res.ContextFailure != wantCategory {
		t.Errorf("result context_failure = %q, want %q", res.ContextFailure, wantCategory)
	}
	if res.Event.DurationMS != wantMS {
		t.Errorf("event duration_ms = %d, want the injected elapsed %d", res.Event.DurationMS, wantMS)
	}
	stored, raw := readStoredHookEvent(t, homePath, res.Event.ID)
	// The category is the record's sibling field, beside breaker_tripped.
	if stored.ContextFailure != wantCategory {
		t.Errorf("stored record context_failure = %q, want %q\n%s", stored.ContextFailure, wantCategory, raw)
	}
	if stored.DurationMS != wantMS {
		t.Errorf("stored duration_ms = %d, want %d\n%s", stored.DurationMS, wantMS, raw)
	}
	if !stored.BreakerTripped {
		t.Errorf("stored record must still say breaker_tripped\n%s", raw)
	}
	return raw
}

// TestContextBreakerEventNamesBudgetTimeout: the timer fires while construction is still
// running. The stored record says budget_timeout and the event stores how long the hook
// waited.
//
// Falsifier: drop the category or duration stamp in contextBreakerResult, or read the
// clock anywhere but on the timer branch, and this goes RED.
func TestContextBreakerEventNamesBudgetTimeout(t *testing.T) {
	clock := &sharedClock{}
	deps, budget := liveContextFailureDeps(t, clock)
	waited := budget + 37*time.Millisecond
	deps.After = func(time.Duration) <-chan time.Time {
		clock.advance(waited)
		fired := make(chan time.Time, 1)
		fired <- clock.now()
		return fired
	}
	deps.BeforeContextBuild = func() { select {} } // construction never finishes

	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/x", Task: "audit", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContextFailureEvent(t, res, deps.HomePath, ContextFailureBudgetTimeout, waited)
}

// TestContextBreakerEventNamesLateCompletion: construction finishes, but the clock has
// passed the budget by then, and the timer never fired. The stored record says
// late_completion and the event stores the real build time, which is over the budget.
//
// Falsifier: map the over-budget branch to any other category, or stamp no duration,
// and this goes RED.
func TestContextBreakerEventNamesLateCompletion(t *testing.T) {
	clock := &sharedClock{}
	deps, budget := liveContextFailureDeps(t, clock)
	buildTime := budget + 777*time.Millisecond
	deps.BeforeContextBuild = func() { clock.advance(buildTime) }

	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/x", Task: "audit", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContextFailureEvent(t, res, deps.HomePath, ContextFailureLateCompletion, buildTime)
	if res.Event.DurationMS <= int(budget.Milliseconds()) {
		t.Errorf("late completion duration_ms = %d, want more than the %d ms budget", res.Event.DurationMS, budget.Milliseconds())
	}
}

// TestContextBreakerEventNamesBuildErrorWithoutErrorText: construction returns an error
// (an unreadable repo-kinds registry). The stored record says build_error, the event
// stores the elapsed time, and neither the result nor the stored record carries the
// error text, which here names a path inside the home directory.
//
// Falsifier: map the error branch to any other category, stamp no duration, or copy the
// error text onto the result or record, and this goes RED.
func TestContextBreakerEventNamesBuildErrorWithoutErrorText(t *testing.T) {
	clock := &sharedClock{}
	deps, _ := liveContextFailureDeps(t, clock)
	if err := os.MkdirAll(filepath.Join(deps.HomePath, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps.HomePath, "config", "repo-kinds.yaml"), []byte("schema_version: wrong\nrepos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const cwd = "/repo/x"
	_, _, buildErr := contract.ResolveRepoKindWithHome(deps.HomePath, "", cwd)
	if buildErr == nil || strings.TrimSpace(buildErr.Error()) == "" {
		t.Fatalf("precondition: the live build must hit a real error, got %v", buildErr)
	}
	spent := 321 * time.Millisecond
	deps.BeforeContextBuild = func() { clock.advance(spent) }

	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: cwd, Task: "audit", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	raw := assertContextFailureEvent(t, res, deps.HomePath, ContextFailureBuildError, spent)

	resultJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for label, body := range map[string][]byte{"result": resultJSON, "stored record": raw} {
		if strings.Contains(string(body), buildErr.Error()) {
			t.Errorf("%s carries the build error text %q:\n%s", label, buildErr.Error(), body)
		}
		if strings.Contains(string(body), "repo-kinds") {
			t.Errorf("%s carries part of the build error (the registry name):\n%s", label, body)
		}
	}
}

// TestLiveContextSuccessStatesNoContextFailure: a live build that finishes within budget
// is not a failure, so its result names no category and the stored record has no key.
func TestLiveContextSuccessStatesNoContextFailure(t *testing.T) {
	clock := &sharedClock{}
	deps, _ := liveContextFailureDeps(t, clock)
	deps.BeforeContextBuild = func() { clock.advance(3 * time.Millisecond) }

	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/x", Task: "audit", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.BreakerTripped || res.Event.Status != "ok" {
		t.Fatalf("precondition: a build within budget must succeed, got breaker=%v status=%q", res.BreakerTripped, res.Event.Status)
	}
	if res.ContextFailure != "" {
		t.Errorf("successful build result context_failure = %q, want empty", res.ContextFailure)
	}
	_, raw := readStoredHookEvent(t, deps.HomePath, res.Event.ID)
	if strings.Contains(string(raw), `"context_failure"`) {
		t.Errorf("successful build record must not carry context_failure:\n%s", raw)
	}
}
