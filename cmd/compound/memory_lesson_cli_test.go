package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
	"compound/internal/promotedriver"
)

func writeSkillFixture(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The lesson door promotes on write AND lands one dated line in the named
// skill's "## Lessons" section, resolved from --skills-dir (never a hardcoded
// personal path). A second identical lesson is a duplicate and adds no line.
func TestMemoryLessonPromotesAndAppendsSkillLine(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv(promotedriver.SkillsDirsEnv, "")
	skills := t.TempDir()
	path := writeSkillFixture(t, skills, "verify-x", "---\nname: verify-x\n---\n# Verify\n\n## Lessons\n\n- 2026-10-01: older lesson.\n\n## Other\n\nbody\n")

	args := []string{"lesson", "--why", "A filter on the wrong key printed failed [] for a red run.", "--how", "Read the stdout verdict line.",
		"--skill", "verify-x", "--skills-dir", skills, "--repo", t.TempDir(), "--trigger", `Bash:proof-check.*\|.*jq`,
		"--evidence", "note:verdict", "--by", "agent:test", "--harness", "claude_code", "--json"}
	out := captureStdout(t, func() { cmdMemory(args) })
	var receipt struct {
		Outcome       string    `json:"outcome"`
		Claim         mem.Claim `json:"claim"`
		PositionID    string    `json:"position_id"`
		SkillFile     string    `json:"skill_file"`
		SkillLines    []string  `json:"skill_lines"`
		SkillAppended bool      `json:"skill_appended"`
	}
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("parse receipt: %v\n%s", err, out)
	}
	if receipt.Outcome != mem.LessonOutcomePromoted || receipt.Claim.ClaimType != mem.ClaimTypeLesson || !receipt.SkillAppended || receipt.SkillFile != path {
		t.Fatalf("bad receipt: %+v", receipt)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	lessons := text[strings.Index(text, "## Lessons"):strings.Index(text, "## Other")]
	if !strings.Contains(lessons, receipt.Claim.ID) || !strings.Contains(lessons, "How: Read the stdout verdict line.") || !strings.Contains(lessons, "- 2026-10-01: older lesson.") {
		t.Fatalf("lesson line must land inside the existing Lessons section, after older lines:\n%s", text)
	}
	if !strings.Contains(lessons, `trigger: {"id":"`+receipt.Claim.ID+`","tool":"Bash","pattern":"proof-check.*\\|.*jq","how_to_apply":"Read the stdout verdict line.","source":"lesson:`+receipt.Claim.ID+`"}`) {
		t.Fatalf("hook-index trigger line missing:\n%s", text)
	}
	if strings.Count(text, "## Lessons") != 1 || !strings.HasSuffix(text, "## Other\n\nbody\n") {
		t.Fatalf("append must not duplicate the section or disturb later sections:\n%s", text)
	}

	store, err := mem.OpenReadOnly(homePath)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := store.Position("skill:verify-x", mem.PositionFoldOptions{})
	if err != nil || len(pos.Positions) != 1 || pos.Positions[0].Position.ActiveClaimID != receipt.Claim.ID {
		t.Fatalf("position must serve the lesson immediately: %+v err=%v", pos, err)
	}

	again := captureStdout(t, func() { cmdMemory(args) })
	if !strings.Contains(again, `"outcome": "duplicate"`) {
		t.Fatalf("same lesson again must be a duplicate:\n%s", again)
	}
	data2, _ := os.ReadFile(path)
	if string(data2) != text {
		t.Fatalf("duplicate lesson must not change the skill file:\n%s", data2)
	}
}

// Source-control snapshots are status: they are reported but never captured
// or promoted (operator ruling 2026-11-03).
func TestAuthorityGitDoesNotPromoteStatusRows(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	repo := initCompoundCommandGitRepo(t)
	out := captureStdout(t, func() { cmdAuthority([]string{"git", repo, "--json"}) })
	var report struct {
		AuthorityClaims   int `json:"authority_claims"`
		StatusNotPromoted int `json:"status_not_promoted"`
		CandidateCount    int `json:"candidate_count"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if report.AuthorityClaims != 0 || report.StatusNotPromoted == 0 || report.StatusNotPromoted != report.CandidateCount {
		t.Fatalf("status rows must be refused, not promoted: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); err == nil {
		data, _ := os.ReadFile(filepath.Join(homePath, "ledger", "claims.jsonl"))
		if strings.TrimSpace(string(data)) != "" {
			t.Fatalf("no status claim may reach the ledger:\n%s", data)
		}
	}
}

// With the producers off, session-mining apply files nothing and says why.
func TestSessionMiningApplyIsOffWithoutProducerOptIn(t *testing.T) {
	t.Setenv("COMPOUND_CANDIDATE_PRODUCERS", "")
	homePath := t.TempDir()
	report := &sessionMiningReport{PreferencesFound: []sessionMiningFinding{{Kind: "tone", Summary: "prefers short answers", Confidence: 0.9, EvidenceRefs: []string{"session:a", "session:b"}}}}
	applied, err := applySessionMiningReport(homePath, report, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.PreferenceCandidateIDs) != 0 || len(applied.Warnings) != 1 || !strings.Contains(applied.Warnings[0], "COMPOUND_CANDIDATE_PRODUCERS") {
		t.Fatalf("producer off must file nothing and say why: %+v", applied)
	}
	if entries, _ := os.ReadDir(filepath.Join(homePath, "inbox", "memory-candidates")); len(entries) != 0 {
		t.Fatalf("producer off wrote %d memory candidates", len(entries))
	}
}
