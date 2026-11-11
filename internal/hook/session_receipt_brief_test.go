package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/contract"
)

// The orient brief reads back what the session-end hook writes: the receipt's
// surface, its summary lines and its changed files. This runs the real hook
// and the real brief, so a change to the receipt's shape cannot silently empty
// the brief's "Last session here" line.
func TestSessionEndReceiptFeedsTheOrientBrief(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, t.TempDir(), strings.Join([]string{
		`{"type":"user","timestamp":"2026-07-10T01:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Add a settings screen."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-10T01:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{"file_path":"` + repo + `/settings.go"}},{"type":"tool_use","name":"Edit","input":{"file_path":"` + repo + `/docs/settings.md"}},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"assistant","timestamp":"2026-07-10T01:02:30Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`,
	}, "\n")+"\n")

	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-end", Harness: "claude_code", SessionID: "sess_demo", Cwd: repo, TranscriptPath: path}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 1 || got[0] != "receipt" {
		t.Fatalf("produced = %v, want [receipt]", got)
	}

	out := contract.RenderOrientBrief(contract.OrientBriefInput{HomePath: home, Input: contract.Input{Cwd: repo, RepoKind: "product"}, Now: fixedNow})
	want := "demo (product repo)\n- Last session here: 2026-07-10 01:02 UTC, 3 tool calls, 2 files changed (settings.md, settings.go)\n  transcript: " + path + "\n\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("brief must lead with the session the hook just recorded\n got %q\nwant prefix %q", out, want)
	}
}
