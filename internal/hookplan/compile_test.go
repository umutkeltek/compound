package hookplan

import (
	"slices"
	"testing"

	"compound/internal/harness"
)

func TestDefaultInteractivePosturesObserveActionsWithoutNativeMutationGuard(t *testing.T) {
	t.Parallel()

	for _, harnessName := range []string{"codex", "claude-code"} {
		harnessName := harnessName
		t.Run(harnessName, func(t *testing.T) {
			t.Parallel()

			compiled, err := CompileDefaultPosture(DefaultPostureRequest{
				Harness:         harnessName,
				Registry:        harness.DefaultRegistry(),
				HandlerRef:      "compound",
				HandlerRevision: "sha256:handler-fixture",
			})
			if err != nil {
				t.Fatal(err)
			}
			if compiled.MutationReady {
				t.Fatalf("default %s posture unexpectedly mutation-ready: %+v", harnessName, compiled)
			}
			for _, intention := range compiled.Intentions {
				if intention.Intention == IntentionGuardMutation {
					t.Fatalf("default %s posture installed a blocking mutation guard: %+v", harnessName, intention)
				}
			}
			assertCompiledIntention(t, compiled, IntentionObserveAction, DispositionNative, []string{"PreToolUse"})
			for _, binding := range compiled.HookPlan.Bindings {
				if binding.Event != "PreToolUse" {
					continue
				}
				if binding.Blocking || binding.EnforcementStrength != "observe" {
					t.Fatalf("default %s PreToolUse binding is not observation-only: %+v", harnessName, binding)
				}
				return
			}
			t.Fatalf("default %s posture omitted PreToolUse action observation", harnessName)
		})
	}
}

func TestDefaultIsolatedClaudePostureRetainsExplicitMutationGuard(t *testing.T) {
	t.Parallel()

	compiled, err := CompileDefaultPosture(DefaultPostureRequest{
		Harness:         "claude-zai",
		Registry:        harness.DefaultRegistry(),
		HandlerRef:      "compound",
		HandlerRevision: "sha256:handler-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.MutationReady {
		t.Fatalf("isolated Claude posture is not mutation-ready: %+v", compiled)
	}
	assertCompiledIntention(t, compiled, IntentionGuardMutation, DispositionNative, []string{"PreToolUse"})
	for _, binding := range compiled.HookPlan.Bindings {
		if binding.Event == "PreToolUse" {
			if !binding.Blocking || binding.EnforcementStrength != "blocking" {
				t.Fatalf("isolated Claude PreToolUse binding is not blocking: %+v", binding)
			}
			return
		}
	}
	t.Fatal("isolated Claude posture omitted PreToolUse mutation guard")
}

func TestCompileSemanticPostureThroughClaudeAdapter(t *testing.T) {
	t.Parallel()

	compiled, err := CompileSemanticPosture(testSemanticPosture("claude-code"), testClaudeAttestation())
	if err != nil {
		t.Fatal(err)
	}

	if !compiled.MutationReady {
		t.Fatalf("Claude native blocking plan must be mutation-ready: %+v", compiled)
	}
	assertCompiledIntention(t, compiled, IntentionPrepareContext, DispositionNative, []string{
		"SessionStart",
		"UserPromptSubmit",
	})
	assertCompiledIntention(t, compiled, IntentionGuardMutation, DispositionNative, []string{
		"PreToolUse",
	})
	assertCompiledIntention(t, compiled, IntentionObserveOutcome, DispositionNative, []string{
		"PostToolUse",
	})
}

func TestClaudeReceiptIntentionCompilesOnlyWhenSessionEndIsAttested(t *testing.T) {
	t.Parallel()

	receiptIntention := HarnessIntention("record_receipt")
	posture := testSemanticPosture("claude-code")
	posture.Intentions = append(posture.Intentions, receiptIntention)
	attestation := testClaudeAttestation()
	attestation.Features["session_end_receipt"] = true
	compiled, err := CompileSemanticPosture(posture, attestation)
	if err != nil {
		t.Fatal(err)
	}
	assertCompiledIntention(t, compiled, receiptIntention, DispositionNative, []string{"SessionEnd"})

	attestation.Features["session_end_receipt"] = false
	compiled, err = CompileSemanticPosture(posture, attestation)
	if err != nil {
		t.Fatal(err)
	}
	assertCompiledIntention(t, compiled, receiptIntention, DispositionUnsupported, nil)
	for _, binding := range compiled.HookPlan.Bindings {
		if binding.Event == "SessionEnd" {
			t.Fatalf("unattested SessionEnd leaked into desired bindings: %+v", binding)
		}
	}
}

func TestDefaultClaudePosturesRecordSessionEndAndCodexDoesNot(t *testing.T) {
	t.Parallel()

	for _, harnessName := range []string{"claude-code", "claude-zai", "claude-minimax", "claude-qwen"} {
		harnessName := harnessName
		t.Run(harnessName, func(t *testing.T) {
			t.Parallel()
			compiled, err := CompileDefaultPosture(DefaultPostureRequest{
				Harness:         harnessName,
				Registry:        harness.DefaultRegistry(),
				HandlerRef:      "compound",
				HandlerRevision: "sha256:handler-fixture",
			})
			if err != nil {
				t.Fatal(err)
			}
			assertCompiledIntention(t, compiled, HarnessIntention("record_receipt"), DispositionNative, []string{"SessionEnd"})
		})
	}

	compiled, err := CompileDefaultPosture(DefaultPostureRequest{
		Harness:         "codex",
		Registry:        harness.DefaultRegistry(),
		HandlerRef:      "compound",
		HandlerRevision: "sha256:handler-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, intention := range compiled.Intentions {
		if intention.Intention == HarnessIntention("record_receipt") {
			t.Fatalf("Codex default posture installed a Claude SessionEnd receipt intention: %+v", intention)
		}
	}
	for _, binding := range compiled.HookPlan.Bindings {
		if binding.Event == "SessionEnd" {
			t.Fatalf("Codex default posture installed SessionEnd: %+v", binding)
		}
	}
}

func TestCompileSemanticPostureThroughCodexAdapter(t *testing.T) {
	t.Parallel()

	compiled, err := CompileSemanticPosture(testSemanticPosture("codex"), HarnessAttestation{
		Harness:          "codex",
		Family:           "codex",
		Surface:          "hooks.json",
		Version:          "fixture",
		FeatureState:     "trusted",
		ConfigScope:      "user",
		Revision:         "sha256:codex-fixture",
		FallbackRevision: "none",
		Features: map[string]bool{
			"session_start_context": true,
			"user_prompt_context":   true,
			"pre_tool_use_block":    true,
			"post_tool_use_event":   true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !compiled.MutationReady {
		t.Fatalf("Codex native blocking plan must be mutation-ready: %+v", compiled)
	}
	assertCompiledIntention(t, compiled, IntentionPrepareContext, DispositionNative, []string{
		"SessionStart",
		"UserPromptSubmit",
	})
	assertCompiledIntention(t, compiled, IntentionGuardMutation, DispositionNative, []string{
		"PreToolUse",
	})
	assertCompiledIntention(t, compiled, IntentionObserveOutcome, DispositionNative, []string{
		"PostToolUse",
	})
}

func TestCompileSemanticPostureThroughOpenCodeAdapterKeepsBlockingGapExplicit(t *testing.T) {
	t.Parallel()

	compiled, err := CompileSemanticPosture(testSemanticPosture("opencode"), HarnessAttestation{
		Harness:          "opencode",
		Family:           "opencode",
		Surface:          "opencode.jsonc+plugin",
		Version:          "fixture",
		FeatureState:     "trusted",
		ConfigScope:      "user",
		Revision:         "sha256:opencode-fixture",
		FallbackRevision: "none",
		Features: map[string]bool{
			"chat_message_context":   true,
			"before_tool_can_deny":   false,
			"after_tool_observation": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if compiled.MutationReady {
		t.Fatalf("OpenCode cannot deny before tool execution and must not become mutation-ready: %+v", compiled)
	}
	assertCompiledIntention(t, compiled, IntentionPrepareContext, DispositionNative, []string{"chat.message"})
	assertCompiledIntention(t, compiled, IntentionGuardMutation, DispositionUnsupported, nil)
	assertCompiledIntention(t, compiled, IntentionObserveOutcome, DispositionNative, []string{"tool.execute.after"})

	for _, binding := range compiled.HookPlan.Bindings {
		if binding.Event == "tool.execute.before" {
			t.Fatalf("unsupported OpenCode pre-tool callback leaked into desired bindings: %+v", binding)
		}
	}
}

func TestCompileSemanticPostureFailsClosedForUnknownHarnessFamily(t *testing.T) {
	t.Parallel()

	_, err := CompileSemanticPosture(testSemanticPosture("mystery"), HarnessAttestation{
		Harness:      "mystery",
		Family:       "mystery",
		Surface:      "unknown",
		Version:      "fixture",
		FeatureState: "unknown",
		ConfigScope:  "unknown",
		Revision:     "sha256:mystery-fixture",
	})
	if err == nil {
		t.Fatal("unknown harness family unexpectedly compiled")
	}
}

func TestCompileSemanticPostureCannotInferFeatureSupportFromFamilyName(t *testing.T) {
	t.Parallel()

	attestation := testClaudeAttestation()
	attestation.Features["pre_tool_use_block"] = false
	compiled, err := CompileSemanticPosture(testSemanticPosture("claude-code"), attestation)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.MutationReady {
		t.Fatalf("family name manufactured native blocking support: %+v", compiled)
	}
	guard := compiledIntention(t, compiled, IntentionGuardMutation)
	if guard.Disposition != DispositionUnsupported {
		t.Fatalf("unattested mutation guard disposition = %q, want unsupported", guard.Disposition)
	}
}

func testSemanticPosture(harnessName string) SemanticPosture {
	return SemanticPosture{
		ID:              "hpost_fixture",
		Revision:        "sha256:posture-fixture",
		AuthorityRef:    "compound://harness-posture/fixture",
		Harness:         harnessName,
		HandlerRef:      "compound",
		HandlerRevision: "sha256:handler-fixture",
		Intentions: []HarnessIntention{
			IntentionPrepareContext,
			IntentionGuardMutation,
			IntentionObserveOutcome,
		},
	}
}

func assertCompiledIntention(
	t *testing.T,
	compiled CompiledPosture,
	intention HarnessIntention,
	disposition Disposition,
	wantEvents []string,
) {
	t.Helper()
	for _, got := range compiled.Intentions {
		if got.Intention != intention {
			continue
		}
		if got.Disposition != disposition {
			t.Fatalf("%s disposition = %q, want %q", intention, got.Disposition, disposition)
		}
		if !slices.Equal(got.NativeEvents, wantEvents) {
			t.Fatalf("%s native events = %v, want %v", intention, got.NativeEvents, wantEvents)
		}
		return
	}
	t.Fatalf("compiled plan missing intention %q: %+v", intention, compiled.Intentions)
}

func compiledIntention(
	t *testing.T,
	compiled CompiledPosture,
	intention HarnessIntention,
) CompiledIntention {
	t.Helper()
	for _, got := range compiled.Intentions {
		if got.Intention == intention {
			return got
		}
	}
	t.Fatalf("compiled plan missing intention %q", intention)
	return CompiledIntention{}
}

func testClaudeAttestation() HarnessAttestation {
	return HarnessAttestation{
		Harness:          "claude-code",
		Family:           "claude",
		Surface:          "settings.json",
		Version:          "fixture",
		FeatureState:     "trusted",
		ConfigScope:      "project",
		Revision:         "sha256:claude-fixture",
		FallbackRevision: "none",
		Features: map[string]bool{
			"session_start_context": true,
			"user_prompt_context":   true,
			"pre_tool_use_block":    true,
			"post_tool_use_event":   true,
		},
	}
}
