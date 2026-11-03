package mem

import (
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/envelope"
)

// Work-close side effects are automatic candidate producers (ruling
// 2026-11-03, Q3a): with the switch off the surprise blob is still stored but
// no memory candidate is filed.
func TestWorkSurpriseProducerOffByDefault(t *testing.T) {
	t.Setenv(candidates.ProducersEnv, "")
	s := newStore(t)
	payload := marshalSurprise(t, envelope.SurpriseSignal{
		PredictedOutcome: "completed", PredictedMoveType: "rule_delta",
		ActualOutcome: "completed", ActualMoveType: "control_plane_repair", Diverged: true,
	})
	cid, candidateID, err := s.ObserveWorkSurprise("work_x", "rec_x", payload, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if cid == "" || candidateID != "" {
		t.Fatalf("want blob kept and no candidate, got cid=%q candidate=%q", cid, candidateID)
	}
	all, err := s.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("producer off filed %d candidates", len(all))
	}
}

// The lesson's skill lines carry the hook-index record the tool-call hook reads.
func TestLessonSkillLinesCarryHookTriggerRecord(t *testing.T) {
	s := newStore(t)
	res, err := s.WriteLesson(LessonInput{Why: "w", How: "use a & b <x>", Actor: "a", Trigger: `Bash:asc screenshots`}, t0)
	if err != nil {
		t.Fatal(err)
	}
	lines := LessonSkillLines(res.Claim)
	want := `trigger: {"id":"` + res.Claim.ID + `","tool":"Bash","pattern":"asc screenshots","how_to_apply":"use a & b <x>","source":"lesson:` + res.Claim.ID + `"}`
	if len(lines) != 2 || lines[1] != want {
		t.Fatalf("lines = %q\nwant trigger line %q", lines, want)
	}
	plain, err := s.WriteLesson(LessonInput{Why: "w2", How: "h2", Actor: "a"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := LessonSkillLines(plain.Claim); len(got) != 1 {
		t.Fatalf("no trigger means no trigger line, got %q", got)
	}
}
