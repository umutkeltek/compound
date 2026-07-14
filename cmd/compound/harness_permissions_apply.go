package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/work"
)

type harnessPermissionApplyReport struct {
	SchemaVersion    string                  `json:"schema_version"`
	Mode             string                  `json:"mode"`
	Harness          string                  `json:"harness"`
	TargetPath       string                  `json:"target_path"`
	Changed          bool                    `json:"changed"`
	WorkID           string                  `json:"work_id"`
	ActionIntentID   string                  `json:"action_intent_id"`
	PolicyDecisionID string                  `json:"policy_decision_id"`
	PolicyOutcome    string                  `json:"policy_outcome"`
	Artifact         harness.HarnessArtifact `json:"artifact"`
	Details          []string                `json:"details,omitempty"`
}

func runHarnessGenerateApply(args []string, stdout, stderr io.Writer, now time.Time) int {
	harnessName := strings.TrimSpace(flagValue(args, "--harness"))
	workID := strings.TrimSpace(flagValue(args, "--work"))
	actor := strings.TrimSpace(flagValue(args, "--actor"))
	if harnessName == "" || workID == "" {
		fmt.Fprintln(stderr, "usage: compound harness generate --harness <name> --apply --work <work_id> [--actor A] [--json]")
		return 2
	}
	if actor == "" {
		actor = "operator"
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(stderr, "error:", err, "(set COMPOUND_HOME)")
		return 1
	}
	render, err := harness.RenderPermissions(homePath, harnessName)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if _, err := store.Get(workID); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	createdAt := now.UTC().Format(time.RFC3339Nano)
	action := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            actIDFor("act_", workID, "harness.permissions.apply", harnessName, render.Artifact.SourceRevision, createdAt),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Owner: "compound", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{render.Artifact.SourceAuthorityRef, render.Artifact.SourceRevision},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         actor,
		Capability:    "harness.permissions.apply",
		Action:        "reconcile_harness_permissions",
		Target:        envelope.ActionTarget{Kind: "harness_settings", ID: harnessName, Path: render.TargetPath},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("reconcile Compound-managed permissions for %s at %s", harnessName, render.TargetPath),
		RollbackPlan:  "restore the settings pre-image; ApplyPermissions automatically restores it if artifact provenance cannot be persisted",
	}
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	decision := policy.Evaluate(action, cfg, now)
	if !policy.Allowed(decision) {
		fmt.Fprintf(stderr, "refused: policy %s (%s)\n", decision.Outcome, strings.Join(decision.Reasons, "; "))
		return 1
	}
	if _, err := store.RecordAction(workID, action, decision, now); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	applied, err := harness.ApplyPermissions(homePath, harnessName)
	if err != nil {
		fmt.Fprintln(stderr, "error: governed action recorded but permission apply failed:", err)
		return 1
	}
	report := harnessPermissionApplyReport{
		SchemaVersion:    "compound.harness_permission_apply.v0.1",
		Mode:             "apply",
		Harness:          harnessName,
		TargetPath:       applied.TargetPath,
		Changed:          applied.Changed,
		WorkID:           workID,
		ActionIntentID:   action.ID,
		PolicyDecisionID: decision.ID,
		PolicyOutcome:    decision.Outcome,
		Artifact:         applied.Artifact,
		Details:          applied.Details,
	}
	if hasFlag(args, "--json") {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "# harness generate (applied)")
	fmt.Fprintf(stdout, "- harness: %s\n", report.Harness)
	fmt.Fprintf(stdout, "- target: %s\n", report.TargetPath)
	fmt.Fprintf(stdout, "- changed: %t\n", report.Changed)
	fmt.Fprintf(stdout, "- work: %s\n", report.WorkID)
	fmt.Fprintf(stdout, "- action_intent: %s\n", report.ActionIntentID)
	fmt.Fprintf(stdout, "- policy: %s (%s)\n", report.PolicyOutcome, report.PolicyDecisionID)
	fmt.Fprintf(stdout, "- content_sha256: %s\n", report.Artifact.ContentSHA256)
	return 0
}
