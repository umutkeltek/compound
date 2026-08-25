package main

// Wall-clock budget for the live hook command.
//
// The harness that invokes compound hooks kills them at ITS OWN timeout
// (Claude Code: 60s by default) and a killed hook produces NO output — the
// operator sees "hook exited without a status code" / "non-blocking status
// code, no stderr" and the harness gets no decision at all. Every complete
// path in runHookCommand prints a decision; none of that matters if the
// process never reaches it.
//
// The budget makes the hook ANSWER first: a watchdog fires a few seconds
// before the harness would kill us, publishes a truthful fail-open response
// (status fail_open, reason budget_exceeded), and exits with the same code
// the finished path would use for a runtime fail-open. A bounded, explained
// answer beats a SIGKILLed void.
//
// COMPOUND_HOOK_BUDGET overrides the default (45s, comfortably under the
// 60s harness default); zero or negative disables the watchdog for
// debugging.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/hook"
)

const defaultHookBudget = 45 * time.Second

// hookBudgetFromEnv resolves the budget; invalid values fall back to the
// default rather than disabling safety on a typo.
func hookBudgetFromEnv(getenv func(string) string) time.Duration {
	raw := ""
	if getenv != nil {
		raw = getenv("COMPOUND_HOOK_BUDGET")
	}
	if raw == "" {
		return defaultHookBudget
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultHookBudget
	}
	return d
}

// hookBudgetAnswer renders the decision the watchdog publishes at expiry.
// Observation mode exits 0 (fail-open, the decision is informational);
// enforced pre-tool-use exits 4, the deny-grade fail-open exit — a budget
// expiry under enforcement must not read as allow.
func hookBudgetAnswer(parsed hookCommandArgs, budget time.Duration, now time.Time) (body any, stderrLine string, exit int) {
	ev := envelope.HookEvent{
		Harness:        parsed.Harness,
		HookName:       hookEnumName(parsed.HookName),
		Status:         "fail_open",
		FailOpenReason: envelope.FailOpenBudgetExceeded,
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("budget:%s:%s:%d", parsed.Harness, parsed.HookName, now.UnixNano())))
	ev.Header = envelope.Header{
		SchemaVersion: envelope.SVHookEvent,
		ID:            "hook_" + hex.EncodeToString(sum[:6]),
		CreatedAt:     now.UTC().Format(time.RFC3339Nano),
		Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
	}
	res := hook.Result{Event: ev, Summary: fmt.Sprintf("hook exceeded its %s wall-clock budget; answered fail-open instead of being killed silently", budget)}
	body = liveHookResponseWithEnforcement(harness.DefaultRegistry(), parsed.Harness, parsed.HookName, res, parsed.Enforce, nil)
	exit = 0
	if parsed.Enforce && parsed.HookName == "pre-tool-use" {
		exit = 4
	}
	stderrLine = fmt.Sprintf("[%s/fail_open] %s (%s) — %s",
		parsed.Harness, ev.HookName, envelope.FailOpenBudgetExceeded, res.Summary)
	return body, stderrLine, exit
}

// hookAnswerGate lets exactly ONE of {main path, watchdog} publish the
// response: os.Stdout must never carry two JSON decisions. The gate also
// records which side won so tests can assert the arbitration.
type hookAnswerGate struct {
	once        sync.Once
	watchdogWon atomic.Bool
}

func newHookAnswerGate() *hookAnswerGate { return &hookAnswerGate{} }

// mainAnswer publishes from the normal completion path. If the watchdog
// already answered (and exited the process), this is a no-op — though in
// practice the watchdog's os.Exit means main never gets here.
func (g *hookAnswerGate) mainAnswer(publish func()) { g.once.Do(publish) }

// watchdogAnswer publishes from the budget watchdog. Reports whether this
// call won the arbitration.
func (g *hookAnswerGate) watchdogAnswer(publish func()) bool {
	won := false
	g.once.Do(func() {
		won = true
		g.watchdogWon.Store(true)
		publish()
	})
	return won
}

// startHookBudgetWatchdog arms the budget for a live hook command and returns
// the disarm function the main path calls when it publishes its own answer.
func startHookBudgetWatchdog(parsed hookCommandArgs, gate *hookAnswerGate, now func() time.Time) (stop func()) {
	budget := hookBudgetFromEnv(os.Getenv)
	if !parsed.Live || budget <= 0 {
		return func() {}
	}
	timer := time.AfterFunc(budget, func() {
		clock := now
		if clock == nil {
			clock = time.Now
		}
		gate.watchdogAnswer(func() {
			body, stderrLine, exit := hookBudgetAnswer(parsed, budget, clock())
			printJSON(body)
			fmt.Fprintln(os.Stderr, stderrLine)
			os.Exit(exit)
		})
	})
	return func() { timer.Stop() }
}
