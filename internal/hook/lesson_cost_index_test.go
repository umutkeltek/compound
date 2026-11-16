package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

func fakeLessonTimer(t *testing.T, slow func(pattern string) bool) map[string]int {
	t.Helper()
	calls := map[string]int{}
	original := mem.LessonMatchTimer
	mem.LessonMatchTimer = func(re *regexp.Regexp, subject string) time.Duration {
		calls[re.String()]++
		if slow(re.String()) {
			return 80 * time.Millisecond
		}
		return 2 * time.Microsecond
	}
	t.Cleanup(func() { mem.LessonMatchTimer = original })
	return calls
}

// A slow trigger already in the ledger (written before the door measured
// cost) is left out at build and reported; its cost is remembered so the
// next automatic build does not time it again, and the repair door measures
// everything again.
func TestSlowLedgerTriggerIsLeftOutAtBuild(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	calls := fakeLessonTimer(t, func(pattern string) bool { return strings.HasPrefix(pattern, "slow-shape") })
	guard := writeLedgerLesson(t, store, "generated files are rebuilt", "Edit the generator, not generated/.", `Write:generated/`)
	appendRawLessonClaim(t, homePath, 2001, "claim_bbbbbbbbbbb1", "Slow advice.", `Write:slow-shape-1`, "agent:test")

	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if build.Serves("claim_bbbbbbbbbbb1") || !build.Serves(guard.ID) {
		t.Fatalf("the slow trigger must be left out and the guard served: %+v", build.Index.Lessons)
	}
	reported := false
	for _, problem := range build.Problems {
		reported = reported || (strings.Contains(problem.Error(), "claim_bbbbbbbbbbb1") && strings.Contains(problem.Error(), "left out"))
	}
	if !reported || len(build.Index.SlowPatterns) != 1 || build.Index.LimitsVersion != LessonIndexLimitsVersion {
		t.Fatalf("the slow trigger must be reported and remembered: %v %+v", build.Problems, build.Index.SlowPatterns)
	}
	for _, lesson := range build.Index.Lessons {
		if lesson.CostMicros < 1 {
			t.Fatalf("every served entry carries its measured cost: %+v", lesson)
		}
	}
	before, guardBefore := calls["slow-shape-1"], calls["generated/"]
	if _, err := RebuildLessonIndex(homePath, ledgerTestNow); err != nil || calls["slow-shape-1"] != before || calls["generated/"] != guardBefore {
		t.Fatalf("an automatic rebuild reuses measured costs (slow timed %d -> %d, guard %d -> %d)", before, calls["slow-shape-1"], guardBefore, calls["generated/"])
	}
	var out, errOut bytes.Buffer
	if code := RunLessonsIndexCommand(nil, &out, &errOut); code != 1 || calls["slow-shape-1"] <= before {
		t.Fatalf("the repair door measures again and still refuses: %d %s %s", code, &out, &errOut)
	}
}

// When the budget runs out, the hook serves what matched before the slow
// pattern instead of nothing. Patterns run cheapest first, so a cheap guard
// is evaluated before a slower lesson that outranks it.
func TestBudgetServesWhatMatchedBeforeASlowPattern(t *testing.T) {
	homePath, _ := ledgerTestHome(t)
	index := LessonIndex{SchemaVersion: LessonIndexSchemaVersion, BuiltAt: "2026-11-16T09:00:00Z", LimitsVersion: LessonIndexLimitsVersion, Lessons: []Lesson{
		{ID: "claim_cccccccccc01", Tools: []string{"Write"}, Pattern: `block-me`, Apply: "Slower lesson, ranked first.", CostMicros: 700},
		{ID: "claim_cccccccccc02", Tools: []string{"Write"}, Pattern: `generated/`, Apply: "Edit the generator, not generated/.", CostMicros: 3},
	}}
	if err := WriteLessonIndex(LessonIndexPath(homePath), index); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	deadline := make(chan time.Time)
	matcher, budget := lessonPatternMatches, spoolMatchDeadline
	defer func() { lessonPatternMatches, spoolMatchDeadline = matcher, budget }()
	lessonPatternMatches = func(re *regexp.Regexp, subject string) bool {
		if re.String() == "block-me" {
			close(started)
			<-release
		}
		return re.MatchString(subject)
	}
	spoolMatchDeadline = func() <-chan time.Time {
		go func() { <-started; close(deadline) }()
		return deadline
	}
	input, _ := json.Marshal(map[string]any{"tool_name": "Write", "session_id": "s", "tool_input": map[string]any{"file_path": "/repo/generated/api.go", "content": "block-me"}})
	var stdout, stderr bytes.Buffer
	code := RunSpoolCommand("claude_code", "pre-tool-use", bytes.NewReader(input), &stdout, &stderr, ledgerTestNow,
		func(lessons string) any { return map[string]string{"additionalContext": lessons} })
	if code != 0 || !strings.Contains(stdout.String(), "claim_cccccccccc02") || strings.Contains(stdout.String(), "claim_cccccccccc01") {
		t.Fatalf("the guard matched before the slow pattern must be served: %s", &stdout)
	}
	if !strings.Contains(stderr.String(), "while lesson claim_cccccccccc01 was running") || !strings.Contains(stderr.String(), "served the 1 lessons") {
		t.Fatalf("the log must name the running pattern: %s", &stderr)
	}
}

// The reviewer's reproduction, real clock: twelve slow Write triggers from an
// older binary sit in the ledger next to a guard on generated/. The build
// leaves the twelve out, so every Write call stays fast and the guard is
// served.
func TestTwelveSlowWriteTriggersDoNotSilenceAGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("times real regex matches")
	}
	homePath, store := ledgerTestHome(t)
	guard := writeLedgerLesson(t, store, "generated files are rebuilt", "Edit the generator, not generated/.", `Write:generated/`)
	for i := 1; i <= 12; i++ {
		appendRawLessonClaim(t, homePath, int64(3000+i), fmt.Sprintf("claim_dddddddddd%02d", i), "Slow advice.", fmt.Sprintf(`Write:(?:\w?\s?){225}#%d`, i), "agent:test")
	}
	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(build.Index.Lessons) != 1 || !build.Serves(guard.ID) || len(build.Index.SlowPatterns) != 12 {
		t.Fatalf("the twelve slow triggers must be left out: %d indexed, %d slow, problems %v", len(build.Index.Lessons), len(build.Index.SlowPatterns), build.Problems)
	}
	content := strings.Repeat("word ", 400)
	input, _ := json.Marshal(map[string]any{"tool_name": "Write", "session_id": "s", "tool_input": map[string]any{"file_path": "/repo/generated/api.go", "content": content}})
	var stdout, stderr bytes.Buffer
	RunSpoolCommand("claude_code", "pre-tool-use", bytes.NewReader(input), &stdout, &stderr, ledgerTestNow,
		func(lessons string) any { return map[string]string{"additionalContext": lessons} })
	if !strings.Contains(stdout.String(), guard.ID) || strings.Contains(stderr.String(), "budget") {
		t.Fatalf("the guard must be served within the budget: %s %s", &stdout, &stderr)
	}
}
