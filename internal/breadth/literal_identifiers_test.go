package breadth

import (
	"testing"

	"compound/internal/mem"
)

func TestExactIdentifierBeforeSemanticSimilarity(t *testing.T) {
	for _, literal := range []string{"70438829", "S034J921", "ticket_81b7ca9a", "ADR-0457"} {
		t.Run(literal, func(t *testing.T) {
			claims := []mem.ClaimText{
				{ClaimID: "exact", Entity: "inventory:" + literal, Text: "inventory:" + literal + " owns the asset"},
				{ClaimID: "similar", Entity: "inventory:other", Text: "ticket or serial " + literal + "0"},
				{ClaimID: "mention", Entity: "notes", Text: "an earlier note mentions " + literal},
			}
			query := "What owns serial " + literal + "?"
			embedder := newFakeEmbedder(2)
			embedder.vectors = map[string][]float32{
				query:                  {1, 0},
				wrapDocText(claims[0]): {0, 1},
				wrapDocText(claims[1]): {1, 0},
				wrapDocText(claims[2]): {1, 0},
			}
			engine, err := NewEngine(EngineConfig{Embedder: embedder, CompoundHome: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.IndexWithError(claims, 1); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				allowed []string
				first   string
			}{
				{[]string{"exact", "similar", "mention"}, "exact"},
				{[]string{"similar", "mention"}, "mention"},
				{[]string{"similar"}, "similar"},
			} {
				hits := engine.Query(query, mem.GateList, tc.allowed, 1)
				if len(hits) != 1 || hits[0].ClaimID != tc.first {
					t.Errorf("allowed %v: got %+v, want %s", tc.allowed, hits, tc.first)
				}
			}
		})
	}
}

func TestIdentifierLookupExcludesOrdinaryNumbersAndPartialMatches(t *testing.T) {
	for _, q := range []string{"Q4 data in 2026", "February 2026-02-17", "how many of the 10 items failed?"} {
		if ids := lookupIdentifiers(q); len(ids) > 0 {
			t.Errorf("query %q treated %v as identifiers", q, ids)
		}
	}
	if containsIdentifier("earlier-adr-0045 or adr_0045 or adr-00456", "adr-0045") {
		t.Fatal("accepted an inexact identifier")
	}
	if !containsIdentifier("decision:adr-0045 (superseded)", "adr-0045") {
		t.Fatal("missed delimited exact identifier")
	}
}
