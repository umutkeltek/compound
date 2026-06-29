package routeevidence

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

const RoutePolicyPlanSchemaVersion = "compound.route_policy_plan.v0.1"

// RoutePolicyPlan is a reviewed routing-policy proposal from a
// route_observation candidate. It never edits gateways, provider registries,
// or model-router config.
type RoutePolicyPlan struct {
	SchemaVersion    string           `json:"schema_version"`
	ID               string           `json:"id"`
	CreatedAt        string           `json:"created_at"`
	Status           string           `json:"status"`
	CandidateID      string           `json:"candidate_id"`
	CandidateVerdict string           `json:"candidate_verdict,omitempty"`
	RouteKey         string           `json:"route_key"`
	Subject          string           `json:"subject"`
	Statement        string           `json:"statement"`
	Detail           string           `json:"detail,omitempty"`
	Confidence       float64          `json:"confidence,omitempty"`
	EvidenceRefs     []string         `json:"evidence_refs"`
	TargetPath       string           `json:"target_path,omitempty"`
	TargetExists     bool             `json:"target_exists"`
	TargetHash       string           `json:"target_hash,omitempty"`
	PolicyDraft      RoutePolicyDraft `json:"policy_draft"`
	Warnings         []string         `json:"warnings,omitempty"`
}

type RoutePolicyDraft struct {
	PolicyID          string   `json:"policy_id"`
	Title             string   `json:"title"`
	AppliesWhen       string   `json:"applies_when"`
	RoutePreference   string   `json:"route_preference"`
	CostGuard         string   `json:"cost_guard"`
	EscalationTrigger string   `json:"escalation_trigger"`
	Verification      string   `json:"verification"`
	RequiredEvidence  []string `json:"required_evidence"`
	ReviewNote        string   `json:"review_note"`
}

type RoutePolicyPlanPaths struct {
	PlanPath   string
	ReviewPath string
}

type routeCandidate struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Subject      string   `json:"subject,omitempty"`
	Statement    string   `json:"statement,omitempty"`
	Detail       string   `json:"detail,omitempty"`
	Confidence   float64  `json:"confidence,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Verdict      string   `json:"verdict,omitempty"`
}

func BuildRoutePolicyPlan(homePath, candidateID, targetPath string, now time.Time) (RoutePolicyPlan, error) {
	homePath = strings.TrimSpace(homePath)
	candidateID = strings.TrimSpace(candidateID)
	if homePath == "" {
		return RoutePolicyPlan{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if candidateID == "" {
		return RoutePolicyPlan{}, fmt.Errorf("candidate id required")
	}
	c, err := readRouteCandidate(homePath, candidateID)
	if err != nil {
		return RoutePolicyPlan{}, err
	}
	if c.Kind != string(candidates.RouteObservation) {
		return RoutePolicyPlan{}, fmt.Errorf("candidate %s kind=%q, want %q", candidateID, c.Kind, candidates.RouteObservation)
	}
	evidenceRefs := uniqueRefs(c.EvidenceRefs)
	targetExists, targetHash, err := hashPlanTarget(targetPath)
	if err != nil {
		return RoutePolicyPlan{}, err
	}
	key := slugifyPlan(firstPlanValue(c.Subject, c.Detail, c.Statement, c.ID))
	if key == "" {
		key = digestPlan12(candidateID)
	}
	statement := firstPlanValue(c.Statement, c.Subject, "reviewed routing observation")
	plan := RoutePolicyPlan{
		SchemaVersion:    RoutePolicyPlanSchemaVersion,
		ID:               idFor("rpp_", candidateID, targetPath),
		CreatedAt:        rfc3339(now),
		Status:           "review_required",
		CandidateID:      candidateID,
		CandidateVerdict: c.Verdict,
		RouteKey:         key,
		Subject:          c.Subject,
		Statement:        c.Statement,
		Detail:           c.Detail,
		Confidence:       c.Confidence,
		EvidenceRefs:     evidenceRefs,
		TargetPath:       strings.TrimSpace(targetPath),
		TargetExists:     targetExists,
		TargetHash:       targetHash,
		PolicyDraft: RoutePolicyDraft{
			PolicyID:          "route_" + key + "_" + digestPlan12(candidateID+c.Statement),
			Title:             firstPlanValue(c.Subject, statement),
			AppliesWhen:       "When a future work item matches this routing observation and its privacy/risk envelope.",
			RoutePreference:   statement,
			CostGuard:         "Use the cheapest sufficient lane only when capability, privacy, latency, and eval gates are satisfied.",
			EscalationTrigger: "Escalate to a stronger or human-approved lane when the cheap lane lacks required capability, evidence quality, or permission.",
			Verification:      "A future ExecutionRouteEvidence row cites this policy draft or its accepted successor and the resulting Transition records routing/cost projections without claiming task success.",
			RequiredEvidence: []string{
				"one or more ExecutionRouteEvidence refs or trace refs",
				"task class or capability class the route applies to",
				"selected target and rejected/fallback target rationale",
				"cost/latency/privacy/capability tradeoff evidence",
			},
			ReviewNote: "This is a routing-policy draft from a route_observation candidate. Reviewers must decide whether evidence is strong enough before applying it to model-router, gateway, or provider config.",
		},
		Warnings: []string{
			"does not mutate gateway or provider config",
			"does not change routing policy",
			"does not promote task success",
		},
	}
	return plan, nil
}

func readRouteCandidate(homePath, candidateID string) (routeCandidate, error) {
	var c routeCandidate
	path := filepath.Join(homePath, candidates.InboxDir(candidates.RouteObservation), candidateID+".json")
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
	return c, nil
}

func RecordRoutePolicyPlan(homePath string, plan RoutePolicyPlan) (RoutePolicyPlanPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return RoutePolicyPlanPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if plan.ID == "" {
		return RoutePolicyPlanPaths{}, fmt.Errorf("route policy plan id required")
	}
	dir := filepath.Join(homePath, "reviews", "route-policy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return RoutePolicyPlanPaths{}, err
	}
	paths := RoutePolicyPlanPaths{
		PlanPath:   filepath.Join(dir, plan.ID+".json"),
		ReviewPath: filepath.Join(dir, plan.ID+".md"),
	}
	data, err := json.MarshalIndent(plan, "", " ")
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.PlanPath, append(data, '\n'), 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ReviewPath, []byte(RenderRoutePolicyReview(plan)), 0o644); err != nil {
		return paths, err
	}
	return paths, nil
}

func RenderRoutePolicyReview(plan RoutePolicyPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Route Policy Review: %s\n\n", plan.CandidateID)
	fmt.Fprintf(&b, "- status: %s\n", plan.Status)
	fmt.Fprintf(&b, "- candidate: %s\n", plan.CandidateID)
	if plan.CandidateVerdict != "" {
		fmt.Fprintf(&b, "- candidate_verdict: %s\n", plan.CandidateVerdict)
	}
	fmt.Fprintf(&b, "- route_key: %s\n", plan.RouteKey)
	fmt.Fprintf(&b, "- policy_id: %s\n", plan.PolicyDraft.PolicyID)
	if plan.TargetPath != "" {
		fmt.Fprintf(&b, "- target: %s\n", plan.TargetPath)
		fmt.Fprintf(&b, "- target_exists: %t\n", plan.TargetExists)
	}
	if plan.TargetHash != "" {
		fmt.Fprintf(&b, "- target_hash: %s\n", plan.TargetHash)
	}
	fmt.Fprintf(&b, "- evidence_refs: %s\n\n", strings.Join(plan.EvidenceRefs, ", "))
	fmt.Fprintln(&b, "This owner-review artifact does not mutate gateway/provider config, change routing policy, or claim task success.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Observation")
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
	fmt.Fprintln(&b, "## Draft Policy")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- applies_when: %s\n", plan.PolicyDraft.AppliesWhen)
	fmt.Fprintf(&b, "- route_preference: %s\n", plan.PolicyDraft.RoutePreference)
	fmt.Fprintf(&b, "- cost_guard: %s\n", plan.PolicyDraft.CostGuard)
	fmt.Fprintf(&b, "- escalation_trigger: %s\n", plan.PolicyDraft.EscalationTrigger)
	fmt.Fprintf(&b, "- verification: %s\n", plan.PolicyDraft.Verification)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Evidence Before Apply")
	fmt.Fprintln(&b)
	for _, evidence := range plan.PolicyDraft.RequiredEvidence {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%s\n", plan.PolicyDraft.ReviewNote)
	return b.String()
}

func hashPlanTarget(path string) (bool, string, error) {
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

func firstPlanValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func slugifyPlan(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
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
	return strings.Trim(b.String(), "-")
}

func digestPlan12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func uniqueRefs(values []string) []string {
	seen := map[string]bool{}
	var out []string
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
