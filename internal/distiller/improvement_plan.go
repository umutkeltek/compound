package distiller

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

	"compound/internal/candidates"
)

const ImprovementPlanSchemaVersion = "compound.distiller.improvement_plan.v0.1"

// ImprovementPlan is a reviewed proposal from method_upgrade or policy_patch
// candidates. It makes distiller self-improvement explicit without silently
// editing prompts, policy, code, eval corpus, or memory truth.
type ImprovementPlan struct {
	SchemaVersion    string           `json:"schema_version"`
	ID               string           `json:"id"`
	CreatedAt        string           `json:"created_at"`
	Status           string           `json:"status"`
	CandidateID      string           `json:"candidate_id"`
	CandidateKind    string           `json:"candidate_kind"`
	CandidateVerdict string           `json:"candidate_verdict,omitempty"`
	ImprovementKey   string           `json:"improvement_key"`
	Subject          string           `json:"subject"`
	Statement        string           `json:"statement"`
	Detail           string           `json:"detail,omitempty"`
	Confidence       float64          `json:"confidence,omitempty"`
	EvidenceRefs     []string         `json:"evidence_refs"`
	TargetPath       string           `json:"target_path,omitempty"`
	TargetExists     bool             `json:"target_exists"`
	TargetHash       string           `json:"target_hash,omitempty"`
	Draft            ImprovementDraft `json:"draft"`
	Warnings         []string         `json:"warnings,omitempty"`
}

type ImprovementDraft struct {
	ImprovementID    string   `json:"improvement_id"`
	Area             string   `json:"area"`
	Title            string   `json:"title"`
	ProposedChange   string   `json:"proposed_change"`
	Guardrail        string   `json:"guardrail"`
	EvalRequirement  string   `json:"eval_requirement"`
	RollbackPlan     string   `json:"rollback_plan"`
	RequiredEvidence []string `json:"required_evidence"`
	ReviewNote       string   `json:"review_note"`
}

type ImprovementPlanPaths struct {
	PlanPath   string
	ReviewPath string
}

func BuildImprovementPlan(homePath, candidateID, targetPath string, now time.Time) (ImprovementPlan, error) {
	homePath = strings.TrimSpace(homePath)
	candidateID = strings.TrimSpace(candidateID)
	targetPath = strings.TrimSpace(targetPath)
	if homePath == "" {
		return ImprovementPlan{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if candidateID == "" {
		return ImprovementPlan{}, fmt.Errorf("candidate id required")
	}
	c, err := readImprovementCandidate(homePath, candidateID)
	if err != nil {
		return ImprovementPlan{}, err
	}
	if c.Kind != KindMethodUpgrade && c.Kind != KindPolicyPatch {
		return ImprovementPlan{}, fmt.Errorf("candidate %s kind=%q, want %q or %q", candidateID, c.Kind, KindMethodUpgrade, KindPolicyPatch)
	}
	if c.Verdict != Park && c.Verdict != Operationalize && c.Verdict != candidates.NoPromote {
		return ImprovementPlan{}, fmt.Errorf("candidate %s verdict=%q, want park, operationalize, or no_promote", candidateID, c.Verdict)
	}
	targetExists, targetHash, err := hashImprovementTarget(targetPath)
	if err != nil {
		return ImprovementPlan{}, err
	}
	evidenceRefs := append([]string(nil), c.EvidenceRefs...)
	sort.Strings(evidenceRefs)
	key := slugifyImprovement(firstImprovementValue(c.Subject, c.Detail, c.Statement, c.ID))
	if key == "" {
		key = digestImprovement12(candidateID)
	}
	area := "method"
	if c.Kind == KindPolicyPatch {
		area = "policy"
	}
	statement := firstImprovementValue(c.Statement, c.Subject, "reviewed distiller improvement")
	plan := ImprovementPlan{
		SchemaVersion:    ImprovementPlanSchemaVersion,
		ID:               idImprovement("imp_", candidateID, targetPath),
		CreatedAt:        rfc3339(now),
		Status:           "review_required",
		CandidateID:      candidateID,
		CandidateKind:    c.Kind,
		CandidateVerdict: c.Verdict,
		ImprovementKey:   key,
		Subject:          c.Subject,
		Statement:        c.Statement,
		Detail:           c.Detail,
		Confidence:       c.Confidence,
		EvidenceRefs:     evidenceRefs,
		TargetPath:       targetPath,
		TargetExists:     targetExists,
		TargetHash:       targetHash,
		Draft: ImprovementDraft{
			ImprovementID:    area + "_" + key + "_" + digestImprovement12(candidateID+c.Statement),
			Area:             area,
			Title:            firstImprovementValue(c.Subject, statement),
			ProposedChange:   statement,
			Guardrail:        "Do not apply this improvement until a reviewed eval case or replay proof shows better extraction/adjudication without increasing false promotion.",
			EvalRequirement:  "Add or cite at least one distiller eval case or replay trace that fails before the change and passes after review.",
			RollbackPlan:     "Revert the reviewed target change and keep this plan as parked evidence if follow-up evals regress.",
			RequiredEvidence: []string{"source trace that produced the improvement candidate", "current policy or method behavior being changed", "expected before/after eval or replay result", "rollback condition and owner target"},
			ReviewNote:       "This distiller improvement draft is candidate evidence. Reviewers must decide whether to apply it to distiller policy, prompts, code, or eval corpus through an explicit owner write, not from candidate output alone.",
		},
		Warnings: []string{
			"does not mutate distiller policy",
			"does not edit prompts or code",
			"does not update eval corpus",
			"does not promote memory truth",
		},
	}
	return plan, nil
}

func readImprovementCandidate(homePath, candidateID string) (Candidate, error) {
	kinds := []candidates.Kind{candidates.MethodUpgrade, candidates.PolicyPatch}
	var found *Candidate
	for _, kind := range kinds {
		path := filepath.Join(homePath, candidates.InboxDir(kind), candidateID+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Candidate{}, err
		}
		var c Candidate
		if err := json.Unmarshal(data, &c); err != nil {
			return Candidate{}, err
		}
		if c.ID == "" {
			c.ID = candidateID
		}
		if c.ID != candidateID {
			return Candidate{}, fmt.Errorf("candidate file id %q does not match requested %q", c.ID, candidateID)
		}
		if found != nil {
			return Candidate{}, fmt.Errorf("candidate id %q found in multiple improvement inboxes", candidateID)
		}
		found = &c
	}
	if found == nil {
		return Candidate{}, fmt.Errorf("candidate %s not found in method_upgrade or policy_patch inbox", candidateID)
	}
	return *found, nil
}

func RecordImprovementPlan(homePath string, plan ImprovementPlan) (ImprovementPlanPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return ImprovementPlanPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if plan.ID == "" {
		return ImprovementPlanPaths{}, fmt.Errorf("improvement plan id required")
	}
	dir := filepath.Join(homePath, "reviews", "distiller-improvements")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ImprovementPlanPaths{}, err
	}
	paths := ImprovementPlanPaths{
		PlanPath:   filepath.Join(dir, plan.ID+".json"),
		ReviewPath: filepath.Join(dir, plan.ID+".md"),
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.PlanPath, append(data, '\n'), 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ReviewPath, []byte(RenderImprovementPlanReview(plan)), 0o644); err != nil {
		return paths, err
	}
	return paths, nil
}

func RenderImprovementPlanReview(plan ImprovementPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Distiller Improvement Review: %s\n\n", plan.CandidateID)
	fmt.Fprintf(&b, "- status: %s\n", plan.Status)
	fmt.Fprintf(&b, "- candidate: %s\n", plan.CandidateID)
	fmt.Fprintf(&b, "- candidate_kind: %s\n", plan.CandidateKind)
	if plan.CandidateVerdict != "" {
		fmt.Fprintf(&b, "- candidate_verdict: %s\n", plan.CandidateVerdict)
	}
	fmt.Fprintf(&b, "- improvement_key: %s\n", plan.ImprovementKey)
	fmt.Fprintf(&b, "- improvement_id: %s\n", plan.Draft.ImprovementID)
	if plan.TargetPath != "" {
		fmt.Fprintf(&b, "- target: %s\n", plan.TargetPath)
		fmt.Fprintf(&b, "- target_exists: %t\n", plan.TargetExists)
	}
	if plan.TargetHash != "" {
		fmt.Fprintf(&b, "- target_hash: %s\n", plan.TargetHash)
	}
	fmt.Fprintf(&b, "- evidence_refs: %s\n\n", strings.Join(plan.EvidenceRefs, ", "))
	fmt.Fprintln(&b, "This owner-review artifact does not mutate distiller policy, edit prompts/code, update eval corpus, or promote memory truth.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Candidate")
	fmt.Fprintln(&b)
	if plan.Subject != "" {
		fmt.Fprintf(&b, "- subject: %s\n", plan.Subject)
	}
	fmt.Fprintf(&b, "- statement: %s\n", plan.Statement)
	if plan.Detail != "" {
		fmt.Fprintf(&b, "- detail: %s\n", plan.Detail)
	}
	if plan.Confidence > 0 {
		fmt.Fprintf(&b, "- confidence: %.2f\n", plan.Confidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Draft Improvement")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- area: %s\n", plan.Draft.Area)
	fmt.Fprintf(&b, "- proposed_change: %s\n", plan.Draft.ProposedChange)
	fmt.Fprintf(&b, "- guardrail: %s\n", plan.Draft.Guardrail)
	fmt.Fprintf(&b, "- eval_requirement: %s\n", plan.Draft.EvalRequirement)
	fmt.Fprintf(&b, "- rollback_plan: %s\n", plan.Draft.RollbackPlan)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Evidence Before Apply")
	fmt.Fprintln(&b)
	for _, evidence := range plan.Draft.RequiredEvidence {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%s\n", plan.Draft.ReviewNote)
	return b.String()
}

func hashImprovementTarget(path string) (bool, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	sum := sha256.Sum256(data)
	return true, hex.EncodeToString(sum[:]), nil
}

func firstImprovementValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func slugifyImprovement(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}

func idImprovement(prefix string, parts ...string) string {
	return prefix + digestImprovement12(strings.Join(parts, "|"))
}

func digestImprovement12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
