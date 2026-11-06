package promotedriver

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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

func TestAppendLessonToSkillConcurrentPublish(t *testing.T) {
	path := writeLessonSkill(t, "# S\n\n"+strings.Repeat("existing text\n", 8000))
	// Both callers start from one generation while a prior publisher holds the
	// stable per-file lock. Neither may report success before it can publish.
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(path), ".SKILL.md.lesson.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 2)
	ready := make(chan struct{}, 2)
	alias := filepath.Join(t.TempDir(), "alias.md")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for i, line := range []string{"lesson A", "lesson B"} {
		target := path
		if i == 1 {
			target = alias
		}
		go func(target, line string) {
			ready <- struct{}{}
			ok, err := AppendLessonToSkill(target, []string{line})
			results <- result{ok, err}
		}(target, line)
	}
	pending := 2
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		for pending > 0 {
			select {
			case <-results:
				pending--
			case <-time.After(5 * time.Second):
				t.Error("publisher cleanup timed out")
				return
			}
		}
	}()
	<-ready
	<-ready
	select {
	case result := <-results:
		pending--
		t.Fatalf("writer returned before prior publisher released the skill: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case result := <-results:
			pending--
			if result.err != nil || !result.ok {
				t.Fatalf("publication: %+v", result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("publisher did not finish")
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"lesson A", "lesson B"} {
		if strings.Count(string(body), "\n"+line+"\n") != 1 {
			t.Fatalf("%s lost or duplicated", line)
		}
		if changed, err := AppendLessonToSkill(path, []string{line}); err != nil || changed {
			t.Fatalf("retry %q: %v %v", line, changed, err)
		}
	}
}
