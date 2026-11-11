package hookplan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first-party unattended lane must be a real lane, not a name: it installs
// a blocking PreToolUse into its own profile, and the operator's interactive
// Claude Code config keeps its watch-only binding through the same install.
func TestUnattendedLaneBlocksInItsOwnProfileAndLeavesInteractiveWatchOnly(t *testing.T) {
	const binary = "/opt/compound/bin/compound"
	base := t.TempDir()

	// An install of every managed harness, the form install.sh and the doctor use.
	if _, err := InstallHooks(base, binary, "global", nil, false); err != nil {
		t.Fatal(err)
	}

	lanePath, ok := LaneHookSettingsPath(base, "claude_unattended")
	if !ok {
		t.Fatal("claude_unattended has no lane profile")
	}
	interactivePath := filepath.Join(base, ConfigRel("claude-code", "global"))
	if lanePath == interactivePath || !strings.Contains(lanePath, filepath.Join("_providers", "unattended-lane")) {
		t.Fatalf("lane profile %q must be its own launcher profile, not the interactive config %q", lanePath, interactivePath)
	}
	if dashed, _ := LaneHookSettingsPath(base, claudeUnattendedHarness); dashed != lanePath {
		t.Fatalf("installer label and registry name disagree: %q vs %q", dashed, lanePath)
	}

	command := onDiskCompoundPreToolUse(t, lanePath)
	if want := HookCommandForEvent(binary, claudeUnattendedHarness, "PreToolUse") + " --enforce"; command != want {
		t.Fatalf("lane PreToolUse = %q, want blocking %q", command, want)
	}
	if !strings.Contains(command, "--harness claude_unattended") {
		t.Fatalf("lane hook must report as claude_unattended, got %q", command)
	}
	if got := InstalledHookPostures(lanePath); len(got) != 1 || got[0].Posture != HookPostureEnforced {
		t.Fatalf("lane posture = %+v, want enforced everywhere", got)
	}
	if live, ok := ReadLiveHookPosture(base, "claude_unattended"); !ok || live.Posture != "enforce" || live.ConfigPath != lanePath {
		t.Fatalf("live lane posture = %+v, %v; want enforce from %s", live, ok, lanePath)
	}

	interactive := onDiskCompoundPreToolUse(t, interactivePath)
	if !strings.HasSuffix(interactive, " --spool") || strings.Contains(interactive, "--enforce") {
		t.Fatalf("interactive Claude Code PreToolUse = %q, want watch-only --spool", interactive)
	}
	if live, ok := ReadLiveHookPosture(base, "claude_code"); !ok || live.Posture != "observe" {
		t.Fatalf("interactive live posture = %+v, %v; want observe", live, ok)
	}

	// Reinstalling the lane alone never touches the interactive config.
	before, err := os.ReadFile(interactivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lanePath); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHooks(base, binary, "global", []string{claudeUnattendedHarness}, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(interactivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("installing the unattended lane rewrote the interactive Claude Code config")
	}
	if got := onDiskCompoundPreToolUse(t, lanePath); got != command {
		t.Fatalf("lane reinstall PreToolUse = %q, want %q", got, command)
	}
}

// Installing only the lane on a machine must not create the interactive config.
func TestUnattendedLaneInstallDoesNotCreateInteractiveConfig(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallHooks(base, "/opt/compound/bin/compound", "global", []string{claudeUnattendedHarness}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Harness != claudeUnattendedHarness {
		t.Fatalf("report entries = %+v, want only %s", rep.Entries, claudeUnattendedHarness)
	}
	entry := rep.Entries[0]
	if entry.Command == "" || !strings.HasSuffix(entry.Command, " --enforce") {
		t.Fatalf("reported guard command = %q, want the blocking PreToolUse", entry.Command)
	}
	if _, err := os.Stat(filepath.Join(base, ConfigRel("claude-code", "global"))); !os.IsNotExist(err) {
		t.Fatalf("lane install created the interactive config, stat err=%v", err)
	}
	if !isClaudeFamilyInstallerLabel(claudeUnattendedHarness) || runtimeHarnessName(claudeUnattendedHarness) != "claude_unattended" {
		t.Fatal("lane SessionEnd must use the bounded Claude adapter under its registry name")
	}
}
