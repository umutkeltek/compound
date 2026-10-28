package routeevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// RouteComparisonSchemaVersion is the sealed shape of a matched route
// comparison for ONE route-change decision.
const RouteComparisonSchemaVersion = "compound.route_comparison.v0.1"

// RouteComparison is the DETERMINISTIC assessment of whether the evidence
// gathered for one route-change decision can recommend replacing the incumbent
// route with the candidate route.
//
// It exists because RouteOutcomeReview assesses ONE route outcome against ONE
// receipt, and that is deliberately not a comparison: the review says
// "route may be used as supporting evidence, not proof of general policy".
// The promotion tallies count wins and losses from a comparison-evidence
// ledger, but those are a COUNT of verdicts somebody else already wrote. They
// never check that the two sides ran the SAME cases, that cost and usage were
// actually measured, that enough repetitions ran, or that a critical case
// regressed.
//
// This type closes that gap. It answers one question -- "can this evidence
// recommend this replacement?" -- and names every reason it cannot.
//
// It is EVIDENCE. It does not change routing policy, does not mutate a
// gateway, does not spend, and does not claim task success.
type RouteComparison struct {
	SchemaVersion  string                    `json:"schema_version"`
	ID             string                    `json:"id"`
	CreatedAt      string                    `json:"created_at"`
	DecisionID     string                    `json:"decision_id"`
	IncumbentRoute Target                    `json:"incumbent_route"`
	CandidateRoute Target                    `json:"candidate_route"`
	Cases          []RouteComparisonCase     `json:"cases"`
	Assessment     RouteComparisonAssessment `json:"assessment"`
	EvidenceRefs   []string                  `json:"evidence_refs"`
	Warnings       []string                  `json:"warnings"`
}

// RouteComparisonCase is one PINNED case, with the runs each side produced on
// it. A case is keyed by the request id -- the same request must have been run
// on both sides for the comparison to be matched.
type RouteComparisonCase struct {
	CaseID        string               `json:"case_id"`
	Critical      bool                 `json:"critical"`
	IncumbentRuns []RouteComparisonRun `json:"incumbent_runs"`
	CandidateRuns []RouteComparisonRun `json:"candidate_runs"`
}

// RouteComparisonRun is one receipt-backed run of a route on a case.
type RouteComparisonRun struct {
	RouteOutcomeReviewID string `json:"route_outcome_review_id"`
	RouteEvidenceID      string `json:"route_evidence_id"`
	ReceiptID            string `json:"receipt_id"`
	ReceiptOutcome       string `json:"receipt_outcome"`
	ReceiptStateChanged  bool   `json:"receipt_state_changed"`
	TotalTokens          int    `json:"total_tokens"`
	CostMicros           int64  `json:"cost_micros"`
	LatencyMS            int64  `json:"latency_ms"`
	// Succeeded is the receipt-derived verdict: the route produced output AND
	// its receipt completed the work. Route metadata alone never sets it.
	Succeeded bool `json:"succeeded"`
}

// RouteComparisonAssessment is the verdict plus the reasons it is what it is.
type RouteComparisonAssessment struct {
	RecommendReplacement bool     `json:"recommend_replacement"`
	Class                string   `json:"class"`
	QualityBasis         string   `json:"quality_basis"`
	Rationale            string   `json:"rationale"`
	MatchedCases         int      `json:"matched_cases"`
	IncumbentCases       int      `json:"incumbent_cases"`
	CandidateCases       int      `json:"candidate_cases"`
	MinRepetitions       int      `json:"min_repetitions"`
	IncumbentSuccesses   int      `json:"incumbent_successes"`
	CandidateSuccesses   int      `json:"candidate_successes"`
	CandidateFailures    int      `json:"candidate_failures"`
	CriticalRegressions  int      `json:"critical_regressions"`
	IncumbentCostMicros  int64    `json:"incumbent_cost_micros"`
	CandidateCostMicros  int64    `json:"candidate_cost_micros"`
	IncumbentLatencyMS   int64    `json:"incumbent_latency_ms"`
	CandidateLatencyMS   int64    `json:"candidate_latency_ms"`
	BlockingReasons      []string `json:"blocking_reasons,omitempty"`
}

// RouteComparisonInput is what the caller hands the builder.
//
// The CASE identity is EXPLICIT, not inferred. A comparison is only meaningful
// when both sides ran the same pinned case, so the caller says which case
// each run belongs to. Inferring it from the route evidence would be exactly
// the mistake this slice exists to stop: the live ledger carries 143,931
// route rows with 143,931 DISTINCT request ids, so no two rows ever share a
// request id and an inferred population would silently compare different
// cases.
type RouteComparisonInput struct {
	DecisionID     string
	IncumbentRoute Target
	CandidateRoute Target
	Cases          []RouteComparisonCaseInput
	CriticalCases  []string
	MinRepetitions int
	CreatedAt      string
}

// RouteComparisonCaseInput is one pinned case, with the runs each side
// produced on it.
type RouteComparisonCaseInput struct {
	CaseID    string
	Incumbent []RouteOutcomeReview
	Candidate []RouteOutcomeReview
}

// routeComparisonReceiptOutcomes is Compound's receipt outcome vocabulary. A
// route's own status is NOT in it: generation success is not a receipt
// outcome.
var routeComparisonReceiptOutcomes = map[string]bool{
	"completed": true,
	"failed":    true,
	"blocked":   true,
	"no_op":     true,
	"cancelled": true,
}

// validRouteComparisonReceiptOutcome reports whether the outcome is one the
// comparison understands. An unknown outcome is a BLOCK, never a pass.
//
// The match is EXACT, like the rest of the repo's receipt handling: the
// receipt outcome vocabulary is lowercase and a differently-spelled outcome is
// a malformed receipt, not a synonym for the real one.
func validRouteComparisonReceiptOutcome(outcome string) bool {
	return routeComparisonReceiptOutcomes[strings.TrimSpace(outcome)]
}

// BuildRouteComparison assesses one route-change decision against the
// route outcomes collected for it.
//
// The rules it enforces, each from the S3 acceptance criteria:
//
//   - The two sides must have run the SAME cases (matched populations). An
//     unmatched side is not evidence about the route; it is evidence about a
//     different case set.
//   - Each case must have the declared minimum number of repetitions on BOTH
//     sides. One lucky run is not a comparison.
//   - Usage and cost must be MEASURED on every run. A missing price is not a
//     price of zero.
//   - A critical case that the incumbent completed and the candidate did not is
//     a regression, and a regression blocks.
//   - A run counts as a success only when its RECEIPT completed the work.
//     Generation success is insufficient.
//
// Every rule that blocks names itself. An operator reading the assessment
// sees exactly which evidence is missing, not a bare refusal.
func BuildRouteComparison(input RouteComparisonInput) RouteComparison {
	comparison := RouteComparison{
		SchemaVersion:  RouteComparisonSchemaVersion,
		DecisionID:     strings.TrimSpace(input.DecisionID),
		IncumbentRoute: input.IncumbentRoute,
		CandidateRoute: input.CandidateRoute,
		Warnings: []string{
			"does not change routing policy",
			"does not mutate gateway provider or model-router config",
			"does not claim task success",
			"does not authorize spend",
			"does not rank providers or models",
		},
	}

	critical := make(map[string]bool, len(input.CriticalCases))
	for _, caseID := range input.CriticalCases {
		if id := strings.TrimSpace(caseID); id != "" {
			critical[id] = true
		}
	}

	minRepetitions := input.MinRepetitions
	if minRepetitions < 1 {
		minRepetitions = 1
	}

	assessment := RouteComparisonAssessment{
		Class:          "comparison_unproven",
		QualityBasis:   "matched_receipt_backed_outcomes_only",
		MinRepetitions: minRepetitions,
	}

	blocking := make([]string, 0, 8)
	matched := 0

	for _, pinned := range input.Cases {
		caseID := strings.TrimSpace(pinned.CaseID)
		if caseID == "" {
			blocking = append(blocking, "a pinned case has no case id; the population cannot be matched")
			continue
		}
		incumbentRuns := comparisonRunsFromReviews(pinned.Incumbent)
		candidateRuns := comparisonRunsFromReviews(pinned.Candidate)
		isCritical := critical[caseID]
		if len(incumbentRuns) > 0 {
			assessment.IncumbentCases++
		}
		if len(candidateRuns) > 0 {
			assessment.CandidateCases++
		}

		comparison.Cases = append(comparison.Cases, RouteComparisonCase{
			CaseID:        caseID,
			Critical:      isCritical,
			IncumbentRuns: incumbentRuns,
			CandidateRuns: candidateRuns,
		})

		if len(incumbentRuns) == 0 {
			blocking = append(blocking, "case "+caseID+" ran only on the candidate route; the populations are not matched")
			continue
		}
		if len(candidateRuns) == 0 {
			blocking = append(blocking, "case "+caseID+" ran only on the incumbent route; the populations are not matched")
			continue
		}
		matched++

		if len(incumbentRuns) < minRepetitions || len(candidateRuns) < minRepetitions {
			blocking = append(blocking, "case "+caseID+" ran "+comparisonInt(len(incumbentRuns))+"/"+comparisonInt(len(candidateRuns))+" times against a declared minimum of "+comparisonInt(minRepetitions)+" repetitions")
		}

		incumbentSucceeded := comparisonRunsSucceeded(incumbentRuns)
		candidateSucceeded := comparisonRunsSucceeded(candidateRuns)
		if incumbentSucceeded {
			assessment.IncumbentSuccesses++
		}
		if candidateSucceeded {
			assessment.CandidateSuccesses++
		}
		if !candidateSucceeded {
			assessment.CandidateFailures++
		}

		for _, run := range append(append([]RouteComparisonRun{}, incumbentRuns...), candidateRuns...) {
			if run.ReceiptID == "" {
				blocking = append(blocking, "case "+caseID+" has a run with no receipt binding; route metadata alone is not evidence")
			}
			if !validRouteComparisonReceiptOutcome(run.ReceiptOutcome) {
				blocking = append(blocking, "case "+caseID+" has a run with an unknown receipt outcome "+comparisonQuoted(run.ReceiptOutcome))
			} else if run.ReceiptOutcome != "completed" {
				// A route that produced output but whose receipt did not
				// complete the work is not a success. This is the generation-
				// success-is-insufficient rule, and it is stated per run so
				// the operator can see WHICH receipt outcome decided it.
				blocking = append(blocking, "case "+caseID+" has a run whose receipt outcome is "+comparisonQuoted(run.ReceiptOutcome)+"; generation success without a completed receipt is not a successful route")
			}
			if run.TotalTokens <= 0 {
				blocking = append(blocking, "case "+caseID+" has a run with no measured usage; missing usage is not zero usage")
			}
			if run.CostMicros <= 0 {
				blocking = append(blocking, "case "+caseID+" has a run with no measured cost or price")
			}
		}

		if isCritical && incumbentSucceeded && !candidateSucceeded {
			assessment.CriticalRegressions++
			blocking = append(blocking, "critical case "+caseID+" completed on the incumbent route and did not complete on the candidate route")
		}
	}

	assessment.MatchedCases = matched
	assessment.BlockingReasons = uniqueSortedStrings(blocking)
	if len(assessment.BlockingReasons) == 0 {
		assessment.BlockingReasons = nil
	}

	for _, c := range comparison.Cases {
		for _, run := range c.IncumbentRuns {
			assessment.IncumbentCostMicros += run.CostMicros
			assessment.IncumbentLatencyMS += run.LatencyMS
		}
		for _, run := range c.CandidateRuns {
			assessment.CandidateCostMicros += run.CostMicros
			assessment.CandidateLatencyMS += run.LatencyMS
		}
	}

	switch {
	case len(comparison.Cases) == 0:
		assessment.Class = "comparison_empty"
		assessment.Rationale = "no route outcomes were supplied for this decision; there is nothing to compare"
	case len(assessment.BlockingReasons) > 0:
		assessment.Class = "comparison_blocked"
		assessment.Rationale = "the collected evidence cannot recommend a replacement: " + strings.Join(assessment.BlockingReasons, "; ")
	case assessment.CandidateFailures > 0:
		assessment.Class = "comparison_candidate_not_equivalent"
		assessment.Rationale = "the candidate route failed cases the incumbent route completed; a replacement is not supported by these outcomes"
	case assessment.CandidateSuccesses < assessment.IncumbentSuccesses:
		assessment.Class = "comparison_candidate_not_equivalent"
		assessment.Rationale = "the candidate route completed fewer cases than the incumbent route on the same pinned population"
	default:
		assessment.RecommendReplacement = true
		assessment.Class = "comparison_supports_replacement"
		assessment.Rationale = "the candidate route matched the incumbent route on every pinned case, with measured usage and cost on every run and no critical regression"
	}

	comparison.Assessment = assessment
	comparison.ID = routeComparisonID(input)
	comparison.EvidenceRefs = routeComparisonEvidenceRefs(comparison)
	comparison.CreatedAt = strings.TrimSpace(input.CreatedAt)
	return comparison
}

// comparisonRunsFromReviews converts the pinned case's reviews into runs.
//
// The CASE identity comes from the caller, not from the review: the review's
// request id identifies ONE gateway call, and the live ledger gives every
// row a distinct one, so deriving the case from it would compare different
// cases and call it a match.
func comparisonRunsFromReviews(reviews []RouteOutcomeReview) []RouteComparisonRun {
	runs := make([]RouteComparisonRun, 0, len(reviews))
	for _, review := range reviews {
		runs = append(runs, RouteComparisonRun{
			RouteOutcomeReviewID: review.ID,
			RouteEvidenceID:      review.RouteEvidenceID,
			ReceiptID:            review.ReceiptID,
			ReceiptOutcome:       review.ReceiptOutcome,
			ReceiptStateChanged:  review.ReceiptStateChanged,
			TotalTokens:          review.Usage.TotalTokens,
			CostMicros:           review.CostMicros,
			LatencyMS:            review.LatencyMS,
			Succeeded:            review.ReceiptOutcome == "completed" && review.ReceiptStateChanged,
		})
	}
	return runs
}

// comparisonRunsSucceeded reports whether the runs on a case show the route
// completing the work. It is receipt-derived: the route's own status never
// enters.
func comparisonRunsSucceeded(runs []RouteComparisonRun) bool {
	for _, run := range runs {
		if run.Succeeded {
			return true
		}
	}
	return false
}

// routeComparisonID derives a stable identity from the decision and the two
// routes, so the same decision compared twice is the same comparison.
func routeComparisonID(input RouteComparisonInput) string {
	parts := []string{
		strings.TrimSpace(input.DecisionID),
		input.IncumbentRoute.Provider + "/" + input.IncumbentRoute.Model + "/" + input.IncumbentRoute.Protocol,
		input.CandidateRoute.Provider + "/" + input.CandidateRoute.Model + "/" + input.CandidateRoute.Protocol,
	}
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "rcmp_" + hex.EncodeToString(hash[:])[:12]
}

// routeComparisonEvidenceRefs cites the route evidence and receipts the
// comparison rests on.
func routeComparisonEvidenceRefs(comparison RouteComparison) []string {
	refs := make([]string, 0, 16)
	for _, c := range comparison.Cases {
		for _, run := range append(append([]RouteComparisonRun{}, c.IncumbentRuns...), c.CandidateRuns...) {
			if run.RouteEvidenceID != "" {
				refs = append(refs, run.RouteEvidenceID)
			}
			if run.ReceiptID != "" {
				refs = append(refs, run.ReceiptID)
			}
		}
	}
	return uniqueSortedStrings(refs)
}

// uniqueSortedStrings removes duplicates and sorts, so the evidence list is
// stable across runs.
func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// comparisonInt renders an integer for a blocking reason, so the comparison
// does not pull fmt into a package that avoids it.
func comparisonInt(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 12)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// comparisonQuoted quotes an outcome for a blocking reason.
func comparisonQuoted(value string) string {
	return "\"" + strings.TrimSpace(value) + "\""
}
