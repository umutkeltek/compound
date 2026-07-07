package breadth

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"compound/internal/mem"
)

func TestTokenizeSplitsIdentifiers(t *testing.T) {
	got := Tokenize("compoundMemory sidecar_breadth EngineHTTP route-plan")
	want := []string{"compound", "memory", "sidecar", "breadth", "engine", "http", "route", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestQueryStripsStopwordsSoTerseEntityClaimsRank(t *testing.T) {
	// Falsifier for entity/predicate recall dilution (measured 2026-07-07): a
	// natural-language question must retrieve the terse entity-keyed claim, not be
	// crowded out of the top-k by stopword-heavy meta-claims. Pre-stopword-filter,
	// the filler terms (what/is/the/for/project) matched the noise claims and
	// pushed the target below k. Live proof this fixes: golden-set recall bakeoff.
	var eng Engine
	eng.Index([]mem.ClaimText{
		{ClaimID: "svc_alpha_database", Text: "alpha database managed postgres instance in a cloud region"},
		{ClaimID: "noise_dec", Text: "decision what is the goal for the project we decided is the binding file"},
		{ClaimID: "noise_pack", Text: "the context pack is what carries the source revisions for the runtime"},
		{ClaimID: "noise_policy", Text: "the policy decision is what governs the action for the work at the gate"},
		{ClaimID: "noise_paper", Text: "document parsing is what the vision language model does for the two stage"},
	}, 1)
	got := eng.Query("What is the database for the alpha project?", mem.GateAll, nil, 3)
	if len(got) == 0 {
		t.Fatalf("query returned no hits")
	}
	if got[0].ClaimID != "svc_alpha_database" {
		t.Fatalf("terse entity claim must rank #1 after stopword filtering; top-3 = %+v", got)
	}
}

func TestSalientTermsFallsBackWhenAllStopwords(t *testing.T) {
	// An all-stopword query must not filter to nothing (that would return zero
	// hits); salientTerms falls back to the unfiltered tokens.
	if got := salientTerms("what is the"); len(got) == 0 {
		t.Fatalf("all-stopword query must fall back to unfiltered tokens, got empty")
	}
	if got := salientTerms("alpha database"); !reflect.DeepEqual(got, []string{"alpha", "database"}) {
		t.Fatalf("salient terms = %v, want [alpha database]", got)
	}
}

func TestQueryEnforcesAllowlistFailClosed(t *testing.T) {
	var eng Engine
	eng.Index([]mem.ClaimText{
		{ClaimID: "c1", Text: "compound memory engine"},
		{ClaimID: "c2", Text: "unrelated memory"},
	}, 1)
	if got := eng.Query("memory", mem.GateList, nil, 10); len(got) != 0 {
		t.Fatalf("empty allowlist must fail closed, got %+v", got)
	}
	got := eng.Query("memory", mem.GateList, []string{"c1"}, 10)
	if len(got) != 1 || got[0].ClaimID != "c1" {
		t.Fatalf("allowlist query = %+v, want c1 only", got)
	}
}

func TestReferenceCoverageUnionCatchesSubstring(t *testing.T) {
	var eng Engine
	eng.Index([]mem.ClaimText{{ClaimID: "c1", Text: "compounding substrate"}}, 1)
	got := eng.Query("compound", mem.GateList, []string{"c1"}, 10)
	if len(got) != 1 || got[0].ClaimID != "c1" {
		t.Fatalf("substring reference hit should be covered, got %+v", got)
	}
}

func TestReferenceCoverageSurvivesTightK(t *testing.T) {
	var eng Engine
	eng.Index([]mem.ClaimText{
		{ClaimID: "substring", Text: "compounding substrate"},
		{ClaimID: "exact1", Text: "compound memory"},
		{ClaimID: "exact2", Text: "compound engine"},
	}, 1)
	got := eng.Query("compound", mem.GateList, []string{"substring", "exact1", "exact2"}, 3)
	seen := map[string]bool{}
	for _, h := range got {
		seen[h.ClaimID] = true
	}
	if !seen["substring"] || !seen["exact1"] || !seen["exact2"] {
		t.Fatalf("all reference hits should survive top-k, got %+v", got)
	}
}

func TestReferenceCoverageMirrorsReferenceTopKWhenReferenceSetExceedsK(t *testing.T) {
	claims := []mem.Claim{
		{ID: "z_first", Entity: "compound", Predicate: "topic", Object: "alpha"},
		{ID: "a_second", Entity: "compound", Predicate: "topic", Object: "beta"},
		{ID: "b_third", Entity: "compound", Predicate: "topic", Object: "gamma"},
	}
	allow := []string{"z_first", "a_second", "b_third"}
	var reference mem.LexicalEngine
	if err := reference.Index(claims, 1); err != nil {
		t.Fatal(err)
	}
	referenceHits, err := reference.Query("compound", mem.GateList, allow, 2)
	if err != nil {
		t.Fatal(err)
	}

	var eng Engine
	eng.Index([]mem.ClaimText{
		{ClaimID: claims[0].ID, Text: mem.ClaimIndexText(claims[0])},
		{ClaimID: claims[1].ID, Text: mem.ClaimIndexText(claims[1])},
		{ClaimID: claims[2].ID, Text: mem.ClaimIndexText(claims[2])},
	}, 1)
	got := eng.Query("compound", mem.GateList, allow, 2)
	gotIDs := map[string]bool{}
	for _, h := range got {
		gotIDs[h.ClaimID] = true
	}
	for _, h := range referenceHits {
		if !gotIDs[h.ClaimID] {
			t.Fatalf("breadth result should cover reference top-k hit %s; reference=%+v got=%+v", h.ClaimID, referenceHits, got)
		}
	}
}

func TestBM25RanksSpecificTerms(t *testing.T) {
	var eng Engine
	eng.Index([]mem.ClaimText{
		{ClaimID: "specific", Text: "compound memory engine"},
		{ClaimID: "broad", Text: "compound only"},
	}, 1)
	got := eng.Query("memory engine", mem.GateList, []string{"specific", "broad"}, 10)
	if len(got) == 0 || got[0].ClaimID != "specific" {
		t.Fatalf("BM25 should rank specific term match first, got %+v", got)
	}
}

func TestRunProtocolWireLoop(t *testing.T) {
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, req := range []mem.Request{
		{SchemaVersion: mem.ProtocolVersion, RequestID: "caps", Op: mem.OpCapabilities},
		{SchemaVersion: mem.ProtocolVersion, RequestID: "idx", Op: mem.OpIndex, CorpusGen: 7, Claims: []mem.ClaimText{{ClaimID: "c1", Text: "compounding substrate"}}},
		{SchemaVersion: mem.ProtocolVersion, RequestID: "query", Op: mem.OpQuery, Text: "compound", Gate: mem.GateList, Allowlist: []string{"c1"}, K: 10},
	} {
		if err := enc.Encode(req); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := RunProtocol(&in, &out, "test:lexical:0:0"); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	var caps, idx, query mem.Response
	for _, resp := range []*mem.Response{&caps, &idx, &query} {
		if err := dec.Decode(resp); err != nil {
			t.Fatal(err)
		}
	}
	if caps.Name != Name || !caps.Breadth || caps.Embedder != "test:lexical:0:0" {
		t.Fatalf("capabilities = %+v", caps)
	}
	if idx.Indexed != 1 || idx.CorpusGen != 7 {
		t.Fatalf("index response = %+v", idx)
	}
	if len(query.Hits) != 1 || query.Hits[0].ClaimID != "c1" {
		t.Fatalf("query response = %+v", query)
	}
}

func TestRunProtocolQueryBeforeIndexFailsTyped(t *testing.T) {
	var in bytes.Buffer
	if err := json.NewEncoder(&in).Encode(mem.Request{SchemaVersion: mem.ProtocolVersion, RequestID: "query", Op: mem.OpQuery, Text: "x", Gate: mem.GateList, Allowlist: []string{"c1"}, K: 1}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunProtocol(&in, &out, ""); err != nil {
		t.Fatal(err)
	}
	var resp mem.Response
	if err := json.NewDecoder(&out).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error == nil || resp.Error.Code != mem.CodeIndexMissing {
		t.Fatalf("query before index response = %+v", resp)
	}
}

func TestFixtureCorpusReferenceCovered(t *testing.T) {
	claims := []mem.ClaimText{
		{ClaimID: "c_compounding", Text: "compounding substrate"},
		{ClaimID: "c_memory", Text: "memory engine route"},
		{ClaimID: "c_camel", Text: "ContextPack hookReadiness"},
	}
	allow := []string{"c_compounding", "c_memory", "c_camel"}
	tests := []struct {
		query string
		k     int
	}{
		{query: "compound", k: 1},
		{query: "memory engine", k: 2},
		{query: "hook readiness", k: 1},
	}
	var eng Engine
	eng.Index(claims, 1)
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			reference := referenceContains(tt.query, docsFromClaims(claims))
			got := eng.Query(tt.query, mem.GateList, allow, tt.k)
			gotIDs := map[string]bool{}
			for _, h := range got {
				gotIDs[h.ClaimID] = true
			}
			for i, h := range reference {
				if i >= tt.k {
					break
				}
				if !gotIDs[h.ClaimID] {
					t.Fatalf("query %q missing reference hit %s in %+v", tt.query, h.ClaimID, got)
				}
			}
		})
	}
}

func docsFromClaims(claims []mem.ClaimText) []doc {
	docs := make([]doc, 0, len(claims))
	for _, c := range claims {
		tokens := Tokenize(c.Text)
		freq := map[string]int{}
		for _, tok := range tokens {
			freq[tok]++
		}
		docs = append(docs, doc{claim: c, tokens: tokens, freq: freq})
	}
	return docs
}
