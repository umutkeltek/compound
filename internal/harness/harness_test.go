package harness

import "testing"

func TestSupportsByHarness(t *testing.T) {
	r := DefaultRegistry()
	// claude_code can block a tool call; codex cannot (must be enforced via wrapper).
	if !r.Supports("claude_code", "PreToolUse") {
		t.Error("claude_code should support PreToolUse block")
	}
	if r.Supports("codex", "PreToolUse") {
		t.Error("codex should NOT be assumed to block a tool call natively")
	}
	// Both capture session-end receipts.
	if !r.Supports("claude_code", "SessionEnd") || !r.Supports("codex", "SessionEnd") {
		t.Error("both should support session-end receipts")
	}
}

func TestGenericFallback(t *testing.T) {
	r := DefaultRegistry()
	h, ok := r.Get("some-unknown-harness")
	if ok {
		t.Error("unknown harness should not be found")
	}
	if !h.Fallback {
		t.Error("fallback flag should be set on the generic stand-in")
	}
	if r.Supports("some-unknown-harness", "PreToolUse") {
		t.Error("generic fallback must not claim to block tool calls (fail-open)")
	}
	if len(h.Limitations) == 0 {
		t.Error("generic harness should carry its fail-open limitation")
	}
}

func TestNamesSorted(t *testing.T) {
	got := DefaultRegistry().Names()
	if len(got) != 2 || got[0] != "claude_code" || got[1] != "codex" {
		t.Errorf("Names() = %v, want sorted [claude_code codex]", got)
	}
}
