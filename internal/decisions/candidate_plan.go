package decisions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/reviewartifact"
)

const DecisionCandidatePlanSchemaVersion = "compound.decision_candidate_plan.v0.1"

// DecisionCandidatePlan is a reviewed DecisionLifecycle source proposal from a
// distiller decision candidate. It never mutates decision source files and never
// promotes memory truth.
type DecisionCandidatePlan struct {
	SchemaVersion    string                 `json:"schema_version"`
	ID               string                 `json:"id"`
	CreatedAt        string                 `json:"created_at"`
	Status           string                 `json:"status"`
	CandidateID      string                 `json:"candidate_id"`
	CandidateVerdict string                 `json:"candidate_verdict,omitempty"`
	DecisionKey      string                 `json:"decision_key"`
	Subject          string                 `json:"subject,omitempty"`
	Statement        string                 `json:"statement,omitempty"`
	Detail           string                 `json:"detail,omitempty"`
	Entity           string                 `json:"entity,omitempty"`
	Predicate        string                 `json:"predicate,omitempty"`
	Object           string                 `json:"object,omitempty"`
	Confidence       float64                `json:"confidence,omitempty"`
	Qualifiers       map[string]string      `json:"qualifiers,omitempty"`
	EvidenceRefs     []string               `json:"evidence_refs"`
	TargetPath       string                 `json:"target_path,omitempty"`
	TargetExists     bool                   `json:"target_exists"`
	TargetHash       string                 `json:"target_hash,omitempty"`
	LifecycleDraft   DecisionLifecycleDraft `json:"lifecycle_draft"`
	Warnings         []string               `json:"warnings,omitempty"`
}

type DecisionLifecycleDraft struct {
	DecisionID       string   `json:"decision_id"`
	EntityID         string   `json:"entity_id"`
	Title            string   `json:"title"`
	Kind             string   `json:"kind"`
	Status           string   `json:"status"`
	Choice           string   `json:"choice"`
	Rationale        string   `json:"rationale"`
	Falsifier        string   `json:"falsifier"`
	ReviewTrigger    string   `json:"review_trigger"`
	Owner            string   `json:"owner,omitempty"`
	Scope            string   `json:"scope,omitempty"`
	EffectiveFrom    string   `json:"effective_from,omitempty"`
	Supersedes       []string `json:"supersedes,omitempty"`
	ReplacedBy       string   `json:"replaced_by,omitempty"`
	RequiredEvidence []string `json:"required_evidence"`
	ReviewNote       string   `json:"review_note"`
}

type DecisionCandidatePlanPaths struct {
	PlanPath   string
	ReviewPath string
}

type decisionCandidateRecord struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Entity       string            `json:"entity,omitempty"`
	Predicate    string            `json:"predicate,omitempty"`
	Object       string            `json:"object,omitempty"`
	Subject      string            `json:"subject,omitempty"`
	Statement    string            `json:"statement,omitempty"`
	Detail       string            `json:"detail,omitempty"`
	Confidence   float64           `json:"confidence,omitempty"`
	Qualifiers   map[string]string `json:"qualifiers,omitempty"`
	EvidenceRefs []string          `json:"evidence_refs,omitempty"`
	Verdict      string            `json:"verdict,omitempty"`
}

func BuildDecisionCandidatePlan(homePath, candidateID, targetPath string, now time.Time) (DecisionCandidatePlan, error) {
	homePath = strings.TrimSpace(homePath)
	candidateID = strings.TrimSpace(candidateID)
	targetPath = strings.TrimSpace(targetPath)
	if homePath == "" {
		return DecisionCandidatePlan{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if candidateID == "" {
		return DecisionCandidatePlan{}, fmt.Errorf("candidate id required")
	}
	c, err := readDecisionCandidate(homePath, candidateID)
	if err != nil {
		return DecisionCandidatePlan{}, err
	}
	if c.Kind != string(candidates.Decision) {
		return DecisionCandidatePlan{}, fmt.Errorf("candidate %s kind=%q, want %q", candidateID, c.Kind, candidates.Decision)
	}
	if c.Verdict != candidates.Park {
		return DecisionCandidatePlan{}, fmt.Errorf("candidate %s verdict=%q, want park", candidateID, c.Verdict)
	}
	targetExists, targetHash, err := reviewartifact.HashTarget(targetPath)
	if err != nil {
		return DecisionCandidatePlan{}, err
	}
	evidenceRefs := append([]string(nil), c.EvidenceRefs...)
	sort.Strings(evidenceRefs)
	qualifiers := copyStringMap(c.Qualifiers)
	key := decisionCandidateKey(c)
	choice := reviewartifact.FirstNonEmpty(c.Statement, c.Object, c.Detail, c.Subject, "review decision candidate")
	title := reviewartifact.FirstNonEmpty(c.Subject, c.Statement, c.Object, key)
	status := decisionCandidateStatus(qualifiers)
	kind := reviewartifact.FirstNonEmpty(qualifiers["kind"], KindOperational)
	falsifier := reviewartifact.FirstNonEmpty(qualifiers["falsifier"], "review required: falsifier before acceptance")
	reviewTrigger := reviewartifact.FirstNonEmpty(qualifiers["review_trigger"], "review required: trigger before acceptance")
	rationale := reviewartifact.FirstNonEmpty(c.Detail, c.Statement, "review candidate evidence and owner context")
	decisionID := reviewartifact.FirstNonEmpty(qualifiers["decision_id"], key)
	if strings.HasPrefix(decisionID, "decision:") {
		decisionID = strings.TrimPrefix(decisionID, "decision:")
	}
	warnings := []string{
		"does not mutate decision source files",
		"does not promote memory truth",
		"does not apply policy or doctrine",
	}
	if len(evidenceRefs) == 0 {
		warnings = append(warnings, "candidate has no evidence refs; reviewer must attach source evidence before acceptance")
	}
	if falsifier == "review required: falsifier before acceptance" || reviewTrigger == "review required: trigger before acceptance" {
		warnings = append(warnings, "accepted decisions require falsifier and review_trigger before promotion")
	}
	plan := DecisionCandidatePlan{
		SchemaVersion:    DecisionCandidatePlanSchemaVersion,
		ID:               reviewartifact.ID("dcp_", candidateID, targetPath),
		CreatedAt:        now.UTC().Format(time.RFC3339),
		Status:           "review_required",
		CandidateID:      candidateID,
		CandidateVerdict: c.Verdict,
		DecisionKey:      key,
		Subject:          c.Subject,
		Statement:        c.Statement,
		Detail:           c.Detail,
		Entity:           c.Entity,
		Predicate:        c.Predicate,
		Object:           c.Object,
		Confidence:       c.Confidence,
		Qualifiers:       qualifiers,
		EvidenceRefs:     evidenceRefs,
		TargetPath:       targetPath,
		TargetExists:     targetExists,
		TargetHash:       targetHash,
		LifecycleDraft: DecisionLifecycleDraft{
			DecisionID:    decisionID,
			EntityID:      decisionEntity(decisionID),
			Title:         title,
			Kind:          kind,
			Status:        status,
			Choice:        choice,
			Rationale:     rationale,
			Falsifier:     falsifier,
			ReviewTrigger: reviewTrigger,
			Owner:         qualifiers["owner"],
			Scope:         qualifiers["scope"],
			EffectiveFrom: reviewartifact.FirstNonEmpty(qualifiers["effective_from"], qualifiers["effective_date"]),
			Supersedes:    splitCSV(qualifiers["supersedes"]),
			ReplacedBy:    qualifiers["replaced_by"],
			RequiredEvidence: []string{
				"source trace evidence supporting the decision candidate",
				"owner/source file that will hold the reviewed decision",
				"falsifier and review_trigger for any accepted decision",
				"explicit reviewer approval before source mutation or promotion",
			},
			ReviewNote: "This is a DecisionLifecycle draft from a decision candidate. Reviewers must decide whether to create or update an owned decision source before any import/promotion.",
		},
		Warnings: warnings,
	}
	return plan, nil
}

func readDecisionCandidate(homePath, candidateID string) (decisionCandidateRecord, error) {
	var c decisionCandidateRecord
	path := filepath.Join(homePath, candidates.InboxDir(candidates.Decision), candidateID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.ID == "" {
		c.ID = candidateID
	}
	if c.ID != candidateID {
		return c, fmt.Errorf("candidate file id %q does not match requested %q", c.ID, candidateID)
	}
	if c.Kind == "" {
		c.Kind = string(candidates.Decision)
	}
	return c, nil
}

func RecordDecisionCandidatePlan(homePath string, plan DecisionCandidatePlan) (DecisionCandidatePlanPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return DecisionCandidatePlanPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if plan.ID == "" {
		return DecisionCandidatePlanPaths{}, fmt.Errorf("decision candidate plan id required")
	}
	paths, err := reviewartifact.Write(homePath, "decision-candidates", plan.ID, plan, " ", RenderDecisionCandidatePlanReview(plan))
	return DecisionCandidatePlanPaths{PlanPath: paths.PlanPath, ReviewPath: paths.ReviewPath}, err
}

func RenderDecisionCandidatePlanReview(plan DecisionCandidatePlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Decision Candidate Review Plan: %s\n\n", plan.CandidateID)
	fmt.Fprintf(&b, "- status: %s\n", plan.Status)
	fmt.Fprintf(&b, "- candidate: %s\n", plan.CandidateID)
	if plan.CandidateVerdict != "" {
		fmt.Fprintf(&b, "- candidate_verdict: %s\n", plan.CandidateVerdict)
	}
	fmt.Fprintf(&b, "- decision_key: %s\n", plan.DecisionKey)
	fmt.Fprintf(&b, "- decision_id: %s\n", plan.LifecycleDraft.DecisionID)
	fmt.Fprintf(&b, "- entity: %s\n", plan.LifecycleDraft.EntityID)
	if plan.TargetPath != "" {
		fmt.Fprintf(&b, "- target_exists: %t\n", plan.TargetExists)
	}
	if plan.TargetHash != "" {
		fmt.Fprintf(&b, "- target_hash: %s\n", plan.TargetHash)
	}
	fmt.Fprintf(&b, "- evidence_refs: %s\n\n", strings.Join(plan.EvidenceRefs, ", "))
	fmt.Fprintln(&b, "This owner-review artifact does not mutate decision source files, apply policy/doctrine, or promote memory truth.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Candidate")
	fmt.Fprintln(&b)
	if plan.Subject != "" {
		fmt.Fprintf(&b, "- subject: %s\n", plan.Subject)
	}
	if plan.Statement != "" {
		fmt.Fprintf(&b, "- statement: %s\n", plan.Statement)
	}
	if plan.Detail != "" {
		fmt.Fprintf(&b, "- detail: %s\n", plan.Detail)
	}
	if plan.Object != "" {
		fmt.Fprintf(&b, "- object: %s\n", plan.Object)
	}
	if plan.Confidence > 0 {
		fmt.Fprintf(&b, "- confidence: %.2f\n", plan.Confidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Draft DecisionLifecycle")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- title: %s\n", plan.LifecycleDraft.Title)
	fmt.Fprintf(&b, "- kind: %s\n", plan.LifecycleDraft.Kind)
	fmt.Fprintf(&b, "- status: %s\n", plan.LifecycleDraft.Status)
	fmt.Fprintf(&b, "- choice: %s\n", plan.LifecycleDraft.Choice)
	fmt.Fprintf(&b, "- rationale: %s\n", plan.LifecycleDraft.Rationale)
	fmt.Fprintf(&b, "- falsifier: %s\n", plan.LifecycleDraft.Falsifier)
	fmt.Fprintf(&b, "- review_trigger: %s\n", plan.LifecycleDraft.ReviewTrigger)
	if plan.LifecycleDraft.Owner != "" {
		fmt.Fprintf(&b, "- owner: %s\n", plan.LifecycleDraft.Owner)
	}
	if plan.LifecycleDraft.Scope != "" {
		fmt.Fprintf(&b, "- scope: %s\n", plan.LifecycleDraft.Scope)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Evidence Before Apply")
	fmt.Fprintln(&b)
	for _, evidence := range plan.LifecycleDraft.RequiredEvidence {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%s\n", plan.LifecycleDraft.ReviewNote)
	return b.String()
}

func decisionCandidateStatus(q map[string]string) string {
	status := reviewartifact.FirstNonEmpty(q["lifecycle_status"], q["status"])
	if status == "" {
		return StatusProposed
	}
	return CanonicalStatus(status)
}

func decisionCandidateKey(c decisionCandidateRecord) string {
	raw := reviewartifact.FirstNonEmpty(c.Qualifiers["decision_id"], c.Subject, c.Statement, c.Object, c.ID)
	raw = strings.TrimPrefix(raw, "decision:")
	key := slugDecisionCandidate(raw)
	if key == "" {
		return digest12(c.ID)
	}
	return key
}

func slugDecisionCandidate(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ':' || r == '_' || r == '-' || r == '/' || r == ' ' || r == '.':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 72 {
		out = strings.Trim(out[:72], "-")
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
