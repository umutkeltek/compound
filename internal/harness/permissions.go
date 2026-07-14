package harness

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

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
	Artifact   HarnessArtifact `json:"artifact"`
	Details    []string        `json:"details,omitempty"`
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
	return PermissionRender{TargetPath: policy.TargetPath, Changed: !jsonEquivalent(before, content), Content: content, Artifact: artifact, Details: permissionDelta(previous, policy)}, nil
}

var writeHarnessArtifactManifestForApply = WriteHarnessArtifactManifest

// ApplyPermissions reconciles only Compound-managed permission entries, writes
// the target atomically, and records the exact authority revision/content hash.
// If provenance persistence fails after the target write, the target is restored
// to its pre-image so a clean drift report can never outpace its artifact record.
func ApplyPermissions(homePath, harness string) (PermissionRender, error) {
	render, err := RenderPermissions(homePath, harness)
	if err != nil {
		return PermissionRender{}, err
	}
	before, readErr := os.ReadFile(render.TargetPath)
	beforeExisted := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return PermissionRender{}, fmt.Errorf("read settings pre-image %s: %w", render.TargetPath, readErr)
	}
	if render.Changed {
		if err := writePermissionFileAtomic(render.TargetPath, render.Content); err != nil {
			return PermissionRender{}, fmt.Errorf("write settings target %s: %w", render.TargetPath, err)
		}
	}
	if err := writeHarnessArtifactManifestForApply(homePath, []HarnessArtifact{render.Artifact}); err != nil {
		restoreErr := restorePermissionPreimage(render.TargetPath, before, beforeExisted)
		if restoreErr != nil {
			return PermissionRender{}, fmt.Errorf("write harness artifact manifest: %v; restore settings pre-image: %w", err, restoreErr)
		}
		return PermissionRender{}, fmt.Errorf("write harness artifact manifest: %w; settings pre-image restored", err)
	}
	return render, nil
}

func PermissionDrift(homePath, harness string) (PermissionDriftReport, error) {
	render, err := RenderPermissions(homePath, harness)
	if err != nil {
		return PermissionDriftReport{}, err
	}
	report := PermissionDriftReport{Harness: harness, TargetPath: render.TargetPath}
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
