package mem

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/reviewartifact"
	"gopkg.in/yaml.v3"
)

const (
	BakeoffQuerySchemaVersion  = "compound.memory_bakeoff_queries.v0.1"
	BakeoffReportSchemaVersion = "compound.memory_bakeoff_report.v0.1"
	DefaultBakeoffQueriesPath  = "fixtures/bakeoff/queries.yaml"
)

type BakeoffQuerySet struct {
	SchemaVersion string         `json:"schema_version" yaml:"schema_version"`
	Queries       []BakeoffQuery `json:"queries" yaml:"queries"`
}

type BakeoffQuery struct {
	ID               string   `json:"id" yaml:"id"`
	Lane             string   `json:"lane" yaml:"lane"`
	Query            string   `json:"query" yaml:"query"`
	ExpectedClaimIDs []string `json:"expected_claim_ids" yaml:"expected_claim_ids"`
	MustHitClaimIDs  []string `json:"-" yaml:"must_hit_claim_ids"`
}

type BakeoffInitReport struct {
	Path       string   `json:"path"`
	Created    bool     `json:"created"`
	QueryCount int      `json:"query_count"`
	Lanes      []string `json:"lanes"`
}

type BakeoffRunOptions struct {
	QueriesPath    string
	K              int
	Candidate      Engine
	AllowExpansion bool
	Now            time.Time
}

type BakeoffReport struct {
	Status string `json:"status,omitempty"`
	reviewartifact.OperatorCard
	SchemaVersion         string               `json:"schema_version"`
	GeneratedAt           string               `json:"generated_at"`
	QueriesPath           string               `json:"queries_path"`
	QueryCount            int                  `json:"query_count"`
	K                     int                  `json:"k"`
	CompareMode           string               `json:"compare_mode"`
	CandidateEngine       string               `json:"candidate_engine"`
	LedgerDigestBefore    string               `json:"ledger_digest_before"`
	LedgerDigestAfter     string               `json:"ledger_digest_after"`
	LedgerDigestUnchanged bool                 `json:"ledger_digest_unchanged"`
	Determinism           BakeoffDeterminism   `json:"determinism"`
	Metrics               BakeoffMetrics       `json:"metrics"`
	Queries               []BakeoffQueryReport `json:"queries"`
	ReportPath            string               `json:"report_path,omitempty"`
}

type BakeoffDeterminism struct {
	Passed     bool     `json:"passed"`
	Mismatches []string `json:"mismatches,omitempty"`
}

type BakeoffMetrics struct {
	RecallAt5        float64 `json:"recall_at_5"`
	RecallAt10       float64 `json:"recall_at_10"`
	MRR              float64 `json:"mrr"`
	ReferenceCovered float64 `json:"reference_covered"`
	LatencyP50MS     float64 `json:"latency_p50_ms"`
	LatencyP95MS     float64 `json:"latency_p95_ms"`
}

type BakeoffQueryReport struct {
	ID                       string              `json:"id"`
	Lane                     string              `json:"lane"`
	Query                    string              `json:"query"`
	ExpectedClaimIDs         []string            `json:"expected_claim_ids"`
	ReferenceClaimIDs        []string            `json:"reference_claim_ids"`
	CandidateClaimIDs        []string            `json:"candidate_claim_ids"`
	CandidateRankedClaimIDs  []string            `json:"candidate_ranked_claim_ids"`
	MissingExpectedClaimIDs  []string            `json:"missing_expected_claim_ids,omitempty"`
	RecallAt5                float64             `json:"recall_at_5"`
	RecallAt10               float64             `json:"recall_at_10"`
	MRR                      float64             `json:"mrr"`
	ReferenceCovered         bool                `json:"reference_covered"`
	ReferenceCoveredFraction float64             `json:"reference_covered_fraction"`
	LatencyMS                float64             `json:"latency_ms"`
	Compare                  EngineCompareReport `json:"compare"`
}

func WriteDefaultBakeoffQueries(path string, force bool) (BakeoffInitReport, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultBakeoffQueriesPath
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			set, loadErr := LoadBakeoffQueries(path)
			if loadErr != nil {
				return BakeoffInitReport{}, loadErr
			}
			return BakeoffInitReport{Path: path, Created: false, QueryCount: len(set.Queries), Lanes: bakeoffLanes(set.Queries)}, nil
		} else if !os.IsNotExist(err) {
			return BakeoffInitReport{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return BakeoffInitReport{}, err
	}
	if err := os.WriteFile(path, []byte(defaultBakeoffQueriesYAML), 0o644); err != nil {
		return BakeoffInitReport{}, err
	}
	set, err := LoadBakeoffQueries(path)
	if err != nil {
		return BakeoffInitReport{}, err
	}
	return BakeoffInitReport{Path: path, Created: true, QueryCount: len(set.Queries), Lanes: bakeoffLanes(set.Queries)}, nil
}

func LoadBakeoffQueries(path string) (BakeoffQuerySet, error) {
	if strings.TrimSpace(path) == "" {
		return BakeoffQuerySet{}, fmt.Errorf("queries path required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BakeoffQuerySet{}, err
	}
	var set BakeoffQuerySet
	if err := yaml.Unmarshal(data, &set); err != nil {
		return BakeoffQuerySet{}, err
	}
	if set.SchemaVersion == "" {
		set.SchemaVersion = BakeoffQuerySchemaVersion
	}
	for i := range set.Queries {
		q := &set.Queries[i]
		if len(q.ExpectedClaimIDs) == 0 && len(q.MustHitClaimIDs) > 0 {
			q.ExpectedClaimIDs = append([]string(nil), q.MustHitClaimIDs...)
		}
		q.ExpectedClaimIDs = uniqueNonEmpty(q.ExpectedClaimIDs)
	}
	if err := validateBakeoffQueries(set); err != nil {
		return BakeoffQuerySet{}, err
	}
	return set, nil
}

func (s *Store) RunBakeoff(opts BakeoffRunOptions) (BakeoffReport, error) {
	if opts.Candidate == nil {
		return BakeoffReport{}, fmt.Errorf("candidate engine required")
	}
	if opts.K <= 0 {
		opts.K = 10
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	set, err := LoadBakeoffQueries(opts.QueriesPath)
	if err != nil {
		return BakeoffReport{}, err
	}
	before, err := s.LedgerDigest()
	if err != nil {
		return BakeoffReport{}, err
	}

	first, err := s.runBakeoffPass(set.Queries, opts)
	if err != nil {
		return BakeoffReport{}, err
	}
	second, err := s.runBakeoffPass(set.Queries, opts)
	if err != nil {
		return BakeoffReport{}, err
	}

	after, err := s.LedgerDigest()
	if err != nil {
		return BakeoffReport{}, err
	}
	report := BakeoffReport{
		SchemaVersion:         BakeoffReportSchemaVersion,
		GeneratedAt:           opts.Now.UTC().Format(time.RFC3339),
		QueriesPath:           opts.QueriesPath,
		QueryCount:            len(first.queries),
		K:                     opts.K,
		CompareMode:           "exact",
		LedgerDigestBefore:    before,
		LedgerDigestAfter:     after,
		LedgerDigestUnchanged: before == after,
		Queries:               first.queries,
	}
	if opts.AllowExpansion {
		report.CompareMode = "allow_expansion"
	}
	if len(first.queries) > 0 {
		report.CandidateEngine = first.queries[0].Compare.Candidate.Engine.Name
	}
	report.Determinism = compareBakeoffPasses(first.queries, second.queries)
	report.Metrics = bakeoffMetrics(first.queries)
	report.Status = "recorded"
	if card, err := OperatorCardForBakeoffReport(report); err == nil {
		report.OperatorCard = card
	}

	writeErr := s.writeBakeoffReport(&report, opts.Now)
	var errs []string
	if writeErr != nil {
		errs = append(errs, writeErr.Error())
	}
	if !report.LedgerDigestUnchanged {
		errs = append(errs, "ledger digest changed during bakeoff")
	}
	if !report.Determinism.Passed {
		errs = append(errs, "bakeoff ranked order is nondeterministic")
	}
	if report.Metrics.ReferenceCovered < 1 {
		errs = append(errs, "candidate did not cover all reference claims")
	}
	for _, q := range report.Queries {
		if len(q.MissingExpectedClaimIDs) > 0 {
			errs = append(errs, fmt.Sprintf("%s missing expected claim(s): %s", q.ID, strings.Join(q.MissingExpectedClaimIDs, ",")))
		}
	}
	if len(errs) > 0 {
		return report, errors.New(strings.Join(errs, "; "))
	}
	return report, nil
}

type bakeoffPass struct {
	queries []BakeoffQueryReport
}

func (s *Store) runBakeoffPass(queries []BakeoffQuery, opts BakeoffRunOptions) (bakeoffPass, error) {
	out := make([]BakeoffQueryReport, 0, len(queries))
	for _, q := range queries {
		start := time.Now()
		compare, err := s.EngineCompareWithOptions(q.Query, opts.K, opts.Candidate, EngineCompareOptions{AllowExpansion: opts.AllowExpansion})
		if err != nil {
			return bakeoffPass{}, err
		}
		latency := time.Since(start)
		ranked := doctorRankedClaimIDs(compare.Candidate.Hits)
		qr := BakeoffQueryReport{
			ID:                       q.ID,
			Lane:                     q.Lane,
			Query:                    q.Query,
			ExpectedClaimIDs:         append([]string(nil), q.ExpectedClaimIDs...),
			ReferenceClaimIDs:        append([]string(nil), compare.ReferenceClaimIDs...),
			CandidateClaimIDs:        append([]string(nil), compare.CandidateClaimIDs...),
			CandidateRankedClaimIDs:  ranked,
			MissingExpectedClaimIDs:  missingWithinK(q.ExpectedClaimIDs, ranked, 10),
			RecallAt5:                recallAtK(q.ExpectedClaimIDs, ranked, 5),
			RecallAt10:               recallAtK(q.ExpectedClaimIDs, ranked, 10),
			MRR:                      reciprocalRank(q.ExpectedClaimIDs, ranked),
			ReferenceCovered:         compare.ReferenceCovered,
			ReferenceCoveredFraction: referenceCoveredFraction(compare.ReferenceClaimIDs, compare.CandidateClaimIDs),
			LatencyMS:                durationMS(latency),
			Compare:                  compare,
		}
		out = append(out, qr)
	}
	return bakeoffPass{queries: out}, nil
}

func (s *Store) writeBakeoffReport(report *BakeoffReport, now time.Time) error {
	dir := filepath.Join(s.home, "reviews", "memory-bakeoffs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stamp := now.UTC().Format("20060102T150405Z")
	name := fmt.Sprintf("memory-bakeoff-%s-%s.json", stamp, shortHash(report.QueriesPath))
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	report.ReportPath = path
	data, err = json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func validateBakeoffQueries(set BakeoffQuerySet) error {
	if set.SchemaVersion != BakeoffQuerySchemaVersion {
		return fmt.Errorf("unsupported bakeoff query schema %q", set.SchemaVersion)
	}
	if len(set.Queries) == 0 {
		return fmt.Errorf("bakeoff query set is empty")
	}
	ids := map[string]bool{}
	for i, q := range set.Queries {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("query %d: id required", i+1)
		}
		if ids[q.ID] {
			return fmt.Errorf("query %q: duplicate id", q.ID)
		}
		ids[q.ID] = true
		if strings.TrimSpace(q.Lane) == "" {
			return fmt.Errorf("query %q: lane required", q.ID)
		}
		if strings.TrimSpace(q.Query) == "" {
			return fmt.Errorf("query %q: query required", q.ID)
		}
		if len(q.ExpectedClaimIDs) == 0 {
			return fmt.Errorf("query %q: expected_claim_ids required", q.ID)
		}
	}
	return nil
}

func bakeoffMetrics(queries []BakeoffQueryReport) BakeoffMetrics {
	if len(queries) == 0 {
		return BakeoffMetrics{}
	}
	var sum5, sum10, sumMRR, coveredRefs, totalRefs float64
	latencies := make([]time.Duration, 0, len(queries))
	for _, q := range queries {
		sum5 += q.RecallAt5
		sum10 += q.RecallAt10
		sumMRR += q.MRR
		coveredRefs += float64(intersectionCount(q.ReferenceClaimIDs, q.CandidateClaimIDs))
		totalRefs += float64(len(q.ReferenceClaimIDs))
		latencies = append(latencies, time.Duration(q.LatencyMS*float64(time.Millisecond)))
	}
	refCovered := 0.0
	if totalRefs > 0 {
		refCovered = coveredRefs / totalRefs
	}
	return BakeoffMetrics{
		RecallAt5:        sum5 / float64(len(queries)),
		RecallAt10:       sum10 / float64(len(queries)),
		MRR:              sumMRR / float64(len(queries)),
		ReferenceCovered: refCovered,
		LatencyP50MS:     durationMS(percentileDuration(latencies, 0.50)),
		LatencyP95MS:     durationMS(percentileDuration(latencies, 0.95)),
	}
}

func compareBakeoffPasses(first, second []BakeoffQueryReport) BakeoffDeterminism {
	d := BakeoffDeterminism{Passed: true}
	if len(first) != len(second) {
		return BakeoffDeterminism{Passed: false, Mismatches: []string{"query count differs"}}
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			d.Passed = false
			d.Mismatches = append(d.Mismatches, fmt.Sprintf("query %d id differs: %s != %s", i, first[i].ID, second[i].ID))
			continue
		}
		if !sameStrings(first[i].CandidateRankedClaimIDs, second[i].CandidateRankedClaimIDs) {
			d.Passed = false
			d.Mismatches = append(d.Mismatches, first[i].ID)
		}
	}
	return d
}

func doctorRankedClaimIDs(hits []EngineDoctorHit) []string {
	out := make([]string, 0, len(hits))
	seen := map[string]bool{}
	for _, hit := range hits {
		if hit.ClaimID == "" || seen[hit.ClaimID] {
			continue
		}
		seen[hit.ClaimID] = true
		out = append(out, hit.ClaimID)
	}
	return out
}

func missingWithinK(expected, ranked []string, k int) []string {
	if k > len(ranked) {
		k = len(ranked)
	}
	got := map[string]bool{}
	for _, id := range ranked[:k] {
		got[id] = true
	}
	var missing []string
	for _, id := range expected {
		if !got[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

func recallAtK(expected, ranked []string, k int) float64 {
	if len(expected) == 0 {
		return 0
	}
	return float64(len(expected)-len(missingWithinK(expected, ranked, k))) / float64(len(expected))
}

func reciprocalRank(expected, ranked []string) float64 {
	want := map[string]bool{}
	for _, id := range expected {
		want[id] = true
	}
	for i, id := range ranked {
		if want[id] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

func referenceCoveredFraction(reference, candidate []string) float64 {
	if len(reference) == 0 {
		return 1
	}
	return float64(intersectionCount(reference, candidate)) / float64(len(reference))
}

func intersectionCount(a, b []string) int {
	set := map[string]bool{}
	for _, id := range b {
		set[id] = true
	}
	n := 0
	for _, id := range a {
		if set[id] {
			n++
		}
	}
	return n
}

func percentileDuration(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), values...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	if p <= 0 {
		return cp[0]
	}
	if p >= 1 {
		return cp[len(cp)-1]
	}
	idx := int(p*float64(len(cp)-1) + 0.5)
	return cp[idx]
}

func durationMS(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

func uniqueNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func bakeoffLanes(queries []BakeoffQuery) []string {
	seen := map[string]bool{}
	var lanes []string
	for _, q := range queries {
		if q.Lane == "" || seen[q.Lane] {
			continue
		}
		seen[q.Lane] = true
		lanes = append(lanes, q.Lane)
	}
	sort.Strings(lanes)
	return lanes
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const defaultBakeoffQueriesYAML = `schema_version: compound.memory_bakeoff_queries.v0.1
queries:
  - id: exact-memory-spine
    lane: exact
    query: "memory spine compound ledger"
    expected_claim_ids:
      - claim_memory_spine
  - id: paraphrase-reference-engine
    lane: paraphrase
    query: "which baseline checks engine answers"
    expected_claim_ids:
      - claim_reference_engine
  - id: casual-no-mutation
    lane: casual
    query: "does recall mess with the real memory truth"
    expected_claim_ids:
      - claim_no_mutation
`
