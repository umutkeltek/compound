package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
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
	ReceiptID        string                  `json:"receipt_id"`
	TransitionID     string                  `json:"transition_id"`
	PreImageRef      string                  `json:"pre_image_ref"`
	PostImageRef     string                  `json:"post_image_ref"`
	Artifact         harness.HarnessArtifact `json:"artifact"`
	Details          []string                `json:"details,omitempty"`
}

var applyHarnessPermissions = harness.ApplyPermissions

func runHarnessGenerateApply(args []string, stdout, stderr io.Writer, now time.Time) int {
	harnessName := strings.TrimSpace(flagValue(args, "--harness"))
	workID := strings.TrimSpace(flagValue(args, "--work"))
	actor := strings.TrimSpace(flagValue(args, "--actor"))
	if harnessName == "" || workID == "" || actor == "" {
		fmt.Fprintln(stderr, "usage: compound harness generate --harness <name> --apply --work <work_id> --actor <operator> [--json]")
		return 2
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(stderr, "error:", err, "(set COMPOUND_HOME)")
		return 1
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	w, err := store.Get(workID)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if w.State != "running" {
		fmt.Fprintf(stderr, "refused: work %s is %s; permission apply requires running work\n", workID, w.State)
		return 1
	}
	if !policy.HasOperatorProvenance(actor) {
		fmt.Fprintf(stderr, "refused: --actor %q lacks operator provenance\n", actor)
		return 1
	}
	leases, err := store.ListLeases()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	leaseHolder := ""
	for _, lease := range leases {
		if lease.WorkID == workID {
			leaseHolder = lease.By
			break
		}
	}
	if leaseHolder == "" || leaseHolder != actor {
		fmt.Fprintf(stderr, "refused: --actor %q does not match work lease holder %q\n", actor, leaseHolder)
		return 1
	}
	render, err := harness.RenderPermissions(homePath, harnessName)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	preImageRef := "sha256:" + permissionApplyDigest(render.Preimage)
	approvedPostImageRef := render.Artifact.ContentSHA256
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
			EvidenceRefs:  []string{render.Artifact.SourceAuthorityRef, render.Artifact.SourceRevision, preImageRef, approvedPostImageRef},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         actor,
		Capability:    "harness.permissions.apply",
		Action:        "reconcile_harness_permissions",
		Target:        envelope.ActionTarget{Kind: "harness_settings", ID: harnessName, Path: render.TargetPath},
		MutationClass: "security",
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
	authorize := permissionApplyAuthorization(store, homePath, workID)
	applied, err := applyHarnessPermissions(homePath, render, action, decision, authorize)
	if err != nil {
		postImageRef := observedPermissionTargetRef(render.TargetPath, render.Preimage)
		changed := postImageRef != preImageRef
		filesChanged := []string{}
		if changed {
			filesChanged = []string{render.TargetPath}
		}
		outcome, recordErr := store.RecordReceiptedAction(workID, work.ReceiptedActionInput{
			Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
			Outcome: "failed", Changed: changed, Summary: "permission activation failed: " + err.Error(), MoveType: "control_plane_repair",
			ProofRefs: []string{render.Artifact.SourceAuthorityRef, render.Artifact.SourceRevision}, BeforeRefs: []string{preImageRef}, AfterRefs: []string{postImageRef},
			FilesChanged: filesChanged, FailureCause: &envelope.FailureCause{SurfaceOutcome: err.Error(), Mechanism: "governed permission activation failed after policy allowance"},
		}, now)
		if recordErr != nil {
			fmt.Fprintf(stderr, "error: permission apply failed: %v; record failed outcome: %v\n", err, recordErr)
			return 1
		}
		fmt.Fprintf(stderr, "error: permission apply failed and was receipted as %s/%s: %v\n", outcome.Receipt.ID, outcome.Transition.ID, err)
		return 1
	}
	postImageRef := applied.Artifact.ContentSHA256
	receiptOutcome := "completed"
	filesChanged := []string{}
	if !applied.Changed {
		receiptOutcome = "no_op"
	} else {
		filesChanged = []string{applied.TargetPath}
	}
	outcome, err := store.RecordReceiptedAction(workID, work.ReceiptedActionInput{
		Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
		Outcome: receiptOutcome, Changed: applied.Changed, Summary: "permission activation " + receiptOutcome, MoveType: "control_plane_repair",
		ProofRefs: []string{applied.Artifact.SourceAuthorityRef, applied.Artifact.SourceRevision}, BeforeRefs: []string{preImageRef}, AfterRefs: []string{postImageRef},
		FilesChanged: filesChanged,
	}, now)
	if err != nil {
		fmt.Fprintln(stderr, "error: permission activation published but outcome receipt failed:", err)
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
		ReceiptID:        outcome.Receipt.ID,
		TransitionID:     outcome.Transition.ID,
		PreImageRef:      preImageRef,
		PostImageRef:     postImageRef,
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
	fmt.Fprintf(stdout, "- receipt: %s\n", report.ReceiptID)
	fmt.Fprintf(stdout, "- transition: %s\n", report.TransitionID)
	fmt.Fprintf(stdout, "- content_sha256: %s\n", report.Artifact.ContentSHA256)
	return 0
}

func permissionApplyAuthorization(store *work.Store, homePath, workID string) harness.PermissionApplyAuthorization {
	return func(action envelope.ActionIntent, approved envelope.PolicyDecision, now time.Time) error {
		currentWork, err := store.Get(workID)
		if err != nil {
			return fmt.Errorf("reload work %s: %w", workID, err)
		}
		if currentWork.State != "running" {
			return fmt.Errorf("work %s is %s; permission apply requires running work", workID, currentWork.State)
		}
		leases, err := store.ListLeases()
		if err != nil {
			return fmt.Errorf("reload work lease: %w", err)
		}
		leaseHolder := ""
		for _, lease := range leases {
			if lease.WorkID == workID {
				leaseHolder = lease.By
				break
			}
		}
		if leaseHolder == "" || leaseHolder != action.Actor {
			return fmt.Errorf("action actor %q does not match current work lease holder %q", action.Actor, leaseHolder)
		}
		if expiresAt := strings.TrimSpace(approved.ExpiresAt); expiresAt != "" {
			expires, err := time.Parse(time.RFC3339, expiresAt)
			if err != nil {
				return fmt.Errorf("approved permission policy expires_at invalid: %w", err)
			}
			if !now.Before(expires) {
				return fmt.Errorf("approved permission policy expired at %s", expiresAt)
			}
		}
		cfg, _, err := policy.LoadConfig(homePath)
		if err != nil {
			return fmt.Errorf("reload permission policy: %w", err)
		}
		current := policy.Evaluate(action, cfg, now)
		if !policy.Allowed(current) {
			return fmt.Errorf("current permission policy %s (%s)", current.Outcome, strings.Join(current.Reasons, "; "))
		}
		if current.StandingGrantID != approved.StandingGrantID {
			return fmt.Errorf("current standing grant %q does not match approved standing grant %q", current.StandingGrantID, approved.StandingGrantID)
		}
		if current.Outcome != approved.Outcome || current.ExpiresAt != approved.ExpiresAt || !reflect.DeepEqual(current.Obligations, approved.Obligations) {
			return fmt.Errorf("current permission policy outcome or obligations no longer match the approved decision")
		}
		return nil
	}
}

func permissionApplyDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("%x", digest[:])
}

func observedPermissionTargetRef(path string, fallback []byte) string {
	body, err := os.ReadFile(path)
	if err != nil {
		body = fallback
	}
	return "sha256:" + permissionApplyDigest(body)
}
