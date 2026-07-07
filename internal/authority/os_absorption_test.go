package authority

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/policy"
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

// TestOSAbsorptionAutoProofDecisionsAndOperatorState is J1's positive proof:
// os_source_decisions_repo and os_source_operator_state report proof_status=ok
// with a proof_ref once their REAL consumers (the decisions source-authority
// ledger and the policy operator-state reader) have live evidence, without any
// row status-string change (decisions stays evidence_only, operator-state
// stays absorbed).
func TestOSAbsorptionAutoProofDecisionsAndOperatorState(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	if _, err := decisions.RecordSourceAuthority(homePath, decisions.DecisionImportReport{
		SourcePath:  "/synthetic/operating-system/decisions/DEC-0001.md",
		SourceKind:  "decision_source",
		EvidenceID:  "ev_dec_1",
		CandidateID: "cand_dec_1",
		ClaimID:     "claim_dec_1",
		Verdict:     candidates.Promote,
		Lifecycle:   decisions.Lifecycle{ID: "dec_0001"},
	}, "test import", now); err != nil {
		t.Fatalf("record decisions source authority: %v", err)
	}

	opSourcePath := filepath.Join(t.TempDir(), "operator-state.json")
	if err := os.WriteFile(opSourcePath, []byte(`{"mode":"test","autonomy_lever":{"level":"L2"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.ImportOperatorState(homePath, opSourcePath, now); err != nil {
		t.Fatalf("import operator state: %v", err)
	}

	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	matrix := OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows: []OSAbsorptionMatrixRow{
			row("os_source_decisions_repo", "/synthetic/operating-system/decisions/", "evidence_only", osAbsorptionDecisionsLedgerRef, "compound decisions authority", ""),
			row("os_source_operator_state", "/synthetic/umut-os/registry/operator-state.json", "absorbed", policy.OperatorStateAuthorityRef, "compound policy evaluate", ""),
		},
	}
	writeJSONFixture(t, matrixPath, matrix)
	if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, now); err != nil {
		t.Fatalf("import matrix: %v", err)
	}

	doctor, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	byID := map[string]OSAbsorptionDoctorRow{}
	for _, r := range doctor.Rows {
		byID[r.ID] = r
	}
	decRow, ok := byID["os_source_decisions_repo"]
	if !ok || decRow.ProofStatus != "ok" || decRow.ProofRef == "" || decRow.Status != "evidence_only" {
		t.Fatalf("expected decisions row proof_status=ok with proof_ref and unchanged status=evidence_only, got %+v", decRow)
	}
	opRow, ok := byID["os_source_operator_state"]
	if !ok || opRow.ProofStatus != "ok" || opRow.ProofRef == "" || opRow.Status != "absorbed" {
		t.Fatalf("expected operator-state row proof_status=ok with proof_ref and unchanged status=absorbed, got %+v", opRow)
	}
}

// TestOSAbsorptionDecisionsProofFailsWhenLedgerAbsent is J1's falsifier: with
// no decisions source-authority record ever written to COMPOUND_HOME, the row
// must flip to proof_status=failed — never ok, never silently manual.
func TestOSAbsorptionDecisionsProofFailsWhenLedgerAbsent(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	matrix := OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows: []OSAbsorptionMatrixRow{
			row("os_source_decisions_repo", "/synthetic/operating-system/decisions/", "evidence_only", osAbsorptionDecisionsLedgerRef, "compound decisions authority", ""),
		},
	}
	writeJSONFixture(t, matrixPath, matrix)
	if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, now); err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	doctor, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if len(doctor.Rows) != 1 {
		t.Fatalf("expected one row, got %+v", doctor.Rows)
	}
	got := doctor.Rows[0]
	if got.ProofStatus != "failed" {
		t.Fatalf("expected proof_status=failed when the decisions ledger is absent, got %+v", got)
	}
	if got.ProofDetail == "" {
		t.Fatalf("expected a failure reason on the row, got %+v", got)
	}
	if got.Status != "evidence_only" {
		t.Fatalf("row status string must not change: %+v", got)
	}
}

// TestOSAbsorptionOperatorStateProofFailsWhenRegistryAbsent mirrors the
// decisions falsifier for the operator-state row: no import ever ran, so the
// policy reader finds nothing and the row must fail proof, not pass or hide
// behind "manual".
func TestOSAbsorptionOperatorStateProofFailsWhenRegistryAbsent(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	matrix := OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows: []OSAbsorptionMatrixRow{
			row("os_source_operator_state", "/synthetic/umut-os/registry/operator-state.json", "absorbed", policy.OperatorStateAuthorityRef, "compound policy evaluate", ""),
		},
	}
	writeJSONFixture(t, matrixPath, matrix)
	if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, now); err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	doctor, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if len(doctor.Rows) != 1 {
		t.Fatalf("expected one row, got %+v", doctor.Rows)
	}
	got := doctor.Rows[0]
	if got.ProofStatus != "failed" {
		t.Fatalf("expected proof_status=failed when the operator-state registry is absent, got %+v", got)
	}
	if got.Status != "absorbed" {
		t.Fatalf("row status string must not change: %+v", got)
	}
}

// TestOSAbsorptionCountsSplitProvenVsAsserted is J2's falsifier: on a fixture
// with one machine-proven absorbed row, one manual (unproven) absorbed-bucket
// row, and one pending row, machine_proven_count must diverge from the
// legacy absorbed_count bucket — proving the split reflects real evidence,
// not the same status-string tally under a new name.
func TestOSAbsorptionCountsSplitProvenVsAsserted(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	capSource := "/synthetic/umut-os/spine/views/skill-manifest.json"
	if _, err := capgraph.RecordSourceAuthority(homePath, capgraph.SourceAuthorityInput{
		SourcePath:     capSource,
		SourceRevision: "sha256:" + digest12(capSource),
		ImportedVia:    "test import",
		NodeIDs:        []string{"skill:manifest"},
		EvidenceRefs:   []string{"fixture:manifest"},
	}, now); err != nil {
		t.Fatalf("record cap source authority: %v", err)
	}

	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	matrix := OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows: []OSAbsorptionMatrixRow{
			row("skill_manifest", capSource, "absorbed", capgraph.CapabilityGraphAuthorityRef, "compound capability authority", "capability_source_authority"),
			row("os_capability_cli", "/synthetic/cli/os-capability", "wrapper", capgraph.CapabilityGraphAuthorityRef, "os capability wrapper", ""),
			row("brain", "/synthetic/brain/projects/compound.md", "pending", "compound://ledger/memory", "compound memory recall", ""),
		},
	}
	writeJSONFixture(t, matrixPath, matrix)
	importReport, err := ImportOSAbsorptionMatrix(homePath, matrixPath, now)
	if err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	if importReport.AbsorbedCount != 2 || importReport.PendingCount != 1 {
		t.Fatalf("unexpected import counts: %+v", importReport)
	}

	doctor, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if doctor.AbsorbedCount != 2 {
		t.Fatalf("expected legacy absorbed_count=2 (status bucket), got %+v", doctor)
	}
	if doctor.MachineProvenCount != 1 {
		t.Fatalf("expected machine_proven_count=1 (only the capability-proven row), got %+v", doctor)
	}
	if doctor.AssertedCount != 1 {
		t.Fatalf("expected asserted_count=1 (the manual wrapper row), got %+v", doctor)
	}
	if doctor.PendingCount != 1 {
		t.Fatalf("expected pending_count=1 (existing), got %+v", doctor)
	}
	if doctor.MachineProvenCount == doctor.AbsorbedCount {
		t.Fatalf("machine_proven_count must diverge from absorbed_count to prove the split is real: %+v", doctor)
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
