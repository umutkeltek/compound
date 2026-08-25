package main

import (
	"fmt"
	"os"
	"time"

	"compound/internal/harness"
	"compound/internal/hook"
)

type hookAnswerGate struct{ *hook.AnswerGate }

func newHookAnswerGate() *hookAnswerGate {
	return &hookAnswerGate{AnswerGate: hook.NewAnswerGate()}
}

func (g *hookAnswerGate) mainAnswer(publish func()) {
	g.MainAnswer(publish)
}

func (g *hookAnswerGate) watchdogAnswer(publish func()) bool {
	return g.WatchdogAnswer(publish)
}

func startHookBudgetWatchdog(parsed hookCommandArgs, gate *hookAnswerGate, now func() time.Time) func() {
	return hook.StartBudgetWatchdog(parsed, gate.AnswerGate, now, func(answer hook.BudgetAnswer) {
		body := liveHookResponseWithEnforcement(
			harness.DefaultRegistry(), parsed.Harness, parsed.HookName, answer.Result, parsed.Enforce, nil,
		)
		printJSON(body)
		fmt.Fprintln(os.Stderr, answer.StderrLine)
		os.Exit(answer.Exit)
	})
}
