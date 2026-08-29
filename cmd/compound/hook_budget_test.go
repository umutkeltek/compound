package main

import (
	"encoding/json"
	"errors"
	"io"
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

func TestHookBudgetInvalidOverrideCannotDefeatHarnessDeadline(t *testing.T) {
	for _, alias := range []string{"oh_my_pi", "oh-my-pi"} {
		command := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: alias}
		if got := resolveHookBudget(command, "soon"); got != 6*time.Second {
			t.Fatalf("%s invalid override budget = %v, want 6s", alias, got)
		}
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
	enforcedAnswer := hook.BudgetExpiryAnswer(enforced, budget, now)
	if enforcedAnswer.Exit != 4 {
		t.Fatalf("enforced pre-tool-use exit = %d, want 4", enforcedAnswer.Exit)
	}
	if enforcedAnswer.Result.Event.Status != "blocked" || enforcedAnswer.Result.Event.FailOpenReason != "" ||
		enforcedAnswer.Result.Event.PolicyPosture != "enforced_block" ||
		enforcedAnswer.Result.Event.EffectiveOutcome != "block" ||
		enforcedAnswer.Result.Event.HarnessExitBehavior != "exit_4" ||
		enforcedAnswer.Result.Event.EvidencePersistence != "not_persisted_watchdog_response" {
		t.Fatalf("enforced timeout event lies about effective outcome: %+v", enforcedAnswer.Result.Event)
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

func TestRunHookCommandWithoutHomeReusesOuterAnswerGate(t *testing.T) {
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "SessionStart",
		"session_id":      "synthetic-session",
		"cwd":             t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdinWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatal(err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinReader, stdoutWriter
	defer func() {
		os.Stdin, os.Stdout = oldStdin, oldStdout
		stdinReader.Close()
		stdoutReader.Close()
	}()

	gate := newHookAnswerGate()
	gate.mainAnswer(func() {})
	code := runHookCommandWithoutHome(
		hookCommandArgs{HookName: "session-start", Live: true, Harness: "oh_my_pi"},
		time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC),
		errors.New("synthetic incompatible home"),
		gate,
	)
	os.Stdout = oldStdout
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("fallback exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(string(output)); got != "" {
		t.Fatalf("pre-won outer gate allowed fallback to publish a second answer: %s", got)
	}
}

func TestHookUsageNamesSupportedOmpHarness(t *testing.T) {
	if !strings.Contains(hookUsage, "oh_my_pi") {
		t.Fatalf("live hook usage omits supported oh_my_pi harness: %s", hookUsage)
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
	for _, alias := range []string{"oh_my_pi", "oh-my-pi"} {
		omp := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: alias}
		if got := resolveHookBudget(omp, ""); got != 6*time.Second {
			t.Fatalf("%s absent override budget = %v, want the registry's 6s", alias, got)
		}
		if got := resolveHookBudget(omp, "2s"); got != 2*time.Second {
			t.Fatalf("%s shorter override budget = %v, want 2s", alias, got)
		}
		if got := resolveHookBudget(omp, "10s"); got != 6*time.Second {
			t.Fatalf("%s excessive override budget = %v, want clamp to 6s", alias, got)
		}
	}
	// Harnesses without a registry budget keep the default.
	claude := hookCommandArgs{HookName: "pre-tool-use", Live: true, Harness: "claude_code"}
	if got := resolveHookBudget(claude, ""); got != hook.DefaultBudget {
		t.Fatalf("claude_code absent override budget = %v, want %v", got, hook.DefaultBudget)
	}
}
