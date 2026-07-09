package hook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
)

func TestSessionEndWorklessTranscriptProducesContentReceipt(t *testing.T) {
	home := t.TempDir()
	path := writeTranscript(t, home, strings.Join([]string{
		`{"type":"user","timestamp":"2026-07-10T01:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Please restore SessionEnd receipt parity."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-10T01:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Loaded /workspace/skills/golang-testing/SKILL.md for this fix."},{"type":"tool_use","name":"Write","input":{"file_path":"internal/hook/session_transcript.go"}},{"type":"tool_use","name":"Edit","input":{"file_path":"internal/hook/hook.go"}},{"type":"tool_use","name":"MultiEdit","input":{"file_path":"internal/hook/consumption.go"}},{"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/hook"}}]}}`,
		`{"type":"assistant","timestamp":"2026-07-10T01:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Implemented the workless receipt path and verified it."}]}}`,
	}, "\n")+"\n")

	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{
		Hook:           "session-end",
		Harness:        "claude_code",
		SessionID:      "sess_content",
		Cwd:            "/repo/product",
		TranscriptPath: path,
	}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 1 || got[0] != "receipt" {
		t.Fatalf("produced = %v, want [receipt]", got)
	}
	rec := res.Produced[0].(*envelope.Receipt)
	if rec.IDs.WorkID != "" || rec.IDs.SessionID != "sess_content" {
		t.Fatalf("receipt ids = %+v, want session-only receipt", rec.IDs)
	}
	if !strings.Contains(rec.StateDelta.Summary, "Please restore SessionEnd receipt parity.") {
		t.Fatalf("summary missing first ask: %q", rec.StateDelta.Summary)
	}
	if !strings.Contains(rec.StateDelta.Summary, "Implemented the workless receipt path") {
		t.Fatalf("summary missing outcome hint: %q", rec.StateDelta.Summary)
	}
	if !strings.Contains(rec.StateDelta.Summary, "tool_calls: 4 (bash: 1)") {
		t.Fatalf("summary missing tool/bash counts: %q", rec.StateDelta.Summary)
	}
	assertStringSet(t, rec.StateDelta.FilesChanged, []string{
		"internal/hook/consumption.go",
		"internal/hook/hook.go",
		"internal/hook/session_transcript.go",
	})
	if !containsString(rec.Skills.Used, "capability://skill:golang-testing") {
		t.Fatalf("skills used = %v, want golang-testing", rec.Skills.Used)
	}
	if !containsString(res.Event.SideEffects, filepath.Join("events", rec.ID+".json")) {
		t.Fatalf("hook side effects = %v, want receipt event ref", res.Event.SideEffects)
	}
	if _, err := os.Stat(filepath.Join(home, "events", rec.ID+".json")); err != nil {
		t.Fatalf("receipt not persisted in event door: %v", err)
	}
}

func TestSessionEndWorklessLargeTranscriptCompletesWithinBound(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "large-transcript.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	if _, err := fmt.Fprintln(w, `{"type":"user","timestamp":"2026-07-10T01:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Summarize a large session."}]}}`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 42_000; i++ {
		if _, err := fmt.Fprintf(w, `{"type":"assistant","timestamp":"2026-07-10T01:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"padding line %05d %s"}]}}`+"\n", i, strings.Repeat("x", 120)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprintln(w, `{"type":"assistant","timestamp":"2026-07-10T01:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Large transcript completed."},{"type":"tool_use","name":"Edit","input":{"file_path":"internal/hook/hook.go"}}]}}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 10_000_000 {
		t.Fatalf("large transcript size = %d, want about 10MB", info.Size())
	}

	deps := baseDeps()
	deps.HomePath = home
	start := time.Now()
	res, err := Run(Fixture{
		Hook:           "session-end",
		Harness:        "claude_code",
		SessionID:      "sess_large",
		TranscriptPath: path,
	}, deps, fixedNow)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("large transcript parse took %s, want <= 5s", elapsed)
	}
	if got := kinds(res.Produced); len(got) != 1 || got[0] != "receipt" {
		t.Fatalf("produced = %v, want [receipt]", got)
	}
}

func TestSessionEndWorklessMalformedTranscriptFailsOpenBareEvent(t *testing.T) {
	home := t.TempDir()
	path := writeTranscript(t, home, "{not json}\n")
	deps := baseDeps()
	deps.HomePath = home

	res, err := Run(Fixture{
		Hook:           "session-end",
		Harness:        "claude_code",
		SessionID:      "sess_malformed",
		TranscriptPath: path,
	}, deps, fixedNow)
	if err != nil {
		t.Fatalf("malformed transcript must fail open, got error: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 0 {
		t.Fatalf("produced = %v, want bare hook event", got)
	}
	if res.Event.IDs.SessionID != "sess_malformed" || res.Event.IDs.WorkID != "" {
		t.Fatalf("event ids = %+v, want session-only bare event", res.Event.IDs)
	}
}

func TestSessionEndWorklessTranscriptDetectsConsumedClaim(t *testing.T) {
	home := t.TempDir()
	const claimRef = "claim://claim_workless01"
	path := writeTranscript(t, home, strings.Join([]string{
		`{"type":"system","timestamp":"2026-07-10T01:00:00Z","message":{"role":"system","content":"Injected memory ` + claimRef + ` for this session."}}`,
		`{"type":"user","timestamp":"2026-07-10T01:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Use the injected memory."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-10T01:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"I used claim_workless01 in the answer."}]}}`,
	}, "\n")+"\n")
	deps := baseDeps()
	deps.HomePath = home

	res, err := Run(Fixture{
		Hook:           "session-end",
		Harness:        "claude_code",
		SessionID:      "sess_claim",
		TranscriptPath: path,
	}, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 1 || got[0] != "receipt" {
		t.Fatalf("produced = %v, want [receipt]", got)
	}
	rec := res.Produced[0].(*envelope.Receipt)
	if !rec.Skills.UsedDeclared {
		t.Fatalf("workless detector ran => used_declared must be true, got %+v", rec.Skills)
	}
	if !containsString(rec.Skills.Used, claimRef) {
		t.Fatalf("consumed claim ref missing from receipt used: %v", rec.Skills.Used)
	}
	proj, err := capgraph.BuildCapabilityUsage(home, capgraph.UsageBuildOptions{GeneratedAt: fixedNow})
	if err != nil {
		t.Fatalf("build usage: %v", err)
	}
	if got := capabilityUsedCount(proj, claimRef); got != 1 {
		b, _ := json.MarshalIndent(proj, "", "  ")
		t.Fatalf("usage projection consumed claim count = %d, want 1\n%s", got, b)
	}
}

func capabilityUsedCount(proj capgraph.CapabilityUsageProjection, ref string) int {
	for _, item := range proj.Capabilities {
		if item.Ref == ref {
			return item.UsedCount
		}
	}
	return 0
}

func assertStringSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, value := range want {
		if !containsString(got, value) {
			t.Fatalf("got %v, want value %q", got, value)
		}
	}
}
