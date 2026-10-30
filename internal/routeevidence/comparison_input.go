package routeevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	homepkg "compound/internal/home"
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

// RouteComparisonRevisionPath is where one recorded revision lands. The
// comparison id is the stable identity of the decision and its two routes; the
// file name is the SHA-256 of the recorded bytes, so a revision is never
// replaced by different evidence.
func RouteComparisonRevisionPath(homePath, comparisonID, digest string) string {
	return filepath.Join(homePath, "reviews", "route-comparisons", comparisonID, "revisions", "sha256-"+digest+".json")
}

// RouteComparisonLatestPath is a rebuildable view: a byte copy of the revision
// recorded most recently, so its SHA-256 names that revision. The revisions are
// the evidence; this view may be rewritten.
func RouteComparisonLatestPath(homePath, comparisonID string) string {
	return filepath.Join(homePath, "reviews", "route-comparisons", comparisonID, "latest.json")
}

// RecordRouteComparison records the comparison as an immutable revision under
// the compound home, then refreshes the latest view, and returns the revision
// path. It records EVIDENCE: the comparison and the reasons it reached its
// verdict. It never touches routing policy, a gateway, a provider, or a
// model-router config.
//
// Only an already existing revision file counts as a replay, and only when its
// bytes equal the new revision. Every other write, sync or path error is a
// failure, even when identical bytes are readable at the path.
func RecordRouteComparison(homePath string, comparison RouteComparison) (string, error) {
	if strings.TrimSpace(homePath) == "" {
		return "", fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(comparison.ID) == "" {
		return "", fmt.Errorf("route comparison id required")
	}
	body, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal route comparison: %w", err)
	}
	digest := sha256.Sum256(body)
	path := RouteComparisonRevisionPath(homePath, comparison.ID, hex.EncodeToString(digest[:]))
	const kind = "route comparison revision"

	existing, err := homepkg.ReadRootedRegularFile(homePath, path, kind)
	switch {
	case err == nil:
		if !bytes.Equal(existing, body) {
			return "", fmt.Errorf("route comparison revision %s exists with different content", path)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := homepkg.WriteRootedNewFileDurable(homePath, path, kind, body, 0o644); err != nil {
			return "", fmt.Errorf("write route comparison revision: %w", err)
		}
	default:
		return "", fmt.Errorf("read route comparison revision: %w", err)
	}

	if err := homepkg.WriteFileAtomic(RouteComparisonLatestPath(homePath, comparison.ID), body, 0o644); err != nil {
		return "", fmt.Errorf("route comparison revision %s recorded; write latest view: %w", path, err)
	}
	return path, nil
}
