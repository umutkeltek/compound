package hookplan

import (
	"strings"
	"testing"
)

// TestInstallRendersSpoolOnlyForInteractiveWatchOnlyPreToolUse is the
// falsifier for the reinstall-reverts-to-the-slow-hook regression: the
// interactive Claude Code and Codex PreToolUse entries must render the
// zero-wait --spool command with the spool timeout, and every enforced lane
// must keep its synchronous --enforce gate with no --spool.
func TestInstallRendersSpoolOnlyForInteractiveWatchOnlyPreToolUse(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallHooks(base, "/usr/local/bin/compound", "global", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, harnessName := range []string{"claude-code", "codex"} {
		entry := hookInstallEntryForHarness(t, rep, harnessName)
		cfg := readJSON(t, entry.ConfigPath)
		group, ok := findCompoundHookGroup(cfg, "PreToolUse")
		if !ok {
			t.Fatalf("%s: no Compound PreToolUse entry rendered", harnessName)
		}
		command := firstCommand(group)
		if !strings.HasSuffix(command, " hook pre-tool-use --live --harness "+runtimeHarnessName(harnessName)+" --spool") {
			t.Fatalf("%s PreToolUse command = %q, want the watch-only --spool form", harnessName, command)
		}
		if strings.Contains(command, "--enforce") {
			t.Fatalf("%s interactive PreToolUse must not enforce: %q", harnessName, command)
		}
		if timeout, _ := group["timeout"].(float64); timeout != WatchOnlySpoolTimeoutSeconds {
			t.Fatalf("%s PreToolUse timeout = %v, want %d", harnessName, group["timeout"], WatchOnlySpoolTimeoutSeconds)
		}
		// Only PreToolUse moves to the spool: the other events keep their
		// synchronous commands (context injection needs the answer).
		for _, event := range []string{"SessionStart", "PostToolUse"} {
			if other, ok := findCompoundHookGroup(cfg, event); ok && strings.Contains(firstCommand(other), "--spool") {
				t.Fatalf("%s %s rendered --spool: %q", harnessName, event, firstCommand(other))
			}
		}
		// The install report carries the same command the config holds.
		for _, binding := range entry.Bindings {
			if binding.Event == "PreToolUse" && (binding.Command != command || binding.Blocking) {
				t.Fatalf("%s report binding = %+v, config command %q", harnessName, binding, command)
			}
		}
	}
	for _, harnessName := range []string{claudeZaiHarness, claudeMinimaxHarness} {
		entry := hookInstallEntryForHarness(t, rep, harnessName)
		group, ok := findCompoundHookGroup(readJSON(t, entry.ConfigPath), "PreToolUse")
		if !ok {
			t.Fatalf("%s: enforced lane lost its PreToolUse gate", harnessName)
		}
		command := firstCommand(group)
		if strings.Contains(command, "--spool") || !strings.HasSuffix(command, " --enforce") {
			t.Fatalf("%s enforced PreToolUse = %q, want the synchronous --enforce gate", harnessName, command)
		}
		if _, pinned := group["timeout"]; pinned {
			t.Fatalf("%s enforced gate got a spool timeout: %+v", harnessName, group)
		}
	}
}

func TestInstalledHookCommandForEventKeepsEnforcementSynchronous(t *testing.T) {
	cases := []struct {
		harness, event string
		blocking       bool
		wantSuffix     string
		wantTimeout    int
	}{
		{"claude-code", "PreToolUse", false, "--harness claude_code --spool", WatchOnlySpoolTimeoutSeconds},
		{"codex", "PreToolUse", false, "--harness codex --spool", WatchOnlySpoolTimeoutSeconds},
		{"claude-code", "PreToolUse", true, "--harness claude_code --enforce", 0},
		{"codex", "PreToolUse", true, "--harness codex --enforce", 0},
		{claudeZaiHarness, "PreToolUse", false, "--harness claude_zai", 0},
		{"claude-code", "PostToolUse", false, "post-tool-use --live --harness claude_code", 0},
	}
	for _, tc := range cases {
		command, timeout := InstalledHookCommandForEvent("/bin/compound", tc.harness, tc.event, tc.blocking)
		if !strings.HasSuffix(command, tc.wantSuffix) || timeout != tc.wantTimeout {
			t.Fatalf("%s/%s blocking=%t = %q timeout %d, want suffix %q timeout %d",
				tc.harness, tc.event, tc.blocking, command, timeout, tc.wantSuffix, tc.wantTimeout)
		}
	}
}
