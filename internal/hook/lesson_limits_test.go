package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const explosivePattern = `(x?){1000}x{1000}`

// The builder leaves an expensive pattern out, and the reader drops one that
// an older binary wrote into an index, instead of running it on every call.
func TestIndexBuilderAndReaderDropExpensivePatterns(t *testing.T) {
	good := Lesson{ID: "lesson_good", Tools: []string{"Bash"}, Pattern: `\bxxxx`, Apply: "A cheap lesson."}
	bad := Lesson{ID: "lesson_explosive", Tools: []string{"Bash"}, Pattern: explosivePattern, Apply: "An expensive lesson."}
	if err := ValidateLesson(bad); err == nil || !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("ValidateLesson must refuse the expensive pattern: %v", err)
	}
	index, problems := BuildLessonIndex([]Lesson{bad, good}, lessonTestNow)
	if len(problems) != 1 || len(index.Lessons) != 1 || index.LimitsVersion != LessonIndexLimitsVersion {
		t.Fatalf("builder must leave the expensive pattern out and mark the index checked: %+v %v", index, problems)
	}

	// An index from an older binary: no limits marker, expensive pattern first.
	old := LessonIndex{SchemaVersion: LessonIndexSchemaVersion, BuiltAt: "2026-11-06T00:00:00Z", Lessons: []Lesson{bad, good}}
	data, _ := json.Marshal(old)
	path := filepath.Join(t.TempDir(), "hook-lessons.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	command := strings.Repeat("x", 1500) // the expensive pattern matches this
	got := MatchLessonsFile(path, "Bash", bashInput(command))
	if len(got) != 1 || got[0].ID != "lesson_good" {
		t.Fatalf("the reader must drop the expensive pattern from an unchecked index: %+v", got)
	}
	// A pattern over the length cap is dropped even from a checked index.
	long := Lesson{ID: "lesson_long", Tools: []string{"Bash"}, Pattern: "x" + strings.Repeat("x?", 600), Apply: "Too long."}
	checked := LessonIndex{SchemaVersion: LessonIndexSchemaVersion, BuiltAt: "2026-11-16T00:00:00Z", Lessons: []Lesson{long, good}, LimitsVersion: LessonIndexLimitsVersion}
	data, _ = json.Marshal(checked)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MatchLessonsFile(path, "Bash", bashInput(command)); len(got) != 1 || got[0].ID != "lesson_good" {
		t.Fatalf("the reader must drop an over-length pattern: %+v", got)
	}
}

// A non-shell tool's input is matched on its first 2 KiB only: a trigger must
// not scan a whole file an agent writes.
func TestNonShellSubjectIsCappedAt2KiB(t *testing.T) {
	lessons := []Lesson{{ID: "lesson_marker", Tools: []string{"Write"}, Pattern: `FIXTURE-MARKER`, Apply: "Seen."}}
	near, _ := json.Marshal(map[string]any{"file_path": "/repo/a.txt", "content": "FIXTURE-MARKER " + strings.Repeat("y", 4000)})
	far, _ := json.Marshal(map[string]any{"file_path": "/repo/a.txt", "content": strings.Repeat("y", 2100) + " FIXTURE-MARKER"})
	if got := MatchLessons(lessons, "Write", LessonSubject(near)); len(got) != 1 {
		t.Fatalf("a marker in the first 2 KiB must match: %+v", got)
	}
	if got := MatchLessons(lessons, "Write", LessonSubject(far)); len(got) != 0 {
		t.Fatalf("a marker past 2 KiB must not be scanned: %+v", got)
	}
	if n := len(LessonSubject(far)); n != lessonInputSubjectMaxBytes {
		t.Fatalf("non-shell subject = %d bytes, want %d", n, lessonInputSubjectMaxBytes)
	}
	shell := strings.Repeat("z", 5000) + " FIXTURE-MARKER"
	if got := LessonSubject(bashInput(shell)); got != shell {
		t.Fatal("a shell command keeps its own (16 KiB) cap")
	}
}

// Past the match budget a tool call gets no lessons, at once, and the hook
// says so in its log and its spool line. The budget is simulated (a matcher
// that never finishes, a deadline that has already fired), so the test does
// not depend on how fast the machine is.
func TestLessonMatchingHasAHardTimeBudget(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	writeLedgerLesson(t, store, "a forced widget run wiped the cache", "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	if _, err := RebuildLessonIndex(homePath, ledgerTestNow); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	matcher, deadline := spoolLessonMatcher, spoolMatchDeadline
	defer func() { spoolLessonMatcher, spoolMatchDeadline = matcher, deadline }()
	spoolLessonMatcher = func(string, string, json.RawMessage) ([]LessonMatch, string) {
		<-release
		return []LessonMatch{{ID: "never", Apply: "never"}}, ""
	}
	fired := make(chan time.Time)
	close(fired)
	spoolMatchDeadline = func() <-chan time.Time { return fired }

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- RunSpoolCommand("claude_code", "pre-tool-use", bytes.NewReader(spoolInput("widget --force all")), &stdout, &stderr, ledgerTestNow,
			func(lessons string) any { return map[string]string{"additionalContext": lessons} })
	}()
	select {
	case code := <-done:
		if code != 0 || strings.TrimSpace(stdout.String()) != `{"additionalContext":""}` || !strings.Contains(stderr.String(), "ran past its") {
			t.Fatalf("past the budget: code=%d stdout=%s stderr=%s", code, &stdout, &stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hook waited for a matcher past its budget")
	}
	lines, _ := filepath.Glob(filepath.Join(SpoolDir(homePath), "*.ndjson"))
	data, _ := os.ReadFile(lines[0])
	var line SpoolLine
	if err := json.Unmarshal(bytes.TrimSpace(data), &line); err != nil || line.LessonIndex != LessonMatchTimeout || len(line.Lessons) != 0 {
		t.Fatalf("spool line must record the timeout: %+v %v", line, err)
	}

	// Within the budget the real matcher answers as before.
	spoolLessonMatcher = matcher
	spoolMatchDeadline = func() <-chan time.Time { return nil }
	stdout.Reset()
	if code := RunSpoolCommand("claude_code", "pre-tool-use", bytes.NewReader(spoolInput("widget --force all")), &stdout, &stderr, ledgerTestNow,
		func(lessons string) any { return map[string]string{"additionalContext": lessons} }); code != 0 || !strings.Contains(stdout.String(), "Run widget --dry-run") {
		t.Fatalf("within the budget: %d %s", code, &stdout)
	}
}
