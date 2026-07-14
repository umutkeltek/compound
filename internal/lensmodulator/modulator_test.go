package lensmodulator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/act"
	"compound/internal/behaviorallens"
	"compound/internal/policy"
)

// These tests drive the GOVERNED MODULATOR — the first organ that ACTS on a recorded
// behavioral-lens signal rather than only observing it. Every proof runs against a
// mktemp COMPOUND_HOME (never a real home) and makes NO provider/network call: signals
// are recorded as synthetic fixtures, and the only mutation is a reversible route/role
// repoint through the governed act seam. The three falsifiers are written to FAIL if the
// modulator's depth is wrong — an ungated write, an uncalibrated arm, or a leaked
// operator_private modulation.

func fixedNow() time.Time { return time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC) }

// mkSignal builds a valid recorded InferredSignal fixture for one lens+subject.
func mkSignal(t *testing.T, lensID, subject string, value float64, vis behaviorallens.Visibility, now time.Time) behaviorallens.InferredSignal {
	t.Helper()
	sig := behaviorallens.InferredSignal{
		SchemaVersion: behaviorallens.SignalSchemaVersion,
		ID:            "blsig_" + lensID + "_" + subject,
		LensID:        lensID,
		Subject:       subject,
		Value:         value,
		Confidence:    0.9,
		Visibility:    vis,
		Method:        "fixture",
		CreatedAt:     now.UTC().Format(time.RFC3339),
	}
	if err := sig.Validate(); err != nil {
		t.Fatalf("test signal invalid: %v", err)
	}
	return sig
}

func recordSignal(t *testing.T, home string, sig behaviorallens.InferredSignal) {
	t.Helper()
	if err := behaviorallens.RecordSignal(home, sig); err != nil {
		t.Fatalf("record signal: %v", err)
	}
}

// grantConfig returns the built-in policy plus ONE StandingGrant scoped to the demote
// capability, external_side_effect class, easy reversibility ceiling, local blast radius,
// and the exact route-store path — the D3 "set the envelope" shape. This is the operator's
// explicit pre-authorization: WITHOUT it the demote holds and act refuses.
func grantConfig(home string) policy.Config {
	cfg := policy.DefaultConfig()
	cfg.StandingGrants = append(cfg.StandingGrants, policy.StandingGrant{
		ID:                   "grant_lens_modulate",
		Capability:           CapabilityDemote,
		MutationClass:        "external_side_effect",
		ReversibilityCeiling: "easy",
		BlastRadius:          "local",
		TargetPath:           RouteStorePath(home),
	})
	return cfg
}

func armSycophancy(t *testing.T, home string, now time.Time) {
	t.Helper()
	th := Threshold{LensID: "sycophancy", MinValue: 0.5, DemoteTo: "demoted", Baseline: "active"}
	if err := Arm(home, th, now); err != nil {
		t.Fatalf("arm threshold: %v", err)
	}
}

// FALSIFIER 3 (calibration guard): arming a threshold with an EMPTY signal ledger for
// that lens is refused; arming succeeds ONLY after >=1 real measured signal is recorded.
// This is what stops a threshold being armed on an assumed/marketing number.
func TestArmRefusesWithoutMeasuredSignal(t *testing.T) {
	home := t.TempDir()
	now := fixedNow()
	th := Threshold{LensID: "sycophancy", MinValue: 0.5, DemoteTo: "demoted", Baseline: "active"}

	if err := Arm(home, th, now); err == nil {
		t.Fatal("expected Arm to refuse on an EMPTY signal ledger (calibration guard)")
	}
	if cfg, _ := LoadThresholds(home); len(cfg.Thresholds) != 0 {
		t.Fatalf("threshold config must stay empty after a refused arm, got %d", len(cfg.Thresholds))
	}

	// A signal for a DIFFERENT lens must not satisfy the guard for this lens.
	recordSignal(t, home, mkSignal(t, "hedging", "lane-x", 0.9, behaviorallens.VisibilityPublicSafe, now))
	if err := Arm(home, th, now); err == nil {
		t.Fatal("expected Arm to refuse when the only signal is for a different lens")
	}

	// Now a real measured signal for THIS lens exists — arming is allowed.
	recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
	if err := Arm(home, th, now); err != nil {
		t.Fatalf("Arm should succeed once a measured signal exists for the lens: %v", err)
	}
	cfg, err := LoadThresholds(home)
	if err != nil {
		t.Fatalf("load thresholds: %v", err)
	}
	if len(cfg.Thresholds) != 1 || cfg.Thresholds[0].LensID != "sycophancy" {
		t.Fatalf("armed threshold not persisted: %+v", cfg.Thresholds)
	}
}

// FALSIFIER 1 (default OFF): with NO threshold armed the modulator does nothing — even
// when a crossing signal and an allowing grant both exist. Absent config = fail-closed.
func TestModulateOffWhenNoThresholdArmed(t *testing.T) {
	home := t.TempDir()
	now := fixedNow()
	recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))

	res, err := Modulate(home, grantConfig(home), "work_off", now)
	if err != nil {
		t.Fatalf("modulate: %v", err)
	}
	if len(res.Modulations) != 0 {
		t.Fatalf("modulator must be OFF with no threshold armed, got %d modulations", len(res.Modulations))
	}
	if _, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "lane-a"); ok {
		t.Fatal("no route-store entry may exist when the modulator is OFF")
	}
}

// FALSIFIER 1 (no allowing PolicyDecision): a crossing signal with an ARMED threshold but
// NO standing grant holds at policy, so act refuses and NOTHING is written. Same input +
// an allowing grant DOES write — proving the PolicyDecision gate is load-bearing, not that
// the modulator simply never writes.
func TestModulateRefusesWithoutGrantWritesWithGrant(t *testing.T) {
	now := fixedNow()

	// (a) no grant -> refused, route store untouched.
	{
		home := t.TempDir()
		recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
		armSycophancy(t, home, now)

		res, err := Modulate(home, policy.DefaultConfig(), "work_nogrant", now)
		if err != nil {
			t.Fatalf("modulate: %v", err)
		}
		if len(res.Modulations) != 1 || res.Modulations[0].Applied || !res.Modulations[0].Refused {
			t.Fatalf("without a grant the demote must be REFUSED and not applied, got %+v", res.Modulations)
		}
		if v, _, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "lane-a"); v == "demoted" {
			t.Fatal("route store was mutated despite a refused (holding) PolicyDecision")
		}
	}

	// (b) allowing grant -> the SAME crossing signal is demoted through act, with a real
	// outward_delta receipt/transition and captured pre/post images.
	{
		home := t.TempDir()
		recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
		armSycophancy(t, home, now)

		res, err := Modulate(home, grantConfig(home), "work_grant", now)
		if err != nil {
			t.Fatalf("modulate: %v", err)
		}
		if len(res.Modulations) != 1 {
			t.Fatalf("want exactly one modulation, got %d", len(res.Modulations))
		}
		m := res.Modulations[0]
		if !m.Applied || m.Refused {
			t.Fatalf("with a grant the demote must apply, got %+v", m)
		}
		if m.MoveType != "outward_delta" {
			t.Fatalf("receipt move_type must be outward_delta, got %q", m.MoveType)
		}
		if m.PreImageRef == "" || m.PostImageRef == "" || m.ReceiptID == "" || m.TransitionID == "" {
			t.Fatalf("applied modulation must carry pre/post refs + receipt/transition ids: %+v", m)
		}
		if v, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "lane-a"); !ok || v != "demoted" {
			t.Fatalf("route store must show lane-a demoted, got value=%q ok=%v", v, ok)
		}
	}
}

// FALSIFIER 2 (crossing vs below + revert round-trip): a lane ABOVE threshold gets a
// reversible demote; a lane BELOW threshold is untouched; and the persisted act.Result
// lets Revert restore the exact pre-image (value equal after apply -> revert).
func TestModulateDemotesCrossingLeavesBelowAndRevertRestores(t *testing.T) {
	home := t.TempDir()
	now := fixedNow()
	recordSignal(t, home, mkSignal(t, "sycophancy", "lane-hi", 0.90, behaviorallens.VisibilityPublicSafe, now)) // crosses 0.5
	recordSignal(t, home, mkSignal(t, "sycophancy", "lane-lo", 0.10, behaviorallens.VisibilityPublicSafe, now)) // below 0.5
	armSycophancy(t, home, now)

	res, err := Modulate(home, grantConfig(home), "work_rt", now)
	if err != nil {
		t.Fatalf("modulate: %v", err)
	}
	// exactly the crossing lane is modulated; the below-threshold lane never appears.
	if len(res.Modulations) != 1 || res.Modulations[0].Lane != "lane-hi" || !res.Modulations[0].Applied {
		t.Fatalf("only the crossing lane should be demoted, got %+v", res.Modulations)
	}
	if _, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "lane-lo"); ok {
		t.Fatal("a lane BELOW threshold must be untouched (no route-store entry)")
	}
	if v, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "lane-hi"); !ok || v != "demoted" {
		t.Fatalf("crossing lane must be demoted, got value=%q ok=%v", v, ok)
	}

	// The apply captured a pre-image artifact holding the baseline ("active").
	store, err := act.NewArtifactStore(home)
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	pre, err := store.Get(res.Modulations[0].PreImageRef)
	if err != nil {
		t.Fatalf("load pre-image artifact: %v", err)
	}
	if string(pre) != "active" {
		t.Fatalf("captured pre-image must be the baseline %q, got %q", "active", string(pre))
	}

	// Revert consumes the persisted act.Result and restores the exact pre-image.
	later := now.Add(time.Second)
	rev, err := Revert(home, grantConfig(home), "lane-hi", "work_rt_revert", later)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !rev.Reverted {
		t.Fatalf("revert must report reverted: %+v", rev)
	}
	if v, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "lane-hi"); !ok || v != "active" {
		t.Fatalf("after revert the lane must be restored to baseline %q, got value=%q ok=%v", "active", v, ok)
	}
}

// FENCE 4 (operator_private stays operator-local): an operator_private signal's modulation
// is written under a quarantined operator-local key root and is EXCLUDED from the public
// route projection. A public_safe modulation is retained. This fails if a private signal's
// modulation ever leaks into the shared route surface / public projection.
func TestOperatorPrivateModulationStaysOperatorLocal(t *testing.T) {
	home := t.TempDir()
	now := fixedNow()
	recordSignal(t, home, mkSignal(t, "sycophancy", "pub-lane", 0.9, behaviorallens.VisibilityPublicSafe, now))
	recordSignal(t, home, mkSignal(t, "sycophancy", "priv-lane", 0.9, behaviorallens.VisibilityOperatorPrivate, now))
	armSycophancy(t, home, now)

	res, err := Modulate(home, grantConfig(home), "work_vis", now)
	if err != nil {
		t.Fatalf("modulate: %v", err)
	}
	if len(res.Modulations) != 2 {
		t.Fatalf("want both lanes modulated operator-locally, got %d", len(res.Modulations))
	}

	// The operator_private modulation is written to the quarantined root, NOT the shared one.
	if _, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityPublicSafe, "priv-lane"); ok {
		t.Fatal("operator_private modulation leaked into the shared route surface")
	}
	if v, ok, _ := CurrentAdjustment(home, behaviorallens.VisibilityOperatorPrivate, "priv-lane"); !ok || v != "demoted" {
		t.Fatalf("operator_private modulation must live under the operator-local root, got value=%q ok=%v", v, ok)
	}

	// The public projection contains ONLY the public_safe modulation.
	pub := res.PublicProjection()
	if len(pub.Modulations) != 1 || pub.Modulations[0].Lane != "pub-lane" {
		t.Fatalf("public projection must expose only the public_safe modulation, got %+v", pub.Modulations)
	}
	for _, m := range pub.Modulations {
		if m.Visibility == behaviorallens.VisibilityOperatorPrivate {
			t.Fatal("public projection must never carry an operator_private modulation")
		}
	}
}

// FALSIFIER 1b (byte-identity on refusal): a refused demote must leave the route store
// EXACTLY as it was — including ABSENT when it never existed. The weaker "value != demoted"
// check (case (a) of TestModulateRefusesWithoutGrantWritesWithGrant) misses a baseline key
// materialized BEFORE policy was consulted. This asserts no route-store write happened at
// all on refusal: it fails if any store mutation precedes an allowing PolicyDecision.
func TestModulateRefusalLeavesRouteStoreByteIdentical(t *testing.T) {
	now := fixedNow()

	// (a) no prior store: a refused demote must NOT create the route store file.
	{
		home := t.TempDir()
		recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
		armSycophancy(t, home, now)

		res, err := Modulate(home, policy.DefaultConfig(), "work_nogrant", now)
		if err != nil {
			t.Fatalf("modulate: %v", err)
		}
		if len(res.Modulations) != 1 || !res.Modulations[0].Refused {
			t.Fatalf("want exactly one refused modulation, got %+v", res.Modulations)
		}
		if _, err := os.Stat(RouteStorePath(home)); !os.IsNotExist(err) {
			t.Fatalf("a refused demote must not create the route store (no write before an allowing PolicyDecision); stat err=%v", err)
		}
	}

	// (b) pre-existing (unrelated) store: a refused demote must leave its bytes IDENTICAL —
	// it may not silently materialize a baseline entry for the refused lane.
	{
		home := t.TempDir()
		recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
		armSycophancy(t, home, now)
		if err := ensureLaneEntry(RouteStorePath(home), publicLaneRoot, "other-lane", "active"); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		before, err := os.ReadFile(RouteStorePath(home))
		if err != nil {
			t.Fatalf("read seeded store: %v", err)
		}

		if _, err := Modulate(home, policy.DefaultConfig(), "work_nogrant2", now); err != nil {
			t.Fatalf("modulate: %v", err)
		}
		after, err := os.ReadFile(RouteStorePath(home))
		if err != nil {
			t.Fatalf("read store after refusal: %v", err)
		}
		if string(before) != string(after) {
			t.Fatalf("refused demote mutated the route store:\n before=%q\n after =%q", string(before), string(after))
		}
	}
}

// FALSIFIER (governed action is observable end-to-end): an APPLIED demote's receipt/transition
// ids in the action ledger must resolve to real envelope files in the home's canonical event
// store (home/events/<id>.json) — not dangling references. This is what makes the modulator's
// own governed action visible to the event readers (executorlens/eventsource/capgraph). It
// fails if the act seam only returns receipts in memory instead of recording them.
func TestAppliedDemoteReceiptResolvesInEventStore(t *testing.T) {
	home := t.TempDir()
	now := fixedNow()
	recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
	armSycophancy(t, home, now)

	res, err := Modulate(home, grantConfig(home), "work_obs", now)
	if err != nil {
		t.Fatalf("modulate: %v", err)
	}
	if len(res.Modulations) != 1 || !res.Modulations[0].Applied {
		t.Fatalf("want exactly one applied modulation, got %+v", res.Modulations)
	}
	m := res.Modulations[0]
	for _, id := range []string{m.ReceiptID, m.TransitionID} {
		if id == "" {
			t.Fatalf("applied modulation must carry receipt+transition ids: %+v", m)
		}
		p := filepath.Join(home, "events", id+".json")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("ledger references %s but it does not resolve in the canonical event store (%s): %v", id, p, err)
		}
	}
}

// Plan is the dry-run default: it names the lanes that WOULD be demoted without touching
// the route store or evaluating policy.
func TestPlanIsDryRunAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	now := fixedNow()
	recordSignal(t, home, mkSignal(t, "sycophancy", "lane-a", 0.9, behaviorallens.VisibilityPublicSafe, now))
	armSycophancy(t, home, now)

	plan, err := Plan(home, now)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Modulations) != 1 || plan.Modulations[0].Applied {
		t.Fatalf("plan must show a would-be (not applied) modulation, got %+v", plan.Modulations)
	}
	if _, err := os.Stat(RouteStorePath(home)); !os.IsNotExist(err) {
		t.Fatal("Plan must not create the route store (dry-run writes nothing)")
	}
}
