package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/retrievalparity"
)

const (
	parityCLIHelperEnv       = "COMPOUND_TEST_PARITY_CLI_HELPER"
	parityCLIHelperFixture   = "COMPOUND_TEST_PARITY_CLI_FIXTURE"
	parityCLIHelperConsumer  = "COMPOUND_TEST_PARITY_CLI_CONSUMER"
	parityFailureHelperEnv   = "COMPOUND_TEST_PARITY_FAILURE_HELPER"
	parityFailureHelperQuery = "COMPOUND_TEST_PARITY_FAILURE_QUERY"
	parityFakeSidecarEnv     = "COMPOUND_TEST_PARITY_FAKE_SIDECAR"
	parityFakeSidecarHitID   = "COMPOUND_TEST_PARITY_FAKE_HIT_ID"
	parityFakeSidecarTestRun = "^TestParityFakeBreadthSidecar$"
)

func TestParityReplayCLIHelper(t *testing.T) {
	if os.Getenv(parityCLIHelperEnv) != "1" {
		return
	}
	cmdMemory([]string{
		"parity-replay",
		"--fixture", os.Getenv(parityCLIHelperFixture),
		"--consumer", os.Getenv(parityCLIHelperConsumer),
		"--json",
	})
	os.Exit(0)
}

func TestParityFailureCLIHelper(t *testing.T) {
	switch os.Getenv(parityFailureHelperEnv) {
	case retrievalparity.ConsumerRecall:
		cmdMemory([]string{"recall", os.Getenv(parityFailureHelperQuery), "--json"})
	case retrievalparity.ConsumerSearch:
		cmdMemory([]string{"search", "--lane", "corpus-fixture", os.Getenv(parityFailureHelperQuery), "--json"})
	default:
		return
	}
	os.Exit(0)
}

func TestParityFakeBreadthSidecar(t *testing.T) {
	if os.Getenv(parityFakeSidecarEnv) != "1" {
		return
	}
	hitID := os.Getenv(parityFakeSidecarHitID)
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	var corpusGen int64
	for {
		var req mem.Request
		if err := dec.Decode(&req); err != nil {
			os.Exit(0)
		}
		resp := mem.Response{
			OK:            true,
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     req.RequestID,
			Name:          "parity-test-breadth",
			Breadth:       true,
			Ready:         true,
			CorpusGen:     corpusGen,
		}
		switch req.Op {
		case mem.OpCapabilities:
		case mem.OpIndex:
			corpusGen = req.CorpusGen
			resp.CorpusGen = corpusGen
			resp.Indexed = len(req.Claims)
		case mem.OpQuery:
			if hitID != "" && slices.Contains(req.Allowlist, hitID) {
				resp.Hits = []mem.Hit{{ClaimID: hitID, Score: 1}}
			}
		default:
			resp.OK = false
			resp.Error = &mem.WireError{Code: mem.CodeBadRequest, Message: "unknown operation"}
		}
		if err := enc.Encode(resp); err != nil {
			os.Exit(0)
		}
	}
}

func TestParityReplayNegativeControlMasksData(t *testing.T) {
	now := time.Now().UTC()
	query := "synthetic retrieval answer baseline anchor"
	unmaskedHome := newParityTestHome(t, now)
	wantRef := seedParityClaim(t, unmaskedHome, "synthetic retrieval answer", now)
	fixturePath := writeParityFixture(t, unmaskedHome, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:     retrievalparity.ConsumerQuery,
			Query:        query,
			ExpectedRefs: []string{wantRef},
		}},
	})

	unmaskedExit, unmaskedReport := runParityReplayCLI(t, unmaskedHome, fixturePath, retrievalparity.ConsumerQuery)
	if unmaskedExit != 0 || !unmaskedReport.ParityPassed || unmaskedReport.NegativeControlValid != nil || len(unmaskedReport.Rows) != 1 || len(unmaskedReport.Rows[0].LostHits) != 0 {
		t.Fatalf("unmasked replay exit=%d report=%+v, want green with no lost hits", unmaskedExit, unmaskedReport)
	}

	controlFixturePath := writeParityFixtureNamed(t, unmaskedHome, "synthetic-control-fixture.json", retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:     retrievalparity.ConsumerQuery,
			Query:        query,
			ExpectedRefs: []string{wantRef},
			NegativeControl: &retrievalparity.NegativeControl{
				MaskedRef: wantRef,
			},
		}},
	})
	failedControlExit, failedControlReport := runParityReplayCLI(t, unmaskedHome, controlFixturePath, retrievalparity.ConsumerQuery)
	if failedControlExit == 0 || !failedControlReport.ParityPassed || failedControlReport.NegativeControlValid == nil || *failedControlReport.NegativeControlValid {
		t.Fatalf("failed control replay exit=%d report=%+v, want nonzero with negative_control_valid=false", failedControlExit, failedControlReport)
	}
	maskedHome := cloneParityHomeWithoutClaim(t, unmaskedHome, strings.TrimPrefix(wantRef, "claim://"), now)
	maskedExit, maskedReport := runParityReplayCLI(t, maskedHome, controlFixturePath, retrievalparity.ConsumerQuery)
	if maskedExit == 0 || maskedReport.ParityPassed || maskedReport.NegativeControlValid == nil || !*maskedReport.NegativeControlValid || len(maskedReport.Rows) != 1 {
		t.Fatalf("masked replay exit=%d report=%+v, want nonzero data-level loss", maskedExit, maskedReport)
	}
	if !slices.Contains(maskedReport.Rows[0].LostHits, wantRef) {
		t.Fatalf("masked replay lost_hits=%v, want %s", maskedReport.Rows[0].LostHits, wantRef)
	}
	t.Logf("f1 unmasked_exit=%d masked_exit=%d masked_lost_hits=%v", unmaskedExit, maskedExit, maskedReport.Rows[0].LostHits)
}

func TestParityReplayOrientMatchesProductionContextPack(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	wantRef := seedParityClaim(t, homePath, "token-disjoint expected marker", now)
	query := "unrelated semantic probe"
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion: retrievalparity.FixtureSchemaVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:     retrievalparity.ConsumerOrient,
			Query:        query,
			OrientInput:  parityTestOrientInput(t, homePath),
			ExpectedRefs: []string{wantRef},
		}},
	})

	t.Setenv(parityFakeSidecarEnv, "1")
	t.Setenv(parityFakeSidecarHitID, strings.TrimPrefix(wantRef, "claim://"))
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", os.Args[0]+" -test.run="+parityFakeSidecarTestRun)

	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FoldPositions(mem.PositionFoldOptions{}, now); err != nil {
		t.Fatalf("fold positions for production-path parity: %v", err)
	}
	report, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerOrient,
	}, now)
	if err != nil || code != 0 {
		t.Fatalf("replay code=%d err=%v report=%+v", code, err, report)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	productionJSON := captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", cwd, "--task", query})
	})
	var production envelope.ContextPack
	if err := json.Unmarshal([]byte(productionJSON), &production); err != nil {
		t.Fatalf("parse production ContextPack: %v\n%s", err, productionJSON)
	}
	if !slices.Contains(production.ActiveMemory, wantRef) {
		t.Fatalf("production semantic path did not surface fake-sidecar hit %s: %v", wantRef, production.ActiveMemory)
	}
	normalizedProduction, hasPosition := parityPersistedOrientRefs(production.ActiveMemory)
	if !hasPosition {
		t.Fatalf("production ContextPack did not exercise position refs: %v", production.ActiveMemory)
	}
	if len(report.Rows) != 1 || !sameStringSet(report.Rows[0].ResultRefs, normalizedProduction) {
		t.Fatalf("replay refs=%v production refs=%v", report.Rows[0].ResultRefs, production.ActiveMemory)
	}
	t.Logf("f2 replay_refs=%v production_refs_normalized=%v", report.Rows[0].ResultRefs, normalizedProduction)
}

func TestParityReplayEngineAvailabilityFollowsConsumerFallback(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "synthetic available engine marker"
	wantRef := seedParityClaim(t, homePath, query, now)
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{
			{Consumer: retrievalparity.ConsumerOrient, Query: query, OrientInput: parityTestOrientInput(t, homePath), ExpectedRefs: []string{wantRef}},
			{Consumer: retrievalparity.ConsumerQuery, Query: query, ExpectedRefs: []string{wantRef}},
		},
	})
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}

	queryAvailable, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
	}, now)
	if err != nil || code != 0 || len(queryAvailable.Rows) != 1 || !queryAvailable.Rows[0].EngineAvailable {
		t.Fatalf("lexical query replay code=%d err=%v report=%+v", code, err, queryAvailable)
	}

	t.Setenv("COMPOUND_MEM_ENGINE_BIN", filepath.Join(t.TempDir(), "missing-engine"))
	orientFallback, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerOrient,
	}, now.Add(time.Second))
	if err != nil || code != 0 || len(orientFallback.Rows) != 1 || !orientFallback.Rows[0].EngineAvailable {
		t.Fatalf("orient fallback replay code=%d err=%v report=%+v", code, err, orientFallback)
	}
	if !slices.Contains(orientFallback.Rows[0].ResultRefs, wantRef) {
		t.Fatalf("orient fallback refs=%v, want %s", orientFallback.Rows[0].ResultRefs, wantRef)
	}

	queryUnavailable, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
	}, now.Add(2*time.Second))
	if err != nil || code == 0 || len(queryUnavailable.Rows) != 1 || queryUnavailable.Rows[0].EngineAvailable {
		t.Fatalf("unavailable query replay code=%d err=%v report=%+v", code, err, queryUnavailable)
	}
	if !slices.Contains(queryUnavailable.Rows[0].LostHits, wantRef) || !slices.Contains(queryUnavailable.Coverage.KnownGaps, "query_engine_unavailable") {
		t.Fatalf("unavailable query loss/gaps=%v/%v, want %s and query_engine_unavailable", queryUnavailable.Rows[0].LostHits, queryUnavailable.Coverage.KnownGaps, wantRef)
	}
}

func TestRetrievalJournalCLIAndBestEffortFailure(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "synthetic journal retrieval marker"
	seedParityClaim(t, homePath, query, now)

	captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--task", query})
	})
	healthyQueryOut := captureStdout(t, func() {
		cmdMemory([]string{"query", query})
	})

	rows, err := retrievalparity.LoadJournal(homePath)
	if err != nil {
		t.Fatalf("load retrieval journal: %v", err)
	}
	byConsumer := map[string]retrievalparity.Row{}
	for _, row := range rows {
		if row.Consumer == retrievalparity.ConsumerOrient || row.Consumer == retrievalparity.ConsumerQuery {
			byConsumer[row.Consumer] = row
		}
	}
	for _, consumer := range []string{retrievalparity.ConsumerOrient, retrievalparity.ConsumerQuery} {
		row, ok := byConsumer[consumer]
		if !ok {
			t.Fatalf("journal consumers=%v, missing %s", byConsumer, consumer)
		}
		if row.QueryClass != retrievalparity.ClassifyQuery(consumer, query) || row.ClassifierVersion != retrievalparity.ClassifierVersion {
			t.Fatalf("journal row classification=%+v", row)
		}
		if !validParitySHA(row.QuerySHA256) || row.LatencyMS < 0 || row.Timestamp == "" || row.ResultCount != len(row.ResultRefs) || !row.EngineAvailable {
			t.Fatalf("journal row incomplete=%+v", row)
		}
		if _, err := time.Parse(time.RFC3339Nano, row.Timestamp); err != nil {
			t.Fatalf("journal timestamp %q: %v", row.Timestamp, err)
		}
	}

	journalPath := filepath.Join(homePath, filepath.FromSlash(retrievalparity.JournalRelPath))
	if err := os.Rename(journalPath, journalPath+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	brokenQueryOut, brokenErr := captureParityOutput(t, func() {
		cmdMemory([]string{"query", query})
	})
	if brokenQueryOut != healthyQueryOut {
		t.Fatalf("journal failure changed consumer stdout\nhealthy=%q\nbroken=%q", healthyQueryOut, brokenQueryOut)
	}
	if got := strings.Count(brokenErr, "retrieval journal warning:"); got != 1 {
		t.Fatalf("journal failure warnings=%d, want 1; stderr=%q", got, brokenErr)
	}
	if strings.Contains(brokenErr, query) {
		t.Fatalf("journal warning leaked raw query: %q", brokenErr)
	}
	t.Logf("f3 journal_rows=%d consumers=%v best_effort_stdout_equal=%t warnings=1", len(rows), []string{retrievalparity.ConsumerOrient, retrievalparity.ConsumerQuery}, brokenQueryOut == healthyQueryOut)
}

func TestRecallFailureJournalsUnavailableObservationBeforeExit(t *testing.T) {
	homePath := newParityTestHome(t, time.Now().UTC())
	query := "synthetic recall failure marker"
	claimsPath := filepath.Join(homePath, "ledger", "claims.jsonl")
	if err := os.WriteFile(claimsPath, []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exitCode, output := runParityFailureCLI(t, homePath, retrievalparity.ConsumerRecall, query)
	if exitCode != 1 || !strings.Contains(output, "error:") {
		t.Fatalf("recall failure exit=%d output=%q, want existing exit 1 and error output", exitCode, output)
	}
	assertUnavailableParityRow(t, homePath, retrievalparity.ConsumerRecall, query)
}

func TestSearchFailureJournalsUnavailableObservationBeforeExit(t *testing.T) {
	homePath := newParityTestHome(t, time.Now().UTC())
	query := "synthetic corpus failure marker"
	configPath := memoryEngineConfigPath(homePath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `schema_version: compound.memory_engine_config.v0.1
corpus_lanes:
  - lane: corpus-fixture
    source_family: synthetic_import
    privacy: private
    embedder:
      provider: ollama
      model: fixture-embed
      dims: 3
      bits: f32
    chunk:
      unit: runes
      size: 64
      overlap: 8
      boundary: structure
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	exitCode, output := runParityFailureCLI(t, homePath, retrievalparity.ConsumerSearch, query)
	if exitCode != 1 || !strings.Contains(output, "memory search:") {
		t.Fatalf("search failure exit=%d output=%q, want existing exit 1 and error output", exitCode, output)
	}
	assertUnavailableParityRow(t, homePath, retrievalparity.ConsumerSearch, query)
}

func TestParityReportPrivacy(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "synthetic journal-only privacy sentinel"
	wantRef := seedParityClaim(t, homePath, query, now)
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:     retrievalparity.ConsumerQuery,
			Query:        query,
			ExpectedRefs: []string{wantRef},
		}},
	})
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	report, reportPath, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
	}, now)
	if err != nil || code != 0 {
		t.Fatalf("replay code=%d err=%v report=%+v", code, err, report)
	}
	persisted, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), query) {
		t.Fatalf("persisted report leaked raw query: %s", persisted)
	}
	if report.FixtureRef == "" || !validParitySHA(report.FixtureRef) || !report.Privacy.RawQueryExcluded || !report.Privacy.QueryRefsHashed {
		t.Fatalf("report privacy metadata=%+v fixture_ref=%q", report.Privacy, report.FixtureRef)
	}
	for _, row := range report.Rows {
		if !validParitySHA(row.QuerySHA256) {
			t.Fatalf("report query ref=%q, want sha256", row.QuerySHA256)
		}
		if row.Consumer == retrievalparity.ConsumerOrient && !validParitySHA(row.InputSHA256) {
			t.Fatalf("orient report input ref=%q, want sha256", row.InputSHA256)
		}
		if row.Consumer != retrievalparity.ConsumerOrient && row.InputSHA256 != "" {
			t.Fatalf("%s report carried orient-only input ref=%q", row.Consumer, row.InputSHA256)
		}
	}
	t.Logf("f4 report=%s raw_query_present=%t query_ref=%s", filepath.Base(reportPath), strings.Contains(string(persisted), query), report.Rows[0].QuerySHA256)
}

func TestParityReportHashesSemanticOrientRefs(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "fixture:retrieval-parity"
	semanticRef := "position://" + query + "::baseline_anchor::slot-aaaaaaaaaaaa"
	fixtureRow := retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerOrient,
		Query:        query,
		OrientInput:  parityTestOrientInput(t, homePath),
		ExpectedRefs: []string{semanticRef},
	}
	replayRow := retrievalparity.NewReplayRow(fixtureRow, []string{semanticRef}, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic orient fixture"),
		retrievalparity.ConsumerOrient,
		0,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{replayRow},
		now,
	)
	reportPath, err := retrievalparity.WriteReport(homePath, report)
	if err != nil {
		t.Fatalf("write orient report: %v", err)
	}
	persisted, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), query) {
		t.Fatalf("persisted orient report leaked raw task: %s", persisted)
	}
	if !slices.Contains(report.Rows[0].ResultRefs, retrievalparity.HashQuery(semanticRef)) {
		t.Fatalf("semantic orient ref was not hashed: %+v", report.Rows[0])
	}
}

func TestParityReportHashesSemanticSearchRefs(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "item_a"
	chunkRef := query + "#0"
	fixtureRow := retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerSearch,
		Query:        query,
		ExpectedRefs: []string{chunkRef},
	}
	replayRow := retrievalparity.NewReplayRow(fixtureRow, []string{chunkRef}, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic search fixture"),
		retrievalparity.ConsumerSearch,
		10,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{replayRow},
		now,
	)
	reportPath, err := retrievalparity.WriteReport(homePath, report)
	if err != nil {
		t.Fatalf("write search report: %v", err)
	}
	persisted, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), query) {
		t.Fatalf("persisted search report leaked raw query: %s", persisted)
	}
	if !slices.Contains(report.Rows[0].ResultRefs, retrievalparity.HashQuery(chunkRef)) {
		t.Fatalf("semantic search ref was not hashed: %+v", report.Rows[0])
	}
}

func TestParityReportHashesNonCanonicalOpaqueRefs(t *testing.T) {
	now := time.Now().UTC()
	query := "syntheticsentinel"
	tests := []struct {
		name     string
		consumer string
		k        int
		ref      string
	}{
		{name: "claim", consumer: retrievalparity.ConsumerQuery, k: 10, ref: "claim://" + query + "_aaaaaaaaaaaa"},
		{name: "candidate", consumer: retrievalparity.ConsumerRecall, ref: query + "_aaaaaaaaaaaa"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homePath := newParityTestHome(t, now)
			fixtureRow := retrievalparity.FixtureRow{
				Consumer:     tt.consumer,
				Query:        query,
				ExpectedRefs: []string{tt.ref},
			}
			replayRow := retrievalparity.NewReplayRow(fixtureRow, []string{tt.ref}, time.Millisecond, true)
			report := retrievalparity.BuildReport(
				retrievalparity.HashQuery("synthetic opaque fixture"),
				tt.consumer,
				tt.k,
				retrievalparity.Coverage{},
				[]retrievalparity.ReplayRow{replayRow},
				now,
			)
			reportPath, err := retrievalparity.WriteReport(homePath, report)
			if err != nil {
				t.Fatalf("write opaque report: %v", err)
			}
			persisted, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(persisted), query) {
				t.Fatalf("persisted opaque report leaked raw query: %s", persisted)
			}
			if !slices.Contains(report.Rows[0].ResultRefs, retrievalparity.HashQuery(tt.ref)) {
				t.Fatalf("noncanonical opaque ref was not hashed: %+v", report.Rows[0])
			}
		})
	}
}

func TestParityFixtureRejectsUnknownFields(t *testing.T) {
	homePath := newParityTestHome(t, time.Now().UTC())
	fixturePath := filepath.Join(homePath, "unknown-field-fixture.json")
	raw := `{"schema_version":"` + retrievalparity.FixtureSchemaVersion + `","classifier_version":"` + retrievalparity.ClassifierVersion + `","coverage":{"known_gaps":[]},"rows":[{"consumer":"query","query":"synthetic strict fixture","expected_ref":"claim://claim_aaaaaaaaaaaa"}]}`
	if err := os.WriteFile(fixturePath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := retrievalparity.LoadFixture(fixturePath); err == nil {
		t.Fatal("fixture with misspelled expected_refs field loaded successfully")
	}
}

func TestParityReportRejectsWrongClassifierVersion(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	fixtureRow := retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerQuery,
		Query:        "synthetic classifier report",
		ExpectedRefs: []string{"claim://claim_aaaaaaaaaaaa"},
	}
	replayRow := retrievalparity.NewReplayRow(fixtureRow, fixtureRow.ExpectedRefs, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic classifier fixture"),
		retrievalparity.ConsumerQuery,
		10,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{replayRow},
		now,
	)
	report.ClassifierVersion = ""
	if _, err := retrievalparity.WriteReport(homePath, report); err == nil {
		t.Fatal("report with empty classifier_version persisted")
	}
}

func TestParitySchemaSeparatesHomeRefsFromReportRefs(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	positionRef := "position://fixture:parity::baseline_anchor::slot-aaaaaaaaaaaa"
	searchRef := "item_a#0"
	fixture := retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{
			{Consumer: retrievalparity.ConsumerOrient, Query: "synthetic orient", OrientInput: parityTestOrientInput(t, homePath), ExpectedRefs: []string{positionRef}},
			{Consumer: retrievalparity.ConsumerSearch, Query: "synthetic search", ExpectedRefs: []string{searchRef}},
		},
	}
	if _, err := retrievalparity.WriteFixture(homePath, "typed-raw-refs.json", fixture); err != nil {
		t.Fatalf("raw production refs should be valid in home-local fixture: %v", err)
	}
	fixture.Rows[0].ExpectedRefs = []string{retrievalparity.HashQuery(positionRef)}
	if _, err := retrievalparity.WriteFixture(homePath, "prehashed-fixture.json", fixture); err == nil {
		t.Fatal("pre-hashed orient ref should not be valid in fixture")
	}

	row := retrievalparity.NewReplayRow(retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerOrient,
		Query:        "synthetic orient",
		OrientInput:  parityTestOrientInput(t, homePath),
		ExpectedRefs: []string{positionRef},
	}, []string{positionRef}, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic validator fixture"),
		retrievalparity.ConsumerOrient,
		0,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{row},
		now,
	)
	report.Rows[0].ExpectedRefs = []string{positionRef}
	report.Rows[0].ResultRefs = []string{positionRef}
	if _, err := retrievalparity.WriteReport(homePath, report); err == nil {
		t.Fatal("raw semantic refs should not be valid in persisted report")
	}
}

func TestParityReplayRejectsUntypedReportFields(t *testing.T) {
	now := time.Now().UTC()
	query := "synthetic adversarial privacy sentinel"
	tests := []struct {
		name    string
		fixture retrievalparity.Fixture
	}{
		{
			name: "expected ref",
			fixture: retrievalparity.Fixture{
				SchemaVersion:     retrievalparity.FixtureSchemaVersion,
				ClassifierVersion: retrievalparity.ClassifierVersion,
				Rows: []retrievalparity.FixtureRow{{
					Consumer:     retrievalparity.ConsumerQuery,
					Query:        query,
					ExpectedRefs: []string{query},
				}},
			},
		},
		{
			name: "coverage gap",
			fixture: retrievalparity.Fixture{
				SchemaVersion:     retrievalparity.FixtureSchemaVersion,
				ClassifierVersion: retrievalparity.ClassifierVersion,
				Coverage: retrievalparity.Coverage{
					KnownGaps: []string{query},
				},
				Rows: []retrievalparity.FixtureRow{{
					Consumer:     retrievalparity.ConsumerQuery,
					Query:        query,
					ExpectedRefs: []string{"claim://claim_000000000000"},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homePath := newParityTestHome(t, now)
			fixturePath := filepath.Join(homePath, "adversarial-fixture.json")
			data, err := json.Marshal(tt.fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := mem.Open(homePath)
			if err != nil {
				t.Fatal(err)
			}
			_, reportPath, code, replayErr := runMemoryParityReplay(homePath, store, []string{
				"--fixture", fixturePath,
				"--consumer", retrievalparity.ConsumerQuery,
			}, now)
			if replayErr == nil || code != 1 || reportPath != "" {
				t.Fatalf("replay code=%d report=%q err=%v, want rejected fixture with no report", code, reportPath, replayErr)
			}
			if strings.Contains(replayErr.Error(), query) {
				t.Fatalf("fixture rejection leaked raw query: %q", replayErr)
			}
			matches, err := filepath.Glob(filepath.Join(homePath, "reviews", retrievalparity.ReviewArea, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != 0 {
				t.Fatalf("rejected fixture persisted report artifacts: %v", matches)
			}
		})
	}
}

func TestParityFreezeBuildsFixtureFromJournal(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "synthetic repeated freeze query"
	claimA := "claim://claim_aaaaaaaaaaaa"
	claimB := "claim://claim_bbbbbbbbbbbb"
	searchChunk := "item_a#0"
	orientInput := parityTestOrientInput(t, homePath)
	retrievalparity.Record(homePath, retrievalparity.NewRow(retrievalparity.ConsumerQuery, query, []string{claimA}, 2*time.Millisecond, true, now.Add(-2*time.Hour)))
	retrievalparity.Record(homePath, retrievalparity.NewRow(retrievalparity.ConsumerQuery, query, []string{claimB}, 3*time.Millisecond, true, now.Add(-time.Hour)))
	retrievalparity.Record(homePath, retrievalparity.NewOrientRow(query, *orientInput, []string{claimA}, time.Millisecond, true, now.Add(-30*time.Minute)))
	retrievalparity.Record(homePath, retrievalparity.NewRow(retrievalparity.ConsumerSearch, query, []string{searchChunk}, time.Millisecond, true, now.Add(-15*time.Minute)))
	controlReportPath := writeParityControlReport(t, homePath, now)

	fixture, fixturePath, code, err := runMemoryParityFreeze(homePath, []string{
		"--from-journal",
		"--window-days", "1",
		"--control-report", controlReportPath,
		"--out", "reviews/retrieval-parity/frozen-synthetic.json",
	}, now)
	if err != nil || code != 0 {
		t.Fatalf("freeze code=%d err=%v fixture=%+v", code, err, fixture)
	}
	if fixture.SchemaVersion != retrievalparity.FixtureSchemaVersion || fixture.ControlReportRef == "" || len(fixture.Rows) != 3 {
		t.Fatalf("frozen fixture=%+v", fixture)
	}
	if filepath.Dir(fixturePath) != filepath.Join(homePath, "reviews", retrievalparity.ReviewArea) {
		t.Fatalf("fixture path escaped review area: %s", fixturePath)
	}
	for _, row := range fixture.Rows {
		if row.Consumer == retrievalparity.ConsumerQuery && !sameStringSet(row.ExpectedRefs, []string{claimA, claimB}) {
			t.Fatalf("freeze did not union repeated refs: %+v", row)
		}
		if row.Consumer == retrievalparity.ConsumerSearch && !sameStringSet(row.ExpectedRefs, []string{searchChunk}) {
			t.Fatalf("freeze did not preserve corpus chunk refs: %+v", row)
		}
	}
	loaded, _, err := retrievalparity.LoadFixture(fixturePath)
	if err != nil || len(loaded.Rows) != len(fixture.Rows) {
		t.Fatalf("load frozen fixture: rows=%d err=%v", len(loaded.Rows), err)
	}
}

func TestParityFreezePreservesZeroTokenTraffic(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	retrievalparity.Record(homePath, retrievalparity.NewOrientRow(
		"",
		*parityTestOrientInput(t, homePath),
		nil,
		time.Millisecond,
		true,
		now.Add(-time.Hour),
	))
	controlReportPath := writeParityControlReport(t, homePath, now)

	fixture, fixturePath, code, err := runMemoryParityFreeze(homePath, []string{
		"--from-journal",
		"--window-days", "1",
		"--control-report", controlReportPath,
	}, now)
	if err != nil || code != 0 {
		t.Fatalf("freeze zero-token traffic code=%d err=%v", code, err)
	}
	if len(fixture.Rows) != 1 {
		t.Fatalf("frozen zero-token rows=%d, want 1", len(fixture.Rows))
	}
	row := fixture.Rows[0]
	if row.Consumer != retrievalparity.ConsumerOrient || row.Query != "" {
		t.Fatalf("frozen zero-token row=%+v", row)
	}
	if got := retrievalparity.ClassifyQuery(row.Consumer, row.Query); got != "orient/tokens:0" {
		t.Fatalf("zero-token class=%q", got)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	report, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerOrient,
	}, now)
	if err != nil || code != 0 || report.K != 0 || len(report.Rows) != 1 || report.Rows[0].QueryClass != "orient/tokens:0" {
		t.Fatalf("replay zero-token traffic code=%d err=%v report=%+v", code, err, report)
	}
}

func TestParityReplayRejectsOrientK(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:    retrievalparity.ConsumerOrient,
			Query:       "",
			OrientInput: parityTestOrientInput(t, homePath),
		}},
	})
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	_, reportPath, code, replayErr := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerOrient,
		"--k", "1",
	}, now)
	if replayErr == nil || code != 2 || reportPath != "" {
		t.Fatalf("orient --k code=%d report=%q err=%v, want usage rejection", code, reportPath, replayErr)
	}
}

func TestParityReplayRejectsOrientFixtureWithoutExactInput(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	fixturePath := filepath.Join(homePath, "missing-orient-input.json")
	fixture := retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Coverage:          retrievalparity.Coverage{KnownGaps: []string{}},
		Rows: []retrievalparity.FixtureRow{{
			Consumer: retrievalparity.ConsumerOrient,
			Query:    "synthetic missing exact orient input",
		}},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, reportPath, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerOrient,
	}, now); err == nil || code == 0 || reportPath != "" || !strings.Contains(err.Error(), "orient_input") {
		t.Fatalf("orient replay without exact input code=%d report=%q err=%v", code, reportPath, err)
	}
}

func TestParityReplayPreservesExactOrientTuple(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	query := "synthetic exact orient tuple"
	tuple := retrievalparity.OrientInput{
		Harness:     "claude_code",
		Cwd:         filepath.Join(t.TempDir(), "library"),
		RepoKind:    "library",
		RepoKindVia: "flag",
		Mode:        "review",
		Topic:       "topic:synthetic",
	}
	production, err := contract.PackWithCapabilities(contract.Input{
		Harness:     tuple.Harness,
		Cwd:         tuple.Cwd,
		Task:        query,
		RepoKind:    tuple.RepoKind,
		RepoKindVia: tuple.RepoKindVia,
		Mode:        tuple.Mode,
		Topic:       tuple.Topic,
	}, orientPolicy(), homePath, now)
	if err != nil {
		t.Fatalf("build production tuple pack: %v", err)
	}
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:     retrievalparity.ConsumerOrient,
			Query:        query,
			OrientInput:  &tuple,
			ExpectedRefs: production.ActiveMemory,
		}},
	})
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	report, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerOrient,
	}, now)
	if err != nil || code != 0 || len(report.Rows) != 1 {
		t.Fatalf("exact-tuple replay code=%d err=%v report=%+v", code, err, report)
	}
	wantInputRef := retrievalparity.HashOrientInput(tuple, query)
	if report.Rows[0].InputSHA256 != wantInputRef {
		t.Fatalf("orient input identity=%q, want %q", report.Rows[0].InputSHA256, wantInputRef)
	}
	wantRefs, _ := parityPersistedOrientRefs(production.ActiveMemory)
	if !sameStringSet(report.Rows[0].ResultRefs, wantRefs) {
		t.Fatalf("exact-tuple replay refs=%v production=%v", report.Rows[0].ResultRefs, production.ActiveMemory)
	}

	changed := tuple
	changed.Topic = "topic:other"
	if retrievalparity.HashOrientInput(changed, query) == wantInputRef {
		t.Fatal("topic did not participate in orient input identity")
	}
}

func TestParityFixtureAcceptsSafeLegacyClaimRefsAndHashesReports(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	legacyRef := "claim://brain-D1"
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer:     retrievalparity.ConsumerQuery,
			Query:        "synthetic legacy claim ref",
			ExpectedRefs: []string{legacyRef},
		}},
	})
	if fixturePath == "" {
		t.Fatal("safe legacy fixture was not written")
	}
	row := retrievalparity.NewReplayRow(retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerQuery,
		Query:        "synthetic legacy claim ref",
		ExpectedRefs: []string{legacyRef},
	}, []string{legacyRef}, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic legacy fixture"),
		retrievalparity.ConsumerQuery,
		10,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{row},
		now,
	)
	reportPath, err := retrievalparity.WriteReport(homePath, report)
	if err != nil {
		t.Fatalf("write legacy-ref report: %v", err)
	}
	persisted, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), legacyRef) {
		t.Fatalf("persisted report leaked legacy ref: %s", persisted)
	}
	want := retrievalparity.HashQuery(legacyRef)
	if !slices.Contains(report.Rows[0].ExpectedRefs, want) || !slices.Contains(report.Rows[0].ResultRefs, want) {
		t.Fatalf("legacy refs were not hashed: %+v", report.Rows[0])
	}
}

func TestParityFreezeRejectsInvalidWindowEndpointsBeforeArtifact(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	retrievalparity.Record(homePath, retrievalparity.NewRow(
		retrievalparity.ConsumerQuery,
		"synthetic endpoint validation",
		nil,
		time.Millisecond,
		true,
		now,
	))
	tests := []struct {
		name       string
		windowDays int
		at         time.Time
		want       string
	}{
		{name: "overflowed start", windowDays: int(^uint(0) >> 1), at: now, want: "window_start"},
		{name: "invalid end", windowDays: 1, at: time.Date(10000, 1, 2, 0, 0, 0, 0, time.UTC), want: "window_end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := retrievalparity.FreezeFromJournal(homePath, tt.windowDays, "", "", tt.at)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("freeze err=%v, want endpoint error containing %q", err, tt.want)
			}
		})
	}
	matches, err := filepath.Glob(filepath.Join(homePath, "reviews", retrievalparity.ReviewArea, "fixture_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("invalid freeze wrote artifacts: %v", matches)
	}
}

func TestParityReportSeparatesControlValidityAndFreezeRequiresControlReport(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	maskedRef := "claim://claim_aaaaaaaaaaaa"
	row := retrievalparity.NewReplayRow(retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerQuery,
		Query:        "synthetic negative control",
		ExpectedRefs: []string{maskedRef},
		NegativeControl: &retrievalparity.NegativeControl{
			MaskedRef: maskedRef,
		},
	}, nil, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic negative control fixture"),
		retrievalparity.ConsumerQuery,
		10,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{row},
		now,
	)
	if report.ParityPassed {
		t.Fatal("masked negative control must remain a parity failure")
	}
	if report.NegativeControlValid == nil || !*report.NegativeControlValid {
		t.Fatalf("negative control validity=%v, want typed true", report.NegativeControlValid)
	}
	controlReportPath, err := retrievalparity.WriteReport(homePath, report)
	if err != nil {
		t.Fatalf("write control report: %v", err)
	}
	retrievalparity.Record(homePath, retrievalparity.NewRow(
		retrievalparity.ConsumerQuery,
		"synthetic traffic to freeze",
		[]string{maskedRef},
		time.Millisecond,
		true,
		now,
	))
	if _, path, code, err := runMemoryParityFreeze(homePath, []string{
		"--from-journal",
		"--window-days", "1",
	}, now); err == nil || code != 2 || path != "" {
		t.Fatalf("freeze without control code=%d path=%q err=%v, want usage rejection", code, path, err)
	}
	fixture, path, code, err := runMemoryParityFreeze(homePath, []string{
		"--from-journal",
		"--window-days", "1",
		"--control-report", controlReportPath,
	}, now)
	if err != nil || code != 0 || path == "" || fixture.ControlReportRef == "" {
		t.Fatalf("freeze with control code=%d path=%q err=%v fixture=%+v", code, path, err, fixture)
	}
}

func TestParityReplayFailsUnavailableEngineWithNoExpectedRefs(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer: retrievalparity.ConsumerQuery,
			Query:    "synthetic empty expected unavailable engine",
		}},
	})
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", filepath.Join(t.TempDir(), "missing-engine"))
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	report, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
	}, now)
	if err != nil || code == 0 || report.ParityPassed || len(report.Rows) != 1 || report.Rows[0].EngineAvailable {
		t.Fatalf("unavailable empty-expected replay code=%d err=%v report=%+v", code, err, report)
	}
}

func TestParityReplayCheckpointResumeRejectsTamperAndWrongIdentity(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{
			{Consumer: retrievalparity.ConsumerQuery, Query: "synthetic checkpoint one"},
			{Consumer: retrievalparity.ConsumerQuery, Query: "synthetic checkpoint two"},
		},
	})
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	report, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
	}, now)
	if err != nil || code != 0 || len(report.Rows) != 2 {
		t.Fatalf("initial checkpoint replay code=%d err=%v report=%+v", code, err, report)
	}
	_, fixtureRef, err := retrievalparity.LoadFixture(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	identity := retrievalparity.NewReplayIdentity(fixtureRef, retrievalparity.ConsumerQuery, 10)
	checkpointPath := retrievalparity.CheckpointPath(homePath, identity)
	checkpoint, err := retrievalparity.LoadCheckpoint(checkpointPath, identity)
	if err != nil || checkpoint.Completed != 2 || len(checkpoint.Rows) != 2 {
		t.Fatalf("load complete checkpoint=%+v err=%v", checkpoint, err)
	}
	checkpoint.Completed = 1
	checkpoint.Rows = checkpoint.Rows[:1]
	if err := retrievalparity.WriteCheckpoint(homePath, &checkpoint); err != nil {
		t.Fatalf("write partial checkpoint: %v", err)
	}
	resumed, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
		"--resume-from", checkpointPath,
	}, now.Add(time.Second))
	if err != nil || code != 0 || len(resumed.Rows) != 2 {
		t.Fatalf("resume code=%d err=%v report=%+v", code, err, resumed)
	}
	if temps, _ := filepath.Glob(filepath.Join(filepath.Dir(checkpointPath), ".*.tmp-*")); len(temps) != 0 {
		t.Fatalf("atomic checkpoint left temp files: %v", temps)
	}

	raw, err := os.ReadFile(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	var tampered map[string]any
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["completed"] = float64(1)
	tamperedRaw, err := json.MarshalIndent(tampered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkpointPath, append(tamperedRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, reportPath, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
		"--resume-from", checkpointPath,
	}, now.Add(2*time.Second)); err == nil || code == 0 || reportPath != "" || !strings.Contains(err.Error(), "tampered") {
		t.Fatalf("tampered resume code=%d report=%q err=%v", code, reportPath, err)
	}

	if err := retrievalparity.WriteCheckpoint(homePath, &checkpoint); err != nil {
		t.Fatalf("restore checkpoint: %v", err)
	}
	otherFixturePath := writeParityFixtureNamed(t, homePath, "other-fixture.json", retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Rows: []retrievalparity.FixtureRow{{
			Consumer: retrievalparity.ConsumerQuery,
			Query:    "synthetic different identity",
		}},
	})
	if _, reportPath, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", otherFixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
		"--resume-from", checkpointPath,
	}, now.Add(3*time.Second)); err == nil || code == 0 || reportPath != "" || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong-identity resume code=%d report=%q err=%v", code, reportPath, err)
	}
}

func TestParityReplayCheckpointResumesAfterRecordedRuntimeGap(t *testing.T) {
	now := time.Now().UTC()
	homePath := newParityTestHome(t, now)
	fixturePath := writeParityFixture(t, homePath, retrievalparity.Fixture{
		SchemaVersion:     retrievalparity.FixtureSchemaVersion,
		ClassifierVersion: retrievalparity.ClassifierVersion,
		Coverage: retrievalparity.Coverage{
			KnownGaps: []string{"fixture_window_unrecorded"},
		},
		Rows: []retrievalparity.FixtureRow{
			{Consumer: retrievalparity.ConsumerQuery, Query: "synthetic unavailable checkpoint one"},
			{Consumer: retrievalparity.ConsumerQuery, Query: "synthetic unavailable checkpoint two"},
		},
	})
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", filepath.Join(t.TempDir(), "missing-engine"))
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	initial, _, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
	}, now)
	if err != nil || code == 0 || len(initial.Rows) != 2 ||
		!slices.Contains(initial.Coverage.KnownGaps, "fixture_window_unrecorded") ||
		!slices.Contains(initial.Coverage.KnownGaps, "query_engine_unavailable") {
		t.Fatalf("initial unavailable replay code=%d err=%v report=%+v", code, err, initial)
	}

	_, fixtureRef, err := retrievalparity.LoadFixture(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	identity := retrievalparity.NewReplayIdentity(fixtureRef, retrievalparity.ConsumerQuery, 10)
	checkpointPath := retrievalparity.CheckpointPath(homePath, identity)
	checkpoint, err := retrievalparity.LoadCheckpoint(checkpointPath, identity)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Completed = 1
	checkpoint.Rows = checkpoint.Rows[:1]
	if err := retrievalparity.WriteCheckpoint(homePath, &checkpoint); err != nil {
		t.Fatalf("write interrupted unavailable checkpoint: %v", err)
	}

	resumed, reportPath, code, err := runMemoryParityReplay(homePath, store, []string{
		"--fixture", fixturePath,
		"--consumer", retrievalparity.ConsumerQuery,
		"--resume-from", checkpointPath,
	}, now.Add(time.Second))
	if err != nil || code == 0 || reportPath == "" || len(resumed.Rows) != 2 {
		t.Fatalf("resume unavailable checkpoint code=%d report=%q err=%v rows=%d", code, reportPath, err, len(resumed.Rows))
	}
	if !slices.Contains(resumed.Coverage.KnownGaps, "fixture_window_unrecorded") ||
		!slices.Contains(resumed.Coverage.KnownGaps, "query_engine_unavailable") {
		t.Fatalf("resumed coverage lost baseline/runtime gaps: %+v", resumed.Coverage)
	}
}

func TestParityReplayCheckpointBindsExactFixturePrefix(t *testing.T) {
	now := time.Now().UTC()
	claimA := "claim://claim_aaaaaaaaaaaa"
	claimB := "claim://claim_bbbbbbbbbbbb"
	tests := []struct {
		name     string
		consumer string
		mutate   func(*retrievalparity.Checkpoint, retrievalparity.FixtureRow)
	}{
		{
			name:     "coverage",
			consumer: retrievalparity.ConsumerQuery,
			mutate: func(checkpoint *retrievalparity.Checkpoint, _ retrievalparity.FixtureRow) {
				checkpoint.Coverage.KnownGaps = []string{"query_engine_unavailable"}
			},
		},
		{
			name:     "query identity",
			consumer: retrievalparity.ConsumerQuery,
			mutate: func(checkpoint *retrievalparity.Checkpoint, _ retrievalparity.FixtureRow) {
				checkpoint.Rows[0].QuerySHA256 = retrievalparity.HashQuery("synthetic checkpoint impostor")
			},
		},
		{
			name:     "expected refs",
			consumer: retrievalparity.ConsumerQuery,
			mutate: func(checkpoint *retrievalparity.Checkpoint, fixtureRow retrievalparity.FixtureRow) {
				fixtureRow.ExpectedRefs = []string{claimA}
				checkpoint.Rows[0] = retrievalparity.NewReplayRow(fixtureRow, nil, time.Millisecond, true)
			},
		},
		{
			name:     "negative control",
			consumer: retrievalparity.ConsumerQuery,
			mutate: func(checkpoint *retrievalparity.Checkpoint, fixtureRow retrievalparity.FixtureRow) {
				fixtureRow.NegativeControl = &retrievalparity.NegativeControl{MaskedRef: claimB}
				checkpoint.Rows[0] = retrievalparity.NewReplayRow(fixtureRow, nil, time.Millisecond, true)
			},
		},
		{
			name:     "orient input",
			consumer: retrievalparity.ConsumerOrient,
			mutate: func(checkpoint *retrievalparity.Checkpoint, _ retrievalparity.FixtureRow) {
				checkpoint.Rows[0].InputSHA256 = retrievalparity.HashQuery("synthetic checkpoint wrong orient input")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homePath := newParityTestHome(t, now)
			fixtureRow := retrievalparity.FixtureRow{
				Consumer:        tt.consumer,
				Query:           "synthetic checkpoint exact prefix",
				ExpectedRefs:    []string{claimA, claimB},
				NegativeControl: &retrievalparity.NegativeControl{MaskedRef: claimA},
			}
			if tt.consumer == retrievalparity.ConsumerOrient {
				fixtureRow.OrientInput = parityTestOrientInput(t, homePath)
				fixtureRow.ExpectedRefs = nil
				fixtureRow.NegativeControl = nil
			}
			fixture := retrievalparity.Fixture{
				SchemaVersion:     retrievalparity.FixtureSchemaVersion,
				ClassifierVersion: retrievalparity.ClassifierVersion,
				Coverage:          retrievalparity.Coverage{KnownGaps: []string{}},
				Rows: []retrievalparity.FixtureRow{
					fixtureRow,
					fixtureRow,
				},
			}
			fixture.Rows[1].Query = "synthetic checkpoint remaining row"
			fixturePath := writeParityFixture(t, homePath, fixture)
			_, fixtureRef, err := retrievalparity.LoadFixture(fixturePath)
			if err != nil {
				t.Fatal(err)
			}
			k := 0
			if tt.consumer == retrievalparity.ConsumerQuery {
				k = 10
			}
			identity := retrievalparity.NewReplayIdentity(fixtureRef, tt.consumer, k)
			checkpoint := retrievalparity.NewCheckpoint(identity, fixture.Coverage)
			checkpoint.Rows = []retrievalparity.ReplayRow{
				retrievalparity.NewReplayRow(fixtureRow, nil, time.Millisecond, true),
			}
			checkpoint.Completed = 1
			tt.mutate(&checkpoint, fixtureRow)
			if err := retrievalparity.WriteCheckpoint(homePath, &checkpoint); err != nil {
				t.Fatalf("write validly sealed mismatched checkpoint: %v", err)
			}
			store, err := mem.Open(homePath)
			if err != nil {
				t.Fatal(err)
			}
			checkpointPath := retrievalparity.CheckpointPath(homePath, identity)
			_, reportPath, code, err := runMemoryParityReplay(homePath, store, []string{
				"--fixture", fixturePath,
				"--consumer", tt.consumer,
				"--resume-from", checkpointPath,
			}, now)
			if err == nil || code == 0 || reportPath != "" || !strings.Contains(err.Error(), "checkpoint") {
				t.Fatalf("mismatched %s checkpoint code=%d report=%q err=%v", tt.name, code, reportPath, err)
			}
		})
	}
}

func newParityTestHome(t *testing.T, now time.Time) string {
	t.Helper()
	homePath := filepath.Join(t.TempDir(), "home")
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init scratch home: %v", err)
	}
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	return homePath
}

func seedParityClaim(t *testing.T, homePath, object string, now time.Time) string {
	t.Helper()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(object))
	captured, err := store.Capture([]mem.Candidate{{
		Entity:         "fixture:retrieval-parity",
		Predicate:      "baseline_anchor",
		Object:         object,
		Confidence:     0.99,
		Derivation:     "deterministic",
		ClaimType:      "architecture",
		PromotionBasis: candidates.BasisExplicitConfirmation,
		SourceTraceRef: "trace_parity_" + hex.EncodeToString(sum[:])[:12],
		SnippetHash:    "sha256:" + hex.EncodeToString(sum[:]),
	}}, now)
	if err != nil {
		t.Fatalf("capture parity claim: %v", err)
	}
	if _, err := store.Adjudicate(captured[0].ID, candidates.Park, "work_parity_fixture", now); err != nil {
		t.Fatalf("park parity claim: %v", err)
	}
	confirmation, err := store.RecordOperatorConfirmation(
		captured[0].ID,
		"test fixture confirms "+captured[0].ID,
		"operator:test",
		now,
	)
	if err != nil {
		t.Fatalf("record parity confirmation: %v", err)
	}
	result, err := store.AdjudicateExplicitConfirmation(captured[0].ID, confirmation.ID, "work_parity_fixture", now)
	if err != nil {
		t.Fatalf("promote parity claim: %v", err)
	}
	if result.Claim == nil {
		t.Fatalf("promotion returned no claim: %+v", result)
	}
	return "claim://" + result.Claim.ID
}

func writeParityFixture(t *testing.T, homePath string, fixture retrievalparity.Fixture) string {
	t.Helper()
	return writeParityFixtureNamed(t, homePath, "synthetic-fixture.json", fixture)
}

func writeParityFixtureNamed(t *testing.T, homePath, name string, fixture retrievalparity.Fixture) string {
	t.Helper()
	path, err := retrievalparity.WriteFixture(homePath, name, fixture)
	if err != nil {
		t.Fatalf("write parity fixture: %v", err)
	}
	return path
}

func parityTestOrientInput(t *testing.T, homePath string) *retrievalparity.OrientInput {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get parity fixture cwd: %v", err)
	}
	in, err := parseOrientArgsWithHome([]string{"--harness", "codex", "--cwd", cwd}, homePath)
	if err != nil {
		t.Fatalf("resolve parity fixture orient input: %v", err)
	}
	tuple := parityOrientInput(in)
	return &tuple
}

func writeParityControlReport(t *testing.T, homePath string, now time.Time) string {
	t.Helper()
	maskedRef := "claim://claim_cccccccccccc"
	row := retrievalparity.NewReplayRow(retrievalparity.FixtureRow{
		Consumer:     retrievalparity.ConsumerQuery,
		Query:        "synthetic freeze control",
		ExpectedRefs: []string{maskedRef},
		NegativeControl: &retrievalparity.NegativeControl{
			MaskedRef: maskedRef,
		},
	}, nil, time.Millisecond, true)
	report := retrievalparity.BuildReport(
		retrievalparity.HashQuery("synthetic freeze control fixture"),
		retrievalparity.ConsumerQuery,
		10,
		retrievalparity.Coverage{},
		[]retrievalparity.ReplayRow{row},
		now,
	)
	path, err := retrievalparity.WriteReport(homePath, report)
	if err != nil {
		t.Fatalf("write parity control report: %v", err)
	}
	return path
}

func cloneParityHomeWithoutClaim(t *testing.T, sourceHome, claimID string, now time.Time) string {
	t.Helper()
	maskedHome := filepath.Join(t.TempDir(), "home")
	if err := os.CopyFS(maskedHome, os.DirFS(sourceHome)); err != nil {
		t.Fatalf("clone scratch home: %v", err)
	}
	if err := home.Init(maskedHome, "test", now); err != nil {
		t.Fatalf("init cloned scratch home: %v", err)
	}
	claimsPath := filepath.Join(maskedHome, "ledger", "claims.jsonl")
	input, err := os.Open(claimsPath)
	if err != nil {
		t.Fatal(err)
	}
	var kept []byte
	found := false
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var claim mem.Claim
		if err := json.Unmarshal(line, &claim); err != nil {
			input.Close()
			t.Fatalf("parse cloned claim: %v", err)
		}
		if claim.ID == claimID {
			found = true
			continue
		}
		kept = append(kept, line...)
		kept = append(kept, '\n')
	}
	if err := scanner.Err(); err != nil {
		input.Close()
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("claim %s not present in cloned ledger", claimID)
	}
	if err := os.WriteFile(claimsPath, kept, 0o644); err != nil {
		t.Fatal(err)
	}
	return maskedHome
}

func runParityReplayCLI(t *testing.T, homePath, fixturePath, consumer string) (int, retrievalparity.Report) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestParityReplayCLIHelper$")
	cmd.Env = parityTestEnv(os.Environ(), map[string]string{
		parityCLIHelperEnv:        "1",
		parityCLIHelperFixture:    fixturePath,
		parityCLIHelperConsumer:   consumer,
		"COMPOUND_HOME":           homePath,
		"COMPOUND_MEM_ENGINE_BIN": "",
	})
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !strings.Contains(err.Error(), "exit status") || !asExitError(err, &exitErr) {
			t.Fatalf("run parity CLI helper: %v\n%s", err, out)
		}
		exitCode = exitErr.ExitCode()
	}
	var report retrievalparity.Report
	if jsonErr := json.Unmarshal(out, &report); jsonErr != nil {
		t.Fatalf("parse parity CLI report (exit=%d): %v\n%s", exitCode, jsonErr, out)
	}
	return exitCode, report
}

func runParityFailureCLI(t *testing.T, homePath, consumer, query string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestParityFailureCLIHelper$")
	cmd.Env = parityTestEnv(os.Environ(), map[string]string{
		parityFailureHelperEnv:    consumer,
		parityFailureHelperQuery:  query,
		"COMPOUND_HOME":           homePath,
		"COMPOUND_MEM_ENGINE_BIN": "",
	})
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if !asExitError(err, &exitErr) {
		t.Fatalf("run parity failure helper: %v\n%s", err, out)
	}
	return exitErr.ExitCode(), string(out)
}

func assertUnavailableParityRow(t *testing.T, homePath, consumer, query string) {
	t.Helper()
	rows, err := retrievalparity.LoadJournal(homePath)
	if err != nil {
		t.Fatalf("load failure journal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("failure journal rows=%d, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Consumer != consumer || row.Query != query || row.EngineAvailable || len(row.ResultRefs) != 0 || row.ResultCount != 0 {
		t.Fatalf("failure journal row=%+v, want unavailable %s observation with zero refs", row, consumer)
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}

func parityTestEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	return out
}

func captureParityOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	fn()
	if err := outW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := errW.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	errBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(errBytes)
}

func sameStringSet(a, b []string) bool {
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func parityPersistedOrientRefs(refs []string) ([]string, bool) {
	out := make([]string, 0, len(refs))
	hasPosition := false
	for _, ref := range refs {
		if strings.HasPrefix(ref, "position://") {
			hasPosition = true
			ref = retrievalparity.HashQuery(ref)
		}
		out = append(out, ref)
	}
	return out, hasPosition
}

func validParitySHA(ref string) bool {
	if !strings.HasPrefix(ref, "sha256:") || len(ref) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(ref, "sha256:"))
	return err == nil
}
