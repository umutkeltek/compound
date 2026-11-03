package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The lesson index is the pre-tool-use hook's only read: a small JSON file that
// maps a trigger (tool name + regular expression over the tool input) to one
// short "how to apply" line. It is derived state built by
// `compound hook lessons-index`; the hook never builds, repairs or validates it
// beyond what matching needs. A missing, oversized or unreadable index means no
// injection, never a block.
const (
	LessonIndexSchemaVersion = "compound.hook_lesson_index.v0.1"
	// LessonIndexRelPath sits in the rebuildable indexes/ family so a lost
	// index is rebuilt, never restored.
	LessonIndexRelPath = "indexes/hook-lessons.json"
	// LessonApplyMaxBytes caps one injected line. A lesson that needs more
	// than this is a skill, not a moment-of-action reminder.
	LessonApplyMaxBytes = 300
	// LessonIndexMaxBytes bounds the hook's single read. An index past this
	// size is ignored by the hook (no injection) so a runaway build cannot
	// turn the zero-wait path into a slow one.
	LessonIndexMaxBytes = 256 << 10
	// LessonMaxInjected caps how many lessons one tool call can surface.
	LessonMaxInjected = 2
	// lessonSubjectMaxBytes bounds the text a trigger regex scans.
	lessonSubjectMaxBytes = 16 << 10
)

// Lesson is one trigger -> how-to-apply pair.
type Lesson struct {
	ID string `json:"id"`
	// Tools lists native tool names the trigger applies to (case-insensitive).
	// Empty means any tool. "Bash" also covers the shell tool names other
	// harnesses use for the same capability.
	Tools []string `json:"tools,omitempty"`
	// Pattern is an RE2 regular expression matched against the tool's
	// command (shell tools) or its compact JSON input (all other tools).
	Pattern string `json:"pattern"`
	// Apply is the single line injected when the trigger matches.
	Apply string `json:"apply"`
	// Source names where the lesson came from (a lesson ref or a skill path).
	Source string `json:"source,omitempty"`
}

// LessonIndex is the on-disk index document.
type LessonIndex struct {
	SchemaVersion string   `json:"schema_version"`
	BuiltAt       string   `json:"built_at"`
	Lessons       []Lesson `json:"lessons"`
}

// LessonMatch is one lesson that fired for a tool call.
type LessonMatch struct {
	ID    string
	Apply string
}

// LessonIndexPath returns the index location inside a Compound home.
func LessonIndexPath(homePath string) string {
	return filepath.Join(homePath, filepath.FromSlash(LessonIndexRelPath))
}

var shellToolAliases = map[string]struct{}{
	"bash":         {},
	"shell":        {},
	"exec_command": {},
	"local_shell":  {},
}

func lessonToolMatches(tools []string, tool string) bool {
	if len(tools) == 0 {
		return true
	}
	tool = strings.ToLower(strings.TrimSpace(tool))
	_, toolIsShell := shellToolAliases[tool]
	for _, want := range tools {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "*" || want == tool {
			return true
		}
		if _, wantShell := shellToolAliases[want]; wantShell && toolIsShell {
			return true
		}
	}
	return false
}

// LessonSubject is the text a trigger pattern scans: the shell command for
// shell-shaped inputs, otherwise the compact JSON of the whole tool input.
func LessonSubject(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(toolInput, &fields); err == nil {
		for _, key := range []string{"command", "cmd"} {
			raw, ok := fields[key]
			if !ok {
				continue
			}
			var command string
			if json.Unmarshal(raw, &command) == nil && command != "" {
				return capLessonSubject(command)
			}
			var argv []string
			if json.Unmarshal(raw, &argv) == nil && len(argv) > 0 {
				return capLessonSubject(strings.Join(argv, " "))
			}
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, toolInput); err != nil {
		return capLessonSubject(string(toolInput))
	}
	return capLessonSubject(compact.String())
}

func capLessonSubject(s string) string {
	if len(s) > lessonSubjectMaxBytes {
		return s[:lessonSubjectMaxBytes]
	}
	return s
}

// MatchLessonsFile reads the index once and returns the lessons whose trigger
// matches this tool call, in index order, at most LessonMaxInjected. Every
// failure (missing file, oversized file, bad JSON, bad pattern) yields no
// match: the caller degrades to "no injection", never to a block.
func MatchLessonsFile(indexPath, tool string, toolInput json.RawMessage) []LessonMatch {
	data, ok := readLessonIndexBytes(indexPath)
	if !ok {
		return nil
	}
	var index LessonIndex
	if err := json.Unmarshal(data, &index); err != nil || index.SchemaVersion != LessonIndexSchemaVersion {
		return nil
	}
	return MatchLessons(index.Lessons, tool, LessonSubject(toolInput))
}

func readLessonIndexBytes(indexPath string) ([]byte, bool) {
	file, err := os.Open(indexPath)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	// Read one byte past the cap so an oversized index is detected without
	// a separate stat call.
	data, err := io.ReadAll(io.LimitReader(file, LessonIndexMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > LessonIndexMaxBytes {
		return nil, false
	}
	return data, true
}

// MatchLessons applies already-loaded lessons to one tool call.
func MatchLessons(lessons []Lesson, tool, subject string) []LessonMatch {
	if subject == "" {
		return nil
	}
	var matches []LessonMatch
	for _, lesson := range lessons {
		if len(matches) >= LessonMaxInjected {
			break
		}
		if lesson.Pattern == "" || lesson.Apply == "" || len(lesson.Apply) > LessonApplyMaxBytes {
			continue
		}
		if !lessonToolMatches(lesson.Tools, tool) {
			continue
		}
		re, err := regexp.Compile(lesson.Pattern)
		if err != nil || !re.MatchString(subject) {
			continue
		}
		matches = append(matches, LessonMatch{ID: lesson.ID, Apply: lesson.Apply})
	}
	return matches
}

// LessonContext renders matched lessons as the additionalContext text.
func LessonContext(matches []LessonMatch) string {
	if len(matches) == 0 {
		return ""
	}
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		lines = append(lines, "Compound lesson ("+match.ID+"): "+match.Apply)
	}
	return strings.Join(lines, "\n")
}

// ValidateLesson checks one lesson against the index contract.
func ValidateLesson(lesson Lesson) error {
	if strings.TrimSpace(lesson.ID) == "" {
		return fmt.Errorf("lesson needs an id")
	}
	if strings.TrimSpace(lesson.Pattern) == "" {
		return fmt.Errorf("lesson %s needs a pattern", lesson.ID)
	}
	if _, err := regexp.Compile(lesson.Pattern); err != nil {
		return fmt.Errorf("lesson %s pattern: %w", lesson.ID, err)
	}
	apply := strings.TrimSpace(lesson.Apply)
	if apply == "" {
		return fmt.Errorf("lesson %s needs an apply line", lesson.ID)
	}
	if strings.ContainsAny(apply, "\r\n") {
		return fmt.Errorf("lesson %s apply must be one line", lesson.ID)
	}
	if len(apply) > LessonApplyMaxBytes {
		return fmt.Errorf("lesson %s apply is %d bytes; limit is %d", lesson.ID, len(apply), LessonApplyMaxBytes)
	}
	return nil
}

// lessonInput accepts the field spellings a lesson producer is likely to use:
// trigger or pattern for the regex, apply or how_to_apply for the line, tool or
// tools for the tool filter.
type lessonInput struct {
	ID         string   `json:"id"`
	Tool       string   `json:"tool"`
	Tools      []string `json:"tools"`
	Pattern    string   `json:"pattern"`
	Trigger    string   `json:"trigger"`
	Apply      string   `json:"apply"`
	HowToApply string   `json:"how_to_apply"`
	Source     string   `json:"source"`
}

func (in lessonInput) lesson(defaultSource string) Lesson {
	tools := append([]string(nil), in.Tools...)
	if strings.TrimSpace(in.Tool) != "" {
		tools = append(tools, strings.TrimSpace(in.Tool))
	}
	pattern := in.Pattern
	if pattern == "" {
		pattern = in.Trigger
	}
	apply := in.Apply
	if apply == "" {
		apply = in.HowToApply
	}
	source := in.Source
	if source == "" {
		source = defaultSource
	}
	return Lesson{ID: strings.TrimSpace(in.ID), Tools: tools, Pattern: pattern, Apply: strings.TrimSpace(apply), Source: source}
}

// ParseLessonsJSON reads a lessons document: either {"lessons":[...]} or a
// bare array of lesson objects.
func ParseLessonsJSON(data []byte, source string) ([]Lesson, error) {
	trimmed := bytes.TrimSpace(data)
	var inputs []lessonInput
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &inputs); err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
	} else {
		var doc struct {
			Lessons []lessonInput `json:"lessons"`
		}
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		inputs = doc.Lessons
	}
	lessons := make([]Lesson, 0, len(inputs))
	for _, in := range inputs {
		lessons = append(lessons, in.lesson(source))
	}
	return lessons, nil
}

// skillTriggerPrefix marks a trigger line inside a skill markdown file. The
// rest of the line is one JSON lesson object, so a regex may contain any
// character without a custom escape syntax.
const skillTriggerPrefix = "trigger:"

// ParseSkillTriggerLines scans markdown under dir for lines of the form
// `trigger: {"id":..., "tools":[...], "pattern":..., "apply":...}`. Lines
// whose payload is not a JSON object are reported as errors, not skipped
// silently, so an author learns the line did nothing.
func ParseSkillTriggerLines(dir string) ([]Lesson, []error) {
	var lessons []Lesson
	var problems []error
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		line := 0
		for scanner.Scan() {
			line++
			text := strings.TrimSpace(scanner.Text())
			text = strings.TrimPrefix(text, "- ")
			if !strings.HasPrefix(text, skillTriggerPrefix) {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(text, skillTriggerPrefix))
			if !strings.HasPrefix(payload, "{") {
				problems = append(problems, fmt.Errorf("%s:%d: trigger line is not a JSON lesson object", path, line))
				continue
			}
			var in lessonInput
			if err := json.Unmarshal([]byte(payload), &in); err != nil {
				problems = append(problems, fmt.Errorf("%s:%d: %w", path, line, err))
				continue
			}
			lessons = append(lessons, in.lesson(fmt.Sprintf("%s:%d", filepath.ToSlash(path), line)))
		}
		if err := scanner.Err(); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", path, err))
		}
		return nil
	})
	if walkErr != nil {
		problems = append(problems, walkErr)
	}
	return lessons, problems
}

// BuildLessonIndex validates lessons, rejects duplicate ids, and returns the
// index document. Invalid lessons are returned as errors and left out.
func BuildLessonIndex(lessons []Lesson, now time.Time) (LessonIndex, []error) {
	var problems []error
	seen := map[string]string{}
	kept := make([]Lesson, 0, len(lessons))
	for _, lesson := range lessons {
		if err := ValidateLesson(lesson); err != nil {
			problems = append(problems, err)
			continue
		}
		if prior, ok := seen[lesson.ID]; ok {
			problems = append(problems, fmt.Errorf("lesson %s is defined twice (%s and %s)", lesson.ID, prior, lesson.Source))
			continue
		}
		seen[lesson.ID] = lesson.Source
		sort.Strings(lesson.Tools)
		kept = append(kept, lesson)
	}
	return LessonIndex{
		SchemaVersion: LessonIndexSchemaVersion,
		BuiltAt:       now.UTC().Format(time.RFC3339),
		Lessons:       kept,
	}, problems
}

// WriteLessonIndex writes the index atomically (temp file + rename) so the
// hook never reads a half-written document.
func WriteLessonIndex(path string, index LessonIndex) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > LessonIndexMaxBytes {
		return fmt.Errorf("lesson index is %d bytes; the hook reads at most %d", len(data), LessonIndexMaxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hook-lessons-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
