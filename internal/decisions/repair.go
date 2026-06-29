package decisions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const RepairPlanSchemaVersion = "compound.decision_lifecycle_repair_plan.v0.1"

// LifecycleRepairPlan is a reviewable owner-source repair artifact. It does not
// mutate source files or promote decisions; it records the exact lifecycle debt
// an owner must repair before accepted records can become current truth.
type LifecycleRepairPlan struct {
	SchemaVersion      string            `json:"schema_version"`
	ID                 string            `json:"id"`
	CreatedAt          string            `json:"created_at"`
	SourceRoot         string            `json:"source_root"`
	Status             string            `json:"status"`
	RepairCount        int               `json:"repair_count"`
	MissingFieldCounts map[string]int    `json:"missing_field_counts"`
	Repairs            []LifecycleRepair `json:"repairs"`
	EvidenceRefs       []string          `json:"evidence_refs"`
}

type LifecycleRepair struct {
	DecisionID  string                 `json:"decision_id"`
	Title       string                 `json:"title"`
	Status      string                 `json:"status"`
	SourcePath  string                 `json:"source_path"`
	Missing     []string               `json:"missing"`
	Fields      []LifecycleRepairField `json:"fields"`
	BodySignals []string               `json:"body_signals,omitempty"`
	EvidenceRef string                 `json:"evidence_ref"`
}

type LifecycleRepairField struct {
	Name           string `json:"name"`
	SuggestedValue string `json:"suggested_value"`
	Basis          string `json:"basis"`
	ReviewNote     string `json:"review_note"`
}

type RepairArtifactPaths struct {
	PlanPath   string
	ReviewPath string
}

func BuildLifecycleRepairPlan(root string, now time.Time) (LifecycleRepairPlan, error) {
	files, err := policyDecisionFiles(root)
	if err != nil {
		return LifecycleRepairPlan{}, err
	}
	plan := LifecycleRepairPlan{
		SchemaVersion:      RepairPlanSchemaVersion,
		CreatedAt:          now.UTC().Format(time.RFC3339),
		SourceRoot:         root,
		Status:             "ok",
		MissingFieldCounts: map[string]int{},
	}
	for _, path := range files {
		repair, ok, err := lifecycleRepairForFile(path)
		if err != nil {
			return LifecycleRepairPlan{}, err
		}
		if !ok {
			continue
		}
		plan.Repairs = append(plan.Repairs, repair)
		plan.EvidenceRefs = append(plan.EvidenceRefs, repair.EvidenceRef)
		for _, field := range repair.Missing {
			plan.MissingFieldCounts[field]++
		}
	}
	plan.RepairCount = len(plan.Repairs)
	if plan.RepairCount > 0 {
		plan.Status = "review_required"
	}
	plan.ID = "dlrp_" + digest12(root+"\n"+repairDigest(plan))
	return plan, nil
}

func RecordLifecycleRepairPlan(homePath string, plan LifecycleRepairPlan) (RepairArtifactPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return RepairArtifactPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	dir := filepath.Join(homePath, "reviews", "decision-lifecycle-repairs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return RepairArtifactPaths{}, err
	}
	paths := RepairArtifactPaths{
		PlanPath:   filepath.Join(dir, plan.ID+".json"),
		ReviewPath: filepath.Join(dir, plan.ID+".md"),
	}
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.PlanPath, append(b, '\n'), 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ReviewPath, []byte(RenderLifecycleRepairReview(plan)), 0o644); err != nil {
		return paths, err
	}
	return paths, nil
}

func RenderLifecycleRepairReview(plan LifecycleRepairPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Decision Lifecycle Repair Plan\n\n")
	fmt.Fprintf(&b, "- plan: `%s`\n", plan.ID)
	fmt.Fprintf(&b, "- status: `%s`\n", plan.Status)
	fmt.Fprintf(&b, "- repairs: %d\n", plan.RepairCount)
	fmt.Fprintf(&b, "- source_root: `%s`\n", plan.SourceRoot)
	if len(plan.MissingFieldCounts) > 0 {
		keys := make([]string, 0, len(plan.MissingFieldCounts))
		for k := range plan.MissingFieldCounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- missing_%s: %d\n", k, plan.MissingFieldCounts[k])
		}
	}
	fmt.Fprintf(&b, "\nThese are owner-review suggestions. Applying them blindly does not replace source-owner review.\n\n")
	for _, repair := range plan.Repairs {
		fmt.Fprintf(&b, "## %s\n\n", repair.DecisionID)
		fmt.Fprintf(&b, "- source: `%s`\n", repair.SourcePath)
		fmt.Fprintf(&b, "- title: %s\n", repair.Title)
		fmt.Fprintf(&b, "- missing: `%s`\n", strings.Join(repair.Missing, "`, `"))
		if len(repair.BodySignals) > 0 {
			fmt.Fprintf(&b, "- body_signals: `%s`\n", strings.Join(repair.BodySignals, "`, `"))
		}
		fmt.Fprintf(&b, "\nSuggested frontmatter additions:\n\n")
		fmt.Fprintf(&b, "```yaml\n")
		for _, field := range repair.Fields {
			fmt.Fprintf(&b, "%s: %q\n", field.Name, field.SuggestedValue)
		}
		fmt.Fprintf(&b, "```\n\n")
		for _, field := range repair.Fields {
			fmt.Fprintf(&b, "- `%s` basis: %s\n", field.Name, field.Basis)
			fmt.Fprintf(&b, "- `%s` review: %s\n", field.Name, field.ReviewNote)
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String()
}

func lifecycleRepairForFile(path string) (LifecycleRepair, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return LifecycleRepair{}, false, err
	}
	fm, body := splitFrontmatter(string(b))
	if strings.TrimSpace(fm) == "" {
		return LifecycleRepair{}, false, nil
	}
	var f decFrontmatter
	_ = yaml.Unmarshal([]byte(fm), &f)
	if f.ID == "" {
		f.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if f.Title == "" {
		f.Title = firstHeading(body)
	}
	if f.Falsifier == "" {
		if mm := reFalsifier.FindStringSubmatch(body); mm != nil {
			f.Falsifier = strings.TrimSpace(mm[1])
		}
	}
	if f.ReviewTrigger == "" {
		if mm := reReviewTrigger.FindStringSubmatch(body); mm != nil {
			f.ReviewTrigger = strings.TrimSpace(mm[1])
		}
	}
	if CanonicalStatus(f.Status) != StatusAccepted {
		return LifecycleRepair{}, false, nil
	}
	l := Lifecycle{
		ID:            f.ID,
		Kind:          kindFromQualifiers(lifecycleQualifiers(f.Kind, "decisions-repo", f.ID, f.Status)),
		Choice:        f.Title,
		Status:        CanonicalStatus(f.Status),
		Falsifier:     strings.TrimSpace(f.Falsifier),
		ReviewTrigger: strings.TrimSpace(f.ReviewTrigger),
	}
	missing := MissingRequired(l)
	if len(missing) == 0 {
		return LifecycleRepair{}, false, nil
	}
	signals := lifecycleBodySignals(body)
	repair := LifecycleRepair{
		DecisionID:  f.ID,
		Title:       f.Title,
		Status:      l.Status,
		SourcePath:  path,
		Missing:     missing,
		BodySignals: signals,
		EvidenceRef: "decision_source:" + filepath.ToSlash(path),
	}
	for _, field := range missing {
		repair.Fields = append(repair.Fields, suggestedLifecycleField(field, f, signals))
	}
	return repair, true, nil
}

func suggestedLifecycleField(field string, f decFrontmatter, signals []string) LifecycleRepairField {
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = strings.TrimSpace(f.ID)
	}
	basis := "owner-review suggestion derived from decision id/title"
	if len(signals) > 0 {
		basis += " and body sections: " + strings.Join(signals, ", ")
	}
	switch field {
	case "falsifier":
		return LifecycleRepairField{
			Name:           field,
			SuggestedValue: fmt.Sprintf("Evidence from a governed run shows %q no longer prevents its stated failure mode or creates more owner/source fragmentation than it removes.", title),
			Basis:          basis,
			ReviewNote:     "Tighten this to the concrete failure evidence that would invalidate or retire the decision before applying.",
		}
	case "review_trigger":
		return LifecycleRepairField{
			Name:           field,
			SuggestedValue: fmt.Sprintf("Re-check when a governed workflow, owner, tool, platform, or recurring failure related to %q changes.", title),
			Basis:          basis,
			ReviewNote:     "Replace with the smallest concrete event that should force a future review if the source owner knows one.",
		}
	default:
		return LifecycleRepairField{
			Name:           field,
			SuggestedValue: "OWNER_REVIEW_REQUIRED",
			Basis:          basis,
			ReviewNote:     "Unknown lifecycle field; source owner must fill this manually.",
		}
	}
}

func lifecycleBodySignals(body string) []string {
	headings := []string{
		"## Requirements",
		"## Non-Negotiables",
		"## Agent Checklist",
		"## Acceptance Criteria",
		"## Verification Commands",
		"## Agent Behavior",
		"## Runtime Policy",
	}
	var out []string
	for _, h := range headings {
		if strings.Contains(body, h) {
			out = append(out, strings.TrimPrefix(h, "## "))
		}
	}
	return out
}

func repairDigest(plan LifecycleRepairPlan) string {
	var parts []string
	for _, r := range plan.Repairs {
		parts = append(parts, r.DecisionID+":"+strings.Join(r.Missing, ",")+":"+r.SourcePath)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}
