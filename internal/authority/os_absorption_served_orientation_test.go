package authority

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

const testServedOrientationProofMode = "served_orientation_authority"

// TestOSAbsorptionServedOrientationRowsRequireProofMode is the removal
// falsifier: EffectiveContract-owned OS absorption rows are structurally
// served-orientation rows. Removing or changing proof_mode must fail matrix
// validation and must not turn the doctor proof into "manual".
func TestOSAbsorptionServedOrientationRowsRequireProofMode(t *testing.T) {
	relevant := row(
		"os_source_example_live_start",
		"/synthetic/example-os/LIVE_START.md",
		"absorbed",
		osAbsorptionEffectiveContractAuthorityRef,
		"compound orient and served client contract",
		"",
	)
	relevant.CurrentRole = "source_in_place_evidence"
	if !osAbsorptionNeedsServedOrientationProof(relevant) {
		t.Fatalf("EffectiveContract-owned row must need served-orientation proof even when proof_mode is removed: %+v", relevant)
	}
	if err := validateOSAbsorptionMatrix(OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows:          []OSAbsorptionMatrixRow{relevant},
	}); err == nil || !strings.Contains(err.Error(), osAbsorptionProofModeServedOrient) {
		t.Fatalf("missing proof_mode should fail the matrix with the required mode named, got %v", err)
	}

	relevant.ProofMode = osAbsorptionProofModeDecisions
	if err := validateOSAbsorptionMatrix(OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows:          []OSAbsorptionMatrixRow{relevant},
	}); err == nil || !strings.Contains(err.Error(), osAbsorptionProofModeServedOrient) {
		t.Fatalf("wrong proof_mode should fail the matrix with the required mode named, got %v", err)
	}

	relevant.ProofMode = osAbsorptionProofModeServedOrient
	if err := validateOSAbsorptionMatrix(OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows:          []OSAbsorptionMatrixRow{relevant},
	}); err != nil {
		t.Fatalf("required proof_mode should validate: %v", err)
	}
}

// TestValidateOSAbsorptionServedEntrypointRejectsAnyNonCompoundOrientHeading
// proves the retired-heading check is genericized: it rejects ANY
// "## Orient first — " heading that is not the required Compound heading,
// not just one hardcoded legacy string.
func TestValidateOSAbsorptionServedEntrypointRejectsAnyNonCompoundOrientHeading(t *testing.T) {
	sourceRef := "/synthetic/example-source/ENTRYPOINT.md"
	body := strings.Join([]string{
		"## Orient first — Compound (the served contract)",
		"compound orient --harness claude --task \"<task>\"",
		"Compound owns durable semantic meaning and promotion.",
		"brain is source/projection/migration evidence.",
		"gbrain is a bridge/index only.",
		"source-in-place evidence",
		"rollback",
		sourceRef,
		"",
		"## Orient first — Arbitrary Other Control Plane",
		"some retired predecessor entrypoint heading, not the required one",
	}, "\n")
	err := validateOSAbsorptionServedEntrypoint("claude", sourceRef, body)
	if err == nil {
		t.Fatalf("expected rejection of an arbitrary non-Compound orient heading, got nil error")
	}
	if !strings.Contains(err.Error(), "retired orient heading") {
		t.Fatalf("error %q does not name the retired-heading reason", err.Error())
	}
	if !strings.Contains(err.Error(), "Arbitrary Other Control Plane") {
		t.Fatalf("error %q should quote the offending heading", err.Error())
	}
}

func TestValidateOSAbsorptionServedEntrypointEnforcesMemoryAuthorityBoundary(t *testing.T) {
	sourceRef := "/synthetic/example-source/ENTRYPOINT.md"
	required := []string{
		"Compound owns durable semantic meaning and promotion.",
		"brain is source/projection/migration evidence.",
		"gbrain is a bridge/index only.",
	}
	validLines := validServedOrientationLines("codex", sourceRef)

	for _, missing := range required {
		t.Run("requires "+missing, func(t *testing.T) {
			lines := make([]string, 0, len(validLines)-1)
			for _, line := range validLines {
				if line != missing {
					lines = append(lines, line)
				}
			}
			err := validateOSAbsorptionServedEntrypoint("codex", sourceRef, strings.Join(lines, "\n"))
			if err == nil {
				t.Fatalf("expected rejection when required memory-authority guidance %q is missing", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("error %q should name missing guidance %q", err.Error(), missing)
			}
		})
	}

	for _, contradictory := range []string{
		"**~/brain** is the curated markdown **source of truth**.",
		"brain is canon.",
		"If curated brain and gbrain disagree, curated brain wins.",
		"Durable outcomes land directly in `~/brain`.",
		"brain remains the durable memory authority.",
		"Save durable facts to ~/brain.",
		"Hermes cron writes to ~/brain/reviews; curate to canon.",
		"gbrain remains the canonical memory authority.",
		"Do not forget: brain remains the durable memory authority.",
		"brain is not merely an index; it remains the durable memory authority.",
	} {
		t.Run("rejects "+contradictory, func(t *testing.T) {
			body := strings.Join(append(append([]string{}, validLines...), contradictory), "\n")
			err := validateOSAbsorptionServedEntrypoint("codex", sourceRef, body)
			if err == nil {
				t.Fatalf("expected rejection of contradictory memory-authority guidance %q", contradictory)
			}
			if !strings.Contains(err.Error(), "contradictory memory authority guidance") {
				t.Fatalf("error %q does not identify the memory-authority contradiction", err.Error())
			}
		})
	}
}

func TestValidateOSAbsorptionServedEntrypointUsesOnlyActiveInstructions(t *testing.T) {
	sourceRef := "/synthetic/example-source/ENTRYPOINT.md"
	requiredAuthority := strings.Join([]string{
		osAbsorptionRequiredMeaningAuthority,
		osAbsorptionRequiredBrainRole,
		osAbsorptionRequiredGBrainRole,
	}, "\n")
	activeWithoutAuthority := strings.Join([]string{
		"## Orient first — Compound (the served contract)",
		"Run `compound orient --harness codex --task \"<task>\"`.",
		"These files remain source-in-place evidence and rollback:",
		sourceRef,
	}, "\n")

	for _, hidden := range []string{
		"<!--\n" + requiredAuthority + "\n-->",
		"```text\n" + requiredAuthority + "\n```",
		"> " + strings.ReplaceAll(requiredAuthority, "\n", "\n> "),
		`Retired quotation: "` + strings.ReplaceAll(requiredAuthority, "\n", " ") + `"`,
	} {
		body := activeWithoutAuthority + "\n" + hidden
		err := validateOSAbsorptionServedEntrypoint("codex", sourceRef, body)
		if err == nil {
			t.Fatalf("authority roles hidden outside active instructions must not satisfy the guard:\n%s", hidden)
		}
		if !strings.Contains(err.Error(), "missing") {
			t.Fatalf("hidden authority roles should report missing active guidance, got %q", err)
		}
	}

	body := strings.Join(append(validServedOrientationLines("codex", sourceRef),
		"<!-- brain remains the durable memory authority. -->",
		"```text",
		"Save durable facts to ~/brain.",
		"```",
		"> Retired guidance: gbrain is canon.",
		`Retired quotation: "brain is the source of truth."`,
		"Never save durable facts directly to ~/brain; route them through Compound.",
		"Compound is the durable semantic authority; brain remains evidence.",
		"The canonical role for gbrain is bridge/index only.",
		`The statement "brain is the source of truth" is a retired quotation kept for migration evidence.`,
	), "\n")
	if err := validateOSAbsorptionServedEntrypoint("codex", sourceRef, body); err != nil {
		t.Fatalf("commented, fenced, quoted-retired, and explicitly forbidden contradictions are not active guidance: %v", err)
	}
}

func TestOSAbsorptionServedOrientationAuthorityProof(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*testing.T, servedOrientationFixture)
		wantStatus string
		wantDetail string
	}{
		{
			name:       "both generated clients and cited ContextPack are live",
			wantStatus: "ok",
		},
		{
			name: "missing served client fails closed",
			mutate: func(t *testing.T, fixture servedOrientationFixture) {
				t.Helper()
				if err := os.Remove(fixture.codexPath); err != nil {
					t.Fatal(err)
				}
			},
			wantStatus: "failed",
			wantDetail: "codex entrypoint",
		},
		{
			name: "missing cited ContextPack fails closed",
			mutate: func(t *testing.T, fixture servedOrientationFixture) {
				t.Helper()
				if err := os.Remove(fixture.contextPackPath); err != nil {
					t.Fatal(err)
				}
			},
			wantStatus: "failed",
			wantDetail: "ContextPack",
		},
		{
			name: "removed proof mode fails closed instead of becoming manual",
			mutate: func(t *testing.T, fixture servedOrientationFixture) {
				t.Helper()
				for _, id := range []string{"os_source_served_orientation_primary", "os_source_served_orientation_secondary"} {
					mutateOSAbsorptionMatrixRow(t, fixture.compoundHome, id, func(row *OSAbsorptionMatrixRow) {
						row.ProofMode = ""
					})
				}
			},
			wantStatus: "failed",
			wantDetail: osAbsorptionProofModeServedOrient,
		},
		{
			name: "arbitrary copied harness entrypoints fail binding",
			mutate: func(t *testing.T, fixture servedOrientationFixture) {
				t.Helper()
				copiedCodex := filepath.Join(t.TempDir(), "AGENTS.md")
				copiedClaude := filepath.Join(t.TempDir(), "CLAUDE.md")
				copyServedOrientationFixtureFile(t, fixture.codexPath, copiedCodex)
				copyServedOrientationFixtureFile(t, fixture.claudePath, copiedClaude)
				for _, id := range []string{"os_source_served_orientation_primary", "os_source_served_orientation_secondary"} {
					mutateOSAbsorptionMatrixRow(t, fixture.compoundHome, id, func(row *OSAbsorptionMatrixRow) {
						row.EvidenceRefs = replaceServedEntrypointEvidence(row.EvidenceRefs, "codex", copiedCodex)
						row.EvidenceRefs = replaceServedEntrypointEvidence(row.EvidenceRefs, "claude", copiedClaude)
					})
				}
			},
			wantStatus: "failed",
			wantDetail: "live installed",
		},
		{
			name: "arbitrary copied source entrypoint fails binding",
			mutate: func(t *testing.T, fixture servedOrientationFixture) {
				t.Helper()
				copiedSource := filepath.Join(t.TempDir(), "LIVE_START.md")
				copyServedOrientationFixtureFile(t, fixture.liveStartPath, copiedSource)
				for _, id := range []string{"os_source_served_orientation_primary", "os_source_served_orientation_secondary"} {
					mutateOSAbsorptionMatrixRow(t, fixture.compoundHome, id, func(row *OSAbsorptionMatrixRow) {
						oldSourceRef := row.SourceRef
						row.SourceRef = copiedSource
						for _, entrypointPath := range []string{fixture.codexPath, fixture.claudePath} {
							body, err := os.ReadFile(entrypointPath)
							if err != nil {
								t.Fatal(err)
							}
							body = []byte(strings.ReplaceAll(string(body), oldSourceRef, copiedSource))
							if err := os.WriteFile(entrypointPath, body, 0o644); err != nil {
								t.Fatal(err)
							}
						}
					})
				}
			},
			wantStatus: "failed",
			wantDetail: "declared absorbed-OS source entrypoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := installServedOrientationFixture(t)
			if tt.mutate != nil {
				tt.mutate(t, fixture)
			}

			doctor, err := OSAbsorptionDoctor(fixture.compoundHome)
			if err != nil {
				t.Fatalf("doctor: %v", err)
			}
			for _, id := range []string{"os_source_served_orientation_primary", "os_source_served_orientation_secondary"} {
				got, ok := findOSAbsorptionDoctorRow(doctor.Rows, id)
				if !ok {
					t.Fatalf("doctor row %s missing: %+v", id, doctor.Rows)
				}
				if got.ProofStatus != tt.wantStatus {
					t.Fatalf("%s proof_status=%q, want %q: %+v", id, got.ProofStatus, tt.wantStatus, got)
				}
				if tt.wantStatus == "ok" {
					for _, want := range []string{
						testServedOrientationProofMode,
						"context_pack=" + fixture.contextPackID,
						"codex_sha256=",
						"claude_sha256=",
					} {
						if !strings.Contains(got.ProofRef, want) {
							t.Fatalf("%s proof_ref %q missing %q", id, got.ProofRef, want)
						}
					}
					continue
				}
				if !strings.Contains(got.ProofDetail, tt.wantDetail) {
					t.Fatalf("%s proof_detail %q missing %q", id, got.ProofDetail, tt.wantDetail)
				}
			}
		})
	}
}

type servedOrientationFixture struct {
	compoundHome    string
	codexPath       string
	claudePath      string
	liveStartPath   string
	contextPackID   string
	contextPackPath string
}

func installServedOrientationFixture(t *testing.T) servedOrientationFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	compoundHome := filepath.Join(root, "compound-home")
	codexPath := filepath.Join(root, ".codex", "AGENTS.md")
	claudePath := filepath.Join(root, ".claude", "CLAUDE.md")
	contextPackID := "ctx_served_orientation"
	contextPackPath := filepath.Join(compoundHome, "context", "packs", contextPackID+".json")
	absorbedOSRoot := filepath.Join(root, "example-os")
	liveStart := filepath.Join(absorbedOSRoot, "LIVE_START.md")
	wholeMap := filepath.Join(absorbedOSRoot, "universe", "WHOLE_SYSTEM_MAP.md")
	if err := os.MkdirAll(filepath.Dir(wholeMap), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{liveStart, wholeMap} {
		if err := os.WriteFile(path, []byte("# synthetic absorbed-OS source entrypoint\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seedAbsorbedOSRegistry(t, compoundHome, absorbedOSRoot)

	for _, entrypoint := range []struct {
		harness string
		path    string
	}{
		{harness: "codex", path: codexPath},
		{harness: "claude", path: claudePath},
	} {
		if err := os.MkdirAll(filepath.Dir(entrypoint.path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := strings.Join([]string{
			"<!-- GENERATED from an absorbed-OS steering config; generator stamp test -->",
			"## Orient first — Compound (the served contract)",
			"Run `compound orient --harness " + entrypoint.harness + " --task \"<task>\"`.",
			"Compound owns durable semantic meaning and promotion.",
			"brain is source/projection/migration evidence.",
			"gbrain is a bridge/index only.",
			"These files remain source-in-place evidence and rollback:",
			liveStart,
			wholeMap,
			"If `compound orient` is unavailable or fails, use the rollback path.",
			"",
		}, "\n")
		if err := os.WriteFile(entrypoint.path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeServedOrientationContextPack(t, contextPackPath, contextPackID)
	matrixPath := filepath.Join(root, "matrix.json")
	rows := make([]OSAbsorptionMatrixRow, 0, 2)
	for _, source := range []struct {
		id  string
		ref string
	}{
		{id: "os_source_served_orientation_primary", ref: liveStart},
		{id: "os_source_served_orientation_secondary", ref: wholeMap},
	} {
		r := row(source.id, source.ref, "absorbed", "compound://contracts/effective-contract", "compound orient and served client contract", testServedOrientationProofMode)
		r.CurrentRole = "source_in_place_evidence"
		r.EvidenceRefs = []string{
			"served_entrypoint:codex:" + codexPath,
			"served_entrypoint:claude:" + claudePath,
			"context_pack:" + contextPackID,
		}
		rows = append(rows, r)
	}
	writeJSONFixture(t, matrixPath, OSAbsorptionSourceMatrix{
		SchemaVersion: OSAbsorptionMatrixSchema,
		Rows:          rows,
	})
	if _, err := ImportOSAbsorptionMatrix(compoundHome, matrixPath, time.Date(2026, 7, 12, 17, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("import matrix: %v", err)
	}
	return servedOrientationFixture{
		compoundHome:    compoundHome,
		codexPath:       codexPath,
		claudePath:      claudePath,
		liveStartPath:   liveStart,
		contextPackID:   contextPackID,
		contextPackPath: contextPackPath,
	}
}

func validServedOrientationLines(harness, sourceRef string) []string {
	return []string{
		"## Orient first — Compound (the served contract)",
		"compound orient --harness " + harness + " --task \"<task>\"",
		osAbsorptionRequiredMeaningAuthority,
		osAbsorptionRequiredBrainRole,
		osAbsorptionRequiredGBrainRole,
		"source-in-place evidence",
		"rollback",
		sourceRef,
	}
}

func copyServedOrientationFixtureFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceServedEntrypointEvidence(refs []string, harness, path string) []string {
	got := append([]string(nil), refs...)
	prefix := osAbsorptionServedEntrypointEvidenceRef + harness + ":"
	for i, ref := range got {
		if strings.HasPrefix(ref, prefix) {
			got[i] = prefix + path
		}
	}
	return got
}

func writeServedOrientationContextPack(t *testing.T, path, id string) {
	t.Helper()
	refs := []struct {
		ref         string
		sourceClass string
	}{
		{ref: "control-plane://orientation-map", sourceClass: "source"},
		{ref: "control-plane://workflow-contract", sourceClass: "source"},
		{ref: "control-plane://sop-packets", sourceClass: "source"},
		{ref: "memory://relevance-gated", sourceClass: "generated"},
	}
	included := make([]envelope.IncludedRef, 0, len(refs))
	revisions := make([]envelope.SourceRev, 0, len(refs))
	for _, ref := range refs {
		included = append(included, envelope.IncludedRef{
			Ref:         ref.ref,
			SourceClass: ref.sourceClass,
			Reason:      "served orientation fixture",
			Confidence:  1,
			Status:      "current",
		})
		revisions = append(revisions, envelope.SourceRev{Source: ref.ref, Rev: "sha256:" + digest12(ref.ref)})
	}
	pack := envelope.ContextPack{
		Header: envelope.Header{
			SchemaVersion: envelope.SVContextPack,
			ID:            id,
			CreatedAt:     "2026-07-12T17:00:00Z",
			Producer:      envelope.Producer{Kind: "test", Name: "served-orientation"},
			IDs:           envelope.IDs{ContractID: "contract_served_orientation"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		SummaryMD:          "served orientation fixture",
		TaskContract:       envelope.TaskContract{Intent: "prove served orientation"},
		Included:           included,
		Excluded:           []envelope.ExcludedRef{{Ref: "repo://unrelated", Reason: "out of scope"}},
		ActiveInstructions: []string{"control-plane://orientation-map"},
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		SourceRevisions:    revisions,
		RequiredChecks:     []string{},
		Avoid:              []string{},
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("fixture ContextPack invalid: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFixture(t, path, pack)
}

func findOSAbsorptionDoctorRow(rows []OSAbsorptionDoctorRow, id string) (OSAbsorptionDoctorRow, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return OSAbsorptionDoctorRow{}, false
}
