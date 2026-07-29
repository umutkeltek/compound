package hookplan

import "compound/internal/harness"

type claudeSemanticAdapter struct{}

func (claudeSemanticAdapter) Compile(
	intention HarnessIntention,
	attestation HarnessAttestation,
) (CompiledIntention, []nativeBinding) {
	switch intention {
	case IntentionPrepareContext:
		events := make([]string, 0, 2)
		if attestation.Features["session_start_context"] {
			events = append(events, "SessionStart")
		}
		if attestation.Features["user_prompt_context"] {
			events = append(events, "UserPromptSubmit")
		}
		if len(events) == 0 {
			return unsupportedIntention(intention, "context injection events are not attested"), nil
		}
		disposition := DispositionNative
		reason := ""
		if len(events) != 2 {
			disposition = DispositionDegraded
			reason = "only part of the context injection lifecycle is attested"
		}
		bindings := make([]nativeBinding, 0, len(events))
		for _, event := range events {
			bindings = append(bindings, nativeBinding{event: event})
		}
		return CompiledIntention{
			Intention:           intention,
			Disposition:         disposition,
			EnforcementStrength: "context",
			NativeEvents:        events,
			Reason:              reason,
		}, bindings
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
		return unsupportedIntention(intention, "Claude adapter does not implement this lifecycle intention"), nil
	}
}

func (claudeSemanticAdapter) Attest(capabilities harness.Capabilities) map[string]bool {
	return map[string]bool{
		"session_start_context": capabilities.SessionStartContext,
		"user_prompt_context":   capabilities.UserPromptContext,
		"pre_tool_use_block":    capabilities.PreToolUseBlock,
		"post_tool_use_event":   capabilities.PostToolUseEvent,
	}
}
