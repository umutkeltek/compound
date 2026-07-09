package bench

import (
	"reflect"
	"testing"
)

// syntheticAcceptanceJSON is a fully SYNTHETIC acceptance file (no real tool/product ids or prompt
// text): fields are deliberately out of sorted order so the determinism + sorting invariants are
// exercised, not merely asserted on already-sorted input.
const syntheticAcceptanceJSON = `{
  "schema_version": 1,
  "class_cases": [
    {"prompt": "classify one", "expected_class": "alpha"},
    {"prompt": "classify two", "expected_class": "beta"}
  ],
  "cases": [
    {
      "id": "case-beta",
      "prompt": "beta prompt text",
      "selected_rule": "rule-beta",
      "required_anywhere": ["node:beta"],
      "required_top_n": {"n": 5, "ids": ["node:beta", "node:alpha"]},
      "required_policy_for": ["node:beta"],
      "forbidden_top_n_contains": {"n": 3, "contains": ["zeta", "alpha"]},
      "must_outrank": [
        {"winner": "node:beta", "loser_contains": ["zeta", "alpha"]},
        {"winner": "node:alpha", "loser_contains": ["gamma"]}
      ]
    },
    {
      "id": "case-alpha",
      "prompt": "alpha prompt text",
      "selected_rule": "rule-alpha",
      "required_top_n": {"n": 8, "ids": ["node:alpha"]},
      "required_policy_for": ["node:alpha"]
    },
    {
      "id": "case-gamma",
      "prompt": "gamma prompt text",
      "selected_rule": "rule-gamma",
      "required_anywhere": ["node:gamma"]
    }
  ]
}`

// TestConvertAcceptanceDeterministic falsifies nondeterminism: converting the same input bytes
// twice yields byte-identical suites, and the output is actually SORTED (cases by id, expected +
// forbidden + outrank slices sorted) — so "deterministic" is not vacuously true on pre-sorted data.
func TestConvertAcceptanceDeterministic(t *testing.T) {
	a, err := ConvertAcceptance([]byte(syntheticAcceptanceJSON), "syn")
	if err != nil {
		t.Fatalf("convert a: %v", err)
	}
	b, err := ConvertAcceptance([]byte(syntheticAcceptanceJSON), "syn")
	if err != nil {
		t.Fatalf("convert b: %v", err)
	}
	ab, err := MarshalSuite(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := MarshalSuite(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ab) != string(bb) {
		t.Fatalf("convert not byte-deterministic:\n--a--\n%s\n--b--\n%s", ab, bb)
	}

	// cases sorted by id
	if got := []string{a.Cases[0].ID, a.Cases[1].ID, a.Cases[2].ID}; !reflect.DeepEqual(got, []string{"case-alpha", "case-beta", "case-gamma"}) {
		t.Fatalf("cases not sorted by id: %v", got)
	}
	beta := a.Cases[1]
	if !reflect.DeepEqual(beta.Expected, []string{"node:alpha", "node:beta"}) {
		t.Fatalf("beta expected = %v, want sorted union [node:alpha node:beta]", beta.Expected)
	}
	if beta.Forbidden == nil || !reflect.DeepEqual(beta.Forbidden.Contains, []string{"alpha", "zeta"}) || beta.Forbidden.N != 3 {
		t.Fatalf("beta forbidden = %+v, want N=3 contains sorted [alpha zeta]", beta.Forbidden)
	}
	if len(beta.MustOutrank) != 2 || beta.MustOutrank[0].Winner != "node:alpha" || beta.MustOutrank[1].Winner != "node:beta" {
		t.Fatalf("beta must_outrank not sorted by winner: %+v", beta.MustOutrank)
	}
	if !reflect.DeepEqual(beta.MustOutrank[1].LoserContains, []string{"alpha", "zeta"}) {
		t.Fatalf("beta outrank loser_contains not sorted: %v", beta.MustOutrank[1].LoserContains)
	}
	// harness empty (condition B), lane exact, kind capability
	if beta.Harness != "" || beta.Lane != "exact" || beta.Kind != KindCapability {
		t.Fatalf("beta case shape = %+v, want empty harness / lane exact / kind capability", beta)
	}
}

// TestConvertAcceptanceDropAccounting falsifies silent truncation: each droppable class is counted
// exactly, and a case whose only extra assertions are droppable (selected_rule + required_policy_for)
// still converts, carrying its expected ids with no forbidden/outrank.
func TestConvertAcceptanceDropAccounting(t *testing.T) {
	suite, err := ConvertAcceptance([]byte(syntheticAcceptanceJSON), "syn")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	want := map[string]int{
		"required_policy_for":          2, // case-beta, case-alpha
		"selected_rule":                3, // all three
		"class_cases":                  2, // two class_cases
		"task_spec_routing_preference": 0, // none present
	}
	got := map[string]int{}
	for _, d := range suite.Dropped {
		got[d.Field] = d.Count
		if d.Reason == "" {
			t.Fatalf("drop record %q has no reason", d.Field)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drop counts = %v, want %v", got, want)
	}

	// case-alpha: only droppable extras -> still converts with its expected id, nothing asserted.
	alpha := suite.Cases[0]
	if alpha.ID != "case-alpha" {
		t.Fatalf("cases[0] = %s, want case-alpha", alpha.ID)
	}
	if !reflect.DeepEqual(alpha.Expected, []string{"node:alpha"}) {
		t.Fatalf("case-alpha expected = %v, want [node:alpha] (droppable assertions must not lose it)", alpha.Expected)
	}
	if alpha.Forbidden != nil || len(alpha.MustOutrank) != 0 {
		t.Fatalf("case-alpha should assert nothing beyond expected, got forbidden=%+v outrank=%+v", alpha.Forbidden, alpha.MustOutrank)
	}
}

const syntheticGoldenYAML = `schema_version: compound.memory_bakeoff_queries.v0.1
queries:
  - id: q-zeta
    lane: exact
    query: synthetic memory query zeta widget
    expected_claim_ids:
      - claim_zeta_two
      - claim_zeta_one
      - claim_zeta_one
  - id: q-alpha
    lane: casual
    query: synthetic memory query alpha gadget
    expected_claim_ids:
      - claim_alpha
`

// TestConvertGoldenProducesMemoryCases falsifies the golden path: bakeoff-schema YAML converts to
// valid, deterministic memory cases (sorted by id, deduped+sorted expected claim ids, lane
// preserved) that pass suite validation.
func TestConvertGoldenProducesMemoryCases(t *testing.T) {
	suite, err := ConvertGolden([]byte(syntheticGoldenYAML), "syn-golden")
	if err != nil {
		t.Fatalf("convert golden: %v", err)
	}
	if len(suite.Cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(suite.Cases))
	}
	if suite.Cases[0].ID != "q-alpha" || suite.Cases[1].ID != "q-zeta" {
		t.Fatalf("cases not sorted by id: %s, %s", suite.Cases[0].ID, suite.Cases[1].ID)
	}
	zeta := suite.Cases[1]
	if zeta.Kind != KindMemory {
		t.Fatalf("kind = %q, want memory", zeta.Kind)
	}
	if zeta.Lane != "exact" || zeta.Task != "synthetic memory query zeta widget" {
		t.Fatalf("zeta lane/task = %q/%q", zeta.Lane, zeta.Task)
	}
	if !reflect.DeepEqual(zeta.Expected, []string{"claim_zeta_one", "claim_zeta_two"}) {
		t.Fatalf("zeta expected = %v, want deduped+sorted [claim_zeta_one claim_zeta_two]", zeta.Expected)
	}
	if suite.Dropped != nil {
		t.Fatalf("golden conversion drops nothing, got %+v", suite.Dropped)
	}

	// deterministic bytes
	a, _ := MarshalSuite(suite)
	s2, _ := ConvertGolden([]byte(syntheticGoldenYAML), "syn-golden")
	b, _ := MarshalSuite(s2)
	if string(a) != string(b) {
		t.Fatalf("golden convert not byte-deterministic")
	}
}

// TestConvertRejectsWrongSchema falsifies silent acceptance of a mismatched source: a golden file
// with the acceptance schema_version (and vice versa) must error, not produce a garbage suite.
func TestConvertRejectsWrongSchema(t *testing.T) {
	if _, err := ConvertGolden([]byte(syntheticAcceptanceJSON), "x"); err == nil {
		t.Fatal("expected golden convert of acceptance json to fail on schema_version")
	}
	if _, err := ConvertAcceptance([]byte(`{"schema_version":2,"cases":[]}`), "x"); err == nil {
		t.Fatal("expected acceptance convert to reject schema_version 2")
	}
}
