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
)

// LessonSources are the extra index sources besides the ledger.
type LessonSources struct {
	SchemaVersion string   `json:"schema_version"`
	Skills        []string `json:"skills,omitempty"`
	From          []string `json:"from,omitempty"`
}

func (s LessonSources) empty() bool { return len(s.Skills) == 0 && len(s.From) == 0 }

// LessonIndexBuild is one published index plus the lessons it left out.
type LessonIndexBuild struct {
	Path     string
	Index    LessonIndex
	Problems []error
}

// Serves reports whether the published index carries lesson id.
func (b LessonIndexBuild) Serves(id string) bool {
	for _, lesson := range b.Index.Lessons {
		if lesson.ID == id {
			return true
		}
	}
	return false
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
// fail validation are returned as Problems and left out; an error means
// nothing was published and the previous index still serves.
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
	if err := WriteLessonIndex(out, index); err != nil {
		return build, err
	}
	build.Index = index
	return build, nil
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
	line := fmt.Sprintf("hook index rebuilt: %d lessons served", len(build.Index.Lessons))
	if n := len(build.Problems); n > 0 {
		line += fmt.Sprintf("; %d trigger lines left out (run `%s` to list them)", n, LessonIndexRepairCommand)
	}
	return line
}

// RecordLessonUses is the `work close --used` step: the memory owner records
// each used lesson (a used, contested lesson supersedes the other), then the
// index is rebuilt so the superseded lesson stops being served at once. Close
// has already succeeded, so a failed rebuild is a warning with its repair.
func RecordLessonUses(homePath, workID, actor, usedCSV string, now time.Time, stdout, stderr io.Writer) {
	if superseded := mem.RecordLessonUses(homePath, workID, actor, usedCSV, stdout, stderr); len(superseded) == 0 {
		return
	}
	build, err := RebuildLessonIndex(homePath, now)
	if err != nil {
		fmt.Fprintf(stderr, "warning: hook lesson index not rebuilt: %v; the superseded lesson is still served until `%s` succeeds\n", err, LessonIndexRepairCommand)
		return
	}
	fmt.Fprintln(stdout, LessonIndexSummary(build))
}
