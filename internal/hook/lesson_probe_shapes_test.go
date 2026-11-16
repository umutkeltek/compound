package hook

import (
	"strings"
	"testing"

	"compound/internal/mem"
)

// The five catch-alls the review found accepted: each is refused without the
// override, and with it ranks after a guard written before them, so the
// guard is served on its command.
func TestCommonCatchAllShapesAreBroad(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	guard := writeLedgerLesson(t, store, "a build reached external testers too early", "Check the release notes before sending a build to testers.", `Bash:\bbuild-publish\s+--testers\b`)
	for i, trigger := range []string{`Bash: -`, `Bash:.{11}`, `Write:.`, `Edit:.`, `*:"[a-z_]+":"/`} {
		in := mem.LessonInput{Why: "synthetic catch-all " + trigger, How: "General advice.", Trigger: trigger, Actor: "agent:test"}
		if _, err := store.WriteLesson(in, ledgerTestNow); err == nil || !strings.Contains(err.Error(), "fires on ordinary calls") {
			t.Fatalf("shape %d %q must be refused as broad: %v", i, trigger, err)
		}
		in.AllowBroadTrigger = true
		if res, err := store.WriteLesson(in, ledgerTestNow); err != nil || res.Claim.Qualifiers[mem.LessonBroadTriggerQualifier] != "allowed" {
			t.Fatalf("shape %q with the override: %+v %v", trigger, res.Claim.Qualifiers, err)
		}
	}
	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil || build.Rank(guard.ID) != 1 {
		t.Fatalf("the guard must outrank every catch-all: rank %d (%v)", build.Rank(guard.ID), err)
	}
	got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("build-publish --testers group-a"))
	if len(got) == 0 || got[0].ID != guard.ID {
		t.Fatalf("the guard must be served first on its command: %+v", got)
	}
}
