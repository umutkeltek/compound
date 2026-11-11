package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"compound/internal/runattempt"
)

const codexPoolSchema = "compound.native_account_pool.v2"
const codexPoolAdapterName = "codex-native-account-pool"

var poolHex = regexp.MustCompile("^[0-9a-f]{64}$")
var poolName = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")

// CodexNativeAccountPoolAdapter is a distinct governed transport. The existing
// CodexAdapter remains the owner of direct native invocation and is not used as
// a wrapper escape hatch.
type CodexNativeAccountPoolAdapter struct {
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

func NewCodexNativeAccountPoolAdapter(store *runattempt.Store) *CodexNativeAccountPoolAdapter {
	return &CodexNativeAccountPoolAdapter{store: store, heartbeatInterval: defaultHeartbeatInterval}
}

func (a *CodexNativeAccountPoolAdapter) Name() string { return codexPoolAdapterName }

type codexPoolContract struct {
	SchemaVersion     string            `json:"schema_version"`
	Provider          string            `json:"provider"`
	ProfileID         string            `json:"profile_id"`
	PoolName          string            `json:"pool_name"`
	ProfileRevision   string            `json:"profile_revision"`
	AuthorityRevision string            `json:"authority_revision"`
	Accounts          []string          `json:"accounts"`
	AccountSettings   map[string]string `json:"account_settings"`
	NativeExecutable  string            `json:"native_executable"`
	NativeSHA256      string            `json:"native_sha256"`
	SwitchbackCLI     string            `json:"switchback_cli"`
	CompoundCLI       string            `json:"compound_cli"`
	CompoundHome      string            `json:"compound_home"`
	CheckerPath       string            `json:"checker_path"`
	CheckerSHA256     string            `json:"checker_sha256"`
	ApprovalPolicy    string            `json:"approval_policy"`
	Sandbox           string            `json:"sandbox"`
	TimeoutSeconds    int               `json:"timeout_seconds"`
	RequireCapture    bool              `json:"require_capture"`
}

func poolOwnedFile(path string, limit int) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute owned file required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(limit) || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("owned regular file required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return nil, errors.New("owned regular file required")
	}
	body, err := readSettingSource(path)
	if err != nil || len(body) > limit {
		return nil, errors.New("owned regular file unavailable")
	}
	return body, nil
}

func poolDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func poolExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute executable required")
	}
	link, err := os.Lstat(path)
	if err != nil {
		return errors.New("executable unavailable")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("unsafe executable")
	}
	for _, item := range []os.FileInfo{link, info} {
		stat, ok := item.Sys().(*syscall.Stat_t)
		if !ok || (int(stat.Uid) != os.Getuid() && stat.Uid != 0) {
			return errors.New("unsafe executable owner")
		}
	}
	return nil
}

func poolFileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > 512*1024*1024 {
		return "", errors.New("native executable unsafe")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, 512*1024*1024+1)); err != nil {
		return "", err
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("native executable changed while hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func loadCodexPoolContract(path, expected string) (codexPoolContract, error) {
	var c codexPoolContract
	if !poolHex.MatchString(expected) {
		return c, errors.New("contract SHA256 required")
	}
	if filepath.Base(filepath.Dir(path)) != "native-account-pools" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "config" {
		return c, errors.New("contract must be in Switchback config/native-account-pools")
	}
	body, err := poolOwnedFile(path, 32*1024)
	if err != nil {
		return c, err
	}
	if poolDigest(body) != expected {
		return c, errors.New("contract revision changed")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, errors.New("contract malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return c, errors.New("contract has trailing data")
	}
	if c.SchemaVersion != codexPoolSchema || c.Provider != "codex" || !c.RequireCapture ||
		(!poolName.MatchString(c.ProfileID) || !poolName.MatchString(c.PoolName)) || len(c.Accounts) == 0 || len(c.Accounts) > 16 ||
		c.TimeoutSeconds < 1 || c.TimeoutSeconds > 2700 {
		return c, errors.New("unsupported Codex pool contract")
	}
	for _, revision := range []string{c.ProfileRevision, c.AuthorityRevision} {
		if !strings.HasPrefix(revision, "sha256:") || !poolHex.MatchString(strings.TrimPrefix(revision, "sha256:")) {
			return c, errors.New("profile or authority revision invalid")
		}
	}
	if !poolHex.MatchString(c.NativeSHA256) || !poolHex.MatchString(c.CheckerSHA256) {
		return c, errors.New("native or checker digest invalid")
	}
	seen := make(map[string]bool)
	for _, name := range c.Accounts {
		if !poolName.MatchString(name) || seen[name] {
			return c, errors.New("account names invalid")
		}
		seen[name] = true
	}
	if len(c.AccountSettings) != len(seen) {
		return c, errors.New("account settings coverage invalid")
	}
	for name, value := range c.AccountSettings {
		if !seen[name] || (value != "missing" && (!strings.HasPrefix(value, "sha256:") || !poolHex.MatchString(strings.TrimPrefix(value, "sha256:")))) {
			return c, errors.New("account settings attestation invalid")
		}
	}
	if c.ApprovalPolicy != "never" && c.ApprovalPolicy != "on-request" {
		return c, errors.New("approval policy unsupported")
	}
	if c.Sandbox != "read-only" && c.Sandbox != "workspace-write" {
		return c, errors.New("sandbox unsupported")
	}
	for _, path := range []string{c.NativeExecutable, c.SwitchbackCLI, c.CompoundCLI, c.CheckerPath, c.CompoundHome} {
		if !filepath.IsAbs(path) {
			return c, errors.New("contract path must be absolute")
		}
	}
	info, err := os.Stat(c.CompoundHome)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return c, errors.New("Compound home unsafe")
	}
	homeOwner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (int(homeOwner.Uid) != os.Getuid() && homeOwner.Uid != 0) {
		return c, errors.New("Compound home owner unsafe")
	}
	if err := poolExecutable(c.SwitchbackCLI); err != nil {
		return c, fmt.Errorf("Switchback: %w", err)
	}
	if err := poolExecutable(c.CompoundCLI); err != nil {
		return c, fmt.Errorf("Compound: %w", err)
	}
	if err := poolExecutable(c.CheckerPath); err != nil {
		return c, fmt.Errorf("checker: %w", err)
	}
	checker, err := poolOwnedFile(c.CheckerPath, 1024*1024)
	if err != nil || poolDigest(checker) != c.CheckerSHA256 {
		return c, errors.New("checker changed")
	}
	if err := poolExecutable(c.NativeExecutable); err != nil {
		return c, fmt.Errorf("native Codex executable: %w", err)
	}
	if err := verifyNativeCodexExecutable(c.NativeExecutable); err != nil {
		return c, err
	}
	nativeSHA, err := poolFileSHA(c.NativeExecutable)
	if err != nil || nativeSHA != c.NativeSHA256 {
		return c, errors.New("native Codex executable changed")
	}
	return c, nil
}

func poolPosture(mode string, c codexPoolContract) error {
	switch mode {
	case "", "plan":
		if c.ApprovalPolicy == "never" && c.Sandbox == "read-only" {
			return nil
		}
	case "default":
		if c.ApprovalPolicy == "on-request" && c.Sandbox == "workspace-write" {
			return nil
		}
	case "bypass":
		if c.ApprovalPolicy == "never" && c.Sandbox == "workspace-write" {
			return nil
		}
	}
	return errors.New("Codex permission mode differs from admitted approval policy and sandbox")
}

type poolBoundedOutput struct{ bytes.Buffer }

func (b *poolBoundedOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 16*1024 {
		return 0, errors.New("admission output oversized")
	}
	return b.Buffer.Write(data)
}

func poolExplicitEnv(extra []string, runtimeRoot, compoundHome string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+2)
	for _, item := range append(os.Environ(), extra...) {
		if !strings.HasPrefix(item, "SWITCHBACK_RUNTIME_ROOT=") &&
			!strings.HasPrefix(item, "COMPOUND_HOME=") {
			env = append(env, item)
		}
	}
	if runtimeRoot != "" {
		env = append(env, "SWITCHBACK_RUNTIME_ROOT="+runtimeRoot)
	}
	return append(env, "COMPOUND_HOME="+compoundHome)
}

func poolAdmissionCheck(ctx context.Context, c codexPoolContract, path, digest string, authorityArgs ...string) error {
	checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	args := append([]string{"--config", path, "--expected-config-sha256", digest, "--check"}, authorityArgs...)
	cmd := exec.CommandContext(checkCtx, c.CheckerPath, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = poolExplicitEnv(nil, "", c.CompoundHome)
	var out poolBoundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("native pool admission refused")
	}
	var result struct {
		Allow bool `json:"allow"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Allow {
		return errors.New("native pool admission refused")
	}
	return nil
}

func (a *CodexNativeAccountPoolAdapter) validate(ctx context.Context, in RunInput) (codexPoolContract, error) {
	var zero codexPoolContract
	if len(in.Args) != 2 {
		return zero, errors.New("Codex pool requires explicit contract path and SHA256")
	}
	path, expected := in.Args[0], in.Args[1]
	c, err := loadCodexPoolContract(path, expected)
	if err != nil {
		return zero, err
	}
	if err := poolPosture(in.PermissionMode, c); err != nil {
		return zero, err
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return zero, errors.New("Codex pool requires explicit prompt")
	}
	if in.ExecutionSelectionSnapshot != nil && in.ExecutionSelectionSnapshot.RequestedEffort != "" {
		return zero, errors.New("Codex pool cannot attest requested effort")
	}
	if err := poolExecutable(in.Command); err != nil {
		return zero, err
	}
	selected, err := os.Stat(in.Command)
	if err != nil {
		return zero, err
	}
	owner, err := os.Stat(c.SwitchbackCLI)
	if err != nil || !os.SameFile(selected, owner) {
		return zero, errors.New("selected Switchback executable differs from contract")
	}
	if in.Timeout > 0 && time.Duration(c.TimeoutSeconds)*time.Second > in.Timeout {
		return zero, errors.New("pool timeout exceeds governed execution budget")
	}
	for _, value := range in.Env {
		if strings.HasPrefix(value, "SWITCHBACK_RUNTIME_ROOT=") || strings.HasPrefix(value, "COMPOUND_HOME=") {
			return zero, errors.New("runtime roots must come from pinned pool contract")
		}
	}
	if err := poolAdmissionCheck(ctx, c, path, expected); err != nil {
		return zero, err
	}
	return c, nil
}

func (a *CodexNativeAccountPoolAdapter) Preflight(ctx context.Context, in RunInput) (RunInput, error) {
	if _, err := a.validate(ctx, in); err != nil {
		return RunInput{}, err
	}
	return in, nil
}

func (a *CodexNativeAccountPoolAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	if in.StartGuard == nil || in.StartRevalidate == nil || in.ActionIntentID == "" ||
		in.ExecutionSelectionSnapshot == nil {
		return RunOutput{}, errors.New("Codex pool requires governed work reservation and selection")
	}
	c, err := a.validate(ctx, in)
	if err != nil {
		return RunOutput{}, err
	}
	snapshot := *in.ExecutionSelectionSnapshot
	snapshot.AccountPoolContractRef = in.Args[0]
	snapshot.AccountPoolContractSHA256 = "sha256:" + in.Args[1]
	in.ExecutionSelectionSnapshot = &snapshot
	prior := in.StartGuard
	in.StartGuard = func(ctx context.Context, start func() error) error {
		checked := func() error {
			if _, err := a.validate(ctx, in); err != nil {
				return err
			}
			return start()
		}
		return prior(ctx, checked)
	}
	settingsJSON, _ := json.Marshal(c.AccountSettings)
	callback, _ := json.Marshal([]string{c.CheckerPath, "--config", in.Args[0], "--expected-config-sha256", in.Args[1], "--check"})
	args := []string{"account", "run", "--provider", "codex", "--profile", c.ProfileID, "--expected-pool-name", c.PoolName,
		"--expected-profile-revision", c.ProfileRevision,
		"--expected-authority-revision", c.AuthorityRevision,
		"--expected-native-executable", c.NativeExecutable,
		"--expected-native-sha256", c.NativeSHA256,
		"--expected-account-settings-json", string(settingsJSON),
		"--require-capture", "--approval-policy", c.ApprovalPolicy,
		"--sandbox", c.Sandbox, "--timeout", fmt.Sprint(c.TimeoutSeconds),
		"--admission-check-command", string(callback), "--prompt", in.Prompt}
	if in.RequestedModelID != "" {
		args = append(args, "--model", in.RequestedModelID)
	}
	runtimeRoot := filepath.Dir(filepath.Dir(filepath.Dir(in.Args[0])))
	build := func(ctx context.Context, input RunInput) *exec.Cmd {
		cmd := exec.CommandContext(ctx, c.SwitchbackCLI, args...)
		cmd.Dir = input.Workspace
		cmd.Env = poolExplicitEnv(input.Env, runtimeRoot, c.CompoundHome)
		return cmd
	}
	return runWithSubprocess(ctx, a.store, a.Name(), in, build, a.heartbeatInterval)
}
