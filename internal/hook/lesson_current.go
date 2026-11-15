package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/mem"
)

// lessonProjection resolves the lesson index entries against one immutable
// memory generation during compilation. The tool-call matcher never opens
// memory.
type lessonProjection struct {
	lessons    []Lesson
	revision   string
	generation int64
	excluded   []string
	// problems are lessons left out of the index; the build still publishes.
	problems []error
}

// currentLessonInstructions compiles the index entries from two sources.
//
// The ledger comes first: every current lesson claim (not retired, not
// superseded) that carries a trigger becomes an entry, in ledger order, so a
// lesson write is served without naming a skill. The extra sources (skill-file
// trigger lines and --from lesson files) come second. For any entry backed by
// a lesson claim the ledger decides: a backed line whose claim is current is
// already served from the ledger, and a backed line whose claim is retired,
// superseded or has lost its trigger is excluded, so a skill line can never
// bring a retired lesson back. Unbacked lines are served as written.
func currentLessonInstructions(homePath string, lessons []Lesson) (lessonProjection, error) {
	result := lessonProjection{}
	view, err := lessonView(homePath)
	if err != nil {
		return result, err
	}
	if view != nil {
		result.revision, result.generation = view.Revision(), view.Generation()
	}
	fromLedger := map[string]bool{}
	for _, claim := range view.Lessons() {
		lesson, ok, err := lessonFromClaim(claim)
		if err != nil {
			result.problems = append(result.problems, err)
			continue
		}
		if ok {
			fromLedger[claim.ID] = true
			result.lessons = append(result.lessons, lesson)
		}
	}
	backed := func(lesson Lesson) bool {
		return strings.HasPrefix(lesson.Source, "lesson:") || strings.HasPrefix(lesson.ID, "claim_")
	}
	excluded := map[string]bool{}
	for _, lesson := range lessons {
		if !backed(lesson) {
			result.lessons = append(result.lessons, lesson)
			continue
		}
		if strings.HasPrefix(lesson.Source, "lesson:") && lesson.Source != "lesson:"+lesson.ID {
			result.problems = append(result.problems, fmt.Errorf("lesson %s source %s does not name the same claim", lesson.ID, lesson.Source))
			continue
		}
		if fromLedger[lesson.ID] {
			continue
		}
		if !excluded[lesson.ID] {
			excluded[lesson.ID] = true
			result.excluded = append(result.excluded, lesson.ID)
		}
	}
	return result, nil
}

// lessonView opens one lesson snapshot of the home's ledger. A home with no
// ledger yet has no lessons; that is not an error.
func lessonView(homePath string) (*mem.CurrentView, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME for the lesson ledger")
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger")); os.IsNotExist(err) {
		return nil, nil
	}
	store, err := mem.OpenReadOnly(homePath)
	if err != nil {
		return nil, err
	}
	return store.CurrentViewForLessons()
}

// lessonFromClaim renders one current lesson claim as an index entry. A
// lesson without a trigger is for recall only (ok=false). An unresolved
// contradiction is served as a conflict notice, never as two instructions.
func lessonFromClaim(claim mem.Claim) (Lesson, bool, error) {
	trigger, present, err := mem.ParseLessonTrigger(claim.Qualifiers["trigger"])
	if err != nil {
		return Lesson{}, false, fmt.Errorf("lesson %s: %w", claim.ID, err)
	}
	if !present {
		return Lesson{}, false, nil
	}
	lesson := Lesson{ID: claim.ID, Tools: []string{trigger.Tool}, Pattern: trigger.Pattern, Apply: claim.Object, Source: "lesson:" + claim.ID}
	if claim.State == mem.ClaimStateNeedsReview {
		lesson.Status = "contested"
		lesson.Apply = "Conflicting lessons remain unresolved; inspect this lesson and its alternatives before choosing."
	}
	return lesson, true, nil
}
