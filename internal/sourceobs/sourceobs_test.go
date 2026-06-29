package sourceobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/harness"
	"compound/internal/projection"
	"compound/internal/tooltopology"
)

var tSource = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func TestApplyGeneratedViewObservationWritesCandidateAndNoPromote(t *testing.T) {
	home := t.TempDir()
	obs := validObservation()

	report, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.CandidateIDs) != 1 || len(report.NoPromoteIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}

	candidatePath := filepath.Join(home, candidates.InboxDir(candidates.CapabilityObs), report.CandidateIDs[0]+".json")
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	var c CandidateRecord
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Kind != string(candidates.CapabilityObs) || c.Verdict != candidates.NoPromote {
		t.Fatalf("candidate kind/verdict = %q/%q", c.Kind, c.Verdict)
	}
	if c.SourceObservationID != obs.ID || c.PromoteAllowed {
		t.Fatalf("candidate source/promote = %q/%v", c.SourceObservationID, c.PromoteAllowed)
	}
	if !hasString(c.EvidenceRefs, "source:fixtures/generated/skill-manifest.json#sha256:abc123") {
		t.Fatalf("candidate evidence refs = %v", c.EvidenceRefs)
	}

	nopRows := readNoPromoteRows(t, home)
	if len(nopRows) != 1 {
		t.Fatalf("no-promote rows = %d, want 1", len(nopRows))
	}
	if nopRows[0].ID != report.NoPromoteIDs[0] || nopRows[0].CandidateRef != c.ID {
		t.Fatalf("no-promote row = %+v", nopRows[0])
	}
	if nopRows[0].OwnerTarget.Kind != "source_observation" || nopRows[0].OwnerTarget.Ref != obs.ID {
		t.Fatalf("owner target = %+v", nopRows[0].OwnerTarget)
	}

	replay, err := Apply(home, obs, tSource.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replay.NoPromoteIDs[0] != report.NoPromoteIDs[0] {
		t.Fatalf("replay no-promote id = %q, want %q", replay.NoPromoteIDs[0], report.NoPromoteIDs[0])
	}
	if rows := readNoPromoteRows(t, home); len(rows) != 1 {
		t.Fatalf("replay appended duplicate no-promote rows: %d", len(rows))
	}
}

func TestProjectionFreshnessObservationWritesMethodUpgradeNoPromote(t *testing.T) {
	home := t.TempDir()
	freshness := projection.Freshness{
		ProjectionID:    "projection:test-dashboard",
		ViewRef:         "generated-dashboard.md",
		SourceLedgerSeq: 8,
		BuiltAt:         "2026-06-29T00:00:00Z",
		CoverageVerdict: "partial",
		Status:          projection.StatusStale,
		OutputHash:      "sha256:view",
		Staleness:       "241h0m0s",
		Sources:         []projection.SourceObservation{{SourceRef: "source-ledger.jsonl", CurrentHash: "sha256:source", Status: "ok"}},
		Checks:          []projection.Check{{Name: "coverage", Status: "fail", Detail: "partial"}},
	}

	obs := FromProjectionFreshness(freshness, tSource)
	if obs.AdapterID != "compound.projection" || obs.GeneratedView.Freshness != projection.StatusStale {
		t.Fatalf("unexpected projection observation: %+v", obs)
	}
	if len(obs.MapsTo) != 1 || obs.MapsTo[0].CandidateKind != string(candidates.MethodUpgrade) || obs.MapsTo[0].PromoteAllowed {
		t.Fatalf("unexpected map target: %+v", obs.MapsTo)
	}
	if !strings.Contains(obs.GeneratedView.DriftReason, "coverage:partial") {
		t.Fatalf("missing drift reason: %q", obs.GeneratedView.DriftReason)
	}

	report, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.CandidateIDs) != 1 || len(report.NoPromoteIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}
	candidatePath := filepath.Join(home, candidates.InboxDir(candidates.MethodUpgrade), report.CandidateIDs[0]+".json")
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	var c CandidateRecord
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Kind != string(candidates.MethodUpgrade) || c.Verdict != candidates.NoPromote || c.SourceObservationID != obs.ID {
		t.Fatalf("candidate = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("projection source observation should not promote claims, stat err=%v", err)
	}
}

func TestToolTopologyObservationWritesCapabilityNoPromote(t *testing.T) {
	home := t.TempDir()
	topo := tooltopology.Topology{
		SchemaVersion: tooltopology.SchemaVersion,
		ID:            "topo_fixture_resolver",
		AdapterID:     "fixture.tool-topology",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: tooltopology.SourceRef{
			Path:        "fixtures/generated/tool-resolver.json",
			Kind:        "generated_view",
			Owner:       "fixture-spine",
			SourceClass: "os-tool-resolver-json",
			Revision:    "sha256:def456",
		},
		Entries: []tooltopology.Entry{
			{
				ID:           "mcp:context7",
				Kind:         "mcp",
				Name:         "context7",
				Interface:    "mcp",
				Status:       "on_demand",
				Triggers:     []string{"docs"},
				EvidenceRefs: []string{"tool_resolver_json:sha256:def456"},
			},
		},
		EvidenceRefs: []string{"tool_resolver_json:sha256:def456"},
	}
	obs := FromToolTopology(topo, tSource)
	if obs.GeneratedView.ViewKind != "tool_topology" || obs.MapsTo[0].CandidateKind != string(candidates.CapabilityObs) {
		t.Fatalf("observation = %+v", obs)
	}
	report, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.CandidateIDs) != 1 || len(report.NoPromoteIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}
	candidatePath := filepath.Join(home, candidates.InboxDir(candidates.CapabilityObs), report.CandidateIDs[0]+".json")
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	var c CandidateRecord
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Verdict != candidates.NoPromote || c.Subject != topo.ID || c.SourceObservationID != obs.ID {
		t.Fatalf("candidate = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("tool topology source observation should not promote claims, stat err=%v", err)
	}
}

func TestSkillManifestObservationWritesCapabilityNoPromote(t *testing.T) {
	home := t.TempDir()
	manifest := capgraph.SkillManifest{
		SchemaVersion: capgraph.SkillManifestSchemaVersion,
		ID:            "skill_manifest_fixture",
		AdapterID:     "fixture.skill-manifest",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: capgraph.SkillSourceRef{
			Path:        "fixtures/skills/skill-manifest.yaml",
			Kind:        "generated_view",
			Owner:       "capability-fixture",
			SourceClass: "generated",
			Revision:    "rev_fixture",
		},
		Skills: []capgraph.SkillManifestEntry{
			{
				ID:           "skill:pricing",
				Name:         "Pricing",
				Status:       "active",
				Triggers:     []string{"pricing"},
				EvidenceRefs: []string{"artifact_sha256:skill-fixture"},
			},
		},
		EvidenceRefs: []string{"artifact_sha256:skill-fixture"},
	}
	obs := FromSkillManifest(manifest, tSource)
	if obs.GeneratedView.ViewKind != "skill_manifest" || obs.MapsTo[0].CandidateKind != string(candidates.CapabilityObs) {
		t.Fatalf("observation = %+v", obs)
	}
	report, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.CandidateIDs) != 1 || len(report.NoPromoteIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}
	candidatePath := filepath.Join(home, candidates.InboxDir(candidates.CapabilityObs), report.CandidateIDs[0]+".json")
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	var c CandidateRecord
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Verdict != candidates.NoPromote || c.Subject != manifest.ID || c.SourceObservationID != obs.ID {
		t.Fatalf("candidate = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skill manifest source observation should not promote claims, stat err=%v", err)
	}
}

func TestHarnessRegistryObservationWritesCapabilityNoPromote(t *testing.T) {
	home := t.TempDir()
	obs := FromHarnessRegistry(harness.DefaultRegistry(), tSource)
	if obs.GeneratedView.ViewKind != "harness_registry" || obs.MapsTo[0].CandidateKind != string(candidates.CapabilityObs) {
		t.Fatalf("observation = %+v", obs)
	}
	if obs.SourceRef.Kind != "registry" || obs.SourceRef.Revision == "" || len(obs.EvidenceRefs) == 0 {
		t.Fatalf("source ref/evidence = %+v refs=%v", obs.SourceRef, obs.EvidenceRefs)
	}
	report, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.CandidateIDs) != 1 || len(report.NoPromoteIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}
	candidatePath := filepath.Join(home, candidates.InboxDir(candidates.CapabilityObs), report.CandidateIDs[0]+".json")
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	var c CandidateRecord
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Verdict != candidates.NoPromote || c.Subject != "harness_registry:default" || c.SourceObservationID != obs.ID {
		t.Fatalf("candidate = %+v", c)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("harness registry source observation should not promote claims, stat err=%v", err)
	}
}

func TestValidateRejectsUnknownCandidateKind(t *testing.T) {
	obs := validObservation()
	obs.MapsTo[0].CandidateKind = "../../bad"
	report := Validate(obs)
	if report.Status != "fail" {
		t.Fatalf("status = %q, want fail", report.Status)
	}
}

func TestPromoteAllowedCandidateParksWithoutNoPromote(t *testing.T) {
	home := t.TempDir()
	obs := validObservation()
	obs.MapsTo[0].PromoteAllowed = true

	report, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.NoPromoteIDs) != 0 {
		t.Fatalf("promote-allowed map should not no-promote: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "no-promote-decisions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("unexpected no-promote ledger, err=%v", err)
	}
}

func validObservation() SourceObservation {
	return SourceObservation{
		SchemaVersion: SchemaVersion,
		ID:            "srcobs_fixture_skill_manifest",
		AdapterID:     "fixture.generated-views",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: SourceRef{
			Path:        "fixtures/generated/skill-manifest.json",
			Kind:        "generated_view",
			Owner:       "fixture-spine",
			SourceClass: "generated",
			Revision:    "sha256:abc123",
		},
		GeneratedView: GeneratedView{
			ViewKind:  "skill_manifest",
			Consumer:  "agent harnesses",
			Generator: "fixture-generate",
			Marker:    "generated",
			Freshness: "fresh",
		},
		MapsTo: []MapTarget{
			{
				ObjectFamily:   "capability",
				CandidateKind:  string(candidates.CapabilityObs),
				EntityHint:     "skill-manifest",
				PromoteAllowed: false,
			},
		},
		EvidenceRefs: []string{"artifact_sha256:abc123"},
	}
}

func readNoPromoteRows(t *testing.T, home string) []struct {
	ID           string `json:"id"`
	CandidateRef string `json:"candidate_ref"`
	OwnerTarget  struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
	} `json:"owner_target"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "ledger", "no-promote-decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var rows []struct {
		ID           string `json:"id"`
		CandidateRef string `json:"candidate_ref"`
		OwnerTarget  struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"owner_target"`
	}
	for _, line := range lines {
		var row struct {
			ID           string `json:"id"`
			CandidateRef string `json:"candidate_ref"`
			OwnerTarget  struct {
				Kind string `json:"kind"`
				Ref  string `json:"ref"`
			} `json:"owner_target"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse row %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
