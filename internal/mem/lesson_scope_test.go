package mem

import (
	"strings"
	"testing"
)

// A trigger that fires on ordinary calls is refused unless its author says
// so by name; the override is recorded on the lesson.
func TestLessonDoorRefusesCatchAllTriggersWithoutOverride(t *testing.T) {
	s := newStore(t)
	write := func(trigger string, allow bool) (LessonResult, error) {
		return s.WriteLesson(LessonInput{Why: "synthetic catch-all " + trigger, How: "be careful", Trigger: trigger, Actor: "agent:test", AllowBroadTrigger: allow}, lessonVerbNow)
	}
	for _, trigger := range []string{`*:.`, `Bash:git`, `Bash:\bls\b`, `Read:.`, `*:\{`, `exec_command:status`} {
		if _, err := write(trigger, false); err == nil || !strings.Contains(err.Error(), "fires on ordinary calls") || !strings.Contains(err.Error(), "--allow-broad-trigger") {
			t.Fatalf("catch-all %s must be refused with the override named: %v", trigger, err)
		}
	}
	res, err := write(`*:.`, true)
	if err != nil || res.Claim.Qualifiers[LessonBroadTriggerQualifier] != "allowed" {
		t.Fatalf("the override must accept and record the catch-all: %+v %v", res.Claim.Qualifiers, err)
	}
	specific, err := write(`Bash:widget\s+--force`, false)
	if err != nil || specific.Claim.Qualifiers[LessonBroadTriggerQualifier] != "" {
		t.Fatalf("a specific trigger needs no override: %+v %v", specific.Claim.Qualifiers, err)
	}
	if _, err := s.AmendLesson(LessonChangeInput{ID: specific.Claim.ID, Trigger: `Bash:.`, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "--allow-broad-trigger") {
		t.Fatalf("an amend to a catch-all must be refused: %v", err)
	}
	amended, err := s.AmendLesson(LessonChangeInput{ID: specific.Claim.ID, Trigger: `Bash:.`, AllowBroadTrigger: true, Actor: "agent:test"}, lessonVerbNow)
	if err != nil || amended.Claim.Qualifiers[LessonBroadTriggerQualifier] != "allowed" {
		t.Fatalf("an amend with the override records it: %+v %v", amended.Claim.Qualifiers, err)
	}
	back, err := s.AmendLesson(LessonChangeInput{ID: amended.Claim.ID, Trigger: `Bash:widget\s+--force`, Actor: "agent:test"}, lessonVerbNow)
	if err != nil || back.Claim.Qualifiers[LessonBroadTriggerQualifier] != "" {
		t.Fatalf("the broad record follows the trigger it describes: %+v %v", back.Claim.Qualifiers, err)
	}
}

// Owner names are reserved: a typed owner name must never look like owner
// approval, so no lesson verb accepts one.
func TestLessonVerbsRefuseOwnerNames(t *testing.T) {
	s := newStore(t)
	ok := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	for _, actor := range []string{"operator:owner", "Operator", "human:someone", "HUMAN-reviewer", "operator-codex"} {
		if _, err := s.WriteLesson(LessonInput{Why: "synthetic owner " + actor, How: "x", Actor: actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("write by %q must be refused: %v", actor, err)
		}
		if _, err := s.RetireLesson(LessonChangeInput{ID: ok.Claim.ID, Reason: "x", Actor: actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("retire by %q must be refused: %v", actor, err)
		}
		if _, err := s.AmendLesson(LessonChangeInput{ID: ok.Claim.ID, How: "y", Actor: actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("amend by %q must be refused: %v", actor, err)
		}
		if _, err := s.RestoreLesson(LessonChangeInput{ID: ok.Claim.ID, Actor: actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("restore by %q must be refused: %v", actor, err)
		}
	}
	if _, err := s.RetireLesson(LessonChangeInput{ID: ok.Claim.ID, Reason: "done", Actor: "agent:operator-helper"}, lessonVerbNow); err != nil {
		t.Fatalf("an agent name that merely contains the word is fine: %v", err)
	}
}
