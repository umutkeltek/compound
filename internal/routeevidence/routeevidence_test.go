package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
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

func TestProviderLedgerRowToEvidenceDropsPrivateSourcePayload(t *testing.T) {
	raw := `{"request_id":"req_provider_1","ts_start":"2026-06-29T10:00:00Z","caller":"codex","project":"product-x","purpose":"classification","route_alias":"cheap.classify","provider":"fixture-provider","model":"fixture-model","transport":"chat_completions","input_tokens":100,"output_tokens":20,"reasoning_tokens":5,"cache_read_tokens":10,"estimated_cost_usd":0.0025,"latency_ms":42,"status":"ok","source":{"private_path":"/Users/example/private/state.db","raw_prompt":"must not copy"}}`
	ev, err := ProviderLedgerRowToEvidence(raw, 1)
	if err != nil {
		t.Fatalf("provider ledger row: %v", err)
	}
	if ev.SelectedTarget.Provider != "fixture-provider" || ev.SelectedTarget.Model != "fixture-model" || ev.Protocol != "chat_completions" {
		t.Fatalf("target = %+v protocol=%q", ev.SelectedTarget, ev.Protocol)
	}
	if ev.Usage.PromptTokens != 110 || ev.Usage.CompletionTokens != 25 || ev.Usage.TotalTokens != 135 {
		t.Fatalf("usage = %+v", ev.Usage)
	}
	if ev.CostMicros != 2500 || ev.LatencyMS != 42 {
		t.Fatalf("cost/latency = %d/%d", ev.CostMicros, ev.LatencyMS)
	}
	joinedRefs := strings.Join(ev.EvidenceRefs, "\n")
	if strings.Contains(joinedRefs, "/Users/example") || strings.Contains(joinedRefs, "raw_prompt") {
		t.Fatalf("private source payload leaked into evidence refs: %s", joinedRefs)
	}
	if !strings.Contains(joinedRefs, "provider-ledger-request:req_provider_1") || !strings.Contains(joinedRefs, "provider-ledger-row:") {
		t.Fatalf("missing provider ledger refs: %s", joinedRefs)
	}
}

func TestImportProviderLedgerFileTailRecordsRouteEvidenceOnly(t *testing.T) {
	home := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "provider-calls.ndjson")
	rows := strings.Join([]string{
		`{"request_id":"req_old","ts_start":"2026-06-29T09:00:00Z","caller":"codex","project":"product-x","purpose":"old","route_alias":"cheap.classify","provider":"fixture-provider","model":"fixture-small","transport":"chat_completions","input_tokens":1,"output_tokens":2,"status":"ok"}`,
		`{"request_id":"req_new","run_id":"run_new","ts_start":"2026-06-29T10:00:00Z","caller":"codex","project":"product-x","purpose":"new","route_alias":"premium.reasoning","provider":"fixture-provider","model":"fixture-large","transport":"responses","input_tokens":100,"output_tokens":50,"estimated_cost_usd":0.01,"latency_ms":55,"status":"ok"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(ledgerPath, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	summary, err := ImportProviderLedgerFile(home, ledgerPath, ProviderLedgerImportOptions{Tail: 1})
	if err != nil {
		t.Fatalf("import provider ledger: %v", err)
	}
	if summary.RowsRead != 2 || summary.Imported != 1 || summary.FirstID == "" || summary.LastID != summary.FirstID {
		t.Fatalf("summary = %+v", summary)
	}
	rowData := readFile(t, filepath.Join(home, "ledger", "execution-route-evidence.jsonl"))
	if strings.Count(string(rowData), "\n") != 1 || !strings.Contains(string(rowData), "req_new") || strings.Contains(string(rowData), "req_old") {
		t.Fatalf("bad route evidence ledger:\n%s", rowData)
	}
	if _, err := os.Stat(filepath.Join(home, "events", summary.Results[0].Transition.ID+".json")); err != nil {
		t.Fatalf("transition not recorded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("provider-ledger import must not promote claims, stat err=%v", err)
	}
}

func TestDoctorProviderLedgerCostsRecordsRouteObservationNoPromote(t *testing.T) {
	home := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "provider-calls.ndjson")
	rows := strings.Join([]string{
		`{"request_id":"req_cost_ok","ts_start":"2026-06-29T09:00:00Z","caller":"codex","project":"product-x","purpose":"classify","route_alias":"cheap.classify","provider":"fixture-provider","model":"fixture-small","transport":"chat_completions","input_tokens":10,"output_tokens":2,"estimated_cost_usd":0.0001,"metadata_quality":"complete","status":"ok"}`,
		`{"request_id":"req_cost_gap","ts_start":"2026-06-29T10:00:00Z","caller":"codex","project":"unknown_project","purpose":"deep-review","route_alias":"premium.reasoning","provider":"fixture-provider","model":"fixture-large","transport":"responses","input_tokens":100,"output_tokens":50,"metadata_quality":"partial","status":"ok"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(ledgerPath, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := DoctorProviderLedgerCosts(ledgerPath, ProviderLedgerImportOptions{})
	if err != nil {
		t.Fatalf("cost doctor: %v", err)
	}
	if report.Status != "review" ||
		report.RowsChecked != 2 ||
		report.TokenUsageRows != 2 ||
		report.CostKnownRows != 1 ||
		report.TokenUsageMissingCost != 1 ||
		report.UnknownProjectRows != 1 ||
		report.PartialMetadataRows != 1 {
		t.Fatalf("report = %+v", report)
	}
	recorded, err := RecordCostDoctorObservation(home, report, t0)
	if err != nil {
		t.Fatalf("record cost observation: %v", err)
	}
	if recorded.CandidateID == "" || recorded.NoPromoteID == "" || !recorded.RecordedNoPromote {
		t.Fatalf("recorded report = %+v", recorded)
	}
	candidatePath := filepath.Join(home, candidates.InboxDir(candidates.RouteObservation), recorded.CandidateID+".json")
	data := readFile(t, candidatePath)
	var cand RouteCostCandidate
	if err := json.Unmarshal(data, &cand); err != nil {
		t.Fatalf("parse candidate: %v", err)
	}
	if cand.Kind != string(candidates.RouteObservation) ||
		cand.Verdict != candidates.NoPromote ||
		!strings.Contains(cand.Statement, "without cost metadata") {
		t.Fatalf("candidate = %+v", cand)
	}
	nopData := readFile(t, filepath.Join(home, "ledger", "no-promote-decisions.jsonl"))
	if !strings.Contains(string(nopData), cand.ID) || !strings.Contains(string(nopData), "route_cost_coverage") {
		t.Fatalf("no-promote rows:\n%s", nopData)
	}
	rerecorded, err := RecordCostDoctorObservation(home, report, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("rerecord cost observation: %v", err)
	}
	if rerecorded.CandidateID != recorded.CandidateID || rerecorded.RecordNoPromoteInserted {
		t.Fatalf("rerecorded report = %+v, want same candidate and no duplicate no-promote", rerecorded)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("cost doctor must not promote claims, stat err=%v", err)
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

func TestLoadAuthoritySummarizesExecutionRouteEvidence(t *testing.T) {
	home := t.TempDir()
	ledgerPath := filepath.Join(t.TempDir(), "provider-calls.ndjson")
	rows := strings.Join([]string{
		`{"request_id":"req_old","ts_start":"2026-06-29T09:00:00Z","caller":"codex","project":"product-x","purpose":"old","route_alias":"cheap.classify","provider":"fixture-provider","model":"fixture-small","transport":"chat_completions","input_tokens":1,"output_tokens":2,"status":"ok"}`,
		`{"request_id":"req_new","run_id":"run_new","ts_start":"2026-06-29T10:00:00Z","caller":"codex","project":"product-x","purpose":"new","route_alias":"premium.reasoning","provider":"fixture-provider","model":"fixture-large","transport":"responses","input_tokens":100,"output_tokens":50,"estimated_cost_usd":0.01,"latency_ms":55,"status":"ok"}`,
		"",
	}, "\n")
	if err := os.WriteFile(ledgerPath, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportProviderLedgerFile(home, ledgerPath, ProviderLedgerImportOptions{}); err != nil {
		t.Fatalf("import provider ledger: %v", err)
	}
	report, err := LoadAuthority(home, 1)
	if err != nil {
		t.Fatalf("load authority: %v", err)
	}
	if report.SchemaVersion != RouteEvidenceAuthorityReportSchema ||
		report.AuthorityRef != RouteEvidenceAuthorityRef ||
		report.Status != "ok" ||
		!report.Installed ||
		report.RouteEvidenceCount != 2 ||
		report.ProviderLedgerEvidenceCount != 2 ||
		report.TokenUsageRows != 2 ||
		report.CostKnownRows != 1 ||
		len(report.Recent) != 1 {
		t.Fatalf("bad authority report: %+v", report)
	}
	if report.Recent[0].Provider != "fixture-provider" || report.Recent[0].RequestID != "req_new" {
		t.Fatalf("recent summary = %+v, want newest provider ledger row", report.Recent[0])
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route evidence authority must not promote claims, stat err=%v", err)
	}
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
