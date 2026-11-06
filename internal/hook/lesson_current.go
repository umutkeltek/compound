package hook

import (
	"fmt"
	"strings"

	"compound/internal/home"
	"compound/internal/mem"
)

// lessonProjection resolves lesson-backed Markdown against one immutable memory
// generation during compilation. The tool-call matcher never opens memory.
type lessonProjection struct {
	lessons    []Lesson
	revision   string
	generation int64
	excluded   []string
}

func currentLessonInstructions(lessons []Lesson) (lessonProjection, error) {
	result := lessonProjection{lessons: lessons}
	backed := func(lesson Lesson) bool {
		return strings.HasPrefix(lesson.Source, "lesson:") || strings.HasPrefix(lesson.ID, "claim_")
	}
	needed := false
	for _, lesson := range lessons {
		needed = needed || backed(lesson)
	}
	if !needed {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	store, err := mem.OpenReadOnly(homePath)
	if err != nil {
		return result, err
	}
	view, err := store.CurrentViewForLessons()
	if err != nil {
		return result, err
	}
	result.revision, result.generation = view.Revision(), view.Generation()
	result.lessons = make([]Lesson, 0, len(lessons))
	for _, lesson := range lessons {
		if !backed(lesson) {
			result.lessons = append(result.lessons, lesson)
			continue
		}
		if strings.HasPrefix(lesson.Source, "lesson:") && lesson.Source != "lesson:"+lesson.ID {
			return result, fmt.Errorf("lesson %s source does not name the same claim", lesson.ID)
		}
		claim, found := view.Lesson(lesson.ID)
		if !found {
			result.excluded = append(result.excluded, lesson.ID)
			continue
		}
		trigger, present, err := mem.ParseLessonTrigger(claim.Qualifiers["trigger"])
		if err != nil {
			return result, fmt.Errorf("lesson %s: %w", lesson.ID, err)
		}
		if !present {
			result.excluded = append(result.excluded, lesson.ID)
			continue
		}
		// Markdown preserves history, but the memory owner supplies current
		// meaning and applicability, including unresolved contradiction state.
		lesson.Tools, lesson.Pattern = []string{trigger.Tool}, trigger.Pattern
		lesson.Apply, lesson.Source = claim.Object, "lesson:"+claim.ID
		if claim.State == mem.ClaimStateNeedsReview {
			lesson.Status = "contested"
			lesson.Apply = "Conflicting lessons remain unresolved; inspect this lesson and its alternatives before choosing."
		}
		result.lessons = append(result.lessons, lesson)
	}
	return result, nil
}
