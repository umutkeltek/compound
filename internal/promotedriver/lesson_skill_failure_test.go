package promotedriver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill file that cannot be written does not keep the lesson from being
// served: the index is rebuilt anyway, and the error says the lesson is in
// the ledger, whether the hook serves it, and how to add the missing line.
func TestSkillAppendFailureStillServesAndSaysSo(t *testing.T) {
	_, store := servingTestHome(t)
	skills := t.TempDir()
	dir := filepath.Join(skills, "widget")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Widget\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	code, _, errOut := runLesson(t, store, "--why", "a forced widget run wiped the synthetic cache", "--how", "Run widget --dry-run before --force.",
		"--by", "agent:test", "--skill", "widget", "--skills-dir", skills, "--repo", skills, "--trigger", `Bash:widget\s+--force`)
	if code != 1 || !strings.Contains(errOut, "is in the ledger and the hook serves it (indexed at rank 1)") ||
		!strings.Contains(errOut, "skill line was not written") || !strings.Contains(errOut, "re-run the same command") {
		t.Fatalf("a failed skill append must say where the lesson stands and how to repair: %d\n%s", code, errOut)
	}
	if got := served(t, "claude_code", "widget --force all"); !strings.Contains(got, "Run widget --dry-run before --force.") {
		t.Fatalf("the lesson must be served although its skill line failed: %q", got)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLesson(t, store, "--why", "a forced widget run wiped the synthetic cache", "--how", "Run widget --dry-run before --force.",
		"--by", "agent:test", "--skill", "widget", "--skills-dir", skills, "--repo", skills, "--trigger", `Bash:widget\s+--force`)
	if code != 0 || !strings.Contains(out, "lesson duplicate") || !strings.Contains(out, "(appended)") {
		t.Fatalf("the re-run fills in the missing skill line: %d %s %s", code, out, errOut)
	}
}
