package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/mem"
)

// fillLesson is a large, synthetic lesson: a trigger of about 220 bytes and a
// how of about 280 bytes, so the index fills after a few hundred lessons.
func fillLesson(i int) mem.LessonInput {
	words := []string{"overwrite-every-remote-file", "delete-the-stale-rows", "prune-the-old-branches", "reset-the-shared-cache", "push-without-review"}
	pattern := fmt.Sprintf(`\bfill-tool-%04d\s+apply-step\b.*--(%s)`, i, strings.Join(words, "|"))
	how := fmt.Sprintf("Lesson %04d: %s", i, strings.Repeat("preview the step first and read every change it lists. ", 5))
	return mem.LessonInput{
		Why: fmt.Sprintf("fill tool %04d forced step lost data", i), How: strings.TrimSpace(how[:280]),
		Trigger: "Bash:" + pattern, Actor: "agent:fill",
	}
}

func fillCommand(i int) string {
	return fmt.Sprintf("fill-tool-%04d apply-step ./x --prune-the-old-branches", i)
}

// A full index publishes the lessons that fit, newest first, instead of
// nothing; it names the rest and warns before it fills.
func TestFullIndexKeepsTheNewestLessonsAndReportsTheRest(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	sawWarning := false
	var ids []string
	var build LessonIndexBuild
	for i := 0; ; i++ {
		res, err := store.WriteLesson(fillLesson(i), ledgerTestNow)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Claim.ID)
		if i%25 != 24 {
			continue
		}
		if build, err = RebuildLessonIndex(homePath, ledgerTestNow); err != nil {
			t.Fatalf("rebuild at %d lessons: %v", len(ids), err)
		}
		if len(build.Unserved) == 0 && build.FillPercent() >= LessonIndexWarnPercent {
			sawWarning = true
			if !strings.Contains(LessonIndexSummary(build), "nearly full") || LessonIndexFullAdvice(build) == "" {
				t.Fatalf("a nearly full index must warn: %q", LessonIndexSummary(build))
			}
		}
		if len(build.Unserved) >= 10 {
			break
		}
	}
	if !sawWarning {
		t.Fatal("the index filled without ever warning at 80%")
	}
	info, err := os.Stat(LessonIndexPath(homePath))
	if err != nil || int(info.Size()) != build.Bytes || build.Bytes > LessonIndexMaxBytes {
		t.Fatalf("published %d bytes (stat %v, %v); cap %d", build.Bytes, info, err, LessonIndexMaxBytes)
	}
	oldest := map[string]bool{}
	for _, id := range ids[:len(build.Unserved)] {
		oldest[id] = true
	}
	for _, id := range build.Unserved {
		if !oldest[id] {
			t.Fatalf("unserved lessons must be the oldest: %s is not among the first %d written", id, len(build.Unserved))
		}
	}
	if len(build.Unserved)+len(build.Index.Lessons) != len(ids) || build.Index.Lessons[0].ID != ids[len(ids)-1] {
		t.Fatalf("index order must be newest first and account for every lesson: %d indexed + %d unserved != %d", len(build.Index.Lessons), len(build.Unserved), len(ids))
	}
	summary := LessonIndexSummary(build)
	if !strings.Contains(summary, "FULL") || !strings.Contains(summary, build.Unserved[0]) || !strings.Contains(LessonIndexFullAdvice(build), "retire") {
		t.Fatalf("summary must name what is not served: %q", summary)
	}
	if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput(fillCommand(len(ids)-1))); len(got) != 1 || got[0].ID != ids[len(ids)-1] {
		t.Fatalf("newest lesson not served: %+v", got)
	}
	if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput(fillCommand(0))); len(got) != 0 {
		t.Fatalf("an unserved lesson was served: %+v", got)
	}
}

// One malformed claims row in the middle of the ledger (a torn final row is
// already repaired on append) is skipped and reported: lessons are still
// written, served and retired around it.
func TestMalformedLedgerRowIsSkippedAndReported(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	a := writeLedgerLesson(t, store, "a forced widget run wiped the cache", "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	b := writeLedgerLesson(t, store, "the gadget flag was renamed", "Use gadget --apply.", `Bash:gadget\b`)
	claimsPath := filepath.Join(homePath, "ledger", "claims.jsonl")
	data, err := os.ReadFile(claimsPath)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.SplitAfter(string(data), "\n")
	broken := rows[0] + "{\"id\": \"claim_torn\", \"seq\": \n" + strings.Join(rows[1:], "")
	if err := os.WriteFile(claimsPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := store.WriteLesson(mem.LessonInput{Why: "the sprocket queue drops jobs", How: "Drain the sprocket queue first.", Trigger: `Bash:sprocket\s+restart`, Actor: "agent:test"}, ledgerTestNow)
	if err != nil || res.SkippedLedgerRows != 1 {
		t.Fatalf("a lesson write must read past one malformed row: %+v %v", res, err)
	}
	c := res.Claim

	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil {
		t.Fatalf("one malformed row must not fail the rebuild: %v", err)
	}
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if !build.Serves(id) {
			t.Fatalf("lesson %s must be served: %+v", id, build.Index.Lessons)
		}
	}
	reported := false
	for _, problem := range build.Problems {
		reported = reported || strings.Contains(problem.Error(), "malformed ledger row skipped: claims.jsonl row 2")
	}
	if !reported {
		t.Fatalf("the skipped row must be reported: %v", build.Problems)
	}

	retired, err := store.RetireLesson(mem.LessonChangeInput{ID: a.ID, Reason: "no longer holds", Actor: "agent:test"}, ledgerTestNow)
	if err != nil || retired.SkippedLedgerRows != 1 {
		t.Fatalf("a retire must read past one malformed row: %+v %v", retired, err)
	}
	if build, err = RebuildLessonIndex(homePath, ledgerTestNow); err != nil || build.Serves(a.ID) || !build.Serves(b.ID) || !build.Serves(c.ID) {
		t.Fatalf("after the retire: %+v %v", build.Index.Lessons, err)
	}
	if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("widget --force all")); len(got) != 0 {
		t.Fatalf("retired lesson still served: %+v", got)
	}
}
