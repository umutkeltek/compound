package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// Falsifiers for the governed model-router promotion door.
//
// Every case runs in a temp home over synthetic .invalid lanes. Nothing here
// touches a live COMPOUND_HOME, and no test seeds the real registries.

const promotionApplyFixtureYAML = `version: 1
purpose: fixture model router for the governed promotion door
rules:
  - free lanes never gate a verdict
capability_classes: [classify-extract, judge-critique]
models:
  auto/judge:
    lane: auto-judge.invalid
    harness: switchback
    tier: premium
    packet_ceiling: large
    cost_class: api-marginal
    capabilities:
      classify-extract:
        level: capable
        evidence: bakeoff round 3 on synthetic corpus
      judge-critique:
        level: primary
        evidence: panel agreement 0.94
  fedora/qwen3.6-35b-a3b:
    lane: fedora-local.invalid
    harness: switchback
    tier: cheap
    packet_ceiling: medium
    cost_class: free
    capabilities:
      classify-extract:
        level: capable
        evidence: local probe sweep
routing_defaults:
  classify-extract: [auto/judge, fedora/qwen3.6-35b-a3b]
  judge-critique: [auto/judge]
`

// promotionApplyHome builds a temp home whose model-router authority carries the
// fixture above, and returns the home path plus the live authority revision.
func promotionApplyHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "model-capabilities.yaml")
	if err := os.WriteFile(source, []byte(promotionApplyFixtureYAML), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import fixture authority: %v", err)
	}
	records, err := LoadModelCapabilitiesSourceAuthority(home)
	if err != nil || len(records) == 0 {
		t.Fatalf("load source authority: %v (%d records)", err, len(records))
	}
	return home, records[len(records)-1].SourceRevision
}

// promotionReviewFile writes a synthetic route-promotion review artifact bound to
// the live authority revision, mirroring what EvaluateLanePromotions emits.
func promotionReviewFile(t *testing.T, home, revision, id, recommendation, laneID, class, current, recommended string) string {
	t.Helper()
	review := PromotionReview{
		SchemaVersion:                 PromotionReviewSchemaVersion,
		ID:                            id,
		CreatedAt:                     rfc3339(t0),
		Status:                        promotionReviewStatusFor(recommendation),
		Recommendation:                recommendation,
		Reason:                        "criteria satisfied on synthetic evidence",
		LaneID:                        laneID,
		CapabilityClass:               class,
		CurrentClaimLevel:             current,
		RecommendedClaimLevel:         recommended,
		EvidenceRefs:                  []string{"rev_synthetic_0001"},
		ModelCapabilitiesAuthorityRef: ModelCapabilitiesAuthorityRef,
		ModelCapabilitiesPath:         ModelCapabilitiesPath(home),
		ModelCapabilitiesSourceID:     "mcsa_synthetic",
		ModelCapabilitiesRevision:     revision,
		CriteriaAuthorityRef:          PromotionCriteriaAuthorityRef,
		CriteriaPath:                  PromotionCriteriaPath(home),
		CriteriaSourceID:              "pcsa_synthetic",
		CriteriaRevision:              "sha256:criteria-fixture",
	}
	path := filepath.Join(t.TempDir(), id+".json")
	body, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write review: %v", err)
	}
	return path
}

// mutateReviewFile rewrites one field of a review artifact on disk, so a
// tampered/stale input can be proven to fail closed.
func mutateReviewFile(t *testing.T, path string, mutate func(map[string]any)) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read review: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse review: %v", err)
	}
	mutate(payload)
	out := filepath.Join(t.TempDir(), "mutated.json")
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal mutated review: %v", err)
	}
	if err := os.WriteFile(out, body, 0o644); err != nil {
		t.Fatalf("write mutated review: %v", err)
	}
	return out
}

// authorityFingerprint captures every byte-surface a refused apply must leave
// untouched: the authority YAML, the source-authority ledger, and the event dir.
type authorityFingerprint struct {
	authority []byte
	ledger    []byte
	events    []string
}

func fingerprintAuthority(t *testing.T, home string) authorityFingerprint {
	t.Helper()
	authority, err := os.ReadFile(ModelCapabilitiesPath(home))
	if err != nil {
		t.Fatalf("read authority: %v", err)
	}
	ledger, _ := os.ReadFile(ModelCapabilitiesSourceAuthorityPath(home))
	var events []string
	_ = filepath.Walk(filepath.Join(home, "events"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			events = append(events, path)
		}
		return nil
	})
	return authorityFingerprint{authority: authority, ledger: ledger, events: events}
}

func (f authorityFingerprint) assertUnchanged(t *testing.T, home, label string) {
	t.Helper()
	got := fingerprintAuthority(t, home)
	if string(got.authority) != string(f.authority) {
		t.Fatalf("%s: authority bytes changed", label)
	}
	if string(got.ledger) != string(f.ledger) {
		t.Fatalf("%s: source-authority ledger changed", label)
	}
	if len(got.events) != len(f.events) {
		t.Fatalf("%s: event directory changed (%d -> %d entries)", label, len(f.events), len(got.events))
	}
}

// allowingDecision evaluates the real policy for the real intent. The default
// policy allows a local_reversible/easy/local action with obligations, which is
// exactly the envelope this door declares.
func allowingDecision(t *testing.T, home, work string, plan PromotionApplyPlan, now time.Time) (envelope.ActionIntent, envelope.PolicyDecision) {
	t.Helper()
	cfg, _, err := policy.LoadConfig(home)
	if err != nil {
		t.Fatalf("load policy config: %v", err)
	}
	ai := PromotionApplyIntent(work, plan, now)
	pd := policy.Evaluate(ai, cfg, now)
	if !policy.Allowed(pd) {
		t.Fatalf("expected an allowing decision for the governed door, got %s: %v", pd.Outcome, pd.Reasons)
	}
	return ai, pd
}

// FALSIFIER 1 — reviewed promote. Dry-run leaves every byte identical; a
// confirmed apply changes exactly one claim, returns the full proof chain, writes
// the applied record, and route planning reflects the new level.
func TestPromotionApplyReviewedPromoteMutatesOnlyTheReviewedClaim(t *testing.T) {
	home, revision := promotionApplyHome(t)
	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
		promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")

	before := fingerprintAuthority(t, home)

	// Dry-run: planning alone must not write.
	plan, err := PlanPromotionApply(home, reviewPath, "operator reviewed bakeoff evidence")
	if err != nil {
		t.Fatalf("plan promote: %v", err)
	}
	before.assertUnchanged(t, home, "dry-run plan")
	if plan.CurrentClaimLevel != "capable" || plan.ProposedClaimLevel != "primary" {
		t.Fatalf("bad plan levels: %+v", plan)
	}

	ai, pd := allowingDecision(t, home, "work_promote_fixture", plan, t0)
	res, err := ApplyPromotionReview(home, "work_promote_fixture", plan, pd, t0)
	if err != nil {
		t.Fatalf("apply promote: %v", err)
	}

	// Full proof chain present and resolvable in the home.
	rec := res.Record
	if rec.ActionIntentID != ai.ID || rec.PolicyDecisionID != pd.ID ||
		rec.ReceiptID == "" || rec.TransitionID == "" ||
		rec.PreImageRef == "" || rec.PostImageRef == "" {
		t.Fatalf("incomplete proof chain: %+v", rec)
	}
	if rec.AuthorityRevisionBefore == rec.AuthorityRevisionAfter {
		t.Fatalf("authority revision did not move: %s", rec.AuthorityRevisionAfter)
	}
	if _, err := os.Stat(PromotionAppliedRecordPath(home, "route_promotion_fixture_promote")); err != nil {
		t.Fatalf("applied record not written: %v", err)
	}

	// Exactly one claim moved; every other row, claim, default, and rule survived.
	after, _, _, err := loadModelCapabilitiesSnapshot(home)
	if err != nil {
		t.Fatalf("reload authority: %v", err)
	}
	if got := after.Models["auto/judge"].Capabilities["classify-extract"].Level; got != "primary" {
		t.Fatalf("reviewed claim not applied: %q", got)
	}
	if got := after.Models["auto/judge"].Capabilities["judge-critique"].Level; got != "primary" {
		t.Fatalf("sibling claim disturbed: %q", got)
	}
	if got := after.Models["fedora/qwen3.6-35b-a3b"].Capabilities["classify-extract"].Level; got != "capable" {
		t.Fatalf("other lane claim disturbed: %q", got)
	}
	if len(after.Models) != 2 || len(after.RoutingDefaults) != 2 || len(after.Rules) != 1 {
		t.Fatalf("manifest shape changed: models=%d defaults=%d rules=%d", len(after.Models), len(after.RoutingDefaults), len(after.Rules))
	}

	// FALSIFIER 4 — provenance: the installed claim names the review and criteria.
	evidence := after.Models["auto/judge"].Capabilities["classify-extract"].Evidence
	for _, want := range []string{"route_promotion_fixture_promote", "pcsa_synthetic", "sha256:criteria-fixture", "operator reviewed bakeoff evidence", "bakeoff round 3"} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("installed evidence missing %q: %q", want, evidence)
		}
	}
	if !strings.Contains(rec.AppliedClaimEvidence, "route_promotion_fixture_promote") || rec.CriteriaRevision != "sha256:criteria-fixture" {
		t.Fatalf("applied record provenance incomplete: %+v", rec)
	}

	// Route planning consumes the new level.
	routePlan, err := PlanCapabilityRoute(home, "classify-extract", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatalf("plan classify-extract route: %v", err)
	}
	if len(routePlan.Chain) == 0 || routePlan.Chain[0].ModelID != "auto/judge" || routePlan.Chain[0].ClaimLevel != "primary" {
		t.Fatalf("route plan does not reflect the applied claim: %+v", routePlan.Chain)
	}
}

// FALSIFIER 2 — reviewed demote. Same proof chain; everything else identical.
func TestPromotionApplyReviewedDemoteMutatesOnlyTheReviewedClaim(t *testing.T) {
	home, revision := promotionApplyHome(t)
	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_demote",
		promotionRecommendationDemote, "fedora/qwen3.6-35b-a3b", "classify-extract", "capable", "assumed")

	plan, err := PlanPromotionApply(home, reviewPath, "operator reviewed failing-rate evidence")
	if err != nil {
		t.Fatalf("plan demote: %v", err)
	}
	_, pd := allowingDecision(t, home, "work_demote_fixture", plan, t0)
	res, err := ApplyPromotionReview(home, "work_demote_fixture", plan, pd, t0)
	if err != nil {
		t.Fatalf("apply demote: %v", err)
	}
	if res.Record.ReceiptID == "" || res.Record.TransitionID == "" || res.Record.PreImageRef == "" {
		t.Fatalf("incomplete demote proof chain: %+v", res.Record)
	}

	after, _, _, err := loadModelCapabilitiesSnapshot(home)
	if err != nil {
		t.Fatalf("reload authority: %v", err)
	}
	if got := after.Models["fedora/qwen3.6-35b-a3b"].Capabilities["classify-extract"].Level; got != "assumed" {
		t.Fatalf("demote not applied: %q", got)
	}
	if got := after.Models["auto/judge"].Capabilities["classify-extract"].Level; got != "capable" {
		t.Fatalf("other lane claim disturbed by demote: %q", got)
	}
	if got := after.Models["auto/judge"].Capabilities["judge-critique"].Evidence; got != "panel agreement 0.94" {
		t.Fatalf("untouched claim evidence rewritten: %q", got)
	}
	if len(after.RoutingDefaults["classify-extract"]) != 2 {
		t.Fatalf("routing defaults disturbed: %+v", after.RoutingDefaults)
	}
}

// FALSIFIER 3 — fail closed. Every malformed, stale, mismatched, or
// already-resolved input refuses BEFORE any mutation, leaving the authority, the
// source-authority ledger, and the event directory byte-identical.
func TestPromotionApplyFailsClosedBeforeAnyAuthorityMutation(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, home, revision, reviewPath string) string
		wantErr string
	}{
		{
			name: "hold recommendation",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["recommendation"] = promotionRecommendationHoldInsufficient
				})
			},
			wantErr: "not applicable",
		},
		{
			name: "missing recommended claim",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["recommended_claim_level"] = ""
				})
			},
			wantErr: "no recommended_claim_level",
		},
		{
			name: "stale current claim",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["current_claim_level"] = "assumed"
				})
			},
			wantErr: "stale",
		},
		{
			name: "stale model authority revision",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["model_capabilities_revision"] = "sha256:0000000000000000"
				})
			},
			wantErr: "stale",
		},
		{
			name: "mismatched lane",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["lane_id"] = "ghost/lane.invalid"
				})
			},
			wantErr: "absent from",
		},
		{
			name: "mismatched capability class",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["capability_class"] = "not-a-class"
				})
			},
			wantErr: "does not claim",
		},
		{
			name: "tampered status",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				return mutateReviewFile(t, reviewPath, func(p map[string]any) {
					p["status"] = promotionReviewStatusFiled
				})
			},
			wantErr: "not appliable",
		},
		{
			name: "malformed json",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				path := filepath.Join(t.TempDir(), "malformed.json")
				if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
					t.Fatalf("write malformed: %v", err)
				}
				return path
			},
			wantErr: "parse route promotion review",
		},
		{
			name: "already hold-recorded",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				dir := filepath.Join(home, "reviews", routePromotionReviewArea)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir reviews: %v", err)
				}
				if err := os.WriteFile(promotionHoldRecordPath(home, "route_promotion_fixture_promote"), []byte("{}"), 0o644); err != nil {
					t.Fatalf("seed hold record: %v", err)
				}
				return reviewPath
			},
			wantErr: "already recorded as a hold",
		},
		{
			name: "already applied",
			prepare: func(t *testing.T, home, revision, reviewPath string) string {
				dir := filepath.Join(home, "reviews", routePromotionReviewArea)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir reviews: %v", err)
				}
				if err := os.WriteFile(PromotionAppliedRecordPath(home, "route_promotion_fixture_promote"), []byte("{}"), 0o644); err != nil {
					t.Fatalf("seed applied record: %v", err)
				}
				return reviewPath
			},
			wantErr: "already has an applied record",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, revision := promotionApplyHome(t)
			reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
				promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")
			before := fingerprintAuthority(t, home)

			input := tc.prepare(t, home, revision, reviewPath)

			_, err := PlanPromotionApply(home, input, "operator rationale")
			if err == nil {
				t.Fatalf("expected refusal for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("refusal for %s should mention %q, got %v", tc.name, tc.wantErr, err)
			}
			before.assertUnchanged(t, home, tc.name)
		})
	}
}

// FALSIFIER 3 (control) — the apply refuses on a NON-allowing PolicyDecision and
// the authority stays byte-identical. This proves the policy gate is load-bearing
// and not decorative: with the class held, the same otherwise-valid plan writes
// nothing.
func TestPromotionApplyRefusesWithoutAnAllowingDecision(t *testing.T) {
	home, revision := promotionApplyHome(t)
	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
		promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")
	plan, err := PlanPromotionApply(home, reviewPath, "operator rationale")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	before := fingerprintAuthority(t, home)

	cfg := policy.DefaultConfig()
	cfg.ClassOutcome["local_reversible"] = policy.ClassRule{Outcome: "hold"}
	ai := PromotionApplyIntent("work_held", plan, t0)
	pd := policy.Evaluate(ai, cfg, t0)
	if policy.Allowed(pd) {
		t.Fatalf("fixture policy should hold, got %s", pd.Outcome)
	}

	if _, err := ApplyPromotionReview(home, "work_held", plan, pd, t0); err == nil {
		t.Fatal("apply must refuse under a non-allowing PolicyDecision")
	}
	before.assertUnchanged(t, home, "held decision")
	if _, err := os.Stat(PromotionAppliedRecordPath(home, "route_promotion_fixture_promote")); err == nil {
		t.Fatal("a refused apply must not write an applied record")
	}
}

// FALSIFIER 5 — undo. The recorded one-line undo restores the EXACT pre-image,
// produces its own receipt/transition, and refuses once the post-image revision
// no longer matches.
func TestPromotionRevertRestoresExactPreImageAndRefusesOnDrift(t *testing.T) {
	home, revision := promotionApplyHome(t)
	original, err := os.ReadFile(ModelCapabilitiesPath(home))
	if err != nil {
		t.Fatalf("read original authority: %v", err)
	}
	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
		promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")

	plan, err := PlanPromotionApply(home, reviewPath, "operator rationale")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, pd := allowingDecision(t, home, "work_undo_fixture", plan, t0)
	applied, err := ApplyPromotionReview(home, "work_undo_fixture", plan, pd, t0)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	appliedPath := PromotionAppliedRecordPath(home, "route_promotion_fixture_promote")

	// The recorded undo command is literal and names this exact record.
	if !strings.Contains(applied.Record.UndoCommand, "compound route promotion-revert --applied "+appliedPath) ||
		!strings.Contains(applied.Record.UndoCommand, "--confirm") {
		t.Fatalf("undo command is not a runnable one-liner: %q", applied.Record.UndoCommand)
	}

	revertPlan, err := PlanPromotionRevert(home, appliedPath)
	if err != nil {
		t.Fatalf("plan revert: %v", err)
	}
	record, err := LoadPromotionAppliedRecord(appliedPath)
	if err != nil {
		t.Fatalf("load applied record: %v", err)
	}
	cfg, _, err := policy.LoadConfig(home)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	revertAI := PromotionRevertIntent("work_undo_fixture", record, t0)
	revertPD := policy.Evaluate(revertAI, cfg, t0)
	if !policy.Allowed(revertPD) {
		t.Fatalf("revert decision should allow, got %s", revertPD.Outcome)
	}
	revertRes, err := RevertPromotionApply(home, "work_undo_fixture", revertPlan, revertPD, t0)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if revertRes.ReceiptID == "" || revertRes.TransitionID == "" ||
		revertRes.ReceiptID == applied.Record.ReceiptID || revertRes.TransitionID == applied.Record.TransitionID {
		t.Fatalf("revert must produce its own receipt/transition: %+v", revertRes)
	}

	restored, err := os.ReadFile(ModelCapabilitiesPath(home))
	if err != nil {
		t.Fatalf("read restored authority: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("revert did not restore the exact pre-image bytes")
	}

	// Drift: the record now points at a post-image revision the authority no
	// longer carries, so a second revert must refuse.
	if _, err := PlanPromotionRevert(home, appliedPath); err == nil {
		t.Fatal("revert must refuse once the post-image revision no longer matches")
	}

	// After the revert, route planning still works: the source-authority ledger
	// head followed the restore instead of naming a reverted-away revision.
	if _, err := PlanCapabilityRoute(home, "classify-extract", CapabilityRoutePlanOptions{}, t0); err != nil {
		t.Fatalf("route planning broken after revert: %v", err)
	}
}

// FALSIFIER 5 (drift control) — a revert must refuse when the authority moved
// after the apply, rather than silently discarding the newer change. This proves
// the revision guard has teeth independently of the record's own status field.
func TestPromotionRevertRefusesWhenAuthorityMovedAfterApply(t *testing.T) {
	home, revision := promotionApplyHome(t)
	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
		promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")
	plan, err := PlanPromotionApply(home, reviewPath, "operator rationale")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, pd := allowingDecision(t, home, "work_drift_fixture", plan, t0)
	if _, err := ApplyPromotionReview(home, "work_drift_fixture", plan, pd, t0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	appliedPath := PromotionAppliedRecordPath(home, "route_promotion_fixture_promote")

	// The undo is valid right now...
	if _, err := PlanPromotionRevert(home, appliedPath); err != nil {
		t.Fatalf("revert should be planable immediately after apply: %v", err)
	}

	// ...but a later authority change invalidates it.
	later := filepath.Join(t.TempDir(), "later.yaml")
	body := `version: 1
purpose: later change
rules: []
capability_classes: [classify-extract]
models:
  fedora/qwen3.6-35b-a3b:
    lane: fedora-local.invalid
    tier: cheap
    packet_ceiling: medium
    capabilities:
      classify-extract:
        level: assumed
        evidence: later independent change
routing_defaults:
  classify-extract: [fedora/qwen3.6-35b-a3b]
`
	if err := os.WriteFile(later, []byte(body), 0o644); err != nil {
		t.Fatalf("write later input: %v", err)
	}
	if _, err := ImportModelCapabilitiesYAML(home, later, t0); err != nil {
		t.Fatalf("later import: %v", err)
	}

	authorityBefore, _ := os.ReadFile(ModelCapabilitiesPath(home))
	if _, err := PlanPromotionRevert(home, appliedPath); err == nil {
		t.Fatal("revert must refuse over a newer authority change")
	}
	authorityAfter, _ := os.ReadFile(ModelCapabilitiesPath(home))
	if string(authorityBefore) != string(authorityAfter) {
		t.Fatal("a refused revert must not touch the authority")
	}
}

// FALSIFIER 7 — the additive bridge is preserved. promotion-apply is the
// authoring door; import stays additive and cannot delete Compound-only rows,
// claims, classes, rules, or route positions — including a row this door just
// promoted.
func TestImportRemainsAdditiveBridgeAfterGovernedApply(t *testing.T) {
	home, revision := promotionApplyHome(t)
	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
		promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")
	plan, err := PlanPromotionApply(home, reviewPath, "operator rationale")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, pd := allowingDecision(t, home, "work_bridge_fixture", plan, t0)
	if _, err := ApplyPromotionReview(home, "work_bridge_fixture", plan, pd, t0); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// A later PARTIAL bridge input mentioning only one lane and one class.
	partial := filepath.Join(t.TempDir(), "partial.yaml")
	body := `version: 1
purpose: partial bridge input
rules: []
capability_classes: [classify-extract]
models:
  fedora/qwen3.6-35b-a3b:
    lane: fedora-local.invalid
    tier: cheap
    packet_ceiling: medium
    capabilities:
      classify-extract:
        level: capable
        evidence: refreshed local probe sweep
routing_defaults:
  classify-extract: [fedora/qwen3.6-35b-a3b]
`
	if err := os.WriteFile(partial, []byte(body), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	if _, err := ImportModelCapabilitiesYAML(home, partial, t0); err != nil {
		t.Fatalf("partial import: %v", err)
	}

	after, _, _, err := loadModelCapabilitiesSnapshot(home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := after.Models["auto/judge"]; !ok {
		t.Fatal("bridge import deleted a Compound-only row")
	}
	if got := after.Models["auto/judge"].Capabilities["classify-extract"].Level; got != "primary" {
		t.Fatalf("bridge import reverted the governed claim: %q", got)
	}
	if _, ok := after.Models["auto/judge"].Capabilities["judge-critique"]; !ok {
		t.Fatal("bridge import deleted a Compound-only claim")
	}
	if _, ok := after.RoutingDefaults["judge-critique"]; !ok {
		t.Fatal("bridge import deleted a Compound-only route position")
	}
	if len(after.Rules) != 1 {
		t.Fatalf("bridge import deleted Compound-only rules: %+v", after.Rules)
	}
}
