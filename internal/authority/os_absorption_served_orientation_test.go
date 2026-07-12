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
			for _, id := range []string{"os_source_umut_live_start", "os_source_umut_whole_system_map"} {
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
	contextPackID   string
	contextPackPath string
}

func installServedOrientationFixture(t *testing.T) servedOrientationFixture {
	t.Helper()
	root := t.TempDir()
	compoundHome := filepath.Join(root, "compound-home")
	codexPath := filepath.Join(root, "codex", "AGENTS.md")
	claudePath := filepath.Join(root, "claude", "CLAUDE.md")
	contextPackID := "ctx_served_orientation"
	contextPackPath := filepath.Join(compoundHome, "context", "packs", contextPackID+".json")
	liveStart := filepath.Join(root, "umut-os", "LIVE_START.md")
	wholeMap := filepath.Join(root, "umut-os", "universe", "WHOLE_SYSTEM_MAP.md")

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
			"<!-- GENERATED from operating-system/umut-os/spine/steering.config.ts; generator stamp test -->",
			"## Orient first — Compound (the served contract)",
			"Run `compound orient --harness " + entrypoint.harness + " --task \"<task>\"`.",
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
		{id: "os_source_umut_live_start", ref: liveStart},
		{id: "os_source_umut_whole_system_map", ref: wholeMap},
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
		contextPackID:   contextPackID,
		contextPackPath: contextPackPath,
	}
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
