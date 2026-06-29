package eventsource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestImportOSEventsWritesTraceEnvelopesIdempotently(t *testing.T) {
	home := t.TempDir()
	fixture := filepath.Join("..", "..", "fixtures", "events", "os-events.ndjson")
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	report, err := ImportOSEvents(home, fixture, ImportOptions{Stream: "control-events"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.Imported != 2 || report.Skipped != 0 || len(report.TraceIDs) != 2 {
		t.Fatalf("report = %+v", report)
	}
	for _, id := range report.TraceIDs {
		var tr envelope.TraceEnvelope
		readJSON(t, filepath.Join(home, "events", id+".json"), &tr)
		if err := tr.Validate(); err != nil {
			t.Fatalf("trace %s invalid: %v", id, err)
		}
	}
	if lines := readLines(t, filepath.Join(home, "events", "traces.ndjson")); len(lines) != 2 {
		t.Fatalf("trace stream lines = %d, want 2", len(lines))
	}

	replay, err := ImportOSEvents(home, fixture, ImportOptions{Stream: "control-events"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Imported != 0 || replay.Skipped != 2 {
		t.Fatalf("replay report = %+v", replay)
	}
	if lines := readLines(t, filepath.Join(home, "events", "traces.ndjson")); len(lines) != 2 {
		t.Fatalf("replay should not append duplicate traces, lines = %d", len(lines))
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("event import must not promote memory claims, stat err=%v", err)
	}
}

func TestBuildTraceEnvelopeNormalizesOSMutationAndEvidence(t *testing.T) {
	raw := `{"schema_version":1,"id":"evt_x","ts":"2026-06-29T01:00:00Z","kind":"packet.generated","event_class":"planning","metadata_quality":"partial","mutation_class":"local_artifact_write","producer":{"caller":"planner","agent":"worker","run_id":"run_x"},"subject":{"path":"/workspace/out.md","project":"control","service":"packetizer"},"evidence":[{"kind":"path","redacted":false,"value":"/workspace/out.md"},{"kind":"secret","redacted":true,"value":"hidden"}],"summary":"Generated packet."}`
	var ev osEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	tr, err := BuildTraceEnvelope(ev, "events.ndjson", 7, ImportOptions{Stream: "events"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if tr.MutationClass != "local_reversible" || tr.Source.MutationRaw != "local_artifact_write" {
		t.Fatalf("mutation = %q raw=%q", tr.MutationClass, tr.Source.MutationRaw)
	}
	if tr.Risk != "tier1" || tr.Uncertainty.Level != "inferred" {
		t.Fatalf("risk/uncertainty = %s/%s", tr.Risk, tr.Uncertainty.Level)
	}
	if !contains(tr.EvidenceRefs, "secret:<redacted>") {
		t.Fatalf("redacted evidence refs = %v", tr.EvidenceRefs)
	}
	if !contains(tr.FilesTouched, "/workspace/out.md") {
		t.Fatalf("files touched = %v", tr.FilesTouched)
	}
	if !contains(tr.ToolsModelsUsed, "service:packetizer") || !contains(tr.ToolsModelsUsed, "agent:worker") {
		t.Fatalf("tools/models = %v", tr.ToolsModelsUsed)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
