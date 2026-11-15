package mem

import (
	"strings"
	"testing"
	"time"
)

var lessonVerbNow = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func writeVerbLesson(t *testing.T, s *Store, how, trigger string) LessonResult {
	t.Helper()
	res, err := s.WriteLesson(LessonInput{
		Why: "synthetic widget cache was wiped by a forced run", How: how, Trigger: trigger,
		Slot: "tool:widget::lesson.cache", Actor: "agent:test",
	}, lessonVerbNow)
	if err != nil {
		t.Fatalf("write lesson: %v", err)
	}
	return res
}

func currentLesson(t *testing.T, s *Store, id string) (Claim, bool) {
	t.Helper()
	view, err := s.CurrentViewForLessons()
	if err != nil {
		t.Fatal(err)
	}
	return view.Lesson(id)
}

// Retire stops a lesson in one step, records who and why, and keeps the old
// version as history; restore brings it back as a new current version.
func TestRetireAndRestoreLessonRecordTheAgent(t *testing.T) {
	s := newStore(t)
	a := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget\s+--force`)

	retired, err := s.RetireLesson(LessonChangeInput{ID: a.Claim.ID, Reason: "the flag no longer wipes the cache", Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if retired.Prior.ID != a.Claim.ID || retired.Claim.ID == a.Claim.ID || retired.Claim.Supersedes != a.Claim.ID {
		t.Fatalf("retire must append a new version superseding the old: %+v", retired)
	}
	q := retired.Claim.Qualifiers
	if retired.Claim.Visibility != "retired" || retired.Claim.CooledReason != CooledReasonLessonRetired ||
		q[lessonChangeKey] != LessonChangeRetired || q[lessonChangedByKey] != "agent:test" ||
		q[lessonChangeReasonKey] != "the flag no longer wipes the cache" || q[lessonChangedOnKey] != "2026-11-16" ||
		q["written_by"] != "agent:test" || q["trigger"] != `Bash:widget\s+--force` {
		t.Fatalf("retired version lacks its record: %+v", retired.Claim)
	}
	if retired.TransitionID == "" {
		t.Fatal("retire must write a memory transition")
	}
	for _, id := range []string{a.Claim.ID, retired.Claim.ID} {
		if _, ok := currentLesson(t, s, id); ok {
			t.Fatalf("retired lesson %s is still current", id)
		}
	}
	if pos := lessonPosition(t, s, "tool:widget"); pos.Status != "retired" || pos.ActiveClaimID != retired.Claim.ID {
		t.Fatalf("position must show the retirement: %+v", pos)
	}
	if _, err := s.RetireLesson(LessonChangeInput{ID: a.Claim.ID, Reason: "again", Actor: "agent:test"}, lessonVerbNow); err == nil ||
		!strings.Contains(err.Error(), "already retired") || !strings.Contains(err.Error(), "restore "+retired.Claim.ID) {
		t.Fatalf("double retire must refuse with the undo command: %v", err)
	}

	// The id an agent saw in the hook line (the original) still works.
	restored, err := s.RestoreLesson(LessonChangeInput{ID: a.Claim.ID, Actor: "agent:other"}, lessonVerbNow)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, ok := currentLesson(t, s, restored.Claim.ID)
	if !ok || got.Object != a.Claim.Object || got.Visibility != "contextual" || got.CooledReason != "" ||
		got.Qualifiers[lessonChangeKey] != LessonChangeRestored || got.Qualifiers[lessonChangedByKey] != "agent:other" ||
		got.Qualifiers[lessonChangeReasonKey] != "" || got.Qualifiers["trigger"] != `Bash:widget\s+--force` {
		t.Fatalf("restored lesson = %+v (current=%v)", got, ok)
	}
	if _, err := s.RestoreLesson(LessonChangeInput{ID: restored.Claim.ID, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "not retired") {
		t.Fatalf("restoring a current lesson must refuse: %v", err)
	}
}

// Writing the exact lesson again after retirement is refused with the restore
// command, so an automated writer cannot silently undo a retirement; a
// different how in the slot is a new lesson, not a contest with history.
func TestWriteLessonAfterRetirementRefusesTheSameHowOnly(t *testing.T) {
	s := newStore(t)
	a := writeVerbLesson(t, s, "Run widget --dry-run before --force.", "")
	retired, err := s.RetireLesson(LessonChangeInput{ID: a.Claim.ID, Reason: "wrong", Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WriteLesson(LessonInput{Why: "synthetic widget cache was wiped by a forced run", How: a.Claim.Object, Slot: "tool:widget::lesson.cache", Actor: "agent:test"}, lessonVerbNow)
	if err == nil || !strings.Contains(err.Error(), "retired as "+retired.Claim.ID) || !strings.Contains(err.Error(), "compound memory lesson restore") {
		t.Fatalf("same how after retirement must refuse with the restore hint: %v", err)
	}
	b := writeVerbLesson(t, s, "Ask the widget owner before forcing.", "")
	if b.Outcome != LessonOutcomePromoted || b.ContestedWith != "" {
		t.Fatalf("a different how after retirement is a new lesson: %+v", b)
	}
}

// Retiring one side of a contest leaves the other side standing alone; the
// restore parks the contradiction again, exactly like the lesson door.
func TestRetireClearsContestAndRestoreParksItAgain(t *testing.T) {
	s := newStore(t)
	a := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	b := writeVerbLesson(t, s, "Never force the widget.", `Bash:widget`)
	if b.Outcome != LessonOutcomeContested {
		t.Fatalf("setup: want contest, got %+v", b)
	}
	retired, err := s.RetireLesson(LessonChangeInput{ID: a.Claim.ID, Reason: "wrong side", Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired.ClearedContest) != 1 || retired.ClearedContest[0] != b.Claim.ID {
		t.Fatalf("retire must clear the contest with %s: %+v", b.Claim.ID, retired)
	}
	if got, ok := currentLesson(t, s, b.Claim.ID); !ok || got.State != ClaimStatePromoted {
		t.Fatalf("remaining lesson must stand alone after the retirement: %+v ok=%v", got, ok)
	}
	if _, err := s.AmendLesson(LessonChangeInput{ID: b.Claim.ID, Trigger: `Bash:widget\s+--force`, Actor: "agent:test"}, lessonVerbNow); err != nil {
		t.Fatalf("an uncontested lesson amends: %v", err)
	}
	restored, err := s.RestoreLesson(LessonChangeInput{ID: retired.Claim.ID, Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ContestedWith == "" {
		t.Fatalf("restoring a different how next to a current lesson must park the contest: %+v", restored)
	}
	if got, _ := currentLesson(t, s, restored.Claim.ID); got.State != ClaimStateNeedsReview {
		t.Fatalf("restored lesson must be contested: %+v", got)
	}
	if _, err := s.AmendLesson(LessonChangeInput{ID: restored.Claim.ID, How: "x", Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "contested") {
		t.Fatalf("amending a contested lesson must refuse and name the way out: %v", err)
	}
}

// Amend replaces how and trigger as a new version; the old version stays as
// history and amending back restores the old meaning.
func TestAmendLessonKeepsHistoryAndAmendsBack(t *testing.T) {
	s := newStore(t)
	a := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	amended, err := s.AmendLesson(LessonChangeInput{ID: a.Claim.ID, Trigger: `Bash:widget\s+--force`, Reason: "too noisy", Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatal(err)
	}
	if amended.Claim.Object != a.Claim.Object || amended.Claim.Qualifiers["trigger"] != `Bash:widget\s+--force` || amended.Prior.Qualifiers["trigger"] != "Bash:widget" {
		t.Fatalf("amend must change only the trigger: %+v", amended)
	}
	if _, err := s.AmendLesson(LessonChangeInput{ID: amended.Claim.ID, Trigger: `Bash:widget\s+--force`, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "nothing to amend") {
		t.Fatalf("a no-op amend must refuse: %v", err)
	}
	dropped, err := s.AmendLesson(LessonChangeInput{ID: a.Claim.ID, DropTrigger: true, How: "Ask the widget owner first.", Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := dropped.Claim.Qualifiers["trigger"]; has || dropped.Claim.Object != "Ask the widget owner first." || dropped.Prior.ID != amended.Claim.ID {
		t.Fatalf("amend by the original id must act on the chain head and drop the trigger: %+v", dropped)
	}
	back, err := s.AmendLesson(LessonChangeInput{ID: dropped.Claim.ID, How: a.Claim.Object, Trigger: a.Claim.Qualifiers["trigger"], Actor: "agent:test"}, lessonVerbNow)
	if err != nil {
		t.Fatal(err)
	}
	if back.Claim.Object != a.Claim.Object || back.Claim.Qualifiers["trigger"] != a.Claim.Qualifiers["trigger"] {
		t.Fatalf("amending back must restore the old meaning: %+v", back.Claim)
	}
	for _, id := range []string{a.Claim.ID, amended.Claim.ID, dropped.Claim.ID} {
		if _, ok := currentLesson(t, s, id); ok {
			t.Fatalf("old version %s must be history, not current", id)
		}
	}
	claims, err := s.loadClaimsRaw()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 4 {
		t.Fatalf("every version must stay in the ledger: %d claims", len(claims))
	}
}

// A lesson that lost a contest was replaced by a different lesson: the verbs
// refuse it and name the winner instead of acting on someone else's lesson.
func TestLessonVerbsDoNotFollowAContestSupersede(t *testing.T) {
	s := newStore(t)
	a := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	b := writeVerbLesson(t, s, "Never force the widget.", `Bash:widget`)
	if _, err := s.ResolveContestedLessonOnUse(b.Claim.ID, "agent:test", "work_example", lessonVerbNow); err != nil {
		t.Fatal(err)
	}
	_, err := s.RetireLesson(LessonChangeInput{ID: a.Claim.ID, Reason: "x", Actor: "agent:test"}, lessonVerbNow)
	if err == nil || !strings.Contains(err.Error(), b.Claim.ID) {
		t.Fatalf("acting on a contest loser must refuse and name the winner: %v", err)
	}
	if _, err := s.RetireLesson(LessonChangeInput{ID: "claim_missing", Reason: "x", Actor: "agent:test"}, lessonVerbNow); err == nil {
		t.Fatal("an unknown id must refuse")
	}
	if _, err := s.RetireLesson(LessonChangeInput{ID: b.Claim.ID, Actor: "agent:test"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("retire without a reason must refuse: %v", err)
	}
	if _, err := s.RetireLesson(LessonChangeInput{ID: b.Claim.ID, Reason: "x"}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("retire without an actor must refuse: %v", err)
	}
}
