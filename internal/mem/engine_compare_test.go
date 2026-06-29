package mem

import "testing"

func TestEngineCompareMatchesProtocolSidecar(t *testing.T) {
	s := storeWith(t, "project:api", "backend_language", "node", "go")
	eng := NewProtocolEngine(&fakeTransport{embedder: "reference:test:0:0", failAfter: -1}, "reference:test:0:0")

	report, err := s.EngineCompare("backend go", 10, eng)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match || !report.LedgerUnchanged {
		t.Fatalf("engine compare should match: %+v", report)
	}
	if report.Reference.Engine.Name != "lexical" || report.Candidate.Engine.Name != "fake" {
		t.Fatalf("engine names = ref %q candidate %q", report.Reference.Engine.Name, report.Candidate.Engine.Name)
	}
	if len(report.ReferenceClaimIDs) != 1 || len(report.CandidateClaimIDs) != 1 || report.ReferenceClaimIDs[0] != report.CandidateClaimIDs[0] {
		t.Fatalf("claim ids ref=%v candidate=%v", report.ReferenceClaimIDs, report.CandidateClaimIDs)
	}
}

func TestEngineCompareReportsDivergentCandidate(t *testing.T) {
	s := storeWith(t, "project:api", "backend_language", "node", "go")
	report, err := s.EngineCompare("backend go", 10, emptyEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Match {
		t.Fatalf("divergent engine should not match: %+v", report)
	}
	if len(report.MissingClaimIDs) != 1 || len(report.ExtraClaimIDs) != 0 {
		t.Fatalf("missing/extra = %v/%v", report.MissingClaimIDs, report.ExtraClaimIDs)
	}
	if !report.LedgerUnchanged {
		t.Fatalf("ledger should remain unchanged: %+v", report)
	}
}

type emptyEngine struct{}

func (emptyEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{Name: "empty", Breadth: true}
}

func (emptyEngine) Index([]Claim, int64) error {
	return nil
}

func (emptyEngine) Query(string, string, []string, int) ([]Hit, error) {
	return nil, nil
}
