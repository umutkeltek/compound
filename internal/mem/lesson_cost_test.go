package mem

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeTimer replaces LessonMatchTimer for one test: cost(pattern, subject
// size, call number for that pattern) decides each run's time.
func fakeTimer(t *testing.T, cost func(pattern string, size, call int) time.Duration) map[string]int {
	t.Helper()
	calls := map[string]int{}
	original := LessonMatchTimer
	LessonMatchTimer = func(re *regexp.Regexp, subject string) time.Duration {
		calls[re.String()]++
		return cost(re.String(), len(subject), calls[re.String()])
	}
	t.Cleanup(func() { LessonMatchTimer = original })
	return calls
}

// The cost check takes the best of several runs (a loaded machine can stall
// one), measures only the subjects the tool sees, and names the subject and
// the limit when it refuses. No wall clock is involved.
func TestLessonPatternCostTakesTheBestOfSeveralRuns(t *testing.T) {
	sizes := map[string]map[int]bool{}
	calls := fakeTimer(t, func(pattern string, size, call int) time.Duration {
		if sizes[pattern] == nil {
			sizes[pattern] = map[int]bool{}
		}
		sizes[pattern][size] = true
		switch pattern {
		case "stalled-once":
			if call == 1 {
				return time.Second // one stalled run
			}
			return time.Microsecond
		case "slow-shape":
			return 50 * time.Millisecond
		}
		return time.Microsecond
	})
	if cost, err := MeasureLessonPattern("stalled-once", true, false); err != nil || cost.Slow() {
		t.Fatalf("one stalled run must not refuse a fast pattern: %+v %v", cost, err)
	}
	cost, err := MeasureLessonPattern("slow-shape", true, false)
	if err != nil || !cost.Slow() || calls["slow-shape"] != lessonCostRuns {
		t.Fatalf("a pattern slow on every run is refused after %d runs: %+v calls=%d %v", lessonCostRuns, cost, calls["slow-shape"], err)
	}
	if msg := cost.Error(); !strings.Contains(msg, "16 KiB") || !strings.Contains(msg, "limit is 6.4ms") {
		t.Fatalf("the refusal names the subject and the limit: %q", msg)
	}
	if _, err := MeasureLessonPattern("write-only", false, true); err != nil || len(sizes["write-only"]) != 1 || !sizes["write-only"][LessonInputSubjectMaxBytes] {
		t.Fatalf("a non-shell trigger is measured on %d-byte subjects only: %v", LessonInputSubjectMaxBytes, sizes["write-only"])
	}
	for _, tc := range []struct {
		tool            string
		shell, nonShell bool
	}{{"Bash", true, false}, {"exec_command", true, false}, {"Write", false, true}, {"*", true, true}} {
		if shell, nonShell := LessonTriggerSubjects(tc.tool); shell != tc.shell || nonShell != tc.nonShell {
			t.Fatalf("%s subjects = %v/%v", tc.tool, shell, nonShell)
		}
	}
}

// The lesson door and amend refuse a trigger the measurer calls slow.
func TestLessonDoorRefusesASlowTrigger(t *testing.T) {
	fakeTimer(t, func(pattern string, size, call int) time.Duration {
		if strings.Contains(pattern, "slow-shape") {
			return 50 * time.Millisecond
		}
		return time.Microsecond
	})
	s := newStore(t)
	if _, err := s.WriteLesson(LessonInput{Why: "synthetic slow", How: "x", Trigger: `Bash:slow-shape`, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "costs 50ms") {
		t.Fatalf("a slow trigger is refused at the door: %v", err)
	}
	ok := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	if _, err := s.AmendLesson(LessonChangeInput{ID: ok.Claim.ID, Trigger: `Write:slow-shape`, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "limit is 800") {
		t.Fatalf("an amend to a slow trigger is refused: %v", err)
	}
}

// Real clock, with patterns at least ten times either side of the limit: an
// ordinary trigger costs microseconds; the nested-optional shape costs about
// 75 ms on a 16 KiB command and 9 ms on 2 KiB (limits 6.4 ms and 0.8 ms).
func TestLessonPatternCostRealClock(t *testing.T) {
	for _, tc := range []struct {
		trigger string
		slow    bool
	}{
		{`Bash:widget\s+--force`, false},
		{`Write:generated/`, false},
		{`Bash:(?:\w?\s?){225}#N`, true},
		{`Write:(?:\w?\s?){225}#1`, true},
	} {
		trigger, _, err := ParseLessonTrigger(tc.trigger)
		if err != nil {
			t.Fatal(err)
		}
		shell, nonShell := LessonTriggerSubjects(trigger.Tool)
		cost, err := MeasureLessonPattern(trigger.Pattern, shell, nonShell)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-28s worst %10s on %2d KiB %-10s limit %s", tc.trigger, cost.Worst, cost.Bytes>>10, cost.Subject, cost.Limit)
		if cost.Slow() != tc.slow {
			t.Fatalf("%s: slow=%v, want %v (%+v)", tc.trigger, cost.Slow(), tc.slow, cost)
		}
	}
}
