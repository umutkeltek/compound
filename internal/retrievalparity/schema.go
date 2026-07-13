package retrievalparity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/reviewartifact"
)

const (
	FixtureSchemaVersion = "compound.parity_fixture.v0.1"
	ReportSchemaVersion  = "compound.parity_report.v0.1"
	ReviewArea           = "retrieval-parity"
)

type Coverage struct {
	WindowStart string   `json:"window_start,omitempty"`
	WindowEnd   string   `json:"window_end,omitempty"`
	KnownGaps   []string `json:"known_gaps"`
}

type NegativeControl struct {
	MaskedRef string `json:"masked_ref"`
}

type FixtureRow struct {
	Consumer        string           `json:"consumer"`
	Query           string           `json:"query"`
	ExpectedRefs    []string         `json:"expected_refs"`
	NegativeControl *NegativeControl `json:"negative_control,omitempty"`
}

type Fixture struct {
	SchemaVersion     string       `json:"schema_version"`
	ClassifierVersion string       `json:"classifier_version"`
	GeneratedAt       string       `json:"generated_at,omitempty"`
	Coverage          Coverage     `json:"coverage"`
	Rows              []FixtureRow `json:"rows"`
}

type PrivacyCheck struct {
	RawQueryExcluded bool `json:"raw_query_excluded"`
	QueryRefsHashed  bool `json:"query_refs_hashed"`
}

type NegativeControlResult struct {
	MaskedRef string `json:"masked_ref"`
	Observed  bool   `json:"observed"`
}

// ReplayRow deliberately contains no raw query field. QuerySHA256 is the only
// query identity that can leave the COMPOUND_HOME-only journal/fixture surface.
type ReplayRow struct {
	Consumer        string                 `json:"consumer"`
	QueryClass      string                 `json:"query_class"`
	QuerySHA256     string                 `json:"query_sha256"`
	ExpectedRefs    []string               `json:"expected_refs"`
	ResultRefs      []string               `json:"result_refs"`
	ResultCount     int                    `json:"result_count"`
	LostHits        []string               `json:"lost_hits"`
	ExtraHits       []string               `json:"extra_hits"`
	LatencyMS       float64                `json:"latency_ms"`
	EngineAvailable bool                   `json:"engine_available"`
	NegativeControl *NegativeControlResult `json:"negative_control,omitempty"`
	Passed          bool                   `json:"passed"`
	privacySafe     bool
}

type ClassSummary struct {
	Consumer   string  `json:"consumer"`
	QueryClass string  `json:"query_class"`
	Rows       int     `json:"rows"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	LostHits   int     `json:"lost_hits"`
	ExtraHits  int     `json:"extra_hits"`
	LatencyMS  float64 `json:"latency_ms"`
}

type Report struct {
	envelope.Header
	ClassifierVersion string         `json:"classifier_version"`
	FixtureRef        string         `json:"fixture_ref"`
	Consumer          string         `json:"consumer"`
	K                 int            `json:"k,omitempty"`
	Coverage          Coverage       `json:"coverage"`
	Rows              []ReplayRow    `json:"rows"`
	Classes           []ClassSummary `json:"classes"`
	Privacy           PrivacyCheck   `json:"privacy_check"`
	Passed            bool           `json:"passed"`
}

func LoadFixture(path string) (Fixture, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, "", err
	}
	var fixture Fixture
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return Fixture{}, "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Fixture{}, "", fmt.Errorf("parity fixture must contain exactly one JSON value")
	}
	fixture = normalizeFixture(fixture)
	if err := ValidateFixture(fixture); err != nil {
		return Fixture{}, "", err
	}
	return fixture, hashBytes(data), nil
}

func ValidateFixture(fixture Fixture) error {
	if fixture.SchemaVersion != FixtureSchemaVersion {
		return fmt.Errorf("unsupported parity fixture schema %q", fixture.SchemaVersion)
	}
	if fixture.ClassifierVersion != ClassifierVersion {
		return fmt.Errorf("unsupported query classifier %q", fixture.ClassifierVersion)
	}
	if len(fixture.Rows) == 0 {
		return fmt.Errorf("parity fixture is empty")
	}
	if fixture.GeneratedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, fixture.GeneratedAt); err != nil {
			return fmt.Errorf("fixture generated_at must be RFC3339Nano")
		}
	}
	if err := validateCoverage(fixture.Coverage); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, row := range fixture.Rows {
		if !validConsumer(row.Consumer) {
			return fmt.Errorf("fixture row %d: unsupported consumer %q", i+1, row.Consumer)
		}
		if strings.TrimSpace(row.Query) == "" && !consumerAllowsBlankQuery(row.Consumer) {
			return fmt.Errorf("fixture row %d: query required", i+1)
		}
		for j, ref := range row.ExpectedRefs {
			if !validFixtureRef(row.Consumer, ref) {
				return fmt.Errorf("fixture row %d: expected_ref %d invalid", i+1, j+1)
			}
		}
		key := row.Consumer + "\x00" + HashQuery(row.Query)
		if seen[key] {
			return fmt.Errorf("fixture row %d: duplicate consumer/query", i+1)
		}
		seen[key] = true
		if row.NegativeControl != nil {
			masked := strings.TrimSpace(row.NegativeControl.MaskedRef)
			if masked == "" {
				return fmt.Errorf("fixture row %d: negative_control.masked_ref required", i+1)
			}
			if !validFixtureRef(row.Consumer, masked) {
				return fmt.Errorf("fixture row %d: negative_control.masked_ref invalid", i+1)
			}
			if !contains(row.ExpectedRefs, masked) {
				return fmt.Errorf("fixture row %d: masked_ref must be an expected_ref", i+1)
			}
		}
	}
	return nil
}

func WriteFixture(homePath, name string, fixture Fixture) (string, error) {
	fixture = normalizeFixture(fixture)
	if err := ValidateFixture(fixture); err != nil {
		return "", err
	}
	return reviewartifact.WriteJSON(homePath, ReviewArea, name, fixture, "  ")
}

func normalizeFixture(fixture Fixture) Fixture {
	if fixture.ClassifierVersion == "" {
		fixture.ClassifierVersion = ClassifierVersion
	}
	fixture.GeneratedAt = strings.TrimSpace(fixture.GeneratedAt)
	fixture.Coverage.WindowStart = strings.TrimSpace(fixture.Coverage.WindowStart)
	fixture.Coverage.WindowEnd = strings.TrimSpace(fixture.Coverage.WindowEnd)
	if fixture.Coverage.KnownGaps == nil {
		fixture.Coverage.KnownGaps = []string{}
	} else {
		fixture.Coverage.KnownGaps = uniqueSorted(fixture.Coverage.KnownGaps)
	}
	for i := range fixture.Rows {
		fixture.Rows[i].Consumer = strings.TrimSpace(fixture.Rows[i].Consumer)
		fixture.Rows[i].ExpectedRefs = uniqueSorted(fixture.Rows[i].ExpectedRefs)
		if fixture.Rows[i].NegativeControl != nil {
			fixture.Rows[i].NegativeControl.MaskedRef = strings.TrimSpace(fixture.Rows[i].NegativeControl.MaskedRef)
		}
	}
	return fixture
}

// FreezeFromJournal records a deduplicated candidate baseline. Repeated rows
// union their result refs so the door does not silently discard intermittent
// pre-intake hits; an operator labels load-bearing refs on this evidence later.
func FreezeFromJournal(homePath string, windowDays int, out string, now time.Time) (Fixture, string, error) {
	if windowDays <= 0 {
		return Fixture{}, "", fmt.Errorf("window-days must be positive")
	}
	rows, gaps, err := loadJournal(homePath, true)
	if err != nil {
		return Fixture{}, "", err
	}
	windowEnd := now.UTC()
	windowStart := windowEnd.Add(-time.Duration(windowDays) * 24 * time.Hour)

	type aggregate struct {
		consumer string
		query    string
		refs     []string
	}
	groups := map[string]*aggregate{}
	seenConsumers := map[string]bool{}
	var earliest time.Time
	for _, row := range rows {
		ts, err := time.Parse(time.RFC3339Nano, row.Timestamp)
		if err != nil {
			gaps = append(gaps, "journal_timestamp_invalid")
			continue
		}
		if ts.Before(windowStart) || ts.After(windowEnd) {
			continue
		}
		if strings.TrimSpace(row.Query) == "" && !consumerAllowsBlankQuery(row.Consumer) {
			gaps = append(gaps, "journal_query_missing:"+row.Consumer)
			continue
		}
		if earliest.IsZero() || ts.Before(earliest) {
			earliest = ts
		}
		seenConsumers[row.Consumer] = true
		key := row.Consumer + "\x00" + row.QuerySHA256
		group := groups[key]
		if group == nil {
			group = &aggregate{consumer: row.Consumer, query: row.Query}
			groups[key] = group
		} else if group.query != row.Query {
			gaps = append(gaps, "query_hash_collision:"+row.QuerySHA256)
			continue
		}
		group.refs = append(group.refs, row.ResultRefs...)
	}
	if len(groups) == 0 {
		return Fixture{}, "", fmt.Errorf("no retrieval traffic in requested window")
	}
	if earliest.After(windowStart.Add(time.Hour)) {
		gaps = append(gaps, "observed_window_shorter_than_requested")
	}
	for _, consumer := range []string{ConsumerOrient, ConsumerRecall, ConsumerQuery, ConsumerSearch} {
		if !seenConsumers[consumer] {
			gaps = append(gaps, "consumer_no_traffic:"+consumer)
		}
	}

	fixture := Fixture{
		SchemaVersion:     FixtureSchemaVersion,
		ClassifierVersion: ClassifierVersion,
		GeneratedAt:       windowEnd.Format(time.RFC3339Nano),
		Coverage: Coverage{
			WindowStart: windowStart.Format(time.RFC3339Nano),
			WindowEnd:   windowEnd.Format(time.RFC3339Nano),
			KnownGaps:   uniqueSorted(gaps),
		},
		Rows: []FixtureRow{},
	}
	for _, group := range groups {
		fixture.Rows = append(fixture.Rows, FixtureRow{
			Consumer:     group.consumer,
			Query:        group.query,
			ExpectedRefs: uniqueSorted(group.refs),
		})
	}
	sort.Slice(fixture.Rows, func(i, j int) bool {
		if fixture.Rows[i].Consumer != fixture.Rows[j].Consumer {
			return fixture.Rows[i].Consumer < fixture.Rows[j].Consumer
		}
		return HashQuery(fixture.Rows[i].Query) < HashQuery(fixture.Rows[j].Query)
	})
	name, err := freezeOutputName(out, fixture)
	if err != nil {
		return Fixture{}, "", err
	}
	path, err := WriteFixture(homePath, name, fixture)
	if err != nil {
		return Fixture{}, "", err
	}
	return fixture, path, nil
}

func freezeOutputName(out string, fixture Fixture) (string, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		data, _ := json.Marshal(fixture)
		return "fixture_" + strings.TrimPrefix(hashBytes(data), "sha256:")[:12] + ".json", nil
	}
	if filepath.IsAbs(out) {
		return "", fmt.Errorf("out must be home-relative")
	}
	clean := filepath.Clean(out)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("out must stay under reviews/%s", ReviewArea)
	}
	if filepath.Dir(clean) != "." {
		wantDir := filepath.Join("reviews", ReviewArea)
		if filepath.Dir(clean) != wantDir {
			return "", fmt.Errorf("out must stay under %s", wantDir)
		}
	}
	name := filepath.Base(clean)
	if filepath.Ext(name) != ".json" {
		return "", fmt.Errorf("out must name a .json fixture")
	}
	return name, nil
}

func NewReplayRow(row FixtureRow, resultRefs []string, latency time.Duration, engineAvailable bool) ReplayRow {
	expected := uniqueSorted(row.ExpectedRefs)
	actual := uniqueSorted(resultRefs)
	lost := difference(expected, actual)
	extra := difference(actual, expected)
	reportExpected := persistedRefs(row.Consumer, expected)
	reportActual := persistedRefs(row.Consumer, actual)
	reportLost := persistedRefs(row.Consumer, lost)
	reportExtra := persistedRefs(row.Consumer, extra)
	privacySafe := validFixtureRefs(row.Consumer, expected) && validFixtureRefs(row.Consumer, actual) &&
		validPersistedRefs(row.Consumer, reportExpected) && validPersistedRefs(row.Consumer, reportActual) &&
		validPersistedRefs(row.Consumer, reportLost) && validPersistedRefs(row.Consumer, reportExtra)
	reportRow := ReplayRow{
		Consumer:        row.Consumer,
		QueryClass:      ClassifyQuery(row.Consumer, row.Query),
		QuerySHA256:     HashQuery(row.Query),
		ExpectedRefs:    reportExpected,
		ResultRefs:      reportActual,
		ResultCount:     len(reportActual),
		LostHits:        reportLost,
		ExtraHits:       reportExtra,
		LatencyMS:       float64(max(latency, 0)) / float64(time.Millisecond),
		EngineAvailable: engineAvailable,
		Passed:          len(lost) == 0,
		privacySafe:     privacySafe,
	}
	if row.NegativeControl != nil {
		masked := strings.TrimSpace(row.NegativeControl.MaskedRef)
		persistedMasked := persistedRef(row.Consumer, masked)
		reportRow.privacySafe = reportRow.privacySafe && validFixtureRef(row.Consumer, masked) && validPersistedRef(row.Consumer, persistedMasked)
		reportRow.NegativeControl = &NegativeControlResult{
			MaskedRef: persistedMasked,
			Observed:  contains(lost, masked),
		}
	}
	return reportRow
}

func BuildReport(fixtureRef, consumer string, k int, coverage Coverage, rows []ReplayRow, now time.Time) Report {
	coverage.KnownGaps = uniqueSorted(coverage.KnownGaps)
	if coverage.WindowStart == "" || coverage.WindowEnd == "" {
		coverage.KnownGaps = uniqueSorted(append(coverage.KnownGaps, "fixture_window_unrecorded"))
	}
	privacySafe := allRowsPrivacySafe(rows) && validateCoverage(coverage) == nil
	report := Report{
		Header: envelope.Header{
			SchemaVersion: ReportSchemaVersion,
			ID:            reviewartifact.ID("parity_", fixtureRef, consumer, now.UTC().Format(time.RFC3339Nano)),
			CreatedAt:     now.UTC().Format(time.RFC3339Nano),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			Scope:         envelope.Scope{Surface: ReviewArea},
			Authority:     envelope.Authority{SourceClass: "review", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{fixtureRef},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ClassifierVersion: ClassifierVersion,
		FixtureRef:        fixtureRef,
		Consumer:          consumer,
		K:                 k,
		Coverage:          coverage,
		Rows:              append([]ReplayRow(nil), rows...),
		Classes:           summarizeClasses(rows),
		Privacy:           PrivacyCheck{RawQueryExcluded: privacySafe, QueryRefsHashed: allRowsHashed(rows)},
		Passed:            true,
	}
	for _, row := range rows {
		if !row.Passed {
			report.Passed = false
			break
		}
	}
	return report
}

func WriteReport(homePath string, report Report) (string, error) {
	if report.SchemaVersion != ReportSchemaVersion {
		return "", fmt.Errorf("unsupported parity report schema %q", report.SchemaVersion)
	}
	if report.ID == "" {
		return "", fmt.Errorf("parity report id required")
	}
	if err := validateReport(report); err != nil {
		return "", err
	}
	return reviewartifact.WriteJSON(homePath, ReviewArea, report.ID+".json", report, "  ")
}

func validateReport(report Report) error {
	if report.ClassifierVersion != ClassifierVersion {
		return fmt.Errorf("unsupported query classifier %q", report.ClassifierVersion)
	}
	if !validSHARef(report.FixtureRef) {
		return fmt.Errorf("parity report fixture_ref invalid")
	}
	if !validConsumer(report.Consumer) {
		return fmt.Errorf("parity report consumer invalid")
	}
	if len(report.Rows) == 0 {
		return fmt.Errorf("parity report rows required")
	}
	if (report.Consumer == ConsumerQuery || report.Consumer == ConsumerSearch) && report.K <= 0 {
		return fmt.Errorf("parity report k required for ranked consumer")
	}
	if (report.Consumer == ConsumerOrient || report.Consumer == ConsumerRecall) && report.K != 0 {
		return fmt.Errorf("parity report k must be omitted for unranked consumer")
	}
	if err := validateCoverage(report.Coverage); err != nil {
		return err
	}
	for i, row := range report.Rows {
		if row.Consumer != report.Consumer || !validQueryClass(row.Consumer, row.QueryClass) {
			return fmt.Errorf("parity report row %d classification invalid", i+1)
		}
		if !validSHARef(row.QuerySHA256) {
			return fmt.Errorf("parity report row %d query_sha256 invalid", i+1)
		}
		for _, refs := range [][]string{row.ExpectedRefs, row.ResultRefs, row.LostHits, row.ExtraHits} {
			if !validPersistedRefs(row.Consumer, refs) {
				return fmt.Errorf("parity report row %d reference invalid", i+1)
			}
		}
		if row.NegativeControl != nil && !validPersistedRef(row.Consumer, row.NegativeControl.MaskedRef) {
			return fmt.Errorf("parity report row %d negative_control invalid", i+1)
		}
	}
	for i, class := range report.Classes {
		if class.Consumer != report.Consumer || !validQueryClass(class.Consumer, class.QueryClass) {
			return fmt.Errorf("parity report class %d classification invalid", i+1)
		}
	}
	if !report.Privacy.RawQueryExcluded || !report.Privacy.QueryRefsHashed || !allRowsHashed(report.Rows) {
		return fmt.Errorf("parity report privacy check failed")
	}
	return nil
}

func summarizeClasses(rows []ReplayRow) []ClassSummary {
	byClass := map[string]*ClassSummary{}
	for _, row := range rows {
		key := row.Consumer + "\x00" + row.QueryClass
		summary := byClass[key]
		if summary == nil {
			summary = &ClassSummary{Consumer: row.Consumer, QueryClass: row.QueryClass}
			byClass[key] = summary
		}
		summary.Rows++
		if row.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.LostHits += len(row.LostHits)
		summary.ExtraHits += len(row.ExtraHits)
		summary.LatencyMS += row.LatencyMS
	}
	out := make([]ClassSummary, 0, len(byClass))
	for _, summary := range byClass {
		if summary.Rows > 0 {
			summary.LatencyMS /= float64(summary.Rows)
		}
		out = append(out, *summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Consumer != out[j].Consumer {
			return out[i].Consumer < out[j].Consumer
		}
		return out[i].QueryClass < out[j].QueryClass
	})
	return out
}

func allRowsHashed(rows []ReplayRow) bool {
	for _, row := range rows {
		if !validSHARef(row.QuerySHA256) {
			return false
		}
	}
	return true
}

func allRowsPrivacySafe(rows []ReplayRow) bool {
	for _, row := range rows {
		if !row.privacySafe {
			return false
		}
	}
	return true
}

func validFixtureRefs(consumer string, refs []string) bool {
	for _, ref := range refs {
		if !validFixtureRef(consumer, ref) {
			return false
		}
	}
	return true
}

func consumerAllowsBlankQuery(consumer string) bool {
	return consumer == ConsumerOrient || consumer == ConsumerRecall
}

func validFixtureRef(consumer, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 512 || containsControl(ref) {
		return false
	}
	claimRef := strings.HasPrefix(ref, "claim://") && validOpaqueID(strings.TrimPrefix(ref, "claim://"))
	switch consumer {
	case ConsumerQuery:
		return claimRef
	case ConsumerOrient:
		return claimRef || validPositionRef(ref)
	case ConsumerRecall:
		return claimRef || validOpaqueID(ref)
	case ConsumerSearch:
		return validSearchChunkRef(ref)
	default:
		return false
	}
}

func persistedRefs(consumer string, refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, persistedRef(consumer, ref))
	}
	return uniqueSorted(out)
}

func persistedRef(consumer, ref string) string {
	ref = strings.TrimSpace(ref)
	if (consumer == ConsumerOrient && validPositionRef(ref)) || (consumer == ConsumerSearch && validSearchChunkRef(ref)) {
		return hashBytes([]byte(ref))
	}
	if strings.HasPrefix(ref, "claim://") {
		claimID := strings.TrimPrefix(ref, "claim://")
		if validOpaqueID(claimID) && !validCanonicalID(claimID, "claim_") {
			return hashBytes([]byte(ref))
		}
	}
	if consumer == ConsumerRecall && validOpaqueID(ref) && !validCanonicalID(ref, "cand_") {
		return hashBytes([]byte(ref))
	}
	return ref
}

func validPersistedRefs(consumer string, refs []string) bool {
	for _, ref := range refs {
		if !validPersistedRef(consumer, ref) {
			return false
		}
	}
	return true
}

func validPersistedRef(consumer, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 512 || containsControl(ref) {
		return false
	}
	claimRef := strings.HasPrefix(ref, "claim://") && validCanonicalID(strings.TrimPrefix(ref, "claim://"), "claim_")
	switch consumer {
	case ConsumerQuery:
		return claimRef || validSHARef(ref)
	case ConsumerOrient:
		return claimRef || validSHARef(ref)
	case ConsumerRecall:
		return claimRef || validCanonicalID(ref, "cand_") || validSHARef(ref)
	case ConsumerSearch:
		return validSHARef(ref)
	default:
		return false
	}
}

func validCanonicalID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && validHex(strings.TrimPrefix(value, prefix), 12)
}

func validPositionRef(ref string) bool {
	const prefix = "position://"
	if !strings.HasPrefix(ref, prefix) {
		return false
	}
	payload := strings.TrimPrefix(ref, prefix)
	marker := strings.LastIndex(payload, "::slot-")
	if marker <= 0 {
		return false
	}
	return validHex(payload[marker+len("::slot-"):], 12)
}

func validOpaqueID(ref string) bool {
	if ref == "" || len(ref) > 512 || containsControl(ref) || strings.IndexFunc(ref, func(r rune) bool { return r == ' ' || r == '\t' }) >= 0 {
		return false
	}
	base := ref
	if chunk := strings.LastIndex(base, "#"); chunk >= 0 {
		if chunk == 0 || !validDecimal(base[chunk+1:], true) {
			return false
		}
		base = base[:chunk]
	}
	separator := strings.LastIndex(base, "_")
	if separator <= 0 || !validHex(base[separator+1:], 12) {
		return false
	}
	return validSafeID(base[:separator])
}

func validSearchChunkRef(ref string) bool {
	separator := strings.LastIndex(ref, "#")
	if separator <= 0 || !validDecimal(ref[separator+1:], true) {
		return false
	}
	return validSafeID(ref[:separator])
}

func validSafeID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func validQueryClass(consumer, queryClass string) bool {
	prefix := consumer + "/tokens:"
	if !strings.HasPrefix(queryClass, prefix) {
		return false
	}
	switch strings.TrimPrefix(queryClass, prefix) {
	case "0", "1", "2-4", "5-12", "13+":
		return true
	default:
		return false
	}
}

func validateCoverage(coverage Coverage) error {
	startText := strings.TrimSpace(coverage.WindowStart)
	endText := strings.TrimSpace(coverage.WindowEnd)
	if (startText == "") != (endText == "") {
		return fmt.Errorf("parity coverage window_start and window_end must be paired")
	}
	if startText != "" {
		start, startErr := time.Parse(time.RFC3339Nano, startText)
		end, endErr := time.Parse(time.RFC3339Nano, endText)
		if startErr != nil || endErr != nil {
			return fmt.Errorf("parity coverage window must be RFC3339Nano")
		}
		if start.After(end) {
			return fmt.Errorf("parity coverage window_start must not follow window_end")
		}
	}
	for i, gap := range coverage.KnownGaps {
		if !validCoverageGap(gap) {
			return fmt.Errorf("parity coverage known_gap %d invalid", i+1)
		}
	}
	return nil
}

func validCoverageGap(gap string) bool {
	switch gap {
	case "observed_window_shorter_than_requested",
		"journal_timestamp_invalid",
		"fixture_window_unrecorded",
		"orient_cwd_unavailable",
		"orient_input_unavailable",
		"orient_retrieval_error",
		"query_engine_unavailable",
		"query_engine_refused",
		"query_retrieval_error":
		return true
	}
	for _, prefix := range []string{"malformed_journal_row:", "invalid_journal_row:"} {
		if strings.HasPrefix(gap, prefix) {
			return validDecimal(strings.TrimPrefix(gap, prefix), false)
		}
	}
	for _, prefix := range []string{"journal_query_missing:", "consumer_no_traffic:"} {
		if strings.HasPrefix(gap, prefix) {
			return validConsumer(strings.TrimPrefix(gap, prefix))
		}
	}
	if strings.HasPrefix(gap, "query_hash_collision:") {
		return validSHARef(strings.TrimPrefix(gap, "query_hash_collision:"))
	}
	return false
}

func validDecimal(value string, allowZero bool) bool {
	n, err := strconv.Atoi(value)
	if err != nil {
		return false
	}
	if allowZero {
		return n >= 0 && strconv.Itoa(n) == value
	}
	return n > 0 && strconv.Itoa(n) == value
}

func validSHARef(ref string) bool {
	if !strings.HasPrefix(ref, "sha256:") || len(ref) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(ref, "sha256:"))
	return err == nil
}

func difference(left, right []string) []string {
	rightSet := map[string]bool{}
	for _, value := range right {
		rightSet[value] = true
	}
	out := []string{}
	for _, value := range left {
		if !rightSet[value] {
			out = append(out, value)
		}
	}
	return uniqueSorted(out)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
