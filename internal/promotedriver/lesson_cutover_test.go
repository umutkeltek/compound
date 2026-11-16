package promotedriver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hook"
)

func skillOnlyLineDir(t *testing.T) string {
	t.Helper()
	skills := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(skills, "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `trigger: {"id":"skill_port","tools":["Bash"],"pattern":"\\bfixture-runner\\s+test\\b","apply":"Use the wrapper; a bare run hangs on an occupied port."}`
	if err := os.WriteFile(filepath.Join(skills, "example", "SKILL.md"), []byte("# Example\n"+line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skills
}

// Cutover guard. With the skill folders recorded (one explicit
// `compound hook lessons-index --skills DIR`), the automatic rebuild after a
// lesson write keeps a skill-file-only line. Without them (an index built by
// hand before sources were recorded), the automatic rebuild drops the line,
// and it says so and names the fix instead of dropping it silently.
func TestRebuildWithRecordedSourcesKeepsSkillOnlyLines(t *testing.T) {
	_, store := servingTestHome(t)
	skills := skillOnlyLineDir(t)
	var out, errOut bytes.Buffer
	if code := hook.RunLessonsIndexCommand([]string{"--skills", skills}, &out, &errOut); code != 0 {
		t.Fatalf("explicit build: %d %s %s", code, &out, &errOut)
	}
	written := writeServedLesson(t, store, "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	if got := served(t, "claude_code", "fixture-runner test flows/"); !strings.Contains(got, "skill_port") {
		t.Fatalf("recorded sources must keep the skill-only line through an automatic rebuild: %q", got)
	}
	if got := served(t, "claude_code", "widget --force"); !strings.Contains(got, written.Claim.ID) {
		t.Fatalf("ledger lesson missing: %q", got)
	}

	// A home whose index was built by hand, with no recorded sources.
	homePath, store := servingTestHome(t)
	if _, err := hook.CompileLessonIndex(homePath, hook.LessonIndexPath(homePath), hook.LessonSources{Skills: []string{skills}}, servingTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hook.LessonSourcesPath(homePath)); !os.IsNotExist(err) {
		t.Fatalf("setup: no sources may be recorded: %v", err)
	}
	if got := served(t, "claude_code", "fixture-runner test flows/"); !strings.Contains(got, "skill_port") {
		t.Fatalf("setup: hand-built index serves the line: %q", got)
	}
	writeServedLesson(t, store, "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	if got := served(t, "claude_code", "fixture-runner test flows/"); got != "" {
		t.Fatalf("without recorded sources the automatic rebuild reads the ledger only: %q", got)
	}
	// The repair door says which line went missing and how to keep it. The
	// write above already dropped it, so rebuild the hand-built state again.
	if _, err := hook.CompileLessonIndex(homePath, hook.LessonIndexPath(homePath), hook.LessonSources{Skills: []string{skills}}, servingTestNow); err != nil {
		t.Fatal(err)
	}
	build, err := hook.RebuildLessonIndex(homePath, servingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	reported := false
	for _, problem := range build.Problems {
		reported = reported || (strings.Contains(problem.Error(), "skill_port") && strings.Contains(problem.Error(), "--skills DIR"))
	}
	if !reported {
		t.Fatalf("a dropped skill-only line must be reported with its fix: %v", build.Problems)
	}
}
