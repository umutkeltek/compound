package hookplan

import (
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/harness"
)

func TestPreToolHookStateNamesTheInstalledBinding(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, command, want string }{
		{"none", "", "absent"},
		{"not-live", "'/usr/local/bin/compound' hook pre-tool-use --harness x", "absent"},
		{"spool", "'/usr/local/bin/compound' hook pre-tool-use --live --harness x --spool", "record-only"},
		{"governed", "'/usr/local/bin/compound' hook pre-tool-use --live --harness x --enforce-governed", "blocking in governed runs"},
		{"enforce", "'/usr/local/bin/compound' hook pre-tool-use --live --harness x --enforce", "blocking"},
	} {
		path := filepath.Join(dir, tc.name, "settings.json")
		body := map[string]any{}
		if tc.command != "" {
			body["hooks"] = map[string]any{"PreToolUse": []any{map[string]any{"matcher": "*", "hooks": []any{
				map[string]any{"type": "command", "command": tc.command}}}}}
		}
		writeJSONFile(t, path, body)
		if got := PreToolHookState(path); got != tc.want {
			t.Errorf("%s: PreToolHookState = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := PreToolHookState(filepath.Join(dir, "missing", "settings.json")); got != "absent" {
		t.Errorf("a missing settings file = %q, want absent", got)
	}
}

// `compound harness check` and `compound hook install --check` must tell the
// same story about every lane home the record-only set hooks, before and after
// the install: the same hook state, and the plan's own install command.
func TestHarnessCheckAgreesWithHookInstallCheckOnLaneHomes(t *testing.T) {
	t.Setenv("LANES_JSON", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	base := providerLaneFixture(t, true)
	reg := harness.DefaultRegistry()
	plan := WiringHookPlan()
	fix := "compound hook install --harness " + ProviderLanesHarness

	agree := func(stage string) map[string]map[string]harness.WiringRow {
		homes, err := plan.ClaudeHomes(base, reg)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := harness.CheckWiring(harness.WiringCheck{Registry: reg, UserHome: base, Homes: homes, Only: "claude_code", PreToolState: plan.PreToolState})
		if err != nil {
			t.Fatal(err)
		}
		byHome := map[string]map[string]harness.WiringRow{}
		for _, r := range rows {
			if byHome[r.Home] == nil {
				byHome[r.Home] = map[string]harness.WiringRow{}
			}
			byHome[r.Home][r.Fact] = r
		}
		report, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Entries) == 0 {
			t.Fatalf("%s: the hook plan found no lane homes", stage)
		}
		for _, entry := range report.Entries {
			home := filepath.Base(filepath.Dir(entry.ConfigPath))
			ctx, perm := byHome[home][harness.FactContext], byHome[home][harness.FactPermissions]
			if state := PreToolHookState(entry.ConfigPath); !strings.HasSuffix(perm.Found, "pre-tool hook "+state) {
				t.Errorf("%s: %s plan reads pre-tool %q, check says %q", stage, home, state, perm.Found)
			}
			switch {
			case entry.Action == "ok" && ctx.Status != harness.WiringPass:
				t.Errorf("%s: %s plan says ok, check says %s: %s", stage, home, ctx.Status, ctx.Found)
			case entry.Action == "absent" && ctx.Status == harness.WiringPass:
				t.Errorf("%s: %s plan says absent, check passes it", stage, home)
			case ctx.Status == harness.WiringFail && ctx.Fix != fix:
				t.Errorf("%s: %s check fix %q, want the plan's %q", stage, home, ctx.Fix, fix)
			case ctx.Status == harness.WiringInfo:
				t.Errorf("%s: %s is in the hook plan but the check calls it unenrolled: %s", stage, home, ctx.Found)
			}
		}
		return byHome
	}

	before := agree("before install")
	if r := before["kimi"][harness.FactContext]; r.Status != harness.WiringFail || r.Fix != fix {
		t.Fatalf("an unhooked record-only lane must fail with the plan's install command: %+v", r)
	}
	if _, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	after := agree("after install")
	if r := after["kimi"][harness.FactPermissions]; !strings.HasSuffix(r.Found, "pre-tool hook record-only") {
		t.Fatalf("a hooked record-only lane reads record-only: %+v", r)
	}
	if r := after["zai-lane"][harness.FactPermissions]; !strings.HasSuffix(r.Found, "pre-tool hook blocking in governed runs") {
		t.Fatalf("the governed lane keeps its own blocking hook: %+v", r)
	}
}
