package mem

import (
	"errors"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
)

func lessonPosition(t *testing.T, s *Store, entity string) Position {
	t.Helper()
	got, err := s.Position(entity, PositionFoldOptions{})
	if err != nil {
		t.Fatalf("position %s: %v", entity, err)
	}
	var lessons []Position
	for _, d := range got.Positions {
		if d.Position.ClaimType == ClaimTypeLesson {
			lessons = append(lessons, d.Position)
		}
	}
	if len(lessons) != 1 {
		t.Fatalf("want exactly one lesson position for %s, got %d: %+v", entity, len(lessons), got.Positions)
	}
	return lessons[0]
}

// A lesson promotes on write: no candidate, no adjudication, the fold serves it
// immediately with lesson provenance.
func TestWriteLessonPromotesOnWriteWithProvenance(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	res, err := s.WriteLesson(LessonInput{
		Why:      "a jq filter on the wrong key printed failed [] for a red run",
		How:      "read the stdout verdict line, not a JSON filter",
		Skill:    "compound-verify",
		Trigger:  `Bash:proof-check.*\|.*jq`,
		Evidence: []string{"note:battery-verdict"},
		Actor:    "agent:test",
		Harness:  "claude_code",
	}, now)
	if err != nil {
		t.Fatalf("write lesson: %v", err)
	}
	if res.Outcome != LessonOutcomePromoted || res.Claim.ID == "" {
		t.Fatalf("want promoted claim, got %+v", res)
	}
	c := res.Claim
	if c.ClaimType != ClaimTypeLesson || c.PromotionBasis != candidates.BasisLessonOnWrite || c.ValidFrom != "2026-11-03T12:00:00Z" {
		t.Fatalf("lesson claim lacks lesson type/basis/valid_from: %+v", c)
	}
	if c.Qualifiers["written_by"] != "agent:test" || c.Qualifiers["harness"] != "claude_code" || c.Qualifiers["written_on"] != "2026-11-03" {
		t.Fatalf("lesson claim lacks provenance qualifiers: %+v", c.Qualifiers)
	}
	if c.Qualifiers["trigger"] != `Bash:proof-check.*\|.*jq` || !strings.Contains(strings.Join(c.EvidenceRefs, ","), "note:battery-verdict") {
		t.Fatalf("lesson claim lacks trigger/evidence: %+v", c)
	}
	all, err := s.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("lesson must not file a candidate, got %d", len(all))
	}
	p := lessonPosition(t, s, "skill:compound-verify")
	if p.ActiveClaimID != c.ID || p.Status != "active" || p.ActiveObject != c.Object || p.ID != res.PositionID {
		t.Fatalf("fold does not serve the lesson: %+v (result %+v)", p, res)
	}
	if !strings.HasPrefix(p.ID, "skill:compound-verify::lesson::slot-") {
		t.Fatalf("lesson position id = %q", p.ID)
	}

	again, err := s.WriteLesson(LessonInput{Why: "A jq filter on the wrong key printed failed [] for a red run", How: "Read the stdout verdict line,  not a JSON filter", Skill: "compound-verify", Actor: "agent:other"}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.Outcome != LessonOutcomeDuplicate || again.Claim.ID != c.ID {
		t.Fatalf("same how again must be a duplicate of %s, got %+v", c.ID, again)
	}
}

func TestWriteLessonRefusesMissingWhyHowActorAndBadTrigger(t *testing.T) {
	s := newStore(t)
	for name, in := range map[string]LessonInput{
		"no why":      {How: "h", Actor: "a"},
		"no how":      {Why: "w", Actor: "a"},
		"no actor":    {Why: "w", How: "h"},
		"bad trigger": {Why: "w", How: "h", Actor: "a", Trigger: "Bash"},
		"bad regex":   {Why: "w", How: "h", Actor: "a", Trigger: "Bash:("},
		"bad slot":    {Why: "w", How: "h", Actor: "a", Slot: "no-separator"},
	} {
		if _, err := s.WriteLesson(in, t0); err == nil {
			t.Fatalf("%s: want refusal", name)
		}
	}
	claims, err := s.loadClaimsRaw()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("refused lessons wrote %d claims", len(claims))
	}
}

// Contradictions park: both kept, each contested by the other, the fold serves
// the newest as contested; the consumer that records a use supersedes the other.
func TestContradictingLessonParksThenConsumerUseSupersedes(t *testing.T) {
	s := newStore(t)
	slot := "tool:poster::lesson.ampersand"
	first, err := s.WriteLesson(LessonInput{Why: "post too long", How: "drop the ampersand", Slot: slot, Actor: "agent:a"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.WriteLesson(LessonInput{Why: "post too long", How: "escape the ampersand", Slot: slot, Actor: "agent:b"}, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Outcome != LessonOutcomeContested || second.ContestedWith != first.Claim.ID {
		t.Fatalf("want contested with %s, got %+v", first.Claim.ID, second)
	}
	p := lessonPosition(t, s, "tool:poster")
	if p.Status != "contested" || p.ActiveClaimID != second.Claim.ID || len(p.ClaimChain) != 2 || len(p.SupersededHistoryRefs) != 0 {
		t.Fatalf("fold must serve the newest as contested and keep both: %+v", p)
	}
	if !oneOf(first.Claim.ID, p.ReviewRefs) || !oneOf(second.Claim.ID, p.ReviewRefs) {
		t.Fatalf("each lesson must be marked contested by the other, review refs %v", p.ReviewRefs)
	}

	// The consumer used the OLDER lesson: it wins, the newer one is superseded.
	use, err := s.ResolveContestedLessonOnUse(first.Claim.ID, "agent:c", "work_x", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if use.Outcome != "superseded_contested" || len(use.Superseded) != 1 || use.Superseded[0] != second.Claim.ID {
		t.Fatalf("use must supersede the contesting lesson, got %+v", use)
	}
	p = lessonPosition(t, s, "tool:poster")
	if p.Status != "active" || p.ActiveClaimID != first.Claim.ID || !oneOf(second.Claim.ID, p.SupersededHistoryRefs) {
		t.Fatalf("after use the used lesson must rule uncontested: %+v", p)
	}
	again, err := s.ResolveContestedLessonOnUse(first.Claim.ID, "agent:c", "work_y", t0.Add(2*time.Hour))
	if err != nil || again.Outcome != "uncontested" {
		t.Fatalf("second use must be a no-op, got %+v err=%v", again, err)
	}
	notLesson, err := s.ResolveContestedLessonOnUse("claim_missing", "agent:c", "", t0)
	if err != nil || notLesson.Outcome != "not_lesson" {
		t.Fatalf("unknown claim must be a no-op, got %+v err=%v", notLesson, err)
	}
}

func TestLessonTriggerFormat(t *testing.T) {
	tr, ok, err := ParseLessonTrigger(`Bash:proof-check.*\|.*jq`)
	if err != nil || !ok {
		t.Fatalf("parse: %v", err)
	}
	if !tr.Matches("Bash", "compound proof-check --json | jq '.failed'") {
		t.Fatal("trigger must fire on a proof-check piped to jq")
	}
	if tr.Matches("Bash", "compound proof-check --wait 8h") || tr.Matches("Edit", "proof-check | jq") {
		t.Fatal("trigger must not fire without the pipe or on another tool")
	}
	if _, ok, err := ParseLessonTrigger(""); ok || err != nil {
		t.Fatal("empty trigger is no trigger")
	}
}

func statusCandidate(t *testing.T, s *Store, basis string) Candidate {
	t.Helper()
	got, err := s.Capture([]Candidate{{
		Entity: "repo:example", Predicate: "untracked_count", Object: "3",
		ObservationDate: "2026-11-03T00:00:00Z", Confidence: 1, Derivation: "deterministic",
		ClaimType: ClaimTypeStatus, PromotionBasis: basis,
		SourceTraceRef: "source-control:test", SnippetHash: "sha256:status", Visibility: "operational",
	}}, t0)
	if err != nil || len(got) != 1 {
		t.Fatalf("capture status: %v %d", err, len(got))
	}
	return got[0]
}

// Status never promotes on a learning door: the operator door and the
// explicit-confirmation door refuse with the typed reason and write no claim.
func TestStatusNeverPromotesOnLearningDoors(t *testing.T) {
	s := newStore(t)
	c := statusCandidate(t, s, candidates.BasisTrustedDeterministic)
	_, err := s.AdjudicateAsOperator(c.ID, "promote", "", "operator:test", t0)
	if !errors.Is(err, ErrStatusNeverPromotes) {
		t.Fatalf("operator promote of status must refuse with ErrStatusNeverPromotes, got %v", err)
	}
	if _, err := s.Adjudicate(c.ID, "park", "", t0); err != nil {
		t.Fatalf("park: %v", err)
	}
	evidence, err := s.RecordOperatorConfirmation(c.ID, "operator confirms", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdjudicateExplicitConfirmation(c.ID, evidence.ID, "", t0); !errors.Is(err, ErrStatusNeverPromotes) {
		t.Fatalf("explicit confirmation of status must refuse, got %v", err)
	}
	claims, err := s.loadClaimsRaw()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("refused status wrote %d claims", len(claims))
	}
	// Non-promote verdicts on status stay allowed (filing it away is fine).
	if _, err := s.AdjudicateAsOperator(c.ID, "no_promote", "", "operator:test", t0); err != nil {
		t.Fatalf("no_promote on status must stay allowed: %v", err)
	}
	if err := StatusNeverPromotes("x", "Status"); !errors.Is(err, ErrStatusNeverPromotes) {
		t.Fatal("rule must be case-insensitive")
	}
	if err := StatusNeverPromotes("x", "decision"); err != nil {
		t.Fatal("non-status must pass")
	}
}
