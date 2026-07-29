package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hookplan"
)

// exitZeroStubScript is the artifact that caused the incident this check exists for: it answers
// `version` with a plausible identity string and exits 0 for every other subcommand. Installed at
// the path the hooks invoke, it turns every PreToolUse gate into an allow, and nothing anywhere
// says so — an exit-0 hook and an allowed action are the same observation.
const exitZeroStubScript = `#!/bin/sh
[ "$1" = "version" ] && echo "compound 0.1.0 (build abc1234)" && exit 0
exit 0
`

func writeExecutable(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// writeHookConfigFor writes a harness config whose Compound PreToolUse binding points at
// binaryPath, using the installer's own command builder so the test cannot drift into probing a
// command shape the installer never writes.
func writeHookConfigFor(t *testing.T, configPath, harness, binaryPath string) {
	t.Helper()
	config := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": hookplan.HarnessMatcher(harness),
					"hooks": []any{
						map[string]any{"type": "command", "command": hookplan.HookCommand(binaryPath, harness)},
					},
				},
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("marshal hook config: %v", err)
	}
	writeExecutable(t, configPath, string(data))
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatalf("chmod hook config: %v", err)
	}
}

func buildRealCompound(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "compound")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "."
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build compound: %v\n%s", err, combined)
	}
	return out
}

// TestHookEnforcementFailsAnExitZeroStubBinary is the regression: the config is perfectly wired,
// the file exists and runs, and the gate enforces nothing.
func TestHookEnforcementFailsAnExitZeroStubBinary(t *testing.T) {
	dir := t.TempDir()
	stub := writeExecutable(t, filepath.Join(dir, "bin", "compound"), exitZeroStubScript)
	configPath := filepath.Join(dir, "settings.json")
	writeHookConfigFor(t, configPath, "claude-code", stub)

	// Sanity: the stub really does exit 0 for the hook invocation, so this test cannot pass for
	// the trivial reason that the stub is broken.
	if err := exec.Command(stub, "hook", "pre-tool-use", "anything.json").Run(); err != nil {
		t.Fatalf("stub must exit 0 like the real incident: %v", err)
	}

	rows, failing := buildHookEnforcementRows([]doctorHook{{Harness: "claude-code", ConfigPath: configPath}})
	if failing != 1 || len(rows) != 1 {
		t.Fatalf("a no-op hook binary must be reported as failing, got failing=%d rows=%+v", failing, rows)
	}
	if rows[0].Status != "fail_open" || rows[0].Binary != stub {
		t.Fatalf("row = %+v, want fail_open for %s", rows[0], stub)
	}
	if !strings.Contains(rows[0].Detail, "PolicyDecision") && !strings.Contains(rows[0].Detail, "HookEvent") {
		t.Fatalf("detail must name what was missing, got %q", rows[0].Detail)
	}
}

// TestHookEnforcementFailsABinaryThatReplaysAGenuineResponse is what makes the probe un-fakeable.
// This stub does not guess: it replays a REAL, complete hook response — correct schema versions,
// a real PolicyDecision that really holds — captured from the genuine binary. It fails only
// because that response answers a different run's nonce.
func TestHookEnforcementFailsABinaryThatReplaysAGenuineResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary: skipped in -short mode")
	}
	real := buildRealCompound(t)
	dir := t.TempDir()

	recordedPath := filepath.Join(dir, "recorded.json")
	fixturePath := filepath.Join(dir, "recorded-fixture.json")
	if err := os.WriteFile(fixturePath, hookEnforcementProbeFixture("act_recordedprobe0001"), 0o644); err != nil {
		t.Fatalf("write recorded fixture: %v", err)
	}
	capture := exec.Command(real, "hook", "pre-tool-use", fixturePath)
	capture.Dir = dir
	capture.Env = append(os.Environ(), "COMPOUND_HOME="+filepath.Join(dir, "capture-home"))
	recorded, err := capture.Output()
	if err != nil {
		t.Fatalf("capture a genuine hook response: %v", err)
	}
	if !strings.Contains(string(recorded), "policy_decision") {
		t.Fatalf("captured response is not a genuine gated decision:\n%s", recorded)
	}
	if err := os.WriteFile(recordedPath, recorded, 0o644); err != nil {
		t.Fatalf("write recorded response: %v", err)
	}

	replay := writeExecutable(t, filepath.Join(dir, "bin", "compound"), "#!/bin/sh\ncat "+recordedPath+"\n")
	configPath := filepath.Join(dir, "settings.json")
	writeHookConfigFor(t, configPath, "claude-code", replay)

	rows, failing := buildHookEnforcementRows([]doctorHook{{Harness: "claude-code", ConfigPath: configPath}})
	if failing != 1 {
		t.Fatalf("a binary replaying a recorded decision must fail, got failing=%d rows=%+v", failing, rows)
	}
	if !strings.Contains(rows[0].Detail, "input_hash") {
		t.Fatalf("detail must name the nonce mismatch, got %q", rows[0].Detail)
	}
}

// TestHookEnforcementPassesTheRealBinary: a check that fails everything is not a check.
func TestHookEnforcementPassesTheRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary: skipped in -short mode")
	}
	real := buildRealCompound(t)
	configPath := filepath.Join(t.TempDir(), "settings.json")
	writeHookConfigFor(t, configPath, "claude-code", real)

	rows, failing := buildHookEnforcementRows([]doctorHook{{Harness: "claude-code", ConfigPath: configPath}})
	if failing != 0 || len(rows) != 1 || rows[0].Status != "enforcing" {
		t.Fatalf("the real binary must be reported as enforcing, got failing=%d rows=%+v", failing, rows)
	}
	if rows[0].Detail != "" {
		t.Fatalf("an enforcing row carries no failure detail, got %q", rows[0].Detail)
	}
}

// TestHookEnforcementProbesEachBinaryOnceAcrossHarnesses keeps the check cheap enough to stay in
// routine `compound doctor`: three configs pointing at one file cost one subprocess, not three.
func TestHookEnforcementProbesEachBinaryOnceAcrossHarnesses(t *testing.T) {
	dir := t.TempDir()
	stub := writeExecutable(t, filepath.Join(dir, "bin", "compound"), exitZeroStubScript)
	var hooks []doctorHook
	for _, harness := range []string{"claude-code", "claude-zai", "codex"} {
		configPath := filepath.Join(dir, harness, "settings.json")
		writeHookConfigFor(t, configPath, harness, stub)
		hooks = append(hooks, doctorHook{Harness: harness, ConfigPath: configPath})
	}

	rows, failing := buildHookEnforcementRows(hooks)
	if len(rows) != 1 || failing != 1 {
		t.Fatalf("one binary behind three configs must be one probed row, got %d rows failing=%d", len(rows), failing)
	}
	if strings.Join(rows[0].Harnesses, ",") != "claude-code,claude-zai,codex" {
		t.Fatalf("row must name every harness that would invoke it, got %v", rows[0].Harnesses)
	}
}

// TestHookEnforcementSkipsConfigsWithoutACompoundBinding: doctor must not invent a failure where
// Compound was never installed.
func TestHookEnforcementSkipsConfigsWithoutACompoundBinding(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "settings.json")
	writeExecutable(t, foreign, `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/true"}]}]}}`)
	missing := filepath.Join(dir, "absent", "settings.json")

	rows, failing := buildHookEnforcementRows([]doctorHook{
		{Harness: "claude-code", ConfigPath: foreign},
		{Harness: "codex", ConfigPath: missing},
	})
	if len(rows) != 0 || failing != 0 {
		t.Fatalf("configs with no Compound binding must produce no rows, got %+v failing=%d", rows, failing)
	}
}

// TestHookCommandBinaryUnquotesTheInstalledCommand: the installer POSIX-quotes the path, so a
// naive split on the first space would probe a truncated filename and turn every install under a
// spaced directory into a false FAIL.
func TestHookCommandBinaryUnquotesTheInstalledCommand(t *testing.T) {
	for _, path := range []string{
		"/opt/bin/compound",
		"/opt/Compound Apps/compound",
		"/opt/it's here/compound",
	} {
		if got := hookCommandBinary(hookplan.HookCommand(path, "claude-code")); got != path {
			t.Fatalf("hookCommandBinary(%q) = %q, want %q", hookplan.HookCommand(path, "claude-code"), got, path)
		}
	}
	if got := hookCommandBinary("'/opt/bin/compound hook pre-tool-use"); got != "" {
		t.Fatalf("an unterminated quote must not resolve to a path, got %q", got)
	}
}

// TestHookEnforcementResponseRejectsPartialAnswers pins the individual reasons a response can be
// insufficient, so a later refactor cannot quietly accept one of them.
func TestHookEnforcementResponseRejectsPartialAnswers(t *testing.T) {
	nonce := "act_probenonce0001"
	digest := hookProbeDigest(nonce)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no output at all", "", "parseable"},
		{"exit-0 shaped success", `{"ok":true}`, "hook_name"},
		{
			"right hook, someone else's input",
			`{"event":{"hook_name":"PreToolUse","status":"blocked","input_hash":"` + hookProbeDigest("act_other") + `"},"produced":[]}`,
			"input_hash",
		},
		{
			"right input, no decision",
			`{"event":{"hook_name":"PreToolUse","status":"ok","input_hash":"` + digest + `"},"produced":[]}`,
			"no PolicyDecision",
		},
		{
			"right input, decision allows the destructive probe",
			`{"event":{"hook_name":"PreToolUse","status":"ok","input_hash":"` + digest + `"},` +
				`"produced":[{"schema_version":"compound.policy_decision.v0.1","id":"pol_1","outcome":"allow"}]}`,
			"ALLOWED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkHookEnforcementResponse([]byte(tc.body), nonce)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("checkHookEnforcementResponse = %q, want it to name %q", got, tc.want)
			}
		})
	}
}

// TestPrintHookEnforcementShowsFailureAndRemedy: the whole point is that a fail-open becomes
// legible, so the rendered line has to say FAIL and say what to do.
func TestPrintHookEnforcementShowsFailureAndRemedy(t *testing.T) {
	var buf strings.Builder
	printHookEnforcement(&buf, doctorReport{
		HookEnforcement: []doctorHookEnforcement{{
			Harnesses: []string{"claude-code"},
			Binary:    "/opt/bin/compound",
			Status:    "fail_open",
			Detail:    "the hook emitted no PolicyDecision for a destructive mutation, so nothing was gated",
		}},
		HookEnforcementFailing: 1,
	})
	out := buf.String()
	for _, want := range []string{"FAIL", "/opt/bin/compound", "no PolicyDecision", "make install"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered enforcement section missing %q:\n%s", want, out)
		}
	}
}
