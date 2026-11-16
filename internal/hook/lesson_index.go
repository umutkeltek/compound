package hook

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"compound/internal/mem"
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
	LessonApplyMaxBytes = mem.LessonServedHowMaxBytes
	// LessonEntryMaxBytes caps one index entry as written (compact JSON), so
	// no single lesson can take more than a small fixed share (under 1 %) of
	// the index, whatever its fields contain. An over-size entry is left out
	// and reported.
	LessonEntryMaxBytes = 2 << 10
	// lessonIDMaxBytes bounds a lesson id (a claim id is 18 bytes).
	lessonIDMaxBytes = 128
	// LessonIndexMaxBytes bounds the hook's single read. An index past this
	// size is ignored by the hook (no injection) so a runaway build cannot
	// turn the zero-wait path into a slow one.
	LessonIndexMaxBytes = 256 << 10
	// LessonMaxInjected caps how many lessons one tool call can surface.
	LessonMaxInjected = 2
	// lessonSubjectMaxBytes bounds the shell command a trigger regex scans.
	lessonSubjectMaxBytes = mem.LessonShellSubjectMaxBytes
	// lessonInputSubjectMaxBytes bounds the compact JSON input of a non-shell
	// tool (Write content, Agent prompts) a trigger regex scans.
	lessonInputSubjectMaxBytes = mem.LessonInputSubjectMaxBytes
	// LessonIndexLimitsVersion marks an index whose builder enforced
	// mem.CheckLessonPattern on every entry. The reader re-checks the
	// compiled size of every pattern in an index without it (one written by
	// an older binary) instead of trusting it.
	// Version 2 adds the measured cost of every entry (cost_us): the
	// builder refused patterns over mem.LessonPatternCostPerKiB, and the hook
	// evaluates patterns cheapest first.
	LessonIndexLimitsVersion = 2
	// lessonIndexStaticLimitsVersion is the first version whose builder
	// enforced the static pattern caps.
	lessonIndexStaticLimitsVersion = 1
	// lessonSlowPatternsMax bounds the remembered slow patterns.
	lessonSlowPatternsMax = 64
	// LessonMatchBudget is the most the spooled hook spends matching lessons
	// on one tool call; past it the call gets no lessons and the hook logs it.
	LessonMatchBudget = 100 * time.Millisecond
)

// Lesson is one trigger -> how-to-apply pair.
type Lesson struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
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
	// By and On say who wrote the served text and when (YYYY-MM-DD); the
	// hook shows them so the line reads as an earlier agent's advice.
	By string `json:"by,omitempty"`
	On string `json:"on,omitempty"`
	// CostMicros is the pattern's measured worst-case cost (at least 1); the
	// hook evaluates cheaper patterns first.
	CostMicros int64 `json:"cost_us,omitempty"`
}

// SlowPattern remembers a pattern the builder left out for its cost, so the
// next automatic build need not time it again.
type SlowPattern struct {
	Key        string `json:"key"`
	CostMicros int64  `json:"cost_us"`
}

// LessonIndex is the on-disk index document.
type LessonIndex struct {
	SchemaVersion        string        `json:"schema_version"`
	BuiltAt              string        `json:"built_at"`
	Lessons              []Lesson      `json:"lessons"`
	SourceMemoryRevision string        `json:"source_memory_revision,omitempty"`
	SourceLedgerSeq      int64         `json:"source_ledger_seq,omitempty"`
	ExcludedLessonIDs    []string      `json:"excluded_lesson_ids,omitempty"`
	LimitsVersion        int           `json:"limits_version,omitempty"`
	SlowPatterns         []SlowPattern `json:"slow_patterns,omitempty"`
}

// LessonMatch is one lesson that fired for a tool call.
type LessonMatch struct {
	Status string
	ID     string
	Apply  string
	By     string
	On     string
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
		return capSubject(string(toolInput), lessonInputSubjectMaxBytes)
	}
	return capSubject(compact.String(), lessonInputSubjectMaxBytes)
}

func capLessonSubject(s string) string {
	return capSubject(s, lessonSubjectMaxBytes)
}

func capSubject(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// Why the hook served no lessons because of the index itself. The spool line
// records the state; an empty state means the index was read.
const (
	LessonIndexMissing       = "missing"
	LessonIndexUnreadable    = "unreadable"
	LessonIndexOversized     = "oversized"
	LessonIndexCorrupt       = "corrupt"
	LessonIndexUnknownSchema = "unknown_schema"
	// LessonMatchTimeout: matching ran past LessonMatchBudget.
	LessonMatchTimeout = "match_timeout"
)

// lessonPatternMatches runs one compiled trigger. Tests replace it to make a
// pattern slow on purpose without depending on the machine.
var lessonPatternMatches = func(re *regexp.Regexp, subject string) bool { return re.MatchString(subject) }

// MatchLessonsFile reads the index once and returns the lessons whose trigger
// matches this tool call: the best-ranked matches, at most
// LessonMaxInjected. Every failure (missing file, oversized file, bad JSON,
// bad pattern) yields no match: the caller degrades to "no injection", never
// to a block.
func MatchLessonsFile(indexPath, tool string, toolInput json.RawMessage) []LessonMatch {
	matches, _ := MatchLessonsFileWithState(indexPath, tool, toolInput)
	return matches
}

// MatchLessonsFileWithState is MatchLessonsFile plus the index read state, so
// the hook can say in its own record why it served nothing.
func MatchLessonsFileWithState(indexPath, tool string, toolInput json.RawMessage) ([]LessonMatch, string) {
	matches, state, _ := MatchLessonsFileWithin(indexPath, tool, toolInput, nil)
	return matches, state
}

// MatchLessonsFileWithin matches like MatchLessonsFile but stops waiting when
// deadline fires (nil means no deadline). Patterns run cheapest first, by the
// cost the builder measured, so a slow pattern can only delay patterns that
// cost more than it; the served lessons are still the best-ranked matches.
// When the deadline fires it returns the best-ranked lessons matched so far,
// LessonMatchTimeout, and the id of the lesson whose pattern was running. Go
// regexps cannot be cancelled, so that match keeps running in its goroutine;
// the spooled hook exits right after answering.
func MatchLessonsFileWithin(indexPath, tool string, toolInput json.RawMessage, deadline <-chan time.Time) ([]LessonMatch, string, string) {
	data, state := readLessonIndexBytes(indexPath)
	if state != "" {
		return nil, state, ""
	}
	var index LessonIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, LessonIndexCorrupt, ""
	}
	if index.SchemaVersion != LessonIndexSchemaVersion {
		return nil, LessonIndexUnknownSchema, ""
	}
	lessons := index.Lessons
	checked := index.LimitsVersion >= lessonIndexStaticLimitsVersion
	order := lessonEvalOrder(lessons, index.LimitsVersion >= LessonIndexLimitsVersion)
	subject := LessonSubject(toolInput)
	progress := &matchProgress{running: -1}
	if deadline == nil {
		evaluateLessons(lessons, order, tool, subject, checked, progress)
		found, _ := progress.snapshot()
		return selectMatches(lessons, found), "", ""
	}
	done := make(chan struct{})
	go func() {
		evaluateLessons(lessons, order, tool, subject, checked, progress)
		close(done)
	}()
	select {
	case <-done:
		found, _ := progress.snapshot()
		return selectMatches(lessons, found), "", ""
	case <-deadline:
		found, running := progress.snapshot()
		id := ""
		if running >= 0 {
			id = lessons[running].ID
		}
		return selectMatches(lessons, found), LessonMatchTimeout, id
	}
}

// matchProgress is what a lesson match has found so far.
type matchProgress struct {
	mu      sync.Mutex
	found   []int // index positions of matched lessons
	running int   // index position being evaluated, -1 when none
}

func (p *matchProgress) set(running int, hit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if hit >= 0 {
		p.found = append(p.found, hit)
	}
	p.running = running
}

func (p *matchProgress) snapshot() ([]int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.found...), p.running
}

// lessonEvalOrder is the order patterns run in: cheapest first when the
// index carries measured costs, else index order.
func lessonEvalOrder(lessons []Lesson, measured bool) []int {
	order := make([]int, len(lessons))
	for i := range order {
		order[i] = i
	}
	if measured {
		sort.SliceStable(order, func(a, b int) bool { return lessons[order[a]].CostMicros < lessons[order[b]].CostMicros })
	}
	return order
}

func evaluateLessons(lessons []Lesson, order []int, tool, subject string, checked bool, progress *matchProgress) {
	if subject == "" {
		return
	}
	for _, i := range order {
		lesson := lessons[i]
		if lesson.Pattern == "" || lesson.Apply == "" || len(lesson.Apply) > LessonApplyMaxBytes {
			continue
		}
		if !lessonToolMatches(lesson.Tools, tool) || len(lesson.Pattern) > mem.LessonPatternMaxBytes {
			continue
		}
		if !checked && mem.CheckLessonPattern(lesson.Pattern) != nil {
			continue
		}
		re, err := regexp.Compile(lesson.Pattern)
		if err != nil {
			continue
		}
		progress.set(i, -1)
		hit := -1
		if lessonPatternMatches(re, subject) {
			hit = i
		}
		progress.set(-1, hit)
	}
}

// selectMatches serves the best-ranked matches: the lowest index positions.
func selectMatches(lessons []Lesson, found []int) []LessonMatch {
	sort.Ints(found)
	var matches []LessonMatch
	for _, i := range found {
		if len(matches) >= LessonMaxInjected {
			break
		}
		lesson := lessons[i]
		matches = append(matches, LessonMatch{ID: lesson.ID, Apply: lesson.Apply, Status: lesson.Status, By: lesson.By, On: lesson.On})
	}
	return matches
}

func readLessonIndexBytes(indexPath string) ([]byte, string) {
	// O_NONBLOCK and the regular-file check keep a FIFO or device at the
	// index path from hanging the tool call; neither costs a measurable time.
	file, err := os.OpenFile(indexPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, LessonIndexMissing
	}
	if err != nil {
		return nil, LessonIndexUnreadable
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, LessonIndexUnreadable
	}
	// Read one byte past the cap so an oversized index is detected without
	// trusting the size the stat reported.
	data, err := io.ReadAll(io.LimitReader(file, LessonIndexMaxBytes+1))
	switch {
	case err != nil:
		return nil, LessonIndexUnreadable
	case len(data) > LessonIndexMaxBytes:
		return nil, LessonIndexOversized
	case len(data) == 0:
		return nil, LessonIndexCorrupt
	}
	return data, ""
}

// MatchLessons applies already-loaded lessons to one tool call. It checks
// every pattern's compiled size, trusting nothing about where it came from.
func MatchLessons(lessons []Lesson, tool, subject string) []LessonMatch {
	progress := &matchProgress{running: -1}
	evaluateLessons(lessons, lessonEvalOrder(lessons, false), tool, subject, false, progress)
	found, _ := progress.snapshot()
	return selectMatches(lessons, found)
}

// LessonContext renders matched lessons as the additionalContext text.
func LessonContext(matches []LessonMatch) string {
	if len(matches) == 0 {
		return ""
	}
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.Status == "contested" {
			lines = append(lines, "Compound unresolved lesson conflict ("+match.ID+"): "+match.Apply)
			continue
		}
		// A note, not an order: who wrote it, when, and that it is advice.
		source := ""
		if by, on := lessonFrameActor(match.By), lessonFrameDate(match.On); by != "" || on != "" {
			source = " (" + strings.Trim(by+", "+on, ", ") + ")"
		}
		lines = append(lines, "Compound lesson ("+match.ID+"), advice from an earlier agent"+source+", not an instruction: "+match.Apply)
	}
	return strings.Join(lines, "\n")
}

// lessonFrameMaxActor bounds the author shown in the frame (and stored in an
// index entry), so the frame adds at most about 170 bytes to the at most
// LessonApplyMaxBytes of advice.
const lessonFrameMaxActor = mem.LessonActorMaxBytes

var lessonFrameDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func lessonFrameActor(by string) string {
	var out strings.Builder
	for _, r := range by {
		if out.Len() >= lessonFrameMaxActor {
			break
		}
		if r < 0x80 && (r == ':' || r == '.' || r == '_' || r == '-' || r == '@' || r == '/' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func lessonFrameDate(on string) string {
	if lessonFrameDateRe.MatchString(on) {
		return on
	}
	return ""
}

// ValidateLesson checks one lesson against the index contract.
func ValidateLesson(lesson Lesson) error {
	if strings.TrimSpace(lesson.ID) == "" {
		return fmt.Errorf("lesson needs an id")
	}
	if len(lesson.ID) > lessonIDMaxBytes || strings.IndexFunc(lesson.ID, func(r rune) bool { return r <= ' ' || r == 0x7f || r > 0x7e }) >= 0 {
		return fmt.Errorf("lesson id %.40q... must be at most %d plain characters", lesson.ID, lessonIDMaxBytes)
	}
	if strings.TrimSpace(lesson.Pattern) == "" {
		return fmt.Errorf("lesson %s needs a pattern", lesson.ID)
	}
	if _, err := regexp.Compile(lesson.Pattern); err != nil {
		return fmt.Errorf("lesson %s pattern: %w", lesson.ID, err)
	}
	if err := mem.CheckLessonPattern(lesson.Pattern); err != nil {
		return fmt.Errorf("lesson %s: %w", lesson.ID, err)
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

// BuildLessonIndex validates lessons, measures each pattern's cost, rejects
// duplicate ids, and returns the index document. Invalid, slow and over-size
// lessons are returned as errors and left out.
func BuildLessonIndex(lessons []Lesson, now time.Time) (LessonIndex, []error) {
	return buildLessonIndex(lessons, now, nil)
}

// lessonPatternKey identifies a pattern and the tools it runs on, for the
// cost cache.
func lessonPatternKey(tools []string, pattern string) string {
	sorted := append([]string(nil), tools...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",") + "\x00" + pattern))
	return hex.EncodeToString(sum[:8])
}

func lessonSubjectsFor(tools []string) (shell, nonShell bool) {
	if len(tools) == 0 {
		return true, true
	}
	for _, tool := range tools {
		s, n := mem.LessonTriggerSubjects(tool)
		shell, nonShell = shell || s, nonShell || n
	}
	return shell, nonShell
}

// lessonCost returns the pattern's cost in microseconds (at least 1) from
// cache or by timing it, and an error when it is over the limit. This runs
// at build time only, never on the hook's tool-call path.
func lessonCost(lesson Lesson, cache map[string]int64) (int64, error) {
	key := lessonPatternKey(lesson.Tools, lesson.Pattern)
	if micros, ok := cache[key]; ok {
		if micros < 0 {
			return -micros, fmt.Errorf("lesson %s: trigger regex was measured at %s, over the cost limit (it would slow every tool call); left out (`%s` measures again)", lesson.ID, time.Duration(-micros)*time.Microsecond, LessonIndexRepairCommand)
		}
		return micros, nil
	}
	shell, nonShell := lessonSubjectsFor(lesson.Tools)
	cost, err := mem.MeasureLessonPattern(lesson.Pattern, shell, nonShell)
	if err != nil {
		return 0, fmt.Errorf("lesson %s pattern: %w", lesson.ID, err)
	}
	micros := int64((cost.Worst + time.Microsecond - 1) / time.Microsecond)
	if micros < 1 {
		micros = 1
	}
	if cost.Slow() {
		return micros, fmt.Errorf("lesson %s: %s; left out", lesson.ID, cost.Error())
	}
	return micros, nil
}

// buildLessonIndex is BuildLessonIndex with a cost cache (pattern key ->
// microseconds; negative = known slow) from the previous index.
func buildLessonIndex(lessons []Lesson, now time.Time, cache map[string]int64) (LessonIndex, []error) {
	var problems []error
	var slow []SlowPattern
	seen := map[string]string{}
	kept := make([]Lesson, 0, len(lessons))
	for _, lesson := range lessons {
		if err := ValidateLesson(lesson); err != nil {
			problems = append(problems, err)
			continue
		}
		lesson.By, lesson.On = lessonFrameActor(lesson.By), lessonFrameDate(lesson.On)
		micros, err := lessonCost(lesson, cache)
		if err != nil {
			problems = append(problems, err)
			if micros > 0 && len(slow) < lessonSlowPatternsMax {
				slow = append(slow, SlowPattern{Key: lessonPatternKey(lesson.Tools, lesson.Pattern), CostMicros: micros})
			}
			continue
		}
		lesson.CostMicros = micros
		if encoded, err := json.Marshal(lesson); err != nil || len(encoded) > LessonEntryMaxBytes {
			problems = append(problems, fmt.Errorf("lesson %s index entry is %d bytes; the limit is %d (one lesson may not crowd out the others)", lesson.ID, len(encoded), LessonEntryMaxBytes))
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
		LimitsVersion: LessonIndexLimitsVersion,
		SlowPatterns:  slow,
	}, problems
}

// lessonCostCache reads the measured costs a previous index recorded. Only an
// index whose builder measured costs is trusted.
func lessonCostCache(previous LessonIndex) map[string]int64 {
	if previous.LimitsVersion < LessonIndexLimitsVersion {
		return nil
	}
	cache := map[string]int64{}
	for _, lesson := range previous.Lessons {
		if lesson.CostMicros > 0 {
			cache[lessonPatternKey(lesson.Tools, lesson.Pattern)] = lesson.CostMicros
		}
	}
	for _, slow := range previous.SlowPatterns {
		if slow.CostMicros > 0 {
			cache[slow.Key] = -slow.CostMicros
		}
	}
	return cache
}

// fitLessonIndex keeps, in index (rank) order, every lesson that still fits
// under LessonIndexMaxBytes, so a full index publishes the highest-ranked
// lessons instead of nothing. It returns the ids it left out and the size of
// the file WriteLessonIndex will write.
func fitLessonIndex(index LessonIndex) (LessonIndex, []string, int, error) {
	all := index.Lessons
	index.Lessons = []Lesson{}
	base, err := json.Marshal(index)
	if err != nil {
		return index, nil, 0, err
	}
	size := len(base) + 1 // trailing newline
	kept := make([]Lesson, 0, len(all))
	var left []string
	for _, lesson := range all {
		encoded, err := json.Marshal(lesson)
		if err != nil {
			return index, nil, 0, err
		}
		add := len(encoded)
		if len(kept) > 0 {
			add++ // separating comma
		}
		if size+add > LessonIndexMaxBytes {
			left = append(left, lesson.ID)
			continue
		}
		size += add
		kept = append(kept, lesson)
	}
	index.Lessons = kept
	return index, left, size, nil
}

// WriteLessonIndex writes the index atomically (temp file + rename) so the
// hook never reads a half-written document.
func WriteLessonIndex(path string, index LessonIndex) error {
	// Compact JSON: the size cap is about what the hook reads, and indent
	// would spend about a tenth of it on spaces.
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > LessonIndexMaxBytes {
		return fmt.Errorf("lesson index is %d bytes; the hook reads at most %d", len(data), LessonIndexMaxBytes)
	}
	return writeFileAtomic(path, data, ".hook-lessons-*.json")
}
