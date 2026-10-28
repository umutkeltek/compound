package routeevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RouteComparisonFile is the on-disk shape of a comparison INPUT: the two
// sides' route outcome reviews, the routes they came from, and the pinned
// case policy the operator declared.
//
// It is an input, not an authority record. The comparison it feeds is
// evidence-only and never changes routing policy.
type RouteComparisonFile struct {
	SchemaVersion  string                    `json:"schema_version"`
	DecisionID     string                    `json:"decision_id"`
	IncumbentRoute Target                    `json:"incumbent_route"`
	CandidateRoute Target                    `json:"candidate_route"`
	Cases          []RouteComparisonCaseFile `json:"cases"`
	CriticalCases  []string                  `json:"critical_cases,omitempty"`
	MinRepetitions int                       `json:"min_repetitions,omitempty"`
}

// RouteComparisonCaseFile is one PINNED case. The case id is declared by the
// operator, because the route evidence's own request id identifies a single
// gateway call and never repeats -- two runs of the same case carry different
// request ids, so an inferred population would compare different cases.
type RouteComparisonCaseFile struct {
	CaseID    string               `json:"case_id"`
	Incumbent []RouteOutcomeReview `json:"incumbent"`
	Candidate []RouteOutcomeReview `json:"candidate"`
}

// RouteComparisonInputSchemaVersion is the accepted input schema.
const RouteComparisonInputSchemaVersion = "compound.route_comparison_input.v0.1"

// LoadRouteComparisonInput reads a comparison input file.
//
// It validates the SHAPE only: the assessment decides whether the evidence
// can recommend a replacement. A missing measurement or an unmatched
// population is a BLOCKING REASON, not a malformed file -- the operator must
// see which evidence is missing, not a parse error that hides it.
func LoadRouteComparisonInput(path string) (RouteComparisonInput, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return RouteComparisonInput{}, fmt.Errorf("route comparison input path required")
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return RouteComparisonInput{}, fmt.Errorf("read route comparison input: %w", err)
	}
	var file RouteComparisonFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return RouteComparisonInput{}, fmt.Errorf("parse route comparison input: %w", err)
	}
	if strings.TrimSpace(file.SchemaVersion) != RouteComparisonInputSchemaVersion {
		return RouteComparisonInput{}, fmt.Errorf("route comparison input schema_version = %q, want %q", file.SchemaVersion, RouteComparisonInputSchemaVersion)
	}
	if strings.TrimSpace(file.DecisionID) == "" {
		return RouteComparisonInput{}, fmt.Errorf("route comparison input decision_id required")
	}
	if strings.TrimSpace(file.IncumbentRoute.Model) == "" || strings.TrimSpace(file.CandidateRoute.Model) == "" {
		return RouteComparisonInput{}, fmt.Errorf("route comparison input requires both incumbent_route and candidate_route")
	}
	if file.IncumbentRoute.Model == file.CandidateRoute.Model &&
		file.IncumbentRoute.Provider == file.CandidateRoute.Provider &&
		file.IncumbentRoute.Protocol == file.CandidateRoute.Protocol {
		return RouteComparisonInput{}, fmt.Errorf("route comparison input incumbent and candidate routes are identical; there is no replacement to assess")
	}
	cases := make([]RouteComparisonCaseInput, 0, len(file.Cases))
	for _, pinned := range file.Cases {
		cases = append(cases, RouteComparisonCaseInput{
			CaseID:    pinned.CaseID,
			Incumbent: pinned.Incumbent,
			Candidate: pinned.Candidate,
		})
	}
	return RouteComparisonInput{
		DecisionID:     file.DecisionID,
		IncumbentRoute: file.IncumbentRoute,
		CandidateRoute: file.CandidateRoute,
		Cases:          cases,
		CriticalCases:  file.CriticalCases,
		MinRepetitions: file.MinRepetitions,
		CreatedAt:      "",
	}, nil
}

// RouteComparisonOutputPath is where a recorded comparison lands.
func RouteComparisonOutputPath(homePath, comparisonID string) string {
	return filepath.Join(homePath, "reviews", "route-comparisons", comparisonID+".json")
}

// RecordRouteComparison writes the comparison artifact under the compound
// home. It records EVIDENCE: the comparison and the reasons it reached its
// verdict. It never touches routing policy, a gateway, a provider, or a
// model-router config.
func RecordRouteComparison(homePath string, comparison RouteComparison) (string, error) {
	if strings.TrimSpace(homePath) == "" {
		return "", fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(comparison.ID) == "" {
		return "", fmt.Errorf("route comparison id required")
	}
	path := RouteComparisonOutputPath(homePath, comparison.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir route comparison dir: %w", err)
	}
	body, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal route comparison: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("write route comparison: %w", err)
	}
	return path, nil
}
