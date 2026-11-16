package mem

import (
	"fmt"
	"strings"
	"testing"
)

// An expensive or long trigger is refused where it is written: the hook runs
// every pattern on every tool call.
func TestLessonDoorRefusesLongAndExpensiveTriggers(t *testing.T) {
	if _, _, err := ParseLessonTrigger(`Bash:(x?){1000}x{1000}`); err == nil || !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("an 18-byte pattern that compiles to thousands of instructions must be refused: %v", err)
	}
	if _, _, err := ParseLessonTrigger("Bash:" + strings.Repeat("a", LessonPatternMaxBytes+1)); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("a pattern over the index cap must be refused: %v", err)
	}
	var alternatives []string
	for i := 0; len(strings.Join(alternatives, "|")) < 590; i++ {
		alternatives = append(alternatives, fmt.Sprintf("%08x-fixture-%04d", i*7919, i))
	}
	long := "Bash:(" + strings.Join(alternatives, "|") + ")"
	if _, ok, err := ParseLessonTrigger(long); err != nil || !ok {
		t.Fatalf("a cheap literal list under the index cap must parse (lessons written before the door cap keep serving): %v", err)
	}

	s := newStore(t)
	write := func(trigger string) error {
		_, err := s.WriteLesson(LessonInput{Why: "synthetic " + trigger[:12], How: "do the safe thing", Trigger: trigger, Actor: "agent:test"}, lessonVerbNow)
		return err
	}
	if err := write(long); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at most %d", LessonTriggerMaxBytes)) {
		t.Fatalf("a new trigger over the door cap must be refused: %v", err)
	}
	if err := write("Bash:" + strings.Repeat("b", LessonTriggerMaxBytes)); err != nil {
		t.Fatalf("a new trigger at the door cap must be accepted: %v", err)
	}
	if err := write(`Bash:(x?){1000}x{1000}`); err == nil {
		t.Fatal("the door must refuse an expensive trigger")
	}
	ok := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	if _, err := s.AmendLesson(LessonChangeInput{ID: ok.Claim.ID, Trigger: long, Actor: "agent:test"}, lessonVerbNow); err == nil {
		t.Fatal("an amend must apply the door cap to its new trigger")
	}
	if _, err := s.AmendLesson(LessonChangeInput{ID: ok.Claim.ID, Trigger: `Bash:(y?){900}y{900}`, Actor: "agent:test"}, lessonVerbNow); err == nil {
		t.Fatal("an amend must refuse an expensive trigger")
	}
}
