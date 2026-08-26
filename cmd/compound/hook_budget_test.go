package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/hook"
)

func TestHookBudgetFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want time.Duration
	}{
		{"unset gives default", nil, hook.DefaultBudget},
		{"valid override", map[string]string{"COMPOUND_HOOK_BUDGET": "10s"}, 10 * time.Second},
		{"invalid falls back to default", map[string]string{"COMPOUND_HOOK_BUDGET": "soon"}, hook.DefaultBudget},
		{"negative is a typo, not a disable-via-garbage", map[string]string{"COMPOUND_HOOK_BUDGET": "-5s"}, hook.DefaultBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			get := func(string) string { return "" }
			if tc.env != nil {
				get = func(k string) string { return tc.env[k] }
			}
			if got := hook.BudgetFromEnv(get); got != tc.want {
				t.Fatalf("budget = %v, want %v", got, tc.want)
			}
		})
	}
	if hook.DefaultBudget >= 60*time.Second {
		t.Fatalf("default budget %v must sit under the harness's 60s hook kill timeout", hook.DefaultBudget)
	}
}

func budgetResponse(command hookCommandArgs, budget time.Duration, now time.Time) (body any, stderrLine string, exit int) {
	answer := hook.BudgetExpiryAnswer(command, budget, now)
	body = liveHookResponseWithEnforcement(
		harness.DefaultRegistry(), command.Harness, command.HookName, answer.Result, command.Enforce, nil,
	)
	return body, answer.StderrLine, answer.Exit
}

func TestHookBudgetAnswerShapesAndExits(t *testing.T) {
	now := time.Date(2026, 8, 24, 23, 0, 0, 0, time.UTC)
	budget := 45 * time.Second

	obs := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: "claude_code"}
	body, stderrLine, exit := budgetResponse(obs, budget, now)
	if exit != 0 {
		t.Fatalf("observation exit = %d, want 0", exit)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var claudeResp struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(raw, &claudeResp); err != nil {
		t.Fatalf("claude body is not valid JSON: %v (%s)", err, raw)
	}
	if claudeResp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("hookEventName = %q, want PreToolUse", claudeResp.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(stderrLine, envelope.FailOpenBudgetExceeded) {
		t.Fatalf("stderr must name the budget reason: %q", stderrLine)
	}

	enforced := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: "claude_code", Enforce: true}
	_, _, exit = budgetResponse(enforced, budget, now)
	if exit != 4 {
		t.Fatalf("enforced pre-tool-use exit = %d, want 4", exit)
	}

	post := hookCommandArgs{HookName: "post-tool-use", Live: true, Harness: "claude_code", Enforce: true}
	_, _, exit = budgetResponse(post, budget, now)
	if exit != 0 {
		t.Fatalf("enforced post-tool-use exit = %d, want 0", exit)
	}

	codexStop := hookCommandArgs{HookName: "stop", Live: true, Harness: "codex"}
	body, _, _ = budgetResponse(codexStop, budget, now)
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"compound.codex_live_hook_response`) ||
		!strings.Contains(string(raw), envelope.FailOpenBudgetExceeded) ||
		!strings.Contains(string(raw), `"status":"fail_open"`) {
		t.Fatalf("codex stop response is incomplete: %s", raw)
	}
}

func TestHookAnswerGateArbitratesExactlyOnce(t *testing.T) {
	gate := hook.NewAnswerGate()
	mainRan, watchdogRan := false, false
	gate.MainAnswer(func() { mainRan = true })
	if won := gate.WatchdogAnswer(func() { watchdogRan = true }); won {
		t.Fatal("watchdog must not win after main answered")
	}
	if !mainRan || watchdogRan {
		t.Fatalf("main=%v watchdog=%v, want true/false", mainRan, watchdogRan)
	}

	second := hook.NewAnswerGate()
	watchdogRan, mainRan = false, false
	if won := second.WatchdogAnswer(func() { watchdogRan = true }); !won {
		t.Fatal("first watchdog call must win")
	}
	second.MainAnswer(func() { mainRan = true })
	if !watchdogRan || mainRan {
		t.Fatalf("watchdog=%v main=%v, want true/false", watchdogRan, mainRan)
	}
}

func TestBudgetExceededReasonIsInEnum(t *testing.T) {
	for _, reason := range envelope.FailOpenReasons {
		if reason == envelope.FailOpenBudgetExceeded {
			return
		}
	}
	t.Fatalf("%q missing from FailOpenReasons", envelope.FailOpenBudgetExceeded)
}

// TestHookBudgetHarnessTierBeatsDefault: the internal fail-open deadline
// must sit under the OUTER harness kill. oh_my_pi's shim kills wrapped hooks
// at ~8s, so its registry budget (6s) must displace the 45s default — a
// 45s watchdog under an 8s kill is one that never gets to bark.
func TestHookBudgetHarnessTierBeatsDefault(t *testing.T) {
	t.Setenv("COMPOUND_HOOK_BUDGET", "")
	omp := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: "oh_my_pi"}
	budget := hook.BudgetFromEnv(os.Getenv)
	if strings.TrimSpace(os.Getenv("COMPOUND_HOOK_BUDGET")) == "" {
		if ms := harness.DefaultRegistry().HookBudgetMS(omp.Harness); ms > 0 {
			budget = time.Duration(ms) * time.Millisecond
		}
	}
	if budget != 6*time.Second {
		t.Fatalf("oh_my_pi budget = %v, want the registry's 6s (under its ~8s outer kill)", budget)
	}
	// An explicit env override still wins, even when equal to the default.
	t.Setenv("COMPOUND_HOOK_BUDGET", "10s")
	if d := hook.BudgetFromEnv(os.Getenv); d != 10*time.Second {
		t.Fatalf("explicit env override = %v, want 10s", d)
	}
	// Harnesses without a registry budget keep the default.
	if ms := harness.DefaultRegistry().HookBudgetMS("claude_code"); ms != 0 {
		t.Fatalf("claude_code unexpectedly declares a hook budget: %dms", ms)
	}
}
