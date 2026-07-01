package capgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
)

func TestExternalSkillCandidateReviewWritesReviewOnlyWithOverlapScan(t *testing.T) {
	home, store := newStore(t)
	if err := store.Add(Node{
		ID:       "skill:react-best-practices",
		Kind:     "skill",
		Name:     "React Best Practices",
		Triggers: []string{"react", "performance", "rendering"},
		Weight:   1,
	}); err != nil {
		t.Fatal(err)
	}
	nodesPath := filepath.Join(home, "registries", "capability-nodes.jsonl")
	beforeNodes, err := os.ReadFile(nodesPath)
	if err != nil {
		t.Fatal(err)
	}
	candidatePath := writeExternalSkillCandidate(t, ExternalSkillCandidate{
		ID:          "vercel-labs/agent-skills/react-best-practices",
		Name:        "React Best Practices",
		Slug:        "react-best-practices",
		Description: "React rendering performance and architecture review checklist",
		Source:      "vercel-labs/agent-skills",
		SourceType:  "github",
		InstallURL:  "https://github.com/vercel-labs/agent-skills",
		URL:         "https://skills.sh/vercel-labs/agent-skills/react-best-practices",
		Hash:        "skills-sh-hash",
		Installs:    515600,
		Official:    true,
		Topics:      []string{"React", "Testing"},
		Files: []ExternalSkillFile{
			{Path: "SKILL.md", Contents: "---\nname: React Best Practices\n---\n"},
		},
		Audits: []ExternalSkillAudit{
			{Provider: "Socket", Status: "pass", Summary: "No alerts", RiskLevel: "LOW"},
		},
	})
	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte("# staged only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err := BuildExternalSkillCandidateReview(home, candidatePath, targetDir, t0)
	if err != nil {
		t.Fatal(err)
	}
	if review.SchemaVersion != SkillCandidateReviewSchemaVersion || review.Status != "review_required" {
		t.Fatalf("review metadata = %+v", review)
	}
	if review.CandidateKind != string(candidates.Skill) || review.CandidateID == "" {
		t.Fatalf("candidate identity = %+v", review)
	}
	if review.ProposedClassification != "shared_skill" || review.LifecycleStage != "staged_candidate" {
		t.Fatalf("classification = %s/%s", review.ProposedClassification, review.LifecycleStage)
	}
	if review.RecommendedDisposition != "merge" {
		t.Fatalf("disposition = %s reason=%s overlaps=%+v", review.RecommendedDisposition, review.DispositionReason, review.OverlapRefs)
	}
	if len(review.OverlapRefs) == 0 || review.OverlapRefs[0].NodeID != "skill:react-best-practices" {
		t.Fatalf("overlaps = %+v", review.OverlapRefs)
	}
	if !review.SourceExists || review.SourceHash == "" || !review.TargetExists || review.TargetHash == "" {
		t.Fatalf("hashes not captured: source=%t/%q target=%t/%q", review.SourceExists, review.SourceHash, review.TargetExists, review.TargetHash)
	}
	paths, err := RecordExternalSkillCandidateReview(home, review)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored SkillCandidateReview
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ID != review.ID || stored.RecommendedDisposition != "merge" {
		t.Fatalf("stored review = %+v", stored)
	}
	markdown, err := os.ReadFile(paths.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Skill Candidate Intake: React Best Practices",
		"Overlap Scan",
		"`skill:react-best-practices`",
		"does not mutate CapabilityGraph",
		"does not install or serve external skill files",
	} {
		if !strings.Contains(string(markdown), want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
	afterNodes, err := os.ReadFile(nodesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterNodes) != string(beforeNodes) {
		t.Fatalf("review mutated graph nodes:\nbefore=%s\nafter=%s", beforeNodes, afterNodes)
	}
	if _, err := os.Stat(filepath.Join(home, candidates.InboxDir(candidates.Skill))); !os.IsNotExist(err) {
		t.Fatalf("review must not write skill inbox candidate, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("review must not promote claims, stat err=%v", err)
	}
}

func TestExternalSkillCandidateReviewRejectsUnsafeFilePaths(t *testing.T) {
	path := writeExternalSkillCandidateRaw(t, `{
 "id": "bad",
 "name": "Bad",
 "source": "owner/repo",
 "files": [{"path": "../SKILL.md", "contents": "x"}]
}`)
	_, err := LoadExternalSkillCandidate(path)
	if err == nil || !strings.Contains(err.Error(), "skill file path must be relative") {
		t.Fatalf("expected relative path validation error, got %v", err)
	}
}

func writeExternalSkillCandidate(t *testing.T, candidate ExternalSkillCandidate) string {
	t.Helper()
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return writeExternalSkillCandidateRaw(t, string(data))
}

func writeExternalSkillCandidateRaw(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "external-skill.json")
	if err := os.WriteFile(path, append([]byte(body), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
