package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

var t0 = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func TestBuildAndJoinTransitionProjectsRoutingNotTaskSuccess(t *testing.T) {
	ev, err := Build(BuildInput{
		CreatedAt:     t0,
		RequestID:     "gateway:req_123",
		WorkID:        "work_123",
		ClientProfile: "gateway.ingress",
		Protocol:      "openai-chat-completions",
		Provider:      "fixture-provider",
		Model:         "fixture-model",
		Usage:         Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		LatencyMS:     23,
		Status:        "completed",
		EvidenceRefs:  []string{"gateway:trace_7", "gateway:trace_7"},
	})
	if err != nil {
		t.Fatalf("build evidence: %v", err)
	}
	joined, tr, err := JoinTransition(ev)
	if err != nil {
		t.Fatalf("join transition: %v", err)
	}
	if joined.TransitionID != tr.ID {
		t.Fatalf("transition_id = %q, want %q", joined.TransitionID, tr.ID)
	}
	if tr.IDs.WorkID != "work_123" || tr.SourceTraceID != "gateway:req_123" {
		t.Fatalf("transition ids = work:%q source:%q", tr.IDs.WorkID, tr.SourceTraceID)
	}
	if len(tr.Projections.Routing) != 1 || tr.Projections.Routing[0] != ev.ID {
		t.Fatalf("routing projection = %+v, want %s", tr.Projections.Routing, ev.ID)
	}
	if len(tr.Projections.Cost) != 1 || tr.Projections.Cost[0] != ev.ID {
		t.Fatalf("cost projection = %+v, want %s", tr.Projections.Cost, ev.ID)
	}
	if tr.SecondOrder.FutureBehaviorChanged {
		t.Fatal("route evidence alone must not claim future behavior changed")
	}
	if !strings.Contains(tr.StateDelta, "not task success") {
		t.Fatalf("state_delta = %q, want explicit not-task-success caveat", tr.StateDelta)
	}
}

func TestImportFileRecordsEvidenceAndTransition(t *testing.T) {
	home := t.TempDir()
	ev := fixtureEvidence()
	path := writeEvidenceFile(t, t.TempDir(), ev)
	result, err := ImportFile(home, path)
	if err != nil {
		t.Fatalf("import route evidence: %v", err)
	}
	if result.Evidence.TransitionID != result.Transition.ID {
		t.Fatalf("transition_id = %q, want %q", result.Evidence.TransitionID, result.Transition.ID)
	}
	rowData := readFile(t, filepath.Join(home, "ledger", "execution-route-evidence.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(rowData)), "\n")
	if len(lines) != 1 {
		t.Fatalf("ledger rows = %d, want 1", len(lines))
	}
	var row Evidence
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("decode evidence row: %v", err)
	}
	if row.ID != ev.ID || row.TransitionID != result.Transition.ID {
		t.Fatalf("row = %+v", row)
	}
	var tr envelope.Transition
	data := readFile(t, filepath.Join(home, "events", result.Transition.ID+".json"))
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatalf("decode transition: %v", err)
	}
	if tr.Projections.Routing[0] != ev.ID || tr.SecondOrder.FutureBehaviorChanged {
		t.Fatalf("transition = %+v", tr)
	}
}

func TestDecodeRejectsUnknownPromptOrResponseFields(t *testing.T) {
	raw := `{
		"schema_version":"compound.execution_route_evidence.v0.1",
		"id":"route_secret",
		"created_at":"2026-06-29T12:00:00Z",
		"request_id":"gateway:req_secret",
		"client_profile":"gateway.ingress",
		"protocol":"openai-chat-completions",
		"selected_target":{"provider":"fixture-provider","model":"fixture-model","protocol":"openai-chat-completions"},
		"status":"completed",
		"prompt_body":"SECRET_PROMPT_BODY"
	}`
	_, err := Decode([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v, want unknown field rejection", err)
	}
}

func TestJoinTransitionRejectsMismatchedTransitionID(t *testing.T) {
	ev := fixtureEvidence()
	ev.TransitionID = "tr_wrong"
	_, _, err := JoinTransition(ev)
	if err == nil || !strings.Contains(err.Error(), "does not match computed") {
		t.Fatalf("err = %v, want transition mismatch", err)
	}
}

func fixtureEvidence() Evidence {
	ev, err := Build(BuildInput{
		CreatedAt:     t0,
		RequestID:     "gateway:req_fixture",
		WorkID:        "work_fixture",
		ClientProfile: "gateway.ingress",
		Protocol:      "openai-chat-completions",
		Provider:      "fixture-provider",
		Model:         "fixture-model",
		Usage:         Usage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: 13},
		LatencyMS:     31,
		Status:        "completed",
		EvidenceRefs:  []string{"gateway:trace_fixture"},
	})
	if err != nil {
		panic(err)
	}
	return ev
}

func writeEvidenceFile(t *testing.T, dir string, ev Evidence) string {
	t.Helper()
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "route.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
