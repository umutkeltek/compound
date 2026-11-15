package hook

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticLessons returns n lessons shaped like real ones: mostly shell
// triggers with an alternation and word boundaries, a few file-tool triggers,
// and a how-to line of about 200 bytes.
func syntheticLessons(n int) []Lesson {
	lessons := make([]Lesson, 0, n)
	for i := 0; i < n; i++ {
		lesson := Lesson{
			ID:      fmt.Sprintf("claim_%012x", 0xabc000+i),
			Tools:   []string{"Bash"},
			Pattern: fmt.Sprintf(`\btool%d\s+(plan|apply|close)-step\b[^|;&]*--(force|steal\b|by[ =]['"]?(owner|admin)\b)`, i),
			Apply:   fmt.Sprintf("Lesson %d: preview with tool%d plan-step first, read every change it lists, and only then run the apply step; stop and ask if it lists a file you did not expect to change.", i, i),
			Source:  fmt.Sprintf("lesson:claim_%012x", 0xabc000+i),
		}
		if i%10 == 9 {
			lesson.Tools = []string{"Edit"}
			lesson.Pattern = fmt.Sprintf(`generated/module%d/`, i)
		}
		lessons = append(lessons, lesson)
	}
	return lessons
}

// BenchmarkLessonReadPathAtScale measures the pre-tool-use hook's whole lesson read
// path (open, bounded read, decode, match) for one shell call, at today's size
// and at 500 lessons.
func BenchmarkLessonReadPathAtScale(b *testing.B) {
	for _, n := range []int{12, 500} {
		index, problems := BuildLessonIndex(syntheticLessons(n), lessonTestNow)
		if len(problems) != 0 {
			b.Fatal(problems)
		}
		path := filepath.Join(b.TempDir(), "hook-lessons.json")
		if err := WriteLessonIndex(path, index); err != nil {
			b.Fatal(err)
		}
		for _, call := range []struct{ name, command string }{
			{"match", fmt.Sprintf("tool%d apply-step --force ./x", n-2)},
			{"miss", "go test ./internal/hook -run TestX -count=1"},
		} {
			input := []byte(fmt.Sprintf(`{"command":%q}`, call.command))
			b.Run(fmt.Sprintf("lessons=%d/%s", n, call.name), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					got := MatchLessonsFile(path, "Bash", input)
					if (call.name == "match") != (len(got) == 1) || (len(got) == 1 && !strings.HasPrefix(got[0].Apply, "Lesson")) {
						b.Fatalf("%s: %+v", call.name, got)
					}
				}
			})
		}
	}
}
