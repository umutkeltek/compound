package routeevidence

import (
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRouteComparisonInlineReviewsAreUnverified(t *testing.T) {
	input := RouteComparisonInput{
		DecisionID:     "decision_unverified",
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		Cases: []RouteComparisonCaseInput{{CaseID: "case-one",
			Incumbent: []RouteOutcomeReview{comparisonCase(t, "inline-inc", "model-a", "request-inc", "completed", true, 100, 50, 10)},
			Candidate: []RouteOutcomeReview{comparisonCase(t, "inline-cand", "model-b", "request-cand", "completed", true, 100, 40, 10)},
		}},
		MinRepetitions: 1,
	}
	got := BuildRouteComparison(input)
	if got.Assessment.RecommendReplacement {
		t.Fatal("caller-written review IDs and outcomes must remain unverified; they cannot recommend replacement")
	}
}

func comparisonOwnedFixture(t *testing.T, mutate func(*Evidence, *envelope.Receipt)) (string, RouteComparisonInput) {
	t.Helper()
	homePath := t.TempDir()
	contract := RouteComparisonContract{SchemaVersion: RouteComparisonContractSchemaVersion, MinRepetitions: 2,
		Cases: []RouteComparisonContractCase{
			{CaseID: "alpha", InputDigest: strings.Repeat("a", 64), AcceptanceDigest: strings.Repeat("b", 64), Critical: true},
			{CaseID: "beta", InputDigest: strings.Repeat("c", 64), AcceptanceDigest: strings.Repeat("d", 64)},
		}}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(contract)
	ref, err := artifacts.Put(body)
	if err != nil {
		t.Fatal(err)
	}
	input := RouteComparisonInput{DecisionID: "decision_owned", EvaluationContractRef: ref,
		IncumbentRoute: Target{Provider: "fixture-provider", Model: "model-a", Protocol: "mcp"},
		CandidateRoute: Target{Provider: "fixture-provider", Model: "model-b", Protocol: "mcp"},
		MinRepetitions: 2, CriticalCases: []string{"alpha"}}
	events, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range contract.Cases {
		row := RouteComparisonCaseInput{CaseID: c.CaseID}
		for _, side := range []string{"a", "b"} {
			for i := range 2 {
				suffix := fmt.Sprintf("%s_%s_%d", c.CaseID, side, i)
				ev, err := Build(BuildInput{CreatedAt: t0, RequestID: "request_" + suffix, WorkID: "work_" + suffix, ClientProfile: "fixture",
					Protocol: "mcp", Provider: "fixture-provider", Model: "model-" + side, Status: "completed",
					Usage: Usage{TotalTokens: 100, PromptTokens: 100}, CostMicros: 50, LatencyMS: 10,
					EvidenceRefs: []string{ref, RouteComparisonCaseRef(c)}})
				if err != nil {
					t.Fatal(err)
				}
				rec := fixtureReceipt("completed", true)
				rec.ID = "rec_" + suffix
				rec.IDs.WorkID = ev.WorkID
				rec.ProofRefs = []string{ev.ID, ref, RouteComparisonCaseRef(c)}
				if mutate != nil {
					mutate(&ev, &rec)
				}
				if err := rec.Validate(); err != nil {
					t.Fatal(err)
				}
				if ev.RecordedVia == RecorderExternalImport {
					fixturePath := filepath.Join(t.TempDir(), "external.json")
					data, _ := json.Marshal(ev)
					if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
						t.Fatal(err)
					}
					if _, err := ImportFile(homePath, fixturePath); err != nil {
						t.Fatal(err)
					}
				} else if err := RecordEvidence(homePath, ev); err != nil {
					t.Fatal(err)
				}
				if _, err := events.PutJSONExclusive(rec.ID, rec, "  "); err != nil {
					t.Fatal(err)
				}
				review, err := BuildRouteOutcomeReview(ev, rec, "", t0)
				if err != nil {
					t.Fatal(err)
				}
				if side == "a" {
					row.Incumbent = append(row.Incumbent, review)
				} else {
					row.Candidate = append(row.Candidate, review)
				}
			}
		}
		input.Cases = append(input.Cases, row)
	}
	return homePath, input
}

func TestRouteComparisonOwnedContractRecommends(t *testing.T) {
	h, input := comparisonOwnedFixture(t, nil)
	resolved := ResolveRouteComparisonInput(h, input)
	got := BuildRouteComparison(resolved)
	if !got.Assessment.RecommendReplacement || got.Assessment.EvidenceStatus != "owned_records_bound" {
		t.Fatalf("owned bound positive failed: %+v", got.Assessment)
	}
	// Population order is not its identity, and real repetitions have different requests.
	input.Cases[0], input.Cases[1] = input.Cases[1], input.Cases[0]
	if got := BuildRouteComparison(ResolveRouteComparisonInput(h, input)); !got.Assessment.RecommendReplacement {
		t.Fatalf("reordered input: %+v", got.Assessment)
	}
	if got.EvaluationContractRef != input.EvaluationContractRef {
		t.Fatal("contract ref lost from result")
	}
}

func TestRouteComparisonOwnedBindingsRejectSubstitution(t *testing.T) {
	tests := []struct {
		name, want string
		change     func(string, *RouteComparisonInput)
	}{
		{"inline facts", "disagrees", func(_ string, in *RouteComparisonInput) { in.Cases[0].Candidate[0].CostMicros++ }},
		{"unresolved review", "cannot be resolved", func(_ string, in *RouteComparisonInput) { in.Cases[0].Candidate[0].RouteEvidenceID = "route_missing" }},
		{"wrong route", "selected route", func(_ string, in *RouteComparisonInput) { in.CandidateRoute.Model = "model-other" }},
		{"omitted noncritical", "required contract case beta", func(_ string, in *RouteComparisonInput) { in.Cases = in.Cases[:1] }},
		{"extra case", "outside", func(_ string, in *RouteComparisonInput) {
			row := in.Cases[0]
			row.CaseID = "extra"
			in.Cases = append(in.Cases, row)
		}},
		{"duplicate case", "duplicate case", func(_ string, in *RouteComparisonInput) { in.Cases = append(in.Cases, in.Cases[0]) }},
		{"critical downgrade", "critical case declaration", func(_ string, in *RouteComparisonInput) { in.CriticalCases = []string{} }},
		{"repetition downgrade", "minimum repetitions", func(_ string, in *RouteComparisonInput) { in.MinRepetitions = 1 }},
		{"copied execution", "distinct", func(_ string, in *RouteComparisonInput) { in.Cases[0].Candidate[1] = in.Cases[0].Candidate[0] }},
		{"case relabel", "both owned records", func(_ string, in *RouteComparisonInput) {
			in.Cases[0].Incumbent, in.Cases[1].Incumbent = in.Cases[1].Incumbent, in.Cases[0].Incumbent
		}},
		{"changed input", "both owned records", func(h string, in *RouteComparisonInput) {
			store, err := home.NewArtifactStore(h)
			if err != nil {
				t.Fatal(err)
			}
			body, err := store.Get(in.EvaluationContractRef)
			if err != nil {
				t.Fatal(err)
			}
			var c RouteComparisonContract
			if err := json.Unmarshal(body, &c); err != nil {
				t.Fatal(err)
			}
			c.Cases[0].InputDigest = strings.Repeat("e", 64)
			body, _ = json.Marshal(c)
			in.EvaluationContractRef, err = store.Put(body)
			if err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, input := comparisonOwnedFixture(t, nil)
			tc.change(h, &input)
			got := BuildRouteComparison(ResolveRouteComparisonInput(h, input))
			if got.Assessment.RecommendReplacement || !containsAnyString(got.Assessment.BlockingReasons, tc.want) {
				t.Fatalf("wanted %q blocker: %+v", tc.want, got.Assessment)
			}
		})
	}
}

func TestRouteComparisonRequiresBothRecordsBindingsAndCrosslink(t *testing.T) {
	for _, name := range []string{"route binding", "receipt binding", "crosslink", "external import", "failed route"} {
		t.Run(name, func(t *testing.T) {
			h, input := comparisonOwnedFixture(t, func(ev *Evidence, rec *envelope.Receipt) {
				switch name {
				case "route binding":
					ev.EvidenceRefs = nil
				case "receipt binding":
					rec.ProofRefs = []string{ev.ID}
				case "crosslink":
					rec.ProofRefs = rec.ProofRefs[1:]
				case "external import":
					ev.RecordedVia = RecorderExternalImport
				case "failed route":
					ev.Status = "failed"
				}
			})
			got := BuildRouteComparison(ResolveRouteComparisonInput(h, input))
			if got.Assessment.RecommendReplacement {
				t.Fatalf("missing %s supported replacement", name)
			}
		})
	}
}

func TestRouteComparisonResolvedInputsCannotBeChanged(t *testing.T) {
	h, input := comparisonOwnedFixture(t, nil)
	resolved := ResolveRouteComparisonInput(h, input)
	resolved.Cases[0].Candidate[0].CostMicros--
	got := BuildRouteComparison(resolved)
	if got.Assessment.RecommendReplacement || !containsAnyString(got.Assessment.BlockingReasons, "changed after validation") {
		t.Fatalf("stale verification: %+v", got.Assessment)
	}
}

func TestRouteComparisonMissingContractReadDoesNotCreateHomeState(t *testing.T) {
	h := t.TempDir()
	in := RouteComparisonInput{EvaluationContractRef: "artifact:sha256:" + strings.Repeat("a", 64)}
	got := BuildRouteComparison(ResolveRouteComparisonInput(h, in))
	if got.Assessment.RecommendReplacement || got.Assessment.EvidenceStatus != "unverified" {
		t.Fatal("missing contract became verified")
	}
	entries, err := os.ReadDir(h)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read created home state: %v %v", entries, err)
	}
}

func TestRouteComparisonRejectsCallerVerificationFlag(t *testing.T) {
	p := filepath.Join(t.TempDir(), "input.json")
	body := []byte(`{"schema_version":"compound.route_comparison_input.v0.1","decision_id":"decision_fixture","incumbent_route":{"provider":"fixture","model":"a","protocol":"mcp"},"candidate_route":{"provider":"fixture","model":"b","protocol":"mcp"},"verified":true}`)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRouteComparisonInput(p); err == nil {
		t.Fatal("caller verification flag was silently accepted")
	}
}

func TestRecordComparisonPreservesWriterErrorWithVisibleBytes(t *testing.T) {
	h := t.TempDir()
	comparison := routeComparisonRevision(t, "completed", true)
	calls := 0
	writer := func(h, p, k string, b []byte, m os.FileMode) error {
		calls++
		if err := home.WriteRootedImmutableFileDurable(h, p, k, b, m); err != nil {
			return err
		}
		return syscall.EIO
	}
	for range 2 {
		if _, err := recordRouteComparisonWithWriter(h, comparison, writer); !errors.Is(err, syscall.EIO) {
			t.Fatalf("visible bytes hid writer error: %v", err)
		}
	}
	if calls != 2 {
		t.Fatal("identical replay bypassed durability writer")
	}
	p, err := RecordRouteComparison(h, comparison)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordRouteComparison(h, comparison); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("stable replay replaced original revision")
	}
}
