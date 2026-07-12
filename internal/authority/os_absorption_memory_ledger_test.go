package authority

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
)

func TestOSAbsorptionDoctorMemoryLedgerAuthority(t *testing.T) {
	const (
		sourceRef       = "/synthetic/brain/projects/compound.md"
		evidenceID      = "ev_memory_ledger_proof"
		knowledgePageID = "knowledge_memory_ledger_proof"
	)

	tests := []struct {
		name            string
		promotionBasis  string
		rowEvidenceRefs []string
		sourcePath      string
		wantStatus      string
		wantProofRef    bool
	}{
		{
			name:            "promoted claim through evidence intermediary proves row",
			promotionBasis:  candidates.BasisCuratedPage,
			rowEvidenceRefs: []string{"knowledge_page:" + knowledgePageID},
			wantStatus:      "ok",
			wantProofRef:    true,
		},
		{
			name:           "promoted claim through curated source path proves row",
			promotionBasis: candidates.BasisCuratedPage,
			sourcePath:     sourceRef,
			wantStatus:     "ok",
			wantProofRef:   true,
		},
		{
			name:            "basisless claim remains pending",
			rowEvidenceRefs: []string{"evidence:" + evidenceID, "knowledge_page:" + knowledgePageID},
			wantStatus:      "pending",
			wantProofRef:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homePath := t.TempDir()
			now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
			matrixPath := filepath.Join(t.TempDir(), "matrix.json")
			memoryRow := row("os_source_brain_project_compound", sourceRef, "pending", "compound://ledger/memory", "compound memory recall", "")
			memoryRow.EvidenceRefs = tt.rowEvidenceRefs
			writeJSONFixture(t, matrixPath, OSAbsorptionSourceMatrix{
				SchemaVersion: OSAbsorptionMatrixSchema,
				Rows:          []OSAbsorptionMatrixRow{memoryRow},
			})
			if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, now); err != nil {
				t.Fatalf("import matrix: %v", err)
			}
			writeMemoryLedgerClaimFixture(t, homePath, evidenceID, knowledgePageID, tt.promotionBasis, tt.sourcePath)
			writeMemoryLedgerEvidenceFixture(t, homePath, evidenceID, knowledgePageID)

			report, err := OSAbsorptionDoctor(homePath)
			if err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if len(report.Rows) != 1 {
				t.Fatalf("doctor rows = %+v, want one", report.Rows)
			}
			got := report.Rows[0]
			if got.ProofStatus != tt.wantStatus {
				t.Fatalf("memory-ledger proof_status = %q, want %q; row=%+v", got.ProofStatus, tt.wantStatus, got)
			}
			if (got.ProofRef != "") != tt.wantProofRef {
				t.Fatalf("memory-ledger proof_ref = %q, want non-empty=%t; row=%+v", got.ProofRef, tt.wantProofRef, got)
			}
			if tt.wantProofRef {
				for _, want := range []string{"claim=claim_memory_ledger_proof", "evidence=" + evidenceID} {
					if !strings.Contains(got.ProofRef, want) {
						t.Fatalf("memory-ledger proof_ref = %q, want %q", got.ProofRef, want)
					}
				}
			}
			for _, rel := range []string{"inbox", "events"} {
				if _, err := os.Stat(filepath.Join(homePath, rel)); !os.IsNotExist(err) {
					t.Fatalf("memory-ledger proof mutated %s, stat err=%v", rel, err)
				}
			}
		})
	}
}

func TestOSAbsorptionDoctorMemoryLedgerAuthorityAcceptsHistoricalPromotedClaim(t *testing.T) {
	const (
		sourceRef       = "/synthetic/brain/projects/compound.md"
		evidenceID      = "ev_memory_ledger_historical"
		knowledgePageID = "knowledge_memory_ledger_historical"
	)
	homePath := t.TempDir()
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	memoryRow := row("os_source_brain_project_compound", sourceRef, "pending", "compound://ledger/memory", "compound memory recall", "")
	memoryRow.EvidenceRefs = []string{"knowledge_page:" + knowledgePageID}
	writeJSONFixture(t, matrixPath, OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows:          []OSAbsorptionMatrixRow{memoryRow},
	})
	if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	writeMemoryLedgerClaimFixture(t, homePath, evidenceID, knowledgePageID, candidates.BasisCuratedPage, sourceRef)
	writeMemoryLedgerEvidenceFixture(t, homePath, evidenceID, knowledgePageID)
	appendMemoryLedgerJSONFixture(t, filepath.Join(homePath, "ledger", "claims.jsonl"), map[string]any{
		"id":              "claim_memory_ledger_newer_unrelated",
		"seq":             2,
		"entity":          "system:compound",
		"predicate":       "os_absorption_end_state",
		"object":          "newer unrelated source",
		"valid_from":      "2026-07-12T12:01:00Z",
		"confidence":      0.9,
		"derivation":      "deterministic",
		"promotion_basis": candidates.BasisCuratedPage,
		"qualifiers": map[string]string{
			"source":      "knowledge_page",
			"page_id":     "knowledge_unrelated",
			"source_path": "/synthetic/brain/projects/unrelated.md",
		},
		"evidence_refs": []string{"cand_unrelated", "ev_unrelated"},
		"supersedes":    "claim_memory_ledger_proof",
		"visibility":    "contextual",
		"created_at":    "2026-07-12T12:01:00Z",
	})
	appendMemoryLedgerJSONFixture(t, filepath.Join(homePath, "ledger", "edges.jsonl"), map[string]any{
		"seq":  2,
		"kind": "supersedes",
		"from": "claim_memory_ledger_newer_unrelated",
		"to":   "claim_memory_ledger_proof",
	})

	report, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := report.Rows[0]
	if got.ProofStatus != "ok" || !strings.Contains(got.ProofRef, "claim=claim_memory_ledger_proof") || !strings.Contains(got.ProofRef, "evidence="+evidenceID) {
		t.Fatalf("historical promoted claim should prove absorption: %+v", got)
	}
}

func writeMemoryLedgerClaimFixture(t *testing.T, homePath, evidenceID, knowledgePageID, promotionBasis, sourcePath string) {
	t.Helper()
	qualifiers := map[string]string{
		"source":  "knowledge_page",
		"page_id": knowledgePageID,
	}
	if sourcePath != "" {
		qualifiers["source_path"] = sourcePath
	}
	claim := map[string]any{
		"id":              "claim_memory_ledger_proof",
		"seq":             1,
		"entity":          "system:compound",
		"predicate":       "os_absorption_end_state",
		"object":          "compound owns the absorbed memory authority",
		"valid_from":      "2026-07-12T12:00:00Z",
		"confidence":      0.9,
		"derivation":      "deterministic",
		"promotion_basis": promotionBasis,
		"qualifiers":      qualifiers,
		"evidence_refs":   []string{"cand_memory_ledger_proof", evidenceID},
		"visibility":      "contextual",
		"created_at":      "2026-07-12T12:00:00Z",
	}
	body, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(homePath, "ledger", "claims.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMemoryLedgerEvidenceFixture(t *testing.T, homePath, evidenceID, knowledgePageID string) {
	t.Helper()
	evidence := map[string]any{
		"id":           evidenceID,
		"source_kind":  "note",
		"source_id":    "knowledge_page:" + knowledgePageID,
		"content_hash": "fixture-memory-ledger-proof",
		"observed_at":  "2026-07-12T12:00:00Z",
		"visibility":   "contextual",
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(homePath, "ledger", "evidence.jsonl")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendMemoryLedgerJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
