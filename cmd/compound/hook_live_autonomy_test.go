package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/hook"
	"compound/internal/policy"
)

// These two tests close the loop the unit tests can only reach halfway: the
// unit tests prove the policy layer clears a hold and the hook layer projects
// it, but the LIVE hook is where the two meet, and it is the live hook whose
// output actually reaches a lane. Both defects this work item repairs were
// invisible to every layer test that existed:
//
//   - liveMonitoredAction dropped the cwd it already had on `in`, so every shell
//     intent arrived at the grant matcher with no path.
//   - liveHookResponseWithEnforcement projected every hold to "ask", including
//     on lanes with nobody to answer.

func autonomyLiveNow() time.Time {
	return time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
}

// TestLiveShellIntentCarriesTheHookCwd is the falsifier for the dropped cwd. It
// asserts on the ActionIntent the live path actually builds, because that is
// the object the grant matcher sees -- an assertion on the classifier would
// have stayed green through the entire defect.
func TestLiveShellIntentCarriesTheHookCwd(t *testing.T) {
	workspace := t.TempDir()
	in := claudeLiveHookInput{
		HookEventName: "PreToolUse",
		SessionID:     "sess_autonomy",
		Cwd:           workspace,
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": "go test ./..."},
	}
	actions, err := liveActionIntents("claude_minimax", in, autonomyLiveNow())
	if err != nil {
		t.Fatalf("live action intents: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("actions=%d, want 1", len(actions))
	}
	if got := actions[0].Target.Path; got != filepath.Clean(workspace) {
		t.Fatalf("live shell intent target path=%q, want the hook cwd %q", got, filepath.Clean(workspace))
	}

	// And it must reach an allowing decision under the operator's real config
	// shape: L3 with one path-scoped grant over the workspace.
	cfg := policy.DefaultConfig()
	cfg.OperatorAutonomyLevel = policy.AutonomyLevelFullProjectScope
	cfg.OperatorAutonomyAuthorityRef = policy.OperatorStateAuthorityRef
	cfg.StandingGrants = []policy.StandingGrant{{
		ID: "grant_workspace", TargetPath: filepath.ToSlash(workspace) + "/**",
	}}
	in.ToolInput = map[string]any{"command": `python3 -c "print(1)"`}
	in.Cwd = filepath.Join(workspace, "repo")
	held, err := liveActionIntents("claude_minimax", in, autonomyLiveNow())
	if err != nil {
		t.Fatalf("live action intents: %v", err)
	}
	if pd := policy.Evaluate(held[0], cfg, autonomyLiveNow()); !policy.Allowed(pd) {
		t.Fatalf("in-envelope command outcome=%q reasons=%v; want allowed", pd.Outcome, pd.Reasons)
	}
}

func TestLivePreToolUseRemoteURIDoesNotInheritLocalProjectGrant(t *testing.T) {
	workspace := t.TempDir()
	locator := "ssh://example-apps.internal/srv/stacks/searxng"
	payload, err := json.Marshal(claudeLiveHookInput{
		HookEventName:      "PreToolUse",
		SessionID:          "sess_remote_uri",
		Cwd:                workspace,
		ToolName:           "mcp__fixture__probe",
		ToolSchemaRevision: "fixture:v1",
		ToolInput:          map[string]any{"path": locator},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := liveHookFixture(hookCommandArgs{
		HookName: "pre-tool-use",
		Harness:  "claude_minimax",
		Live:     true,
		Enforce:  true,
	}, payload, autonomyLiveNow())
	if err != nil {
		t.Fatal(err)
	}
	cfg := policy.DefaultConfig()
	cfg.OperatorAutonomyLevel = policy.AutonomyLevelFullProjectScope
	cfg.OperatorAutonomyAuthorityRef = policy.OperatorStateAuthorityRef
	cfg.StandingGrants = []policy.StandingGrant{{
		ID:         "grant_local_project_only",
		TargetPath: filepath.ToSlash(workspace) + "/**",
	}}
	result, err := hook.Run(fixture, hook.Deps{
		Registry: harness.DefaultRegistry(),
		Orient:   contract.DefaultPolicy(),
		Policy:   cfg,
	}, autonomyLiveNow())
	if err != nil {
		t.Fatal(err)
	}
	var action *envelope.ActionIntent
	var decision *envelope.PolicyDecision
	for _, produced := range result.Produced {
		switch typed := produced.(type) {
		case *envelope.ActionIntent:
			action = typed
		case *envelope.PolicyDecision:
			decision = typed
		}
	}
	if action == nil || decision == nil {
		t.Fatalf("produced=%+v, want action and policy decision", result.Produced)
	}
	if action.Target.Path != "" || action.Target.ID != locator || action.Destination != locator {
		t.Fatalf("remote target localized: %+v", action)
	}
	if action.SecurityFloorOutcome != "hold" || !slices.Contains(action.SecurityReasons, hook.NonLocalTargetSecurityReason) {
		t.Fatalf("remote target security floor=%q reasons=%v", action.SecurityFloorOutcome, action.SecurityReasons)
	}
	if policy.Allowed(*decision) || decision.StandingGrantID != "" {
		t.Fatalf("remote target inherited local grant: %+v", decision)
	}
}

// TestLiveHoldProjectsToDenyOnALaneAndAskInTheOperatorSession is the falsifier
// for the freeze, asserted on the JSON the harness actually reads.
func TestLiveHoldProjectsToDenyOnALaneAndAskInTheOperatorSession(t *testing.T) {
	hold := &envelope.PolicyDecision{
		Header:         envelope.Header{SchemaVersion: envelope.SVPolicyDecision, ID: "pol_autonomy_hold"},
		ActionIntentID: "act_autonomy_hold",
		Outcome:        "hold",
		Reasons:        []string{"baseline for mutation_class \"security\" is hold"},
		Obligations:    []string{},
	}
	res := hook.Result{
		Event:    envelope.HookEvent{Status: "blocked"},
		Produced: []envelope.Envelope{hold},
	}
	registry := harness.DefaultRegistry()

	for _, tc := range []struct {
		harnessName string
		want        string
	}{
		{"claude_code", "ask"},
		{"claude-code", "ask"},
		{"claude_minimax", "deny"},
		{"claude-minimax", "deny"},
		{"claude_qwen", "deny"},
		{"claude_kimi", "deny"},
		{"claude_zai", "deny"},
	} {
		t.Run(tc.harnessName, func(t *testing.T) {
			out, ok := liveHookResponseWithEnforcement(
				registry, tc.harnessName, "pre-tool-use", res, true, nil,
			).(claudeLiveHookResponse)
			if !ok {
				t.Fatalf("response type mismatch for %q", tc.harnessName)
			}
			if got := out.HookSpecificOutput.PermissionDecision; got != tc.want {
				t.Fatalf("%s permissionDecision=%q, want %q", tc.harnessName, got, tc.want)
			}
			if strings.TrimSpace(out.HookSpecificOutput.PermissionDecisionReason) == "" {
				t.Fatalf("%s carries no reason; a lane needs something to act on", tc.harnessName)
			}
		})
	}

	// Observation mode still publishes nothing at all: posture decides what an
	// ENFORCED hold looks like, never whether an unenforced hook starts gating.
	out := liveHookResponseWithEnforcement(
		registry, "claude_minimax", "pre-tool-use", res, false, nil,
	).(claudeLiveHookResponse)
	if out.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("unenforced lane published a permission decision: %+v", out.HookSpecificOutput)
	}
}
