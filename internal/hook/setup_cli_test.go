package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/hookplan"
)

// setupMachine is a synthetic user: an empty Compound home location, a home folder that holds
// harness configs, an empty PATH plus a bin folder for fake harness commands, and a synthetic
// compound binary. Nothing here reads the real machine.
type setupMachine struct {
	compoundHome, userHome, bin, binary string
}

func newSetupMachine(t *testing.T) setupMachine {
	t.Helper()
	root := t.TempDir()
	m := setupMachine{
		compoundHome: filepath.Join(root, "compound-home"),
		userHome:     filepath.Join(root, "user"),
		bin:          filepath.Join(root, "bin"),
	}
	m.binary = filepath.Join(m.userHome, ".local", "bin", "compound")
	for _, dir := range []string{m.userHome, m.bin, filepath.Dir(m.binary)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(m.binary, []byte("synthetic compound binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_HOME", m.compoundHome)
	t.Setenv("HOME", m.userHome)
	t.Setenv("PATH", m.bin)
	t.Setenv("SHELL", "/bin/zsh")
	return m
}

func (m setupMachine) withClaude(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(m.userHome, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.userHome, ".claude", "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (m setupMachine) withCommand(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(m.bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (m setupMachine) run(t *testing.T, cmd func([]string, *bytes.Buffer, *bytes.Buffer) int, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cmd(append(args, "--binary", m.binary), &out, &errOut)
	return code, out.String(), errOut.String()
}

func initCmd(args []string, out, errOut *bytes.Buffer) int { return RunInitCommand(args, out, errOut) }

// snapshotTree records every file and its bytes under root, so a test can prove a command
// changed nothing at all.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(path)
			files[path] = string(data)
		}
		return nil
	})
	return files
}

func TestInitOnAMachineWithNoHarness(t *testing.T) {
	m := newSetupMachine(t)
	code, out, errOut := m.run(t, initCmd)
	if code != 0 {
		t.Fatalf("init exit %d: %s%s", code, out, errOut)
	}
	for _, want := range []string{"Compound home: " + m.compoundHome + " (created)", "Claude Code: not found", "Codex: not found", "No supported harness found"} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output missing %q:\n%s", want, out)
		}
	}
	if _, err := home.LoadManifest(m.compoundHome); err != nil {
		t.Fatalf("init must create the home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.userHome, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("init wired a harness that is not here: %v", err)
	}
}

func TestInitWiresClaudeCodeOnlyWhenOnlyClaudeCodeIsHere(t *testing.T) {
	m := newSetupMachine(t)
	m.withClaude(t)
	code, out, errOut := m.run(t, initCmd)
	if code != 0 {
		t.Fatalf("init exit %d: %s%s", code, out, errOut)
	}
	settings := filepath.Join(m.userHome, ".claude", "settings.json")
	if !strings.Contains(out, "Claude Code: wired ("+settings+")") || !strings.Contains(out, "Codex: not found") {
		t.Fatalf("init output:\n%s", out)
	}
	body, _ := os.ReadFile(settings)
	if !strings.Contains(string(body), "hook session-start --live --harness claude_code") || !strings.Contains(string(body), `"theme": "dark"`) {
		t.Fatalf("settings after init:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(m.userHome, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("init created a codex config without codex: %v", err)
	}
	manifest, err := contract.LoadInstalledHooksManifest(m.compoundHome)
	if err != nil || len(manifest.Hooks) == 0 {
		t.Fatalf("init must publish the installed-hook authority: %v %+v", err, manifest)
	}
	if !strings.Contains(out, "Next: start Claude Code") || !strings.Contains(out, "compound hook uninstall --harness claude-code") {
		t.Fatalf("init must say what to do next and how to undo:\n%s", out)
	}

	code, out, _ = m.run(t, initCmd)
	if code != 0 || !strings.Contains(out, "Compound home: "+m.compoundHome+" (ready)") || !strings.Contains(out, "Claude Code: already wired") {
		t.Fatalf("a second init must report what is already there (exit %d):\n%s", code, out)
	}
}

func TestInitWiresBothHarnessesAndHonoursHarnessFilter(t *testing.T) {
	m := newSetupMachine(t)
	m.withClaude(t)
	m.withCommand(t, "codex") // Codex installed but never opened: no ~/.codex yet
	code, out, errOut := m.run(t, initCmd, "--harness", "codex")
	if code != 0 || !strings.Contains(out, "Codex: wired") || strings.Contains(out, "Claude Code") {
		t.Fatalf("--harness codex must wire only Codex (exit %d):\n%s%s", code, out, errOut)
	}
	code, out, errOut = m.run(t, initCmd)
	if code != 0 || !strings.Contains(out, "Claude Code: wired") || !strings.Contains(out, "Codex: already wired") {
		t.Fatalf("init with both (exit %d):\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(m.userHome, ".codex", "hooks.json")); err != nil {
		t.Fatalf("codex hooks missing: %v", err)
	}
}

func TestInitCheckChangesNothing(t *testing.T) {
	m := newSetupMachine(t)
	m.withClaude(t)
	m.withCommand(t, "codex")
	before := snapshotTree(t, filepath.Dir(m.compoundHome))
	code, out, errOut := m.run(t, initCmd, "--check")
	if code != 1 {
		t.Fatalf("--check on an unset machine must exit 1, got %d:\n%s%s", code, out, errOut)
	}
	for _, want := range []string{"(missing; `compound init` creates it)", "Claude Code: not wired", "Codex: not wired", "Run `compound init` to finish."} {
		if !strings.Contains(out, want) {
			t.Fatalf("--check output missing %q:\n%s", want, out)
		}
	}
	if after := snapshotTree(t, filepath.Dir(m.compoundHome)); len(after) != len(before) {
		t.Fatalf("--check changed files: %d before, %d after", len(before), len(after))
	} else {
		for path, body := range before {
			if after[path] != body {
				t.Fatalf("--check changed %s", path)
			}
		}
	}
	if code, _, _ := m.run(t, initCmd); code != 0 {
		t.Fatal("init failed")
	}
	if code, out, _ := m.run(t, initCmd, "--check"); code != 0 || !strings.Contains(out, "Claude Code: wired") {
		t.Fatalf("--check after init must exit 0 and say wired (exit %d):\n%s", code, out)
	}
}

func TestSetupCommandsRefuseUnknownFlags(t *testing.T) {
	m := newSetupMachine(t)
	for name, cmd := range map[string]func([]string, *bytes.Buffer, *bytes.Buffer) int{
		"init":      initCmd,
		"install":   func(a []string, o, e *bytes.Buffer) int { return RunInstallCommand(a, o, e) },
		"uninstall": func(a []string, o, e *bytes.Buffer) int { return RunUninstallCommand(a, o, e) },
	} {
		if code, _, errOut := m.run(t, cmd, "--harnes", "codex"); code != 2 || !strings.Contains(errOut, "harnes") {
			t.Fatalf("%s accepted a typo'd flag (exit %d): %s", name, code, errOut)
		}
	}
	if _, err := os.Stat(m.compoundHome); !os.IsNotExist(err) {
		t.Fatalf("a refused command created the home: %v", err)
	}
}

// `--scope project` used to target the local settings file in the HOME folder, a file Claude Code never
// reads for the project, unless --base was also given. It now means the current project.
func TestHookInstallProjectScopeTargetsTheCurrentProject(t *testing.T) {
	m := newSetupMachine(t)
	project := filepath.Join(m.userHome, "work", "demo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	code, out, errOut := m.run(t, func(a []string, o, e *bytes.Buffer) int { return RunInstallCommand(a, o, e) }, "--harness", "claude-code", "--scope", "project", "--check")
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if !strings.Contains(out, filepath.Join(project, hookplan.ConfigRel("claude-code", "project"))) {
		t.Fatalf("--scope project must target the current project:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(m.userHome, ".claude")) {
		t.Fatalf("--scope project still targets the home folder:\n%s", out)
	}
}

func TestHookUninstallCommandLeavesUnrelatedKeys(t *testing.T) {
	m := newSetupMachine(t)
	m.withClaude(t)
	if code, out, errOut := m.run(t, initCmd); code != 0 {
		t.Fatalf("init: %s%s", out, errOut)
	}
	uninstall := func(a []string, o, e *bytes.Buffer) int { return RunUninstallCommand(a, o, e) }
	code, out, errOut := m.run(t, uninstall, "--harness", "claude-code")
	if code != 0 || !strings.Contains(out, "- claude-code: removed") {
		t.Fatalf("uninstall (exit %d):\n%s%s", code, out, errOut)
	}
	var settings map[string]any
	body, _ := os.ReadFile(filepath.Join(m.userHome, ".claude", "settings.json"))
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["theme"] != "dark" || len(settings) != 1 {
		t.Fatalf("uninstall must leave exactly the user's own settings, got %v", settings)
	}
	manifest, err := contract.LoadInstalledHooksManifest(m.compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range manifest.Hooks {
		if h.Harness == "claude_code" && h.Status != "removed" {
			t.Fatalf("authority still lists %+v as installed", h)
		}
	}
	if code, out, _ := m.run(t, initCmd, "--check"); code != 1 || !strings.Contains(out, "Claude Code: not wired") {
		t.Fatalf("after uninstall, init --check must say not wired (exit %d):\n%s", code, out)
	}
}

func TestPathAdviceNamesTheUsersShellAndProfile(t *testing.T) {
	userHome := t.TempDir()
	dir := filepath.Join(userHome, ".local", "bin")
	cases := []struct{ shell, goos, want string }{
		{"/bin/zsh", "darwin", "Add this line to ~/.zshrc, then open a new terminal:\n  export PATH=\"$HOME/.local/bin:$PATH\""},
		{"/bin/bash", "darwin", "~/.bash_profile"},
		{"/usr/bin/bash", "linux", "~/.bashrc"},
		{"/usr/bin/fish", "linux", "fish_add_path $HOME/.local/bin"},
		{"", "linux", "~/.profile"},
	}
	for _, c := range cases {
		got := pathAdvice(dir, "/usr/bin:/bin", c.shell, userHome, c.goos)
		if !strings.Contains(got, "is not on your PATH") || !strings.Contains(got, c.want) {
			t.Fatalf("shell %q: %q missing %q", c.shell, got, c.want)
		}
	}
	if got := pathAdvice(dir, dir+":/usr/bin", "/bin/zsh", userHome, "darwin"); !strings.Contains(got, "ok in this terminal") {
		t.Fatalf("on PATH but in no profile: %q", got)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".zshrc"), []byte("export PATH=\"$HOME/.local/bin:$PATH\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pathAdvice(dir, dir+":/usr/bin", "/bin/zsh", userHome, "darwin"); got != "" {
		t.Fatalf("on PATH and in ~/.zshrc: want silence, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(userHome, ".bashrc")); !os.IsNotExist(err) {
		t.Fatal("pathAdvice must never write a profile")
	}
}

func TestReceiptLastShowsTheNewestReceipt(t *testing.T) {
	m := newSetupMachine(t)
	receipt := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := RunReceiptCommand(args, &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, out := receipt("last"); code != 1 || !strings.Contains(out, "no receipt yet") {
		t.Fatalf("empty home (exit %d): %s", code, out)
	}
	if err := home.Init(m.compoundHome, home.HomeKindGlobal, time.Now()); err != nil {
		t.Fatal(err)
	}
	store, err := eventstore.Open(m.compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	for i, at := range []string{"2026-11-07T10:00:00Z", "2026-11-07T12:00:00Z", "2026-11-07T11:00:00Z"} {
		r := envelope.Receipt{Outcome: "completed", MoveType: "audit_only", ProofRefs: []string{"transcript:/synthetic/session-" + at + ".jsonl"}}
		r.SchemaVersion, r.ID, r.CreatedAt = "compound.receipt.v0.1", "rec_synthetic"+string(rune('a'+i)), at
		r.IDs.SessionID = "session-" + at
		r.StateDelta.FilesChanged = []string{"README.md"}
		if _, err := store.PutJSON(r.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	code, out := receipt("last")
	if code != 0 {
		t.Fatalf("receipt last exit %d: %s", code, out)
	}
	for _, want := range []string{"receipt rec_syntheticb  2026-11-07T12:00:00Z", "session: session-2026-11-07T12:00:00Z", "outcome: completed", "files changed: 1 (README.md)", "transcript: /synthetic/session-2026-11-07T12:00:00Z.jsonl"} {
		if !strings.Contains(out, want) {
			t.Fatalf("receipt last missing %q:\n%s", want, out)
		}
	}
	if code, out := receipt("last", "--bogus"); code != 2 {
		t.Fatalf("unknown flag must be refused (exit %d): %s", code, out)
	}
}
