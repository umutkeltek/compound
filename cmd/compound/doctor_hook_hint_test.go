package main

import (
	"strings"
	"testing"
)

// The model lanes are wired by `hook install --harness claude-provider-lanes`, never by
// `compound init`, so the doctor hint for an unwired lane row must name that command.
func TestDoctorHookWireHintNamesTheLaneCommand(t *testing.T) {
	lanes := "compound hook install --harness claude-provider-lanes"
	only := doctorHookWireHint([]doctorHook{
		{Harness: "claude-code", Status: "ok"},
		{Harness: "claude_kimi", Status: "absent", Selector: "claude-provider-lanes"},
		{Harness: "kimi", Status: "error", Selector: "claude-provider-lanes"},
	})
	if !strings.Contains(only, lanes) || strings.Contains(only, "compound init") {
		t.Fatalf("lane-only hint = %q, want the lane command and not init", only)
	}
	both := doctorHookWireHint([]doctorHook{
		{Harness: "codex", Status: "drift"},
		{Harness: "claude_kimi", Status: "absent", Selector: "claude-provider-lanes"},
	})
	if !strings.Contains(both, "`compound init`") || !strings.Contains(both, lanes) {
		t.Fatalf("mixed hint = %q, want both commands", both)
	}
	if got := doctorHookWireHint([]doctorHook{{Harness: "claude-code", Status: "ok"}}); got != "" {
		t.Fatalf("nothing unwired must give no hint: %q", got)
	}
	if got := doctorHookWireHint([]doctorHook{{Harness: "opencode", Status: "foreign"}, {Harness: "codex", Status: "absent"}}); !strings.Contains(got, "move it aside") {
		t.Fatalf("a foreign file met first keeps its hint: %q", got)
	}
}
