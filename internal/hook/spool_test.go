package hook

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func spoolTestHome(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homePath, "observed"), 0o755); err != nil {
		t.Fatal(err)
	}
	return homePath
}

func spoolInput(command string) []byte {
	encoded, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "sess_spool",
		"cwd":             "/repo",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
	})
	return encoded
}

func TestNewSpoolLineRecordsSourceListsByDigestNotCopy(t *testing.T) {
	envelopes := `[{"id":"src_one","body":"SERVED-SOURCE-BODY-ONE"},{"id":"src_two","body":"SERVED-SOURCE-BODY-TWO"}]`
	raw := `{"hook_event_name":"PreToolUse","session_id":" sess_x ","tool_name":"Bash",` +
		`"tool_input":{"command":"ls"},"source_envelopes":` + envelopes +
		`,"input_sources":["compound://sources/a"],"evidence_refs":["compound://evidence/b","compound://evidence/c"]}`
	in := DecodeSpoolInput([]byte(raw))
	line := NewSpoolLine("pre-tool-use", "claude-code", in, lessonTestNow, nil)
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	for _, copied := range []string{"SERVED-SOURCE-BODY", "compound://sources/a", "compound://evidence/b"} {
		if strings.Contains(string(encoded), copied) {
			t.Fatalf("spool line copied a served source list (%s):\n%s", copied, encoded)
		}
	}
	if line.SourceLists["source_envelopes"].Count != 2 || line.SourceLists["evidence_refs"].Count != 2 || line.SourceLists["input_sources"].Count != 1 {
		t.Fatalf("source list digests = %+v", line.SourceLists)
	}
	if len(line.SourceLists["source_envelopes"].SHA256) != 64 {
		t.Fatalf("missing digest: %+v", line.SourceLists)
	}
	if line.Harness != "claude_code" || line.SessionID != "sess_x" || line.ToolName != "Bash" {
		t.Fatalf("line header = %+v", line)
	}
	// The caller's decoded map is untouched (the hook still matches lessons on it).
	if _, ok := in.Fields["source_envelopes"]; !ok {
		t.Fatal("NewSpoolLine mutated the caller's input map")
	}
	var replayInput map[string]json.RawMessage
	if err := json.Unmarshal(line.Input, &replayInput); err != nil || string(replayInput["tool_input"]) != `{"command":"ls"}` {
		t.Fatalf("replay input = %s (%v)", line.Input, err)
	}
}

func TestNewSpoolLineKeepsMultiLineInputOnOneLine(t *testing.T) {
	raw := "{\n  \"tool_name\": \"Bash\",\n  \"tool_input\": {\"command\": \"echo hi\"}\n}"
	line := NewSpoolLine("pre-tool-use", "codex", DecodeSpoolInput([]byte(raw)), lessonTestNow, nil)
	encoded, _ := json.Marshal(line)
	if strings.Contains(string(encoded), "\n") {
		t.Fatalf("spool line spans lines: %s", encoded)
	}
	bad := NewSpoolLine("pre-tool-use", "codex", DecodeSpoolInput([]byte("not json")), lessonTestNow, nil)
	if bad.InputError == "" || bad.InputSHA256 == "" || len(bad.Input) != 0 {
		t.Fatalf("invalid input line = %+v", bad)
	}
}

func TestAppendSpoolLineNeverScaffoldsAHome(t *testing.T) {
	notAHome := filepath.Join(t.TempDir(), "absent-home")
	line := NewSpoolLine("pre-tool-use", "claude_code", DecodeSpoolInput(spoolInput("ls")), lessonTestNow, nil)
	if err := AppendSpoolLine(notAHome, line, lessonTestNow); err == nil {
		t.Fatal("append into a missing home succeeded")
	}
	if _, err := os.Stat(notAHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool created a home skeleton: %v", err)
	}
}

func appendSpoolCommands(t *testing.T, homePath string, at time.Time, commands ...string) {
	t.Helper()
	for _, command := range commands {
		line := NewSpoolLine("pre-tool-use", "claude_code", DecodeSpoolInput(spoolInput(command)), at, []string{"lesson_x"})
		if err := AppendSpoolLine(homePath, line, at); err != nil {
			t.Fatal(err)
		}
	}
}

func commandOf(t *testing.T, line SpoolLine) string {
	t.Helper()
	var in struct {
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(line.Input, &in); err != nil {
		t.Fatal(err)
	}
	return in.ToolInput.Command
}

func TestDrainSpoolReplaysOnceInOrderAndSkipsPartialLines(t *testing.T) {
	homePath := spoolTestHome(t)
	yesterday := lessonTestNow.Add(-24 * time.Hour)
	appendSpoolCommands(t, homePath, yesterday, "y1", "y2")
	appendSpoolCommands(t, homePath, lessonTestNow, "t1", "fail-me", "t3")
	// A writer mid-append: no trailing newline yet.
	today := filepath.Join(SpoolDir(homePath), spoolDayName(lessonTestNow))
	file, err := os.OpenFile(today, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"schema_version":"compound.hook_spool.v0.1","hook":"pre-`); err != nil {
		t.Fatal(err)
	}
	file.Close()
	// Make yesterday's file old enough to be removed once drained.
	yesterdayFile := filepath.Join(SpoolDir(homePath), spoolDayName(yesterday))
	if err := os.Chtimes(yesterdayFile, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}

	var replayed []string
	replay := func(line SpoolLine) error {
		command := commandOf(t, line)
		if command == "fail-me" {
			return errors.New("replay failed on purpose")
		}
		replayed = append(replayed, command)
		return nil
	}
	report, err := DrainSpool(homePath, lessonTestNow, 0, replay)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(replayed, ",") != "y1,y2,t1,t3" {
		t.Fatalf("replayed = %v", replayed)
	}
	if report.Replayed != 4 || report.Rejected != 1 || report.LessonsSeen != 4 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(yesterdayFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drained past-day file was not removed: %v", err)
	}
	if _, err := os.Stat(today); err != nil {
		t.Fatalf("today's file must stay: %v", err)
	}
	rejected, err := os.ReadFile(filepath.Join(SpoolDir(homePath), spoolRejectedName))
	if err != nil || !strings.Contains(string(rejected), "replay failed on purpose") || !strings.Contains(string(rejected), "fail-me") {
		t.Fatalf("rejected log = %s (%v)", rejected, err)
	}

	// Second pass: nothing new, the partial line is still not consumed.
	replayed = nil
	report, err = DrainSpool(homePath, lessonTestNow, 0, replay)
	if err != nil || report.Replayed != 0 || report.Rejected != 0 || len(replayed) != 0 {
		t.Fatalf("second drain = %+v replayed=%v err=%v", report, replayed, err)
	}

	// The writer finishes its line; the next pass picks it up.
	file, _ = os.OpenFile(today, os.O_WRONLY|os.O_APPEND, 0o600)
	rest := `tool-use","harness":"claude_code","spooled_at":"2026-11-03T12:00:00Z","input":{"tool_name":"Bash","tool_input":{"command":"t4"}}}` + "\n"
	if _, err := file.WriteString(rest); err != nil {
		t.Fatal(err)
	}
	file.Close()
	report, err = DrainSpool(homePath, lessonTestNow, 0, replay)
	if err != nil || report.Replayed != 1 || strings.Join(replayed, ",") != "t4" {
		t.Fatalf("completed partial line = %+v replayed=%v err=%v", report, replayed, err)
	}
}

func TestDrainSpoolHonorsMaxLinesAndResumes(t *testing.T) {
	homePath := spoolTestHome(t)
	appendSpoolCommands(t, homePath, lessonTestNow, "a", "b", "c")
	var replayed []string
	replay := func(line SpoolLine) error {
		replayed = append(replayed, commandOf(t, line))
		return nil
	}
	if _, err := DrainSpool(homePath, lessonTestNow, 2, replay); err != nil {
		t.Fatal(err)
	}
	if _, err := DrainSpool(homePath, lessonTestNow, 2, replay); err != nil {
		t.Fatal(err)
	}
	if strings.Join(replayed, ",") != "a,b,c" {
		t.Fatalf("bounded drains replayed %v", replayed)
	}
}

func TestDrainSpoolRefusesASecondDrainer(t *testing.T) {
	homePath := spoolTestHome(t)
	appendSpoolCommands(t, homePath, lessonTestNow, "a")
	lock, err := os.OpenFile(filepath.Join(SpoolDir(homePath), spoolDrainLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockSpoolFile(lock, false); err != nil {
		t.Fatal(err)
	}
	defer unlockSpoolFile(lock)
	_, err = DrainSpool(homePath, lessonTestNow, 0, func(SpoolLine) error {
		t.Fatal("replayed while another drainer held the lock")
		return nil
	})
	if !errors.Is(err, ErrSpoolDrainBusy) {
		t.Fatalf("err = %v, want ErrSpoolDrainBusy", err)
	}
}
