package mem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineDoctorProvesLedgerUnchanged(t *testing.T) {
	s := storeWith(t, "product:focus", "positioning", "private capture")
	before, err := s.LedgerDigest()
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.EngineDoctor("private capture", 10, &LexicalEngine{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.LedgerDigest()
	if err != nil {
		t.Fatal(err)
	}
	if !report.LedgerUnchanged || report.LedgerHashBefore != before || report.LedgerHashAfter != after || before != after {
		t.Fatalf("ledger should be unchanged: before=%s after=%s report=%+v", before, after, report)
	}
	if report.Engine.Name != "lexical" || report.Engine.CorpusGen != report.LedgerGen {
		t.Fatalf("engine generation should match ledger generation: %+v", report)
	}
	if len(report.Hits) != 1 || report.Hits[0].Entity != "product:focus" {
		t.Fatalf("hits = %+v", report.Hits)
	}
}

func TestEngineDoctorDetectsEngineLedgerMutation(t *testing.T) {
	s := storeWith(t, "system:compound", "owner", "ledger")
	_, err := s.EngineDoctor("ledger", 10, mutatingEngine{s: s})
	if err == nil || err.Error() != "rented engine mutated owned ledger" {
		t.Fatalf("expected ledger mutation failure, got %v", err)
	}
}

func TestEngineDoctorDegradesExplicitlyWhenUnavailable(t *testing.T) {
	s := storeWith(t, "system:compound", "owner", "ledger")
	report, err := s.EngineDoctor("ledger", 10, unavailableEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Degraded || report.Error == "" || !report.LedgerUnchanged {
		t.Fatalf("expected explicit degraded unchanged report, got %+v", report)
	}
}

type mutatingEngine struct {
	s *Store
}

func (e mutatingEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{Name: "bad-ledger-writer", Breadth: true}
}

func (e mutatingEngine) Index([]Claim, int64) error {
	return os.WriteFile(filepath.Join(e.s.home, "ledger", "engine-owned-view"), []byte("bad"), 0o644)
}

func (e mutatingEngine) Query(string, string, []string, int) ([]Hit, error) {
	return nil, nil
}

type unavailableEngine struct{}

func (unavailableEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{Name: "down", Breadth: true}
}

func (unavailableEngine) Index([]Claim, int64) error {
	return ErrEngineUnavailable
}

func (unavailableEngine) Query(string, string, []string, int) ([]Hit, error) {
	return nil, errors.New("should not query after unavailable index")
}
