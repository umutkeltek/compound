package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var lessonTestNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func writeTestLessonIndex(t *testing.T, lessons []Lesson) string {
	t.Helper()
	index, problems := BuildLessonIndex(lessons, lessonTestNow)
	if len(problems) != 0 {
		t.Fatalf("build index problems: %v", problems)
	}
	path := filepath.Join(t.TempDir(), "indexes", "hook-lessons.json")
	if err := WriteLessonIndex(path, index); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return path
}

func testLessons() []Lesson {
	return []Lesson{
		{ID: "lesson_replace_deletes_first", Tools: []string{"Bash"}, Pattern: `\basc\s+screenshots\b.*--replace`, Apply: "--replace deletes the live set before the upload succeeds; upload a new set first."},
		{ID: "lesson_tail_hides_exit", Tools: []string{"Bash"}, Pattern: `\|\s*tail\b`, Apply: "A pipe into tail reports tail's exit code; redirect to a log and echo EXIT=$?."},
		{ID: "lesson_edit_generated", Tools: []string{"Edit", "Write"}, Pattern: `generated/`, Apply: "Files under generated/ are rebuilt; edit the source instead."},
	}
}

func toolInput(t *testing.T, value map[string]any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestMatchLessonsFileInjectsOnlyTheMatchingTrigger(t *testing.T) {
	path := writeTestLessonIndex(t, testLessons())

	got := MatchLessonsFile(path, "Bash", toolInput(t, map[string]any{"command": "asc screenshots upload --app 1 --replace ./shots"}))
	if len(got) != 1 || got[0].ID != "lesson_replace_deletes_first" {
		t.Fatalf("matching call = %+v, want only the replace lesson", got)
	}
	if ctx := LessonContext(got); !strings.Contains(ctx, "deletes the live set") || !strings.HasPrefix(ctx, "Compound lesson (lesson_replace_deletes_first): ") {
		t.Fatalf("lesson context = %q", ctx)
	}

	if got := MatchLessonsFile(path, "Bash", toolInput(t, map[string]any{"command": "go test ./internal/hook"})); len(got) != 0 {
		t.Fatalf("unrelated call injected %+v", got)
	}
	// Same text on a tool the lesson does not name: no injection.
	if got := MatchLessonsFile(path, "Read", toolInput(t, map[string]any{"file_path": "asc screenshots --replace"})); len(got) != 0 {
		t.Fatalf("tool filter ignored: %+v", got)
	}
	// Other harnesses name the shell tool differently; a Bash lesson still fires.
	if got := MatchLessonsFile(path, "exec_command", toolInput(t, map[string]any{"cmd": []string{"bash", "-lc", "make 2>&1 | tail -5"}})); len(got) != 1 || got[0].ID != "lesson_tail_hides_exit" {
		t.Fatalf("codex shell alias = %+v", got)
	}
	// Non-shell tools match against their whole input.
	if got := MatchLessonsFile(path, "Write", toolInput(t, map[string]any{"file_path": "/repo/generated/api.go", "content": "x"})); len(got) != 1 || got[0].ID != "lesson_edit_generated" {
		t.Fatalf("write tool = %+v", got)
	}
	// Two lessons in one command are both surfaced, in index order.
	got = MatchLessonsFile(path, "Bash", toolInput(t, map[string]any{"command": "asc screenshots upload --replace 2>&1 | tail -3"}))
	if len(got) != 2 || got[0].ID != "lesson_replace_deletes_first" || got[1].ID != "lesson_tail_hides_exit" {
		t.Fatalf("two-lesson call = %+v", got)
	}
}

func TestMatchLessonsCapsInjectedLessonsPerCall(t *testing.T) {
	lessons := []Lesson{}
	for _, id := range []string{"a", "b", "c", "d"} {
		lessons = append(lessons, Lesson{ID: "lesson_" + id, Pattern: "deploy", Apply: "line " + id})
	}
	got := MatchLessons(lessons, "Bash", "deploy now")
	if len(got) != LessonMaxInjected {
		t.Fatalf("matches = %d, want cap %d", len(got), LessonMaxInjected)
	}
}

func TestMatchLessonsFileDegradesToNoInjection(t *testing.T) {
	input := toolInput(t, map[string]any{"command": "asc screenshots upload --replace"})
	dir := t.TempDir()

	if got := MatchLessonsFile(filepath.Join(dir, "missing.json"), "Bash", input); got != nil {
		t.Fatalf("missing index matched %+v", got)
	}
	garbage := filepath.Join(dir, "garbage.json")
	if err := os.WriteFile(garbage, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MatchLessonsFile(garbage, "Bash", input); got != nil {
		t.Fatalf("garbage index matched %+v", got)
	}
	wrongSchema := filepath.Join(dir, "wrong.json")
	doc, _ := json.Marshal(LessonIndex{SchemaVersion: "other.v9", Lessons: testLessons()})
	if err := os.WriteFile(wrongSchema, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MatchLessonsFile(wrongSchema, "Bash", input); got != nil {
		t.Fatalf("wrong-schema index matched %+v", got)
	}
	// An oversized index is ignored rather than read slowly.
	big := LessonIndex{SchemaVersion: LessonIndexSchemaVersion, Lessons: testLessons()}
	pad := strings.Repeat("x", LessonIndexMaxBytes)
	big.Lessons = append(big.Lessons, Lesson{ID: "pad", Pattern: "zzz", Apply: "pad", Source: pad})
	oversized := filepath.Join(dir, "big.json")
	encoded, _ := json.Marshal(big)
	if err := os.WriteFile(oversized, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MatchLessonsFile(oversized, "Bash", input); got != nil {
		t.Fatalf("oversized index matched %+v", got)
	}
	// A lesson with a bad pattern that slipped into the file is skipped; the
	// good ones still fire.
	mixed := LessonIndex{SchemaVersion: LessonIndexSchemaVersion, Lessons: append([]Lesson{{ID: "bad", Pattern: "(", Apply: "x"}}, testLessons()...)}
	mixedPath := filepath.Join(dir, "mixed.json")
	encoded, _ = json.Marshal(mixed)
	if err := os.WriteFile(mixedPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MatchLessonsFile(mixedPath, "Bash", input); len(got) != 1 || got[0].ID != "lesson_replace_deletes_first" {
		t.Fatalf("mixed index = %+v", got)
	}
}

func TestBuildLessonIndexRejectsLessonsThatBreakTheContract(t *testing.T) {
	lessons := append(testLessons(),
		Lesson{ID: "too_long", Pattern: "x", Apply: strings.Repeat("a", LessonApplyMaxBytes+1)},
		Lesson{ID: "two_lines", Pattern: "x", Apply: "one\ntwo"},
		Lesson{ID: "bad_regex", Pattern: "(", Apply: "x"},
		Lesson{ID: "", Pattern: "x", Apply: "x"},
		Lesson{ID: "lesson_tail_hides_exit", Pattern: "y", Apply: "duplicate id"},
	)
	index, problems := BuildLessonIndex(lessons, lessonTestNow)
	if len(index.Lessons) != 3 {
		t.Fatalf("kept %d lessons, want the 3 valid ones: %+v", len(index.Lessons), index.Lessons)
	}
	if len(problems) != 5 {
		t.Fatalf("problems = %d, want 5: %v", len(problems), problems)
	}
	// Exactly at the cap is allowed.
	if err := ValidateLesson(Lesson{ID: "edge", Pattern: "x", Apply: strings.Repeat("a", LessonApplyMaxBytes)}); err != nil {
		t.Fatalf("300-byte apply rejected: %v", err)
	}
}

func TestParseLessonsJSONAcceptsProducerSpellings(t *testing.T) {
	doc := `{"lessons":[{"id":"l1","tool":"Bash","trigger":"maestro\\s+test","how_to_apply":"use the wrapper"}]}`
	lessons, err := ParseLessonsJSON([]byte(doc), "fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 || lessons[0].Pattern != `maestro\s+test` || lessons[0].Apply != "use the wrapper" || len(lessons[0].Tools) != 1 || lessons[0].Source != "fixture.json" {
		t.Fatalf("lessons = %+v", lessons)
	}
	bare := `[{"id":"l2","pattern":"x","apply":"y"}]`
	lessons, err = ParseLessonsJSON([]byte(bare), "bare.json")
	if err != nil || len(lessons) != 1 || lessons[0].ID != "l2" {
		t.Fatalf("bare array = %+v, %v", lessons, err)
	}
}

func TestParseSkillTriggerLinesReadsJSONTriggersAndReportsBadOnes(t *testing.T) {
	dir := t.TempDir()
	skill := strings.Join([]string{
		"# Example skill",
		"Some prose that mentions trigger: in the middle is not a trigger line.",
		`trigger: {"id":"skill_port","tools":["Bash"],"pattern":"\\bmaestro\\s+test\\b","apply":"Run the wrapper; bare runs hang on an occupied port."}`,
		"- trigger: not json at all",
		"",
	}, "\n")
	if err := os.MkdirAll(filepath.Join(dir, "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "example", "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	lessons, problems := ParseSkillTriggerLines(dir)
	if len(lessons) != 1 || lessons[0].ID != "skill_port" || !strings.HasSuffix(lessons[0].Source, "example/SKILL.md:3") {
		t.Fatalf("lessons = %+v", lessons)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "SKILL.md:4") {
		t.Fatalf("problems = %v", problems)
	}
	got := MatchLessons(lessons, "Bash", "maestro test flows/")
	if len(got) != 1 {
		t.Fatalf("skill trigger did not fire: %+v", got)
	}
}

// BenchmarkMatchLessonsFile measures the whole in-process injection path: one
// file read, JSON decode, and regex match over a realistic index size.
func BenchmarkMatchLessonsFile(b *testing.B) {
	lessons := benchmarkLessons(200)
	index, _ := BuildLessonIndex(lessons, lessonTestNow)
	path := filepath.Join(b.TempDir(), "hook-lessons.json")
	if err := WriteLessonIndex(path, index); err != nil {
		b.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"command": "asc screenshots upload --app 1 --replace ./shots"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := MatchLessonsFile(path, "Bash", input); len(got) == 0 {
			b.Fatal("benchmark lesson did not match")
		}
	}
}

func benchmarkLessons(n int) []Lesson {
	lessons := testLessons()
	for i := len(lessons); i < n; i++ {
		lessons = append(lessons, Lesson{
			ID:      "lesson_filler_" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + time.Duration(i).String(),
			Tools:   []string{"Bash"},
			Pattern: `\bfiller-tool-` + string(rune('a'+i%26)) + `\s+--flag-[0-9]+`,
			Apply:   "Filler lesson used to size the index like a real one.",
		})
	}
	return lessons
}

// TestMatchLessonsFileStaysFarUnderTheHookBudget fails if the injection path
// grows real work (more IO, per-call rebuilds): the hook's whole budget is
// 50 ms per call including process start, so the match itself must stay a
// small fraction of it even on a loaded machine.
func TestMatchLessonsFileStaysFarUnderTheHookBudget(t *testing.T) {
	result := testing.Benchmark(BenchmarkMatchLessonsFile)
	perCall := time.Duration(result.NsPerOp())
	t.Logf("MatchLessonsFile with 200 lessons: %s/op over %d ops", perCall, result.N)
	if perCall > 10*time.Millisecond {
		t.Fatalf("lesson match takes %s per call; budget for the whole hook is 50ms", perCall)
	}
}
