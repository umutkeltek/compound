package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
)

const briefNothingRecorded = "no sessions recorded for this checkout yet"

// briefFacts renders the brief for cwd and returns its fact section: the lines
// before the first blank one.
func briefFacts(t *testing.T, homePath, cwd string) []string {
	t.Helper()
	out := RenderOrientBrief(OrientBriefInput{HomePath: homePath, Input: Input{Cwd: cwd, RepoKind: "product"}, Pack: briefTestPack(), Now: briefTestNow})
	if len(out) > OrientBriefMaxBytes {
		t.Fatalf("brief is %d bytes, cap is %d", len(out), OrientBriefMaxBytes)
	}
	facts, _, _ := strings.Cut(out, "\n\n")
	return strings.Split(facts, "\n")
}

func briefDir(t *testing.T, elem ...string) string {
	t.Helper()
	dir := filepath.Join(elem...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// sessionReceiptSummary has the shape the session-end hook writes into a
// session receipt's summary.
func sessionReceiptSummary(toolCalls, bashCalls int, first, last, transcript string) string {
	return strings.Join([]string{
		"task_ref: sha256:example",
		"outcome_hint_ref: none",
		fmt.Sprintf("tool_calls: %d (bash: %d)", toolCalls, bashCalls),
		"window: " + first + " -> " + last,
		"transcript: " + transcript,
	}, "\n")
}

// putSessionReceipt records a session receipt the way the session-end hook
// does: the surface is the cwd the session ran in.
func putSessionReceipt(t *testing.T, homePath, id, createdAt, surface, summary string, files ...string) {
	t.Helper()
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	r := envelope.Receipt{Outcome: "completed", MoveType: "audit_only"}
	r.SchemaVersion, r.ID, r.CreatedAt = envelope.SVReceipt, id, createdAt
	r.IDs.SessionID = "session-" + id
	r.Scope.Surface = surface
	r.StateDelta.Summary = summary
	r.StateDelta.FilesChanged = files
	if _, err := store.PutJSON(r.ID, r, "  "); err != nil {
		t.Fatal(err)
	}
}

// A new home has no projection and no records: one line, and it says what is
// missing in terms a new user can act on. Reading must leave the home untouched.
func TestOrientBriefNativeFactsOnAnEmptyHome(t *testing.T) {
	homePath := t.TempDir()
	repo := briefDir(t, t.TempDir(), "demo")
	facts := briefFacts(t, homePath, repo)
	if want := []string{"demo (product repo): " + briefNothingRecorded}; !slices.Equal(facts, want) {
		t.Fatalf("facts\n got %q\nwant %q", facts, want)
	}
	entries, err := os.ReadDir(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rendering the brief must create nothing under the home, found %d entries", len(entries))
	}
}

// The last session in the checkout is one line built from its receipt. A
// session that ran in a subdirectory is still a session in this checkout.
func TestOrientBriefNativeFactsShowTheLastSession(t *testing.T) {
	repo := briefDir(t, t.TempDir(), "demo")
	sub := briefDir(t, repo, "internal", "example")
	const transcript = "/synthetic/transcripts/demo-session.jsonl"
	summary := sessionReceiptSummary(34, 12, "2026-03-04T08:02:11.250Z", "2026-03-04T09:14:59.900Z", transcript)
	want := []string{
		"demo (product repo)",
		"- Last session here: 2026-03-04 09:14 UTC, 34 tool calls, 3 files changed (a.go, b.md, c.md)",
		"  transcript: " + transcript,
	}
	for name, surface := range map[string]string{"checkout root": repo, "subdirectory": sub} {
		t.Run(name, func(t *testing.T) {
			homePath := t.TempDir()
			putSessionReceipt(t, homePath, "rec_examplesession", "2026-03-04T09:15:02Z", surface, summary, "/synthetic/demo/a.go", "/synthetic/demo/docs/b.md", "c.md")
			if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, want) {
				t.Fatalf("facts\n got %q\nwant %q", facts, want)
			}
		})
	}
}

// A part the receipt does not carry is left out of the line, never filled in.
func TestOrientBriefNativeFactsLeaveOutWhatTheReceiptLacks(t *testing.T) {
	const transcript = "/synthetic/transcripts/demo-session.jsonl"
	cases := []struct {
		name, createdAt, summary string
		files                    []string
		want                     string
	}{
		{
			name:      "no window, no tool calls, no files: creation time and transcript only",
			createdAt: "2026-03-04T09:15:02Z",
			summary:   "task_ref: none\nwindow: ? -> ?\ntranscript: " + transcript,
			want:      "- Last session here: 2026-03-04 09:15 UTC\n  transcript: " + transcript,
		},
		{
			name:      "more files than the line shows are counted",
			createdAt: "2026-03-04T09:15:02Z",
			summary:   sessionReceiptSummary(1, 0, "2026-03-04T09:00:00Z", "2026-03-04T09:10:00Z", transcript),
			files:     []string{"a.go", "b.go", "c.go", "d.go", "e.go"},
			want:      "- Last session here: 2026-03-04 09:10 UTC, 1 tool call, 5 files changed (a.go, b.go, c.go, +2 more)\n  transcript: " + transcript,
		},
		{
			name:      "one changed file, no transcript line",
			createdAt: "2026-03-04T09:15:02Z",
			summary:   "tool_calls: 0 (bash: 0)",
			files:     []string{"/synthetic/demo/a.go"},
			want:      "- Last session here: 2026-03-04 09:15 UTC, 0 tool calls, 1 file changed (a.go)",
		},
		{
			name:      "nothing readable: the receipt id is the only fact left",
			createdAt: "not a time",
			summary:   "",
			want:      "- Last session here: receipt rec_examplesession",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homePath := t.TempDir()
			repo := briefDir(t, t.TempDir(), "demo")
			putSessionReceipt(t, homePath, "rec_examplesession", tc.createdAt, repo, tc.summary, tc.files...)
			if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, append([]string{"demo (product repo)"}, strings.Split(tc.want, "\n")...)) {
				t.Fatalf("facts\n got %q\nwant line %q", facts, tc.want)
			}
		})
	}
}

// Of two sessions in the checkout the newer one is shown, whatever their ids.
func TestOrientBriefNativeFactsPickTheNewestSession(t *testing.T) {
	homePath := t.TempDir()
	repo := briefDir(t, t.TempDir(), "demo")
	older := sessionReceiptSummary(3, 1, "2026-03-03T10:00:00Z", "2026-03-03T10:30:00Z", "/synthetic/transcripts/older.jsonl")
	newer := sessionReceiptSummary(8, 2, "2026-03-04T07:00:00Z", "2026-03-04T07:45:00Z", "/synthetic/transcripts/newer.jsonl")
	putSessionReceipt(t, homePath, "rec_examplezz", "2026-03-03T10:30:05Z", repo, older)
	putSessionReceipt(t, homePath, "rec_exampleaa", "2026-03-04T07:45:05Z", repo, newer)
	want := []string{"demo (product repo)", "- Last session here: 2026-03-04 07:45 UTC, 8 tool calls", "  transcript: /synthetic/transcripts/newer.jsonl"}
	if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, want) {
		t.Fatalf("facts\n got %q\nwant %q", facts, want)
	}
}

// A session recorded for any other place must not appear: a sibling whose name
// starts with this checkout's name, the parent, a checkout nested inside this
// one, a relative surface, a path that is gone, or a receipt of no session.
func TestOrientBriefNativeFactsNeverShowAnotherCheckoutsSession(t *testing.T) {
	homePath := t.TempDir()
	parent := t.TempDir()
	repo := briefDir(t, parent, "app")
	sibling := briefDir(t, parent, "app-other")
	nested := briefDir(t, repo, "vendor", "clone")
	briefDir(t, nested, ".git")
	briefDir(t, repo, "docs")
	// A relative surface resolves against the process, so stand in the checkout.
	t.Chdir(repo)

	summary := func(name string) string {
		return sessionReceiptSummary(2, 1, "2026-03-04T08:00:00Z", "2026-03-04T08:30:00Z", "/synthetic/transcripts/"+name+".jsonl")
	}
	for id, surface := range map[string]string{
		"rec_examplesibling":  sibling,
		"rec_exampleparent":   parent,
		"rec_examplenested":   nested,
		"rec_examplenestedin": filepath.Join(nested, "src"),
		"rec_examplerelative": "docs",
		"rec_exampledot":      ".",
		"rec_examplegone":     filepath.Join(repo, "removed"),
	} {
		putSessionReceipt(t, homePath, id, "2026-03-04T08:30:05Z", surface, summary(id))
	}
	briefDir(t, nested, "src")
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	sessionless := envelope.Receipt{Outcome: "completed", MoveType: "audit_only"}
	sessionless.SchemaVersion, sessionless.ID, sessionless.CreatedAt = envelope.SVReceipt, "rec_examplenosession", "2026-03-04T08:30:05Z"
	sessionless.Scope.Surface = repo
	sessionless.StateDelta.Summary = summary("nosession")
	if _, err := store.PutJSON(sessionless.ID, sessionless, "  "); err != nil {
		t.Fatal(err)
	}

	if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, []string{"app (product repo): " + briefNothingRecorded}) {
		t.Fatalf("a session from another place leaked into the brief: %q", facts)
	}
	// The nested checkout owns its own session, and the sibling its own.
	for cwd, id := range map[string]string{nested: "rec_examplenestedin", sibling: "rec_examplesibling"} {
		facts := briefFacts(t, homePath, cwd)
		if len(facts) != 3 || facts[2] != "  transcript: /synthetic/transcripts/"+id+".jsonl" {
			t.Fatalf("%s must show its own session %s, got %q", cwd, id, facts)
		}
	}
	// An older session of this checkout is found among the newer foreign ones.
	putSessionReceipt(t, homePath, "rec_exampleown", "2026-03-04T06:00:00Z", repo, summary("own"))
	facts := briefFacts(t, homePath, repo)
	if len(facts) != 3 || facts[2] != "  transcript: /synthetic/transcripts/own.jsonl" {
		t.Fatalf("the checkout's own session must be shown, got %q", facts)
	}
}

// A projection entry for the checkout still decides the whole fact section:
// the session receipt is there, and it is not printed until the entry is gone.
func TestOrientBriefProjectionEntryWinsOverNativeFacts(t *testing.T) {
	homePath := t.TempDir()
	repo := briefDir(t, t.TempDir(), "demo")
	const transcript = "/synthetic/transcripts/demo-session.jsonl"
	putSessionReceipt(t, homePath, "rec_examplesession", "2026-03-04T09:15:02Z", repo, sessionReceiptSummary(4, 1, "2026-03-04T09:00:00Z", "2026-03-04T09:14:00Z", transcript))
	writeRepoBriefs(t, homePath, RepoBriefs{
		GeneratedAt: briefTestNow.Add(-2 * time.Hour).Format(time.RFC3339),
		Briefs:      []RepoBrief{{RepoRoot: repo, Title: "Example Lantern", Lines: []string{"State: LIVE 2.3.4 since 2026-02-01 (31 days)"}}},
	})
	covered := []string{
		"Example Lantern (product repo)",
		"- State: LIVE 2.3.4 since 2026-02-01 (31 days)",
		"(facts from state/repo-briefs.json as of 2026-03-04 10:00 UTC)",
	}
	if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, covered) {
		t.Fatalf("a covered checkout must render the projection only\n got %q\nwant %q", facts, covered)
	}

	if err := os.Remove(filepath.Join(homePath, filepath.FromSlash(RepoBriefsRelPath))); err != nil {
		t.Fatal(err)
	}
	uncovered := []string{
		"demo (product repo)",
		"- Last session here: 2026-03-04 09:14 UTC, 4 tool calls",
		"  transcript: " + transcript,
	}
	if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, uncovered) {
		t.Fatalf("without the entry the same receipt must be printed\n got %q\nwant %q", facts, uncovered)
	}
}

// Oversized session records cannot push the brief past its caps. The fact
// section is at most two sessions of two lines each, however many sessions
// changed files; a file list too long for a line is clipped, and a transcript
// path is whole or replaced, never cut.
func TestOrientBriefNativeFactsStayInsideTheCaps(t *testing.T) {
	homePath := t.TempDir()
	repo := briefDir(t, t.TempDir(), "demo")
	huge := strings.Repeat("ü", 3000)
	transcript := func(name string, length int) string {
		return "/synthetic/transcripts/" + name + "-" + strings.Repeat("d", length) + ".jsonl"
	}
	var files []string
	for i := 0; i < 400; i++ {
		files = append(files, fmt.Sprintf("/synthetic/demo/%s-%03d.go", strings.Repeat("f", 120), i))
	}
	// Oldest to newest: two sessions that changed hundreds of files, then one
	// that changed none and whose transcript path does not fit a line.
	putSessionReceipt(t, homePath, "rec_exampleolder", "2026-03-04T07:30:05Z", repo, sessionReceiptSummary(640, 200, "2026-03-04T06:00:00Z", "2026-03-04T07:30:00Z", transcript("older", 240)), files...)
	putSessionReceipt(t, homePath, "rec_examplework", "2026-03-04T09:14:05Z", repo, sessionReceiptSummary(912, 300, "2026-03-04T08:00:00Z", "2026-03-04T09:14:00Z", transcript("work", 240)), files...)
	putSessionReceipt(t, homePath, "rec_exampleask", "2026-03-04T10:01:05Z", repo, sessionReceiptSummary(3, 1, "2026-03-04T10:00:00Z", "2026-03-04T10:01:00Z", transcript("ask", orientBriefLineMaxBytes)))

	pack := briefTestPack()
	for i := range pack.ActivePositions {
		pack.ActivePositions[i].Entity = "product:example-lantern"
		pack.ActivePositions[i].Stance = huge
	}
	for i := range pack.CapabilityOffers {
		pack.CapabilityOffers[i].Name = huge
	}
	out := RenderOrientBrief(OrientBriefInput{HomePath: homePath, Input: Input{Cwd: repo, RepoKind: "product"}, Pack: pack, Now: briefTestNow})
	if len(out) > OrientBriefMaxBytes {
		t.Fatalf("brief is %d bytes, cap is %d", len(out), OrientBriefMaxBytes)
	}
	section, _, _ := strings.Cut(out, "\n\n")
	facts := strings.Split(section, "\n")
	if len(facts) != 5 || len(facts)-1 > orientBriefFactLineLimit {
		t.Fatalf("want the title and two sessions of two lines each, got %d lines:\n%s", len(facts), section)
	}
	for _, line := range facts {
		if len(line) > orientBriefLineMaxBytes+len("- ") {
			t.Fatalf("line of %d bytes passes the per-line clip: %.80q", len(line), line)
		}
	}
	if facts[1] != "- Last session here: 2026-03-04 10:01 UTC, 3 tool calls" || facts[2] != "  transcript: path too long for the brief; compound event show rec_exampleask" {
		t.Fatalf("the newest session must be named, its unfitting path replaced: %q / %q", facts[1], facts[2])
	}
	if !strings.HasPrefix(facts[3], "- Last session that changed files: 2026-03-04 09:14 UTC, 912 tool calls, 400 files changed (fff") || !strings.HasSuffix(facts[3], "fff...") || len(facts[3]) != orientBriefLineMaxBytes+len("- ") {
		t.Fatalf("the file list must be clipped at the line limit (%d bytes): %q", len(facts[3]), facts[3])
	}
	if facts[4] != "  transcript: "+transcript("work", 240) {
		t.Fatalf("a path that fits must stand whole on its own line: %q", facts[4])
	}
	if strings.Contains(section, "older") {
		t.Fatalf("only the newest session that changed files is named:\n%s", section)
	}
}

// A store that cannot be read is reported on its own line: breakage must not
// read as "nothing recorded".
func TestOrientBriefNativeFactsReportAnUnreadableStore(t *testing.T) {
	homePath := t.TempDir()
	repo := briefDir(t, t.TempDir(), "demo")
	if err := os.WriteFile(filepath.Join(homePath, "events"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	facts := briefFacts(t, homePath, repo)
	if len(facts) != 2 || facts[0] != "demo (product repo)" || !strings.HasPrefix(facts[1], "- Session receipts unavailable: ") {
		t.Fatalf("an unreadable store must be named, got %q", facts)
	}
}

// A real transcript path is long: the harness encodes the whole checkout path
// into it. It must reach the agent whole, and a path longer than a brief line
// is replaced by the command that prints the receipt, never cut.
func TestOrientBriefNativeFactsNeverCutTheTranscriptPath(t *testing.T) {
	repo := briefDir(t, t.TempDir(), "demo")
	for name, tc := range map[string]struct {
		pathLen int
		whole   bool
	}{
		"a long real-world path stays whole": {pathLen: 240, whole: true},
		"a path past the line limit":         {pathLen: orientBriefLineMaxBytes + 40, whole: false},
	} {
		t.Run(name, func(t *testing.T) {
			homePath := t.TempDir()
			transcript := "/synthetic/" + strings.Repeat("p", tc.pathLen-len("/synthetic/.jsonl")) + ".jsonl"
			summary := sessionReceiptSummary(17, 4, "2026-03-04T09:00:00Z", "2026-03-04T09:14:00Z", transcript)
			putSessionReceipt(t, homePath, "rec_examplesession", "2026-03-04T09:15:02Z", repo, summary,
				"/synthetic/demo/a-rather-long-file-name.md", "/synthetic/demo/another-rather-long-file-name.md", "/synthetic/demo/third.md", "/synthetic/demo/fourth.go")
			facts := briefFacts(t, homePath, repo)
			if len(facts) != 3 {
				t.Fatalf("want title, session line and transcript line, got %q", facts)
			}
			want := "  transcript: " + transcript
			if !tc.whole {
				want = "  transcript: path too long for the brief; compound event show rec_examplesession"
			}
			if facts[2] != want {
				t.Fatalf("transcript line\n got %q\nwant %q", facts[2], want)
			}
			if strings.Contains(facts[1], "transcript") || strings.HasSuffix(facts[2], "...") {
				t.Fatalf("the path must not share the session line or end in an ellipsis: %q", facts)
			}
		})
	}
}

// A session that only asked a question changes no files. It must not hide the
// session that did the work: the brief names both.
func TestOrientBriefNativeFactsKeepTheLastSessionThatChangedFiles(t *testing.T) {
	repo := briefDir(t, t.TempDir(), "demo")
	homePath := t.TempDir()
	work := sessionReceiptSummary(17, 4, "2026-03-04T08:00:00Z", "2026-03-04T08:40:00Z", "/synthetic/transcripts/work.jsonl")
	ask := sessionReceiptSummary(3, 2, "2026-03-04T09:00:00Z", "2026-03-04T09:01:00Z", "/synthetic/transcripts/ask.jsonl")
	putSessionReceipt(t, homePath, "rec_examplework", "2026-03-04T08:40:05Z", repo, work, "/synthetic/demo/settings.go")
	putSessionReceipt(t, homePath, "rec_exampleask", "2026-03-04T09:01:05Z", repo, ask)
	want := []string{
		"demo (product repo)",
		"- Last session here: 2026-03-04 09:01 UTC, 3 tool calls",
		"  transcript: /synthetic/transcripts/ask.jsonl",
		"- Last session that changed files: 2026-03-04 08:40 UTC, 17 tool calls, 1 file changed (settings.go)",
		"  transcript: /synthetic/transcripts/work.jsonl",
	}
	if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, want) {
		t.Fatalf("facts\n got %q\nwant %q", facts, want)
	}

	// When the newest session is the one that changed files, it is named once.
	later := sessionReceiptSummary(5, 1, "2026-03-04T10:00:00Z", "2026-03-04T10:20:00Z", "/synthetic/transcripts/later.jsonl")
	putSessionReceipt(t, homePath, "rec_examplelater", "2026-03-04T10:20:05Z", repo, later, "/synthetic/demo/README.md")
	want = []string{
		"demo (product repo)",
		"- Last session here: 2026-03-04 10:20 UTC, 5 tool calls, 1 file changed (README.md)",
		"  transcript: /synthetic/transcripts/later.jsonl",
	}
	if facts := briefFacts(t, homePath, repo); !slices.Equal(facts, want) {
		t.Fatalf("facts\n got %q\nwant %q", facts, want)
	}
}
