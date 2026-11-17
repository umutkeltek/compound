package mem

import (
	"strings"
	"testing"
)

// The --by value a lesson records is what the hook shows: invisible
// characters are removed before the reserved-name check, look-alike letters
// are refused, the 64-byte cap counts the cleaned value, and a good name is
// stored exactly as given. Without CleanLessonActor a zero-width space inside
// "operator:" passed the reserved-name check and the hook then showed the
// owner name.
func TestLessonActorIsCleanedBeforeItIsCheckedAndStored(t *testing.T) {
	s := newStore(t)
	ok := writeVerbLesson(t, s, "Run widget --dry-run before --force.", `Bash:widget`)
	n := 0
	write := func(actor string) (LessonResult, error) {
		n++
		return s.WriteLesson(LessonInput{Why: "synthetic actor case " + strings.Repeat("i", n), How: "x", Actor: actor}, lessonVerbNow)
	}

	zeroWidthOwner := "opera\u200btor:owner"     // zero-width space inside "operator:"
	cyrillicOwner := "\u043eperator:owner"       // Cyrillic o, looks like "operator:owner"
	over64 := "agent:" + strings.Repeat("a", 59) // 65 bytes
	refusals := []struct {
		actor, want string
	}{
		{zeroWidthOwner, "reserved"},
		{cyrillicOwner, "plain ASCII"},
		{over64, "--by is 65 bytes"},
	}
	for _, c := range refusals {
		if _, err := write(c.actor); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("write by %q must be refused (%q): %v", c.actor, c.want, err)
		}
		if _, err := s.RetireLesson(LessonChangeInput{ID: ok.Claim.ID, Reason: "x", Actor: c.actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("retire by %q must be refused (%q): %v", c.actor, c.want, err)
		}
		if _, err := s.AmendLesson(LessonChangeInput{ID: ok.Claim.ID, How: "y", Actor: c.actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("amend by %q must be refused (%q): %v", c.actor, c.want, err)
		}
		if _, err := s.RestoreLesson(LessonChangeInput{ID: ok.Claim.ID, Actor: c.actor}, lessonVerbNow); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("restore by %q must be refused (%q): %v", c.actor, c.want, err)
		}
	}
	if _, ok := currentLesson(t, s, ok.Claim.ID); !ok {
		t.Fatal("a refused verb must leave the lesson current")
	}

	good := "agent:b2-test_1.0/x@lane" // every allowed punctuation mark; not an e-mail shape (redact gate)
	res, err := write(good)
	if err != nil || res.Claim.Qualifiers["written_by"] != good {
		t.Fatalf("a plain name is accepted and stored as given: %q %v", res.Claim.Qualifiers["written_by"], err)
	}

	// Invisible characters inside an otherwise good name are removed, and the
	// cap counts what is stored: 64 visible bytes plus a zero-width space fit.
	ok64 := "agent:" + strings.Repeat("b", LessonActorMaxBytes-6)
	hidden := ok64[:20] + "\u200b" + ok64[20:] + "\t"
	res, err = write(hidden)
	if err != nil || res.Claim.Qualifiers["written_by"] != ok64 {
		t.Fatalf("a name with invisible characters is stored cleaned: %q %v", res.Claim.Qualifiers["written_by"], err)
	}
	retired, err := s.RetireLesson(LessonChangeInput{ID: ok.Claim.ID, Reason: "done", Actor: "agent:te\u200bst"}, lessonVerbNow)
	if err != nil || retired.Claim.Qualifiers[lessonChangedByKey] != "agent:test" {
		t.Fatalf("a verb records the cleaned actor: %q %v", retired.Claim.Qualifiers[lessonChangedByKey], err)
	}
}
