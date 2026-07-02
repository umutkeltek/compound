package knowledge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

var testNow = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

func TestImportRecordsEvidenceCandidateExtractionAndSource(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}

	report, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if report.Items != 3 || report.Imported != 3 || report.Echoes != 0 || report.Reused != 0 {
		t.Fatalf("report counts = %+v", report)
	}
	if len(report.EvidenceIDs) != 3 || len(report.CandidateIDs) != 3 || len(report.ExtractionIDs) != 3 {
		t.Fatalf("report ids = %+v", report)
	}

	evidence, err := store.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 3 {
		t.Fatalf("evidence rows = %d; want 3", len(evidence))
	}
	if evidence[0].SourceKind != "arxiv" || evidence[0].Actor != "tool" {
		t.Fatalf("evidence[0] = %+v", evidence[0])
	}

	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 {
		t.Fatalf("candidate rows = %d; want 3", len(candidates))
	}
	if candidates[0].State != "proposed" || candidates[0].Qualifiers["source"] != "knowledge_import" {
		t.Fatalf("candidate[0] = %+v", candidates[0])
	}
	if candidates[0].PromotionBasis != "trusted_deterministic_import" {
		t.Fatalf("metadata candidate basis = %q, want trusted_deterministic_import (deterministic tool import)", candidates[0].PromotionBasis)
	}
	if candidates[0].Visibility != "contextual" {
		t.Fatalf("public metadata candidate visibility = %q, want contextual", candidates[0].Visibility)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("knowledge import must not create promoted claims, stat err=%v", err)
	}

	extractionPath := filepath.Join(homePath, "inbox", "knowledge-extractions", report.ExtractionIDs[0]+".json")
	var extraction ExtractionRequest
	readJSON(t, extractionPath, &extraction)
	if extraction.SchemaVersion != ExtractionRequestSchemaVersion || extraction.State != "pending" || extraction.EvidenceRef == "" {
		t.Fatalf("extraction = %+v", extraction)
	}

	var sources []SourceRegistryRow
	readJSONLRows(t, filepath.Join(homePath, "registries", "knowledge-sources.jsonl"), &sources)
	if len(sources) != 1 || sources[0].SourceID != "source:arxiv:arxiv:cs.AI+cs.CL" || sources[0].TrustPosterior != 0.5 {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestImportFoldsEchoAcrossSourceKinds(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}

	echo, err := ImportFile(homePath, store, fixturePath("web-echo-export.json"), testNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if echo.Items != 1 || echo.Imported != 0 || echo.Echoes != 1 || echo.Reused != 0 {
		t.Fatalf("echo report counts = %+v", echo)
	}
	if len(echo.CandidateIDs) != 0 || len(echo.ExtractionIDs) != 0 {
		t.Fatalf("echo must not create candidates/extractions: %+v", echo)
	}

	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 {
		t.Fatalf("echo created duplicate candidates: %+v", candidates)
	}

	var events []DistributionEvent
	readJSONLRows(t, filepath.Join(homePath, "ledger", "knowledge-distribution.jsonl"), &events)
	if len(events) != 1 {
		t.Fatalf("distribution events = %+v", events)
	}
	if events[0].CanonicalRef != "https://arxiv.org/abs/2506.12345" || events[0].EchoSourceKind != "web" || events[0].FirstEvidenceRef != first.EvidenceIDs[0] {
		t.Fatalf("distribution event = %+v", events[0])
	}
}

func TestExtractPendingWritesProposedMemoryCandidatesAndMarksDone(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	importReport, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := t.TempDir()
	writeExtractionFixture(t, fixtures, importReport.ExtractionIDs[0], importReport.EvidenceIDs[0])

	report, err := Extract(homePath, store, ExtractOptions{ItemID: importReport.ExtractionIDs[0], FixturesDir: fixtures}, testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Processed != 1 || report.Created != 1 || len(report.CandidateIDs) != 1 {
		t.Fatalf("extract report = %+v", report)
	}

	var req ExtractionRequest
	readJSON(t, filepath.Join(homePath, "inbox", "knowledge-extractions", importReport.ExtractionIDs[0]+".json"), &req)
	if req.State != "done" || req.CompletedAt == "" || len(req.CandidateIDs) != 1 || req.RouteEvidence == nil || req.RouteEvidence.Provider != "fixture" {
		t.Fatalf("extraction request not marked done: %+v", req)
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	var extracted *mem.Candidate
	for i := range candidates {
		if candidates[i].ID == report.CandidateIDs[0] {
			extracted = &candidates[i]
			break
		}
	}
	if extracted == nil {
		t.Fatalf("extracted candidate %s not found in %+v", report.CandidateIDs[0], candidates)
	}
	if extracted.State != "proposed" || extracted.Qualifiers["source"] != "knowledge_extraction" || extracted.Qualifiers["extraction_id"] != req.ID {
		t.Fatalf("candidate shape = %+v", extracted)
	}
	if extracted.SourceTraceRef != "knowledge_extraction:"+req.ID || extracted.Visibility != "candidate" {
		t.Fatalf("candidate provenance = %+v", extracted)
	}

	replay, err := Extract(homePath, store, ExtractOptions{ItemID: req.ID, FixturesDir: fixtures}, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replay.Processed != 0 || replay.Skipped != 1 || len(replay.CandidateIDs) != 1 || replay.CandidateIDs[0] != extracted.ID {
		t.Fatalf("idempotent replay = %+v", replay)
	}
}

func TestExtractRejectsUngroundedFixtureEvidenceRefs(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	importReport, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := t.TempDir()
	writeExtractionFixture(t, fixtures, importReport.ExtractionIDs[0], "ev_wrong")

	report, err := Extract(homePath, store, ExtractOptions{ItemID: importReport.ExtractionIDs[0], FixturesDir: fixtures}, testNow.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "failure(s)") {
		t.Fatalf("expected completed-with-failures error, got %v", err)
	}
	if report.Failed != 1 || report.Processed != 0 {
		t.Fatalf("report = %+v", report)
	}
	// The grounded-evidence rejection is preserved on the item itself.
	var req ExtractionRequest
	readJSON(t, filepath.Join(homePath, "inbox", "knowledge-extractions", importReport.ExtractionIDs[0]+".json"), &req)
	if req.State != "error" || !strings.Contains(req.Error, "evidence_ref") {
		t.Fatalf("item should hold the grounded-evidence error: %+v", req)
	}
}

func TestExtractOpenAICompatibleUsesEvalCorpusAndRecordsRouteEvidence(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	importReport, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		body := extractionFixtureBody(importReport.ExtractionIDs[0], importReport.EvidenceIDs[0])
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant", "content": body},
			}},
			"usage": map[string]int{"total_tokens": 12},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	report, err := Extract(homePath, store, ExtractOptions{
		ItemID:              importReport.ExtractionIDs[0],
		OpenAICompatibleURL: server.URL,
		Model:               "fixture-model",
		EvalCorpusDir:       filepath.Join("..", "..", "fixtures", "distiller"),
	}, testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || report.Processed != 1 || report.Created != 1 || len(report.CandidateIDs) != 1 {
		t.Fatalf("calls=%d report=%+v", calls, report)
	}
	var req ExtractionRequest
	readJSON(t, filepath.Join(homePath, "inbox", "knowledge-extractions", importReport.ExtractionIDs[0]+".json"), &req)
	if req.RouteEvidence == nil || req.RouteEvidence.Provider != "openai-compatible" || !req.RouteEvidence.Called || req.RouteEvidence.LiveModelCalls != 1 {
		t.Fatalf("route evidence = %+v", req.RouteEvidence)
	}
	if req.RouteEvidence.Model != "fixture-model" || req.RouteEvidence.Usage.TotalTokens != 12 {
		t.Fatalf("route evidence model/usage = %+v", req.RouteEvidence)
	}
}

func TestWholeFileImportIsIdempotent(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Items != 3 || second.Imported != 0 || second.Echoes != 0 || second.Reused != 3 {
		t.Fatalf("second report counts = %+v", second)
	}
	if strings.Join(second.EvidenceIDs, ",") != strings.Join(first.EvidenceIDs, ",") {
		t.Fatalf("reused evidence ids changed: first=%+v second=%+v", first.EvidenceIDs, second.EvidenceIDs)
	}

	evidence, err := store.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 3 {
		t.Fatalf("idempotent import created duplicate evidence: %+v", evidence)
	}
	var imports []ImportIndexRow
	readJSONLRows(t, filepath.Join(homePath, "indexes", "knowledge-imports.jsonl"), &imports)
	if len(imports) != 3 {
		t.Fatalf("idempotent import appended rows: %+v", imports)
	}
}

func TestDecodeExportRejectsStrictShapeProblems(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown_field",
			body: `{"schema_version":"compound.knowledge_export.v0.1","source":{"kind":"arxiv","ref":"arxiv:cs.AI","captured_at":"2026-07-02T12:00:00Z"},"items":[{"ref":"x","content":"body","extra":true}]}`,
			want: "unknown field",
		},
		{
			name: "missing_ref",
			body: `{"schema_version":"compound.knowledge_export.v0.1","source":{"kind":"arxiv","ref":"arxiv:cs.AI","captured_at":"2026-07-02T12:00:00Z"},"items":[{"content":"body"}]}`,
			want: "items[0].ref required",
		},
		{
			name: "empty_items",
			body: `{"schema_version":"compound.knowledge_export.v0.1","source":{"kind":"arxiv","ref":"arxiv:cs.AI","captured_at":"2026-07-02T12:00:00Z"},"items":[]}`,
			want: "items nonempty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeExport([]byte(tt.body)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodeExport error = %v; want %q", err, tt.want)
			}
		})
	}
}

func TestNewMediumImportsWithoutCoreChange(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "podcast-export.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": "compound.knowledge_export.v0.1",
  "source": {"kind": "podcast", "ref": "podcast:meaning-machines", "captured_at": "2026-07-02T12:00:00Z"},
  "items": [{
    "ref": "episode:42",
    "title": "Evidence Before Truth",
    "authors": ["Host Example"],
    "published_at": "2026-07-01",
    "topics": ["memory"],
    "content": "A podcast episode discusses why captured material should become evidence before candidate claims.",
    "lang": "en"
  }]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ImportFile(homePath, store, path, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if report.Items != 1 || report.Imported != 1 || len(report.CandidateIDs) != 1 {
		t.Fatalf("podcast report = %+v", report)
	}
	evidence, err := store.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].SourceKind != "podcast" {
		t.Fatalf("podcast evidence = %+v", evidence)
	}
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "fixtures", "knowledge", name)
}

func writeExtractionFixture(t *testing.T, dir, extractionID, evidenceID string) {
	t.Helper()
	body := extractionFixtureBody(extractionID, evidenceID)
	if err := os.WriteFile(filepath.Join(dir, extractionID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func extractionFixtureBody(extractionID, evidenceID string) string {
	return fmt.Sprintf(`{
 "schema_version": "compound.distiller.llm_extraction.v0.1",
 "trace_ref": "knowledge_extraction:%s",
 "extractor": "fixture",
 "model": "fixture-rules",
 "source_refs": [%q],
 "candidates": [
  {
   "kind": "memory",
   "entity": "paper:retrieval-agents",
   "predicate": "claim",
   "object": "Retrieval agents need grounded evidence to avoid echo amplification.",
   "confidence": 0.78,
   "derivation": "extracted",
   "claim_type": "architecture",
   "qualifiers": {"domain": "knowledge-intake"},
   "evidence_refs": [%q]
  }
 ]
}`, extractionID, evidenceID, evidenceID)
}

func readJSON(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatal(err)
	}
}

func readJSONLRows[T any](t *testing.T, path string, out *[]T) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row T
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		*out = append(*out, row)
	}
}

func TestExtractFailsSoftPerItem(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	importReport, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(importReport.ExtractionIDs) < 2 {
		t.Fatalf("need >=2 extraction requests, got %d", len(importReport.ExtractionIDs))
	}
	// Fixture responses exist for exactly one request: every other item fails
	// and must not strand the run (state=error, retryable) while the good one
	// completes.
	fixtures := t.TempDir()
	writeExtractionFixture(t, fixtures, importReport.ExtractionIDs[0], importReport.EvidenceIDs[0])

	report, err := Extract(homePath, store, ExtractOptions{Pending: true, FixturesDir: fixtures}, testNow.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "failure(s)") {
		t.Fatalf("want completed-with-failures error, got %v", err)
	}
	want := len(importReport.ExtractionIDs) - 1
	if report.Processed != 1 || report.Failed != want {
		t.Fatalf("processed=%d failed=%d, want 1 and %d", report.Processed, report.Failed, want)
	}
	states := map[string]int{}
	for _, id := range importReport.ExtractionIDs {
		var req ExtractionRequest
		readJSON(t, filepath.Join(homePath, "inbox", "knowledge-extractions", id+".json"), &req)
		states[req.State]++
	}
	if states["done"] != 1 || states["error"] != want {
		t.Fatalf("states = %v", states)
	}
}

func TestExtractionPromptCarriesCandidateShape(t *testing.T) {
	req := ExtractionRequest{ID: "kx_x", ItemRef: "arxiv:1234.5678v1", SourceKind: "arxiv", Title: "T", EvidenceRef: "ev_1"}
	llmReq := buildKnowledgeExtractionRequest(req, mem.EvidenceItem{ID: "ev_1", QuoteText: "quote"})
	joined := strings.Join(llmReq.Instructions, "\n")
	for _, want := range []string{"knowledge:arxiv:arxiv:1234.5678v1", "trace_ref must be knowledge_extraction:kx_x", `Candidate kind must be "memory"`, "entity, predicate, and object", "snake_case", "falsifiable"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("instructions missing %q:\n%s", want, joined)
		}
	}
}
