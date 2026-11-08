package hookplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The adapter folder is where the plugin is developed and tested with bun; the embedded copy
// is what a release binary installs. They must be the same bytes, or `compound init` ships a
// plugin nobody tested.
func TestEmbeddedOpenCodePluginMatchesAdapterSource(t *testing.T) {
	adapter, err := os.ReadFile(filepath.Join("..", "..", "adapters", "opencode", "compound.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(adapter) != openCodePluginSource {
		t.Fatal("internal/hookplan/opencode_plugin.ts drifted from adapters/opencode/compound.ts; copy the adapter file over the embedded one")
	}
	if !strings.HasPrefix(openCodePluginSource, OpenCodePluginMarker+"\n") {
		t.Fatal("the plugin must start with the ownership marker line")
	}
	for _, want := range []string{`"session-start"`, `"user-prompt-submit"`, `"post-tool-use"`, `"--harness", "opencode"`, `"session.created"`, `"chat.message"`, `"tool.execute.after"`} {
		if !strings.Contains(openCodePluginSource, want) {
			t.Fatalf("plugin no longer carries %s", want)
		}
	}
}

func TestInstallOpenCodePluginWritesOwnedFileAndIsIdempotent(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallOpenCodePlugin(base, false)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, ".config", "opencode", "plugins", "compound.ts")
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "installed" || rep.Entries[0].ConfigPath != target || rep.Entries[0].Harness != "opencode" {
		t.Fatalf("first install entries = %+v", rep.Entries)
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != openCodePluginSource {
		t.Fatalf("installed plugin is not the embedded source: %v", err)
	}
	events := map[string]bool{}
	for _, b := range rep.Entries[0].Bindings {
		events[b.Event] = true
		if b.Blocking {
			t.Fatalf("OpenCode cannot block; binding %+v claims it", b)
		}
	}
	if !events["SessionStart"] || !events["UserPromptSubmit"] || !events["PostToolUse"] || events["SessionEnd"] || len(events) != 3 {
		t.Fatalf("bindings must be exactly session start, prompt and post-tool: %v", events)
	}
	if rep, _ = InstallOpenCodePlugin(base, false); rep.Entries[0].Action != "ok" {
		t.Fatalf("second install action = %s, want ok", rep.Entries[0].Action)
	}
	if rep, _ = InstallOpenCodePlugin(base, true); rep.Entries[0].Action != "ok" {
		t.Fatalf("check after install action = %s, want ok", rep.Entries[0].Action)
	}
}

// `bun adapters/opencode/install.ts` wrote the plugin without a marker before `compound init`
// learned OpenCode. That exact copy is Compound's and is refreshed in place; nothing else is.
func TestInstallOpenCodePluginAdoptsTheLegacyUnmarkedCopy(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, OpenCodePluginRelPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := strings.TrimPrefix(openCodePluginSource, OpenCodePluginMarker+"\n")
	if err := os.WriteFile(target, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, err := InstallOpenCodePlugin(base, true); err != nil || rep.Entries[0].Action != "drift" {
		t.Fatalf("check on the legacy copy = %+v %v, want drift", rep.Entries, err)
	}
	rep, err := InstallOpenCodePlugin(base, false)
	if err != nil || rep.Entries[0].Action != "updated" {
		t.Fatalf("install over the legacy copy = %+v %v, want updated", rep.Entries, err)
	}
	if body, _ := os.ReadFile(target); string(body) != openCodePluginSource {
		t.Fatal("legacy copy was not refreshed to the marked source")
	}
}

func TestInstallOpenCodePluginRefusesAForeignFile(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, OpenCodePluginRelPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "export default async () => ({})\n"
	if err := os.WriteFile(target, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{true, false} {
		if _, err := InstallOpenCodePlugin(base, check); err == nil || !strings.Contains(err.Error(), "not compound-owned") {
			t.Fatalf("check=%v: expected a refusal, got %v", check, err)
		}
	}
	if body, _ := os.ReadFile(target); string(body) != foreign {
		t.Fatal("the foreign plugin was changed")
	}
	entry, err := openCodePluginFile().uninstall(target, false)
	if err != nil || entry.Action != "absent" {
		t.Fatalf("uninstall must leave a foreign file alone: %+v %v", entry, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("uninstall removed a file Compound does not own")
	}
}

func TestInstallOpenCodePluginCheckWritesNothing(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallOpenCodePlugin(base, true)
	if err != nil || rep.Entries[0].Action != "absent" {
		t.Fatalf("check on an empty home = %+v %v, want absent", rep.Entries, err)
	}
	if _, err := os.Stat(filepath.Join(base, ".config")); !os.IsNotExist(err) {
		t.Fatalf("check created files: %v", err)
	}
}
