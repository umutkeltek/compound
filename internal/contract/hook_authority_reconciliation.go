package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/harness"
	"compound/internal/hookplan"
)

const liveHookInstallationRef = "harness-installation://live"

// InstalledHooksReconciliation keeps desired authority, observed installation,
// and their adjudication separate on the wire.
type InstalledHooksReconciliation struct {
	Plan           hookplan.HookPlan             `json:"hook_plan"`
	Observation    hookplan.InstallationSnapshot `json:"installation_observation"`
	Reconciliation hookplan.Reconciliation       `json:"reconciliation"`
}

// ReconcileInstalledHooks reads Compound's desired installed-hooks authority
// and read-only harness config evidence rooted at harnessHome. It never writes
// either source and never promotes observed registrations into HookPlan.
func ReconcileInstalledHooks(compoundHome, harnessHome string, registry harness.Registry) (InstalledHooksReconciliation, error) {
	manifest, err := LoadInstalledHooksManifest(compoundHome)
	if err != nil {
		if !os.IsNotExist(err) {
			return InstalledHooksReconciliation{}, err
		}
		manifest = InstalledHooksManifest{SchemaVersion: installedHooksSchemaVersion}
	}
	plan, err := hookPlanFromInstalledManifest(manifest, harnessHome)
	if err != nil {
		return InstalledHooksReconciliation{}, err
	}
	observation, err := observeHarnessHookInstallation(harnessHome)
	if err != nil {
		return InstalledHooksReconciliation{}, err
	}
	reconciliation, err := hookplan.Reconcile(plan, observation, registry)
	if err != nil {
		return InstalledHooksReconciliation{}, err
	}
	return InstalledHooksReconciliation{
		Plan:           plan,
		Observation:    observation,
		Reconciliation: reconciliation,
	}, nil
}

func hookPlanFromInstalledManifest(manifest InstalledHooksManifest, harnessHome string) (hookplan.HookPlan, error) {
	plan := hookplan.HookPlan{
		SchemaVersion: hookplan.HookPlanSchemaVersion,
		ID:            "hook-plan",
		AuthorityRef:  InstalledHooksAuthorityRef,
	}
	for _, hook := range manifest.Hooks {
		if !desiredHookStatus(hook.Status) {
			continue
		}
		event := strings.TrimSpace(hook.Event)
		if event == "" {
			event = strings.TrimSpace(hook.Name)
		}
		binding := hookplan.Binding{
			Harness:            strings.TrimSpace(hook.Harness),
			Event:              event,
			Matcher:            strings.TrimSpace(hook.Matcher),
			HandlerRef:         canonicalHookHandlerRef(hook.Ref, harnessHome),
			InvocationRevision: strings.TrimSpace(hook.InvocationRevision),
			HandlerRevision:    strings.TrimSpace(hook.Revision),
			Activation:         hookActivation(hook.Status),
			Blocking:           event == "PreToolUse",
		}
		binding.ID = desiredHookBindingID(binding)
		plan.Bindings = append(plan.Bindings, binding)
	}
	sort.SliceStable(plan.Bindings, func(i, j int) bool {
		return desiredHookBindingSortKey(plan.Bindings[i]) < desiredHookBindingSortKey(plan.Bindings[j])
	})
	encoded, err := json.Marshal(plan.Bindings)
	if err != nil {
		return hookplan.HookPlan{}, fmt.Errorf("marshal desired hook plan: %w", err)
	}
	plan.Revision = "sha256:" + hashBytes(encoded)
	return plan, nil
}

func desiredHookStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "", "current", "active", "pending", "pending_activation", "activation_required":
		return true
	default:
		return false
	}
}

func hookActivation(status string) string {
	switch strings.TrimSpace(status) {
	case "pending", "pending_activation", "activation_required":
		return hookplan.ActivationOperator
	default:
		return hookplan.ActivationManaged
	}
}

func desiredHookBindingID(binding hookplan.Binding) string {
	id := "hook://" + binding.Harness + "/" + binding.Event
	if binding.Matcher != "" {
		id += "?matcher=" + hashBytes([]byte(binding.Matcher))
	}
	return id
}

func desiredHookBindingSortKey(binding hookplan.Binding) string {
	return strings.Join([]string{binding.Harness, binding.Event, binding.Matcher, binding.ID, binding.HandlerRef}, "\x00")
}

type observedHarnessHooksFile struct {
	Hooks map[string][]observedHarnessHookGroup `json:"hooks"`
}

type observedHarnessHookGroup struct {
	Matcher  string                       `json:"matcher"`
	Disabled bool                         `json:"disabled"`
	Hooks    []observedHarnessHookCommand `json:"hooks"`
}

type observedHarnessHookCommand struct {
	Type     string `json:"type"`
	Command  string `json:"command"`
	Disabled bool   `json:"disabled"`
}

type hookInstallationSource struct {
	Harness string
	Ref     string
	Path    string
}

func observeHarnessHookInstallation(harnessHome string) (hookplan.InstallationSnapshot, error) {
	snapshot := hookplan.InstallationSnapshot{
		SchemaVersion: hookplan.InstallationSnapshotSchemaVersion,
		Ref:           liveHookInstallationRef,
	}
	sources := []hookInstallationSource{
		{Harness: "claude_code", Ref: "harness://claude_code/settings", Path: filepath.Join(harnessHome, ".claude", "settings.json")},
		{Harness: "codex", Ref: "harness://codex/hooks", Path: filepath.Join(harnessHome, ".codex", "hooks.json")},
	}
	var sourceRevisions []string
	seen := map[string]bool{}
	for _, source := range sources {
		data, err := os.ReadFile(source.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return hookplan.InstallationSnapshot{}, fmt.Errorf("read %s hook installation: %w", source.Harness, err)
		}
		sourceRevisions = append(sourceRevisions, source.Ref+"@sha256:"+hashBytes(data))
		bindings, err := parseObservedHarnessHooks(data, source, harnessHome)
		if err != nil {
			return hookplan.InstallationSnapshot{}, err
		}
		for _, binding := range bindings {
			key := strings.Join([]string{binding.Harness, binding.Event, binding.Matcher, binding.HandlerRef, binding.InvocationRevision, binding.SourceRef}, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			snapshot.Bindings = append(snapshot.Bindings, binding)
		}
	}
	sort.SliceStable(snapshot.Bindings, func(i, j int) bool {
		return observedHookBindingSortKey(snapshot.Bindings[i]) < observedHookBindingSortKey(snapshot.Bindings[j])
	})
	revisionMaterial, err := json.Marshal(struct {
		Sources  []string                   `json:"sources"`
		Bindings []hookplan.ObservedBinding `json:"bindings"`
	}{Sources: sourceRevisions, Bindings: snapshot.Bindings})
	if err != nil {
		return hookplan.InstallationSnapshot{}, fmt.Errorf("marshal observed hook installation: %w", err)
	}
	snapshot.Revision = "sha256:" + hashBytes(revisionMaterial)
	return snapshot, nil
}

func parseObservedHarnessHooks(data []byte, source hookInstallationSource, harnessHome string) ([]hookplan.ObservedBinding, error) {
	var file observedHarnessHooksFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s hook installation: %w", source.Harness, err)
	}
	events := make([]string, 0, len(file.Hooks))
	for event := range file.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	var bindings []hookplan.ObservedBinding
	for _, event := range events {
		for _, group := range file.Hooks[event] {
			for _, command := range group.Hooks {
				if command.Type != "" && command.Type != "command" {
					continue
				}
				handlerRef := canonicalHookHandlerRef(command.Command, harnessHome)
				if handlerRef == "" {
					continue
				}
				bindings = append(bindings, hookplan.ObservedBinding{
					Harness:            source.Harness,
					Event:              event,
					Matcher:            strings.TrimSpace(group.Matcher),
					HandlerRef:         handlerRef,
					InvocationRevision: canonicalHookInvocationRevision(command.Command, harnessHome),
					Active:             !group.Disabled && !command.Disabled,
					SourceRef:          source.Ref,
				})
			}
		}
	}
	return bindings, nil
}

func observedHookBindingSortKey(binding hookplan.ObservedBinding) string {
	return strings.Join([]string{binding.Harness, binding.Event, binding.Matcher, binding.BindingID, binding.HandlerRef, binding.InvocationRevision, binding.SourceRef}, "\x00")
}

func canonicalHookHandlerRef(value, harnessHome string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	normalized := strings.ReplaceAll(value, "${HOME}", "$HOME")
	normalizationHome := strings.TrimSpace(harnessHome)
	if normalizationHome == "" {
		normalizationHome, _ = os.UserHomeDir()
	}
	if normalizationHome != "" {
		normalized = strings.ReplaceAll(normalized, filepath.Clean(normalizationHome), "$HOME")
	}
	if strings.HasPrefix(normalized, "~/") {
		normalized = "$HOME/" + strings.TrimPrefix(normalized, "~/")
	}
	fields := canonicalHookCommandFields(normalized)
	for _, field := range fields {
		if strings.HasPrefix(field, "handler:") {
			return field
		}
	}
	for _, field := range fields {
		if field != "compound" && !strings.HasSuffix(field, "/.local/bin/compound") {
			continue
		}
		return field
	}
	for i := len(fields) - 1; i >= 0; i-- {
		field := fields[i]
		for _, marker := range []string{"$HOME/.claude/hooks/", "$HOME/.codex/hooks/"} {
			if index := strings.Index(field, marker); index >= 0 {
				return strings.TrimRight(field[index:], `"'`)
			}
		}
		if strings.HasPrefix(field, "hook://") || strings.HasPrefix(field, "handler:") {
			return field
		}
	}
	return "handler:" + canonicalHookInvocationRevision(normalized, "")
}

func canonicalHookInvocationRevision(value, harnessHome string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	normalized := strings.ReplaceAll(value, "${HOME}", "$HOME")
	normalizationHome := strings.TrimSpace(harnessHome)
	if normalizationHome == "" {
		normalizationHome, _ = os.UserHomeDir()
	}
	if normalizationHome != "" {
		normalized = strings.ReplaceAll(normalized, filepath.Clean(normalizationHome), "$HOME")
	}
	if strings.HasPrefix(normalized, "~/") {
		normalized = "$HOME/" + strings.TrimPrefix(normalized, "~/")
	}
	return "sha256:" + hashBytes([]byte(strings.Join(canonicalHookCommandFields(normalized), " ")))
}

func canonicalHookCommandFields(command string) []string {
	fields := strings.Fields(command)
	for i := range fields {
		fields[i] = strings.Trim(fields[i], "\"'`")
	}
	return fields
}
