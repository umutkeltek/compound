package bench

// Converters turn the two REAL evaluation sources into bench suites, deterministically. "Same
// input bytes → byte-identical suite" is the load-bearing invariant: every slice is sorted, no
// timestamp or map-iteration order leaks in, so a converted suite is a stable artifact that can be
// diffed and re-baselined. The converted suites carry private tool/product/claim ids, so they are
// written only under $COMPOUND_HOME by the caller — never into the tracked tree.
//
// Faithful to the Wave-2 gate's "condition B" (the bench's own SurfaceForContext path): prompt →
// task, harness stays empty, and only the surfacing-expressible assertions carry over
// (required_anywhere ∪ required_top_n.ids → expected, forbidden_top_n_contains → Forbidden,
// must_outrank → MustOutrank). required_policy_for is NOT evaluated here ("only the resolver
// attaches policy") — it is dropped loudly, along with the resolver-internal selected_rule and the
// classifier class_cases. No silent truncation: DropAccounting names every dropped class.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// AcceptanceSchemaVersion is the schema_version the capability-resolver acceptance file must carry.
const AcceptanceSchemaVersion = 1

// GoldenSchemaVersion is the schema_version the recall-golden bakeoff file must carry.
const GoldenSchemaVersion = "compound.memory_bakeoff_queries.v0.1"

// Drop-accounting field names (stable strings; also the keys a reader greps for).
const (
	dropRequiredPolicyFor = "required_policy_for"
	dropSelectedRule      = "selected_rule"
	dropClassCases        = "class_cases"
	dropTaskSpecRouting   = "task_spec_routing_preference"
)

// splitForID assigns a deterministic, stable held_in/held_out split from the case id alone
// (FNV-1a mod 10: <7 held_in, else held_out). Stable across runs and independent of case order, so
// re-converting the same source keeps every case in the same split — a moved case would silently
// invalidate a baseline comparison.
func splitForID(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	if h.Sum32()%10 < 7 {
		return SplitHeldIn
	}
	return SplitHeldOut
}

// SplitRuleDoc is the human-readable split rule, surfaced in the CLI usage so the assignment is not
// a black box.
const SplitRuleDoc = "split = FNV-1a(id) mod 10 < 7 ? held_in : held_out (stable per case id)"

type acceptanceFile struct {
	SchemaVersion int               `json:"schema_version"`
	ClassCases    []json.RawMessage `json:"class_cases"`
	Cases         []acceptanceCase  `json:"cases"`
}

type acceptanceCase struct {
	ID                    string              `json:"id"`
	Prompt                string              `json:"prompt"`
	SelectedRule          string              `json:"selected_rule"`
	RequiredAnywhere      []string            `json:"required_anywhere"`
	RequiredTopN          *acceptanceTopN     `json:"required_top_n"`
	RequiredPolicyFor     []string            `json:"required_policy_for"`
	ForbiddenTopNContains *acceptanceForbid   `json:"forbidden_top_n_contains"`
	MustOutrank           []acceptanceOutrank `json:"must_outrank"`
	TaskSpec              json.RawMessage     `json:"task_spec"`
}

type acceptanceTopN struct {
	N   int      `json:"n"`
	IDs []string `json:"ids"`
}

type acceptanceForbid struct {
	N        int      `json:"n"`
	Contains []string `json:"contains"`
}

type acceptanceOutrank struct {
	Winner        string   `json:"winner"`
	LoserContains []string `json:"loser_contains"`
}

// ConvertAcceptance parses the capability-resolver acceptance JSON and returns the bench suite plus
// the drop-accounting. It is deterministic: cases sorted by id, every id/substring slice sorted.
func ConvertAcceptance(data []byte, suiteName string) (Suite, error) {
	var f acceptanceFile
	if err := json.Unmarshal(data, &f); err != nil {
		return Suite{}, fmt.Errorf("parse acceptance json: %w", err)
	}
	if f.SchemaVersion != AcceptanceSchemaVersion {
		return Suite{}, fmt.Errorf("acceptance schema_version must be %d, got %d", AcceptanceSchemaVersion, f.SchemaVersion)
	}
	if len(f.Cases) == 0 {
		return Suite{}, fmt.Errorf("acceptance file has no cases")
	}

	var (
		cases            []Case
		droppedPolicyFor int
		droppedSelected  int
		droppedTaskSpec  int
	)
	for i, ac := range f.Cases {
		if strings.TrimSpace(ac.ID) == "" {
			return Suite{}, fmt.Errorf("acceptance cases[%d] missing id", i)
		}
		expected := map[string]bool{}
		for _, id := range ac.RequiredAnywhere {
			if s := strings.TrimSpace(id); s != "" {
				expected[s] = true
			}
		}
		if ac.RequiredTopN != nil {
			for _, id := range ac.RequiredTopN.IDs {
				if s := strings.TrimSpace(id); s != "" {
					expected[s] = true
				}
			}
		}
		if len(expected) == 0 {
			return Suite{}, fmt.Errorf("acceptance case %q has no required_anywhere/required_top_n ids to convert", ac.ID)
		}
		c := Case{
			ID:       ac.ID,
			Kind:     KindCapability,
			Task:     ac.Prompt,
			Expected: sortedKeys(expected),
			Lane:     "exact",
			Split:    splitForID(ac.ID),
		}
		if ac.ForbiddenTopNContains != nil && len(ac.ForbiddenTopNContains.Contains) > 0 {
			subs := append([]string(nil), ac.ForbiddenTopNContains.Contains...)
			sort.Strings(subs)
			c.Forbidden = &ForbiddenTopN{N: ac.ForbiddenTopNContains.N, Contains: subs}
		}
		if len(ac.MustOutrank) > 0 {
			c.MustOutrank = convertOutrank(ac.MustOutrank)
		}
		cases = append(cases, c)

		if len(ac.RequiredPolicyFor) > 0 {
			droppedPolicyFor++
		}
		if strings.TrimSpace(ac.SelectedRule) != "" {
			droppedSelected++
		}
		if len(ac.TaskSpec) > 0 && string(ac.TaskSpec) != "null" {
			droppedTaskSpec++
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })

	dropped := []DropRecord{
		{Field: dropRequiredPolicyFor, Count: droppedPolicyFor, Reason: "resolver-internal: only the resolver attaches policy; condition B (SurfaceForContext) does not evaluate it — Wave-2 exclusion kept"},
		{Field: dropSelectedRule, Count: droppedSelected, Reason: "resolver-internal rule id; not a surfacing assertion"},
		{Field: dropClassCases, Count: len(f.ClassCases), Reason: "classifier check (prompt→expected_class); out of the capability-surfacing scope"},
		{Field: dropTaskSpecRouting, Count: droppedTaskSpec, Reason: "task_spec routing preference (capability_route/avoid) is not a surfacing assertion; case converted via its raw prompt"},
	}

	suite := Suite{Suite: suiteName, Dropped: dropped, Cases: cases}
	if err := suite.validate(); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

func convertOutrank(in []acceptanceOutrank) []OutrankAssertion {
	out := make([]OutrankAssertion, 0, len(in))
	for _, o := range in {
		if strings.TrimSpace(o.Winner) == "" {
			continue
		}
		losers := make([]string, 0, len(o.LoserContains))
		for _, l := range o.LoserContains {
			if s := strings.TrimSpace(l); s != "" {
				losers = append(losers, s)
			}
		}
		sort.Strings(losers)
		out = append(out, OutrankAssertion{Winner: o.Winner, LoserContains: losers})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Winner != out[j].Winner {
			return out[i].Winner < out[j].Winner
		}
		return strings.Join(out[i].LoserContains, ",") < strings.Join(out[j].LoserContains, ",")
	})
	return out
}

type goldenFile struct {
	SchemaVersion string        `yaml:"schema_version"`
	Queries       []goldenQuery `yaml:"queries"`
}

type goldenQuery struct {
	ID               string   `yaml:"id"`
	Lane             string   `yaml:"lane"`
	Query            string   `yaml:"query"`
	ExpectedClaimIDs []string `yaml:"expected_claim_ids"`
}

// ConvertGolden parses the recall-golden bakeoff YAML and returns the memory bench suite. It is
// deterministic (cases sorted by id, expected claim ids sorted+deduped). Nothing is dropped — the
// bakeoff schema is already surfacing-shaped — so the suite carries no Dropped block.
func ConvertGolden(data []byte, suiteName string) (Suite, error) {
	var f goldenFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return Suite{}, fmt.Errorf("parse golden yaml: %w", err)
	}
	if strings.TrimSpace(f.SchemaVersion) != GoldenSchemaVersion {
		return Suite{}, fmt.Errorf("golden schema_version must be %q, got %q", GoldenSchemaVersion, f.SchemaVersion)
	}
	if len(f.Queries) == 0 {
		return Suite{}, fmt.Errorf("golden file has no queries")
	}
	var cases []Case
	for i, q := range f.Queries {
		if strings.TrimSpace(q.ID) == "" {
			return Suite{}, fmt.Errorf("golden queries[%d] missing id", i)
		}
		expected := map[string]bool{}
		for _, id := range q.ExpectedClaimIDs {
			if s := strings.TrimSpace(id); s != "" {
				expected[s] = true
			}
		}
		if len(expected) == 0 {
			return Suite{}, fmt.Errorf("golden query %q has no expected_claim_ids", q.ID)
		}
		cases = append(cases, Case{
			ID:       q.ID,
			Kind:     KindMemory,
			Task:     q.Query,
			Expected: sortedKeys(expected),
			Lane:     q.Lane,
			Split:    splitForID(q.ID),
		})
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	suite := Suite{Suite: suiteName, Cases: cases}
	if err := suite.validate(); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

// MarshalSuite renders a suite to deterministic, byte-stable JSON (2-space indent, trailing
// newline). The determinism guarantee rests on the converters sorting every slice; Go marshals
// struct fields in declaration order and map keys sorted, so no ordering nondeterminism remains.
func MarshalSuite(s Suite) ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
