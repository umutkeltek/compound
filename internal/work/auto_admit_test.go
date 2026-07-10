package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
	autonomyproof "compound/internal/routeevidence/autonomy"
)

// makeProposedDirectorWork creates a director-proposed work of the given autonomy class,
// in state `proposed`, carrying the director provenance + trigger evidence refs the
// kill-switch keys on.
func makeProposedDirectorWork(t *testing.T, s *Store, class, id string, now time.Time) string {
	t.Helper()
	plan := ensureJobHealthAutonomyPlan(t, s)
	w, err := s.Create(CreateInput{
		WorkID:           id,
		Owner:            "compound-authority",
		Actor:            "director",
		SuccessPredicate: "review " + class + " signal " + id,
		State:            "proposed",
		EvidenceRefs:     []string{"proposed_by:director", "director_trigger:" + class + ":" + id, "route_plan:" + plan.ID},
		RouteDecisionID:  plan.ID,
	}, now)
	if err != nil {
		t.Fatalf("create proposed %s work: %v", class, err)
	}
	if w.State != "proposed" {
		t.Fatalf("director work should start proposed, got %q", w.State)
	}
	return w.ID
}

// TestKillSwitchHaltsAutoAdmit is the headline W9-J4 proof: a simulated trust-dial
// auto-admit of an ELIGIBLE class is denied while the policy-owned halt is engaged, a
// per-class disable blocks only its own class, and an unset halt lets the admit proceed.
// It fails if any auto-admit path ignores the flag.
func TestKillSwitchHaltsAutoAdmit(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)

	// Phase A — halt UNSET: an eligible auto-admit proceeds to admitted.
	jobA := makeProposedDirectorWork(t, s, "job_health", "work_kill_a", fixedNow)
	resA, err := s.AdmitAuto(jobA, eligible, fixedNow.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("unset halt must let eligible auto-admit proceed: %v", err)
	}
	if !resA.Admitted || resA.WorkState != "admitted" {
		t.Fatalf("unset halt auto-admit should reach admitted: %+v", resA)
	}
	if got, _ := s.Get(jobA); got.State != "admitted" {
		t.Fatalf("unset halt work should be admitted on disk, got %q", got.State)
	}

	// Phase B — GLOBAL halt: the same eligible auto-admit is denied and stays proposed.
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage global halt: %v", err)
	}
	jobB := makeProposedDirectorWork(t, s, "job_health", "work_kill_b", fixedNow)
	resB, err := s.AdmitAuto(jobB, eligible, fixedNow.Add(2*time.Minute))
	if err == nil {
		t.Fatalf("global halt must deny an eligible auto-admit")
	}
	if resB.Admitted || !resB.Halted {
		t.Fatalf("global halt result should be halted, not admitted: %+v", resB)
	}
	if got, _ := s.Get(jobB); got.State != "proposed" {
		t.Fatalf("global halt must leave work proposed, got %q", got.State)
	}

	// Phase C — PER-CLASS disable blocks job_health. A sibling class also stays
	// proposed because this first evidence-autonomy slice is class-scoped; the
	// job_health artifact cannot be replayed against another class.
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{
		DisabledClasses: map[string]string{"job_health": "noisy class parked"},
	}); err != nil {
		t.Fatalf("engage per-class halt: %v", err)
	}
	jobC := makeProposedDirectorWork(t, s, "job_health", "work_kill_c", fixedNow)
	srcC := makeProposedDirectorWork(t, s, "source_connection_readiness", "work_kill_src", fixedNow)

	resC, err := s.AdmitAuto(jobC, eligible, fixedNow.Add(3*time.Minute))
	if err == nil || resC.Admitted {
		t.Fatalf("per-class disable must block its own class: err=%v res=%+v", err, resC)
	}
	if got, _ := s.Get(jobC); got.State != "proposed" {
		t.Fatalf("disabled class must stay proposed, got %q", got.State)
	}

	resSrc, err := s.AdmitAuto(srcC, eligible, fixedNow.Add(4*time.Minute))
	if err == nil || resSrc.Admitted || !strings.Contains(err.Error(), "no enabled evidence-autonomy slice") {
		t.Fatalf("job_health eligibility must not admit a sibling class: err=%v res=%+v", err, resSrc)
	}
	if got, _ := s.Get(srcC); got.State != "proposed" {
		t.Fatalf("sibling class should stay proposed, got %q", got.State)
	}
}

// TestKillSwitchDoesNotBlockOperatorAdmit is the required negative: a manual operator
// admit through the admission seam is UNAFFECTED by the kill-switch, even under a global
// halt. Governance halts autonomy, not the operator.
func TestKillSwitchDoesNotBlockOperatorAdmit(t *testing.T) {
	s := newStore(t)
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage global halt: %v", err)
	}
	id := makeProposedDirectorWork(t, s, "job_health", "work_op_admit", fixedNow)

	out, err := s.Advance(id, AdvanceInput{Actor: "operator-test", To: "admitted"}, fixedNow.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("operator admit must not be blocked by autonomy halt: %v", err)
	}
	if out.State != "admitted" {
		t.Fatalf("operator admit should reach admitted under halt, got %q", out.State)
	}
}

// TestKillSwitchHaltsAdvanceSeamDefenseInDepth proves the generic Advance seam carries its
// own halt check: an autonomous (non-operator) admission is stopped BY THE HALT, not merely
// by the operator-provenance guard, so no auto-admit path can route around the flag.
func TestKillSwitchHaltsAdvanceSeamDefenseInDepth(t *testing.T) {
	s := newStore(t)
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage global halt: %v", err)
	}
	id := makeProposedDirectorWork(t, s, "job_health", "work_did", fixedNow)

	_, err := s.Advance(id, AdvanceInput{Actor: "auto-admit:director", To: "admitted"}, fixedNow.Add(1*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "autonomy halted") {
		t.Fatalf("generic seam must halt an autonomous admission with an autonomy-halt error, got %v", err)
	}
	if got, _ := s.Get(id); got.State != "proposed" {
		t.Fatalf("halted advance must leave work proposed, got %q", got.State)
	}
}

// TestAutoAdmitRequiresEligibilityGrant proves that the admission seam never
// infers authority: without a typed, content-addressed eligibility artifact,
// nothing auto-admits even with the halt unset.
func TestAutoAdmitRequiresEligibilityGrant(t *testing.T) {
	s := newStore(t)
	id := makeProposedDirectorWork(t, s, "job_health", "work_elig", fixedNow)

	res, err := s.AdmitAuto(id, AutoAdmitInput{By: "director"}, fixedNow.Add(1*time.Minute))
	if err == nil {
		t.Fatalf("auto-admit without a clean-window grant must be denied")
	}
	if res.Admitted {
		t.Fatalf("ineligible auto-admit must not admit: %+v", res)
	}
	if got, _ := s.Get(id); got.State != "proposed" {
		t.Fatalf("ineligible auto-admit must stay proposed, got %q", got.State)
	}
}

func TestAutoAdmitRejectsCallerAssertedEligibilityString(t *testing.T) {
	s := newStore(t)
	id := makeProposedDirectorWork(t, s, "job_health", "work_asserted_eligibility", fixedNow)
	res, err := s.AdmitAuto(id, AutoAdmitInput{
		By:                "director",
		EligibilityReason: "clean-window: trust me",
		GrantRef:          "grant:asserted-only",
	}, fixedNow.Add(time.Minute))
	if err == nil || res.Admitted || !strings.Contains(err.Error(), "typed eligibility artifact required") {
		t.Fatalf("caller assertion must not establish eligibility: err=%v res=%+v", err, res)
	}
	if got, _ := s.Get(id); got.State != "proposed" {
		t.Fatalf("caller assertion changed work state: %q", got.State)
	}
}

func TestAutoAdmitOperatorOnlyRiskFrontierAlwaysWins(t *testing.T) {
	tests := []struct {
		name          string
		mutationClass string
		reversibility string
		blastRadius   string
	}{
		{name: "external", mutationClass: "external_side_effect", reversibility: "easy", blastRadius: "local"},
		{name: "public", mutationClass: "public", reversibility: "easy", blastRadius: "local"},
		{name: "destructive", mutationClass: "destructive", reversibility: "easy", blastRadius: "local"},
		{name: "financial", mutationClass: "financial", reversibility: "easy", blastRadius: "local"},
		{name: "security", mutationClass: "security", reversibility: "easy", blastRadius: "local"},
		{name: "irreversible", mutationClass: "local_reversible", reversibility: "irreversible", blastRadius: "local"},
		{name: "hard", mutationClass: "local_reversible", reversibility: "hard", blastRadius: "local"},
		{name: "none", mutationClass: "local_reversible", reversibility: "none", blastRadius: "local"},
		{name: "wide", mutationClass: "local_reversible", reversibility: "easy", blastRadius: "public"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			eligible := typedJobHealthEligibility(t, s, fixedNow)
			id := makeProposedDirectorWork(t, s, "job_health", "work_risk_"+tc.name, fixedNow)
			w, err := s.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			var action envelope.ActionIntent
			if err := s.readJSON(s.envPath(w.RunIDs[0]), &action); err != nil {
				t.Fatal(err)
			}
			action.MutationClass = tc.mutationClass
			action.Reversibility = tc.reversibility
			action.BlastRadius = tc.blastRadius
			if err := action.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := s.writeJSON(s.envPath(action.ID), &action); err != nil {
				t.Fatal(err)
			}
			res, err := s.AdmitAuto(id, eligible, fixedNow.Add(time.Minute))
			if err == nil || res.Admitted || !strings.Contains(err.Error(), "operator-only") {
				t.Fatalf("operator-only risk escaped: err=%v res=%+v", err, res)
			}
			if got, _ := s.Get(id); got.State != "proposed" {
				t.Fatalf("risk denial changed work state: %q", got.State)
			}
		})
	}
}

func TestAutoAdmitRevalidatesGrantAtAdmission(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	seedDecisionClaim(t, s, "grant", "job-health-autonomy-revoked", map[string]string{
		"supersedes": "job-health-autonomy-test",
	}, fixedNow.Add(-time.Hour))
	id := makeProposedDirectorWork(t, s, "job_health", "work_revoked_at_admit", fixedNow)
	res, err := s.AdmitAuto(id, eligible, fixedNow.Add(time.Minute))
	if err == nil || res.Admitted || !strings.Contains(err.Error(), "missing or ambiguous") {
		t.Fatalf("revoked grant escaped admission revalidation: err=%v res=%+v", err, res)
	}
	if got, _ := s.Get(id); got.State != "proposed" {
		t.Fatalf("revoked grant changed work state: %q", got.State)
	}
}

func TestAutoAdmitRejectsTamperedEligibilityArtifact(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	id := strings.TrimPrefix(eligible.EligibilityRef, "eligibility:")
	path := filepath.Join(s.home, "artifacts", "autonomy-eligibility", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	payload["failure_count"] = float64(99)
	tampered, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(tampered, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	workID := makeProposedDirectorWork(t, s, "job_health", "work_tampered_eligibility", fixedNow)
	res, err := s.AdmitAuto(workID, eligible, fixedNow.Add(time.Minute))
	if err == nil || res.Admitted || !strings.Contains(err.Error(), "content digest mismatch") {
		t.Fatalf("tampered eligibility escaped: err=%v res=%+v", err, res)
	}
	if got, _ := s.Get(workID); got.State != "proposed" {
		t.Fatalf("tampered eligibility changed work state: %q", got.State)
	}
}

func TestAutoAdmitSerializesConcurrentHaltAtCommit(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	workID := makeProposedDirectorWork(t, s, "job_health", "work_halt_race", fixedNow)

	entered := make(chan struct{})
	release := make(chan struct{})
	previousHook := autoAdmitBeforeCommit
	autoAdmitBeforeCommit = func() {
		close(entered)
		<-release
	}
	defer func() { autoAdmitBeforeCommit = previousHook }()

	type admitOutcome struct {
		result AutoAdmitResult
		err    error
	}
	admitDone := make(chan admitOutcome, 1)
	go func() {
		result, err := s.AdmitAuto(workID, eligible, fixedNow.Add(time.Minute))
		admitDone <- admitOutcome{result: result, err: err}
	}()
	<-entered

	haltStarted := make(chan struct{})
	haltDone := make(chan error, 1)
	go func() {
		close(haltStarted)
		haltDone <- policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "concurrent incident"})
	}()
	<-haltStarted
	select {
	case err := <-haltDone:
		t.Fatalf("halt writer crossed the admission critical section: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)

	admitted := <-admitDone
	if admitted.err != nil || !admitted.result.Admitted {
		t.Fatalf("admission holding the lock should linearize first: err=%v result=%+v", admitted.err, admitted.result)
	}
	if err := <-haltDone; err != nil {
		t.Fatal(err)
	}
	second := makeProposedDirectorWork(t, s, "job_health", "work_after_concurrent_halt", fixedNow)
	if result, err := s.AdmitAuto(second, eligible, fixedNow.Add(2*time.Minute)); err == nil || result.Admitted || !result.Halted {
		t.Fatalf("halt must govern the next admission after it linearizes: err=%v result=%+v", err, result)
	}
}

func TestAutoAdmitConcurrentAttemptsCommitExactlyOnce(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	workID := makeProposedDirectorWork(t, s, "job_health", "work_double_admit", fixedNow)

	start := make(chan struct{})
	type admitOutcome struct {
		result AutoAdmitResult
		err    error
	}
	outcomes := make(chan admitOutcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			result, err := s.AdmitAuto(workID, eligible, fixedNow.Add(time.Minute))
			outcomes <- admitOutcome{result: result, err: err}
		}()
	}
	close(start)
	successes := 0
	failures := 0
	for i := 0; i < 2; i++ {
		outcome := <-outcomes
		if outcome.err == nil && outcome.result.Admitted {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent admission must commit exactly once: successes=%d failures=%d", successes, failures)
	}
	w, events, err := s.Show(workID)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "admitted" {
		t.Fatalf("winning admission did not persist: %+v", w)
	}
	admissionEvents := 0
	for _, event := range events {
		if event.From == "proposed" && event.To == "admitted" {
			admissionEvents++
		}
	}
	if admissionEvents != 1 {
		t.Fatalf("concurrent admission emitted %d admitted events, want 1: %+v", admissionEvents, events)
	}
}

func TestStaleCreatePlanCannotOverwriteAdmittedWork(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	routePlan := ensureJobHealthAutonomyPlan(t, s)
	const workID = "work_stale_create_after_admit"
	stale, err := s.CreatePlan(CreateInput{
		WorkID:           workID,
		Owner:            "compound-authority",
		Actor:            "director",
		SuccessPredicate: "stale create plan cannot overwrite admitted state",
		State:            "proposed",
		RouteDecisionID:  routePlan.ID,
		EvidenceRefs: []string{
			"proposed_by:director",
			"director_trigger:job_health:" + workID,
			"route_plan:" + routePlan.ID,
		},
	}, fixedNow, CreatePlanOptions{Actor: "director"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.admitWork(stale, fixedNow, "created", nil); err != nil {
		t.Fatal(err)
	}
	if result, err := s.AdmitAuto(workID, eligible, fixedNow.Add(time.Minute)); err != nil || !result.Admitted {
		t.Fatalf("setup admission failed: err=%v result=%+v", err, result)
	}
	if _, err := s.admitWork(stale, fixedNow.Add(2*time.Minute), "stale replay", nil); err == nil || !strings.Contains(err.Error(), "refusing stale create-plan") {
		t.Fatalf("stale create plan was not refused: %v", err)
	}
	got, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "admitted" {
		t.Fatalf("stale create plan overwrote admitted state: %+v", got)
	}
}

func TestAutoAdmitSerializesConcurrentTerminalCreation(t *testing.T) {
	s := newStore(t)
	plan := ensureJobHealthAutonomyPlan(t, s)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	workID := makeProposedDirectorWork(t, s, "job_health", "work_terminal_create_race", fixedNow)

	entered := make(chan struct{})
	release := make(chan struct{})
	previousHook := autoAdmitBeforeCommit
	autoAdmitBeforeCommit = func() {
		close(entered)
		<-release
	}
	defer func() { autoAdmitBeforeCommit = previousHook }()

	type admitOutcome struct {
		result AutoAdmitResult
		err    error
	}
	admitDone := make(chan admitOutcome, 1)
	go func() {
		result, err := s.AdmitAuto(workID, eligible, fixedNow.Add(time.Minute))
		admitDone <- admitOutcome{result: result, err: err}
	}()
	<-entered

	createStarted := make(chan struct{})
	createDone := make(chan error, 1)
	go func() {
		close(createStarted)
		_, err := s.Create(CreateInput{
			WorkID:           "work_concurrent_terminal_sample",
			Owner:            "compound-authority",
			Actor:            "operator-test",
			SuccessPredicate: "terminal sample created during admission",
			State:            "failed",
			RouteDecisionID:  plan.ID,
			EvidenceRefs: []string{
				"proposed_by:director",
				"director_trigger:job_health:concurrent-terminal",
				"route_plan:" + plan.ID,
			},
		}, fixedNow.Add(24*time.Hour))
		createDone <- err
	}()
	<-createStarted
	select {
	case err := <-createDone:
		t.Fatalf("terminal creation crossed the admission critical section: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)

	admitted := <-admitDone
	if admitted.err != nil || !admitted.result.Admitted {
		t.Fatalf("admission holding the lock should linearize first: err=%v result=%+v", admitted.err, admitted.result)
	}
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	terminal, err := s.Get("work_concurrent_terminal_sample")
	if err != nil || terminal.State != "failed" {
		t.Fatalf("concurrent terminal sample was not persisted after the lock released: state=%q err=%v", terminal.State, err)
	}
	artifactID := strings.TrimPrefix(eligible.EligibilityRef, "eligibility:")
	artifactBody, err := os.ReadFile(filepath.Join(s.home, "artifacts", "autonomy-eligibility", artifactID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var sealed autonomyproof.Artifact
	if err := json.Unmarshal(artifactBody, &sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := autonomyproof.Compile(s.home, sealed.Grant, sealed.RoutePlan, fixedNow); err == nil || !strings.Contains(err.Error(), "terminal timestamp after eligibility evaluation") {
		t.Fatalf("post-generation terminal sample did not invalidate the frozen evidence set: terminal=%+v err=%v", terminal, err)
	}

	autoAdmitBeforeCommit = previousHook
	next := makeProposedDirectorWork(t, s, "job_health", "work_after_terminal_sample", fixedNow.Add(2*time.Minute))
	if result, err := s.AdmitAuto(next, eligible, fixedNow.Add(3*time.Minute)); err == nil || result.Admitted || !strings.Contains(err.Error(), "stale or source evidence changed") {
		t.Fatalf("new terminal evidence must invalidate the old artifact: err=%v result=%+v", err, result)
	}
}
