package panel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
)

func TestLoadMemoryCandidateTargetsFiltersStateAndMapsVisibility(t *testing.T) {
	homePath := t.TempDir()
	writeMemoryCandidateRecord(t, homePath, "mcand_alpha", map[string]any{
		"id":               "mcand_alpha",
		"entity":           "service:example-api",
		"predicate":        "uses_runtime",
		"object":           "go-http",
		"claim_type":       "architecture",
		"confidence":       0.91,
		"promotion_basis":  "runtime_proof",
		"state":            "parked",
		"visibility":       "contextual",
		"derivation":       "deterministic",
		"source_trace_ref": "trace://alpha",
		"snippet_hash":     "sha256:alpha",
		"extra_field":      "payload stays raw",
	})
	writeMemoryCandidateRecord(t, homePath, "mcand_beta", map[string]any{
		"id":               "mcand_beta",
		"entity":           "service:example-api",
		"predicate":        "uses_cache",
		"object":           "memory",
		"confidence":       0.65,
		"promotion_basis":  "",
		"state":            "proposed",
		"visibility":       "candidate",
		"derivation":       "inferred",
		"source_trace_ref": "trace://beta",
		"snippet_hash":     "sha256:beta",
	})
	writeMemoryCandidateRecord(t, homePath, "mcand_gamma", map[string]any{
		"id":               "mcand_gamma",
		"entity":           "service:example-worker",
		"predicate":        "uses_queue",
		"object":           "jobs",
		"confidence":       0.82,
		"promotion_basis":  "trusted_deterministic_import",
		"state":            "parked",
		"visibility":       "ambient",
		"derivation":       "deterministic",
		"source_trace_ref": "trace://gamma",
		"snippet_hash":     "sha256:gamma",
	})

	targets, err := LoadMemoryCandidateTargets(homePath, []string{"parked"}, 0)
	if err != nil {
		t.Fatalf("load targets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2: %+v", len(targets), targets)
	}
	if targets[0].Kind != "memory_candidate" || targets[0].Ref != "mcand_alpha" {
		t.Fatalf("first target identity = %+v", targets[0])
	}
	if targets[0].Visibility != "contextual" {
		t.Fatalf("visibility = %q, want contextual", targets[0].Visibility)
	}
	if !strings.Contains(targets[0].Summary, "service:example-api uses_runtime go-http") ||
		!strings.Contains(targets[0].Summary, "basis=runtime_proof") ||
		!strings.Contains(targets[0].Summary, "confidence=0.91") {
		t.Fatalf("summary missing compact candidate fields: %q", targets[0].Summary)
	}
	if !bytes.Contains(targets[0].Payload, []byte(`"extra_field"`)) {
		t.Fatalf("payload did not preserve raw candidate JSON: %s", targets[0].Payload)
	}

	limited, err := LoadMemoryCandidateTargets(homePath, nil, 1)
	if err != nil {
		t.Fatalf("load default proposed targets: %v", err)
	}
	if len(limited) != 1 || limited[0].Ref != "mcand_beta" {
		t.Fatalf("default proposed/limit result = %+v, want mcand_beta only", limited)
	}
}

func TestLoadMemoryCandidateTargetAddsKnowledgeExtractionProvenance(t *testing.T) {
	homePath := t.TempDir()
	writeMemoryCandidateRecord(t, homePath, "mcand_kx", map[string]any{
		"id":               "mcand_kx",
		"entity":           "knowledge:arxiv:2607.01211v1",
		"predicate":        "limitation",
		"object":           "benchmark scoring overweights worst tasks",
		"confidence":       0.89,
		"state":            "proposed",
		"visibility":       "candidate",
		"derivation":       "extracted",
		"source_trace_ref": "knowledge_extraction:kx_alpha",
		"qualifiers": map[string]string{
			"source":        "knowledge_extraction",
			"extraction_id": "kx_alpha",
			"item_ref":      "arxiv:2607.01211v1",
		},
	})
	dir := filepath.Join(homePath, "inbox", "knowledge-extractions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir extraction dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kx_alpha.json"), []byte(`{
		"schema_version":"compound.knowledge_extraction_request.v0.1",
		"id":"kx_alpha",
		"item_ref":"arxiv:2607.01211v1",
		"evidence_ref":"ev_alpha",
		"source_kind":"arxiv",
		"title":"Benchmark Paper",
		"content_digest":"sha256:alpha",
		"state":"done",
		"created_at":"2026-07-02T12:00:00Z",
		"completed_at":"2026-07-02T12:30:00Z",
		"candidate_ids":["mcand_kx","mcand_other"],
		"route_evidence":{
			"provider":"openai-compatible",
			"model":"auto/extract",
			"called":true,
			"usage":{"total_tokens":2351},
			"latency_ms":1234
		}
	}`), 0o644); err != nil {
		t.Fatalf("write extraction request: %v", err)
	}

	target, err := LoadTargetInput(homePath, "memory_candidate", "mcand_kx")
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(target.Payload, &payload); err != nil {
		t.Fatalf("decode target payload: %v", err)
	}
	if payload["object"] != "benchmark scoring overweights worst tasks" {
		t.Fatalf("candidate payload was not preserved: %+v", payload)
	}
	prov, ok := payload["source_provenance"].(map[string]any)
	if !ok {
		t.Fatalf("source_provenance missing: %+v", payload)
	}
	for key, want := range map[string]string{
		"status":           "resolved",
		"source_trace_ref": "knowledge_extraction:kx_alpha",
		"extraction_id":    "kx_alpha",
		"evidence_ref":     "ev_alpha",
		"source_kind":      "arxiv",
		"item_ref":         "arxiv:2607.01211v1",
		"title":            "Benchmark Paper",
		"model":            "auto/extract",
		"provider":         "openai-compatible",
	} {
		if got := prov[key]; got != want {
			t.Fatalf("source_provenance[%s] = %v, want %q; full=%+v", key, got, want, prov)
		}
	}
	if got := prov["candidate_count"]; got != float64(2) {
		t.Fatalf("candidate_count = %v, want 2; full=%+v", got, prov)
	}
}

func writeMemoryCandidateRecord(t *testing.T, homePath, id string, payload map[string]any) {
	t.Helper()
	if err := candidates.WriteCandidate(homePath, candidates.Memory, id, payload); err != nil {
		t.Fatalf("write memory candidate %s: %v", id, err)
	}
}
