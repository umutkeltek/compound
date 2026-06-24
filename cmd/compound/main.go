// Command compound is the personal-OS CLI. v1 covers the Memory layer.
// Architecture: docs/memory-architecture.md. The owned spine (object model,
// ledger, scope/query planning, promotion) lives in internal/; storage is rented
// behind internal/engine.
package main

import (
	"fmt"
	"os"
	"path/filepath"

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
