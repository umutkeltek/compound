package panel

import (
	"bytes"
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
		t.Fatalf("load default parked targets: %v", err)
	}
	if len(limited) != 1 || limited[0].Ref != "mcand_alpha" {
		t.Fatalf("default parked/limit result = %+v, want mcand_alpha only", limited)
	}
}

func writeMemoryCandidateRecord(t *testing.T, homePath, id string, payload map[string]any) {
	t.Helper()
	if err := candidates.WriteCandidate(homePath, candidates.Memory, id, payload); err != nil {
		t.Fatalf("write memory candidate %s: %v", id, err)
	}
}
