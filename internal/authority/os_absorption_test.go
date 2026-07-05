package authority

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/capgraph"
)

func TestOSAbsorptionMatrixImportAndDoctor(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	capRows := []string{
		"/synthetic/umut-os/spine/views/skill-manifest.json",
		"/synthetic/umut-os/registry/tool-topology.json",
		"/synthetic/umut-os/spine/views/skills-selection-board.json",
	}
	for _, source := range capRows {
		if _, err := capgraph.RecordSourceAuthority(homePath, capgraph.SourceAuthorityInput{
			SourcePath:     source,
			SourceRevision: "sha256:" + digest12(source),
			ImportedVia:    "test import",
			NodeIDs:        []string{"skill:" + filepath.Base(source)},
			EvidenceRefs:   []string{"fixture:" + filepath.Base(source)},
		}, now); err != nil {
			t.Fatalf("record cap source authority: %v", err)
		}
	}
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	matrix := OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows: []OSAbsorptionMatrixRow{
			row("skill_manifest", capRows[0], "absorbed", capgraph.CapabilityGraphAuthorityRef, "compound capability authority", "capability_source_authority"),
			row("tool_topology", capRows[1], "absorbed", capgraph.CapabilityGraphAuthorityRef, "compound capability authority", "capability_source_authority"),
			row("skills_selection_board", capRows[2], "absorbed", capgraph.CapabilityGraphAuthorityRef, "compound capability authority", "capability_source_authority"),
			row("brain", "/synthetic/brain/projects/compound.md", "pending", "compound://ledger/memory", "compound memory recall", ""),
			row("gbrain", "gbrain://synthetic-store", "pending", "compound://adapters/memory-engine/gbrain", "compound memory engine-doctor", ""),
			row("infra_os", "/synthetic/infra-os/project-registry.yaml", "pending", "compound://registries/projects", "compound runtime doctor", ""),
			row("os_status", "cli:os status", "pending", "compound://projections/system-status", "os status wrapper", ""),
		},
	}
	writeJSONFixture(t, matrixPath, matrix)
	report, err := ImportOSAbsorptionMatrix(homePath, matrixPath, now)
	if err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	if report.RowCount != 7 || report.AbsorbedCount != 3 || report.PendingCount != 4 {
		t.Fatalf("unexpected import counts: %+v", report)
	}
	doctor, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if doctor.Status != "ok" {
		t.Fatalf("doctor should pass: %+v", doctor)
	}
	if doctor.AutoProofCount != 3 || doctor.PendingWithGateCount != 4 {
		t.Fatalf("doctor proof counts wrong: %+v", doctor)
	}
}

func TestOSAbsorptionDoctorFlagsMissingRequiredFields(t *testing.T) {
	homePath := t.TempDir()
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	matrix := OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows: []OSAbsorptionMatrixRow{
			{
				ID:          "bad_row",
				SourceRef:   "/synthetic/source",
				OldOwner:    "legacy",
				OldRole:     "legacy role",
				Privacy:     "private",
				Status:      "pending",
				CurrentRole: "pending",
			},
		},
	}
	writeJSONFixture(t, matrixPath, matrix)
	if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	doctor, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if doctor.Status != "fail" {
		t.Fatalf("doctor should fail: %+v", doctor)
	}
	if doctor.MissingRequiredCount != 1 {
		t.Fatalf("expected missing row count, got %+v", doctor)
	}
}

func row(id, source, status, owner, consumer, proofMode string) OSAbsorptionMatrixRow {
	return OSAbsorptionMatrixRow{
		ID:             id,
		SourceRef:      source,
		OldOwner:       "legacy",
		OldRole:        "legacy source",
		CurrentRole:    "evidence_only_input",
		Privacy:        "private",
		CompoundOwner:  owner,
		Consumer:       consumer,
		Proof:          consumer + " proof",
		ProofMode:      proofMode,
		RetirementGate: "consumer reads Compound authority",
		Status:         status,
	}
}

func writeJSONFixture(t *testing.T, path string, v any) {
	t.Helper()
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
