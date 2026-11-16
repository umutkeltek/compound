package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"compound/internal/mem"
)

// A lesson write serves itself. The index is compiled from the memory ledger
// (every current lesson claim with a trigger) plus the extra sources an
// explicit build named, and every command that changes a lesson (write,
// retire, amend, restore, and the `work close --used` supersede step) rebuilds
// it as part of the same command. No one has to remember to rebuild it, and
// no one has to pass skill folders by hand to get a ledger lesson served.
const (
	LessonSourcesSchemaVersion = "compound.hook_lesson_sources.v0.1"
	// LessonSourcesRelPath records the extra sources (skill folders and
	// lesson files) the last explicit `compound hook lessons-index` named, so
	// automatic rebuilds read the same ones. It is configuration, not derived
	// state: losing the index must not lose its sources.
	LessonSourcesRelPath = "config/hook-lesson-sources.json"
	// lessonIndexLockRelPath serializes index builds in one home. A build reads
	// the ledger and publishes inside the lock, so the last build to finish
	// always saw every ledger write that finished before it started.
	lessonIndexLockRelPath = "lock/hook-lessons.lock"
	// LessonIndexRepairCommand rebuilds the index by hand.
	LessonIndexRepairCommand = "compound hook lessons-index"
	// LessonIndexWarnPercent is the fill level at which every build warns
	// that the index is nearly full.
	LessonIndexWarnPercent = 80
)

// LessonSources are the extra index sources besides the ledger.
type LessonSources struct {
	SchemaVersion string   `json:"schema_version"`
	Skills        []string `json:"skills,omitempty"`
	From          []string `json:"from,omitempty"`
}

func (s LessonSources) empty() bool { return len(s.Skills) == 0 && len(s.From) == 0 }

// LessonIndexBuild is one published index plus what it left out.
type LessonIndexBuild struct {
	Path     string
	Index    LessonIndex
	Problems []error
	// Unserved are valid lessons left out because the index is full, lowest
	// rank first out. They stay in the ledger and go live once room is made.
	Unserved []string
	// Bytes is the published file size.
	Bytes int
}

// Serves reports whether the published index carries lesson id.
func (b LessonIndexBuild) Serves(id string) bool {
	return b.Rank(id) > 0
}

// Rank is id's 1-based position in the published index, 0 when absent. A
// tool call serves the first LessonMaxInjected matches in this order.
func (b LessonIndexBuild) Rank(id string) int {
	for i, lesson := range b.Index.Lessons {
		if lesson.ID == id {
			return i + 1
		}
	}
	return 0
}

// FillPercent is the published size as a share of LessonIndexMaxBytes.
func (b LessonIndexBuild) FillPercent() int {
	return b.Bytes * 100 / LessonIndexMaxBytes
}

// LessonSourcesPath returns the recorded-sources file inside a home.
func LessonSourcesPath(homePath string) string {
	return filepath.Join(homePath, filepath.FromSlash(LessonSourcesRelPath))
}

// ReadLessonSources returns the recorded extra sources; none recorded is empty.
func ReadLessonSources(homePath string) (LessonSources, error) {
	data, err := os.ReadFile(LessonSourcesPath(homePath))
	if os.IsNotExist(err) {
		return LessonSources{SchemaVersion: LessonSourcesSchemaVersion}, nil
	}
	if err != nil {
		return LessonSources{}, err
	}
	var sources LessonSources
	if err := json.Unmarshal(data, &sources); err != nil {
		return LessonSources{}, fmt.Errorf("%s: %w", LessonSourcesRelPath, err)
	}
	if sources.SchemaVersion != LessonSourcesSchemaVersion {
		return LessonSources{}, fmt.Errorf("%s: schema %q, want %q", LessonSourcesRelPath, sources.SchemaVersion, LessonSourcesSchemaVersion)
	}
	return sources, nil
}

// WriteLessonSources records sources as absolute paths (temp file + rename),
// so a rebuild started from any folder reads the same ones.
func WriteLessonSources(homePath string, sources LessonSources) (LessonSources, error) {
	abs := func(paths []string) ([]string, error) {
		out := make([]string, 0, len(paths))
		for _, path := range paths {
			resolved, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			out = append(out, resolved)
		}
		return out, nil
	}
	var err error
	recorded := LessonSources{SchemaVersion: LessonSourcesSchemaVersion}
	if recorded.Skills, err = abs(sources.Skills); err != nil {
		return recorded, err
	}
	if recorded.From, err = abs(sources.From); err != nil {
		return recorded, err
	}
	data, err := json.MarshalIndent(recorded, "", "  ")
	if err != nil {
		return recorded, err
	}
	return recorded, writeFileAtomic(LessonSourcesPath(homePath), append(data, '\n'), ".hook-lesson-sources-*.json")
}

// RebuildLessonIndex recompiles the home's index from the ledger and the
// recorded sources. Commands that change a lesson call it after their ledger
// write; a failure leaves the last complete index in place.
func RebuildLessonIndex(homePath string, now time.Time) (LessonIndexBuild, error) {
	sources, err := ReadLessonSources(homePath)
	if err != nil {
		return LessonIndexBuild{}, err
	}
	return CompileLessonIndex(homePath, LessonIndexPath(homePath), sources, now)
}

// CompileLessonIndex builds and publishes one index at out. The ledger is
// always a source; sources adds skill folders and lesson files. Lessons that
// fail validation are returned as Problems and left out; lessons past the
// size cap are returned as Unserved and left out. An error means nothing was
// published and the previous index still serves.
func CompileLessonIndex(homePath, out string, sources LessonSources, now time.Time) (LessonIndexBuild, error) {
	build := LessonIndexBuild{Path: out}
	unlock, err := lockLessonIndexBuild(homePath)
	if err != nil {
		return build, err
	}
	defer unlock()
	var lessons []Lesson
	for _, path := range sources.From {
		data, err := os.ReadFile(path)
		if err != nil {
			build.Problems = append(build.Problems, err)
			continue
		}
		parsed, err := ParseLessonsJSON(data, path)
		if err != nil {
			build.Problems = append(build.Problems, err)
			continue
		}
		lessons = append(lessons, parsed...)
	}
	for _, dir := range sources.Skills {
		parsed, errs := ParseSkillTriggerLines(dir)
		lessons = append(lessons, parsed...)
		build.Problems = append(build.Problems, errs...)
	}
	projection, err := currentLessonInstructions(homePath, lessons)
	if err != nil {
		return build, fmt.Errorf("resolve current lessons: %w", err)
	}
	build.Problems = append(build.Problems, projection.problems...)
	index, buildProblems := BuildLessonIndex(projection.lessons, now)
	index.SourceMemoryRevision = projection.revision
	index.SourceLedgerSeq = projection.generation
	index.ExcludedLessonIDs = projection.excluded
	build.Problems = append(build.Problems, buildProblems...)
	fitted, unserved, size, err := fitLessonIndex(index)
	if err != nil {
		return build, err
	}
	build.Problems = append(build.Problems, droppedSkillLines(out, fitted, unserved)...)
	if err := WriteLessonIndex(out, fitted); err != nil {
		return build, err
	}
	build.Index, build.Unserved, build.Bytes = fitted, unserved, size
	return build, nil
}

// droppedSkillLines reports skill-file and --from lines that the index at
// path serves now but the new index will not, because no recorded source
// carries them any more. The first automatic rebuild on a home whose index
// was built by hand from skill folders (and has no recorded sources) would
// otherwise drop those lines silently.
func droppedSkillLines(path string, next LessonIndex, unserved []string) []error {
	data, state := readLessonIndexBytes(path)
	if state != "" {
		return nil
	}
	var previous LessonIndex
	if json.Unmarshal(data, &previous) != nil {
		return nil
	}
	kept := map[string]bool{}
	for _, lesson := range next.Lessons {
		kept[lesson.ID] = true
	}
	for _, id := range unserved {
		kept[id] = true
	}
	var problems []error
	for _, lesson := range previous.Lessons {
		if kept[lesson.ID] || strings.HasPrefix(lesson.Source, "lesson:") || strings.HasPrefix(lesson.ID, "claim_") {
			continue
		}
		problems = append(problems, fmt.Errorf("lesson %s (from %s) was served but is in no recorded source now; record its folder with `%s --skills DIR` to keep it", lesson.ID, lesson.Source, LessonIndexRepairCommand))
	}
	return problems
}

// RemoveLessonsFromIndex drops ids from the published index without a full
// rebuild. A removal only shrinks the file, so it always fits. Retire and the
// supersede step use it when a full rebuild fails, so a lesson that must stop
// being served stops even then. A missing or unreadable index already serves
// nothing, so there is nothing to remove.
func RemoveLessonsFromIndex(homePath string, ids []string) ([]string, error) {
	unlock, err := lockLessonIndexBuild(homePath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	path := LessonIndexPath(homePath)
	data, state := readLessonIndexBytes(path)
	if state != "" {
		return nil, nil
	}
	var index LessonIndex
	if json.Unmarshal(data, &index) != nil || index.SchemaVersion != LessonIndexSchemaVersion {
		return nil, nil
	}
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := make([]Lesson, 0, len(index.Lessons))
	var removed []string
	for _, lesson := range index.Lessons {
		if drop[lesson.ID] {
			removed = append(removed, lesson.ID)
			continue
		}
		kept = append(kept, lesson)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	index.Lessons = kept
	index.ExcludedLessonIDs = append(index.ExcludedLessonIDs, removed...)
	return removed, WriteLessonIndex(path, index)
}

func lockLessonIndexBuild(homePath string) (func(), error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME for the lesson index lock")
	}
	path := filepath.Join(homePath, filepath.FromSlash(lessonIndexLockRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	// Keep the lock file: removing it would let two builders lock different
	// inodes and publish out of order.
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func writeFileAtomic(path string, data []byte, pattern string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), pattern)
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
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// LessonIndexSummary is the short status a lesson-changing command prints
// about the index it rebuilt.
func LessonIndexSummary(build LessonIndexBuild) string {
	line := fmt.Sprintf("hook index rebuilt: %d lessons indexed (%d%% of the %d KiB cap)", len(build.Index.Lessons), build.FillPercent(), LessonIndexMaxBytes>>10)
	if n := len(build.Unserved); n > 0 {
		line += fmt.Sprintf("; FULL, %d lessons not served: %s", n, LessonIDList(build.Unserved))
	} else if build.FillPercent() >= LessonIndexWarnPercent {
		line += "; nearly full"
	}
	if n := len(build.Problems); n > 0 {
		line += fmt.Sprintf("; %d trigger lines or ledger rows left out (run `%s` to list them)", n, LessonIndexRepairCommand)
	}
	return line
}

// LessonIndexFullAdvice says what to do about a full or nearly full index.
func LessonIndexFullAdvice(build LessonIndexBuild) string {
	switch {
	case len(build.Unserved) > 0:
		return fmt.Sprintf("the hook index is full (%d KiB): %d lessons are in the ledger but not served (%s); retire or amend lessons (`compound memory lesson retire <claim_id> --reason ... --by <actor>`) to make room",
			LessonIndexMaxBytes>>10, len(build.Unserved), LessonIDList(build.Unserved))
	case build.FillPercent() >= LessonIndexWarnPercent:
		return fmt.Sprintf("the hook index is %d%% full (%d KiB cap); retire lessons that no longer hold before it fills", build.FillPercent(), LessonIndexMaxBytes>>10)
	}
	return ""
}

// LessonIDList renders at most ten ids.
func LessonIDList(ids []string) string {
	if len(ids) <= 10 {
		return strings.Join(ids, ",")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:10], ","), len(ids)-10)
}

// RecordLessonUses is the `work close --used` step: the memory owner records
// each used lesson (a used, contested lesson supersedes the other), then the
// index is rebuilt so the superseded lesson stops being served at once. If
// the rebuild fails, the superseded lessons are removed from the published
// index instead. Close has already succeeded, so failures are warnings.
func RecordLessonUses(homePath, workID, actor, usedCSV string, now time.Time, stdout, stderr io.Writer) {
	superseded := mem.RecordLessonUses(homePath, workID, actor, usedCSV, stdout, stderr)
	if len(superseded) == 0 {
		return
	}
	build, err := RebuildLessonIndex(homePath, now)
	if err != nil {
		if _, removeErr := RemoveLessonsFromIndex(homePath, superseded); removeErr != nil {
			fmt.Fprintf(stderr, "warning: hook lesson index not rebuilt (%v) and the superseded lessons could not be removed from it (%v); they are still served until `%s` succeeds\n", err, removeErr, LessonIndexRepairCommand)
			return
		}
		fmt.Fprintf(stderr, "warning: hook lesson index not rebuilt (%v); the superseded lessons were removed from it; run `%s` to repair the rest\n", err, LessonIndexRepairCommand)
		return
	}
	fmt.Fprintln(stdout, LessonIndexSummary(build))
	if advice := LessonIndexFullAdvice(build); advice != "" {
		fmt.Fprintln(stderr, "warning:", advice)
	}
}
