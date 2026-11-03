package hook

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The hook spool is the zero-wait recording path for watch-only PreToolUse.
// The hook appends the native input as one JSON line and answers at once; a
// separate `compound hook drain` replays each line through the same
// record-building path the synchronous hook uses, so the records still land,
// just later and off the agent's critical path.
const (
	SpoolSchemaVersion       = "compound.hook_spool.v0.1"
	SpoolCursorSchemaVersion = "compound.hook_spool_cursor.v0.1"
	SpoolDrainSchemaVersion  = "compound.hook_spool_drain.v0.1"
	// SpoolRelDir lives in the observed/ evidence family; a new top-level
	// home directory would fail `compound home doctor`.
	SpoolRelDir        = "observed/hook-spool"
	spoolCursorName    = "cursor.json"
	spoolDrainLockName = "drain.lock"
	spoolRejectedName  = "rejected.ndjson"
	// spoolRemoveGrace keeps a drained past-day file for a moment so a writer
	// that resolved the day name just before midnight cannot append to an
	// unlinked inode.
	spoolRemoveGrace     = time.Minute
	spoolCursorSaveEvery = 64
)

// spoolSourceListKeys are served source lists a harness adapter may echo back
// in its hook input. They are recorded by hash only: copied source lists were
// most of the bytes of an action record, and the drain rebuilds the active
// sources from the home anyway.
var spoolSourceListKeys = []string{"source_envelopes", "input_sources", "evidence_refs"}

var spoolDayFile = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.ndjson$`)

// ErrSpoolDrainBusy reports that another drainer holds the drain lock.
var ErrSpoolDrainBusy = errors.New("another hook drain is running")

// SpoolSourceList records an omitted source list by digest.
type SpoolSourceList struct {
	SHA256 string `json:"sha256"`
	Count  int    `json:"count"`
	Bytes  int    `json:"bytes"`
}

// SpoolLine is one spooled hook call.
type SpoolLine struct {
	SchemaVersion string                     `json:"schema_version"`
	Hook          string                     `json:"hook"`
	Harness       string                     `json:"harness"`
	SpooledAt     string                     `json:"spooled_at"`
	SessionID     string                     `json:"session_id,omitempty"`
	ToolName      string                     `json:"tool_name,omitempty"`
	Lessons       []string                   `json:"lessons,omitempty"`
	Input         json.RawMessage            `json:"input,omitempty"`
	InputError    string                     `json:"input_error,omitempty"`
	InputSHA256   string                     `json:"input_sha256,omitempty"`
	SourceLists   map[string]SpoolSourceList `json:"omitted_source_lists,omitempty"`
}

// SpoolInput is the decoded native hook input the spool path needs.
type SpoolInput struct {
	Fields    map[string]json.RawMessage
	SessionID string
	ToolName  string
	ToolInput json.RawMessage
	Err       error
	Raw       []byte
}

// DecodeSpoolInput parses the native hook stdin once. A parse failure is kept
// on the result (the call is still recorded) rather than returned.
func DecodeSpoolInput(data []byte) SpoolInput {
	in := SpoolInput{Raw: data}
	if err := json.Unmarshal(data, &in.Fields); err != nil {
		in.Err = err
		return in
	}
	in.SessionID = rawJSONString(in.Fields["session_id"])
	in.ToolName = rawJSONString(in.Fields["tool_name"])
	in.ToolInput = in.Fields["tool_input"]
	return in
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// NewSpoolLine builds the record for one hook call. Source lists are replaced
// by their digest; the remaining input is compacted onto one line.
func NewSpoolLine(hookName, harnessName string, in SpoolInput, now time.Time, lessonIDs []string) SpoolLine {
	line := SpoolLine{
		SchemaVersion: SpoolSchemaVersion,
		Hook:          hookName,
		Harness:       NormalizeHarness(harnessName),
		SpooledAt:     now.UTC().Format(time.RFC3339Nano),
		SessionID:     in.SessionID,
		ToolName:      in.ToolName,
		Lessons:       lessonIDs,
	}
	if in.Err != nil {
		sum := sha256.Sum256(in.Raw)
		line.InputError = in.Err.Error()
		line.InputSHA256 = hex.EncodeToString(sum[:])
		return line
	}
	fields := in.Fields
	for _, key := range spoolSourceListKeys {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		if line.SourceLists == nil {
			line.SourceLists = map[string]SpoolSourceList{}
			// Copy before deleting so the caller's map is not mutated.
			copied := make(map[string]json.RawMessage, len(fields))
			for k, v := range fields {
				copied[k] = v
			}
			fields = copied
		}
		var items []json.RawMessage
		count := 0
		if json.Unmarshal(raw, &items) == nil {
			count = len(items)
		}
		sum := sha256.Sum256(raw)
		line.SourceLists[key] = SpoolSourceList{SHA256: hex.EncodeToString(sum[:]), Count: count, Bytes: len(raw)}
		delete(fields, key)
	}
	if line.SourceLists != nil {
		encoded, err := json.Marshal(fields)
		if err == nil {
			line.Input = encoded
		}
		return line
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, in.Raw); err == nil {
		line.Input = compact.Bytes()
	}
	return line
}

// SpoolDir returns the spool directory for a home.
func SpoolDir(homePath string) string {
	return filepath.Join(homePath, filepath.FromSlash(SpoolRelDir))
}

func spoolDayName(now time.Time) string {
	return now.UTC().Format("2006-01-02") + ".ndjson"
}

// AppendSpoolLine appends one line to today's spool file. It never creates a
// Compound home: when the home's observed/ family is absent it returns an
// error and the caller answers the harness without recording.
func AppendSpoolLine(homePath string, line SpoolLine, now time.Time) error {
	if strings.TrimSpace(homePath) == "" {
		return fmt.Errorf("hook spool needs a Compound home")
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	dir := SpoolDir(homePath)
	path := filepath.Join(dir, spoolDayName(now))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		// Create only the leaf: a missing observed/ means there is no home
		// here, and the spool must not scaffold one.
		if mkErr := os.Mkdir(dir, 0o700); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
			return fmt.Errorf("hook spool dir: %w", mkErr)
		}
		file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockSpoolFile(file, true); err != nil {
		return err
	}
	defer unlockSpoolFile(file)
	_, err = file.Write(encoded)
	return err
}

type spoolCursor struct {
	SchemaVersion string           `json:"schema_version"`
	Offsets       map[string]int64 `json:"offsets"`
}

func loadSpoolCursor(dir string) (spoolCursor, error) {
	cursor := spoolCursor{SchemaVersion: SpoolCursorSchemaVersion, Offsets: map[string]int64{}}
	data, err := os.ReadFile(filepath.Join(dir, spoolCursorName))
	if errors.Is(err, os.ErrNotExist) {
		return cursor, nil
	}
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(data, &cursor); err != nil {
		return cursor, fmt.Errorf("hook spool cursor: %w", err)
	}
	if cursor.Offsets == nil {
		cursor.Offsets = map[string]int64{}
	}
	cursor.SchemaVersion = SpoolCursorSchemaVersion
	return cursor, nil
}

func saveSpoolCursor(dir string, cursor spoolCursor) error {
	data, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cursor-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, spoolCursorName))
}

// SpoolDrainFile summarizes one day file in a drain pass.
type SpoolDrainFile struct {
	Name     string `json:"name"`
	From     int64  `json:"from_offset"`
	To       int64  `json:"to_offset"`
	Replayed int    `json:"replayed"`
	Rejected int    `json:"rejected"`
	Removed  bool   `json:"removed,omitempty"`
}

// SpoolDrainReport is the result of one drain pass.
type SpoolDrainReport struct {
	SchemaVersion string           `json:"schema_version"`
	Replayed      int              `json:"replayed"`
	Rejected      int              `json:"rejected"`
	LessonsSeen   int              `json:"lessons_seen"`
	Files         []SpoolDrainFile `json:"files"`
	RejectedRef   string           `json:"rejected_ref,omitempty"`
}

// SpoolReplay rebuilds the records for one spooled call.
type SpoolReplay func(line SpoolLine) error

// DrainSpool replays every complete, not-yet-drained spool line, oldest day
// first. A line that cannot be replayed is copied to rejected.ndjson with its
// error and the cursor moves past it, so one bad line never stalls the queue.
// A trailing partial line (a writer mid-append) is left for the next pass.
// maxLines bounds one pass; zero means no bound.
func DrainSpool(homePath string, now time.Time, maxLines int, replay SpoolReplay) (SpoolDrainReport, error) {
	report := SpoolDrainReport{SchemaVersion: SpoolDrainSchemaVersion, Files: []SpoolDrainFile{}}
	dir := SpoolDir(homePath)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, spoolDrainLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	if err := lockSpoolFile(lock, false); err != nil {
		return report, ErrSpoolDrainBusy
	}
	defer unlockSpoolFile(lock)

	cursor, err := loadSpoolCursor(dir)
	if err != nil {
		return report, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && spoolDayFile.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	today := spoolDayName(now)
	budget := maxLines
	for _, name := range names {
		if maxLines > 0 && budget <= 0 {
			break
		}
		fileReport, consumed, err := drainSpoolFile(dir, name, &cursor, budget, maxLines > 0, replay, &report)
		budget -= consumed
		if err != nil {
			_ = saveSpoolCursor(dir, cursor)
			return report, err
		}
		if name < today {
			info, statErr := os.Stat(filepath.Join(dir, name))
			if statErr == nil && info.Size() == cursor.Offsets[name] && now.Sub(info.ModTime()) >= spoolRemoveGrace {
				if os.Remove(filepath.Join(dir, name)) == nil {
					delete(cursor.Offsets, name)
					fileReport.Removed = true
				}
			}
		}
		if fileReport.Replayed > 0 || fileReport.Rejected > 0 || fileReport.Removed {
			report.Files = append(report.Files, fileReport)
		}
	}
	// Forget cursor entries whose file is gone.
	for name := range cursor.Offsets {
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			delete(cursor.Offsets, name)
		}
	}
	if report.Rejected > 0 {
		report.RejectedRef = filepath.ToSlash(filepath.Join(SpoolRelDir, spoolRejectedName))
	}
	return report, saveSpoolCursor(dir, cursor)
}

func drainSpoolFile(dir, name string, cursor *spoolCursor, budget int, bounded bool, replay SpoolReplay, report *SpoolDrainReport) (SpoolDrainFile, int, error) {
	start := cursor.Offsets[name]
	fileReport := SpoolDrainFile{Name: name, From: start, To: start}
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return fileReport, 0, err
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return fileReport, 0, err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	offset := start
	consumed := 0
	for {
		if bounded && consumed >= budget {
			break
		}
		raw, err := reader.ReadBytes('\n')
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			// EOF, or a partial line a writer is still appending.
			if err != nil && !errors.Is(err, io.EOF) {
				return fileReport, consumed, err
			}
			break
		}
		consumed++
		var line SpoolLine
		replayErr := json.Unmarshal(raw, &line)
		if replayErr == nil && line.SchemaVersion != SpoolSchemaVersion {
			replayErr = fmt.Errorf("unknown spool schema %q", line.SchemaVersion)
		}
		if replayErr == nil && line.InputError != "" {
			replayErr = fmt.Errorf("spooled input was not JSON: %s", line.InputError)
		}
		if replayErr == nil {
			replayErr = replay(line)
		}
		if replayErr != nil {
			if err := appendSpoolRejected(dir, name, offset, raw, replayErr); err != nil {
				return fileReport, consumed, err
			}
			fileReport.Rejected++
			report.Rejected++
		} else {
			fileReport.Replayed++
			report.Replayed++
			report.LessonsSeen += len(line.Lessons)
		}
		offset += int64(len(raw))
		cursor.Offsets[name] = offset
		fileReport.To = offset
		if consumed%spoolCursorSaveEvery == 0 {
			if err := saveSpoolCursor(dir, *cursor); err != nil {
				return fileReport, consumed, err
			}
		}
	}
	if _, ok := cursor.Offsets[name]; !ok {
		cursor.Offsets[name] = offset
	}
	return fileReport, consumed, nil
}

func appendSpoolRejected(dir, name string, offset int64, raw []byte, cause error) error {
	record := struct {
		File   string          `json:"file"`
		Offset int64           `json:"offset"`
		Error  string          `json:"error"`
		Line   json.RawMessage `json:"line,omitempty"`
		Text   string          `json:"text,omitempty"`
	}{File: name, Offset: offset, Error: cause.Error()}
	trimmed := bytes.TrimSpace(raw)
	if json.Valid(trimmed) {
		record.Line = trimmed
	} else {
		record.Text = string(trimmed)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, spoolRejectedName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(encoded, '\n'))
	return err
}
