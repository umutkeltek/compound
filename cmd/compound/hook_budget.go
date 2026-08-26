package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/harness"
	"compound/internal/hook"
)

type hookAnswerGate struct{ *hook.AnswerGate }

func newHookAnswerGate() *hookAnswerGate { return &hookAnswerGate{AnswerGate: hook.NewAnswerGate()} }

func (g *hookAnswerGate) mainAnswer(publish func()) { g.MainAnswer(publish) }

func (g *hookAnswerGate) watchdogAnswer(publish func()) bool {
	return g.WatchdogAnswer(publish)
}

func startHookBudgetWatchdog(parsed hookCommandArgs, gate *hookAnswerGate, now func() time.Time) func() {
	// Resolution order: operator env override > harness-specific budget >
	// default. A harness whose outer shim kills hooks at ~8s (oh_my_pi)
	// declares HookBudgetMS in the registry so the internal fail-open answer
	// fires FIRST — a 45s default under an 8s kill is a watchdog that never
	// gets to bark. An EXPLICIT env value (even the default duration) always
	// wins over the registry tier.
	budget := hook.BudgetFromEnv(os.Getenv)
	if strings.TrimSpace(os.Getenv("COMPOUND_HOOK_BUDGET")) == "" {
		if ms := harness.DefaultRegistry().HookBudgetMS(parsed.Harness); ms > 0 {
			budget = time.Duration(ms) * time.Millisecond
		}
	}
	return hook.StartBudgetWatchdogWithBudget(parsed, gate.AnswerGate, now, budget, func(answer hook.BudgetAnswer) {
		body := liveHookResponseWithEnforcement(
			harness.DefaultRegistry(), parsed.Harness, parsed.HookName, answer.Result, parsed.Enforce, nil,
		)
		printJSON(body)
		fmt.Fprintln(os.Stderr, answer.StderrLine)
		os.Exit(answer.Exit)
	})
}
