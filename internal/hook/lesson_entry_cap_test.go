package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/mem"
)

// appendRawLessonClaim writes a lesson row straight into the ledger, the way
// a binary from before the door's field caps could have.
func appendRawLessonClaim(t *testing.T, homePath string, seq int64, id, object, trigger, writtenBy string) {
	t.Helper()
	claim := mem.Claim{
		ID: id, Seq: seq, Entity: "lessons", Predicate: "lesson." + id, Object: object, ValidFrom: "2026-11-16T09:00:00Z",
		Confidence: 0.9, Derivation: "inferred", ClaimType: mem.ClaimTypeLesson, Visibility: "contextual",
		Qualifiers:   map[string]string{"domain": "lesson", "why": "synthetic " + id, "written_by": writtenBy, "written_on": "2026-11-16", "trigger": trigger},
		EvidenceRefs: []string{"lesson-writer:synthetic"}, CreatedAt: "2026-11-16T09:00:00Z",
	}
	data, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(homePath, "ledger", "claims.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

// No single entry can push other lessons out of the index, whatever one of
// its fields contains: an over-long field is cleaned to its bound or the
// entry is left out and reported. The index is filled to about 90 % with
// ordinary lessons first; each hostile entry would otherwise rank first.
func TestOneOverLongFieldCannotDisplaceOtherLessons(t *testing.T) {
	homePath, _ := ledgerTestHome(t)
	huge := strings.Repeat("A", 255<<10)
	var fillers []map[string]any
	for i := 0; i < 450; i++ {
		fillers = append(fillers, map[string]any{
			"id": fmt.Sprintf("filler_%04d", i), "tools": []string{"Bash"}, "pattern": fmt.Sprintf(`\bfiller-tool-%04d\s+apply\b`, i),
			"apply": fmt.Sprintf("Filler %04d: %s", i, strings.Repeat("preview the step and read every change it lists. ", 6)[:270]),
		})
	}
	hostileFrom := []map[string]any{
		{"id": "x" + huge, "tools": []string{"Bash"}, "pattern": `hostile-id`, "apply": "Long id."},
		{"id": "hostile_tools", "tools": strings.Split(strings.Repeat("Bash,", 5000), ",")[:5000], "pattern": `hostile-tools`, "apply": "Many tools."},
		{"id": "hostile_source", "tools": []string{"Bash"}, "pattern": `hostile-source`, "apply": "Long source.", "source": huge},
		{"id": "claim_" + strings.Repeat("ab", 64<<10), "tools": []string{"Bash"}, "pattern": `hostile-claim-id`, "apply": "Long claim id."},
	}
	writeFrom := func(name string, lessons []map[string]any) string {
		data, err := json.Marshal(map[string]any{"lessons": lessons})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	sources := LessonSources{From: []string{writeFrom("fillers.json", fillers)}}
	before, err := CompileLessonIndex(homePath, LessonIndexPath(homePath), sources, ledgerTestNow)
	if err != nil || len(before.Unserved) != 0 || before.FillPercent() < 80 {
		t.Fatalf("setup: want a nearly full index with nothing left out: %d%%, unserved %d, %v", before.FillPercent(), len(before.Unserved), err)
	}

	appendRawLessonClaim(t, homePath, 1001, "claim_aaaaaaaaaaa1", "Long author.", `Bash:hostile-author`, "agent:"+huge)
	appendRawLessonClaim(t, homePath, 1002, "claim_aaaaaaaaaaa2", huge, `Bash:hostile-how`, "agent:test")
	appendRawLessonClaim(t, homePath, 1003, "claim_aaaaaaaaaaa3", "Long tool.", "T"+strings.Repeat("t", 10<<10)+`:hostile-tool`, "agent:test")
	appendRawLessonClaim(t, homePath, 1004, "claim_aaaaaaaaaaa4", "Escaped pattern.", "Bash:"+strings.Repeat("<", 900), "agent:test")
	sources.From = append(sources.From, writeFrom("hostile.json", hostileFrom))
	after, err := CompileLessonIndex(homePath, LessonIndexPath(homePath), sources, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(fillers); i++ {
		if !after.Serves(fmt.Sprintf("filler_%04d", i)) {
			t.Fatalf("filler_%04d was displaced by a hostile entry (%d indexed, %d unserved, %d%% full)", i, len(after.Index.Lessons), len(after.Unserved), after.FillPercent())
		}
	}
	if len(after.Unserved) != 0 {
		t.Fatalf("nothing may be left out for lack of room: %v", after.Unserved)
	}
	var author *Lesson
	for i := range after.Index.Lessons {
		if after.Index.Lessons[i].ID == "claim_aaaaaaaaaaa1" {
			author = &after.Index.Lessons[i]
		}
	}
	if author == nil || len(author.By) > mem.LessonActorMaxBytes {
		t.Fatalf("a long author is cleaned to its bound and the lesson still served: %+v", author)
	}
	problems := ""
	for _, problem := range after.Problems {
		problems += problem.Error() + "\n"
	}
	for _, want := range []string{"claim_aaaaaaaaaaa2", "claim_aaaaaaaaaaa3", "claim_aaaaaaaaaaa4", "hostile_tools", "hostile_source", "plain characters", "is not a claim id"} {
		if !strings.Contains(problems, want) {
			t.Fatalf("each hostile entry must be reported (missing %q):\n%.2000s", want, problems)
		}
	}
	for _, lesson := range after.Index.Lessons {
		if encoded, _ := json.Marshal(lesson); len(encoded) > LessonEntryMaxBytes {
			t.Fatalf("entry %s is %d bytes", lesson.ID, len(encoded))
		}
	}
}
