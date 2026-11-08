package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hookplan"
)

// A harness that loads a whole hook file (OpenCode, oh-my-pi) shows up in the doctor's hook
// section when its config folder exists: absent until init wires it, ok after, and the hint
// names `compound init`. Detection is by folder only, so a temp base never sees the host PATH.
func TestDoctorListsWholeFileHarnessesByTheirFolder(t *testing.T) {
	t.Setenv("COMPOUND_PLACEMENT_GUARD_MODE", "")
	t.Setenv("PLACEMENT_GUARD_MODE", "")
	home := t.TempDir()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	report := func() doctorReport {
		var out, errOut bytes.Buffer
		runDoctor([]string{"--home", home, "--base", base, "--binary", "/usr/local/bin/compound", "--json"}, &out, &errOut)
		var rep doctorReport
		if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
			t.Fatalf("parse doctor json: %v (%q)", err, out.String())
		}
		return rep
	}
	rep := report()
	if len(rep.Hooks) != 0 {
		t.Fatalf("no settings harness here, got %+v", rep.Hooks)
	}
	plugin := filepath.Join(base, ".config", "opencode", "plugins", "compound.ts")
	if len(rep.HookFiles) != 1 || rep.HookFiles[0].Harness != "opencode" || rep.HookFiles[0].Status != "absent" || rep.HookFiles[0].ConfigPath != plugin {
		t.Fatalf("OpenCode folder without plugin must be one absent row, got %+v", rep.HookFiles)
	}
	if !strings.Contains(rep.HookHint, "compound init") {
		t.Fatalf("unwired harness must point at init, got %q", rep.HookHint)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "harness opencode absent") {
		t.Fatalf("an unwired installed harness must be a doctor warning, got %q", rep.Warnings)
	}
	if _, err := hookplan.InstallOpenCodePlugin(base, false); err != nil {
		t.Fatal(err)
	}
	rep = report()
	if len(rep.HookFiles) != 1 || rep.HookFiles[0].Status != "ok" || rep.HookHint != "" {
		t.Fatalf("after init the row is ok and the hint is gone, got %+v hint=%q", rep.HookFiles, rep.HookHint)
	}
	var text bytes.Buffer
	printDoctor(&text, rep)
	if !strings.Contains(text.String(), "- opencode     ok        "+plugin) {
		t.Fatalf("text doctor must print the plugin row:\n%s", text.String())
	}
}
