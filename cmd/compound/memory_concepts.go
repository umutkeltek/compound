package main

import (
	"fmt"
	"os"
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
