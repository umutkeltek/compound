package routeevidence

import (
	"strings"
	"testing"

	"compound/internal/envelope"
)

// S3 -- Compare replacement routes using matched, receipt-backed outcomes.
//
// The acceptance bullets this file pins:
//
//   - Establish incumbent correctness, critical-case outcomes, complete cost,
//     and latency on pinned cases with declared repetitions.
//   - Missing usage or prices, unmatched populations, insufficient
//     repetitions, and critical regressions cannot recommend replacement.
//   - Generation success is insufficient.
//
// What exists today: RouteOutcomeReview assesses ONE route outcome against
// ONE receipt (assessRouteOutcome). It is deliberately evidence-only and says
// so -- "receipt completed with state delta; route may be used as supporting
// evidence, not proof of general policy". The promotion tallies count wins
// and losses from a comparison-evidence ledger, but they are a COUNT of
// verdicts somebody already wrote; they never check that the two sides ran
// the SAME cases, that the cost and usage were actually measured, that enough
// repetitions ran, or that a critical case regressed.
//
// So the gap is a missing DETERMINISTIC COMPARISON: given the incumbent and
// candidate route outcomes for one decision, say whether the evidence can
// recommend a replacement, and refuse when it cannot.
//
// The assessment is evidence-only. It never changes routing policy; it names
// the blocking reason so the operator sees WHY a replacement is not
// recommendable.

// comparisonCase builds one route outcome review for a case.
func comparisonCase(t *testing.T, id, targetModel, requestID string, outcome string, stateChanged bool, usageTokens int, costMicros int64, latencyMS int64) RouteOutcomeReview {
	t.Helper()
	return RouteOutcomeReview{
		SchemaVersion:       RouteOutcomeReviewSchemaVersion,
		ID:                  id,
		CreatedAt:           "2026-10-27T10:00:00Z",
		Status:              "recorded",
		RouteEvidenceID:     "ev_" + id,
		RequestID:           requestID,
		WorkID:              "work_" + id,
		ReceiptID:           "rec_" + id,
		ReceiptOutcome:      outcome,
		ReceiptStateChanged: stateChanged,
		RouteStatus:         "completed",
		SelectedTarget:      Target{Provider: "fixture-provider", Model: targetModel, Protocol: "mcp"},
		Usage:               Usage{TotalTokens: usageTokens, PromptTokens: usageTokens},
		CostMicros:          costMicros,
		LatencyMS:           latencyMS,
		EvidenceRefs:        []string{"ev_" + id, "rec_" + id},
	}
}

// A matched population with complete cost/usage and enough repetitions, where
// the candidate is no worse on any critical case, CAN recommend replacement.
func TestRouteComparisonRecommendsWhenMatchedAndMeasured(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
		comparisonCase(t, "inc_b", "model-a", "req_b", "completed", true, 1200, 600, 140),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 900, 450, 100),
		comparisonCase(t, "cand_b", "model-b", "req_b", "completed", true, 1100, 550, 130),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases: []RouteComparisonCaseInput{
			{CaseID: "req_a", Incumbent: incumbent[:1], Candidate: candidate[:1]},
			{CaseID: "req_b", Incumbent: incumbent[1:], Candidate: candidate[1:]},
		},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})

	if !comparison.Assessment.RecommendReplacement {
		t.Fatalf("matched, measured comparison must recommend replacement: %+v", comparison.Assessment)
	}
	if comparison.Assessment.MatchedCases != 2 {
		t.Fatalf("matched cases = %d, want 2", comparison.Assessment.MatchedCases)
	}
	if comparison.Assessment.BlockingReasons != nil {
		t.Fatalf("matched comparison must have no blocking reasons: %v", comparison.Assessment.BlockingReasons)
	}
	// The recommendation must name the incumbent it would displace and the
	// candidate it would install -- a comparison without routes is not a decision.
	if comparison.IncumbentRoute.Model != "model-a" || comparison.CandidateRoute.Model != "model-b" {
		t.Fatalf("comparison routes = %+v -> %+v", comparison.IncumbentRoute, comparison.CandidateRoute)
	}
}

// Missing usage or prices on EITHER side blocks the recommendation: the
// upstream model-swap gate checked accuracy/cost/latency without proving the
// cost was actually measured, and missing usage became zero.
func TestRouteComparisonBlocksOnMissingUsageOrCost(t *testing.T) {
	base := func() ([]RouteOutcomeReview, []RouteOutcomeReview) {
		return []RouteOutcomeReview{
			comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
		}, []RouteOutcomeReview{
			comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 1000, 500, 120),
		}
	}
	for _, tc := range []struct {
		name    string
		mutate  func(incumbent, candidate []RouteOutcomeReview)
		wantSub string
	}{
		{
			name: "candidate missing usage",
			mutate: func(incumbent, candidate []RouteOutcomeReview) {
				candidate[0].Usage = Usage{}
			},
			wantSub: "usage",
		},
		{
			name: "candidate missing cost",
			mutate: func(incumbent, candidate []RouteOutcomeReview) {
				candidate[0].CostMicros = 0
			},
			wantSub: "cost",
		},
		{
			name: "incumbent missing usage",
			mutate: func(incumbent, candidate []RouteOutcomeReview) {
				incumbent[0].Usage = Usage{}
			},
			wantSub: "usage",
		},
		{
			name: "incumbent missing cost",
			mutate: func(incumbent, candidate []RouteOutcomeReview) {
				incumbent[0].CostMicros = 0
			},
			wantSub: "cost",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incumbent, candidate := base()
			tc.mutate(incumbent, candidate)
			comparison := BuildRouteComparison(RouteComparisonInput{
				DecisionID:     "decision_route_change_fixture",
				IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
				CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
				Cases: []RouteComparisonCaseInput{
					{CaseID: "req_a", Incumbent: incumbent[:1], Candidate: candidate[:1]},
					{CaseID: "req_b", Incumbent: incumbent[1:]},
					{CaseID: "req_c", Candidate: candidate[1:]},
				},
				CriticalCases:  []string{"req_a"},
				MinRepetitions: 1,
			})
			if comparison.Assessment.RecommendReplacement {
				t.Fatalf("missing measurement must block the recommendation: %+v", comparison.Assessment)
			}
			if !containsAnyString(comparison.Assessment.BlockingReasons, tc.wantSub) {
				t.Fatalf("blocking reasons %v must mention %q", comparison.Assessment.BlockingReasons, tc.wantSub)
			}
		})
	}
}

// Unmatched populations block the recommendation: the two sides must have run
// the SAME cases, or the difference is not attributable to the route.
func TestRouteComparisonBlocksOnUnmatchedPopulation(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
		comparisonCase(t, "inc_b", "model-a", "req_b", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 1000, 500, 120),
		comparisonCase(t, "cand_c", "model-b", "req_c", "completed", true, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases: []RouteComparisonCaseInput{
			{CaseID: "req_a", Incumbent: incumbent[:1], Candidate: candidate[:1]},
			{CaseID: "req_b", Incumbent: incumbent[1:]},
			{CaseID: "req_c", Candidate: candidate[1:]},
		},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("unmatched populations must block the recommendation: %+v", comparison.Assessment)
	}
	if !containsAnyString(comparison.Assessment.BlockingReasons, "matched") {
		t.Fatalf("blocking reasons %v must name the unmatched population", comparison.Assessment.BlockingReasons)
	}
}

// Insufficient repetitions block the recommendation.
func TestRouteComparisonBlocksOnInsufficientRepetitions(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 3,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("insufficient repetitions must block the recommendation: %+v", comparison.Assessment)
	}
	if !containsAnyString(comparison.Assessment.BlockingReasons, "repetition") {
		t.Fatalf("blocking reasons %v must name the insufficient repetitions", comparison.Assessment.BlockingReasons)
	}
}

// A critical-case regression blocks the recommendation even when everything
// else is matched and measured. Critical regressions are exactly the case the
// upstream "model is already obsolete" gate could not see.
func TestRouteComparisonBlocksOnCriticalCaseRegression(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "failed", false, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("a critical-case regression must block the recommendation: %+v", comparison.Assessment)
	}
	if !containsAnyString(comparison.Assessment.BlockingReasons, "critical") {
		t.Fatalf("blocking reasons %v must name the critical regression", comparison.Assessment.BlockingReasons)
	}
	if comparison.Assessment.CriticalRegressions == 0 {
		t.Fatalf("critical regressions = %d, want at least 1", comparison.Assessment.CriticalRegressions)
	}
}

// Generation success is insufficient. A route that produced output but whose
// RECEIPT did not complete the work is not a successful route -- and a
// candidate whose receipt did not complete the work cannot be recommended.
func TestRouteComparisonRejectsGenerationSuccessWithoutReceiptOutcome(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	// The candidate GENERATED successfully (route status completed) but its
	// receipt did not complete the work.
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "no_op", false, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("generation success without a completed receipt must not be recommended: %+v", comparison.Assessment)
	}
	if !containsAnyString(comparison.Assessment.BlockingReasons, "receipt") {
		t.Fatalf("blocking reasons %v must name the receipt outcome", comparison.Assessment.BlockingReasons)
	}
}

// The comparison is EVIDENCE. It must not mutate routing, must not claim task
// success, and must say so in its own warnings -- the same posture the
// single-outcome review takes.
func TestRouteComparisonIsEvidenceOnly(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	joined := strings.ToLower(strings.Join(comparison.Warnings, " "))
	for _, want := range []string{"does not change routing policy", "does not claim task success"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("comparison warnings %v must include %q", comparison.Warnings, want)
		}
	}
	// The receipt outcome, not the route status, decides the per-case verdict.
	if comparison.Cases[0].CandidateRuns[0].ReceiptOutcome != "completed" {
		t.Fatalf("case run must carry the receipt outcome: %+v", comparison.Cases[0].CandidateRuns[0])
	}
}

// An input with no cases at all cannot recommend anything.
func TestRouteComparisonBlocksOnEmptyPopulation(t *testing.T) {
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("an empty population must not recommend replacement: %+v", comparison.Assessment)
	}
	if comparison.Assessment.MatchedCases != 0 {
		t.Fatalf("matched cases = %d, want 0", comparison.Assessment.MatchedCases)
	}
}

// The comparison must bind to the receipt, not to route metadata alone.
func TestRouteComparisonRequiresReceiptBinding(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate[0].ReceiptID = ""
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("a run without a receipt binding must not recommend replacement: %+v", comparison.Assessment)
	}
	if !containsAnyString(comparison.Assessment.BlockingReasons, "receipt") {
		t.Fatalf("blocking reasons %v must name the missing receipt binding", comparison.Assessment.BlockingReasons)
	}
}

// The assessment must not panic on a receipt whose outcome is nonsense; an
// unknown outcome is a BLOCK, not a pass.
func TestRouteComparisonBlocksOnUnknownReceiptOutcome(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "wibble", false, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("an unknown receipt outcome must block the recommendation: %+v", comparison.Assessment)
	}
}

// A receipt whose outcome is a known failure shape is still a FAILURE, even
// when the route itself reported success.
func TestRouteComparisonTreatsFailedReceiptAsFailure(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "failed", false, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.Assessment.RecommendReplacement {
		t.Fatalf("a failed candidate receipt must block the recommendation: %+v", comparison.Assessment)
	}
	if comparison.Assessment.CandidateFailures == 0 {
		t.Fatalf("candidate failures = %d, want at least 1", comparison.Assessment.CandidateFailures)
	}
}

// The receipt outcome vocabulary is Compound's, not the route's.
func TestRouteComparisonReceiptOutcomeVocabulary(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "blocked", "no_op", "cancelled"} {
		if !validRouteComparisonReceiptOutcome(outcome) {
			t.Fatalf("receipt outcome %q must be a known outcome", outcome)
		}
	}
	for _, outcome := range []string{"", "wibble", "Completed"} {
		if validRouteComparisonReceiptOutcome(outcome) {
			t.Fatalf("receipt outcome %q must not be accepted", outcome)
		}
	}
}

// The comparison must carry a valid envelope header so it can be recorded.
func TestRouteComparisonIsValidEnvelope(t *testing.T) {
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_a", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_a", "completed", true, 1000, 500, 120),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "req_a", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"req_a"},
		MinRepetitions: 1,
	})
	if comparison.SchemaVersion != RouteComparisonSchemaVersion {
		t.Fatalf("schema version = %q, want %q", comparison.SchemaVersion, RouteComparisonSchemaVersion)
	}
	if comparison.ID == "" {
		t.Fatal("comparison must carry an id")
	}
	if comparison.DecisionID != "decision_route_change_fixture" {
		t.Fatalf("decision id = %q", comparison.DecisionID)
	}
	// The comparison cites the receipt-backed evidence it rests on.
	if len(comparison.EvidenceRefs) == 0 {
		t.Fatal("comparison must cite its evidence")
	}
	for _, ref := range comparison.EvidenceRefs {
		if !strings.HasPrefix(ref, "ev_") && !strings.HasPrefix(ref, "rec_") {
			t.Fatalf("evidence ref %q must cite route evidence or a receipt", ref)
		}
	}
	// It must be bindable to a receipt, like the single-outcome review.
	if comparison.Assessment.QualityBasis == "" {
		t.Fatal("comparison must declare its quality basis")
	}
	_ = envelope.Receipt{}
}

// The CASE identity is the CALLER's declaration, never the route evidence's
// own request id.
//
// This matters because the live ledger gives every row a DISTINCT request id:
// 143,931 rows carry 143,931 request ids, so no two rows ever share one. An
// implementation that derived the case from the request id would find no
// matched population at all, and one that paired rows by position would
// compare different cases and call it a match.
func TestRouteComparisonCaseIdentityIsOperatorPinned(t *testing.T) {
	// Two runs of the SAME case carry DIFFERENT request ids, exactly as the
	// live ledger does.
	incumbent := []RouteOutcomeReview{
		comparisonCase(t, "inc_a", "model-a", "req_incumbent_1", "completed", true, 1000, 500, 120),
	}
	candidate := []RouteOutcomeReview{
		comparisonCase(t, "cand_a", "model-b", "req_candidate_1", "completed", true, 900, 450, 100),
	}
	comparison := BuildRouteComparison(RouteComparisonInput{
		DecisionID:     "decision_route_change_fixture",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases:          []RouteComparisonCaseInput{{CaseID: "case_pinned_by_operator", Incumbent: incumbent, Candidate: candidate}},
		CriticalCases:  []string{"case_pinned_by_operator"},
		MinRepetitions: 1,
	})
	if !comparison.Assessment.RecommendReplacement {
		t.Fatalf("operator-pinned case with different request ids must still match: %+v", comparison.Assessment)
	}
	if comparison.Assessment.MatchedCases != 1 {
		t.Fatalf("matched cases = %d, want 1", comparison.Assessment.MatchedCases)
	}
	if comparison.Cases[0].CaseID != "case_pinned_by_operator" {
		t.Fatalf("case id = %q, want the operator-pinned id", comparison.Cases[0].CaseID)
	}
	// The runs still carry their OWN request ids, so the evidence stays
	// traceable to the exact gateway calls.
	if comparison.Cases[0].IncumbentRuns[0].RouteEvidenceID != "ev_inc_a" {
		t.Fatalf("case run lost its route evidence binding: %+v", comparison.Cases[0].IncumbentRuns[0])
	}
}

func containsAnyString(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), strings.ToLower(want)) {
			return true
		}
	}
	return false
}
