package capgraph

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

	"gopkg.in/yaml.v3"
)

const CapabilityEvaluationPlanSchemaVersion = "compound.capability.evaluation_plan.v0.1"

// CapabilityEvaluationPlan is an owner-review artifact for an evaluated
// capability that is not yet known to the CapabilityGraph. It never mutates
// graph nodes, installs skill files, executes tools, or promotes claims.
type CapabilityEvaluationPlan struct {
	SchemaVersion  string              `json:"schema_version"`
	ID             string              `json:"id"`
	CreatedAt      string              `json:"created_at"`
	Status         string              `json:"status"`
	EvaluationSet  string              `json:"evaluation_set"`
	EvaluationID   string              `json:"evaluation_id"`
	Capability     string              `json:"capability"`
	Verdict        string              `json:"verdict"`
	RubricScore    float64             `json:"rubric_score,omitempty"`
	SourceReceipt  string              `json:"source_receipt,omitempty"`
	Signals        map[string]any      `json:"signals,omitempty"`
	EvidenceRefs   []string            `json:"evidence_refs"`
	TargetPath     string              `json:"target_path,omitempty"`
	TargetExists   bool                `json:"target_exists"`
	TargetHash     string              `json:"target_hash,omitempty"`
	NodeDraft      CapabilityNodeDraft `json:"node_draft"`
	ReviewChoices  []string            `json:"review_choices"`
	RequiredReview []string            `json:"required_review"`
	Warnings       []string            `json:"warnings,omitempty"`
}

type CapabilityNodeDraft struct {
	NodeID           string   `json:"node_id"`
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	Triggers         []string `json:"triggers"`
	Weight           float64  `json:"weight"`
	RequiredEvidence []string `json:"required_evidence"`
	ReviewNote       string   `json:"review_note"`
}

type CapabilityEvaluationPlanPaths struct {
	PlanPath          string
	ReviewPath        string
	ManifestDraftPath string
}

func BuildCapabilityEvaluationPlan(homePath string, set CapabilityEvaluationSet, selector, targetPath string, now time.Time) (CapabilityEvaluationPlan, error) {
	if strings.TrimSpace(homePath) == "" {
		return CapabilityEvaluationPlan{}, fmt.Errorf("COMPOUND_HOME required")
	}
	eval, err := selectCapabilityEvaluation(set, selector)
	if err != nil {
		return CapabilityEvaluationPlan{}, err
	}
	store, err := Open(homePath)
	if err != nil {
		return CapabilityEvaluationPlan{}, err
	}
	nodes, err := store.loadNodes()
	if err != nil {
		return CapabilityEvaluationPlan{}, err
	}
	if nodeID := resolveCapabilityNode(nodes, eval.Capability); nodeID != "" {
		return CapabilityEvaluationPlan{}, fmt.Errorf("capability %q already known as %s; use import-evaluations for graph evidence", eval.Capability, nodeID)
	}
	targetExists, targetHash, err := hashSkillTarget(targetPath)
	if err != nil {
		return CapabilityEvaluationPlan{}, err
	}
	key := slugifySkill(firstEvalNonEmpty(eval.Capability, eval.ID))
	if key == "" {
		key = digestEvalPlan12(eval.ID)
	}
	kind := suggestedCapabilityKind(eval)
	nodeID := kind + ":" + key
	triggers := planSkillTriggers(eval.Capability, eval.Verdict, signalString(eval.Signals, "cluster"), signalString(eval.Signals, "source_family"))
	evidenceRefs := capabilityEvaluationPlanEvidence(set, eval)
	plan := CapabilityEvaluationPlan{
		SchemaVersion: CapabilityEvaluationPlanSchemaVersion,
		ID:            idEvalPlan("cep_", set.ID, eval.ID, targetPath),
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Status:        "review_required",
		EvaluationSet: set.ID,
		EvaluationID:  eval.ID,
		Capability:    eval.Capability,
		Verdict:       eval.Verdict,
		RubricScore:   eval.RubricNormalized,
		SourceReceipt: eval.SourceReceipt,
		Signals:       eval.Signals,
		EvidenceRefs:  evidenceRefs,
		TargetPath:    strings.TrimSpace(targetPath),
		TargetExists:  targetExists,
		TargetHash:    targetHash,
		NodeDraft: CapabilityNodeDraft{
			NodeID:   nodeID,
			Kind:     kind,
			Name:     humanCapabilityName(eval.Capability),
			Triggers: triggers,
			Weight:   evaluationConfidence(eval),
			RequiredEvidence: []string{
				"source capability evaluation record",
				"reviewed capability kind and trigger scope",
				"explicit input/output or operating contract",
				"overlap/replacement decision against existing CapabilityGraph nodes",
				"one validation receipt or eval showing useful behavior",
			},
			ReviewNote: "This is a CapabilityGraph node draft from an unknown evaluated capability. Reviewers may change kind/id/triggers or reject it; importing requires a separate reviewed skill/manifest path.",
		},
		ReviewChoices: append([]string(nil), NodeKinds...),
		RequiredReview: []string{
			"keep/adapt/merge/publicize does not equal install",
			"choose node kind deliberately",
			"record replacement/overlap if an owned capability already covers this",
			"do not surface to agents until imported through the graph contract",
		},
		Warnings: []string{
			"does not mutate CapabilityGraph",
			"does not install skill files",
			"does not execute tools or MCPs",
			"does not promote memory truth",
		},
	}
	sort.Strings(plan.ReviewChoices)
	return plan, nil
}

func RecordCapabilityEvaluationPlan(homePath string, plan CapabilityEvaluationPlan) (CapabilityEvaluationPlanPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return CapabilityEvaluationPlanPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(plan.ID) == "" {
		return CapabilityEvaluationPlanPaths{}, fmt.Errorf("capability evaluation plan id required")
	}
	dir := filepath.Join(homePath, "reviews", "capability-evaluation-intake")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CapabilityEvaluationPlanPaths{}, err
	}
	paths := CapabilityEvaluationPlanPaths{
		PlanPath:          filepath.Join(dir, plan.ID+".json"),
		ReviewPath:        filepath.Join(dir, plan.ID+".md"),
		ManifestDraftPath: filepath.Join(dir, plan.ID+".skill-manifest.yaml"),
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.PlanPath, append(data, '\n'), 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ReviewPath, []byte(RenderCapabilityEvaluationPlanReview(plan)), 0o644); err != nil {
		return paths, err
	}
	draft, err := CapabilityEvaluationPlanSkillManifest(plan)
	if err != nil {
		return paths, err
	}
	manifestData, err := yaml.Marshal(draft)
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ManifestDraftPath, manifestData, 0o644); err != nil {
		return paths, err
	}
	return paths, nil
}

func CapabilityEvaluationPlanSkillManifest(plan CapabilityEvaluationPlan) (SkillManifest, error) {
	if strings.TrimSpace(plan.ID) == "" {
		return SkillManifest{}, fmt.Errorf("capability evaluation plan id required")
	}
	if strings.TrimSpace(plan.NodeDraft.NodeID) == "" {
		return SkillManifest{}, fmt.Errorf("capability evaluation plan node draft id required")
	}
	manifest := SkillManifest{
		SchemaVersion: SkillManifestSchemaVersion,
		ID:            "skill_manifest:" + plan.ID,
		AdapterID:     "compound.capability-evaluation-plan",
		SourceSystem:  "compound",
		ObservedAt:    plan.CreatedAt,
		SourceRef: SkillSourceRef{
			Path:        "capability-evaluation:" + plan.EvaluationID,
			Kind:        "review_artifact",
			Owner:       plan.Capability,
			SourceClass: "capability_evaluation_intake",
			Revision:    plan.ID,
		},
		EvidenceRefs: append([]string(nil), plan.EvidenceRefs...),
		Skills: []SkillManifestEntry{
			{
				ID:           plan.NodeDraft.NodeID,
				Kind:         plan.NodeDraft.Kind,
				Name:         plan.NodeDraft.Name,
				Status:       "needs_review",
				Triggers:     append([]string(nil), plan.NodeDraft.Triggers...),
				Capabilities: []string{plan.Capability},
				EvidenceRefs: append([]string(nil), plan.EvidenceRefs...),
				Weight:       plan.NodeDraft.Weight,
			},
		},
	}
	return manifest, nil
}

func RenderCapabilityEvaluationPlanReview(plan CapabilityEvaluationPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Capability Evaluation Intake: %s\n\n", plan.Capability)
	fmt.Fprintf(&b, "- status: %s\n", plan.Status)
	fmt.Fprintf(&b, "- evaluation_set: %s\n", plan.EvaluationSet)
	fmt.Fprintf(&b, "- evaluation: %s\n", plan.EvaluationID)
	fmt.Fprintf(&b, "- capability: %s\n", plan.Capability)
	fmt.Fprintf(&b, "- verdict: %s\n", plan.Verdict)
	if plan.RubricScore > 0 {
		fmt.Fprintf(&b, "- rubric_score: %.2f\n", plan.RubricScore)
	}
	if plan.SourceReceipt != "" {
		fmt.Fprintf(&b, "- source_receipt: %s\n", plan.SourceReceipt)
	}
	if plan.TargetPath != "" {
		fmt.Fprintf(&b, "- target: %s\n", plan.TargetPath)
		fmt.Fprintf(&b, "- target_exists: %t\n", plan.TargetExists)
	}
	if plan.TargetHash != "" {
		fmt.Fprintf(&b, "- target_hash: %s\n", plan.TargetHash)
	}
	fmt.Fprintf(&b, "- evidence_refs: %s\n\n", strings.Join(plan.EvidenceRefs, ", "))
	fmt.Fprintln(&b, "This owner-review artifact does not mutate CapabilityGraph, install skill files, execute tools/MCPs, surface offers, or promote memory truth.")
	fmt.Fprintln(&b, "The sibling skill-manifest draft uses status `needs_review`, so importing it remains non-surfacing until a reviewer edits it to `active`.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Draft CapabilityGraph Node")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- id: %s\n", plan.NodeDraft.NodeID)
	fmt.Fprintf(&b, "- kind: %s\n", plan.NodeDraft.Kind)
	fmt.Fprintf(&b, "- name: %s\n", plan.NodeDraft.Name)
	fmt.Fprintf(&b, "- triggers: %s\n", strings.Join(plan.NodeDraft.Triggers, ", "))
	fmt.Fprintf(&b, "- weight: %.2f\n", plan.NodeDraft.Weight)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Evidence Before Import")
	fmt.Fprintln(&b)
	for _, evidence := range plan.NodeDraft.RequiredEvidence {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Review")
	fmt.Fprintln(&b)
	for _, item := range plan.RequiredReview {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%s\n", plan.NodeDraft.ReviewNote)
	return b.String()
}

func selectCapabilityEvaluation(set CapabilityEvaluationSet, selector string) (CapabilityEvaluation, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return CapabilityEvaluation{}, fmt.Errorf("--capability is required")
	}
	if len(set.Evaluations) == 0 {
		return CapabilityEvaluation{}, fmt.Errorf("capability evaluation set has no evaluations")
	}
	var matches []CapabilityEvaluation
	for _, eval := range set.Evaluations {
		if err := eval.Validate(); err != nil {
			return CapabilityEvaluation{}, err
		}
		if capabilityEvaluationMatches(eval, selector) {
			matches = append(matches, eval)
		}
	}
	if len(matches) == 0 {
		return CapabilityEvaluation{}, fmt.Errorf("capability evaluation %q not found", selector)
	}
	if len(matches) > 1 {
		return CapabilityEvaluation{}, fmt.Errorf("capability evaluation %q is ambiguous (%d matches)", selector, len(matches))
	}
	return matches[0], nil
}

func capabilityEvaluationMatches(eval CapabilityEvaluation, selector string) bool {
	candidates := []string{
		eval.Capability,
		eval.ID,
		capabilityEvaluationNodeID(eval),
		"capability://" + eval.Capability,
	}
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate), selector) {
			return true
		}
	}
	return false
}

func capabilityEvaluationPlanEvidence(set CapabilityEvaluationSet, eval CapabilityEvaluation) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}
	add("capability_evaluation_set:" + set.ID)
	if set.Revision != "" {
		add("source_revision:" + set.Revision)
	}
	for _, ref := range set.EvidenceRefs {
		if strings.Contains(ref, eval.ID) {
			add(ref)
		}
	}
	add("capability_evaluation:" + eval.ID)
	if eval.SourceReceipt != "" {
		add("source_receipt:" + eval.SourceReceipt)
	}
	sort.Strings(out)
	return out
}

func suggestedCapabilityKind(eval CapabilityEvaluation) string {
	joined := strings.ToLower(strings.Join([]string{
		eval.Capability,
		eval.Verdict,
		signalString(eval.Signals, "kind"),
		signalString(eval.Signals, "cluster"),
		signalString(eval.Signals, "source_family"),
	}, " "))
	switch {
	case strings.Contains(joined, "mcp"):
		return "mcp"
	case strings.Contains(joined, "tool") || strings.Contains(joined, "cli"):
		return "tool"
	case strings.Contains(joined, "script"):
		return "script"
	case strings.Contains(joined, "procedure") || strings.Contains(joined, "workflow"):
		return "procedure"
	case strings.Contains(joined, "model"):
		return "model_profile"
	case strings.Contains(joined, "lens"):
		return "lens"
	default:
		return "skill"
	}
}

func signalString(signals map[string]any, key string) string {
	if signals == nil {
		return ""
	}
	value, ok := signals[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func humanCapabilityName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unknown capability"
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == '.'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func idEvalPlan(prefix string, parts ...string) string {
	return prefix + digestEvalPlan12(strings.Join(parts, "|"))
}

func digestEvalPlan12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
