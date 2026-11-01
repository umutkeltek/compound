package contract

import (
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

func TestOperationalBranchSnapshotNeverEntersAmbient(t *testing.T) {
	for _, tc := range []struct {
		name           string
		task           string
		repoKind       string
		semanticBranch bool
	}{
		{"lexical", "Inspect fixture mixed consumer guidance", "control-plane", false},
		{"highest-semantic-rank", "Summarize a workflow", "product", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			store, err := mem.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			seeds := []mem.Candidate{
				{
					Entity: "repo:fixture-mixed", Predicate: "current_branch", Object: "stale-feature-branch",
					Confidence: 1, Derivation: "deterministic", ClaimType: "status",
					Qualifiers:     map[string]string{"domain": "current_branch"},
					PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: "ev_branch_snapshot",
					SnippetHash: "sha256:branch-snapshot", Visibility: "operational",
					ObservationDate: fixedNow.Format(time.RFC3339),
				},
				{
					Entity: "CapabilityGraph", Predicate: "surfacing_feedback_status", Object: "synthetic consumer guidance",
					Confidence: 1, Derivation: "deterministic", ClaimType: "status",
					Qualifiers:     map[string]string{"domain": "capability-surfacing"},
					PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: "ev_branch_control",
					SnippetHash: "sha256:branch-control", Visibility: "operational",
					ObservationDate: fixedNow.Format(time.RFC3339),
				},
			}
			captured, err := store.Capture(seeds, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			claims := map[string]mem.Claim{}
			for i, candidate := range captured {
				result, err := store.Adjudicate(candidate.ID, "promote", "test", fixedNow.Add(time.Duration(i)*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				if result.Claim == nil {
					t.Fatal("fixture was not promoted")
				}
				claims[result.Claim.Entity] = *result.Claim
			}
			branch, control := claims["repo:fixture-mixed"], claims["CapabilityGraph"]
			if branch.ID == "" || control.ID == "" {
				t.Fatal("missing fixture claim")
			}
			hits := []mem.Hit{{ClaimID: control.ID, Score: 0.8}}
			if tc.semanticBranch {
				hits = append(hits, mem.Hit{ClaimID: branch.ID, Score: 0.99})
			}
			fake := &fakeBreadthEngine{hits: hits}
			original := breadthEngineFactory
			breadthEngineFactory = func(string) (mem.Engine, func()) { return fake, func() {} }
			t.Cleanup(func() { breadthEngineFactory = original })
			t.Setenv("COMPOUND_MEM_ENGINE_BIN", "fake-sidecar")
			in := Input{
				Harness: "codex", Cwd: "/repo/fixture-current", Task: tc.task,
				RepoKind: tc.repoKind, RepoKindVia: "flag", Mode: "review",
			}
			pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			branchRef, controlRef := "claim://"+branch.ID, "claim://"+control.ID
			if !contains(pack.ActiveMemory, controlRef) {
				t.Error("useful operational control was lost")
			}
			if contains(pack.ActiveMemory, branchRef) || hasIncluded(pack, branchRef) {
				t.Error("historical branch snapshot entered ambient claims")
			}
			for _, position := range pack.ActivePositions {
				if position.Entity == branch.Entity {
					t.Errorf("historical branch snapshot entered ambient positions: %+v", position)
				}
			}
			if strings.Contains(pack.SummaryMD, branch.Object) {
				t.Error("historical branch snapshot entered the summary")
			}
			effective, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if contains(effective.ActiveMemory, branchRef) {
				t.Error("historical branch snapshot entered the effective contract")
			}
			if !contains(effective.ActiveMemory, controlRef) {
				t.Error("effective contract lost control")
			}

			// Ambient suppression must not delete or hide explicit historical evidence.
			recall, err := store.Recall(branch.Entity, mem.RecallOpts{AsOfSeq: branch.Seq})
			if err != nil {
				t.Fatal(err)
			}
			if len(recall.Claims) != 1 || recall.Claims[0].ID != branch.ID || recall.Claims[0].Object != branch.Object {
				t.Fatalf("explicit historical recall lost branch evidence: %+v", recall)
			}
		})
	}
}
