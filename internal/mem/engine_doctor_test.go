package mem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	report, err := s.EngineDoctor("ledger", 10, mutatingEngine{s: s})
	if err == nil || !strings.HasPrefix(err.Error(), "rented engine mutated owned ledger") {
		t.Fatalf("expected ledger mutation failure, got %v", err)
	}
	// The invariant must stay red AND name the offending ledger path the engine wrote.
	if !strings.Contains(err.Error(), "engine-owned-view") {
		t.Fatalf("engine mutation must name the offending ledger path: %v", err)
	}
	if len(report.EngineViolations) == 0 || !strings.Contains(strings.Join(report.EngineViolations, "; "), "engine-owned-view") {
		t.Fatalf("expected report.EngineViolations to name the engine-written path: %+v", report)
	}
}

// TestEngineDoctorFailsWhenAppendedRowLacksProvenance proves the retry does NOT weaken
// the invariant: a claim row appended to the ledger WITHOUT producer provenance (empty
// derivation) is engine-attributed, not a legitimate governed write, so the hop stays red
// and names the row.
func TestEngineDoctorFailsWhenAppendedRowLacksProvenance(t *testing.T) {
	s := storeWith(t, "system:compound", "owner", "ledger")
	report, err := s.EngineDoctor("ledger", 10, &provenancelessWriterEngine{store: s})
	if err == nil || !strings.HasPrefix(err.Error(), "rented engine mutated owned ledger") {
		t.Fatalf("a provenance-less appended row must stay a hard failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "without producer provenance") {
		t.Fatalf("failure must name the missing-provenance row: %v", err)
	}
	if len(report.EngineViolations) == 0 {
		t.Fatalf("expected an engine violation for the provenance-less row: %+v", report)
	}
}

// TestEngineDoctorDoesNotBlameEngineForConcurrentGovernedWrite is the real-world
// falsifier: a SEPARATE compound process appends a governed claim to the owned ledger
// while the doctor's breadth query runs, and the rented engine never re-indexes to the
// new generation mid-query (the realistic shape the pre-fix retry condition missed).
// The doctor must attribute the delta to a legitimate producer, retry once against the
// fresh gen, and pass with an INFO note — never blame the rented engine.
func TestEngineDoctorDoesNotBlameEngineForConcurrentGovernedWrite(t *testing.T) {
	s := storeWith(t, "system:compound", "owner", "ledger")
	eng := &externalWriterEngine{store: s}
	report, err := s.EngineDoctor("ledger", 10, eng)
	if err != nil {
		t.Fatalf("concurrent governed write must not be blamed on the rented engine: %v; report=%+v", err, report)
	}
	if !report.LedgerUnchanged || report.RetryCount != 1 || !report.OwnerLedgerAdvanced {
		t.Fatalf("expected one stable-snapshot retry after a concurrent governed write: %+v", report)
	}
	if report.AdvancedRows < 1 {
		t.Fatalf("expected advanced-row accounting for the concurrent governed write: %+v", report)
	}
	if !strings.Contains(report.Note, "concurrent legit") || !strings.Contains(report.Note, "fresh gen") {
		t.Fatalf("expected an INFO note naming concurrent legit writers verified against a fresh gen: %+v", report)
	}
	if len(report.EngineViolations) != 0 {
		t.Fatalf("a legit concurrent write must produce no engine violations: %+v", report)
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

// externalWriterEngine models the REAL concurrent-write shape: a separate compound
// process (NOT the rented engine) appends a governed claim to the owned ledger while the
// doctor's breadth query runs. Unlike ownerAdvancingEngine, it deliberately does NOT
// re-index itself to the new generation — the writer is a different actor, so the engine
// stays at the pre-query gen. This is exactly the case the old
// `report.Engine.CorpusGen == afterGen` retry gate could never fire for.
type externalWriterEngine struct {
	store *Store
	base  LexicalEngine
	wrote bool
}

func (e *externalWriterEngine) Capabilities() EngineCapabilities {
	caps := e.base.Capabilities()
	caps.Name = "external-writer-fixture"
	return caps
}

func (e *externalWriterEngine) Index(claims []Claim, gen int64) error {
	return e.base.Index(claims, gen)
}

func (e *externalWriterEngine) Query(text, gate string, allowlist []string, k int) ([]Hit, error) {
	if !e.wrote {
		e.wrote = true
		err := e.store.withLock(func() error {
			seq, err := e.store.nextSeq()
			if err != nil {
				return err
			}
			return appendJSONL(e.store.claimsLog(), Claim{
				ID: "claim_external_legit", Seq: seq, Entity: "other-session", Predicate: "closed",
				Object: "governed concurrent write", ValidFrom: t0.Format("2006-01-02T15:04:05Z07:00"),
				Confidence: 1, Derivation: "deterministic", Visibility: "operational",
			})
		})
		if err != nil {
			return nil, err
		}
		// Deliberately NOT re-indexing the base engine to the new gen: the external writer
		// is a different actor; the rented engine remains at the pre-query generation.
	}
	return e.base.Query(text, gate, allowlist, k)
}

// provenancelessWriterEngine appends a claim row that skips the governed door's producer
// provenance (empty derivation). It stands in for a rented engine masquerading a write as
// a legit concurrent one — the doctor must still refuse it.
type provenancelessWriterEngine struct {
	store *Store
	base  LexicalEngine
	wrote bool
}

func (e *provenancelessWriterEngine) Capabilities() EngineCapabilities {
	caps := e.base.Capabilities()
	caps.Name = "provenanceless-writer-fixture"
	return caps
}

func (e *provenancelessWriterEngine) Index(claims []Claim, gen int64) error {
	return e.base.Index(claims, gen)
}

func (e *provenancelessWriterEngine) Query(text, gate string, allowlist []string, k int) ([]Hit, error) {
	if !e.wrote {
		e.wrote = true
		err := e.store.withLock(func() error {
			seq, err := e.store.nextSeq()
			if err != nil {
				return err
			}
			return appendJSONL(e.store.claimsLog(), Claim{
				ID: "claim_no_provenance", Seq: seq, Entity: "engine", Predicate: "wrote",
				Object: "row without provenance", ValidFrom: t0.Format("2006-01-02T15:04:05Z07:00"),
				Confidence: 1, Derivation: "", Visibility: "operational",
			})
		})
		if err != nil {
			return nil, err
		}
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
