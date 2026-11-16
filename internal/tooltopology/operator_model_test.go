package tooltopology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const wantOperatorModelHeader = "## Operator model (private, from config/operator-model.md)"

func writeOperatorModel(t *testing.T, home, body string) {
	t.Helper()
	dmWriteFile(t, filepath.Join(home, "config", "operator-model.md"), body)
}

func TestServeOperatorModelAbsentIsSilent(t *testing.T) {
	if got := ServeOperatorModel(t.TempDir()); got != "" {
		t.Fatalf("absent operator model must serve nothing, got %q", got)
	}
	if got := ServeOperatorModel(""); got != "" {
		t.Fatalf("an unset home must serve nothing, got %q", got)
	}
	// A directory where the file should be is unreadable as a model: silent, never an error.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config", "operator-model.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ServeOperatorModel(home); got != "" {
		t.Fatalf("an unreadable operator model must serve nothing, got %q", got)
	}
	// A whitespace-only file carries no preference: silent.
	blank := t.TempDir()
	writeOperatorModel(t, blank, " \n\t\n")
	if got := ServeOperatorModel(blank); got != "" {
		t.Fatalf("a blank operator model must serve nothing, got %q", got)
	}
}

func TestServeOperatorModelSmallFileServedWholeUnderHeader(t *testing.T) {
	home := t.TempDir()
	writeOperatorModel(t, home, "- Lead with the decision.\n- Plain language, short sentences.  \n\n \n")
	want := wantOperatorModelHeader + "\n- Lead with the decision.\n- Plain language, short sentences."
	got := ServeOperatorModel(home)
	if got != want {
		t.Fatalf("small operator model:\n got %q\nwant %q", got, want)
	}
	if again := ServeOperatorModel(home); again != got {
		t.Fatalf("serving must be byte-deterministic:\n first %q\nsecond %q", got, again)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestServeSessionStartCarriesOperatorModelAfterDoorMap(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	without := ServeSessionStart(home, now, DefaultDoorMapTTL)
	if without != ServeDoorMap(home, now, DefaultDoorMapTTL) || without != "" {
		t.Fatalf("without a model or a door map the payload is empty, got %q", without)
	}
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(OperatorModelPath)), []byte("- lead with the decision\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	with := ServeSessionStart(home, now, DefaultDoorMapTTL)
	if !strings.HasPrefix(with, operatorModelHeader) {
		t.Fatalf("with no door map the model leads, no blank lines before it:\n%s", with)
	}
	if _, err := Refresh(home, Inputs{DoorsJSON: []byte(`[{"door":"web","status":"PASS","detail":"ok"}]`), DoorsObservedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	doorMap := ServeDoorMap(home, now, DefaultDoorMapTTL)
	if both := ServeSessionStart(home, now, DefaultDoorMapTTL); doorMap == "" || !strings.HasPrefix(both, doorMap+"\n\n"+operatorModelHeader) {
		t.Fatalf("model must follow the door map one blank line apart:\n%s", both)
	}
	if !strings.HasSuffix(with, "- lead with the decision") {
		t.Fatalf("model body missing:\n%s", with)
	}
}
