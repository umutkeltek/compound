package hookplan

import "compound/internal/harness"

type codexSemanticAdapter struct{}

func (codexSemanticAdapter) Compile(
	intention HarnessIntention,
	attestation HarnessAttestation,
) (CompiledIntention, []nativeBinding) {
	switch intention {
	case IntentionPrepareContext:
		if !attestation.Features["session_start_context"] {
			return unsupportedIntention(intention, "session start context injection is not attested"), nil
		}
		return CompiledIntention{
			Intention:           intention,
			Disposition:         DispositionNative,
			EnforcementStrength: "context",
			NativeEvents:        []string{"SessionStart"},
		}, []nativeBinding{{event: "SessionStart"}}
	case IntentionPrepareTurnContext:
		if !attestation.Features["user_prompt_context"] {
			return unsupportedIntention(intention, "per-turn context injection is not attested"), nil
		}
		return CompiledIntention{
			Intention:           intention,
			Disposition:         DispositionNative,
			EnforcementStrength: "context",
			NativeEvents:        []string{"UserPromptSubmit"},
		}, []nativeBinding{{event: "UserPromptSubmit"}}
	case IntentionGuardMutation:
		if !attestation.Features["pre_tool_use_block"] {
			return unsupportedIntention(intention, "native blocking pre-tool execution is not attested"), nil
		}
		return CompiledIntention{
			Intention:           intention,
			Disposition:         DispositionNative,
			EnforcementStrength: "blocking",
			NativeEvents:        []string{"PreToolUse"},
		}, []nativeBinding{{event: "PreToolUse", matcher: "*"}}
	case IntentionObserveAction:
		if !attestation.Features["pre_tool_use_block"] {
			return unsupportedIntention(intention, "pre-tool action observation is not attested"), nil
		}
		return CompiledIntention{
			Intention:           intention,
			Disposition:         DispositionNative,
			EnforcementStrength: "observe",
			NativeEvents:        []string{"PreToolUse"},
		}, []nativeBinding{{event: "PreToolUse", matcher: "*"}}
	case IntentionObserveOutcome:
		if !attestation.Features["post_tool_use_event"] {
			return unsupportedIntention(intention, "post-tool outcome observation is not attested"), nil
		}
		return CompiledIntention{
			Intention:           intention,
			Disposition:         DispositionNative,
			EnforcementStrength: "observe",
			NativeEvents:        []string{"PostToolUse"},
		}, []nativeBinding{{event: "PostToolUse", matcher: "*"}}
	default:
		return unsupportedIntention(intention, "Codex adapter does not implement this lifecycle intention"), nil
	}
}

func (codexSemanticAdapter) Attest(capabilities harness.Capabilities) map[string]bool {
	return map[string]bool{
		"session_start_context": capabilities.SessionStartContext,
		"user_prompt_context":   capabilities.UserPromptContext,
		"pre_tool_use_block":    capabilities.PreToolUseBlock,
		"post_tool_use_event":   capabilities.PostToolUseEvent,
	}
}
