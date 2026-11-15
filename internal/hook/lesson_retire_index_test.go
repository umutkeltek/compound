package hook

import (
	"bytes"
	"testing"

	"compound/internal/mem"
)

// A retired lesson is never brought back by its skill line: not by the
// rebuild inside the retire, not by the repair door reading the recorded
// sources, and not by an explicit build naming the skill folder. The ledger
// wins over the skill file for every lesson-backed line.
func TestRetiredLessonIsNeverBroughtBackBySkillLines(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	gone := writeLedgerLesson(t, store, "the gadget flag was renamed", "Use gadget --apply.", `Bash:gadget\b`)
	skills := writeSkillLines(t, gone)
	if _, err := WriteLessonSources(homePath, LessonSources{Skills: []string{skills}}); err != nil {
		t.Fatal(err)
	}
	if build, err := RebuildLessonIndex(homePath, ledgerTestNow); err != nil || !build.Serves(gone.ID) {
		t.Fatalf("setup: lesson not served: %v", err)
	}
	retired, err := store.RetireLesson(mem.LessonChangeInput{ID: gone.ID, Reason: "the flag exists again", Actor: "agent:test"}, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil || build.Serves(gone.ID) || build.Serves(retired.Claim.ID) {
		t.Fatalf("retired lesson still indexed: %+v %v", build.Index.Lessons, err)
	}
	if len(build.Index.ExcludedLessonIDs) != 1 || build.Index.ExcludedLessonIDs[0] != gone.ID {
		t.Fatalf("retired lesson must be listed as excluded: %v", build.Index.ExcludedLessonIDs)
	}
	for _, args := range [][]string{nil, {"--skills", skills}} {
		var stdout, stderr bytes.Buffer
		if code := RunLessonsIndexCommand(args, &stdout, &stderr); code != 0 {
			t.Fatalf("lessons-index %v: %d %s %s", args, code, &stdout, &stderr)
		}
		for _, command := range []string{"gadget --apply now", "gadget"} {
			if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput(command)); len(got) != 0 {
				t.Fatalf("lessons-index %v brought the retired lesson back on %q: %+v", args, command, got)
			}
		}
	}
	if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("fixture-runner test flows/")); len(got) != 1 {
		t.Fatalf("unbacked skill line lost: %+v", got)
	}
}
