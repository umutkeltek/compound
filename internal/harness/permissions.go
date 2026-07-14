package harness

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"compound/internal/envelope"
	homepkg "compound/internal/home"
	"compound/internal/policy"

	"gopkg.in/yaml.v3"
)

const (
	PermissionAuthoritySchemaVersion = "compound.harness_permission_authority.v0.1"
	HarnessArtifactSchemaVersion     = "compound.harness_artifact.v0.1"
)

type PermissionRules struct {
	Allow       []string `json:"allow" yaml:"allow"`
	Deny        []string `json:"deny" yaml:"deny"`
	Ask         []string `json:"ask" yaml:"ask"`
	DefaultMode string   `json:"default_mode" yaml:"default_mode"`
}

type AutoModePolicy struct {
	Allow []string `json:"allow" yaml:"allow"`
}

type PermissionAuthority struct {
	SchemaVersion string          `json:"schema_version" yaml:"schema_version"`
	Harness       string          `json:"harness" yaml:"harness"`
	AuthorityRef  string          `json:"authority_ref" yaml:"authority_ref"`
	TargetPath    string          `json:"target_path" yaml:"target_path"`
	Permissions   PermissionRules `json:"permissions" yaml:"permissions"`
	AutoMode      AutoModePolicy  `json:"auto_mode" yaml:"auto_mode"`
}

type HarnessArtifact struct {
	SchemaVersion      string              `json:"schema_version"`
	Path               string              `json:"path"`
	Harness            string              `json:"harness"`
	SourceAuthorityRef string              `json:"source_authority_ref"`
	SourceRevision     string              `json:"source_revision"`
	ContentSHA256      string              `json:"content_sha256"`
	GeneratedAt        string              `json:"generated_at,omitempty"`
	ManagedEntries     map[string][]string `json:"managed_entries,omitempty"`
}

type PermissionRender struct {
	TargetPath string          `json:"target_path"`
	Changed    bool            `json:"changed"`
	Content    []byte          `json:"-"`
	Preimage   []byte          `json:"-"`
	PreExisted bool            `json:"-"`
	Artifact   HarnessArtifact `json:"artifact"`
	Details    []string        `json:"details,omitempty"`
}

const permissionApplyPendingSchemaVersion = "compound.harness_permission_apply_pending.v0.1"

type permissionApplyPending struct {
	SchemaVersion      string `json:"schema_version"`
	Harness            string `json:"harness"`
	TargetPath         string `json:"target_path"`
	RecoveryName       string `json:"recovery_name,omitempty"`
	BeforeExisted      bool   `json:"before_existed"`
	BeforeContent      []byte `json:"before_content,omitempty"`
	BeforeSHA256       string `json:"before_sha256,omitempty"`
	IntendedSHA256     string `json:"intended_sha256"`
	SourceAuthorityRef string `json:"source_authority_ref"`
	SourceRevision     string `json:"source_revision"`
}

type PermissionDriftReport struct {
	Harness    string   `json:"harness"`
	TargetPath string   `json:"target_path"`
	Drifted    bool     `json:"drifted"`
	Details    []string `json:"details,omitempty"`
}

func PermissionAuthorityRef(harness string) string {
	return "compound://config/harness-permissions/" + strings.TrimSpace(harness)
}

func PermissionAuthorityPath(homePath, harness string) string {
	return filepath.Join(homePath, "config", "harness-permissions", strings.TrimSpace(harness)+".yaml")
}

func HarnessArtifactManifestPath(homePath string) string {
	return filepath.Join(homePath, "config", "harness-artifacts.jsonl")
}

func (p PermissionAuthority) Validate() error {
	if p.SchemaVersion != PermissionAuthoritySchemaVersion {
		return fmt.Errorf("schema_version = %q, want %q", p.SchemaVersion, PermissionAuthoritySchemaVersion)
	}
	if strings.TrimSpace(p.Harness) == "" || strings.TrimSpace(p.TargetPath) == "" {
		return fmt.Errorf("harness and target_path are required")
	}
	if strings.TrimSpace(p.AuthorityRef) != PermissionAuthorityRef(p.Harness) {
		return fmt.Errorf("authority_ref = %q, want %q", p.AuthorityRef, PermissionAuthorityRef(p.Harness))
	}
	for name, rules := range map[string][]string{
		"permissions.allow": p.Permissions.Allow,
		"permissions.deny":  p.Permissions.Deny,
		"permissions.ask":   p.Permissions.Ask,
		"auto_mode.allow":   p.AutoMode.Allow,
	} {
		seen := map[string]bool{}
		for _, rule := range rules {
			rule = strings.TrimSpace(rule)
			if rule == "" {
				return fmt.Errorf("%s contains an empty rule", name)
			}
			if seen[rule] {
				return fmt.Errorf("%s contains duplicate rule %q", name, rule)
			}
			seen[rule] = true
		}
	}
	for _, rule := range p.Permissions.Allow {
		if strings.TrimSpace(rule) == "mcp__*" {
			return fmt.Errorf("permissions.allow rule %q is unsupported; use a literal server rule such as mcp__gbrain__*", rule)
		}
	}
	if len(p.AutoMode.Allow) == 0 || strings.TrimSpace(p.AutoMode.Allow[0]) != "$defaults" {
		return fmt.Errorf("auto_mode.allow must place $defaults first")
	}
	for i := 1; i < len(p.AutoMode.Allow); i++ {
		if strings.TrimSpace(p.AutoMode.Allow[i]) == "$defaults" {
			return fmt.Errorf("auto_mode.allow must contain $defaults once, first")
		}
	}
	return nil
}

func LoadPermissionAuthority(homePath, harness string) (PermissionAuthority, string, error) {
	path := PermissionAuthorityPath(homePath, harness)
	body, err := os.ReadFile(path)
	if err != nil {
		return PermissionAuthority{}, "", fmt.Errorf("read permission authority %s: %w", path, err)
	}
	var policy PermissionAuthority
	if err := yaml.Unmarshal(body, &policy); err != nil {
		return PermissionAuthority{}, "", fmt.Errorf("parse permission authority %s: %w", path, err)
	}
	if err := policy.Validate(); err != nil {
		return PermissionAuthority{}, "", fmt.Errorf("validate permission authority %s: %w", path, err)
	}
	if policy.Harness != strings.TrimSpace(harness) {
		return PermissionAuthority{}, "", fmt.Errorf("permission authority %s declares harness %q, want %q", path, policy.Harness, strings.TrimSpace(harness))
	}
	policy.Permissions.Allow = sortedRules(policy.Permissions.Allow)
	policy.Permissions.Deny = sortedRules(policy.Permissions.Deny)
	policy.Permissions.Ask = sortedRules(policy.Permissions.Ask)
	policy.AutoMode.Allow = append([]string{"$defaults"}, sortedRules(policy.AutoMode.Allow[1:])...)
	return policy, "sha256:" + digest(body), nil
}

func RenderPermissions(homePath, harness string) (PermissionRender, error) {
	policy, sourceRevision, err := LoadPermissionAuthority(homePath, harness)
	if err != nil {
		return PermissionRender{}, err
	}
	before, err := os.ReadFile(policy.TargetPath)
	if err != nil && !os.IsNotExist(err) {
		return PermissionRender{}, fmt.Errorf("read settings target %s: %w", policy.TargetPath, err)
	}
	beforeExisted := err == nil
	settings := map[string]any{}
	if len(bytes.TrimSpace(before)) > 0 {
		if err := json.Unmarshal(before, &settings); err != nil {
			return PermissionRender{}, fmt.Errorf("parse settings target %s: %w", policy.TargetPath, err)
		}
	}
	previous, _, err := latestHarnessArtifact(homePath, harness, policy.TargetPath)
	if err != nil {
		return PermissionRender{}, err
	}
	permissions := objectAt(settings, "permissions")
	permissions["allow"] = reconcileRules(stringSlice(permissions["allow"]), previous.ManagedEntries["permissions.allow"], policy.Permissions.Allow, false)
	permissions["deny"] = reconcileRules(stringSlice(permissions["deny"]), previous.ManagedEntries["permissions.deny"], policy.Permissions.Deny, false)
	permissions["ask"] = reconcileRules(stringSlice(permissions["ask"]), previous.ManagedEntries["permissions.ask"], policy.Permissions.Ask, false)
	permissions["defaultMode"] = policy.Permissions.DefaultMode
	settings["permissions"] = permissions
	autoMode := objectAt(settings, "autoMode")
	autoMode["allow"] = reconcileRules(stringSlice(autoMode["allow"]), previous.ManagedEntries["autoMode.allow"], policy.AutoMode.Allow, true)
	settings["autoMode"] = autoMode
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return PermissionRender{}, err
	}
	content = append(content, '\n')
	artifact := HarnessArtifact{
		SchemaVersion: HarnessArtifactSchemaVersion, Path: policy.TargetPath, Harness: harness,
		SourceAuthorityRef: policy.AuthorityRef, SourceRevision: sourceRevision,
		ContentSHA256: "sha256:" + digest(content),
		ManagedEntries: map[string][]string{
			"permissions.allow": append([]string(nil), policy.Permissions.Allow...),
			"permissions.deny":  append([]string(nil), policy.Permissions.Deny...),
			"permissions.ask":   append([]string(nil), policy.Permissions.Ask...),
			"autoMode.allow":    append([]string(nil), policy.AutoMode.Allow...),
		},
	}
	return PermissionRender{
		TargetPath: policy.TargetPath,
		Changed:    !bytes.Equal(before, content),
		Content:    content,
		Preimage:   append([]byte(nil), before...),
		PreExisted: beforeExisted,
		Artifact:   artifact,
		Details:    permissionDelta(previous, policy),
	}, nil
}

var writeHarnessArtifactManifestForApply = WriteHarnessArtifactManifest
var applyPermissionsBeforeTargetCommit = func() {}
var applyPermissionsAfterTargetWrite = func() error { return nil }
var permissionApplySyncBoundary = func(_ string, path string) error {
	return syncPermissionParentDirectory(path)
}

func PermissionApplyPendingPath(homePath, harness string) string {
	return filepath.Join(homePath, "transactions", "harness-permissions", strings.TrimSpace(harness)+".json")
}

func permissionApplyLockPath(homePath string) string {
	return filepath.Join(homePath, "locks", "harness-permissions.lock")
}

// ApplyPermissions reconciles only Compound-managed permission entries, writes
// the target through a recoverable transaction, and records the exact authority
// revision/content hash. The lock serializes Compound writers; the preimage CAS
// refuses a concurrent manual edit. An interrupted transaction stays visible to
// drift and is recovered before the next apply.
func ApplyPermissions(homePath string, approved PermissionRender, action envelope.ActionIntent, decision envelope.PolicyDecision) (PermissionRender, error) {
	if err := validatePermissionApplyApproval(approved, action, decision); err != nil {
		return PermissionRender{}, err
	}
	harnessName := approved.Artifact.Harness
	var render PermissionRender
	err := withPermissionApplyLock(homePath, func() error {
		if err := recoverPermissionApplyPending(homePath, harnessName); err != nil {
			return err
		}
		current, err := RenderPermissions(homePath, harnessName)
		if err != nil {
			return err
		}
		if !samePermissionRender(current, approved) {
			return fmt.Errorf("approved render no longer matches current authority, target path, preimage, or content")
		}
		render = approved
		beforeExisted := render.PreExisted
		if !render.Changed {
			previous, ok, err := latestHarnessArtifact(homePath, harnessName, render.TargetPath)
			if err != nil {
				return err
			}
			if ok && sameHarnessArtifact(previous, render.Artifact) {
				return nil
			}
			if err := writeHarnessArtifactManifestForApply(homePath, []HarnessArtifact{render.Artifact}); err != nil {
				return err
			}
			return permissionApplySyncBoundary("manifest_write", HarnessArtifactManifestPath(homePath))
		}
		recoveryName := permissionRecoveryName(render)
		pending := permissionApplyPending{
			SchemaVersion: permissionApplyPendingSchemaVersion,
			Harness:       harnessName, TargetPath: render.TargetPath, RecoveryName: recoveryName, BeforeExisted: beforeExisted,
			BeforeContent: append([]byte(nil), render.Preimage...), IntendedSHA256: render.Artifact.ContentSHA256,
			SourceAuthorityRef: render.Artifact.SourceAuthorityRef, SourceRevision: render.Artifact.SourceRevision,
		}
		if beforeExisted {
			pending.BeforeSHA256 = "sha256:" + digest(render.Preimage)
		}
		body, err := json.MarshalIndent(pending, "", "  ")
		if err != nil {
			return err
		}
		pendingPath := PermissionApplyPendingPath(homePath, harnessName)
		if err := writePermissionPrivateFileAtomic(pendingPath, append(body, '\n')); err != nil {
			return fmt.Errorf("write permission apply pending transaction: %w", err)
		}
		if err := permissionApplySyncBoundary("pending_write", pendingPath); err != nil {
			return fmt.Errorf("sync permission apply pending transaction: %w", err)
		}
		applyPermissionsBeforeTargetCommit()
		publication, err := beginPermissionTargetPublication(render, recoveryName)
		if err != nil {
			if removeErr := removePermissionPending(pendingPath); removeErr != nil {
				return errors.Join(err, removeErr)
			}
			return err
		}
		defer publication.Close()
		if err := permissionApplySyncBoundary("target_publish", render.TargetPath); err != nil {
			return rollbackPermissionPublication(publication, pendingPath, fmt.Errorf("sync permission target publication: %w", err))
		}
		if err := applyPermissionsAfterTargetWrite(); err != nil {
			return fmt.Errorf("permission apply interrupted after target write: %w", err)
		}
		if err := writeHarnessArtifactManifestForApply(homePath, []HarnessArtifact{render.Artifact}); err != nil {
			return rollbackPermissionPublication(publication, pendingPath, fmt.Errorf("write harness artifact manifest: %w", err))
		}
		if err := permissionApplySyncBoundary("manifest_write", HarnessArtifactManifestPath(homePath)); err != nil {
			return fmt.Errorf("sync harness artifact manifest: %w", err)
		}
		if err := publication.Commit(); err != nil {
			return fmt.Errorf("commit permission target publication: %w", err)
		}
		if err := removePermissionPending(pendingPath); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return PermissionRender{}, err
	}
	return render, nil
}

func withPermissionApplyLock(homePath string, fn func() error) error {
	path := permissionApplyLockPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}

func recoverPermissionApplyPending(homePath, harness string) error {
	path := PermissionApplyPendingPath(homePath, harness)
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pending permission apply transaction: %w", err)
	}
	var pending permissionApplyPending
	if err := json.Unmarshal(body, &pending); err != nil {
		return fmt.Errorf("parse pending permission apply transaction: %w", err)
	}
	if pending.SchemaVersion != permissionApplyPendingSchemaVersion || pending.Harness != harness || strings.TrimSpace(pending.TargetPath) == "" {
		return fmt.Errorf("pending permission apply transaction identity is invalid")
	}
	policyAuthority, sourceRevision, err := LoadPermissionAuthority(homePath, harness)
	if err != nil {
		return fmt.Errorf("validate pending permission apply authority: %w", err)
	}
	if pending.SourceRevision != sourceRevision {
		return fmt.Errorf("pending permission apply source revision %q no longer matches %q", pending.SourceRevision, sourceRevision)
	}
	if pending.SourceAuthorityRef != policyAuthority.AuthorityRef || pending.TargetPath != policyAuthority.TargetPath {
		return fmt.Errorf("pending permission apply authority or target path no longer matches current authority")
	}
	if pending.BeforeExisted && pending.BeforeSHA256 != "sha256:"+digest(pending.BeforeContent) {
		return fmt.Errorf("pending permission apply transaction pre-image digest is invalid")
	}
	current, readErr := os.ReadFile(pending.TargetPath)
	currentExisted := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read pending permission target: %w", readErr)
	}
	currentSHA := ""
	if currentExisted {
		currentSHA = "sha256:" + digest(current)
	}
	currentIsBefore := currentExisted == pending.BeforeExisted && (!currentExisted || currentSHA == pending.BeforeSHA256)
	currentIsIntended := currentExisted && currentSHA == pending.IntendedSHA256
	if !currentIsBefore && !currentIsIntended {
		return fmt.Errorf("pending permission apply target changed after interruption; refusing to overwrite manual state")
	}
	if currentIsBefore {
		if pending.RecoveryName != "" {
			_ = os.Remove(filepath.Join(filepath.Dir(pending.TargetPath), pending.RecoveryName))
		}
		return removePermissionPending(path)
	}
	latest, ok, err := latestHarnessArtifact(homePath, harness, pending.TargetPath)
	if err != nil {
		return err
	}
	if ok && latest.ContentSHA256 == pending.IntendedSHA256 && latest.SourceRevision == pending.SourceRevision {
		if pending.RecoveryName != "" {
			if err := os.Remove(filepath.Join(filepath.Dir(pending.TargetPath), pending.RecoveryName)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("complete pending permission target publication: %w", err)
			}
			if err := syncPermissionParentDirectory(pending.TargetPath); err != nil {
				return err
			}
		}
		return removePermissionPending(path)
	}
	if !pending.BeforeExisted || pending.RecoveryName == "" {
		return fmt.Errorf("pending permission apply requires manual recovery; atomic pre-image is unavailable")
	}
	publication, err := beginPermissionRecoveryPublication(pending, current)
	if err != nil {
		return fmt.Errorf("recover pending permission apply pre-image: %w", err)
	}
	defer publication.Close()
	if err := publication.Commit(); err != nil {
		return fmt.Errorf("commit recovered permission apply pre-image: %w", err)
	}
	return removePermissionPending(path)
}

func validatePermissionApplyApproval(approved PermissionRender, action envelope.ActionIntent, decision envelope.PolicyDecision) error {
	if err := action.Validate(); err != nil {
		return fmt.Errorf("permission apply action intent invalid: %w", err)
	}
	if err := decision.Validate(); err != nil {
		return fmt.Errorf("permission apply policy decision invalid: %w", err)
	}
	if strings.TrimSpace(approved.Artifact.Harness) == "" || approved.TargetPath == "" || approved.TargetPath != approved.Artifact.Path {
		return fmt.Errorf("permission apply approved render target identity is invalid")
	}
	if approved.Artifact.ContentSHA256 != "sha256:"+digest(approved.Content) {
		return fmt.Errorf("permission apply approved render content digest is invalid")
	}
	if action.Capability != "harness.permissions.apply" {
		return fmt.Errorf("permission apply capability must be harness.permissions.apply")
	}
	if action.Action != "reconcile_harness_permissions" {
		return fmt.Errorf("permission apply action must be reconcile_harness_permissions")
	}
	if action.Target.Kind != "harness_settings" || action.Target.ID != approved.Artifact.Harness || action.Target.Path != approved.TargetPath {
		return fmt.Errorf("permission apply action target does not bind approved render")
	}
	if action.MutationClass != "security" || action.Reversibility != "easy" || action.BlastRadius != "local" {
		return fmt.Errorf("permission apply requires security mutation_class with easy reversibility and local blast radius")
	}
	if !policy.HasOperatorProvenance(action.Actor) {
		return fmt.Errorf("permission apply requires explicit operator provenance")
	}
	if decision.ActionIntentID != action.ID || decision.IDs.WorkID != action.IDs.WorkID {
		return fmt.Errorf("permission apply policy decision does not bind action intent")
	}
	if !policy.Allowed(decision) || strings.TrimSpace(decision.StandingGrantID) == "" {
		return fmt.Errorf("permission apply requires an allowed standing-grant policy decision")
	}
	return nil
}

func samePermissionRender(a, b PermissionRender) bool {
	return a.TargetPath == b.TargetPath && a.Changed == b.Changed && a.PreExisted == b.PreExisted &&
		bytes.Equal(a.Content, b.Content) && bytes.Equal(a.Preimage, b.Preimage) &&
		reflect.DeepEqual(a.Artifact, b.Artifact) && reflect.DeepEqual(a.Details, b.Details)
}

type permissionTargetPublication struct {
	root        *os.Root
	publication *homepkg.RootedFileCAS
}

func (p *permissionTargetPublication) Commit() error   { return p.publication.Commit() }
func (p *permissionTargetPublication) Rollback() error { return p.publication.Rollback() }
func (p *permissionTargetPublication) Close() error    { return p.root.Close() }

func beginPermissionTargetPublication(render PermissionRender, recoveryName string) (*permissionTargetPublication, error) {
	parent := filepath.Dir(render.TargetPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(render.TargetPath)
	current, currentInfo, err := readPermissionRootedFile(root, name)
	if os.IsNotExist(err) && !render.PreExisted {
		current, currentInfo, err = nil, nil, nil
	}
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if (currentInfo != nil) != render.PreExisted || !bytes.Equal(current, render.Preimage) {
		_ = root.Close()
		return nil, fmt.Errorf("settings target %s preimage changed before permission publication", render.TargetPath)
	}
	replacementInfo, err := writePermissionRootedReplacement(root, recoveryName, render.Content, currentInfo)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	publication, err := homepkg.BeginRootedFileCAS(root, name, recoveryName, current, currentInfo, render.Content, replacementInfo)
	if err != nil {
		if publication == nil || publication.State() == homepkg.RootedFileCASNotPublished {
			_ = root.Remove(recoveryName)
		}
		_ = root.Close()
		return nil, err
	}
	return &permissionTargetPublication{root: root, publication: publication}, nil
}

func beginPermissionRecoveryPublication(pending permissionApplyPending, current []byte) (*permissionTargetPublication, error) {
	root, err := os.OpenRoot(filepath.Dir(pending.TargetPath))
	if err != nil {
		return nil, err
	}
	name := filepath.Base(pending.TargetPath)
	actual, currentInfo, err := readPermissionRootedFile(root, name)
	if err != nil || !bytes.Equal(actual, current) {
		_ = root.Close()
		return nil, errors.Join(fmt.Errorf("pending target changed during recovery"), err)
	}
	recoveryData, recoveryInfo, err := readPermissionRootedFile(root, pending.RecoveryName)
	if err != nil || !bytes.Equal(recoveryData, pending.BeforeContent) {
		_ = root.Close()
		return nil, errors.Join(fmt.Errorf("pending recovery pre-image changed"), err)
	}
	publication, err := homepkg.BeginRootedFileCAS(root, name, pending.RecoveryName, actual, currentInfo, recoveryData, recoveryInfo)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &permissionTargetPublication{root: root, publication: publication}, nil
}

func writePermissionRootedReplacement(root *os.Root, name string, body []byte, currentInfo os.FileInfo) (os.FileInfo, error) {
	mode := os.FileMode(0o644)
	if currentInfo != nil {
		mode = currentInfo.Mode().Perm()
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return nil, err
	}
	cleanup := func(cause error) (os.FileInfo, error) {
		_ = file.Close()
		_ = root.Remove(name)
		return nil, cause
	}
	if n, err := file.Write(body); err != nil {
		return cleanup(err)
	} else if n != len(body) {
		return cleanup(io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(name)
		return nil, err
	}
	data, info, err := readPermissionRootedFile(root, name)
	if err != nil || !bytes.Equal(data, body) {
		return cleanup(errors.Join(fmt.Errorf("permission replacement changed before publication"), err))
	}
	return info, nil
}

func readPermissionRootedFile(root *os.Root, name string) ([]byte, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("permission target must be a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(before, opened) || !os.SameFile(opened, after) || int64(len(data)) != after.Size() {
		return nil, nil, fmt.Errorf("permission target changed while read")
	}
	return data, after, nil
}

func permissionRecoveryName(render PermissionRender) string {
	return fmt.Sprintf(".%s.%d.%s.permission-swap", filepath.Base(render.TargetPath), os.Getpid(), strings.TrimPrefix(render.Artifact.ContentSHA256, "sha256:")[:12])
}

func rollbackPermissionPublication(publication *permissionTargetPublication, pendingPath string, cause error) error {
	if err := publication.Rollback(); err != nil {
		return fmt.Errorf("%v; manual/concurrent state preserved and pending recovery retained: %w", cause, err)
	}
	if err := removePermissionPending(pendingPath); err != nil {
		return fmt.Errorf("%v; settings pre-image restored; %w", cause, err)
	}
	return fmt.Errorf("%w; settings pre-image restored", cause)
}

func removePermissionPending(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove permission apply pending transaction: %w", err)
	}
	if err := permissionApplySyncBoundary("pending_remove", path); err != nil {
		return fmt.Errorf("sync permission apply pending removal: %w", err)
	}
	return nil
}

func syncPermissionParentDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}

func sameHarnessArtifact(a, b HarnessArtifact) bool {
	return a.Path == b.Path &&
		a.Harness == b.Harness &&
		a.SourceAuthorityRef == b.SourceAuthorityRef &&
		a.SourceRevision == b.SourceRevision &&
		a.ContentSHA256 == b.ContentSHA256 &&
		reflect.DeepEqual(a.ManagedEntries, b.ManagedEntries)
}

func PermissionDrift(homePath, harness string) (PermissionDriftReport, error) {
	render, err := RenderPermissions(homePath, harness)
	if err != nil {
		return PermissionDriftReport{}, err
	}
	report := PermissionDriftReport{Harness: harness, TargetPath: render.TargetPath}
	if _, err := os.Stat(PermissionApplyPendingPath(homePath, harness)); err == nil {
		report.Details = append(report.Details, "incomplete permission apply transaction requires recovery")
	} else if !os.IsNotExist(err) {
		return report, fmt.Errorf("inspect pending permission apply transaction: %w", err)
	}
	current, err := os.ReadFile(render.TargetPath)
	if err != nil {
		return report, err
	}
	var settings map[string]any
	if err := json.Unmarshal(current, &settings); err != nil {
		return report, err
	}
	for _, rule := range stringSlice(objectAt(settings, "permissions")["allow"]) {
		if rule == "mcp__*" {
			report.Details = append(report.Details, "unsupported permissions.allow rule mcp__*")
		}
	}
	if !jsonEquivalent(current, render.Content) {
		report.Details = append(report.Details, managedRuleDifferences(current, render.Content)...)
	}
	report.Details = uniqueSorted(report.Details)
	report.Drifted = len(report.Details) > 0
	return report, nil
}

func WriteHarnessArtifactManifest(homePath string, artifacts []HarnessArtifact) error {
	existing, err := loadHarnessArtifacts(homePath)
	if err != nil {
		return err
	}
	byKey := map[string]HarnessArtifact{}
	for _, artifact := range existing {
		byKey[artifact.Harness+"\x00"+artifact.Path] = artifact
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, artifact := range artifacts {
		artifact.SchemaVersion = HarnessArtifactSchemaVersion
		if artifact.GeneratedAt == "" {
			artifact.GeneratedAt = now
		}
		byKey[artifact.Harness+"\x00"+artifact.Path] = artifact
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, key := range keys {
		if err := encoder.Encode(byKey[key]); err != nil {
			return err
		}
	}
	path := HarnessArtifactManifestPath(homePath)
	return writePermissionFileAtomic(path, body.Bytes())
}

func restorePermissionPreimage(path string, body []byte, existed bool) error {
	if existed {
		return writePermissionFileAtomic(path, body)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writePermissionFileAtomic(path string, body []byte) error {
	return writePermissionFileAtomicMode(path, body, 0o644)
}

func writePermissionPrivateFileAtomic(path string, body []byte) error {
	return writePermissionFileAtomicMode(path, body, 0o600)
}

func writePermissionFileAtomicMode(path string, body []byte, defaultMode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := defaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writePermissionFileAtomicCAS(path string, body, expected []byte, expectedExisted bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	current, readErr := os.ReadFile(path)
	currentExisted := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if currentExisted != expectedExisted || (currentExisted && !bytes.Equal(current, expected)) {
		return fmt.Errorf("settings target %s changed during permission apply; refusing to overwrite concurrent state", path)
	}
	return os.Rename(tempPath, path)
}

func loadHarnessArtifacts(homePath string) ([]HarnessArtifact, error) {
	path := HarnessArtifactManifestPath(homePath)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []HarnessArtifact
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var artifact HarnessArtifact
		if err := json.Unmarshal(scanner.Bytes(), &artifact); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		out = append(out, artifact)
	}
	return out, scanner.Err()
}

func latestHarnessArtifact(homePath, harness, targetPath string) (HarnessArtifact, bool, error) {
	artifacts, err := loadHarnessArtifacts(homePath)
	if err != nil {
		return HarnessArtifact{}, false, err
	}
	for i := len(artifacts) - 1; i >= 0; i-- {
		if artifacts[i].Harness == harness && artifacts[i].Path == targetPath {
			return artifacts[i], true, nil
		}
	}
	return HarnessArtifact{ManagedEntries: map[string][]string{}}, false, nil
}

func reconcileRules(current, previousManaged, desired []string, defaultsFirst bool) []string {
	managed := map[string]bool{}
	for _, rule := range previousManaged {
		managed[rule] = true
	}
	out, seen := []string{}, map[string]bool{}
	for _, rule := range current {
		if managed[rule] || seen[rule] {
			continue
		}
		seen[rule], out = true, append(out, rule)
	}
	if defaultsFirst {
		filtered := out[:0]
		for _, rule := range out {
			if rule != "$defaults" {
				filtered = append(filtered, rule)
			}
		}
		out = filtered
		if len(desired) > 0 && desired[0] == "$defaults" {
			out = append([]string{"$defaults"}, out...)
			seen["$defaults"] = true
			desired = desired[1:]
		}
	}
	for _, rule := range desired {
		if !seen[rule] {
			seen[rule], out = true, append(out, rule)
		}
	}
	return out
}

func objectAt(root map[string]any, key string) map[string]any {
	if value, ok := root[key].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func stringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if rule, ok := value.(string); ok {
				out = append(out, rule)
			}
		}
		return out
	default:
		return nil
	}
}

func sortedRules(rules []string) []string {
	out := make([]string, len(rules))
	for i := range rules {
		out[i] = strings.TrimSpace(rules[i])
	}
	sort.Strings(out)
	return out
}

func jsonEquivalent(a, b []byte) bool {
	if len(bytes.TrimSpace(a)) == 0 {
		a = []byte("{}")
	}
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}

func managedRuleDifferences(current, intended []byte) []string {
	var got, want map[string]any
	if json.Unmarshal(current, &got) != nil || json.Unmarshal(intended, &want) != nil {
		return []string{"settings JSON differs"}
	}
	var out []string
	for _, path := range []string{"permissions.allow", "permissions.deny", "permissions.ask", "autoMode.allow"} {
		parts := strings.Split(path, ".")
		gotRules := stringSlice(objectAt(got, parts[0])[parts[1]])
		wantRules := stringSlice(objectAt(want, parts[0])[parts[1]])
		gotSet, wantSet := map[string]bool{}, map[string]bool{}
		for _, rule := range gotRules {
			gotSet[rule] = true
		}
		for _, rule := range wantRules {
			wantSet[rule] = true
		}
		for _, rule := range wantRules {
			if !gotSet[rule] {
				out = append(out, fmt.Sprintf("%s missing %q", path, rule))
			}
		}
		for _, rule := range gotRules {
			if !wantSet[rule] {
				out = append(out, fmt.Sprintf("%s extra %q", path, rule))
			}
		}
		if reflect.DeepEqual(gotSet, wantSet) && !reflect.DeepEqual(gotRules, wantRules) {
			if path == "autoMode.allow" {
				out = append(out, "autoMode.allow order differs; $defaults must be first")
			} else {
				out = append(out, path+" managed rule order differs")
			}
		}
	}
	gotMode, wantMode := objectAt(got, "permissions")["defaultMode"], objectAt(want, "permissions")["defaultMode"]
	if gotMode != wantMode {
		out = append(out, fmt.Sprintf("permissions.defaultMode is %q, want %q", gotMode, wantMode))
	}
	return out
}

func permissionDelta(previous HarnessArtifact, policy PermissionAuthority) []string {
	var out []string
	desired := map[string][]string{"permissions.allow": policy.Permissions.Allow, "permissions.deny": policy.Permissions.Deny, "permissions.ask": policy.Permissions.Ask, "autoMode.allow": policy.AutoMode.Allow}
	for path, oldRules := range previous.ManagedEntries {
		keep := map[string]bool{}
		for _, rule := range desired[path] {
			keep[rule] = true
		}
		for _, rule := range oldRules {
			if !keep[rule] {
				out = append(out, fmt.Sprintf("%s remove managed %q", path, rule))
			}
		}
	}
	return uniqueSorted(out)
}

func uniqueSorted(values []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value], out = true, append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
