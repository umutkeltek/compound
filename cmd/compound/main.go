// Command compound is the personal-OS CLI. v1 covers the Memory layer.
// Architecture: docs/memory-architecture.md. The owned spine (object model,
// ledger, scope/query planning, promotion) lives in internal/; storage is rented
// behind internal/engine.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/umutkeltek/compound/internal/distill"
	"github.com/umutkeltek/compound/internal/engine"
	"github.com/umutkeltek/compound/internal/ledger"
	"github.com/umutkeltek/compound/internal/memory"
	"github.com/umutkeltek/compound/internal/pattern"
	"github.com/umutkeltek/compound/internal/position"
	"github.com/umutkeltek/compound/internal/recall"
	"github.com/umutkeltek/compound/internal/reconcile"
	"github.com/umutkeltek/compound/internal/scope"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("compound %s\n", version)
	case "distill":
		cmdDistill(os.Args[2:])
	case "scope":
		cmdScope(os.Args[2:])
	case "promote":
		cmdPromote(os.Args[2:])
	case "build":
		cmdBuild()
	case "reconcile":
		cmdReconcile(os.Args[2:])
	case "recall":
		cmdRecall(os.Args[2:])
	case "pack":
		cmdPack(os.Args[2:])
	case "ledger":
		cmdLedger(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `compound — personal OS (Memory layer)

usage:
  compound version
  compound distill <session.jsonl> [--model=M] [--ingest]   deep-distill -> candidate claims
  compound scope <session.jsonl> [--explain]                attribute a session to entities (no cwd)
  compound recall "<query>"     planner: owned claims (entity mode) + engine breadth
  compound promote <claim-id> --basis=<b>   candidate -> promoted (the only path to canon)
  compound build                derive current Positions + recurring Patterns from Claims
  compound reconcile [--entity=E]   alive memory: supersede contradicted/duplicate claims (LLM)
  compound pack "<topic>"       breadth via the rented engine
  compound ledger stat          record counts per object kind
`)
}

func ledgerRoot() string {
	if r := os.Getenv("COMPOUND_LEDGER"); r != "" {
		return r
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".compound", "ledger")
}

func scopeSuffix(primary string) string {
	if primary == "" {
		return ""
	}
	return " · primary=" + primary
}

func cmdRecall(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `usage: compound recall "<query>"`)
		os.Exit(2)
	}
	query := strings.Join(args, " ")
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	plan := recall.MakePlan(query, scope.SeedEntities(), false)
	res, err := recall.Run(l, engine.NewGBrain(), plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recall: %v\n", err)
		os.Exit(1)
	}
	note := ""
	if plan.Entity != "" {
		note = " · entity=" + plan.Entity
	}
	fmt.Printf("mode: %s%s\n\n", plan.Mode, note)
	if len(res.Positions) > 0 {
		fmt.Println("## Current positions")
		for _, p := range res.Positions {
			fmt.Printf("- [%s] %s/%s → active %s (%d in chain)\n", p.Status, p.EntitySlug, p.TopicSlug, p.ActiveClaimID, len(p.ClaimChain))
		}
		fmt.Println()
	}
	if len(res.Claims) > 0 {
		fmt.Println("## Owned claims (ledger)")
		for _, c := range res.Claims {
			fmt.Printf("- [%s] %s\n", c.Status, c.Object)
		}
		fmt.Println()
	}
	if len(res.Hits) > 0 {
		fmt.Println("## Breadth (engine)")
		for _, h := range res.Hits {
			fmt.Printf("- `%s` — %s\n", h.Slug, h.Snippet)
		}
	}
	if len(res.Positions) == 0 && len(res.Claims) == 0 && len(res.Hits) == 0 {
		fmt.Println("(nothing — zero_result_not_proof; check coverage)")
	}
}

func cmdBuild() {
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	claims, err := recall.LoadClaims(l)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		os.Exit(1)
	}
	positions := position.Build(claims)
	patterns := pattern.Build(claims)
	for _, p := range positions {
		if err := l.Append("positions", p); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n", err)
			os.Exit(1)
		}
	}
	for _, p := range patterns {
		if err := l.Append("patterns", p); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("built %d positions, %d patterns from %d claims\n", len(positions), len(patterns), len(claims))
}

func cmdReconcile(args []string) {
	var entity string
	for _, a := range args {
		if strings.HasPrefix(a, "--entity=") {
			entity = strings.TrimPrefix(a, "--entity=")
		}
	}
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	sup, dups, err := reconcile.Reconcile(context.Background(), l, "", entity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("reconciled: %d superseded, %d duplicates resolved\n", sup, dups)
	fmt.Fprintln(os.Stderr, "run `compound build` to refresh positions")
}

func cmdPromote(args []string) {
	var id, basis string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--basis="):
			basis = strings.TrimPrefix(a, "--basis=")
		case !strings.HasPrefix(a, "--") && id == "":
			id = a
		}
	}
	if id == "" || basis == "" {
		fmt.Fprintln(os.Stderr, "usage: compound promote <claim-id> --basis=explicit_confirmation|curated_page|decision_record|runtime_proof")
		os.Exit(2)
	}
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	c, err := recall.Promote(l, id, memory.PromotionBasis(basis))
	if err != nil {
		fmt.Fprintf(os.Stderr, "promote: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("promoted %s (basis: %s)\n  %s\n", c.ID, c.PromotionBasis, c.Object)
}

func cmdPack(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `usage: compound pack "<topic>"`)
		os.Exit(2)
	}
	eng := engine.NewGBrain()
	hits, err := eng.Query(args[0], 12)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pack: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("# Context Pack — %s\n\n## Breadth (engine:%s)\n", args[0], eng.Capabilities().Name)
	for _, h := range hits {
		fmt.Printf("- `%s` — %s\n", h.Slug, h.Snippet)
	}
	fmt.Fprintln(os.Stderr, "\n(note: distilled positions + grounding land when the distiller is ported)")
}

func cmdLedger(args []string) {
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ledger: %s\n", l.Root())
	for _, kind := range ledger.Kinds {
		n, _ := l.Count(kind)
		fmt.Printf("  %-18s %d\n", kind, n)
	}
}

func cmdDistill(args []string) {
	var session, model string
	ingest := false
	for _, a := range args {
		switch {
		case a == "--ingest":
			ingest = true
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case !strings.HasPrefix(a, "--") && session == "":
			session = a
		}
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "usage: compound distill <session.jsonl> [--model=M] [--ingest]")
		os.Exit(2)
	}
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "[1/3] cleaning + distilling %s ...\n", session)
	res, err := distill.Run(ctx, session, distill.Options{Model: model})
	if err != nil {
		fmt.Fprintf(os.Stderr, "distill: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "      %d Umut turns · %d decision-turns · %d chunks · %d calls · %d/%d tok\n",
		res.Clean.UmutKept, res.DecisionTurns, res.Chunks, res.Calls, res.PromptTokens, res.CompletionTokens)

	fmt.Fprintln(os.Stderr, "[2/3] extracting durable decisions ...")
	decisions, err := distill.ExtractDecisions(ctx, res.Model, res.Distillation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "      %d durable decisions\n", len(decisions))

	out := writeDecisionLedger(session, decisions)
	fmt.Println(out)

	if ingest {
		l, err := ledger.Open(ledgerRoot())
		if err != nil {
			fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
			os.Exit(1)
		}
		primary := ""
		if pkt, e := scope.Resolve(session, scope.SeedEntities()); e == nil {
			primary = pkt.PrimaryScope
			fmt.Fprintf(os.Stderr, "      scope: %s%s\n", pkt.Status, scopeSuffix(primary))
		}
		added, dropped, err := distill.ToLedger(l, decisions, session, primary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[3/3] appended %d candidate claims (+ evidence), %d deduped → %s\n", added, dropped, l.Root())
	} else {
		fmt.Fprintln(os.Stderr, "[3/3] not ingested (pass --ingest to append candidate claims to the ledger)")
	}
}

func writeDecisionLedger(session string, ds []distill.Decision) string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".compound", "distill")
	_ = os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, "decision-ledger.go.md")
	var b strings.Builder
	fmt.Fprintf(&b, "# Decisions (Go distiller)\n\nSource: `%s`\n\n", session)
	for _, d := range ds {
		fmt.Fprintf(&b, "- **[SELECT]** %s\n  - confidence: %s · domain: %s\n", d.Statement, d.Confidence, d.Domain)
		if d.Quote != "" {
			fmt.Fprintf(&b, "  - quote: %q\n", d.Quote)
		}
	}
	_ = os.WriteFile(out, []byte(b.String()), 0o644)
	return out
}

func cmdScope(args []string) {
	var session string
	explain := false
	for _, a := range args {
		switch {
		case a == "--explain":
			explain = true
		case !strings.HasPrefix(a, "--") && session == "":
			session = a
		}
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "usage: compound scope <session.jsonl> [--explain]")
		os.Exit(2)
	}
	pkt, err := scope.Resolve(session, scope.SeedEntities())
	if err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		os.Exit(1)
	}
	if explain {
		fmt.Printf("session: %s\nstatus:  %s\nprimary: %s\n\n", pkt.SessionID, pkt.Status, pkt.PrimaryScope)
		fmt.Printf("  %-14s %5s  %-13s  signals\n", "entity", "score", "relation")
		for _, a := range pkt.Assignments {
			fmt.Printf("  %-14s %5d  %-13s  %s\n", a.Entity, a.Score, a.Relation, strings.Join(a.Signals, ", "))
		}
		return
	}
	b, _ := json.MarshalIndent(pkt, "", "  ")
	fmt.Println(string(b))
}
