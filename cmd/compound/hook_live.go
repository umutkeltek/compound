package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	hookfixtures "compound/fixtures/hooks"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/harvest"
	"compound/internal/hook"
	"compound/internal/work"
)

const hookUsage = "usage: compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> [--enforce] <fixture.json> OR compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> --live --harness <claude_code|claude_zai|claude_minimax|claude_qwen|claude_unattended|codex|oh_my_pi|opencode> [--enforce|--enforce-governed] [--parity-log <dir>] OR compound hook pre-tool-use --live --harness <name> --spool OR compound hook drain [--once|--follow] OR compound hook lessons-index [--from lessons.json] [--skills DIR]"

type hookCommandArgs = hook.BudgetCommand

type claudeLiveHookInput = hook.LiveInput

// The live input translator lives in internal/hook; the command registry it
// classifies Compound's own commands against stays here.
func init() { hook.LiveFirstPartyCommands = firstPartyCommandCatalog }

type claudeHookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

type claudeLiveHookResponse struct {
	HookSpecificOutput claudeHookSpecificOutput `json:"hookSpecificOutput"`
}

type claudeSessionEndHookResponse struct{}

type codexLiveHookResponse struct {
	SchemaVersion      string `json:"schema_version"`
	HookName           string `json:"hook_name"`
	EventID            string `json:"event_id"`
	Status             string `json:"status"`
	FailOpenReason     string `json:"fail_open_reason,omitempty"`
	AdditionalContext  string `json:"additional_context,omitempty"`
	PermissionDecision string `json:"permission_decision,omitempty"`
	PermissionReason   string `json:"permission_reason,omitempty"`
	Decision           string `json:"decision,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type liveStopTriageOutcome struct {
	Handled     bool
	Decision    string
	Reason      string
	Disposition string
	ReviewID    string
}

type liveHookParityRecord struct {
	SchemaVersion     string   `json:"schema_version"`
	HookName          string   `json:"hook_name"`
	Harness           string   `json:"harness"`
	EventID           string   `json:"event_id"`
	Status            string   `json:"status"`
	FailOpenReason    string   `json:"fail_open_reason,omitempty"`
	InputHash         string   `json:"input_hash,omitempty"`
	SummaryHash       string   `json:"summary_hash,omitempty"`
	ProducedKinds     []string `json:"produced_kinds"`
	WorkID            string   `json:"work_id,omitempty"`
	SessionID         string   `json:"session_id,omitempty"`
	ContextRequestID  string   `json:"context_request_id,omitempty"`
	ContextRequestRef string   `json:"context_request_ref,omitempty"`
	ContractID        string   `json:"contract_id,omitempty"`
	ContractRef       string   `json:"contract_ref,omitempty"`
	ContextPackID     string   `json:"context_pack_id,omitempty"`
	ContextPackRef    string   `json:"context_pack_ref,omitempty"`
	InjectionRef      string   `json:"injection_ref,omitempty"`
	CreatedAt         string   `json:"created_at"`
}

func loadHookFixture(parsed hookCommandArgs, now time.Time) (hook.Fixture, error) {
	if parsed.Live {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return hook.Fixture{}, err
		}
		// Empty stdin is a real-world scenario (background dispatch,
		// harness recovery paths, missing payload) — return a no-op
		// fixture so the caller still gets a well-formed response
		// instead of a parse error that blocks the harness.
		if len(data) == 0 {
			return hook.Fixture{Hook: parsed.HookName, Harness: hook.NormalizeHarness(parsed.Harness), Live: parsed.Live}, nil
		}
		return liveHookFixture(parsed, data, now)
	}
	return loadRecordedHookFixture(parsed)
}

func loadHookFixtureForHome(parsed hookCommandArgs, now time.Time, homePath string) (hook.Fixture, error) {
	if strings.TrimSpace(homePath) == "" {
		return hook.Fixture{}, fmt.Errorf("home-aware hook fixture load requires compatible home")
	}
	if parsed.Live {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return hook.Fixture{}, err
		}
		if len(data) == 0 {
			return hook.Fixture{Hook: parsed.HookName, Harness: hook.NormalizeHarness(parsed.Harness), Live: parsed.Live}, nil
		}
		return liveHookFixtureForHome(parsed, data, now, homePath)
	}
	return loadRecordedHookFixture(parsed)
}

func loadRecordedHookFixture(parsed hookCommandArgs) (hook.Fixture, error) {
	data, err := readHookFixtureReference(parsed.FixturePath)
	if err != nil {
		return hook.Fixture{}, err
	}
	var fx hook.Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return hook.Fixture{}, fmt.Errorf("parse hook fixture: %w", err)
	}
	fx.Hook = parsed.HookName // the CLI verb is authoritative
	return fx, nil
}

func readHookFixtureReference(ref string) ([]byte, error) {
	if hookfixtures.HasReference(ref) {
		return hookfixtures.ReadReference(ref)
	}
	return os.ReadFile(ref)
}

func liveHookFixture(parsed hookCommandArgs, data []byte, now time.Time) (hook.Fixture, error) {
	return hook.BuildLiveFixture(parsed, data, now, "", false)
}

func liveHookFixtureForHome(parsed hookCommandArgs, data []byte, now time.Time, homePath string) (hook.Fixture, error) {
	if strings.TrimSpace(homePath) == "" {
		return hook.Fixture{}, fmt.Errorf("home-aware live hook fixture requires compatible home")
	}
	return hook.BuildLiveFixture(parsed, data, now, homePath, true)
}

// liveHookResponse preserves the enforced projection expected by direct unit
// tests. Installed hooks use liveHookResponseWithEnforcement with their parsed
// --enforce mode.
func liveHookResponse(
	harnessName, hookName string,
	res hook.Result,
	stopOutcome *liveStopTriageOutcome,
) any {
	return liveHookResponseWithEnforcement(harness.DefaultRegistry(), harnessName, hookName, res, true, stopOutcome)
}

func liveHookResponseWithEnforcement(
	registry harness.Registry,
	harnessName, hookName string,
	res hook.Result,
	enforce bool,
	stopOutcome *liveStopTriageOutcome,
) any {
	if hookName == "user-prompt-submit" {
		return claudeLiveHookResponse{HookSpecificOutput: claudeHookSpecificOutput{
			HookEventName:     hook.EnumName(hookName),
			AdditionalContext: livePromptAdditionalContext(res),
		}}
	}
	if hook.NormalizeHarness(harnessName) == "codex" &&
		hookName != "session-start" && hookName != "post-tool-use" {
		if hookName == "pre-tool-use" {
			out := claudeHookSpecificOutput{
				HookEventName:     hook.EnumName(hookName),
				AdditionalContext: res.ContextOutput,
			}
			if enforce {
				decision, reason := livePermissionDecision(registry, harnessName, res)
				if decision == "" {
					return claudeLiveHookResponse{HookSpecificOutput: out}
				}
				// Codex PreToolUse supports allow/deny; a Compound hold denies this
				// attempt until its separate review/approval changes the policy state.
				out.PermissionDecision = "deny"
				out.PermissionDecisionReason = reason
			}
			return claudeLiveHookResponse{HookSpecificOutput: out}
		}
		out := codexLiveHookResponse{
			SchemaVersion:  "compound.codex_live_hook_response.v0.1",
			HookName:       hook.EnumName(hookName),
			EventID:        res.Event.Header.ID,
			Status:         res.Event.Status,
			FailOpenReason: res.Event.FailOpenReason,
		}
		if strings.TrimSpace(res.ContextOutput) != "" {
			out.AdditionalContext = res.ContextOutput
		}
		if hookName == "stop" && stopOutcome != nil {
			out.Decision = stopOutcome.Decision
			out.Reason = stopOutcome.Reason
		}
		switch hookName {
		case "session-start":
			out.AdditionalContext = liveAdditionalContext(res.Summary, res.ContextOutput)
		case "user-prompt-submit":
			out.AdditionalContext = res.ContextOutput
		case "pre-tool-use":
			if decision, reason := livePermissionDecision(registry, harnessName, res); decision != "" {
				out.PermissionDecision = decision
				out.PermissionReason = reason
			}
		}
		return out
	}
	if hookName == "session-end" {
		return claudeSessionEndHookResponse{}
	}
	out := claudeHookSpecificOutput{HookEventName: hook.EnumName(hookName)}
	if strings.TrimSpace(res.ContextOutput) != "" {
		out.AdditionalContext = res.ContextOutput
	}
	switch hookName {
	case "session-start":
		out.AdditionalContext = liveAdditionalContext(res.Summary, res.ContextOutput)
	case "user-prompt-submit":
		out.AdditionalContext = res.ContextOutput
	case "pre-tool-use":
		if enforce {
			decision, reason := livePermissionDecision(registry, harnessName, res)
			if decision == "" {
				return claudeLiveHookResponse{HookSpecificOutput: out}
			}
			out.PermissionDecision = decision
			out.PermissionDecisionReason = reason
		}
	}
	return claudeLiveHookResponse{HookSpecificOutput: out}
}

// runHookSpool answers watch-only PreToolUse in the same native shape as the
// synchronous path, carrying only matched lesson lines.
func runHookSpool(parsed hookCommandArgs, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	return hook.RunSpoolCommand(parsed.Harness, parsed.HookName, stdin, stdout, stderr, now, func(lessons string) any {
		return liveHookResponseWithEnforcement(harness.DefaultRegistry(), parsed.Harness, parsed.HookName, hook.Result{ContextOutput: lessons}, false, nil)
	})
}

// newHookSpoolReplay rebuilds one spooled call the way runHookCommand would
// have, at its call time, in observation mode. It never consumes coordination
// deliveries: the agent already ran the call and no one would read them.
func newHookSpoolReplay(homePath string) (hook.SpoolReplay, error) {
	orientation, err := loadOrientationPolicy()
	if err != nil {
		return nil, err
	}
	policyCfg, err := loadRuntimePolicyConfig()
	if err != nil {
		return nil, err
	}
	deps := hook.Deps{Registry: harness.DefaultRegistry(), Orient: orientation, Policy: policyCfg, HomePath: homePath}
	if store, err := work.Open(homePath); err == nil {
		deps.Store = store
	}
	return func(line hook.SpoolLine) error {
		at, err := time.Parse(time.RFC3339Nano, line.SpooledAt)
		if err != nil || line.Hook != "pre-tool-use" || len(line.Input) == 0 {
			return fmt.Errorf("spool line not replayable (hook %q, time %q, %d input bytes)", line.Hook, line.SpooledAt, len(line.Input))
		}
		fx, err := liveHookFixtureForHome(hookCommandArgs{HookName: line.Hook, Harness: line.Harness, Live: true}, line.Input, at, homePath)
		if err != nil {
			return err
		}
		fx.CoordinationDelivery, fx.Enforced = nil, false
		_, err = hook.Run(fx, deps, at)
		return err
	}, nil
}

func livePromptAdditionalContext(res hook.Result) string {
	if res.Event.Status != "fail_open" || res.Event.FailOpenReason != envelope.FailOpenContextUnavailable {
		return res.ContextOutput
	}
	const warning = "Context build failed. Harness may be running without Compound context."
	if strings.TrimSpace(res.ContextOutput) == "" {
		return warning
	}
	if strings.Contains(res.ContextOutput, warning) {
		return res.ContextOutput
	}
	return warning + "\n\n" + res.ContextOutput
}

func liveAdditionalContext(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n\n")
}

func prepareLiveStopTriage(parsed hookCommandArgs, homePath string, fx *hook.Fixture, now time.Time) (*liveStopTriageOutcome, error) {
	if fx == nil || !parsed.Live || parsed.HookName != "stop" || hook.NormalizeHarness(parsed.Harness) != "codex" || strings.TrimSpace(homePath) == "" {
		return nil, nil
	}
	out, err := harvest.HandleCodexStop(homePath, harvest.CodexStopInput{
		SessionID:            fx.SessionID,
		TurnID:               fx.TurnID,
		TranscriptPath:       fx.TranscriptPath,
		LastAssistantMessage: fx.LastAssistant,
		Cwd:                  fx.Cwd,
		StopHookActive:       fx.StopHookActive,
	}, now)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.WorkID) != "" {
		fx.WorkID = out.WorkID
	}
	fx.AdapterSideEffects = append([]string{}, out.SideEffects...)
	fx.StopTriageHandled = out.Handled
	return &liveStopTriageOutcome{Handled: out.Handled, Decision: out.Decision, Reason: out.Reason, Disposition: out.Disposition, ReviewID: out.ReviewID}, nil
}

func writeLiveHookParityLog(dir string, res hook.Result, now time.Time) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	record := liveHookParityRecord{
		SchemaVersion:  "compound.hook_live_parity.v0.1",
		HookName:       res.Event.HookName,
		Harness:        res.Event.Harness,
		EventID:        res.Event.Header.ID,
		Status:         res.Event.Status,
		FailOpenReason: res.Event.FailOpenReason,
		InputHash:      res.Event.InputHash,
		SummaryHash:    hook.ShortHash(res.Summary),
		ProducedKinds:  hook.ProducedKinds(res.Produced),
		WorkID:         res.Event.IDs.WorkID,
		SessionID:      res.Event.IDs.SessionID,
		CreatedAt:      now.UTC().Format(time.RFC3339),
	}
	if res.Injection != nil {
		record.ContextRequestID = res.Injection.ContextRequestID
		record.ContextRequestRef = res.Injection.ContextRequestRef
		record.ContractID = res.Injection.ContractID
		record.ContractRef = res.Injection.ContractRef
		record.ContextPackID = res.Injection.ContextPackID
		record.ContextPackRef = res.Injection.ContextPackRef
		record.InjectionRef = res.Injection.Ref
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	name := fmt.Sprintf("%s-%s.json", strings.ToLower(res.Event.HookName), res.Event.Header.ID)
	return os.WriteFile(filepath.Join(dir, name), b, 0o644)
}

func livePermissionDecision(registry harness.Registry, harnessName string, res hook.Result) (string, string) {
	return hook.NativePermissionDecisionForHarness(res, registry, hook.NormalizeHarness(harnessName))
}
