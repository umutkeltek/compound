package lanes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func TestRefreshRegisteredProviderUsesOnlyItsRecordOnlyHome(t *testing.T) {
	hp, base := t.TempDir(), t.TempDir()
	if err := home.Init(hp, home.HomeKindGlobal, time.Now()); err != nil {
		t.Fatal(err)
	}
	opts := RefreshLaneOptions{HomePath: hp, BaseDir: base, BinaryPath: "/opt/example/bin/compound", Now: time.Now()}
	file := func(label string) string {
		return filepath.Join(base, ".switchback", "config", "claude", "_providers", label, "settings.json")
	}
	write := func(label, body string) {
		p := file(label)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(label string) string {
		b, err := os.ReadFile(file(label))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	write("kimi", "{}")
	write("deepseek-flash-api", "{}")
	if got := preflightLaneRefresh(opts, "claude_kimi"); got.Status != statusRefreshReady {
		t.Fatalf("registered and discovered provider preflight: %+v", got)
	}
	if read("kimi") != "{}" {
		t.Fatal("preflight modified provider profile")
	}
	if err := refreshLaneHookInstall(opts, "claude_kimi"); err != nil {
		t.Fatal(err)
	}
	if body := read("kimi"); !strings.Contains(body, "--spool") || !strings.Contains(body, "claude_kimi") || strings.Contains(body, "--enforce-governed") {
		t.Fatalf("record-only hooks = %s", body)
	}
	if read("deepseek-flash-api") != "{}" {
		t.Fatal("one lane refresh changed its neighbor")
	}
	write("kimi", "{broken")
	if got := preflightLaneRefresh(opts, "claude_kimi"); got.Status != StatusError {
		t.Fatalf("malformed profile passed: %+v", got)
	}
	if err := os.Remove(file("kimi")); err != nil {
		t.Fatal(err)
	}
	if got := preflightLaneRefresh(opts, "claude_kimi"); got.Status != StatusError {
		t.Fatalf("missing profile passed: %+v", got)
	}
}
