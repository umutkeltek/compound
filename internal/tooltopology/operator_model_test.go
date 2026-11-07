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

func TestServeOperatorModelOversizeTruncatesAtLastNewlineBeforeCap(t *testing.T) {
	// 50 lines of exactly 100 bytes (99 visible + newline) = 5000 bytes; the
	// trailing newline is trimmed, leaving 4999 bytes of content. The last
	// newline at or before byte 3072 ends line 30 (index 2999), so 30 whole
	// lines (2999 bytes) are kept and 4999-2999 = 2000 bytes are omitted.
	var b strings.Builder
	lines := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		line := "pref " + strings.Repeat(string(rune('a'+i%26)), 94)
		if len(line) != 99 {
			t.Fatalf("fixture line is %d bytes, want 99", len(line))
		}
		lines = append(lines, line)
		b.WriteString(line + "\n")
	}
	home := t.TempDir()
	writeOperatorModel(t, home, b.String())

	got := ServeOperatorModel(home)
	want := wantOperatorModelHeader + "\n" + strings.Join(lines[:30], "\n") + "\n" +
		"(truncated: 2000 bytes omitted; full text in config/operator-model.md)"
	if got != want {
		t.Fatalf("oversize operator model:\n got tail %q\nwant tail %q", tail(got, 200), tail(want, 200))
	}
	body := strings.TrimPrefix(got, wantOperatorModelHeader+"\n")
	kept := body[:strings.LastIndex(body, "\n")]
	if len(kept) > MaxOperatorModelBytes {
		t.Fatalf("kept content is %d bytes, cap %d", len(kept), MaxOperatorModelBytes)
	}
	if again := ServeOperatorModel(home); again != got {
		t.Fatal("oversize serving must be byte-deterministic")
	}
}

func TestServeOperatorModelTruncationEdges(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		kept      string
		omitted   string
		wantWhole bool
	}{
		{
			name:      "exactly at cap is whole",
			body:      strings.Repeat("x", MaxOperatorModelBytes) + "\n\n",
			kept:      strings.Repeat("x", MaxOperatorModelBytes),
			wantWhole: true,
		},
		{
			name:    "newline exactly at cap keeps the full cap",
			body:    strings.Repeat("a", MaxOperatorModelBytes) + "\ntail",
			kept:    strings.Repeat("a", MaxOperatorModelBytes),
			omitted: "5",
		},
		{
			name:    "one long line falls back to a byte cut",
			body:    strings.Repeat("b", 4000),
			kept:    strings.Repeat("b", MaxOperatorModelBytes),
			omitted: "928",
		},
		{
			name:    "byte cut never splits a rune",
			body:    "a" + strings.Repeat("é", 2000),
			kept:    "a" + strings.Repeat("é", 1535),
			omitted: "930",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeOperatorModel(t, home, tc.body)
			want := wantOperatorModelHeader + "\n" + tc.kept
			if !tc.wantWhole {
				want += "\n(truncated: " + tc.omitted + " bytes omitted; full text in config/operator-model.md)"
			}
			if got := ServeOperatorModel(home); got != want {
				t.Fatalf("got tail %q\nwant tail %q (len got %d want %d)", tail(got, 120), tail(want, 120), len(got), len(want))
			}
		})
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
