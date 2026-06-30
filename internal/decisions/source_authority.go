package decisions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DecisionSourceAuthoritySchemaVersion = "compound.decision_source_authority.v0.1"

// SourceAuthorityRecord records the handoff from a legacy decision source to a
// Compound decision ref. The source remains evidence/writeback material; it is
// no longer the active context authority for this decision slice.
type SourceAuthorityRecord struct {
	SchemaVersion       string   `json:"schema_version"`
	ID                  string   `json:"id"`
	CreatedAt           string   `json:"created_at"`
	SourcePath          string   `json:"source_path"`
	SourceKind          string   `json:"source_kind"`
	SourceRole          string   `json:"source_role"`
	ImportedVia         string   `json:"imported_via"`
	AuthorityRef        string   `json:"authority_ref"`
	AuthorityStatus     string   `json:"authority_status"`
	DecisionID          string   `json:"decision_id"`
	EvidenceID          string   `json:"evidence_id"`
	CandidateID         string   `json:"candidate_id"`
	ClaimID             string   `json:"claim_id,omitempty"`
	Retires             string   `json:"retires"`
	RetirementCondition string   `json:"retirement_condition"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

func RecordSourceAuthority(homePath string, report DecisionImportReport, importedVia string, now time.Time) (SourceAuthorityRecord, error) {
	if strings.TrimSpace(homePath) == "" {
		return SourceAuthorityRecord{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(report.SourcePath) == "" {
		return SourceAuthorityRecord{}, fmt.Errorf("source path required")
	}
	decisionID := strings.TrimSpace(report.Lifecycle.ID)
	if decisionID == "" {
		decisionID = report.CandidateID
	}
	rec := SourceAuthorityRecord{
		SchemaVersion:       DecisionSourceAuthoritySchemaVersion,
		ID:                  decisionSourceAuthorityID(report.SourcePath, report.EvidenceID, report.ClaimID, importedVia),
		CreatedAt:           now.UTC().Format(time.RFC3339),
		SourcePath:          report.SourcePath,
		SourceKind:          firstNonEmpty(report.SourceKind, "decision_source"),
		SourceRole:          "source_in_place_evidence",
		ImportedVia:         firstNonEmpty(importedVia, "decisions import"),
		AuthorityRef:        "decision://" + decisionID,
		AuthorityStatus:     "compound_primary",
		DecisionID:          decisionID,
		EvidenceID:          report.EvidenceID,
		CandidateID:         report.CandidateID,
		ClaimID:             report.ClaimID,
		Retires:             "decision_source_authority:" + report.SourcePath,
		RetirementCondition: "future agents consume the decision via Compound ContextPack; source updates go through reviewed writeback/import",
		EvidenceRefs:        cleanRefs([]string{report.EvidenceID, report.CandidateID, report.ClaimID}),
	}
	if err := appendSourceAuthority(homePath, rec); err != nil {
		return SourceAuthorityRecord{}, err
	}
	return rec, nil
}

func LoadSourceAuthority(homePath string) ([]SourceAuthorityRecord, error) {
	path := decisionSourceAuthorityLog(homePath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []SourceAuthorityRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	byID := map[string]SourceAuthorityRecord{}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec SourceAuthorityRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, err
		}
		if rec.ID == "" {
			return nil, fmt.Errorf("decision source authority record missing id")
		}
		if _, ok := byID[rec.ID]; !ok {
			order = append(order, rec.ID)
		}
		byID[rec.ID] = rec
	}
	out := make([]SourceAuthorityRecord, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

func appendSourceAuthority(homePath string, rec SourceAuthorityRecord) error {
	if err := os.MkdirAll(filepath.Join(homePath, "registries"), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(decisionSourceAuthorityLog(homePath), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func decisionSourceAuthorityLog(homePath string) string {
	return filepath.Join(homePath, "registries", "decision-source-authority.jsonl")
}

func decisionSourceAuthorityID(path, evidenceID, claimID, importedVia string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{path, evidenceID, claimID, importedVia}, "|")))
	return "decsrc_" + hex.EncodeToString(sum[:])[:12]
}

func cleanRefs(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
