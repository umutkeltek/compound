package sourceobs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
)

const SourceObservationListSchemaVersion = "compound.source_observations_projection.v0.1"

type SourceObservationListReport struct {
	SchemaVersion string                      `json:"schema_version"`
	GeneratedAt   string                      `json:"generated_at"`
	AuthorityRef  string                      `json:"authority_ref"`
	Counts        SourceObservationListCounts `json:"counts"`
	Items         []SourceObservationListItem `json:"items"`
	Warnings      []string                    `json:"warnings,omitempty"`
}

type SourceObservationListCounts struct {
	TotalCandidates    int `json:"total_candidates"`
	SourceObservations int `json:"source_observations"`
	NoPromoteDecisions int `json:"no_promote_decisions"`
	PromoteAllowed     int `json:"promote_allowed"`
	PromoteDisallowed  int `json:"promote_disallowed"`
}

type SourceObservationListItem struct {
	CandidateID         string   `json:"candidate_id"`
	Kind                string   `json:"kind"`
	Subject             string   `json:"subject,omitempty"`
	Statement           string   `json:"statement,omitempty"`
	SourceObservationID string   `json:"source_observation_id"`
	SourceRef           string   `json:"source_ref,omitempty"`
	Verdict             string   `json:"verdict,omitempty"`
	PromoteAllowed      bool     `json:"promote_allowed"`
	CreatedAt           string   `json:"created_at,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs,omitempty"`
	NoPromoteID         string   `json:"no_promote_id,omitempty"`
}

func ListSourceObservations(homePath string, now time.Time) (SourceObservationListReport, error) {
	if now.IsZero() {
		now = time.Now()
	}
	report := SourceObservationListReport{
		SchemaVersion: SourceObservationListSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		AuthorityRef:  "compound://source/observations",
		Items:         []SourceObservationListItem{},
	}
	noPromote, err := sourceObservationNoPromoteIndex(homePath)
	if err != nil {
		return report, err
	}
	report.Counts.NoPromoteDecisions = len(noPromote)
	for _, kind := range candidates.AllKinds() {
		dir := filepath.Join(homePath, candidates.InboxDir(kind))
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return report, err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			var rec CandidateRecord
			data, err := os.ReadFile(path)
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s unreadable: %v", entry.Name(), err))
				continue
			}
			if err := json.Unmarshal(data, &rec); err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s malformed: %v", entry.Name(), err))
				continue
			}
			if strings.TrimSpace(rec.SourceObservationID) == "" {
				continue
			}
			report.Counts.TotalCandidates++
			item := SourceObservationListItem{
				CandidateID:         firstNonEmpty(rec.ID, strings.TrimSuffix(entry.Name(), ".json")),
				Kind:                firstNonEmpty(rec.Kind, string(kind)),
				Subject:             rec.Subject,
				Statement:           rec.Statement,
				SourceObservationID: rec.SourceObservationID,
				SourceRef:           rec.SourceRef,
				Verdict:             rec.Verdict,
				PromoteAllowed:      rec.PromoteAllowed,
				CreatedAt:           rec.CreatedAt,
				EvidenceRefs:        append([]string(nil), rec.EvidenceRefs...),
				NoPromoteID:         noPromote[firstNonEmpty(rec.ID, strings.TrimSuffix(entry.Name(), ".json"))],
			}
			if item.PromoteAllowed {
				report.Counts.PromoteAllowed++
			} else {
				report.Counts.PromoteDisallowed++
			}
			report.Items = append(report.Items, item)
		}
	}
	report.Counts.SourceObservations = len(report.Items)
	sort.SliceStable(report.Items, func(i, j int) bool {
		if report.Items[i].CreatedAt != report.Items[j].CreatedAt {
			return report.Items[i].CreatedAt > report.Items[j].CreatedAt
		}
		return report.Items[i].CandidateID < report.Items[j].CandidateID
	})
	return report, nil
}

func sourceObservationNoPromoteIndex(homePath string) (map[string]string, error) {
	out := map[string]string{}
	path := filepath.Join(homePath, "ledger", "no-promote-decisions.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row struct {
			ID           string `json:"id"`
			CandidateRef string `json:"candidate_ref"`
			OwnerTarget  struct {
				Kind string `json:"kind"`
			} `json:"owner_target"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		if row.OwnerTarget.Kind == "source_observation" && row.CandidateRef != "" && row.ID != "" {
			out[row.CandidateRef] = row.ID
		}
	}
	return out, scanner.Err()
}
