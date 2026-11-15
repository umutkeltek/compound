package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"compound/internal/home"
)

// RunSpoolCommand is the zero-wait PreToolUse path for watch-only harnesses.
// It reads stdin, matches the lesson index (one small file read), appends one
// spool line, and answers through respond, which owns the native response
// shape. It never classifies policy, never writes the event store, and always
// exits 0: a recording failure must not become a gate.
func RunSpoolCommand(harnessName, hookName string, stdin io.Reader, stdout, stderr io.Writer, now time.Time, respond func(lessonContext string) any) int {
	data, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "hook spool: read stdin:", err)
	}
	homePath, homeErr := home.Resolve("")
	lessonContext := ""
	if len(data) > 0 {
		in := DecodeSpoolInput(data)
		var lessonIDs []string
		indexState := ""
		if in.Err == nil && homeErr == nil {
			indexPath := LessonIndexPath(homePath)
			matches, state := MatchLessonsFileWithState(indexPath, NormalizeNativeTool(harnessName, in.ToolName), in.ToolInput)
			for _, match := range matches {
				lessonIDs = append(lessonIDs, match.ID)
			}
			lessonContext = LessonContext(matches)
			indexState = state
			if state != "" && state != LessonIndexMissing {
				// A broken index means no lessons, never a failed or slow call.
				fmt.Fprintf(stderr, "hook spool: lesson index %s at %s; serving no lessons (repair: %s)\n", state, indexPath, LessonIndexRepairCommand)
			}
		}
		if homeErr == nil {
			// Same rule as every other home writer: never append to a home
			// this binary cannot mutate (missing or newer manifest). The
			// manifest is about 100 bytes; the check costs about 2 ms.
			homeErr = home.RequireMutationCompatible(homePath)
		}
		if homeErr == nil {
			line := NewSpoolLine(hookName, harnessName, in, now, lessonIDs)
			line.LessonIndex = indexState
			if err := AppendSpoolLine(homePath, line, now); err != nil {
				fmt.Fprintln(stderr, "hook spool: not recorded:", err)
			}
		} else {
			fmt.Fprintln(stderr, "hook spool: home unavailable; not recorded:", homeErr)
		}
	}
	encoded, err := json.Marshal(respond(lessonContext))
	if err != nil {
		fmt.Fprintln(stderr, "hook spool: encode response:", err)
		return 0
	}
	_, _ = stdout.Write(append(encoded, '\n'))
	return 0
}

const drainUsage = "usage: compound hook drain [--once|--follow] [--interval 2s] [--max-lines N]"

// RunDrainCommand replays spooled PreToolUse calls through the record-building
// path the synchronous hook uses; newReplay supplies that path. It records; it
// never delivers. Exit codes: 0 ok, 1 error, 2 usage, 3 another drainer runs.
func RunDrainCommand(args []string, stdout, stderr io.Writer, newReplay func(homePath string) (SpoolReplay, error)) int {
	follow := false
	interval := 2 * time.Second
	maxLines := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--once":
			follow = false
		case "--follow":
			follow = true
		case "--interval":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, drainUsage)
				return 2
			}
			i++
			parsed, err := time.ParseDuration(args[i])
			if err != nil || parsed <= 0 {
				fmt.Fprintln(stderr, drainUsage)
				return 2
			}
			interval = parsed
		case "--max-lines":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, drainUsage)
				return 2
			}
			i++
			parsed, err := strconv.Atoi(args[i])
			if err != nil || parsed < 0 {
				fmt.Fprintln(stderr, drainUsage)
				return 2
			}
			maxLines = parsed
		case "-h", "--help":
			fmt.Fprintln(stdout, drainUsage)
			return 0
		default:
			fmt.Fprintln(stderr, drainUsage)
			return 2
		}
	}
	homePath, err := home.Resolve("")
	if err == nil {
		err = home.RequireMutationCompatible(homePath)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	replay, err := newReplay(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !follow {
		report, err := DrainSpool(homePath, time.Now(), maxLines, replay)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			if errors.Is(err, ErrSpoolDrainBusy) {
				return 3
			}
			return 1
		}
		writeDrainReport(stdout, report)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		report, err := DrainSpool(homePath, time.Now(), maxLines, replay)
		switch {
		case errors.Is(err, ErrSpoolDrainBusy):
			// Another drainer owns the queue; keep polling in case it exits.
		case err != nil:
			fmt.Fprintln(stderr, "error:", err)
		case report.Replayed > 0 || report.Rejected > 0:
			writeDrainReport(stdout, report)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(interval):
		}
	}
}

func writeDrainReport(stdout io.Writer, report SpoolDrainReport) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return
	}
	_, _ = stdout.Write(append(encoded, '\n'))
}

const lessonsIndexUsage = "usage: compound hook lessons-index [--from lessons.json]... [--skills DIR]... [--out PATH] [--json]\n" +
	"  Builds the index from the memory ledger (every current lesson with a trigger) plus the named sources.\n" +
	"  Named sources are recorded for the home's own index; with none named, the recorded ones are read."

// RunLessonsIndexCommand builds the PreToolUse lesson trigger index by hand.
// Lesson-changing commands rebuild it on their own (RebuildLessonIndex); this
// is the repair door and the way to record extra sources. Invalid lessons are
// reported and left out; the valid subset is still written, and the exit code
// is 1 so automation notices the rejected lines.
func RunLessonsIndexCommand(args []string, stdout, stderr io.Writer) int {
	var fromFiles, skillDirs []string
	out := ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "--skills", "--out":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, lessonsIndexUsage)
				return 2
			}
			switch args[i] {
			case "--from":
				fromFiles = append(fromFiles, args[i+1])
			case "--skills":
				skillDirs = append(skillDirs, args[i+1])
			default:
				out = args[i+1]
			}
			i++
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, lessonsIndexUsage)
			return 0
		default:
			fmt.Fprintln(stderr, lessonsIndexUsage)
			return 2
		}
	}
	homePath, err := home.Resolve("")
	if err == nil && out == "" {
		err = home.RequireMutationCompatible(homePath)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	homeIndex := out == "" || filepath.Clean(out) == filepath.Clean(LessonIndexPath(homePath))
	if out == "" {
		out = LessonIndexPath(homePath)
	}
	// Named sources replace the recorded ones; with none named the build
	// reads the ledger plus what the last explicit build recorded.
	sources := LessonSources{SchemaVersion: LessonSourcesSchemaVersion, Skills: skillDirs, From: fromFiles}
	if sources.empty() {
		if sources, err = ReadLessonSources(homePath); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	build, err := CompileLessonIndex(homePath, out, sources, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	recordedPath := ""
	if homeIndex && (len(skillDirs) > 0 || len(fromFiles) > 0) {
		// The home's own index records its named sources so the automatic
		// rebuild after a lesson write reads the same folders.
		if _, err := WriteLessonSources(homePath, sources); err != nil {
			fmt.Fprintln(stderr, "error: index written but sources not recorded:", err)
			return 1
		}
		recordedPath = LessonSourcesPath(homePath)
	}
	index, problems := build.Index, build.Problems
	messages := make([]string, 0, len(problems))
	for _, problem := range problems {
		messages = append(messages, problem.Error())
	}
	if jsonOut {
		encoded, _ := json.MarshalIndent(map[string]any{
			"path":                   out,
			"lessons":                len(index.Lessons),
			"problems":               messages,
			"source_memory_revision": index.SourceMemoryRevision,
			"source_ledger_seq":      index.SourceLedgerSeq,
			"excluded_lesson_ids":    index.ExcludedLessonIDs,
			"skills":                 sources.Skills,
			"from":                   sources.From,
			"sources_recorded":       recordedPath,
		}, "", "  ")
		fmt.Fprintln(stdout, string(encoded))
	} else {
		fmt.Fprintf(stdout, "hook lessons index: %d lessons -> %s\n", len(index.Lessons), out)
		if recordedPath != "" {
			fmt.Fprintf(stdout, "sources recorded for automatic rebuilds: %s\n", recordedPath)
		}
		for _, message := range messages {
			fmt.Fprintln(stdout, "- rejected:", message)
		}
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}
