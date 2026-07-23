package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/hookplan"
	"compound/internal/work"
)

func init() {
	work.DelegateHarnessReadiness = evaluateDelegateHarnessReadiness
}

// evaluateDelegateHarnessReadiness is the read-only activation check for the
// governed delegated-session path. Tuple resolution and capacity answer where
// work could run; this check proves that Compound's current permission, hook,
// and instruction authorities are actually ready for that harness.
func evaluateDelegateHarnessReadiness(req work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
	harnessName := normalizeDelegateReadinessHarness(req.Harness)
	if harnessName == "" {
		return work.DelegateHarnessReadinessResult{}, fmt.Errorf("harness is required")
	}

	var details []string
	var evidence []work.DelegateHarnessReadinessEvidence
	if req.CommandOverridden {
		details = append(details, "executable: raw command override is not attested by the resolved lane tuple")
	} else if strings.TrimSpace(req.Command) == "" {
		details = append(details, "executable: resolved command is empty")
	} else {
		evidence = append(evidence, work.DelegateHarnessReadinessEvidence{
			Kind: "executable",
			Ref:  "lane-tuple://" + firstDelegateReadinessValue(req.RequestedModelID, req.LaneID, harnessName),
		})
	}

	_, permissionRevision, err := harness.LoadPermissionAuthority(req.HomePath, harnessName)
	if err != nil {
		details = append(details, "permission_authority: "+err.Error())
	} else {
		evidence = append(evidence, work.DelegateHarnessReadinessEvidence{
			Kind:     "permission_authority",
			Ref:      harness.PermissionAuthorityRef(harnessName),
			Revision: permissionRevision,
		})
		drift, err := harness.PermissionDrift(req.HomePath, harnessName)
		switch {
		case err != nil:
			details = append(details, "permission_posture: "+err.Error())
		case drift.Drifted:
			detail := strings.Join(drift.Details, ", ")
			if detail == "" {
				detail = "managed settings differ from authority"
			}
			details = append(details, "permission_posture: "+detail)
		default:
			livePosture, readErr := os.ReadFile(drift.TargetPath)
			if readErr != nil {
				details = append(details, "permission_posture: read applied target: "+readErr.Error())
			} else {
				evidence = append(evidence, work.DelegateHarnessReadinessEvidence{
					Kind:     "permission_posture",
					Ref:      "harness-posture://" + harnessName,
					Revision: delegateReadinessBytesRevision(livePosture),
				})
			}
		}
	}

	registry := harness.DefaultRegistry()
	manifest, err := contract.LoadInstalledHooksManifest(req.HomePath)
	if err != nil {
		details = append(details, "blocking_hook: "+err.Error())
	} else {
		status := "not_installed"
		for _, row := range contract.InstalledHookGateReadiness(manifest, nativePreToolUseBlockMap(registry)) {
			if row.Harness == harnessName && row.Hook == "PreToolUse" {
				status = row.Status
				break
			}
		}
		if status != "native_blocking" {
			details = append(details, "blocking_hook: status="+status)
		}
	}

	harnessHome := strings.TrimSpace(req.HarnessHome)
	if harnessHome == "" {
		harnessHome, err = os.UserHomeDir()
		if err != nil {
			details = append(details, "blocking_hook_live: resolve harness home: "+err.Error())
		}
	}
	if harnessHome != "" {
		reconciled, reconcileErr := contract.ReconcileInstalledHooks(req.HomePath, harnessHome, registry)
		if reconcileErr != nil {
			details = append(details, "blocking_hook_live: "+reconcileErr.Error())
		} else {
			delta := delegatePreToolUseReconciliation(reconciled.Reconciliation, harnessName)
			switch {
			case delta == nil:
				details = append(details, "blocking_hook_live: no observed PreToolUse reconciliation")
			case delta.State != hookplan.StateReady:
				details = append(details, "blocking_hook_live: status="+delta.State)
			default:
				evidence = append(evidence,
					work.DelegateHarnessReadinessEvidence{
						Kind:     "blocking_hook_plan",
						Ref:      "hook://" + harnessName + "/PreToolUse",
						Revision: reconciled.Reconciliation.PlanRevision,
					},
					work.DelegateHarnessReadinessEvidence{
						Kind:     "blocking_hook_installation",
						Ref:      reconciled.Reconciliation.ObservationRef,
						Revision: reconciled.Reconciliation.ObservationRevision,
					},
				)
			}
		}
	}

	packDrift, err := harness.InstructionPackDrift(req.HomePath, harnessName, req.Now)
	switch {
	case err != nil:
		details = append(details, "instruction_pack: "+err.Error())
	case packDrift.Drifted:
		detail := strings.Join(packDrift.Details, ", ")
		if detail == "" {
			detail = "latest pack differs from current authorities"
		}
		details = append(details, "instruction_pack: "+detail)
	default:
		pack, err := loadDelegateInstructionPack(req.HomePath, harnessName)
		switch {
		case err != nil:
			details = append(details, "instruction_pack: "+err.Error())
		case pack.SchemaVersion != harness.InstructionPackSchemaVersion:
			details = append(details, "instruction_pack: schema_version is not current")
		case normalizeDelegateReadinessHarness(pack.Harness) != harnessName:
			details = append(details, "instruction_pack: harness identity does not match")
		default:
			if pack.PolicyCore.Absent {
				details = append(details, "instruction_pack: policy_core is absent")
			}
			if pack.Posture.Absent {
				details = append(details, "instruction_pack: posture is absent")
			}
			if pack.Roster.InstalledHooks.Absent {
				details = append(details, "instruction_pack: installed_hooks roster is absent")
			}
			if pack.Roster.EffectiveContract.Absent {
				details = append(details, "instruction_pack: effective_contract roster is absent")
			}
			if strings.TrimSpace(pack.Roster.RegistryRevision) == "" {
				details = append(details, "instruction_pack: registry_revision is absent")
			}
			if !pack.PolicyCore.Absent && !pack.Posture.Absent &&
				!pack.Roster.InstalledHooks.Absent && !pack.Roster.EffectiveContract.Absent {
				contextEvidence, contextDetails := validateDelegateInstructionContext(req, pack)
				evidence = append(evidence, contextEvidence...)
				details = append(details, contextDetails...)
			}
			if len(details) == 0 {
				evidence = append(evidence, work.DelegateHarnessReadinessEvidence{
					Kind:     "instruction_pack",
					Ref:      "instruction-pack://" + harnessName,
					Revision: pack.ContentSHA256,
				})
			}
		}
	}

	return work.DelegateHarnessReadinessResult{
		Ready:    len(details) == 0,
		Details:  details,
		Evidence: evidence,
	}, nil
}

func delegatePreToolUseReconciliation(reconciliation hookplan.Reconciliation, harnessName string) *hookplan.Delta {
	for i := range reconciliation.Deltas {
		delta := &reconciliation.Deltas[i]
		if normalizeDelegateReadinessHarness(delta.Harness) == harnessName && delta.Event == "PreToolUse" {
			return delta
		}
	}
	return nil
}

func validateDelegateInstructionContext(req work.DelegateHarnessReadinessRequest, pack harness.InstructionPack) ([]work.DelegateHarnessReadinessEvidence, []string) {
	var evidence []work.DelegateHarnessReadinessEvidence
	var details []string
	contextPackID := strings.TrimSpace(req.ContextPackID)
	if contextPackID == "" {
		return evidence, []string{"instruction_pack: work has no context_pack_id"}
	}
	if !strings.HasPrefix(contextPackID, "ctx_") || strings.ContainsAny(contextPackID, `/\`) {
		return evidence, []string{"instruction_pack: work context_pack_id is invalid"}
	}

	contextPath := filepath.Join(req.HomePath, "context", "packs", contextPackID+".json")
	var contextPack envelope.ContextPack
	if err := readDelegateReadinessJSON(contextPath, &contextPack); err != nil {
		return evidence, []string{"instruction_pack: context pack: " + err.Error()}
	}
	switch {
	case contextPack.ID != contextPackID:
		details = append(details, "instruction_pack: context pack identity does not match work")
	case contextPack.Validate() != nil:
		details = append(details, "instruction_pack: context pack is invalid")
	}
	if workID := strings.TrimSpace(contextPack.IDs.WorkID); workID != "" && workID != strings.TrimSpace(req.WorkID) {
		details = append(details, "instruction_pack: context pack work identity does not match")
	}

	const latestContractRef = "contracts/effective/latest.json"
	if filepath.ToSlash(filepath.Clean(pack.Roster.EffectiveContract.Ref)) != latestContractRef {
		details = append(details, "instruction_pack: effective_contract ref is not the owned latest projection")
		return evidence, details
	}
	var effective envelope.EffectiveContract
	if err := readDelegateReadinessJSON(filepath.Join(req.HomePath, filepath.FromSlash(latestContractRef)), &effective); err != nil {
		details = append(details, "instruction_pack: effective contract: "+err.Error())
		return evidence, details
	}
	if err := effective.Validate(); err != nil {
		details = append(details, "instruction_pack: effective contract is invalid")
	}
	if normalizeDelegateReadinessHarness(effective.Harness) != normalizeDelegateReadinessHarness(req.Harness) {
		details = append(details, "instruction_pack: effective contract harness identity does not match")
	}
	if strings.TrimSpace(contextPack.IDs.ContractID) == "" || contextPack.IDs.ContractID != effective.ID {
		details = append(details, "instruction_pack: work context pack does not bind the effective contract")
	}
	if workID := strings.TrimSpace(effective.IDs.WorkID); workID != "" && workID != strings.TrimSpace(req.WorkID) {
		details = append(details, "instruction_pack: effective contract work identity does not match")
	}
	if !delegateReadinessSurfaceMatches(contextPack.Scope.Surface, req.Workspace) ||
		!delegateReadinessSurfaceMatches(effective.Scope.Surface, req.Workspace) {
		details = append(details, "instruction_pack: contract/context workspace scope does not match delegated workspace")
	}
	if len(details) == 0 {
		evidence = append(evidence,
			work.DelegateHarnessReadinessEvidence{
				Kind: "context_pack",
				Ref:  "context-pack://" + contextPack.ID,
			},
			work.DelegateHarnessReadinessEvidence{
				Kind:     "effective_contract",
				Ref:      "contract://" + effective.ID,
				Revision: pack.Roster.EffectiveContract.Revision,
			},
		)
	}
	return evidence, details
}

func readDelegateReadinessJSON(path string, out any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func delegateReadinessSurfaceMatches(surface, workspace string) bool {
	surface = strings.TrimSpace(surface)
	workspace = strings.TrimSpace(workspace)
	if surface == "" || workspace == "" {
		return false
	}
	surfaceAbs, surfaceErr := filepath.Abs(surface)
	workspaceAbs, workspaceErr := filepath.Abs(workspace)
	if surfaceErr != nil || workspaceErr != nil {
		return false
	}
	return filepath.Clean(surfaceAbs) == filepath.Clean(workspaceAbs)
}

func firstDelegateReadinessValue(values ...string) string {
	for _, value := range values {
		if normalized := strings.TrimSpace(value); normalized != "" {
			return normalized
		}
	}
	return "unknown"
}

func delegateReadinessBytesRevision(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", sum)
}

func loadDelegateInstructionPack(homePath, harnessName string) (harness.InstructionPack, error) {
	body, err := os.ReadFile(harness.InstructionPackPath(homePath, harnessName))
	if err != nil {
		return harness.InstructionPack{}, err
	}
	var pack harness.InstructionPack
	if err := json.Unmarshal(body, &pack); err != nil {
		return harness.InstructionPack{}, fmt.Errorf("decode latest pack: %w", err)
	}
	return pack, nil
}

func normalizeDelegateReadinessHarness(value string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "-", "_")
	if normalized == "claude" {
		return "claude_code"
	}
	return normalized
}
