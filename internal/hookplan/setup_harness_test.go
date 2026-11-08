package hookplan

import (
	"os"
	"path/filepath"
	"testing"
)

func syntheticBinary(t *testing.T, base string) string {
	t.Helper()
	binary := filepath.Join(base, "bin", "compound")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("synthetic compound binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary
}

func entriesByHarness(rep HookInstallReport) map[string]HookInstallEntry {
	out := map[string]HookInstallEntry{}
	for _, e := range rep.Entries {
		out[e.Harness] = e
	}
	return out
}

// One init call covers both kinds of harness: settings entries for Claude Code and Codex, a
// whole owned file for OpenCode and oh-my-pi. Names not asked for are not touched.
func TestInstallSetupHarnessesWiresSettingsAndFiles(t *testing.T) {
	base := t.TempDir()
	binary := syntheticBinary(t, base)
	names := []string{"claude-code", "opencode", "oh-my-pi"}
	rep, err := InstallSetupHarnesses(base, binary, names, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := entriesByHarness(rep)
	wantPath := map[string]string{
		"claude-code": filepath.Join(base, ".claude", "settings.json"),
		"opencode":    filepath.Join(base, ".config", "opencode", "plugins", "compound.ts"),
		"oh-my-pi":    filepath.Join(base, ".omp", "agent", "hooks", "pre", "compound.ts"),
	}
	if len(got) != len(wantPath) {
		t.Fatalf("entries = %+v, want one per requested harness", rep.Entries)
	}
	for name, path := range wantPath {
		e := got[name]
		if e.Action != "installed" || e.ConfigPath != path {
			t.Fatalf("%s entry = %+v, want installed at %s", name, e, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s file missing after install: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("codex was not requested but got a config: %v", err)
	}
	check, err := InstallSetupHarnesses(base, binary, names, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range check.Entries {
		if e.Action != "ok" {
			t.Fatalf("check after install: %s is %s, want ok", e.Harness, e.Action)
		}
	}

	removed, err := UninstallHooks(base, binary, "global", nil, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	gone := entriesByHarness(removed)
	for _, name := range names {
		if gone[name].Action != "removed" {
			t.Fatalf("uninstall all: %s is %s, want removed", name, gone[name].Action)
		}
	}
	for _, name := range []string{"opencode", "oh-my-pi"} {
		if _, err := os.Stat(wantPath[name]); !os.IsNotExist(err) {
			t.Fatalf("%s hook file still there after uninstall: %v", name, err)
		}
	}
	again, err := UninstallHooks(base, binary, "global", []string{"opencode"}, "", false, nil)
	if err != nil || len(again.Entries) != 1 || again.Entries[0].Action != "absent" {
		t.Fatalf("uninstall of an absent plugin = %+v %v, want one absent entry", again.Entries, err)
	}
	would, err := InstallSetupHarnesses(base, binary, []string{"oh-my-pi"}, false, nil)
	if err != nil || would.Entries[0].Action != "installed" {
		t.Fatalf("reinstall after uninstall = %+v %v", would.Entries, err)
	}
	if dry, _ := UninstallHooks(base, binary, "global", []string{"oh-my-pi"}, "", true, nil); dry.Entries[0].Action != "would remove" {
		t.Fatalf("check-mode uninstall = %+v, want would remove", dry.Entries)
	}
	if _, err := os.Stat(wantPath["oh-my-pi"]); err != nil {
		t.Fatal("check-mode uninstall removed the file")
	}
}

func TestOwnedFileHarnessRowsListOnlyPresentHarnesses(t *testing.T) {
	base := t.TempDir()
	rows, err := OwnedFileHarnessRows(base, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty home must list nothing: %+v %v", rows, err)
	}
	if err := os.MkdirAll(filepath.Join(base, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	rows, err = OwnedFileHarnessRows(base, nil)
	if err != nil || len(rows) != 1 || rows[0].Harness != "opencode" || rows[0].Action != "absent" {
		t.Fatalf("OpenCode folder without plugin = %+v %v, want one absent row", rows, err)
	}
	if _, err := InstallOpenCodePlugin(base, false); err != nil {
		t.Fatal(err)
	}
	if rows, _ = OwnedFileHarnessRows(base, nil); rows[0].Action != "ok" {
		t.Fatalf("after install = %+v, want ok", rows)
	}
	shim := filepath.Join(base, OmpShimRelPath)
	if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("// someone else's hook\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err = OwnedFileHarnessRows(base, nil)
	if err != nil || len(rows) != 2 || rows[1].Harness != "oh-my-pi" || rows[1].Action != "foreign" {
		t.Fatalf("foreign shim must be reported, not fail the doctor: %+v %v", rows, err)
	}
	// A command on PATH counts as present when the caller passes a lookPath.
	empty := t.TempDir()
	onPath := func(name string) (string, error) {
		if name == "omp" {
			return "/synthetic/bin/omp", nil
		}
		return "", os.ErrNotExist
	}
	rows, err = OwnedFileHarnessRows(empty, onPath)
	if err != nil || len(rows) != 1 || rows[0].Harness != "oh-my-pi" || rows[0].Action != "absent" {
		t.Fatalf("omp on PATH = %+v %v, want one absent row", rows, err)
	}
}

func TestSetupHarnessNamesAreTheRoster(t *testing.T) {
	got := SetupHarnessNames()
	want := []string{"claude-code", "codex", "opencode", "oh-my-pi"}
	if len(got) != len(want) {
		t.Fatalf("roster = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roster = %v, want %v", got, want)
		}
		if SetupHarnessLabel(want[i]) == "" {
			t.Fatalf("%s has no label", want[i])
		}
	}
}
