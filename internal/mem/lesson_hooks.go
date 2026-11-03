package mem

// lesson_hooks.go — the lesson's edges to its consumers: the hook-index line in
// the skill file, the `work close --used` supersede step, and the status filter
// importers use so status never promotes. See lesson.go for the ruling.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// LessonTriggerLine is the hook-index record for a lesson with a trigger:
// `trigger: {"id":…,"tool":…,"pattern":…,"how_to_apply":…,"source":…}`. The
// tool-call hook reads these lines from skill files. A harness delivers hook
// context with the NEXT tool result, so a trigger should match the step before
// the damage (the listing or dry-run call), not only the destructive one.
type LessonTriggerLine struct {
	ID         string `json:"id"`
	Tool       string `json:"tool"`
	Pattern    string `json:"pattern"`
	HowToApply string `json:"how_to_apply"`
	Source     string `json:"source"`
}

// LessonSkillLines renders what the lesson door appends to the skill's
// "## Lessons" section: the dated why/how line, plus a `trigger: {JSON}` line
// when the lesson carries a trigger.
func LessonSkillLines(claim Claim) []string {
	lines := []string{LessonSkillLine(claim)}
	trigger, ok, err := ParseLessonTrigger(claim.Qualifiers["trigger"])
	if err != nil || !ok {
		return lines
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(LessonTriggerLine{ID: claim.ID, Tool: trigger.Tool, Pattern: trigger.Pattern, HowToApply: claim.Object, Source: "lesson:" + claim.ID}); err != nil {
		return lines
	}
	return append(lines, "trigger: "+strings.TrimSpace(buf.String()))
}

// RecordLessonUses is the `work close --used` hook-up: every used ref naming a
// lesson claim (claim_* or lesson:claim_*) resolves its contradiction in favour
// of the used lesson. Close has already succeeded, so failures are warnings.
func RecordLessonUses(homePath, workID, actor, usedCSV string, stdout, stderr io.Writer) {
	var refs []string
	for _, ref := range strings.Split(usedCSV, ",") {
		if ref = strings.TrimPrefix(strings.TrimSpace(ref), "lesson:"); strings.HasPrefix(ref, "claim_") {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return
	}
	store, err := Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "warning: lesson use not recorded:", err)
		return
	}
	for _, ref := range refs {
		res, err := store.ResolveContestedLessonOnUse(ref, actor, workID, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "warning: lesson use %s not recorded: %v\n", ref, err)
		} else if len(res.Superseded) > 0 {
			fmt.Fprintf(stdout, "lesson %s used: superseded contested %s\n", ref, strings.Join(res.Superseded, ","))
		}
	}
}

// WithoutStatusRows drops status-typed candidates (status never promotes) and
// reports how many it dropped, for importers that would otherwise promote them.
func WithoutStatusRows(in []Candidate) ([]Candidate, int) {
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		if StatusNeverPromotes(c.ID, c.ClaimType) == nil {
			out = append(out, c)
		}
	}
	return out, len(in) - len(out)
}
