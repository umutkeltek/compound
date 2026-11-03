package promotedriver

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLessonSkill(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAppendLessonToSkillCreatesSectionAndIsIdempotentPerLine(t *testing.T) {
	path := writeLessonSkill(t, "# S\n\nbody\n")
	block := []string{"- 2026-11-03: why. How: how. (lesson claim_abc)"}
	if ok, err := AppendLessonToSkill(path, block); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "# S\n\nbody\n\n## Lessons\n\n- 2026-11-03: why. How: how. (lesson claim_abc)\n" {
		t.Fatalf("unexpected file:\n%q", data)
	}
	if ok, err := AppendLessonToSkill(path, block); err != nil || ok {
		t.Fatalf("same lines again must be a no-op: ok=%v err=%v", ok, err)
	}
	// A re-run that adds a trigger line fills in only the missing line.
	block = append(block, `trigger: {"id":"claim_abc"}`)
	if ok, err := AppendLessonToSkill(path, block); err != nil || !ok {
		t.Fatalf("missing trigger line must be appended: ok=%v err=%v", ok, err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "# S\n\nbody\n\n## Lessons\n\n- 2026-11-03: why. How: how. (lesson claim_abc)\ntrigger: {\"id\":\"claim_abc\"}\n" {
		t.Fatalf("unexpected file after fill-in:\n%q", data)
	}
}

func TestResolveLessonSkillFileFailsClosedOnUnknownSkill(t *testing.T) {
	if _, err := ResolveLessonSkillFile("no-such-skill", []string{t.TempDir()}); err == nil {
		t.Fatal("an unresolved --skill must refuse before any ledger write")
	}
	if _, err := ResolveLessonSkillFile("../escape", []string{t.TempDir()}); err == nil {
		t.Fatal("a path-shaped skill name must be refused")
	}
}
