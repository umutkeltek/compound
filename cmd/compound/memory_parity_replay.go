package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"compound/internal/contract"
	"compound/internal/corpus"
	"compound/internal/mem"
	"compound/internal/retrievalparity"
)

func cmdMemoryParityReplay(homePath string, store *mem.Store, args []string) {
	report, reportPath, code, err := runMemoryParityReplay(homePath, store, args, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory parity-replay:", err)
		if code == 0 {
			code = 1
		}
		os.Exit(code)
	}
	if hasFlag(args, "--json") {
		printJSON(report)
	} else {
		fmt.Printf("# memory parity-replay %s\n", report.ID)
		fmt.Printf("- consumer: %s\n", report.Consumer)
		fmt.Printf("- rows: %d\n", len(report.Rows))
		fmt.Printf("- parity_passed: %t\n", report.ParityPassed)
		if report.NegativeControlValid != nil {
			fmt.Printf("- negative_control_valid: %t\n", *report.NegativeControlValid)
		}
		fmt.Printf("- report: %s\n", reportPath)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func runMemoryParityReplay(homePath string, store *mem.Store, args []string, now time.Time) (retrievalparity.Report, string, int, error) {
	fixturePath := valueFlag(args, "--fixture")
	consumer := strings.TrimSpace(valueFlag(args, "--consumer"))
	if fixturePath == "" || consumer == "" {
		return retrievalparity.Report{}, "", 2, fmt.Errorf("usage: compound memory parity-replay --fixture <file.json> --consumer orient|query [--k N (query only)] [--resume-from <checkpoint.json>] [--json]")
	}
	if consumer != retrievalparity.ConsumerOrient && consumer != retrievalparity.ConsumerQuery {
		return retrievalparity.Report{}, "", 2, fmt.Errorf("consumer must be orient or query")
	}
	rawK := valueFlag(args, "--k")
	k := 0
	if consumer == retrievalparity.ConsumerOrient && rawK != "" {
		return retrievalparity.Report{}, "", 2, fmt.Errorf("--k is supported only for the query consumer")
	}
	if consumer == retrievalparity.ConsumerQuery {
		k = 10
	}
	if rawK != "" {
		parsed, err := strconv.Atoi(rawK)
		if err != nil || parsed <= 0 {
			return retrievalparity.Report{}, "", 2, fmt.Errorf("k must be a positive integer")
		}
		k = parsed
	}
	fixture, fixtureRef, err := retrievalparity.LoadFixture(fixturePath)
	if err != nil {
		return retrievalparity.Report{}, "", 1, err
	}
	selected := make([]retrievalparity.FixtureRow, 0, len(fixture.Rows))
	for _, row := range fixture.Rows {
		if row.Consumer == consumer {
			selected = append(selected, row)
		}
	}
	if len(selected) == 0 {
		return retrievalparity.Report{}, "", 1, fmt.Errorf("fixture has no %s rows", consumer)
	}

	identity := retrievalparity.NewReplayIdentity(fixtureRef, consumer, k)
	checkpoint := retrievalparity.NewCheckpoint(identity, fixture.Coverage)
	if resumePath := strings.TrimSpace(valueFlag(args, "--resume-from")); resumePath != "" {
		checkpoint, err = retrievalparity.LoadCheckpoint(resumePath, identity)
		if err != nil {
			return retrievalparity.Report{}, "", 1, err
		}
		if checkpoint.Completed > len(selected) {
			return retrievalparity.Report{}, "", 1, fmt.Errorf("parity checkpoint has more completed rows than the selected fixture")
		}
	}
	coverage := checkpoint.Coverage
	for _, fixtureRow := range selected[checkpoint.Completed:] {
		resultRefs := []string{}
		engineAvailable := false
		latency := time.Duration(0)
		switch consumer {
		case retrievalparity.ConsumerOrient:
			if fixtureRow.OrientInput == nil {
				return retrievalparity.Report{}, "", 1, fmt.Errorf("orient fixture row requires exact orient_input")
			}
			in := contract.Input{
				Harness:     fixtureRow.OrientInput.Harness,
				Cwd:         fixtureRow.OrientInput.Cwd,
				Task:        fixtureRow.Query,
				RepoKind:    fixtureRow.OrientInput.RepoKind,
				RepoKindVia: fixtureRow.OrientInput.RepoKindVia,
				Mode:        fixtureRow.OrientInput.Mode,
				Topic:       fixtureRow.OrientInput.Topic,
			}
			started := time.Now()
			pack, packErr := contract.PackWithCapabilities(in, orientPolicy(), homePath, now)
			latency = time.Since(started)
			if packErr != nil {
				coverage.KnownGaps = append(coverage.KnownGaps, "orient_retrieval_error")
				break
			}
			resultRefs = append(resultRefs, pack.ActiveMemory...)
			engineAvailable = true
		case retrievalparity.ConsumerQuery:
			eng, closeEng := memBreadthEngine()
			started := time.Now()
			claims, queryErr := store.QueryBreadth(fixtureRow.Query, k, eng)
			closeEng()
			latency = time.Since(started)
			engineAvailable = queryErr == nil
			switch {
			case queryErr == nil:
				resultRefs = parityClaimRefs(claims)
			case errors.Is(queryErr, mem.ErrUnsupported), errors.Is(queryErr, mem.ErrEngineUnavailable):
				coverage.KnownGaps = append(coverage.KnownGaps, "query_engine_unavailable")
			case errors.Is(queryErr, mem.ErrEmbedderMismatch), errors.Is(queryErr, mem.ErrProtocolVersion):
				coverage.KnownGaps = append(coverage.KnownGaps, "query_engine_refused")
			default:
				coverage.KnownGaps = append(coverage.KnownGaps, "query_retrieval_error")
			}
		}
		checkpoint.Rows = append(checkpoint.Rows, retrievalparity.NewReplayRow(fixtureRow, resultRefs, latency, engineAvailable))
		checkpoint.Completed = len(checkpoint.Rows)
		checkpoint.Coverage = coverage
		if err := retrievalparity.WriteCheckpoint(homePath, &checkpoint); err != nil {
			return retrievalparity.Report{}, "", 1, fmt.Errorf("write parity checkpoint: %w", err)
		}
	}
	report := retrievalparity.BuildReport(fixtureRef, consumer, k, checkpoint.Coverage, checkpoint.Rows, now)
	reportPath, err := retrievalparity.WriteReport(homePath, report)
	if err != nil {
		return report, "", 1, err
	}
	if !report.ParityPassed || (report.NegativeControlValid != nil && !*report.NegativeControlValid) {
		return report, reportPath, 1, nil
	}
	return report, reportPath, 0, nil
}

func cmdMemoryParityFreeze(homePath string, args []string) {
	fixture, fixturePath, code, err := runMemoryParityFreeze(homePath, args, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "memory parity-freeze:", err)
		if code == 0 {
			code = 1
		}
		os.Exit(code)
	}
	fmt.Println("# memory parity-freeze")
	fmt.Printf("- rows: %d\n", len(fixture.Rows))
	fmt.Printf("- window_start: %s\n", fixture.Coverage.WindowStart)
	fmt.Printf("- window_end: %s\n", fixture.Coverage.WindowEnd)
	fmt.Printf("- known_gaps: %d\n", len(fixture.Coverage.KnownGaps))
	fmt.Printf("- fixture: %s\n", fixturePath)
}

func runMemoryParityFreeze(homePath string, args []string, now time.Time) (retrievalparity.Fixture, string, int, error) {
	if !hasFlag(args, "--from-journal") {
		return retrievalparity.Fixture{}, "", 2, fmt.Errorf("usage: compound memory parity-freeze --from-journal --window-days N --control-report <report.json> [--out <home-relative path>]")
	}
	rawDays := valueFlag(args, "--window-days")
	windowDays, err := strconv.Atoi(rawDays)
	if err != nil || windowDays <= 0 {
		return retrievalparity.Fixture{}, "", 2, fmt.Errorf("window-days must be a positive integer")
	}
	controlReportPath := strings.TrimSpace(valueFlag(args, "--control-report"))
	if controlReportPath == "" {
		return retrievalparity.Fixture{}, "", 2, fmt.Errorf("control-report is required")
	}
	fixture, path, err := retrievalparity.FreezeFromJournal(homePath, windowDays, valueFlag(args, "--out"), controlReportPath, now)
	if err != nil {
		return retrievalparity.Fixture{}, "", 1, err
	}
	return fixture, path, 0, nil
}

func parityClaimRefs(claims []mem.Claim) []string {
	refs := make([]string, 0, len(claims))
	for _, claim := range claims {
		refs = append(refs, "claim://"+claim.ID)
	}
	return refs
}

func parityRecallRefs(result mem.RecallResult) []string {
	refs := parityClaimRefs(result.Claims)
	for _, candidate := range result.EvidenceCandidates {
		refs = append(refs, candidate.ID)
	}
	return refs
}

func parityCorpusRefs(report corpus.QueryReport) []string {
	refs := make([]string, 0, len(report.Hits))
	for _, hit := range report.Hits {
		ref := strings.TrimSpace(hit.ChunkID)
		if ref == "" {
			ref = strings.TrimSpace(hit.ItemID)
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func parityOrientInput(in contract.Input) retrievalparity.OrientInput {
	return retrievalparity.OrientInput{
		Harness:     in.Harness,
		Cwd:         in.Cwd,
		RepoKind:    in.RepoKind,
		RepoKindVia: in.RepoKindVia,
		Mode:        in.Mode,
		Topic:       in.Topic,
	}
}
