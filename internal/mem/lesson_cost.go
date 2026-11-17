package mem

// lesson_cost.go — what a trigger pattern really costs.
//
// The hook runs every trigger on every tool call. The static caps (length,
// compiled instructions) do not predict cost: `(?:\w?\s?){225}#N` compiles to
// about 900 instructions, like a harmless 594-byte literal list, but costs
// about 74 ms on a 16 KiB command against 1.4 ms. So the door and the index
// builder time each pattern against fixed worst-case subjects and refuse one
// over a per-KiB limit.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// LessonShellSubjectMaxBytes and LessonInputSubjectMaxBytes are the most
	// the hook matches of a shell command and of a non-shell tool's input. The
	// hook truncates to these; the cost check measures at these sizes.
	LessonShellSubjectMaxBytes = 16 << 10
	LessonInputSubjectMaxBytes = 2 << 10

	// LessonPatternCostPerKiB is the most a trigger may cost per KiB of
	// worst-case subject: 6.4 ms on a 16 KiB command, 0.8 ms on 2 KiB of
	// input. Measured (minimum of 5 runs, loaded machine): ordinary triggers
	// cost 0.1-15 us per KiB, the slowest trigger in real use about 90 us per
	// KiB, the known-slow shape `(?:\w?\s?){225}#N` about 4,600 us per KiB.
	// The limit sits about 4x above real use and 11x below the slow shape.
	LessonPatternCostPerKiB = 400 * time.Microsecond

	// lessonCostRuns is how often one subject is timed before a pattern is
	// called slow. A loaded machine can stall one run; it rarely stalls five.
	lessonCostRuns = 5
)

// LessonMatchTimer times one match of re against subject. Tests replace it so
// cost decisions do not depend on how fast the machine is.
var LessonMatchTimer = func(re *regexp.Regexp, subject string) time.Duration {
	start := time.Now()
	re.MatchString(subject)
	return time.Since(start)
}

// LessonPatternCost is the measured cost of one pattern.
type LessonPatternCost struct {
	// Worst is the slowest subject's best (minimum) time.
	Worst time.Duration
	// Subject names that stress subject; Bytes is its size and Limit the
	// limit at that size.
	Subject string
	Bytes   int
	Limit   time.Duration
}

// Slow reports whether the pattern costs more than its limit.
func (c LessonPatternCost) Slow() bool { return c.Worst > c.Limit }

// Error explains a slow pattern.
func (c LessonPatternCost) Error() string {
	return fmt.Sprintf("trigger regex costs %s on a worst-case %d KiB %s subject; the limit is %s (it would slow every tool call)",
		c.Worst.Round(10*time.Microsecond), c.Bytes>>10, c.Subject, c.Limit)
}

type lessonStressSubject struct {
	name string
	text string
}

var (
	lessonStressOnce               sync.Once
	lessonShellStress, lessonInput []lessonStressSubject
)

// lessonStressSubjects are fixed, deterministic worst-case inputs: text that
// makes optional repeats branch (word, space, word, ...), an ordinary shell
// command repeated, pseudo-random printable bytes, and for non-shell tools a
// file-writing payload.
func lessonStressSubjects(shell, nonShell bool) []lessonStressSubject {
	lessonStressOnce.Do(func() {
		fill := func(unit string, n int) string { return strings.Repeat(unit, n/len(unit)+1)[:n] }
		random := make([]byte, LessonShellSubjectMaxBytes)
		x := uint32(0x92d68ca2) // xorshift32 seed; written in hex because the redact gate reads the decimal form as a phone number
		for i := range random {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			random[i] = byte(32 + x%95)
		}
		shellText := map[string]string{
			"word-space": fill("a ", LessonShellSubjectMaxBytes),
			"command":    fill("cd /repo/project && go test ./internal/... -run TestThing -count=1 2>&1 | tail -n 40; ", LessonShellSubjectMaxBytes),
			"random":     string(random),
		}
		for _, name := range []string{"word-space", "command", "random"} {
			lessonShellStress = append(lessonShellStress, lessonStressSubject{name, shellText[name]})
			lessonInput = append(lessonInput, lessonStressSubject{name, shellText[name][:LessonInputSubjectMaxBytes]})
		}
		lessonInput = append(lessonInput, lessonStressSubject{"file-write", fill(`{"file_path":"/repo/a/b.go","content":"package main\n\nfunc main() { println(\"hi\") }\n"}`, LessonInputSubjectMaxBytes)})
	})
	var out []lessonStressSubject
	if shell {
		out = append(out, lessonShellStress...)
	}
	if nonShell {
		out = append(out, lessonInput...)
	}
	return out
}

// LessonTriggerSubjects says which subjects a trigger tool is matched on: a
// shell tool sees commands, any other tool its input, and "*" both.
func LessonTriggerSubjects(tool string) (shell, nonShell bool) {
	if strings.TrimSpace(tool) == "*" {
		return true, true
	}
	if IsShellToolName(tool) {
		return true, false
	}
	return false, true
}

// MeasureLessonPattern times pattern on the worst-case subjects. Each subject
// is timed until one run is under its limit, at most lessonCostRuns times;
// the first subject whose best run is over its limit ends the measurement.
func MeasureLessonPattern(pattern string, shell, nonShell bool) (LessonPatternCost, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return LessonPatternCost{}, err
	}
	var worst LessonPatternCost
	for _, subject := range lessonStressSubjects(shell, nonShell) {
		limit := LessonPatternCostPerKiB * time.Duration(len(subject.text)) / 1024
		best := time.Duration(-1)
		for run := 0; run < lessonCostRuns; run++ {
			if took := LessonMatchTimer(re, subject.text); best < 0 || took < best {
				best = took
			}
			if best <= limit {
				break
			}
		}
		cost := LessonPatternCost{Worst: best, Subject: subject.name, Bytes: len(subject.text), Limit: limit}
		// Compare by share of the limit, so a 2 KiB and a 16 KiB subject rank fairly.
		if worst.Limit == 0 || best*worst.Limit > worst.Worst*limit {
			worst = cost
		}
		if cost.Slow() {
			return cost, nil
		}
	}
	return worst, nil
}

// CheckLessonTriggerCost refuses a trigger that costs more than the limit.
func CheckLessonTriggerCost(trigger LessonTrigger) error {
	shell, nonShell := LessonTriggerSubjects(trigger.Tool)
	cost, err := MeasureLessonPattern(trigger.Pattern, shell, nonShell)
	if err != nil {
		return err
	}
	if cost.Slow() {
		return fmt.Errorf("%s", cost.Error())
	}
	return nil
}
