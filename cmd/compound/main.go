// Command compound is the personal-OS CLI. v1 covers the Memory layer.
// Architecture: docs/memory-architecture.md. The owned spine (object model,
// ledger, scope/query planning, promotion) lives in internal/; storage is rented
// behind internal/engine.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/umutkeltek/compound/internal/distill"
	"github.com/umutkeltek/compound/internal/engine"
	"github.com/umutkeltek/compound/internal/ledger"
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
  compound recall <entity>      entity-anchored recall (precision)
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

func cmdRecall(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound recall <entity>")
		os.Exit(2)
	}
	facts, err := engine.NewGBrain().Recall(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "recall: %v\n", err)
		os.Exit(1)
	}
	if len(facts) == 0 {
		fmt.Println("(no matching facts — zero_result_not_proof; check freshness/coverage)")
		return
	}
	for _, f := range facts {
		fmt.Printf("- %s\n", f.Claim)
	}
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
		n, err := distill.ToLedger(l, decisions, session)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[3/3] appended %d candidate claims (+ evidence) to %s\n", n, l.Root())
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
