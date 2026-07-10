package main

import (
	"os"

	"compound/internal/contract"
	"compound/internal/harness"
	"compound/internal/hookplan"
)

type hooksAuthorityReport struct {
	Privacy                     string                                   `json:"privacy"`
	AuthorityRef                string                                   `json:"authority_ref"`
	AuthorityPath               string                                   `json:"authority_path"`
	Installed                   bool                                     `json:"installed"`
	HookCount                   int                                      `json:"hook_count,omitempty"`
	Harnesses                   []string                                 `json:"harnesses,omitempty"`
	PreToolUseReadiness         []contract.HookGateReadiness             `json:"pre_tool_use_readiness,omitempty"`
	HookReadiness               []contract.InstalledHookRuntimeReadiness `json:"hook_readiness,omitempty"`
	HookPlan                    *hookplan.HookPlan                       `json:"hook_plan,omitempty"`
	InstallationObservation     *hookplan.InstallationSnapshot           `json:"installation_observation,omitempty"`
	Reconciliation              *hookplan.Reconciliation                 `json:"reconciliation,omitempty"`
	SourceRoleCounts            map[string]int                           `json:"source_role_counts,omitempty"`
	RetiredSourceAuthorityCount int                                      `json:"retired_source_authority_count"`
	RetiredSourceAuthorityRefs  []string                                 `json:"retired_source_authority_refs,omitempty"`
	SourceAuthority             []contract.InstalledHooksSourceAuthority `json:"source_authority,omitempty"`
	SourceRecordPath            string                                   `json:"source_record_path"`
}

func populateLiveHookReconciliation(report *hooksAuthorityReport, compoundHome string) error {
	harnessHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	state, err := contract.ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		return err
	}
	report.HookPlan = &state.Plan
	report.InstallationObservation = &state.Observation
	report.Reconciliation = &state.Reconciliation
	return nil
}
