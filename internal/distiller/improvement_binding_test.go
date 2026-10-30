package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"gopkg.in/yaml.v3"
)

// S4 -- Bind improvement acceptance to the change and its negative control.
//
// The acceptance bullets this file pins:
//
//   - An unrelated proof must NOT satisfy a review.
//   - Wrong-target, wrong-revision, and incomplete-consumer-set evidence must
//     be rejected.
//   - Cross-consumer regressions must be rejected.
//   - A regression-fix claim requires the SAME case to fail the intended
//     semantic assertion BEFORE and pass AFTER. Infrastructure failure,
//     both-green pairs, and unpaired passes cannot establish a fix.
//   - Replay stays zero-live-call.
//
// Today the accept path only checks that each cited eval proof is RECORDED and
// PASSING. It never checks that the proof is about the thing being changed, or
// that anything failed BEFORE the change. So an unrelated passing proof
// satisfies any review, and an improvement that fixes nothing looks identical
// to one that fixes a real regression.

// s4FixedNow is the fixed clock the S4 fixtures use.
func s4FixedNow() time.Time {
	return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
}

// s4Case is one pinned replay case: its id, the assertion it exercises, and
// the consumers it covers.
type s4Case struct {
	ID         string
	Assertion  string
	Consumers  []string
	Regression bool // this case fails before the change and passes after
}

// s4Proof is a recorded eval proof bound to a case.
func s4Proof(t *testing.T, home, id, caseID, assertion string, consumers []string, status string, liveCalls int, runErr string) EvalProofRecord {
	return s4ProofAt(t, home, id, caseID, assertion, consumers, status, liveCalls, runErr, s4TargetDigest)
}

// s4TargetDigest is the revision the pinned proofs were captured against.
const s4TargetDigest = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"

// s4ProofAt records a proof captured against a specific target revision.
func s4ProofAt(t *testing.T, home, id, caseID, assertion string, consumers []string, status string, liveCalls int, runErr string, targetDigest string) EvalProofRecord {
	t.Helper()
	checks := []EvalCheck{{Name: assertion, Status: "pass"}}
	if status == "fail" {
		checks = []EvalCheck{{Name: assertion, Status: "fail", Detail: "assertion did not hold before the change"}}
	}
	decisions := make([]AdjudicationDecision, 0, len(consumers))
	for _, consumer := range consumers {
		decisions = append(decisions, AdjudicationDecision{Kind: consumer, Verdict: "park"})
	}
	proof, err := RecordEvalReport(home, EvalReport{
		SchemaVersion: EvalCaseSchemaVersion,
		ID:            id,
		CaseID:        caseID,
		Status:        status,
		TraceRef:      "trace_" + id,
		TargetDigest:  targetDigest,
		Checks:        checks,
		Run: EvalRunReport{
			LiveModelCalls: liveCalls,
			Error:          runErr,
			Decisions:      decisions,
		},
	})
	if err != nil {
		t.Fatalf("record eval proof: %v", err)
	}
	return proof
}

// s4WriteCandidate writes a method_upgrade candidate the plan can be built from.
func s4WriteCandidate(t *testing.T, home, candidateID string) {
	t.Helper()
	dir := filepath.Join(home, candidates.InboxDir(candidates.MethodUpgrade))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir candidate inbox: %v", err)
	}
	candidate := Candidate{
		ID:           candidateID,
		Kind:         KindMethodUpgrade,
		Subject:      "conversation_mining",
		Statement:    "narrow the conversation-mining import so ungrounded facts never become candidates",
		Detail:       "self_improvement",
		Confidence:   0.86,
		EvidenceRefs: []string{"source_observation:convmin"},
		Verdict:      candidates.NoPromote,
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), data, 0o644); err != nil {
		t.Fatalf("write candidate: %v", err)
	}
}

// s4Review builds an accept review bound to the given cases.
func s4Review(t *testing.T, plan ImprovementPlan, cases []s4Case, after []EvalProofRecord, before []EvalProofRecord) ImprovementReview {
	t.Helper()
	review := ImprovementReview{
		SchemaVersion: ImprovementReviewSchemaVersion,
		ReviewID:      "review_" + plan.Draft.ImprovementID,
		PlanID:        plan.ID,
		CandidateID:   plan.CandidateID,
		CandidateKind: plan.CandidateKind,
		ImprovementID: plan.Draft.ImprovementID,
		Area:          plan.Draft.Area,
		Decision:      ImprovementReviewDecisionAccept,
		Rationale:     "the pinned replay case fails before the change and passes after it",
		Confidence:    0.8,
		EvidenceRefs:  plan.EvidenceRefs,
		TargetPath:    plan.TargetPath,
		TargetHash:    plan.TargetHash,
		Draft:         plan.Draft,
		ReplayCases:   s4ReplayCaseBindings(cases),
	}
	for _, proof := range after {
		review.EvalProofRefs = append(review.EvalProofRefs, proof.ProofRef)
	}
	for _, proof := range before {
		review.BeforeEvalProofRefs = append(review.BeforeEvalProofRefs, proof.ProofRef)
	}
	return review
}

func s4ReplayCaseBindings(cases []s4Case) []ReplayCaseBinding {
	out := make([]ReplayCaseBinding, 0, len(cases))
	for _, c := range cases {
		out = append(out, ReplayCaseBinding{
			CaseID:        c.ID,
			Assertion:     c.Assertion,
			Consumers:     append([]string(nil), c.Consumers...),
			RegressionFix: c.Regression,
		})
	}
	return out
}

// The falsifier the audit asks for FIRST: reproduce whether an unrelated
// proof can satisfy a review. Before the binding, it can.
func TestImprovementReviewRejectsUnrelatedProof(t *testing.T) {
	home := t.TempDir()
	candidateID := "method_upgrade_s4_unrelated"
	s4WriteCandidate(t, home, candidateID)

	plan, err := BuildImprovementPlan(home, candidateID, "", s4FixedNow())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := RecordImprovementPlan(home, plan); err != nil {
		t.Fatalf("record plan: %v", err)
	}

	// The proof is about a DIFFERENT case than the review claims to fix.
	unrelated := s4Proof(t, home, "eval_unrelated_case", "case_other", "some other assertion", []string{"memory"}, "pass", 0, "")
	cases := []s4Case{{ID: "case_target", Assertion: "assertion_target", Consumers: []string{"memory"}, Regression: true}}
	review := s4Review(t, plan, cases, []EvalProofRecord{unrelated}, nil)

	if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
		t.Fatal("an unrelated proof must not satisfy a review that claims to fix a pinned case")
	} else if !strings.Contains(err.Error(), "unrelated") && !strings.Contains(err.Error(), "case") {
		t.Fatalf("unrelated-proof refusal must name the case mismatch, got %v", err)
	}
}

// A wrong-target proof (right case, but the proof's own recorded case identity
// does not match) and an incomplete consumer set are both rejected.
func TestImprovementReviewRejectsWrongTargetAndIncompleteConsumers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(review *ImprovementReview)
		wantSub string
	}{
		{
			name: "wrong target digest",
			mutate: func(review *ImprovementReview) {
				review.ReplayCases[0].TargetDigest = "sha256:" + strings.Repeat("a", 64)
			},
			wantSub: "target",
		},
		{
			name: "incomplete consumer set",
			mutate: func(review *ImprovementReview) {
				review.ReplayCases[0].Consumers = []string{"memory", "skill"}
			},
			wantSub: "consumer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			candidateID := "method_upgrade_s4_" + strings.ReplaceAll(tc.name, " ", "_")
			s4WriteCandidate(t, home, candidateID)
			plan, err := BuildImprovementPlan(home, candidateID, "", s4FixedNow())
			if err != nil {
				t.Fatalf("build plan: %v", err)
			}
			if _, err := RecordImprovementPlan(home, plan); err != nil {
				t.Fatalf("record plan: %v", err)
			}
			cases := []s4Case{{ID: "case_target", Assertion: "assertion_target", Consumers: []string{"memory"}, Regression: true}}
			after := s4Proof(t, home, "eval_after_case_target", "case_target", "assertion_target", []string{"memory"}, "pass", 0, "")
			before := s4Proof(t, home, "eval_before_case_target", "case_target", "assertion_target", []string{"memory"}, "fail", 0, "")
			review := s4Review(t, plan, cases, []EvalProofRecord{after}, []EvalProofRecord{before})
			tc.mutate(&review)

			if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			} else if !strings.Contains(strings.ToLower(err.Error()), tc.wantSub) {
				t.Fatalf("%s refusal must mention %q, got %v", tc.name, tc.wantSub, err)
			}
		})
	}
}

// A regression-fix claim needs the SAME case to fail BEFORE and pass AFTER.
func TestImprovementReviewRequiresBeforeAndAfterPair(t *testing.T) {
	build := func(t *testing.T) (string, ImprovementPlan, ImprovementReview) {
		t.Helper()
		home := t.TempDir()
		candidateID := "method_upgrade_s4_pair"
		s4WriteCandidate(t, home, candidateID)
		plan, err := BuildImprovementPlan(home, candidateID, "", s4FixedNow())
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		if _, err := RecordImprovementPlan(home, plan); err != nil {
			t.Fatalf("record plan: %v", err)
		}
		cases := []s4Case{{ID: "case_target", Assertion: "assertion_target", Consumers: []string{"memory"}, Regression: true}}
		after := s4Proof(t, home, "eval_after_case_target", "case_target", "assertion_target", []string{"memory"}, "pass", 0, "")
		before := s4Proof(t, home, "eval_before_case_target", "case_target", "assertion_target", []string{"memory"}, "fail", 0, "")
		return home, plan, s4Review(t, plan, cases, []EvalProofRecord{after}, []EvalProofRecord{before})
	}

	t.Run("unpaired pass is refused", func(t *testing.T) {
		home, _, review := build(t)
		review.BeforeEvalProofRefs = nil
		if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
			t.Fatal("a pass with no failing before proof cannot establish a fix")
		} else if !strings.Contains(strings.ToLower(err.Error()), "before") {
			t.Fatalf("unpaired refusal must name the missing before proof, got %v", err)
		}
	})

	t.Run("both green is refused", func(t *testing.T) {
		home, _, review := build(t)
		// The "before" proof passes too: nothing failed, so nothing was fixed.
		greenBefore := s4Proof(t, home, "eval_green_before", "case_target", "assertion_target", []string{"memory"}, "pass", 0, "")
		review.BeforeEvalProofRefs = []string{greenBefore.ProofRef}
		if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
			t.Fatal("a both-green pair cannot establish a fix")
		} else if !strings.Contains(strings.ToLower(err.Error()), "fail") {
			t.Fatalf("both-green refusal must name the failing before proof, got %v", err)
		}
	})

	t.Run("infrastructure failure is refused", func(t *testing.T) {
		home, _, review := build(t)
		// The before proof failed because the RUN errored, not because the
		// intended semantic assertion failed.
		infraBefore := s4Proof(t, home, "eval_infra_before", "case_target", "assertion_target", []string{"memory"}, "fail", 0, "extractor crashed")
		review.BeforeEvalProofRefs = []string{infraBefore.ProofRef}
		if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
			t.Fatal("an infrastructure failure cannot establish a fix")
		} else if !strings.Contains(strings.ToLower(err.Error()), "infrastructure") && !strings.Contains(strings.ToLower(err.Error()), "assertion") {
			t.Fatalf("infra-failure refusal must name the semantic assertion, got %v", err)
		}
	})

	t.Run("matched pair is accepted", func(t *testing.T) {
		home, _, review := build(t)
		if _, err := RecordImprovementReview(home, review, s4FixedNow()); err != nil {
			t.Fatalf("a matched before/after pair on the same case must be accepted: %v", err)
		}
	})
}

// A cross-consumer regression -- the fix helps one consumer and breaks
// another -- must not certify.
func TestImprovementReviewRejectsCrossConsumerRegression(t *testing.T) {
	home := t.TempDir()
	candidateID := "method_upgrade_s4_cross"
	s4WriteCandidate(t, home, candidateID)
	plan, err := BuildImprovementPlan(home, candidateID, "", s4FixedNow())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := RecordImprovementPlan(home, plan); err != nil {
		t.Fatalf("record plan: %v", err)
	}
	cases := []s4Case{{ID: "case_target", Assertion: "assertion_target", Consumers: []string{"memory", "skill"}, Regression: true}}
	// The after proof covers only ONE of the two declared consumers: the other
	// consumer's coverage is missing, so the evidence is incomplete.
	after := s4Proof(t, home, "eval_after_partial", "case_target", "assertion_target", []string{"memory"}, "pass", 0, "")
	before := s4Proof(t, home, "eval_before_full", "case_target", "assertion_target", []string{"memory", "skill"}, "fail", 0, "")
	review := s4Review(t, plan, cases, []EvalProofRecord{after}, []EvalProofRecord{before})

	if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
		t.Fatal("evidence covering only some of the affected consumers must be rejected")
	} else if !strings.Contains(strings.ToLower(err.Error()), "consumer") {
		t.Fatalf("cross-consumer refusal must name the consumer coverage, got %v", err)
	}
}

// Replay stays zero-live-call: a proof that called a model live cannot
// certify an improvement.
func TestImprovementReviewRejectsLiveCallProof(t *testing.T) {
	home := t.TempDir()
	candidateID := "method_upgrade_s4_live"
	s4WriteCandidate(t, home, candidateID)
	plan, err := BuildImprovementPlan(home, candidateID, "", s4FixedNow())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := RecordImprovementPlan(home, plan); err != nil {
		t.Fatalf("record plan: %v", err)
	}
	cases := []s4Case{{ID: "case_target", Assertion: "assertion_target", Consumers: []string{"memory"}, Regression: true}}
	live := s4Proof(t, home, "eval_live_after", "case_target", "assertion_target", []string{"memory"}, "pass", 2, "")
	before := s4Proof(t, home, "eval_live_before", "case_target", "assertion_target", []string{"memory"}, "fail", 0, "")
	review := s4Review(t, plan, cases, []EvalProofRecord{live}, []EvalProofRecord{before})

	if _, err := RecordImprovementReview(home, review, s4FixedNow()); err == nil {
		t.Fatal("a proof with live model calls must not certify an improvement")
	}
}

// The review YAML draft must EXPOSE the binding fields, or a reviewer cannot
// fill them in and the accept path is unreachable in practice.
func TestImprovementReviewYAMLDraftExposesBindingFields(t *testing.T) {
	home := t.TempDir()
	candidateID := "method_upgrade_s4_draft"
	s4WriteCandidate(t, home, candidateID)
	plan, err := BuildImprovementPlan(home, candidateID, "", s4FixedNow())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	paths, err := RecordImprovementPlan(home, plan)
	if err != nil {
		t.Fatalf("record plan: %v", err)
	}
	data, err := os.ReadFile(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatalf("read review draft: %v", err)
	}
	// The draft must expose these as YAML KEYS the reviewer can fill in, not
	// merely mention them in prose: a substring check passes on the
	// eval_requirement sentence alone, which leaves the reviewer with no
	// key to fill and the accept path unreachable.
	var draft map[string]any
	if err := yaml.Unmarshal(data, &draft); err != nil {
		t.Fatalf("parse review draft yaml: %v", err)
	}
	for _, want := range []string{"replay_cases", "before_eval_proof_refs", "eval_proof_refs"} {
		if _, present := draft[want]; !present {
			t.Fatalf("review YAML draft must expose the key %q:\n%s", want, data)
		}
	}
}
