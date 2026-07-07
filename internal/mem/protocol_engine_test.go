package mem

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestMain lets the test binary re-exec itself AS the reference engine, so the held-coprocess
// transport can be proven end-to-end with a real process and zero Rust.
func TestMain(m *testing.M) {
	if os.Getenv("COMPOUND_MOCK_ENGINE") != "" {
		_ = RunReferenceEngine(os.Stdin, os.Stdout, os.Getenv("MOCK_EMBEDDER"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeTransport is an in-process, countable, controllable engine for the adapter unit tests
// (idempotency, gate, identity, unavailability) — no spawning, fully deterministic.
type fakeTransport struct {
	claims                 []ClaimText
	edges                  []WireEdge
	gen                    int64
	ready                  bool
	embedder               string
	schemaVersion          int // 0 = unstamped (tolerated); set != ProtocolVersion to test skew
	indexCalls, queryCalls int
	calls, failAfter       int // failAfter>=0: RoundTrip errors once calls exceed it
}

func (f *fakeTransport) RoundTrip(req Request) (Response, error) {
	f.calls++
	if f.failAfter >= 0 && f.calls > f.failAfter {
		return Response{}, errors.New("transport down")
	}
	switch req.Op {
	case OpCapabilities:
		return Response{OK: true, SchemaVersion: f.schemaVersion, Name: "fake", Breadth: true, Embedder: f.embedder, Ready: f.ready, CorpusGen: f.gen}, nil
	case OpIndex:
		f.indexCalls++
		if f.ready && req.CorpusGen == f.gen {
			return Response{OK: true, Indexed: 0, CorpusGen: f.gen}, nil
		}
		f.claims, f.edges, f.gen, f.ready = req.Claims, req.Edges, req.CorpusGen, true
		return Response{OK: true, Indexed: len(f.claims), CorpusGen: f.gen}, nil
	case OpQuery:
		f.queryCalls++
		return Response{OK: true, Hits: refMatch(f.claims, req.Text, req.Gate, req.Allowlist, req.K), CorpusGen: f.gen}, nil
	}
	return Response{OK: false, Error: &WireError{Code: CodeBadRequest, Message: "?"}}, nil
}
func (f *fakeTransport) Close() error { return nil }

func storeWith(t *testing.T, e, p string, objs ...string) *Store {
	s := newStore(t)
	for _, o := range objs {
		c, _ := s.Capture([]Candidate{cand(e, p, o, "")}, t0)
		s.Adjudicate(c[0].ID, "promote", "", t0)
	}
	return s
}

func TestProtocolIndexesLedgerOwnedContext(t *testing.T) {
	s := newStore(t)
	promote := func(c Candidate) Claim {
		t.Helper()
		got, err := s.Capture([]Candidate{c}, t0)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Adjudicate(got[0].ID, "promote", "", t0)
		if err != nil {
			t.Fatal(err)
		}
		return *res.Claim
	}

	appCand := cand("app", "runtime", "go", "")
	appCand.ClaimType = "architecture"
	appCand.Derivation = "deterministic"
	appCand.SourceTraceRef = "trace_app"
	app := promote(appCand)
	auth := promote(cand("auth", "runtime", "jwt", ""))
	db := promote(cand("db", "engine", "postgres", ""))
	s.Relate(app.Entity, "depends_on", auth.Entity, 0.9, t0)
	s.Relate(app.Entity, "depends_on", db.Entity, 0.4, t0)
	s.Relate(app.Entity, "depends_on", "absent", 0.95, t0)

	ft := &fakeTransport{embedder: "e", failAfter: -1}
	if _, err := s.QueryBreadth("runtime", 10, NewProtocolEngine(ft, "e")); err != nil {
		t.Fatal(err)
	}

	got := claimTextByID(ft.claims, app.ID)
	if got == nil {
		t.Fatalf("indexed claims missing %s: %+v", app.ID, ft.claims)
	}
	if got.Entity != app.Entity || got.ClaimType != app.ClaimType || got.Seq != app.Seq ||
		got.CreatedAt != app.CreatedAt || got.Confidence != app.Confidence ||
		got.Derivation != app.Derivation || got.EvidenceN != len(app.EvidenceRefs) {
		t.Fatalf("claim wire metadata = %+v, want claim %+v evidence_n=%d", *got, app, len(app.EvidenceRefs))
	}
	if len(ft.edges) != 1 {
		t.Fatalf("indexed edges = %+v, want only the visible high-confidence app->auth edge", ft.edges)
	}
	if ft.edges[0] != (WireEdge{From: app.Entity, Kind: "depends_on", To: auth.Entity, Confidence: 0.9}) {
		t.Fatalf("edge wire context = %+v", ft.edges[0])
	}
	if ft.gen != s.currentGen() || ft.gen <= db.Seq {
		t.Fatalf("corpus generation should include relation-edge seq; got %d current=%d dbSeq=%d", ft.gen, s.currentGen(), db.Seq)
	}
}

func claimTextByID(claims []ClaimText, id string) *ClaimText {
	for i := range claims {
		if claims[i].ClaimID == id {
			return &claims[i]
		}
	}
	return nil
}

// The mandatory fix the judge caught: Index must be idempotent on corpus_gen, or an out-of-process
// engine re-embeds the whole corpus on every query. Two queries at the same generation => ONE index.
func TestProtocolIdempotentIndex(t *testing.T) {
	s := storeWith(t, "project:api", "lang", "node", "go")
	ft := &fakeTransport{embedder: "e", failAfter: -1}
	eng := NewProtocolEngine(ft, "e")
	for i := 0; i < 2; i++ {
		if _, err := s.QueryBreadth("lang", 10, eng); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	if ft.indexCalls != 1 {
		t.Errorf("Index should run ONCE for two same-gen queries (no re-embed storm); got %d", ft.indexCalls)
	}
	if ft.queryCalls != 2 {
		t.Errorf("expected 2 queries, got %d", ft.queryCalls)
	}
}

// GateList + empty allowlist must match NOTHING (fail-closed); GateAll skips the gate (admin).
func TestProtocolGateFailClosed(t *testing.T) {
	ft := &fakeTransport{embedder: "e", failAfter: -1}
	eng := NewProtocolEngine(ft, "e")
	cs := []Claim{{ID: "c1", Entity: "x", Predicate: "lang", Object: "go"}}
	if err := eng.Index(cs, 1); err != nil {
		t.Fatal(err)
	}
	listEmpty, _ := eng.Query("lang", GateList, nil, 10)
	if len(listEmpty) != 0 {
		t.Errorf("GateList + empty allowlist must match nothing (fail-closed); got %d", len(listEmpty))
	}
	all, _ := eng.Query("lang", GateAll, nil, 10)
	if len(all) != 1 {
		t.Errorf("GateAll should bypass the gate; got %d", len(all))
	}
	gated, _ := eng.Query("lang", GateList, []string{"c1"}, 10)
	if len(gated) != 1 {
		t.Errorf("GateList with the id allowed should match; got %d", len(gated))
	}
}

// An embedder mismatch is a HARD refuse (ErrEmbedderMismatch), never a silent degrade — a silent
// model swap would invisibly wreck recall.
func TestProtocolIdentityMismatch(t *testing.T) {
	s := storeWith(t, "x", "p", "v")
	ft := &fakeTransport{embedder: "engine-model-B", failAfter: -1}
	eng := NewProtocolEngine(ft, "indexed-with-model-A")
	_, err := s.QueryBreadth("v", 10, eng)
	if !errors.Is(err, ErrEmbedderMismatch) {
		t.Errorf("embedder mismatch must be a hard ErrEmbedderMismatch refuse; got %v", err)
	}
}

// A down transport degrades to ErrEngineUnavailable (caller falls back to the ledger), never nil,nil.
func TestProtocolUnavailableDegrades(t *testing.T) {
	s := storeWith(t, "x", "p", "v")
	ft := &fakeTransport{embedder: "e", failAfter: 0} // fail on the very first round-trip
	eng := NewProtocolEngine(ft, "e")
	_, err := s.QueryBreadth("v", 10, eng)
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Errorf("a down engine must degrade to ErrEngineUnavailable; got %v", err)
	}
}

// Protocol-version skew is a HARD refuse (ErrProtocolVersion), not a degrade.
func TestProtocolSchemaSkew(t *testing.T) {
	s := storeWith(t, "x", "p", "v")
	ft := &fakeTransport{embedder: "e", failAfter: -1, schemaVersion: ProtocolVersion + 1}
	if _, err := s.QueryBreadth("v", 10, NewProtocolEngine(ft, "e")); !errors.Is(err, ErrProtocolVersion) {
		t.Errorf("a protocol-version skew must hard-refuse with ErrProtocolVersion; got %v", err)
	}
}

// dim_mismatch is identity-class and must map to ErrEmbedderMismatch (hard refuse).
func TestWireErrMapping(t *testing.T) {
	for _, code := range []string{CodeDimMismatch, CodeEmbedderMismatch} {
		if !errors.Is(wireErr(Response{Error: &WireError{Code: code, Message: "x"}}), ErrEmbedderMismatch) {
			t.Errorf("%s should map to ErrEmbedderMismatch", code)
		}
	}
	if !errors.Is(wireErr(Response{Error: &WireError{Code: CodeEngineUnavailable, Message: "x"}}), ErrEngineUnavailable) {
		t.Errorf("%s should map to ErrEngineUnavailable", CodeEngineUnavailable)
	}
}

// A non-responding sidecar must TIME OUT (and get killed), not hang forever holding the lock.
func TestHeldCoprocessTimeout(t *testing.T) {
	tr := &HeldCoprocessTransport{argv: []string{"sleep", "30"}, timeout: 200 * time.Millisecond}
	defer tr.Close()
	start := time.Now()
	if _, err := tr.RoundTrip(Request{SchemaVersion: ProtocolVersion, RequestID: "r1", Op: OpCapabilities}); err == nil {
		t.Error("a non-responding engine should time out, not hang")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("round-trip should have timed out fast, took %s", elapsed)
	}
}

// Fail-closed across the REAL JSON wire: GateList + an empty allowlist (which omitempty would have
// dropped) must still match nothing — proving the omitempty fix and the reference engine agree.
func TestWireFailClosedAcrossWire(t *testing.T) {
	tr := NewHeldCoprocess([]string{os.Args[0]}, []string{"COMPOUND_MOCK_ENGINE=1", "MOCK_EMBEDDER=ref:test:0:0"})
	eng := NewProtocolEngine(tr, "ref:test:0:0")
	defer eng.Close()
	if err := eng.Index([]Claim{{ID: "c1", Entity: "x", Predicate: "lang", Object: "go"}}, 1); err != nil {
		t.Fatal(err)
	}
	empty, err := eng.Query("lang", GateList, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("GateList + empty allowlist over the real wire must fail closed; got %d hits", len(empty))
	}
	allowed, _ := eng.Query("lang", GateList, []string{"c1"}, 10)
	if len(allowed) != 1 {
		t.Errorf("an allowed id should match across the wire; got %d", len(allowed))
	}
}

// End-to-end over a REAL held coprocess (the test binary re-execs as the reference engine): the
// process spawns once, stays warm, and breadth is gated to the current claim — with zero Rust.
func TestProtocolWarmCoprocess(t *testing.T) {
	s := storeWith(t, "project:healthapp", "backend_language", "node", "go") // go is current
	tr := NewHeldCoprocess([]string{os.Args[0]}, []string{"COMPOUND_MOCK_ENGINE=1", "MOCK_EMBEDDER=ref:test:0:0"})
	eng := NewProtocolEngine(tr, "ref:test:0:0")
	defer eng.Close()
	for i := 0; i < 2; i++ { // two queries, one warm process
		hits, err := s.QueryBreadth("backend language", 10, eng)
		if err != nil {
			t.Fatalf("warm query %d: %v", i, err)
		}
		if len(hits) != 1 || hits[0].Object != "go" {
			t.Fatalf("breadth via coprocess should return the current 'go'; got %v", objs(hits))
		}
	}
}

func TestClaimIndexTextLeadsWithClaimID(t *testing.T) {
	// The ID is part of the indexable surface so exact-identifier lookups
	// (DEC-0021, brain-D4) can rank the claim itself. Stable contract: mock and
	// real sidecar must agree on this text.
	c := Claim{ID: "DEC-0042", Entity: "decisions", Predicate: "decided", Object: "widgets stay blue"}
	if got := ClaimIndexText(c); got != "DEC-0042 decisions decided widgets stay blue" {
		t.Fatalf("ClaimIndexText = %q, want ID-led surface", got)
	}
}
