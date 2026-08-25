package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestHookBudgetFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want time.Duration
	}{
		{"unset gives default", nil, defaultHookBudget},
		{"valid override", map[string]string{"COMPOUND_HOOK_BUDGET": "10s"}, 10 * time.Second},
		{"invalid falls back to default", map[string]string{"COMPOUND_HOOK_BUDGET": "soon"}, defaultHookBudget},
		{"negative is a typo, not a disable-via-garbage", map[string]string{"COMPOUND_HOOK_BUDGET": "-5s"}, defaultHookBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			get := func(string) string { return "" }
			if tc.env != nil {
				get = func(k string) string { return tc.env[k] }
			}
			if got := hookBudgetFromEnv(get); got != tc.want {
				t.Fatalf("budget = %v, want %v", got, tc.want)
			}
		})
	}
	if defaultHookBudget >= 60*time.Second {
		t.Fatalf("default budget %v must sit under the harness's 60s hook kill timeout", defaultHookBudget)
	}
}

// The watchdog's answer must be a VALID live-hook response for the harness —
// the entire point is replacing a signal-killed void with a decision the
// harness can parse. Observation mode exits 0; enforced pre-tool-use exits 4
// (deny-grade fail-open), because a budget expiry under enforcement must
// never read as allow.
func TestHookBudgetAnswerShapesAndExits(t *testing.T) {
	now := time.Date(2026, 8, 24, 23, 0, 0, 0, time.UTC)
	budget := 45 * time.Second

	obs := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: "claude_code"}
	body, stderrLine, exit := hookBudgetAnswer(obs, budget, now)
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
	_, _, exit = hookBudgetAnswer(enforced, budget, now)
	if exit != 4 {
		t.Fatalf("enforced pre-tool-use exit = %d, want 4 (deny-grade fail-open)", exit)
	}

	post := hookCommandArgs{HookName: "post-tool-use", Live: true, Harness: "claude_code", Enforce: true}
	_, _, exit = hookBudgetAnswer(post, budget, now)
	if exit != 0 {
		t.Fatalf("enforced post-tool-use exit = %d, want 0 (nothing to gate)", exit)
	}

	// The codex-specific schema applies only to the stop hook (per
	// liveHookResponseWithEnforcement's branch order: session-start,
	// post-tool-use, and pre-tool-use all answer in the claude shape).
	// Assert the codex schema where it actually applies.
	codexStop := hookCommandArgs{HookName: "stop", Live: true, Harness: "codex"}
	body, _, _ = hookBudgetAnswer(codexStop, budget, now)
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"compound.codex_live_hook_response`) {
		t.Fatalf("codex stop must get the codex response schema: %s", raw)
	}
	if !strings.Contains(string(raw), envelope.FailOpenBudgetExceeded) {
		t.Fatalf("codex response must carry the budget fail-open reason: %s", raw)
	}
	if !strings.Contains(string(raw), `"status":"fail_open"`) {
		t.Fatalf("codex response must carry the fail_open status: %s", raw)
	}
}

// Exactly one of {main path, watchdog} may publish a response: two JSON
// decisions on stdout would be worse than one late one.
func TestHookAnswerGateArbitratesExactlyOnce(t *testing.T) {
	g := newHookAnswerGate()
	mainRan, watchdogRan := false, false
	g.mainAnswer(func() { mainRan = true })
	if won := g.watchdogAnswer(func() { watchdogRan = true }); won {
		t.Fatal("watchdog must not win after main answered")
	}
	if mainRan != true || watchdogRan != false {
		t.Fatalf("main=%v watchdog=%v, want true/false", mainRan, watchdogRan)
	}

	g2 := newHookAnswerGate()
	watchdogRan2, mainRan2 := false, false
	if won := g2.watchdogAnswer(func() { watchdogRan2 = true }); !won {
		t.Fatal("first watchdog call must win")
	}
	g2.mainAnswer(func() { mainRan2 = true })
	if watchdogRan2 != true || mainRan2 != false {
		t.Fatalf("watchdog=%v main=%v, want true/false", watchdogRan2, mainRan2)
	}
}

// The budget reason must be a legal enum member: an event carrying it (or a
// response echoing it) can never fail validation as an unrecognized string.
func TestBudgetExceededReasonIsInEnum(t *testing.T) {
	for _, r := range envelope.FailOpenReasons {
		if r == envelope.FailOpenBudgetExceeded {
			return
		}
	}
	t.Fatalf("%q missing from FailOpenReasons", envelope.FailOpenBudgetExceeded)
}
