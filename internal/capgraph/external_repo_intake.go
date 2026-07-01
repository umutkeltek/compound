package capgraph

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ExternalRepoIntakeReportSchemaVersion = "compound.capability.external_repo_intake_report.v0.1"
	ExternalRepoIntakeAuthorityRef        = "compound://reviews/external-repo-intake"
)

type ExternalRepoIntakeReport struct {
	SchemaVersion         string                       `json:"schema_version"`
	AuthorityRef          string                       `json:"authority_ref"`
	Status                string                       `json:"status"`
	BatchCount            int                          `json:"batch_count"`
	LatestBatchID         string                       `json:"latest_batch_id,omitempty"`
	LatestSummaryPath     string                       `json:"latest_summary_path,omitempty"`
	MapPath               string                       `json:"map_path,omitempty"`
	SourceObservationPath string                       `json:"source_observation_path,omitempty"`
	CandidateCount        int                          `json:"candidate_count,omitempty"`
	CountsByDecision      map[string]int               `json:"counts_by_decision,omitempty"`
	CountsByLane          map[string]int               `json:"counts_by_lane,omitempty"`
	OpenReviewCount       int                          `json:"open_review_count,omitempty"`
	MergeExistingCount    int                          `json:"merge_existing_count,omitempty"`
	ReferenceOnlyCount    int                          `json:"reference_only_count,omitempty"`
	SourceObservationID   string                       `json:"source_observation_id,omitempty"`
	SourceCandidateIDs    []string                     `json:"source_candidate_ids,omitempty"`
	NoPromoteIDs          []string                     `json:"no_promote_ids,omitempty"`
	RepoQuery             string                       `json:"repo_query,omitempty"`
	Matches               []ExternalRepoIntakeDecision `json:"matches,omitempty"`
	HighValue             []ExternalRepoIntakeDecision `json:"high_value,omitempty"`
	Recent                []ExternalRepoIntakeDecision `json:"recent,omitempty"`
	Boundaries            []string                     `json:"boundaries"`
}

type ExternalRepoIntakeDecision struct {
	Repo             string  `json:"repo"`
	Decision         string  `json:"decision"`
	Lane             string  `json:"lane,omitempty"`
	Status           string  `json:"status,omitempty"`
	Owner            string  `json:"owner,omitempty"`
	NextReviewStep   string  `json:"next_review_step,omitempty"`
	NotToDo          string  `json:"not_to_do,omitempty"`
	GraphHint        string  `json:"graph_hint,omitempty"`
	DuplicateRisk    float64 `json:"duplicate_risk,omitempty"`
	EvidenceStrength float64 `json:"evidence_strength,omitempty"`
	ReviewID         string  `json:"review_id,omitempty"`
	ReviewPath       string  `json:"review_path,omitempty"`
}

type externalRepoIntakeRunSummary struct {
	SchemaVersion         string                       `json:"schema_version"`
	ID                    string                       `json:"id"`
	CreatedAt             string                       `json:"created_at"`
	MapPath               string                       `json:"map_path"`
	SourceObservationPath string                       `json:"source_observation_path"`
	CandidateCount        int                          `json:"candidate_count"`
	SourceObserveOutput   []string                     `json:"source_observe_output"`
	Decisions             []ExternalRepoIntakeDecision `json:"decisions"`
	CountsByDecision      map[string]int               `json:"counts_by_decision"`
	CountsByLane          map[string]int               `json:"counts_by_lane"`
}

type externalRepoIntakeMap struct {
	Boundaries []string                    `json:"boundaries"`
	Items      []externalRepoIntakeMapItem `json:"items"`
}

type externalRepoIntakeMapItem struct {
	Repo           string `json:"repo"`
	Owner          string `json:"owner"`
	NextReviewStep string `json:"next_review_step"`
	NotToDo        string `json:"not_to_do"`
}

type externalRepoIntakeSummaryFile struct {
	path    string
	summary externalRepoIntakeRunSummary
	created time.Time
}

func ExternalRepoIntakeReportForHome(homePath string, repoQuery ...string) (ExternalRepoIntakeReport, error) {
	report := ExternalRepoIntakeReport{
		SchemaVersion: ExternalRepoIntakeReportSchemaVersion,
		AuthorityRef:  ExternalRepoIntakeAuthorityRef,
		Status:        "missing",
		Boundaries: []string{
			"review-only",
			"no clone/install/serve",
			"no plugin execution or MCP auth",
			"no CapabilityGraph mutation",
			"no skill inbox candidate creation unless a later create_candidate decision is recorded",
			"no memory promotion",
		},
	}
	if strings.TrimSpace(homePath) == "" {
		return report, fmt.Errorf("COMPOUND_HOME required")
	}
	root := filepath.Join(homePath, "inbox", "external-repo-intake")
	var summaries []externalRepoIntakeSummaryFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) != "run-summary.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read external repo intake summary %s: %w", path, err)
		}
		var summary externalRepoIntakeRunSummary
		if err := json.Unmarshal(data, &summary); err != nil {
			return fmt.Errorf("parse external repo intake summary %s: %w", path, err)
		}
		created, _ := time.Parse(time.RFC3339, summary.CreatedAt)
		if created.IsZero() {
			if info, statErr := d.Info(); statErr == nil {
				created = info.ModTime()
			}
		}
		summaries = append(summaries, externalRepoIntakeSummaryFile{path: path, summary: summary, created: created})
		return nil
	})
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	if len(summaries) == 0 {
		return report, nil
	}
	sort.Slice(summaries, func(i, j int) bool {
		if !summaries[i].created.Equal(summaries[j].created) {
			return summaries[i].created.After(summaries[j].created)
		}
		return summaries[i].path > summaries[j].path
	})
	latest := summaries[0].summary
	report.Status = "ok"
	report.BatchCount = len(summaries)
	report.LatestBatchID = latest.ID
	report.LatestSummaryPath = summaries[0].path
	report.MapPath = latest.MapPath
	report.SourceObservationPath = latest.SourceObservationPath
	report.CandidateCount = latest.CandidateCount
	report.CountsByDecision = copyIntMap(latest.CountsByDecision)
	report.CountsByLane = copyIntMap(latest.CountsByLane)
	report.OpenReviewCount = latest.CountsByDecision["keep_needs_review"]
	report.MergeExistingCount = latest.CountsByDecision["merge_existing"]
	report.ReferenceOnlyCount = latest.CountsByDecision["reference_only"]
	report.SourceObservationID, report.SourceCandidateIDs, report.NoPromoteIDs = parseExternalRepoSourceObserve(latest.SourceObserveOutput)

	metadata := loadExternalRepoIntakeMap(latest.MapPath)
	decisions := enrichExternalRepoDecisions(latest.Decisions, metadata)
	query := ""
	if len(repoQuery) > 0 {
		query = strings.TrimSpace(repoQuery[0])
	}
	if query != "" {
		report.RepoQuery = query
		report.Matches = matchExternalRepoDecisions(decisions, query)
	}
	report.HighValue = filterExternalRepoDecisions(decisions, "keep_needs_review", 12)
	report.Recent = limitExternalRepoDecisions(decisions, 12)
	if len(metadata.Boundaries) > 0 {
		report.Boundaries = metadata.Boundaries
	}
	return report, nil
}

func parseExternalRepoSourceObserve(lines []string) (string, []string, []string) {
	var sourceObservation string
	var candidates []string
	var noPromote []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		switch {
		case strings.HasPrefix(trimmed, "source observation "):
			sourceObservation = strings.TrimSpace(strings.TrimPrefix(trimmed, "source observation "))
		case strings.HasPrefix(trimmed, "source_candidate:"):
			candidates = append(candidates, strings.TrimSpace(strings.TrimPrefix(trimmed, "source_candidate:")))
		case strings.HasPrefix(trimmed, "source_no_promote:"):
			noPromote = append(noPromote, strings.TrimSpace(strings.TrimPrefix(trimmed, "source_no_promote:")))
		}
	}
	return sourceObservation, candidates, noPromote
}

func loadExternalRepoIntakeMap(path string) externalRepoIntakeMap {
	if strings.TrimSpace(path) == "" {
		return externalRepoIntakeMap{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return externalRepoIntakeMap{}
	}
	var m externalRepoIntakeMap
	if err := json.Unmarshal(data, &m); err != nil {
		return externalRepoIntakeMap{}
	}
	return m
}

func enrichExternalRepoDecisions(decisions []ExternalRepoIntakeDecision, metadata externalRepoIntakeMap) []ExternalRepoIntakeDecision {
	byRepo := map[string]externalRepoIntakeMapItem{}
	for _, item := range metadata.Items {
		byRepo[strings.ToLower(item.Repo)] = item
	}
	out := make([]ExternalRepoIntakeDecision, 0, len(decisions))
	for _, decision := range decisions {
		if item, ok := byRepo[strings.ToLower(decision.Repo)]; ok {
			decision.Owner = item.Owner
			decision.NextReviewStep = item.NextReviewStep
			decision.NotToDo = item.NotToDo
		}
		out = append(out, decision)
	}
	return out
}

func matchExternalRepoDecisions(decisions []ExternalRepoIntakeDecision, query string) []ExternalRepoIntakeDecision {
	needle := normalizeExternalRepoQuery(query)
	if needle == "" {
		return nil
	}
	var out []ExternalRepoIntakeDecision
	for _, decision := range decisions {
		haystack := normalizeExternalRepoQuery(decision.Repo)
		if haystack == needle || strings.Contains(haystack, needle) || strings.Contains(needle, haystack) {
			out = append(out, decision)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Lane < out[j].Lane
	})
	return out
}

func normalizeExternalRepoQuery(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.TrimSuffix(v, ".git")
	for _, prefix := range []string{
		"https://github.com/",
		"http://github.com/",
		"github.com/",
	} {
		v = strings.TrimPrefix(v, prefix)
	}
	return strings.Trim(v, "/")
}

func filterExternalRepoDecisions(decisions []ExternalRepoIntakeDecision, decision string, limit int) []ExternalRepoIntakeDecision {
	var out []ExternalRepoIntakeDecision
	for _, row := range decisions {
		if row.Decision == decision {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EvidenceStrength != out[j].EvidenceStrength {
			return out[i].EvidenceStrength > out[j].EvidenceStrength
		}
		return out[i].Repo < out[j].Repo
	})
	return limitExternalRepoDecisions(out, limit)
}

func limitExternalRepoDecisions(decisions []ExternalRepoIntakeDecision, limit int) []ExternalRepoIntakeDecision {
	if limit <= 0 || len(decisions) <= limit {
		return decisions
	}
	return decisions[:limit]
}

func copyIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
