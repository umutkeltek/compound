package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/runattempt"
)

const claudePoolAdapterName = "claude-native-account-pool"

// ClaudeNativeAccountPoolAdapter uses Switchback's enrolled, isolated native
// accounts. It deliberately refuses mutation modes: a global Claude authority
// cannot attest the settings and blocking hooks of the account Switchback picks.
type ClaudeNativeAccountPoolAdapter struct {
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

func NewClaudeNativeAccountPoolAdapter(store *runattempt.Store) *ClaudeNativeAccountPoolAdapter {
	return &ClaudeNativeAccountPoolAdapter{store: store, heartbeatInterval: defaultHeartbeatInterval}
}

func (a *ClaudeNativeAccountPoolAdapter) Name() string { return claudePoolAdapterName }

type claudePoolContract struct {
	SchemaVersion  string   `json:"schema_version"`
	Provider       string   `json:"provider"`
	Accounts       []string `json:"accounts"`
	SettingsPath   string   `json:"settings_path"`
	SettingsSHA256 string   `json:"settings_sha256"`
	SwitchbackCLI  string   `json:"switchback_cli"`
	CompoundCLI    string   `json:"compound_cli"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

func (a *ClaudeNativeAccountPoolAdapter) validate(ctx context.Context, in RunInput, requireReservation bool) (claudePoolContract, error) {
	var c claudePoolContract
	if in.PermissionMode != "" && in.PermissionMode != "plan" {
		return c, errors.New("Claude pool supports plan only; selected-account mutation authorities are not attested")
	}
	if len(in.Args) != 4 || !poolHex.MatchString(in.Args[1]) || !poolHex.MatchString(in.Args[3]) {
		return c, errors.New("Claude pool requires explicit contract path, SHA256, checker path and SHA256")
	}
	if filepath.Base(filepath.Dir(in.Args[0])) != "native-account-pools" || filepath.Base(filepath.Dir(filepath.Dir(in.Args[0]))) != "config" {
		return c, errors.New("Claude pool contract must be in Switchback config/native-account-pools")
	}
	body, err := poolOwnedFile(in.Args[0], 32*1024)
	if err != nil || poolDigest(body) != in.Args[1] {
		return c, errors.New("Claude pool contract unavailable or changed")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, errors.New("Claude pool contract malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return c, errors.New("Claude pool contract has trailing data")
	}
	if (c.SchemaVersion != "compound.native_account_pool.v1" && c.SchemaVersion != "compound.native_account_pool.v3") || c.Provider != "claude" ||
		len(c.Accounts) < 1 || len(c.Accounts) > 16 || !poolHex.MatchString(c.SettingsSHA256) ||
		c.TimeoutSeconds < 1 || c.TimeoutSeconds > 2700 {
		return c, errors.New("unsupported Claude pool contract")
	}
	seen := map[string]bool{}
	for _, account := range c.Accounts {
		if !poolName.MatchString(account) || strings.EqualFold(account, "default") || seen[account] {
			return c, errors.New("Claude pool requires distinct enrolled non-default accounts")
		}
		seen[account] = true
	}
	settings, err := poolOwnedFile(c.SettingsPath, 1024*1024)
	if err != nil || poolDigest(settings) != c.SettingsSHA256 {
		return c, errors.New("Claude pool settings unavailable or changed")
	}
	for _, executable := range []string{c.SwitchbackCLI, c.CompoundCLI, in.Args[2], in.Command} {
		if err := poolExecutable(executable); err != nil {
			return c, err
		}
	}
	checker, err := poolOwnedFile(in.Args[2], 1024*1024)
	if err != nil || poolDigest(checker) != in.Args[3] {
		return c, errors.New("Claude pool checker unavailable or changed")
	}
	selected, err := os.Stat(in.Command)
	if err != nil {
		return c, err
	}
	owner, err := os.Stat(c.SwitchbackCLI)
	if err != nil || !os.SameFile(selected, owner) {
		return c, errors.New("selected Switchback executable differs from Claude pool contract")
	}
	if in.Timeout > 0 && time.Duration(c.TimeoutSeconds)*time.Second > in.Timeout {
		return c, errors.New("Claude pool timeout exceeds governed execution budget")
	}
	if in.ExecutionSelectionSnapshot != nil && in.ExecutionSelectionSnapshot.RequestedEffort != "" {
		return c, errors.New("Claude pool cannot attest requested effort")
	}
	for _, value := range in.Env {
		key := strings.SplitN(value, "=", 2)[0]
		if key == "COMPOUND_HOME" || key == "SWITCHBACK_RUNTIME_ROOT" || key == "CLAUDE_CONFIG_DIR" {
			return c, errors.New("Claude pool runtime roots cannot be overridden")
		}
	}
	if c.SchemaVersion == "compound.native_account_pool.v3" && !requireReservation {
		return c, nil
	}
	authorityArgs, err := claudePoolAuthorityArgs(c, in)
	if err != nil {
		return c, err
	}
	if err := poolAdmissionCheck(ctx, codexPoolContract{CheckerPath: in.Args[2], CompoundHome: a.store.Home()}, in.Args[0], in.Args[1], authorityArgs...); err != nil {
		return c, err
	}
	return c, nil
}

func (a *ClaudeNativeAccountPoolAdapter) Preflight(ctx context.Context, in RunInput) (RunInput, error) {
	if strings.TrimSpace(in.Prompt) == "" {
		return RunInput{}, errors.New("Claude pool requires explicit prompt")
	}
	if _, err := a.validate(ctx, in, false); err != nil {
		return RunInput{}, err
	}
	in.PermissionMode = "plan"
	return in, nil
}

func (a *ClaudeNativeAccountPoolAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	if in.StartGuard == nil || in.StartRevalidate == nil || in.ActionIntentID == "" || in.ExecutionSelectionSnapshot == nil {
		return RunOutput{}, errors.New("Claude pool requires governed work reservation and selection")
	}
	resolved, err := a.Preflight(ctx, in)
	if err != nil {
		return RunOutput{}, err
	}
	in = resolved
	c, err := a.validate(ctx, in, true)
	if err != nil {
		return RunOutput{}, err
	}
	session, err := newNativeSessionID()
	if err != nil {
		return RunOutput{}, err
	}
	snapshot := *in.ExecutionSelectionSnapshot
	snapshot.AccountPoolContractRef = in.Args[0]
	snapshot.AccountPoolContractSHA256 = "sha256:" + in.Args[1]
	snapshot.NativeSessionID = session
	snapshot.NativeSessionEvidence = claudeNativeSessionEvidence
	in.ExecutionSelectionSnapshot = &snapshot
	prior := in.StartGuard
	in.StartGuard = func(ctx context.Context, start func() error) error {
		return prior(ctx, func() error {
			if _, err := a.validate(ctx, in, true); err != nil {
				return err
			}
			return start()
		})
	}
	authorityArgs, _ := claudePoolAuthorityArgs(c, in)
	callbackArgs := append([]string{in.Args[2], "--config", in.Args[0], "--expected-config-sha256", in.Args[1], "--check"}, authorityArgs...)
	callback, _ := json.Marshal(callbackArgs)
	args := []string{"account", "run", "--provider", "claude", "--pool", strings.Join(c.Accounts, ","),
		"--permission-mode", "plan", "--max-attempts", "1", "--require-capture",
		"--expected-settings-sha256", c.SettingsSHA256, "--timeout", fmt.Sprint(c.TimeoutSeconds),
		"--session-id", session, "--admission-check-command", string(callback), "--prompt", in.Prompt}
	if in.RequestedModelID != "" {
		args = append(args, "--model", in.RequestedModelID)
	}
	build := func(ctx context.Context, input RunInput) *exec.Cmd {
		cmd := exec.CommandContext(ctx, c.SwitchbackCLI, args...)
		cmd.Dir = input.Workspace
		runtimeRoot := filepath.Dir(filepath.Dir(filepath.Dir(in.Args[0])))
		for _, value := range poolExplicitEnv(input.Env, runtimeRoot, a.store.Home()) {
			if !strings.HasPrefix(value, "CLAUDE_CONFIG_DIR=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		return cmd
	}
	return runWithSubprocess(ctx, a.store, a.Name(), in, build, a.heartbeatInterval)
}

func claudePoolAuthorityArgs(c claudePoolContract, in RunInput) ([]string, error) {
	if c.SchemaVersion != "compound.native_account_pool.v3" {
		return nil, nil
	}
	if in.WorkID == "" || in.ActionIntentID == "" || in.ExecutionPosture == nil || in.ExecutionPosture.PermissionMode != "plan" || in.ExecutionPosture.PolicyDecisionID == "" {
		return nil, errors.New("Claude read-only pool requires exact governed plan reservation")
	}
	return []string{"--work", in.WorkID, "--policy-decision", in.ExecutionPosture.PolicyDecisionID, "--action-intent", in.ActionIntentID, "--permission-mode", "plan"}, nil
}
