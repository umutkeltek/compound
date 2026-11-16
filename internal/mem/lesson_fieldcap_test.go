package mem

import (
	"strings"
	"testing"
)

// Every field the hook index carries is bounded at the door.
func TestLessonDoorBoundsEveryIndexedField(t *testing.T) {
	s := newStore(t)
	write := func(in LessonInput) error {
		in.Actor = strings.TrimSpace(in.Actor)
		if in.Why == "" {
			in.Why = "synthetic field cap " + in.How[:min(10, len(in.How))] + in.Trigger
		}
		_, err := s.WriteLesson(in, lessonVerbNow)
		return err
	}
	ok64 := "agent:" + strings.Repeat("a", LessonActorMaxBytes-6)
	if err := write(LessonInput{How: "fine", Trigger: `Bash:cap-one`, Actor: ok64}); err != nil {
		t.Fatalf("a 64-byte actor is accepted: %v", err)
	}
	if err := write(LessonInput{How: "fine", Trigger: `Bash:cap-two`, Actor: ok64 + "a"}); err == nil || !strings.Contains(err.Error(), "--by is 65 bytes") {
		t.Fatalf("a 65-byte actor is refused: %v", err)
	}
	long := strings.Repeat("x", LessonServedHowMaxBytes+1)
	if err := write(LessonInput{How: long, Trigger: `Bash:cap-three`, Actor: "agent:test"}); err == nil || !strings.Contains(err.Error(), "injected as one line") {
		t.Fatalf("a served how over 300 bytes is refused: %v", err)
	}
	if err := write(LessonInput{Why: "recall only", How: long, Actor: "agent:test"}); err != nil {
		t.Fatalf("a trigger-less lesson may use the longer limit: %v", err)
	}
	if err := write(LessonInput{How: "fine", Trigger: "T" + strings.Repeat("t", LessonToolMaxBytes) + ":cap-four", Actor: "agent:test"}); err == nil || !strings.Contains(err.Error(), "trigger tool is") {
		t.Fatalf("a long tool name is refused: %v", err)
	}
	ok := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	if _, err := s.AmendLesson(LessonChangeInput{ID: ok.Claim.ID, How: long, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "injected as one line") {
		t.Fatalf("an amend may not grow a served how past 300 bytes: %v", err)
	}
	if _, err := s.RetireLesson(LessonChangeInput{ID: ok.Claim.ID, Reason: "x", Actor: ok64 + "a"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "--by is 65 bytes") {
		t.Fatalf("a verb refuses a long actor: %v", err)
	}
}
