package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/hookplan"
	"compound/internal/mem"
	"compound/internal/promotedriver"
)

const spoolTestLessons = `{"lessons":[
 {"id":"lesson_replace_deletes_first","tools":["Bash"],"trigger":"\\basc\\s+screenshots\\b.*--replace","how_to_apply":"--replace deletes the live set before the upload succeeds; upload a new set first, then remove the old one."},
 {"id":"lesson_tail_hides_exit","tools":["Bash"],"trigger":"\\|\\s*tail\\b","how_to_apply":"A pipe into tail reports tail's exit code; redirect to a log and echo EXIT=$?."}
]}`

func initSpoolTestHome(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	return homePath
}

func writeSpoolTestLessonIndex(t *testing.T, homePath string) {
	t.Helper()
	lessons, err := hook.ParseLessonsJSON([]byte(spoolTestLessons), "test")
	if err != nil {
		t.Fatal(err)
	}
	index, problems := hook.BuildLessonIndex(lessons, cliTestNow)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if err := hook.WriteLessonIndex(hook.LessonIndexPath(homePath), index); err != nil {
		t.Fatal(err)
	}
}

func bashHookInput(sessionID, cwd, command string) string {
	return `{"hook_event_name":"PreToolUse","session_id":` + quoteJSON(sessionID) + `,"cwd":` + quoteJSON(cwd) +
		`,"tool_name":"Bash","tool_input":{"command":` + quoteJSON(command) + `}}`
}

func runSpool(t *testing.T, harnessName, input string) (claudeLiveHookResponse, int) {
	t.Helper()
	return runSpoolAt(t, harnessName, input, time.Now())
}

func runSpoolAt(t *testing.T, harnessName, input string, at time.Time) (claudeLiveHookResponse, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	parsed, err := parseHookArgs([]string{"pre-tool-use", "--live", "--harness", harnessName, "--spool"})
	if err != nil {
		t.Fatal(err)
	}
	code := runHookSpool(parsed, strings.NewReader(input), &stdout, &stderr, at)
	var resp claudeLiveHookResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("spool response is not the native hook shape: %v\n%s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return resp, code
}

func spoolLines(t *testing.T, homePath string) []hook.SpoolLine {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(hook.SpoolDir(homePath), "*.ndjson"))
	var lines []hook.SpoolLine
	for _, path := range paths {
		if filepath.Base(path) == "rejected.ndjson" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var line hook.SpoolLine
			if err := json.Unmarshal(raw, &line); err != nil {
				t.Fatalf("spool line %q: %v", raw, err)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func TestHookSpoolInjectsTheLessonAndLeavesTheEventStoreAlone(t *testing.T) {
	homePath := initSpoolTestHome(t)
	writeSpoolTestLessonIndex(t, homePath)
	repo := t.TempDir()

	for _, harnessName := range []string{"claude_code", "codex"} {
		resp, code := runSpool(t, harnessName, bashHookInput("sess_"+harnessName, repo, "asc screenshots upload --app app-fixture-1 --replace ./shots"))
		if code != 0 {
			t.Fatalf("%s spool exit = %d", harnessName, code)
		}
		out := resp.HookSpecificOutput
		if out.HookEventName != "PreToolUse" || !strings.Contains(out.AdditionalContext, "lesson_replace_deletes_first") ||
			!strings.Contains(out.AdditionalContext, "deletes the live set") {
			t.Fatalf("%s matching call response = %+v", harnessName, out)
		}
		if out.PermissionDecision != "" {
			t.Fatalf("%s spool path published a permission decision: %+v", harnessName, out)
		}

		resp, code = runSpool(t, harnessName, bashHookInput("sess_"+harnessName, repo, "go test ./internal/hook"))
		if code != 0 || resp.HookSpecificOutput.AdditionalContext != "" || resp.HookSpecificOutput.PermissionDecision != "" {
			t.Fatalf("%s unrelated call = %+v exit %d", harnessName, resp.HookSpecificOutput, code)
		}
	}

	lines := spoolLines(t, homePath)
	if len(lines) != 4 {
		t.Fatalf("spool lines = %d, want 4", len(lines))
	}
	if len(lines[0].Lessons) != 1 || lines[0].Lessons[0] != "lesson_replace_deletes_first" || len(lines[1].Lessons) != 0 {
		t.Fatalf("lesson ids on spool lines = %+v / %+v", lines[0].Lessons, lines[1].Lessons)
	}
	if lines[2].Harness != "codex" || lines[0].SessionID != "sess_claude_code" {
		t.Fatalf("spool line header = %+v", lines[2])
	}
	// The zero-wait path must not classify or persist anything else.
	if _, err := os.Stat(filepath.Join(homePath, "events", "hook-events.ndjson")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool path wrote the hook event stream: %v", err)
	}
}

func TestHookSpoolWithoutAHomeAnswersAndCreatesNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-home")
	t.Setenv("COMPOUND_HOME", missing)
	resp, code := runSpool(t, "claude_code", bashHookInput("s", "/repo", "asc screenshots --replace"))
	if code != 0 || resp.HookSpecificOutput.HookEventName != "PreToolUse" || resp.HookSpecificOutput.AdditionalContext != "" {
		t.Fatalf("no-home spool = %+v exit %d", resp.HookSpecificOutput, code)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool created a home: %v", err)
	}
	// A directory that looks like a home but has no manifest is not written to.
	noManifest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(noManifest, "observed"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_HOME", noManifest)
	if _, code := runSpool(t, "claude_code", bashHookInput("s", "/repo", "ls")); code != 0 {
		t.Fatalf("no-manifest spool exit = %d", code)
	}
	if _, err := os.Stat(hook.SpoolDir(noManifest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool wrote into a home without a manifest: %v", err)
	}
	// Empty stdin still answers with the native shape and records nothing.
	homePath := initSpoolTestHome(t)
	resp, code = runSpool(t, "claude_code", "")
	if code != 0 || resp.HookSpecificOutput.HookEventName != "PreToolUse" || len(spoolLines(t, homePath)) != 0 {
		t.Fatalf("empty stdin = %+v exit %d", resp.HookSpecificOutput, code)
	}
}

func TestParseHookArgsKeepsSpoolWatchOnly(t *testing.T) {
	for _, args := range [][]string{
		{"pre-tool-use", "--live", "--harness", "claude_code", "--spool", "--enforce"},
		{"post-tool-use", "--live", "--harness", "claude_code", "--spool"},
		{"pre-tool-use", "--spool", "fixtures/hooks/pre-tool-use.claude.json"},
		{"pre-tool-use", "--live", "--harness", "claude_code", "--spool", "--parity-log", "/tmp/x"},
	} {
		if _, err := parseHookArgs(args); err == nil {
			t.Fatalf("parseHookArgs(%v) accepted a spool form that is not watch-only pre-tool-use", args)
		}
	}
	parsed, err := parseHookArgs([]string{"pre-tool-use", "--live", "--harness", "codex", "--spool"})
	if err != nil || !parsed.Spool || parsed.Enforce {
		t.Fatalf("valid spool args = %+v, %v", parsed, err)
	}
	// Enforced lanes are unchanged: no --spool means the synchronous gate.
	parsed, err = parseHookArgs([]string{"pre-tool-use", "--live", "--harness", "claude_minimax", "--enforce"})
	if err != nil || parsed.Spool || !parsed.Enforce {
		t.Fatalf("enforced args = %+v, %v", parsed, err)
	}
}

var hookRecordID = regexp.MustCompile(`(act_live|act|pol|hook|work_live|hold|rev|appr)_[0-9a-f]{8,}`)

// normalizedHookEvents reads the hook event stream and strips the fields that
// legitimately differ between two runs of the same call (ids, timestamps).
func normalizedHookEvents(t *testing.T, homePath string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(homePath, "events", "hook-events.ndjson"))
	if err != nil {
		t.Fatalf("read hook events: %v", err)
	}
	var out []string
	for _, raw := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		// Live action ids (and the input hash derived from them) fold in the
		// call time, so two runs of one call differ only there.
		delete(event, "id")
		delete(event, "created_at")
		delete(event, "duration_ms")
		delete(event, "input_hash")
		encoded, _ := json.Marshal(event)
		out = append(out, hookRecordID.ReplaceAllString(string(encoded), "${1}_ID"))
	}
	sort.Strings(out)
	return out
}

// TestHookDrainWritesTheRecordsTheSynchronousHookWrites is the parity proof:
// the same native call, once through today's synchronous watch-only hook and
// once through spool + drain, leaves the same hook event in the home.
func TestHookDrainWritesTheRecordsTheSynchronousHookWrites(t *testing.T) {
	repo := t.TempDir()
	inputs := []string{
		bashHookInput("sess_parity", repo, "go test ./internal/hook"),
		bashHookInput("sess_parity", repo, "asc screenshots upload --app app-fixture-1 --replace ./shots"),
		`{"hook_event_name":"PreToolUse","session_id":"sess_parity","cwd":` + quoteJSON(repo) +
			`,"tool_name":"Write","tool_input":{"file_path":` + quoteJSON(filepath.Join(repo, "README.md")) + `,"content":"x"}}`,
	}

	syncHome := initSpoolTestHome(t)
	for _, input := range inputs {
		var code int
		captureStdout(t, func() {
			withStdin(t, input, func() {
				code = runHookCommand([]string{"pre-tool-use", "--live", "--harness", "claude_code"})
			})
		})
		if code != 0 {
			t.Fatalf("synchronous hook exit = %d", code)
		}
	}
	want := normalizedHookEvents(t, syncHome)

	spoolHome := initSpoolTestHome(t)
	writeSpoolTestLessonIndex(t, spoolHome)
	// Spool the calls 90 minutes ago so the drain time and the call time differ.
	callTime := time.Now().Add(-90 * time.Minute)
	for _, input := range inputs {
		if _, code := runSpoolAt(t, "claude_code", input, callTime); code != 0 {
			t.Fatalf("spool exit = %d", code)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := hook.RunDrainCommand([]string{"--once"}, &stdout, &stderr, newHookSpoolReplay); code != 0 {
		t.Fatalf("drain exit = %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	var report hook.SpoolDrainReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("drain report: %v\n%s", err, stdout.String())
	}
	if report.Replayed != len(inputs) || report.Rejected != 0 || report.LessonsSeen != 1 {
		t.Fatalf("drain report = %+v\nstderr: %s", report, stderr.String())
	}
	got := normalizedHookEvents(t, spoolHome)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("drained records differ from synchronous records\nsync:\n%s\n\ndrain:\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}

	// Drained records carry the time the agent made the call, not the drain time.
	spooled := spoolLines(t, spoolHome)
	eventsData, err := os.ReadFile(filepath.Join(spoolHome, "events", "hook-events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range spooled {
		at, err := time.Parse(time.RFC3339Nano, line.SpooledAt)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(eventsData), `"created_at":"`+at.UTC().Format(time.RFC3339)+`"`) {
			t.Fatalf("no drained event at spooled time %s:\n%s", at.UTC().Format(time.RFC3339), eventsData)
		}
	}

	// A second drain writes nothing more.
	stdout.Reset()
	if code := hook.RunDrainCommand([]string{"--once"}, &stdout, &stderr, newHookSpoolReplay); code != 0 {
		t.Fatalf("second drain exit = %d", code)
	}
	if again := normalizedHookEvents(t, spoolHome); len(again) != len(got) {
		t.Fatalf("second drain duplicated records: %d -> %d", len(got), len(again))
	}
}

// TestHookSpoolPathStaysUnderTheBudget fails if the spool path grows real work.
// Process start adds roughly 10 ms on top; the per-call target is under 50 ms.
func TestHookSpoolPathStaysUnderTheBudget(t *testing.T) {
	homePath := initSpoolTestHome(t)
	writeSpoolTestLessonIndex(t, homePath)
	parsed, err := parseHookArgs([]string{"pre-tool-use", "--live", "--harness", "claude_code", "--spool"})
	if err != nil {
		t.Fatal(err)
	}
	input := bashHookInput("sess_budget", "/repo", "asc screenshots upload --replace ./shots")
	const runs = 50
	samples := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		var stdout, stderr bytes.Buffer
		start := time.Now()
		runHookSpool(parsed, strings.NewReader(input), &stdout, &stderr, time.Now())
		samples = append(samples, time.Since(start))
		if !strings.Contains(stdout.String(), "lesson_replace_deletes_first") {
			t.Fatalf("run %d lost the lesson: %s %s", i, stdout.String(), stderr.String())
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	median := samples[runs/2]
	t.Logf("in-process spool path: median %s, p90 %s, max %s", median, samples[runs*9/10], samples[runs-1])
	if median > 25*time.Millisecond {
		t.Fatalf("spool path median %s; the whole hook budget is 50ms per call", median)
	}
}

func TestHookLessonsIndexBuildsFromJSONAndSkillTriggers(t *testing.T) {
	homePath := initSpoolTestHome(t)
	dir := t.TempDir()
	lessonsFile := filepath.Join(dir, "lessons.json")
	if err := os.WriteFile(lessonsFile, []byte(spoolTestLessons), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(dir, "skills", "example")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "# Example\n" + `trigger: {"id":"skill_port","tools":["Bash"],"pattern":"\\bmaestro\\s+test\\b","apply":"Use the wrapper; a bare run hangs on an occupied port."}` + "\n"
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := hook.RunLessonsIndexCommand([]string{"--from", lessonsFile, "--skills", filepath.Join(dir, "skills"), "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("lessons-index exit = %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(hook.LessonIndexPath(homePath))
	if err != nil {
		t.Fatal(err)
	}
	var index hook.LessonIndex
	if err := json.Unmarshal(data, &index); err != nil || len(index.Lessons) != 3 {
		t.Fatalf("index = %+v (%v)", index, err)
	}
	resp, _ := runSpool(t, "claude_code", bashHookInput("s", "/repo", "maestro test flows/"))
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "skill_port") {
		t.Fatalf("skill trigger did not reach the hook: %+v", resp.HookSpecificOutput)
	}

	// A bad lesson is reported with exit 1, and the valid subset is kept.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`[{"id":"bad","pattern":"(","apply":"x"},{"id":"ok","pattern":"y","apply":"z"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := hook.RunLessonsIndexCommand([]string{"--from", bad}, &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "rejected") {
		t.Fatalf("bad lessons exit = %d\n%s", code, stdout.String())
	}
	data, _ = os.ReadFile(hook.LessonIndexPath(homePath))
	if !strings.Contains(string(data), `"id": "ok"`) || strings.Contains(string(data), `"id": "bad"`) {
		t.Fatalf("index after partial build:\n%s", data)
	}
}

func writeSpoolHookConfig(t *testing.T, configPath, harnessName, binaryPath string) {
	t.Helper()
	command, timeout := hookplan.InstalledHookCommandForEvent(binaryPath, harnessName, "PreToolUse", false)
	if !strings.HasSuffix(command, " --spool") {
		t.Fatalf("installer no longer renders the spool route for %s: %q", harnessName, command)
	}
	config := `{"hooks":{"PreToolUse":[{"matcher":"*","timeout":` + strconv.Itoa(timeout) +
		`,"hooks":[{"type":"command","command":` + quoteJSON(command) + `}]}]}}`
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHookDoctorProbesTheInstalledSpoolRouteThroughDrain proves the doctor
// still checks the policy evidence for a spool install: the real binary
// answers, its drain writes the records, and the probe reads them.
func TestHookDoctorProbesTheInstalledSpoolRouteThroughDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary: skipped in -short mode")
	}
	real := buildRealCompound(t)
	dir := t.TempDir()
	var hooks []doctorHook
	for _, harnessName := range []string{"claude-code", "codex"} {
		configPath := filepath.Join(dir, harnessName, "settings.json")
		writeSpoolHookConfig(t, configPath, harnessName, real)
		hooks = append(hooks, doctorHook{Harness: harnessName, ConfigPath: configPath})
	}
	rows, failing := buildHookEnforcementRows(hooks)
	if failing != 0 || len(rows) != 2 {
		t.Fatalf("spool routes must probe green through drain, got failing=%d rows=%+v", failing, rows)
	}
	for _, row := range rows {
		if row.Posture != "observation_only" || row.Status != "observing" {
			t.Fatalf("spool row = %+v, want observation_only/observing", row)
		}
	}
}

// TestHookDoctorFailsASpoolRouteThatNeverRecords is the falsifier: a binary
// that answers the native shape and exits 0 for drain but writes nothing must
// not pass as an observing hook.
func TestHookDoctorFailsASpoolRouteThatNeverRecords(t *testing.T) {
	dir := t.TempDir()
	stub := writeExecutable(t, filepath.Join(dir, "compound"),
		"#!/bin/sh\nprintf '%s\\n' '{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\"}}'\nexit 0\n")
	configPath := filepath.Join(dir, "settings.json")
	writeSpoolHookConfig(t, configPath, "claude-code", stub)
	rows, failing := buildHookEnforcementRows([]doctorHook{{Harness: "claude-code", ConfigPath: configPath}})
	if failing != 1 || len(rows) != 1 || rows[0].Status != "fail_open" || !strings.Contains(rows[0].Detail, "persisted no") {
		t.Fatalf("a spool route without records must fail the probe, got failing=%d rows=%+v", failing, rows)
	}
	// The spool flag never stands in for enforcement.
	spoolArgs := []string{"hook", "pre-tool-use", "--live", "--harness", "claude_code", "--spool"}
	if detail := hook.ValidateDoctorProbeArgs(spoolArgs, true, "claude_code"); detail == "" {
		t.Fatal("a --spool route was accepted as an enforced posture")
	}
	if detail := hook.ValidateDoctorProbeArgs(spoolArgs, false, "claude_code"); detail != "" {
		t.Fatalf("watch-only spool route refused: %s", detail)
	}
}

func TestLessonCorrectionReachesRealSpoolHook(t *testing.T) {
	homePath := initSpoolTestHome(t)
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	skills := t.TempDir()
	skillDir := filepath.Join(skills, "example")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("# Example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(how string) promotedriver.LessonReceipt {
		t.Helper()
		var out, errors bytes.Buffer
		code := promotedriver.RunLessonCLI(store, []string{"--why", "synthetic correction", "--how", how, "--by", "agent:test",
			"--skill", "example", "--skills-dir", skills, "--repo", skills, "--slot", "tool:example::lesson.output", "--trigger", "Bash:example-command", "--json"}, &out, &errors)
		if code != 0 {
			t.Fatalf("write lesson: %d %s %s", code, &out, &errors)
		}
		var result promotedriver.LessonReceipt
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	build := func() []byte {
		t.Helper()
		var out, errors bytes.Buffer
		if code := hook.RunLessonsIndexCommand([]string{"--skills", skills, "--json"}, &out, &errors); code != 0 {
			t.Fatalf("build: %d %s %s", code, &out, &errors)
		}
		body, err := os.ReadFile(hook.LessonIndexPath(homePath))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	delivered := func() string {
		t.Helper()
		resp, code := runSpool(t, "claude_code", bashHookInput("correction", "/repo", "example-command"))
		if code != 0 {
			t.Fatalf("hook exit %d", code)
		}
		return resp.HookSpecificOutput.AdditionalContext
	}
	a := write("Use the original method.")
	build()
	if got := delivered(); !strings.Contains(got, a.Claim.Object) {
		t.Fatalf("initial lesson absent: %s", got)
	}
	b := write("Use the corrected method.")
	build()
	if got := delivered(); strings.Contains(got, a.Claim.Object) || strings.Contains(got, b.Claim.Object) || !strings.Contains(strings.ToLower(got), "conflict") {
		t.Fatalf("unresolved conflict delivered as instructions: %s", got)
	}
	if result, err := store.ResolveContestedLessonOnUse(b.Claim.ID, "agent:test", "work_example", time.Now()); err != nil || len(result.Superseded) != 1 {
		t.Fatalf("resolve: %+v %v", result, err)
	}
	// Stale or edited Markdown must not override the current memory body.
	historyBefore, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.ReplaceAll(string(historyBefore), b.Claim.Object, "stale Markdown instruction")
	stale = strings.ReplaceAll(stale, "Bash", "Read")
	stale = strings.ReplaceAll(stale, "example-command", "stale-command")
	if err := os.WriteFile(skillPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	index := build()
	if !strings.Contains(string(index), a.Claim.ID) {
		t.Fatal("excluded historical ID not recorded")
	}
	var revision struct {
		SourceMemoryRevision string `json:"source_memory_revision"`
		SourceLedgerSeq      int64  `json:"source_ledger_seq"`
	}
	if err := json.Unmarshal(index, &revision); err != nil || revision.SourceMemoryRevision == "" || revision.SourceLedgerSeq == 0 {
		t.Fatalf("index not revision-bound: %s (%v)", index, err)
	}
	got := delivered()
	if strings.Contains(got, a.Claim.ID) || !strings.Contains(got, b.Claim.Object) || strings.Contains(strings.ToLower(got), "conflict") {
		t.Fatalf("correction not delivered: %s", got)
	}
	history, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(history), a.Claim.ID) || !strings.Contains(string(history), b.Claim.ID) {
		t.Fatal("historical text was removed")
	}
	// The actual tool-call consumer must rely only on the compiled snapshot.
	if err := os.WriteFile(filepath.Join(homePath, "ledger", "claims.jsonl"), []byte("invalid ledger\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if current := delivered(); current != got {
		t.Fatalf("hook read memory after compilation: %s", current)
	}
	var failedOut, failedErr bytes.Buffer
	if code := hook.RunLessonsIndexCommand([]string{"--skills", skills, "--json"}, &failedOut, &failedErr); code != 1 {
		t.Fatalf("corrupt memory build must fail visibly: %d %s %s", code, &failedOut, &failedErr)
	}
	retained, err := os.ReadFile(hook.LessonIndexPath(homePath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained, index) {
		t.Fatal("failed build replaced the last complete index")
	}
}
