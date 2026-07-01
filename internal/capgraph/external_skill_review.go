package capgraph

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

const SkillCandidateReviewSchemaVersion = "compound.capability.skill_candidate_review.v0.1"

// ExternalSkillCandidate is a source-catalog skill record before it becomes a
// Compound inbox candidate, graph node, staged skill, or served adapter.
type ExternalSkillCandidate struct {
	SchemaVersion string                     `json:"schema_version,omitempty"`
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	Slug          string                     `json:"slug,omitempty"`
	Description   string                     `json:"description,omitempty"`
	Source        string                     `json:"source,omitempty"`
	SourceType    string                     `json:"source_type,omitempty"`
	InstallURL    string                     `json:"install_url,omitempty"`
	URL           string                     `json:"url,omitempty"`
	RepositoryURL string                     `json:"repository_url,omitempty"`
	Hash          string                     `json:"hash,omitempty"`
	Installs      int                        `json:"installs,omitempty"`
	IsDuplicate   bool                       `json:"is_duplicate,omitempty"`
	Official      bool                       `json:"official,omitempty"`
	Topics        []string                   `json:"topics,omitempty"`
	Agents        []string                   `json:"agents,omitempty"`
	Tags          []string                   `json:"tags,omitempty"`
	Files         []ExternalSkillFile        `json:"files,omitempty"`
	Audits        []ExternalSkillAudit       `json:"audits,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
}

type ExternalSkillFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents,omitempty"`
}

type ExternalSkillAudit struct {
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	Summary   string `json:"summary,omitempty"`
	RiskLevel string `json:"risk_level,omitempty"`
	AuditedAt string `json:"audited_at,omitempty"`
}

type SkillCandidateReview struct {
	SchemaVersion          string                  `json:"schema_version"`
	ID                     string                  `json:"id"`
	CreatedAt              string                  `json:"created_at"`
	Status                 string                  `json:"status"`
	CandidateKind          string                  `json:"candidate_kind"`
	CandidateID            string                  `json:"candidate_id"`
	SourcePath             string                  `json:"source_path"`
	SourceExists           bool                    `json:"source_exists"`
	SourceHash             string                  `json:"source_hash,omitempty"`
	TargetPath             string                  `json:"target_path,omitempty"`
	TargetExists           bool                    `json:"target_exists"`
	TargetHash             string                  `json:"target_hash,omitempty"`
	External               ExternalSkillCandidate  `json:"external"`
	ProposedClassification string                  `json:"proposed_classification"`
	LifecycleStage         string                  `json:"lifecycle_stage"`
	RecommendedDisposition string                  `json:"recommended_disposition"`
	DispositionReason      string                  `json:"disposition_reason"`
	OverlapRefs            []SkillCandidateOverlap `json:"overlap_refs,omitempty"`
	AuditSignals           []SkillCandidateAudit   `json:"audit_signals,omitempty"`
	EvidenceRequired       []string                `json:"evidence_required"`
	EvalFixtureSuggestions []string                `json:"eval_fixture_suggestions"`
	NextAction             string                  `json:"next_action"`
	Warnings               []string                `json:"warnings"`
}

type SkillCandidateOverlap struct {
	NodeID        string   `json:"node_id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	MatchedTokens []string `json:"matched_tokens"`
	Score         int      `json:"score"`
	Retired       bool     `json:"retired,omitempty"`
	EvidenceOnly  bool     `json:"evidence_only,omitempty"`
}

type SkillCandidateAudit struct {
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	RiskLevel string `json:"risk_level,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

type SkillCandidateReviewPaths struct {
	PlanPath   string
	ReviewPath string
}

func LoadExternalSkillCandidate(path string) (ExternalSkillCandidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ExternalSkillCandidate{}, fmt.Errorf("read external skill candidate %s: %w", path, err)
	}
	var candidate ExternalSkillCandidate
	if err := json.Unmarshal(data, &candidate); err != nil {
		return ExternalSkillCandidate{}, fmt.Errorf("parse external skill candidate %s: %w", path, err)
	}
	if err := candidate.Validate(); err != nil {
		return ExternalSkillCandidate{}, fmt.Errorf("validate external skill candidate %s: %w", path, err)
	}
	return candidate, nil
}

func (c ExternalSkillCandidate) Validate() error {
	if strings.TrimSpace(c.ID) == "" && strings.TrimSpace(c.Name) == "" && strings.TrimSpace(c.Slug) == "" {
		return fmt.Errorf("id, name, or slug required")
	}
	if strings.TrimSpace(c.Source) == "" && strings.TrimSpace(c.URL) == "" && strings.TrimSpace(c.InstallURL) == "" && strings.TrimSpace(c.RepositoryURL) == "" {
		return fmt.Errorf("source, url, install_url, or repository_url required")
	}
	for _, file := range c.Files {
		clean := filepath.Clean(file.Path)
		if strings.TrimSpace(file.Path) == "" || clean == "." || strings.HasPrefix(filepath.ToSlash(clean), "../") || filepath.IsAbs(file.Path) {
			return fmt.Errorf("skill file path must be relative, got %q", file.Path)
		}
	}
	return nil
}

func BuildExternalSkillCandidateReview(homePath, candidatePath, targetPath string, now time.Time) (SkillCandidateReview, error) {
	candidate, err := LoadExternalSkillCandidate(candidatePath)
	if err != nil {
		return SkillCandidateReview{}, err
	}
	sourceExists, sourceHash, err := reviewartifact.HashFileTarget(candidatePath)
	if err != nil {
		return SkillCandidateReview{}, err
	}
	targetExists, targetHash, err := hashCandidateTarget(targetPath)
	if err != nil {
		return SkillCandidateReview{}, err
	}
	overlaps, err := skillCandidateOverlaps(homePath, candidate)
	if err != nil {
		return SkillCandidateReview{}, err
	}
	classification := classifyExternalSkillCandidate(candidate)
	disposition, reason := recommendExternalSkillCandidateDisposition(candidate, overlaps)
	candidateID := externalSkillCandidateID(candidate)
	review := SkillCandidateReview{
		SchemaVersion:          SkillCandidateReviewSchemaVersion,
		ID:                     reviewartifact.ID("scr_", sourceHash, candidateID, targetPath),
		CreatedAt:              now.UTC().Format(time.RFC3339),
		Status:                 "review_required",
		CandidateKind:          string(candidates.Skill),
		CandidateID:            candidateID,
		SourcePath:             candidatePath,
		SourceExists:           sourceExists,
		SourceHash:             sourceHash,
		TargetPath:             strings.TrimSpace(targetPath),
		TargetExists:           targetExists,
		TargetHash:             targetHash,
		External:               candidate,
		ProposedClassification: classification,
		LifecycleStage:         "staged_candidate",
		RecommendedDisposition: disposition,
		DispositionReason:      reason,
		OverlapRefs:            overlaps,
		AuditSignals:           externalSkillAuditSignals(candidate),
		EvidenceRequired: []string{
			"source provenance and license reviewed",
			"duplicate scan against existing CapabilityGraph and served skill roots",
			"deterministic tool/MCP/CLI replacement check completed",
			"trigger scope narrow enough for description-match surfacing",
			"security audit or manual content review recorded",
			"one eval fixture proves the skill changes a real outcome",
			"rollback/TTL for staged candidate recorded before serving",
		},
		EvalFixtureSuggestions: evalFixtureSuggestions(candidate),
		NextAction:             "review this artifact; only after review create a skill inbox candidate or staged non-served skill folder",
		Warnings: []string{
			"review-only: does not mutate CapabilityGraph",
			"does not install or serve external skill files",
			"does not execute tools, MCPs, or marketplace CLIs",
			"does not promote memory truth",
		},
	}
	return review, nil
}

func RecordExternalSkillCandidateReview(homePath string, review SkillCandidateReview) (SkillCandidateReviewPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return SkillCandidateReviewPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(review.ID) == "" {
		return SkillCandidateReviewPaths{}, fmt.Errorf("skill candidate review id required")
	}
	paths, err := reviewartifact.Write(homePath, "skill-candidates", review.ID, review, " ", RenderExternalSkillCandidateReview(review))
	if err != nil {
		return SkillCandidateReviewPaths{}, err
	}
	return SkillCandidateReviewPaths{PlanPath: paths.PlanPath, ReviewPath: paths.ReviewPath}, nil
}

func RenderExternalSkillCandidateReview(review SkillCandidateReview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Skill Candidate Intake: %s\n\n", review.External.displayName())
	fmt.Fprintf(&b, "- status: `%s`\n", review.Status)
	fmt.Fprintf(&b, "- candidate_kind: `%s`\n", review.CandidateKind)
	fmt.Fprintf(&b, "- candidate_id: `%s`\n", review.CandidateID)
	fmt.Fprintf(&b, "- source: %s\n", firstEvalNonEmpty(review.External.URL, review.External.InstallURL, review.External.RepositoryURL, review.External.Source))
	fmt.Fprintf(&b, "- proposed_classification: `%s`\n", review.ProposedClassification)
	fmt.Fprintf(&b, "- lifecycle_stage: `%s`\n", review.LifecycleStage)
	fmt.Fprintf(&b, "- recommended_disposition: `%s`\n", review.RecommendedDisposition)
	fmt.Fprintf(&b, "- reason: %s\n", review.DispositionReason)
	if review.External.Installs > 0 {
		fmt.Fprintf(&b, "- installs: %d\n", review.External.Installs)
	}
	if review.External.Hash != "" {
		fmt.Fprintf(&b, "- upstream_hash: `%s`\n", review.External.Hash)
	}
	if review.SourceHash != "" {
		fmt.Fprintf(&b, "- source_hash: `%s`\n", review.SourceHash)
	}
	if review.TargetPath != "" {
		fmt.Fprintf(&b, "- target: %s\n", review.TargetPath)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Overlap Scan")
	fmt.Fprintln(&b)
	if len(review.OverlapRefs) == 0 {
		fmt.Fprintln(&b, "- none found in current CapabilityGraph")
	} else {
		for _, overlap := range review.OverlapRefs {
			fmt.Fprintf(&b, "- `%s` (%s, %s) score=%d tokens=%s\n", overlap.NodeID, overlap.Kind, overlap.Name, overlap.Score, strings.Join(overlap.MatchedTokens, ","))
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Audit Signals")
	fmt.Fprintln(&b)
	if len(review.AuditSignals) == 0 {
		fmt.Fprintln(&b, "- none supplied; manual security/content review required")
	} else {
		for _, signal := range review.AuditSignals {
			fmt.Fprintf(&b, "- %s: %s", signal.Provider, signal.Status)
			if signal.RiskLevel != "" {
				fmt.Fprintf(&b, " (%s)", signal.RiskLevel)
			}
			if signal.Summary != "" {
				fmt.Fprintf(&b, " - %s", signal.Summary)
			}
			fmt.Fprintln(&b)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Evidence Required")
	fmt.Fprintln(&b)
	for _, evidence := range review.EvidenceRequired {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Eval Fixture Suggestions")
	fmt.Fprintln(&b)
	for _, suggestion := range review.EvalFixtureSuggestions {
		fmt.Fprintf(&b, "- %s\n", suggestion)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Boundaries")
	fmt.Fprintln(&b)
	for _, warning := range review.Warnings {
		fmt.Fprintf(&b, "- %s\n", warning)
	}
	return b.String()
}

func (c ExternalSkillCandidate) displayName() string {
	return firstEvalNonEmpty(c.Name, c.Slug, c.ID, c.Source)
}

func externalSkillCandidateID(c ExternalSkillCandidate) string {
	id := reviewartifact.Slugify(firstEvalNonEmpty(c.ID, c.Source+"/"+c.Slug, c.Name, c.Slug), 72)
	if id == "" {
		id = "external-" + reviewartifact.Digest12(c.Source+c.URL+c.InstallURL)
	}
	return id
}

func classifyExternalSkillCandidate(c ExternalSkillCandidate) string {
	text := strings.ToLower(strings.Join([]string{c.Name, c.Slug, c.Description, c.Source, strings.Join(c.Tags, " "), strings.Join(c.Topics, " ")}, " "))
	if hasSkillMD(c) {
		return "shared_skill"
	}
	if strings.Contains(text, "mcp") || strings.Contains(text, "cli") || strings.Contains(text, "tool") || strings.Contains(text, "api") {
		return "tool"
	}
	if strings.Contains(text, "recipe") || strings.Contains(text, "workflow") || strings.Contains(text, "doctrine") || strings.Contains(text, "playbook") {
		return "recipe"
	}
	if strings.Contains(text, "plugin") || strings.Contains(text, "marketplace") {
		return "plugin_or_marketplace"
	}
	return "staged_candidate"
}

func recommendExternalSkillCandidateDisposition(c ExternalSkillCandidate, overlaps []SkillCandidateOverlap) (string, string) {
	if c.IsDuplicate {
		return "reference_only", "source marks candidate as duplicate/fork; keep as provenance unless reviewer chooses merge"
	}
	for _, signal := range externalSkillAuditSignals(c) {
		status := strings.ToLower(signal.Status)
		risk := strings.ToLower(signal.RiskLevel)
		if status == "fail" || risk == "critical" || risk == "high" {
			return "reject", "external audit signal is high risk or failing"
		}
	}
	if len(overlaps) > 0 && overlaps[0].Score >= 2 {
		return "merge", "candidate overlaps existing CapabilityGraph capability; adapt useful parts into owner instead of serving a duplicate"
	}
	return "park", "external marketplace evidence is not runtime proof; keep staged until eval and review pass"
}

func externalSkillAuditSignals(c ExternalSkillCandidate) []SkillCandidateAudit {
	out := make([]SkillCandidateAudit, 0, len(c.Audits))
	for _, audit := range c.Audits {
		provider := strings.TrimSpace(audit.Provider)
		status := strings.TrimSpace(audit.Status)
		if provider == "" && status == "" {
			continue
		}
		out = append(out, SkillCandidateAudit{
			Provider:  firstEvalNonEmpty(provider, "unknown"),
			Status:    firstEvalNonEmpty(status, "unknown"),
			RiskLevel: strings.TrimSpace(audit.RiskLevel),
			Summary:   strings.TrimSpace(audit.Summary),
		})
	}
	return out
}

func evalFixtureSuggestions(c ExternalSkillCandidate) []string {
	name := c.displayName()
	task := strings.TrimSpace(c.Description)
	if task == "" {
		task = name
	}
	return []string{
		fmt.Sprintf("baseline prompt without candidate: complete a real %q task using current served skills", task),
		fmt.Sprintf("candidate prompt with %q staged as context only; compare output quality, specificity, and policy fit", name),
		"failure fixture: prompt near an owned overlapping skill and prove the candidate does not steal routing unless reviewed",
		"receipt fixture: record the exact state delta the candidate improved; popularity/install count is not success evidence",
	}
}

func hasSkillMD(c ExternalSkillCandidate) bool {
	for _, file := range c.Files {
		if strings.EqualFold(filepath.ToSlash(file.Path), "skill.md") || strings.HasSuffix(strings.ToLower(filepath.ToSlash(file.Path)), "/skill.md") {
			return true
		}
	}
	return false
}

func skillCandidateOverlaps(homePath string, c ExternalSkillCandidate) ([]SkillCandidateOverlap, error) {
	store, err := Open(homePath)
	if err != nil {
		return nil, err
	}
	nodes, err := store.List()
	if err != nil {
		return nil, err
	}
	candidateTokens := reviewTokenSet(c.ID, c.Name, c.Slug, c.Description, c.Source, strings.Join(c.Tags, " "), strings.Join(c.Topics, " "))
	var overlaps []SkillCandidateOverlap
	for _, node := range nodes {
		nodeTokens := reviewTokenSet(node.ID, node.Name, strings.Join(node.Triggers, " "))
		var matched []string
		for token := range candidateTokens {
			if nodeTokens[token] {
				matched = append(matched, token)
			}
		}
		sort.Strings(matched)
		score := len(matched)
		if sameSlug(node.Name, c.Name) || sameSlug(node.ID, c.ID) || sameSlug(node.ID, c.Slug) {
			score += 3
		}
		if score == 0 {
			continue
		}
		overlaps = append(overlaps, SkillCandidateOverlap{
			NodeID:        node.ID,
			Kind:          node.Kind,
			Name:          node.Name,
			MatchedTokens: matched,
			Score:         score,
			Retired:       node.Retired,
			EvidenceOnly:  node.EvidenceOnly,
		})
	}
	sort.Slice(overlaps, func(i, j int) bool {
		if overlaps[i].Score != overlaps[j].Score {
			return overlaps[i].Score > overlaps[j].Score
		}
		return overlaps[i].NodeID < overlaps[j].NodeID
	})
	if len(overlaps) > 8 {
		overlaps = overlaps[:8]
	}
	return overlaps, nil
}

func reviewTokenSet(values ...string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			if len(token) < 3 {
				continue
			}
			out[token] = true
		}
	}
	return out
}

func sameSlug(a, b string) bool {
	as := reviewartifact.Slugify(a, 0)
	bs := reviewartifact.Slugify(b, 0)
	return as != "" && as == bs
}

func hashCandidateTarget(path string) (bool, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	if info.IsDir() {
		return reviewartifact.HashTreeTarget(path)
	}
	return reviewartifact.HashFileTarget(path)
}
