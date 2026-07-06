package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/mem"
)

func cmdMemoryImportTopics(store *mem.Store, args []string) {
	path := firstMemoryPos(args, map[string]bool{})
	if path == "" {
		fmt.Fprintln(os.Stderr, "usage: compound memory import-topics <topics.json> [--json]")
		os.Exit(2)
	}
	report, err := store.ImportTopicsFile(path, time.Now())
	die(err)
	if hasFlag(args, "--json") {
		printJSON(report)
		return
	}
	fmt.Printf("# memory import-topics %d\n", report.TopicCount)
	fmt.Printf("- source_ref: %s\n", report.SourceRef)
	fmt.Printf("- source_hash: %s\n", report.SourceHash)
	fmt.Printf("- evidence_ref: %s\n", report.EvidenceRef)
	fmt.Printf("- entity_created: %d\n", report.EntityCreated)
	fmt.Printf("- entity_updated: %d\n", report.EntityUpdated)
	fmt.Printf("- entity_reused: %d\n", report.EntityReused)
	fmt.Printf("- edges_created: %d\n", report.EdgesCreated)
	fmt.Printf("- edges_reused: %d\n", report.EdgesReused)
}

func cmdMemoryFold(store *mem.Store, args []string) {
	report, err := store.FoldPositions(mem.PositionFoldOptions{Entity: valueFlag(args, "--entity")}, time.Now())
	die(err)
	if hasFlag(args, "--json") {
		printJSON(report)
		return
	}
	fmt.Printf("# memory fold positions=%d as_of_seq=%d\n", report.Count, report.AsOfSeq)
	for _, p := range report.Positions {
		fmt.Printf("%-28s = %-12s [%s]  support:%d history:%d\n", p.ID, p.ActiveObject, p.Status, len(p.SupportingClaimRefs), len(p.SupersededHistoryRefs))
	}
}

func cmdMemoryPosition(store *mem.Store, args []string) {
	entity := firstMemoryPos(args, map[string]bool{"--as-of": true, "--topic": true})
	if entity == "" {
		fmt.Fprintln(os.Stderr, "usage: compound memory position <entity> [--as-of N] [--topic topic-slug] [--json]")
		os.Exit(2)
	}
	asOf, err := parseMemoryAsOf(args)
	die(err)
	result, err := store.Position(entity, mem.PositionFoldOptions{AsOfSeq: asOf, Topic: valueFlag(args, "--topic")})
	die(err)
	if hasFlag(args, "--json") {
		printJSON(result)
		return
	}
	fmt.Println("# " + result.Coverage)
	if len(result.TopicSubtree) > 0 {
		fmt.Printf("topic_subtree: %s\n", strings.Join(result.TopicSubtree, ", "))
	}
	for _, detail := range result.Positions {
		p := detail.Position
		fmt.Printf("%s.%s = %s  [%s, claim %s]\n", p.Entity, p.Topic, p.ActiveObject, p.Status, p.ActiveClaimID)
		if len(p.SupportingClaimRefs) > 0 {
			fmt.Printf("  supporting: %s\n", strings.Join(p.SupportingClaimRefs, ","))
		}
		if len(p.SupersededHistoryRefs) > 0 {
			fmt.Printf("  superseded: %s\n", strings.Join(p.SupersededHistoryRefs, ","))
		}
		if len(p.ReviewRefs) > 0 {
			fmt.Printf("  review: %s\n", strings.Join(p.ReviewRefs, ","))
		}
		for _, claim := range detail.Claims {
			fmt.Printf("  claim %s seq=%d %s.%s=%s evidence=%s\n", claim.ID, claim.Seq, claim.Entity, claim.Predicate, claim.Object, strings.Join(claim.EvidenceRefs, ","))
		}
		for _, evidence := range detail.Evidence {
			fmt.Printf("  evidence %s source=%s:%s\n", evidence.ID, evidence.SourceKind, evidence.SourceID)
		}
	}
}

func cmdMemoryRecall(store *mem.Store, args []string) {
	asOf, err := parseMemoryAsOf(args)
	die(err)
	entity := firstMemoryPos(args, map[string]bool{"--as-of": true, "--topic": true})
	rr, err := store.Recall(entity, mem.RecallOpts{
		AsOfSeq:            asOf,
		IncludeRetired:     hasFlag(args, "--include-retired"),
		IncludeNeedsReview: hasFlag(args, "--include-needs-review"),
		IncludeEvidence:    hasFlag(args, "--include-evidence"),
		Topic:              valueFlag(args, "--topic"),
	})
	die(err)
	if hasFlag(args, "--json") {
		printJSON(rr)
		return
	}
	fmt.Println("# " + rr.Coverage)
	if len(rr.TopicSubtree) > 0 {
		fmt.Printf("topic_subtree: %s\n", strings.Join(rr.TopicSubtree, ", "))
	}
	for _, c := range rr.Claims {
		fmt.Printf("%s.%s = %s  (seq %d, since %s, %s, conf %.2f)\n", c.Entity, c.Predicate, c.Object, c.Seq, c.ValidFrom, c.Derivation, c.Confidence)
	}
	for _, c := range rr.EvidenceCandidates {
		fmt.Printf("evidence %s.%s = %s (%s, conf %.2f, annotations %d)\n", c.Entity, c.Predicate, c.Object, c.State, c.Confidence, len(c.Annotations))
		for _, ann := range c.Annotations {
			fmt.Printf(" annotation %s decision=%s original=%s confidence=%.2f run=%s verdict=%s\n", ann.ID, ann.Decision, ann.OriginalDecision, ann.Confidence, ann.RunID, ann.VerdictArtifact)
		}
	}
}

func cmdMemoryBakeoff(store *mem.Store, args []string, engineFactory func() (mem.Engine, func())) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound memory bakeoff <init|run --queries file>")
		os.Exit(2)
	}
	switch args[0] {
	case "init":
		path := valueFlag(args[1:], "--queries")
		if path == "" {
			path = filepath.Join("fixtures", "bakeoff", "queries.yaml")
		}
		report, err := mem.WriteDefaultBakeoffQueries(path, hasFlag(args[1:], "--force"))
		die(err)
		if hasFlag(args[1:], "--json") {
			printJSON(report)
			return
		}
		fmt.Println("# memory bakeoff init")
		fmt.Printf("- path: %s\n", report.Path)
		fmt.Printf("- created: %t\n", report.Created)
		fmt.Printf("- queries: %d\n", report.QueryCount)
		fmt.Printf("- lanes: %s\n", strings.Join(report.Lanes, ","))
	case "run":
		rest := args[1:]
		queriesPath := valueFlag(rest, "--queries")
		if queriesPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound memory bakeoff run --queries <file> [--engine <bin>] [--k N] [--json]")
			os.Exit(2)
		}
		k := 10
		if raw := valueFlag(rest, "--k"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "error: --k must be a positive integer")
				os.Exit(2)
			}
			k = n
		}
		var restore func()
		if bin := valueFlag(rest, "--engine"); bin != "" {
			restore = captureEnv("COMPOUND_MEM_ENGINE_BIN", "COMPOUND_MEM_ENGINE_EMBEDDER")
			if err := os.Setenv("COMPOUND_MEM_ENGINE_BIN", bin); err != nil {
				die(err)
			}
			if embedder := valueFlag(rest, "--embedder"); embedder != "" {
				if err := os.Setenv("COMPOUND_MEM_ENGINE_EMBEDDER", embedder); err != nil {
					restore()
					die(err)
				}
			}
		} else {
			restore = func() {}
		}
		eng, closeEng := engineFactory()
		report, err := store.RunBakeoff(mem.BakeoffRunOptions{
			QueriesPath:    queriesPath,
			K:              k,
			Candidate:      eng,
			AllowExpansion: true,
			Now:            time.Now(),
		})
		closeEng()
		restore()
		if hasFlag(rest, "--json") {
			printJSON(report)
			if err != nil {
				os.Exit(1)
			}
			return
		}
		fmt.Println("# memory bakeoff run")
		fmt.Printf("- report: %s\n", report.ReportPath)
		fmt.Printf("- queries: %d\n", report.QueryCount)
		fmt.Printf("- candidate_engine: %s\n", report.CandidateEngine)
		fmt.Printf("- recall_at_5: %.3f\n", report.Metrics.RecallAt5)
		fmt.Printf("- recall_at_10: %.3f\n", report.Metrics.RecallAt10)
		fmt.Printf("- mrr: %.3f\n", report.Metrics.MRR)
		fmt.Printf("- reference_covered: %.3f\n", report.Metrics.ReferenceCovered)
		fmt.Printf("- latency_p50_ms: %.3f\n", report.Metrics.LatencyP50MS)
		fmt.Printf("- latency_p95_ms: %.3f\n", report.Metrics.LatencyP95MS)
		fmt.Printf("- ledger_digest_unchanged: %t\n", report.LedgerDigestUnchanged)
		fmt.Printf("- determinism: %t\n", report.Determinism.Passed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memory bakeoff:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound memory bakeoff <init|run --queries file>")
		os.Exit(2)
	}
}

func parseMemoryAsOf(args []string) (int64, error) {
	raw := valueFlag(args, "--as-of")
	if raw == "" {
		return 0, nil
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("--as-of must be a non-negative ledger seq")
	}
	return seq, nil
}

func firstMemoryPos(args []string, valueFlags map[string]bool) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if valueFlags[arg] {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}
