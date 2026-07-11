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

func TestEngineDoctorRetriesWhenOwnedLedgerAdvancesDuringProbe(t *testing.T) {
	s := storeWith(t, "system:compound", "owner", "ledger")
	eng := &ownerAdvancingEngine{store: s}
	report, err := s.EngineDoctor("ledger", 10, eng)
	if err != nil {
		t.Fatalf("owner advancement must not be blamed on rented engine: %v; report=%+v", err, report)
	}
	if !report.LedgerUnchanged || report.RetryCount != 1 || !report.OwnerLedgerAdvanced {
		t.Fatalf("expected one stable-snapshot retry after owner advancement: %+v", report)
	}
	if report.Engine.CorpusGen != report.LedgerGen || report.LedgerGenAfter != report.LedgerGen {
		t.Fatalf("stable report generations differ: %+v", report)
	}
}

func TestQueryBreadthRefusesEngineGenerationAheadOfOwnedLedger(t *testing.T) {
	s := storeWith(t, "system:compound", "owner", "ledger")
	eng := &aheadEngine{gen: s.currentGen() + 1}
	_, err := s.QueryBreadth("ledger", 10, eng)
	if !errors.Is(err, ErrEngineAhead) {
		t.Fatalf("expected engine-ahead refusal, got %v", err)
	}
	if eng.indexed {
		t.Fatal("owner boundary must not rebuild an ahead engine backwards")
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

type ownerAdvancingEngine struct {
	store    *Store
	base     LexicalEngine
	advanced bool
}

func (e *ownerAdvancingEngine) Capabilities() EngineCapabilities {
	caps := e.base.Capabilities()
	caps.Name = "owner-advancement-fixture"
	return caps
}

func (e *ownerAdvancingEngine) Index(claims []Claim, gen int64) error {
	return e.base.Index(claims, gen)
}

func (e *ownerAdvancingEngine) Query(text, gate string, allowlist []string, k int) ([]Hit, error) {
	if !e.advanced {
		e.advanced = true
		err := e.store.withLock(func() error {
			seq, err := e.store.nextSeq()
			if err != nil {
				return err
			}
			return appendJSONL(e.store.claimsLog(), Claim{
				ID: "claim_owner_advanced", Seq: seq, Entity: "owner", Predicate: "advanced",
				Object: "concurrent legitimate write", ValidFrom: t0.Format("2006-01-02T15:04:05Z07:00"),
				Confidence: 1, Derivation: "deterministic", Visibility: "operational",
			})
		})
		if err != nil {
			return nil, err
		}
		_ = e.base.Index(append(e.base.claims, Claim{ID: "claim_owner_advanced"}), e.store.currentGen())
	}
	return e.base.Query(text, gate, allowlist, k)
}

type aheadEngine struct {
	gen     int64
	indexed bool
}

func (e *aheadEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{Name: "ahead", Breadth: true, CorpusGen: e.gen}
}
func (e *aheadEngine) Index([]Claim, int64) error                         { e.indexed = true; return nil }
func (e *aheadEngine) Query(string, string, []string, int) ([]Hit, error) { return nil, nil }
