package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/persist"
	"compound/internal/persist/pg"
)

const storeMigrateUsage = `usage: compound store migrate (--dry-run | --apply) [--json] [--home P]

Bulk-loads the file-based ledger (claims, edges, candidates) from COMPOUND_HOME
into the Postgres backend. Read-only on files; additive writes to pg.

Flags:
  --dry-run   Count records without writing (default)
  --apply     Actually write records to Postgres
  --json      Machine-readable output
  --home P    COMPOUND_HOME path (default: $COMPOUND_HOME)

Environment:
  COMPOUND_DSN  Postgres connection string (required for --apply)
`

type migrateSummary struct {
	Mode              string `json:"mode"` // dry-run|apply
	ClaimsFound       int    `json:"claims_found"`
	EdgesFound        int    `json:"edges_found"`
	CandidatesFound   int    `json:"candidates_found"`
	ClaimsWritten     int    `json:"claims_written,omitempty"`
	EdgesWritten      int    `json:"edges_written,omitempty"`
	CandidatesWritten int    `json:"candidates_written,omitempty"`
	Errors            int    `json:"errors,omitempty"`
	Duration          string `json:"duration,omitempty"`
}

func runStoreMigrate(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	apply := hasFlag(args, "--apply")

	if hasUsageFlag(args) {
		fmt.Fprint(stdout, storeMigrateUsage)
		return 0
	}
	if !apply && !hasFlag(args, "--dry-run") {
		// Default to dry-run
		fmt.Fprintln(stderr, "store migrate: no mode specified, defaulting to --dry-run")
	}

	homePath := valueFlag(args, "--home")
	if homePath == "" {
		var err error
		homePath, err = home.Resolve("")
		if err != nil {
			fmt.Fprintf(stderr, "store migrate: resolve home: %v\n", err)
			return 1
		}
	}
	if homePath == "" {
		fmt.Fprintln(stderr, "store migrate: no COMPOUND_HOME (set $COMPOUND_HOME or pass --home)")
		return 1
	}

	start := time.Now()
	summary := migrateSummary{Mode: "dry-run"}
	if apply {
		summary.Mode = "apply"
	}

	// Count/read claims
	claimsPath := filepath.Join(homePath, "ledger", "claims.jsonl")
	claims, claimErr := readClaimsJSONL(claimsPath)
	if claimErr != nil {
		if os.IsNotExist(claimErr) {
			fmt.Fprintf(stderr, "store migrate: claims.jsonl not found at %s\n", claimsPath)
		} else {
			fmt.Fprintf(stderr, "store migrate: read claims: %v\n", claimErr)
			return 1
		}
	}
	summary.ClaimsFound = len(claims)

	// Count/read edges
	edgesPath := filepath.Join(homePath, "ledger", "edges.jsonl")
	edges, edgeErr := readEdgesJSONL(edgesPath)
	if edgeErr != nil && !os.IsNotExist(edgeErr) {
		fmt.Fprintf(stderr, "store migrate: read edges: %v\n", edgeErr)
		return 1
	}
	summary.EdgesFound = len(edges)

	// Count/read candidates
	candDir := filepath.Join(homePath, candidates.InboxDir(candidates.Memory))
	cands, candErr := readCandidateFiles(candDir)
	if candErr != nil && !os.IsNotExist(candErr) {
		fmt.Fprintf(stderr, "store migrate: read candidates: %v\n", candErr)
		return 1
	}
	summary.CandidatesFound = len(cands)

	if !apply {
		summary.Duration = time.Since(start).Round(time.Millisecond).String()
		emitMigrateSummary(stdout, summary, jsonOut)
		return 0
	}

	// ─── Apply mode ──────────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := persist.ConfigFromEnv()
	if cfg.Backend != persist.BackendPostgres {
		fmt.Fprintln(stderr, "store migrate --apply: COMPOUND_STORE must be 'postgres'")
		return 1
	}
	if cfg.DSN == "" {
		fmt.Fprintln(stderr, "store migrate --apply: COMPOUND_DSN is required")
		return 1
	}

	store, err := pg.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "store migrate: connect: %v\n", err)
		return 1
	}
	defer store.Close()

	if err := store.EnsureSchema(ctx); err != nil {
		fmt.Fprintf(stderr, "store migrate: schema: %v\n", err)
		return 1
	}

	// Write claims
	for _, c := range claims {
		pc := mapMemClaimToPersist(c)
		if _, err := store.AppendClaim(pc, time.Now()); err != nil {
			summary.Errors++
			if !jsonOut {
				fmt.Fprintf(stderr, "  claim %s: %v\n", c.ID, err)
			}
			continue
		}
		summary.ClaimsWritten++
	}

	// Write edges
	for _, e := range edges {
		pe := mapMemEdgeToPersist(e)
		if err := store.AppendEdge(pe, time.Now()); err != nil {
			summary.Errors++
			if !jsonOut {
				fmt.Fprintf(stderr, "  edge %s→%s: %v\n", e.From, e.To, err)
			}
			continue
		}
		summary.EdgesWritten++
	}

	// Write candidates
	for _, c := range cands {
		pc := mapMemCandidateToPersist(c)
		if err := store.AppendCandidate(pc, time.Now()); err != nil {
			summary.Errors++
			if !jsonOut {
				fmt.Fprintf(stderr, "  candidate %s: %v\n", c.ID, err)
			}
			continue
		}
		summary.CandidatesWritten++
	}

	summary.Duration = time.Since(start).Round(time.Millisecond).String()
	emitMigrateSummary(stdout, summary, jsonOut)

	if summary.Errors > 0 {
		return 1
	}
	return 0
}

// ─── File readers ────────────────────────────────────────────────────────────────

func readClaimsJSONL(path string) ([]mem.Claim, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var claims []mem.Claim
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024) // 4MB max line
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c mem.Claim
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue // skip malformed lines
		}
		claims = append(claims, c)
	}
	return claims, scanner.Err()
}

func readEdgesJSONL(path string) ([]mem.Edge, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var edges []mem.Edge
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e mem.Edge
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		edges = append(edges, e)
	}
	return edges, scanner.Err()
}

func readCandidateFiles(dir string) ([]mem.Candidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var cands []mem.Candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var c mem.Candidate
		if err := json.Unmarshal(data, &c); err != nil {
			continue
		}
		if c.ID == "" {
			// Use filename stem as ID if not embedded
			c.ID = strings.TrimSuffix(entry.Name(), ".json")
		}
		cands = append(cands, c)
	}
	return cands, nil
}

// ─── Mappers ─────────────────────────────────────────────────────────────────────

func mapMemClaimToPersist(c mem.Claim) persist.Claim {
	return persist.Claim{
		Seq:             c.Seq,
		ID:              c.ID,
		Entity:          c.Entity,
		Predicate:       c.Predicate,
		Object:          c.Object,
		ObservationDate: c.ValidFrom,
		Confidence:      c.Confidence,
		Derivation:      c.Derivation,
		ClaimType:       c.ClaimType,
		CanonClass:      c.CanonClass,
		Qualifiers:      c.Qualifiers,
		Visibility:      c.Visibility,
		State:           c.State,
		SourceTraceRef:  "",
		EvidenceRefs:    c.EvidenceRefs,
		CreatedAt:       c.CreatedAt,
	}
}

func mapMemEdgeToPersist(e mem.Edge) persist.Edge {
	return persist.Edge{
		From: e.From,
		To:   e.To,
		Kind: e.Kind,
		Seq:  e.Seq,
	}
}

func mapMemCandidateToPersist(c mem.Candidate) persist.Candidate {
	return persist.Candidate{
		ID:             c.ID,
		Entity:         c.Entity,
		Predicate:      c.Predicate,
		Object:         c.Object,
		Confidence:     c.Confidence,
		Derivation:     c.Derivation,
		Visibility:     c.Visibility,
		State:          c.State,
		SourceTraceRef: c.SourceTraceRef,
		CreatedAt:      c.CreatedAt,
	}
}

// ─── Output ──────────────────────────────────────────────────────────────────────

func emitMigrateSummary(stdout io.Writer, summary migrateSummary, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(summary)
		return
	}
	fmt.Fprintf(stdout, "store migrate (%s)\n", summary.Mode)
	fmt.Fprintf(stdout, "  claims:     %d found", summary.ClaimsFound)
	if summary.Mode == "apply" {
		fmt.Fprintf(stdout, " → %d written", summary.ClaimsWritten)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  edges:      %d found", summary.EdgesFound)
	if summary.Mode == "apply" {
		fmt.Fprintf(stdout, " → %d written", summary.EdgesWritten)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  candidates: %d found", summary.CandidatesFound)
	if summary.Mode == "apply" {
		fmt.Fprintf(stdout, " → %d written", summary.CandidatesWritten)
	}
	fmt.Fprintln(stdout)
	if summary.Errors > 0 {
		fmt.Fprintf(stdout, "  errors:     %d\n", summary.Errors)
	}
	if summary.Duration != "" {
		fmt.Fprintf(stdout, "  duration:   %s\n", summary.Duration)
	}
}
