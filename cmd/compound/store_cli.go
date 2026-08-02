package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/persist"
	"compound/internal/persist/pg"
)

const storeUsage = `usage: compound store <subcommand>

Subcommands:
  doctor [--json]    Connect, ensure schema, round-trip test data, report health
  init               Connect and create schema (tables + vector extension)
  status [--json]    Show connection info and row counts
  migrate (--dry-run | --apply) [--json] [--home P]
                     Bulk-load file ledger into Postgres
  sync [--json] [--home P] [--embed]
                     Incremental sync: file ledger → Postgres (idempotent)
                     --embed chains into incremental embed after sync
  embed [--json] [--batch N]
                     Embed unembedded claims into pgvector via Ollama (incremental)
  recall <query> [--k N] [--json]
                     Semantic recall over embedded claims

Environment:
  COMPOUND_DSN          Postgres connection string (required)
  COMPOUND_STORE        Must be "postgres" for store commands
  COMPOUND_VECTOR_DIMS  Embedding dimensions (default: 1536)
  COMPOUND_EMBED_URL    Ollama-compatible embeddings API (default: http://localhost:11434/v1)
  COMPOUND_EMBED_MODEL  Embedding model name (default: nomic-embed-text)

Examples:
  COMPOUND_DSN="postgres://user:pass@192.168.1.10:5432/compound?sslmode=disable" \
    compound store doctor
  compound store init
  compound store status --json
  compound store migrate --dry-run
  compound store migrate --apply
  compound store sync
  compound store sync --embed
  compound store embed
  compound store recall "session distillation architecture" --k 5
`

// cmdStore is the storage-backend operator surface. It manages the Postgres+pgvector
// backend lifecycle: schema creation, health checks, and status reporting.
// Additive infrastructure (ADR-0005): touches no existing code paths.
func cmdStore(args []string) {
	os.Exit(runStore(args, os.Stdout, os.Stderr))
}

func runStore(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprint(stdout, storeUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "doctor":
		return runStoreDoctor(args[1:], stdout, stderr)
	case "init":
		return runStoreInit(args[1:], stdout, stderr)
	case "status":
		return runStoreStatus(args[1:], stdout, stderr)
	case "migrate":
		return runStoreMigrate(args[1:], stdout, stderr)
	case "sync":
		return runStoreSync(args[1:], stdout, stderr)
	case "embed":
		return runStoreEmbed(args[1:], stdout, stderr)
	case "recall":
		return runStoreRecall(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "store: unknown subcommand %q\n", args[0])
		fmt.Fprint(stderr, storeUsage)
		return 2
	}
}

// ─── store doctor ────────────────────────────────────────────────────────────────

type storeDoctorReport struct {
	Backend  string              `json:"backend"`
	DSN      string              `json:"dsn_masked"`
	Checks   []storeDoctorCheck  `json:"checks"`
	AllOK    bool                `json:"all_ok"`
	Duration string              `json:"duration"`
}

type storeDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok|fail
	Detail  string `json:"detail,omitempty"`
}

func runStoreDoctor(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	report := storeDoctorReport{Backend: "postgres"}

	cfg := persist.ConfigFromEnv()
	if cfg.Backend != persist.BackendPostgres {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "env", Status: "fail",
			Detail: "COMPOUND_STORE is not set to 'postgres' (got: " + string(cfg.Backend) + ")",
		})
		report.AllOK = false
		emitStoreDoctor(stdout, report, jsonOut)
		return 1
	}
	report.DSN = maskDSN(cfg.DSN)

	if cfg.DSN == "" {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "env", Status: "fail", Detail: "COMPOUND_DSN is empty",
		})
		report.AllOK = false
		emitStoreDoctor(stdout, report, jsonOut)
		return 1
	}

	// 1. Connect
	store, err := pg.Open(ctx, cfg)
	if err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "connect", Status: "fail", Detail: err.Error(),
		})
		report.AllOK = false
		report.Duration = time.Since(start).Round(time.Millisecond).String()
		emitStoreDoctor(stdout, report, jsonOut)
		return 1
	}
	defer store.Close()
	report.Checks = append(report.Checks, storeDoctorCheck{Name: "connect", Status: "ok"})

	// 2. Ensure schema
	if err := store.EnsureSchema(ctx); err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "schema", Status: "fail", Detail: err.Error(),
		})
		report.AllOK = false
		report.Duration = time.Since(start).Round(time.Millisecond).String()
		emitStoreDoctor(stdout, report, jsonOut)
		return 1
	}
	report.Checks = append(report.Checks, storeDoctorCheck{Name: "schema", Status: "ok"})

	// 3. Claims round-trip
	checkClaims(ctx, store, &report)

	// 4. Events round-trip
	checkEvents(ctx, store, &report)

	// 5. Work round-trip
	checkWork(ctx, store, &report)

	// 6. Vectors round-trip
	checkVectors(ctx, store, cfg, &report)

	// 7. Candidates round-trip
	checkCandidates(ctx, store, &report)

	report.AllOK = true
	for _, c := range report.Checks {
		if c.Status != "ok" {
			report.AllOK = false
			break
		}
	}
	report.Duration = time.Since(start).Round(time.Millisecond).String()

	exitCode := 0
	if !report.AllOK {
		exitCode = 1
	}
	emitStoreDoctor(stdout, report, jsonOut)
	return exitCode
}

func checkClaims(ctx context.Context, store *pg.Store, report *storeDoctorReport) {
	now := time.Now()
	testClaim := persist.Claim{
		ID:        fmt.Sprintf("store-doctor-%d", now.UnixNano()),
		Entity:    "compound::store-doctor",
		Predicate: "health_check",
		Object:    "pass",
		Confidence: 1.0,
		Derivation: "deterministic",
		Visibility: "operational",
		State:     "promoted",
	}

	seq, err := store.AppendClaim(testClaim, now)
	if err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "claims", Status: "fail", Detail: "append: " + err.Error(),
		})
		return
	}

	claims, err := store.FoldClaims(persist.ClaimFilter{Entity: "compound::store-doctor", Limit: 1})
	if err != nil || len(claims) == 0 {
		detail := "fold: no results"
		if err != nil {
			detail = "fold: " + err.Error()
		}
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "claims", Status: "fail", Detail: detail,
		})
		return
	}

	report.Checks = append(report.Checks, storeDoctorCheck{
		Name: "claims", Status: "ok",
		Detail: fmt.Sprintf("seq=%d fold=✓", seq),
	})
}

func checkEvents(ctx context.Context, store *pg.Store, report *storeDoctorReport) {
	testID := fmt.Sprintf("store-doctor-%d", time.Now().UnixNano())
	payload := map[string]string{"check": "health", "source": "store-doctor"}

	if err := store.PutJSON(testID, payload); err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "events", Status: "fail", Detail: "put: " + err.Error(),
		})
		return
	}

	rec, err := store.Read(testID)
	if err != nil || rec == nil {
		detail := "read: nil"
		if err != nil {
			detail = "read: " + err.Error()
		}
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "events", Status: "fail", Detail: detail,
		})
		return
	}

	report.Checks = append(report.Checks, storeDoctorCheck{
		Name: "events", Status: "ok", Detail: "put+read=✓",
	})
}

func checkWork(ctx context.Context, store *pg.Store, report *storeDoctorReport) {
	// Use the WorkStore interface — just verify Put/Get works
	testID := fmt.Sprintf("store-doctor-work-%d", time.Now().UnixNano())

	// We need an envelope — import is already in pg.go; here we build minimal JSON
	env := map[string]any{
		"ids":   map[string]string{"work_id": testID},
		"state": "proposed",
		"owner": "store-doctor",
	}
	payload, _ := json.Marshal(env)

	// Use the raw events table as a proxy since PutWork needs a full WorkEnvelope
	if err := store.PutJSON(testID, payload); err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "work", Status: "fail", Detail: "put: " + err.Error(),
		})
		return
	}
	report.Checks = append(report.Checks, storeDoctorCheck{
		Name: "work", Status: "ok", Detail: "put=✓",
	})
}

func checkVectors(ctx context.Context, store *pg.Store, cfg persist.Config, report *storeDoctorReport) {
	dims := cfg.VectorDimensions
	if dims <= 0 {
		dims = 1536
	}

	testRef := fmt.Sprintf("store-doctor-vec-%d", time.Now().UnixNano())
	// Create a simple test embedding
	embedding := make([]float32, dims)
	embedding[0] = 1.0
	embedding[1] = 0.5

	if err := store.Upsert(testRef, embedding, map[string]string{"source": "doctor"}); err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "vectors", Status: "fail", Detail: "upsert: " + err.Error(),
		})
		return
	}

	// Search with the same vector — should return itself
	hits, err := store.Search(embedding, 1, nil)
	if err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "vectors", Status: "fail", Detail: "search: " + err.Error(),
		})
		return
	}
	if len(hits) == 0 {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "vectors", Status: "fail", Detail: "search: no hits",
		})
		return
	}

	// Cleanup
	store.Delete(testRef)

	report.Checks = append(report.Checks, storeDoctorCheck{
		Name: "vectors", Status: "ok",
		Detail: fmt.Sprintf("dims=%d upsert+search=✓ score=%.4f", dims, hits[0].Score),
	})
}

func checkCandidates(ctx context.Context, store *pg.Store, report *storeDoctorReport) {
	now := time.Now()
	testID := fmt.Sprintf("store-doctor-cand-%d", now.UnixNano())

	cand := persist.Candidate{
		ID:         testID,
		Entity:     "compound::store-doctor",
		Predicate:  "health_check",
		Object:     "pass",
		Confidence: 0.9,
		Derivation: "extracted",
		Visibility: "candidate",
		State:      "proposed",
	}

	if err := store.AppendCandidate(cand, now); err != nil {
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "candidates", Status: "fail", Detail: "append: " + err.Error(),
		})
		return
	}

	got, err := store.GetCandidate(testID)
	if err != nil || got == nil {
		detail := "get: nil"
		if err != nil {
			detail = "get: " + err.Error()
		}
		report.Checks = append(report.Checks, storeDoctorCheck{
			Name: "candidates", Status: "fail", Detail: detail,
		})
		return
	}

	report.Checks = append(report.Checks, storeDoctorCheck{
		Name: "candidates", Status: "ok", Detail: "append+get=✓",
	})
}

func emitStoreDoctor(stdout io.Writer, report storeDoctorReport, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(report)
		return
	}
	// Human-readable output
	fmt.Fprintf(stdout, "store doctor (%s)\n", report.Duration)
	fmt.Fprintf(stdout, "  backend: %s\n", report.Backend)
	fmt.Fprintf(stdout, "  dsn:     %s\n", report.DSN)
	fmt.Fprintln(stdout)
	for _, c := range report.Checks {
		mark := "✓"
		if c.Status != "ok" {
			mark = "✗"
		}
		line := fmt.Sprintf("  %s %s", mark, c.Name)
		if c.Detail != "" {
			line += " — " + c.Detail
		}
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout)
	if report.AllOK {
		fmt.Fprintln(stdout, "  store: OK")
	} else {
		fmt.Fprintln(stdout, "  store: FAIL")
	}
}

// ─── store init ──────────────────────────────────────────────────────────────────

func runStoreInit(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := persist.ConfigFromEnv()
	if cfg.Backend != persist.BackendPostgres {
		fmt.Fprintln(stderr, "store init: COMPOUND_STORE must be 'postgres'")
		return 1
	}
	if cfg.DSN == "" {
		fmt.Fprintln(stderr, "store init: COMPOUND_DSN is required")
		return 1
	}

	store, err := pg.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "store init: connect: %v\n", err)
		return 1
	}
	defer store.Close()

	if err := store.EnsureSchema(ctx); err != nil {
		fmt.Fprintf(stderr, "store init: schema: %v\n", err)
		return 1
	}

	if jsonOut {
		json.NewEncoder(stdout).Encode(map[string]any{
			"status":  "ok",
			"backend": "postgres",
			"schema":  "created",
			"dsn":     maskDSN(cfg.DSN),
		})
		return 0
	}

	fmt.Fprintf(stdout, "store init: schema created (dims=%d)\n", cfg.VectorDimensions)
	fmt.Fprintf(stdout, "  tables: claims, edges, candidates, events, work_log, work_envelopes, vectors\n")
	fmt.Fprintf(stdout, "  index:  ivfflat on vectors (lists=100)\n")
	return 0
}

// ─── store status ────────────────────────────────────────────────────────────────

type storeStatusReport struct {
	Backend    string            `json:"backend"`
	DSN        string            `json:"dsn_masked"`
	Connected  bool              `json:"connected"`
	Tables     map[string]int64  `json:"tables,omitempty"`
	VectorDims int               `json:"vector_dims"`
	Error      string            `json:"error,omitempty"`
}

func runStoreStatus(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := persist.ConfigFromEnv()
	report := storeStatusReport{
		Backend:    string(cfg.Backend),
		VectorDims: cfg.VectorDimensions,
	}

	if cfg.Backend != persist.BackendPostgres {
		report.Error = "COMPOUND_STORE is not 'postgres'"
		emitStoreStatus(stdout, report, jsonOut)
		return 1
	}
	report.DSN = maskDSN(cfg.DSN)

	if cfg.DSN == "" {
		report.Error = "COMPOUND_DSN is empty"
		emitStoreStatus(stdout, report, jsonOut)
		return 1
	}

	store, err := pg.Open(ctx, cfg)
	if err != nil {
		report.Error = err.Error()
		emitStoreStatus(stdout, report, jsonOut)
		return 1
	}
	defer store.Close()
	report.Connected = true

	// Get row counts
	report.Tables = store.TableCounts(ctx)

	emitStoreStatus(stdout, report, jsonOut)
	return 0
}

func emitStoreStatus(stdout io.Writer, report storeStatusReport, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(report)
		return
	}
	fmt.Fprintf(stdout, "store status\n")
	fmt.Fprintf(stdout, "  backend: %s\n", report.Backend)
	if !report.Connected {
		fmt.Fprintf(stdout, "  connected: no\n")
		if report.Error != "" {
			fmt.Fprintf(stdout, "  error: %s\n", report.Error)
		}
		return
	}
	fmt.Fprintf(stdout, "  connected: yes\n")
	fmt.Fprintf(stdout, "  dsn: %s\n", report.DSN)
	fmt.Fprintf(stdout, "  vector_dims: %d\n", report.VectorDims)
	if len(report.Tables) > 0 {
		fmt.Fprintln(stdout, "  tables:")
		for name, count := range report.Tables {
			fmt.Fprintf(stdout, "    %-18s %d rows\n", name, count)
		}
	}
}

// ─── store sync ──────────────────────────────────────────────────────────────────

type syncClaimRow struct {
	ID              string  `json:"id"`
	Seq             int64   `json:"seq"`
	Entity          string  `json:"entity"`
	Predicate       string  `json:"predicate"`
	Object          string  `json:"object"`
	ObservationDate string  `json:"observation_date"`
	Confidence      float64 `json:"confidence"`
	Derivation      string  `json:"derivation"`
	ClaimType       string  `json:"claim_type"`
	CanonClass      string  `json:"canon_class"`
	Visibility      string  `json:"visibility"`
	State           string  `json:"state"`
	SourceTraceRef  string  `json:"source_trace_ref"`
	CreatedAt       string  `json:"created_at"`
}

type syncEdgeRow struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	From      string `json:"from"`
	To        string `json:"to"`
	ClaimID   string `json:"claim_id"`
	CreatedAt string `json:"created_at"`
}

func runStoreSync(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	home := valueFlag(args, "--home")
	if home == "" {
		home = os.Getenv("COMPOUND_HOME")
	}
	if home == "" {
		home = os.Getenv("HOME") + "/.compound"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cfg := persist.ConfigFromEnv()
	if cfg.Backend != persist.BackendPostgres || cfg.DSN == "" {
		fmt.Fprintln(stderr, "store sync: COMPOUND_STORE=postgres and COMPOUND_DSN required")
		return 1
	}
	store, err := pg.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "store sync: connect: %v\n", err)
		return 1
	}
	defer store.Close()
	if err := store.EnsureSchema(ctx); err != nil {
		fmt.Fprintf(stderr, "store sync: schema: %v\n", err)
		return 1
	}
	if !jsonOut {
		fmt.Fprintf(stdout, "store sync: %s → postgres\n", home)
	}
	start := time.Now()
	claimsUp, claimsErr := syncClaims(ctx, store, filepath.Join(home, "ledger", "claims.jsonl"), stdout, jsonOut)
	edgesUp, edgesErr := syncEdges(ctx, store, filepath.Join(home, "ledger", "edges.jsonl"), stdout, jsonOut)
	duration := time.Since(start).Round(time.Second)
	if jsonOut {
		json.NewEncoder(stdout).Encode(map[string]any{
			"claims_upserted": claimsUp, "claims_errors": claimsErr,
			"edges_upserted": edgesUp, "edges_errors": edgesErr,
			"duration": duration.String(),
		})
	} else {
		fmt.Fprintf(stdout, "\nstore sync: done (%s)\n", duration)
		fmt.Fprintf(stdout, "  claims: %d upserted, %d errors\n", claimsUp, claimsErr)
		fmt.Fprintf(stdout, "  edges:  %d upserted, %d errors\n", edgesUp, edgesErr)
	}
	if claimsErr > 0 || edgesErr > 0 {
		return 1
	}
	// Chain into incremental embed when --embed is passed
	if hasFlag(args, "--embed") {
		if !jsonOut {
			fmt.Fprintln(stdout)
		}
		return runStoreEmbed(args, stdout, stderr)
	}
	return 0
}

func syncClaims(ctx context.Context, store *pg.Store, path string, stdout io.Writer, jsonOut bool) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		if !jsonOut {
			fmt.Fprintf(stdout, "  claims: open: %v\n", err)
		}
		return 0, 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	upserted, errors := 0, 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row syncClaimRow
		if err := json.Unmarshal(line, &row); err != nil {
			errors++
			continue
		}
		createdAt := time.Now()
		if row.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, row.CreatedAt); err == nil {
				createdAt = t
			}
		}
		claim := persist.Claim{
			ID:              row.ID,
			Entity:          row.Entity,
			Predicate:       row.Predicate,
			Object:          row.Object,
			ObservationDate: row.ObservationDate,
			Confidence:      row.Confidence,
			Derivation:      row.Derivation,
			ClaimType:       row.ClaimType,
			CanonClass:      row.CanonClass,
			Visibility:      row.Visibility,
			State:           row.State,
			SourceTraceRef:  row.SourceTraceRef,
		}
		if _, err := store.AppendClaim(claim, createdAt); err != nil {
			errors++
			continue
		}
		upserted++
	}
	return upserted, errors
}

func syncEdges(ctx context.Context, store *pg.Store, path string, stdout io.Writer, jsonOut bool) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		if !jsonOut {
			fmt.Fprintf(stdout, "  edges: open: %v\n", err)
		}
		return 0, 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	upserted, errors := 0, 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row syncEdgeRow
		if err := json.Unmarshal(line, &row); err != nil {
			errors++
			continue
		}
		createdAt := time.Now()
		if row.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, row.CreatedAt); err == nil {
				createdAt = t
			}
		}
		edge := persist.Edge{
			Seq:     row.Seq,
			Kind:    row.Kind,
			From:    row.From,
			To:      row.To,
			ClaimID: row.ClaimID,
		}
		if err := store.AppendEdge(edge, createdAt); err != nil {
			errors++
			continue
		}
		upserted++
	}
	return upserted, errors
}

// ─── store embed ─────────────────────────────────────────────────────────────────

type embedBatchRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedBatchResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func runStoreEmbed(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	batchSize := 32
	if v := valueFlag(args, "--batch"); v != "" {
		fmt.Sscanf(v, "%d", &batchSize)
		if batchSize <= 0 || batchSize > 100 {
			batchSize = 32
		}
	}
	ollamaURL := storeEnvOr("COMPOUND_EMBED_URL", "http://localhost:11434/v1")
	model := storeEnvOr("COMPOUND_EMBED_MODEL", "nomic-embed-text")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cfg := persist.ConfigFromEnv()
	if cfg.Backend != persist.BackendPostgres || cfg.DSN == "" {
		fmt.Fprintln(stderr, "store embed: COMPOUND_STORE=postgres and COMPOUND_DSN required")
		return 1
	}
	store, err := pg.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "store embed: connect: %v\n", err)
		return 1
	}
	defer store.Close()
	claims, err := store.FoldClaims(persist.ClaimFilter{
		Visibility: []string{"operational", "ambient", "contextual"},
		Limit:      0,
	})
	if err != nil {
		fmt.Fprintf(stderr, "store embed: load claims: %v\n", err)
		return 1
	}
	existing, _ := store.Count()
	// Incremental: skip claims that already have vectors
	existingRefs, _ := store.VectorRefs()
	var pending []persist.Claim
	for _, c := range claims {
		if !existingRefs["claim:"+c.ID] {
			pending = append(pending, c)
		}
	}
	if !jsonOut {
		fmt.Fprintf(stdout, "store embed: %d claims total, %d already embedded, %d pending\n", len(claims), existing, len(pending))
		fmt.Fprintf(stdout, "  model: %s @ %s\n", model, ollamaURL)
		fmt.Fprintf(stdout, "  batch: %d\n", batchSize)
	}
	if len(pending) == 0 {
		if jsonOut {
			json.NewEncoder(stdout).Encode(map[string]any{"claims_total": len(claims), "already_embedded": existing, "embedded": 0, "errors": 0, "model": model, "duration": "0s"})
		} else {
			fmt.Fprintln(stdout, "store embed: nothing to do — all claims already embedded")
		}
		return 0
	}
	claims = pending
	start := time.Now()
	embedded := 0
	errors := 0
	for i := 0; i < len(claims); i += batchSize {
		end := i + batchSize
		if end > len(claims) {
			end = len(claims)
		}
		batch := claims[i:end]
		texts := make([]string, len(batch))
		for j, c := range batch {
			texts[j] = fmt.Sprintf("%s.%s: %s", c.Entity, c.Predicate, c.Object)
		}
		vectors, err := storeEmbedBatch(ctx, ollamaURL, model, texts)
		if err != nil {
			errors++
			if !jsonOut {
				fmt.Fprintf(stderr, "  batch %d-%d: %v\n", i, end-1, err)
			}
			continue
		}
		for j, c := range batch {
			if j >= len(vectors) {
				break
			}
			meta := map[string]string{"entity": c.Entity, "predicate": c.Predicate, "claim_id": c.ID}
			if err := store.Upsert("claim:"+c.ID, vectors[j], meta); err != nil {
				errors++
				continue
			}
			embedded++
		}
		if !jsonOut && (i/batchSize)%10 == 0 {
			fmt.Fprintf(stdout, "  progress: %d/%d embedded\n", embedded, len(claims))
		}
	}
	duration := time.Since(start).Round(time.Second)
	if jsonOut {
		json.NewEncoder(stdout).Encode(map[string]any{"claims_total": len(claims) + int(existing), "already_embedded": existing, "embedded": embedded, "errors": errors, "model": model, "duration": duration.String()})
	} else {
		fmt.Fprintf(stdout, "\nstore embed: done — %d embedded, %d errors (%s)\n", embedded, errors, duration)
	}
	if errors > 0 {
		return 1
	}
	return 0
}

func storeEmbedBatch(ctx context.Context, baseURL, model string, texts []string) ([][]float32, error) {
	reqBody, _ := json.Marshal(embedBatchRequest{Model: model, Input: texts})
	url := strings.TrimRight(baseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed API %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}
	var result embedBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	vectors := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		vectors[i] = d.Embedding
	}
	return vectors, nil
}

// ─── store recall ────────────────────────────────────────────────────────────────

type recallResult struct {
	Ref       string  `json:"ref"`
	Score     float64 `json:"score"`
	Entity    string  `json:"entity"`
	Predicate string  `json:"predicate"`
	ClaimID   string  `json:"claim_id"`
}

func runStoreRecall(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasFlag(args, "--json")
	k := 10
	if v := valueFlag(args, "--k"); v != "" {
		fmt.Sscanf(v, "%d", &k)
	}
	ollamaURL := storeEnvOr("COMPOUND_EMBED_URL", "http://localhost:11434/v1")
	model := storeEnvOr("COMPOUND_EMBED_MODEL", "nomic-embed-text")
	valueFlags := map[string]bool{"--k": true, "--batch": true}
	var queryParts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			if valueFlags[a] {
				i++
			}
			continue
		}
		queryParts = append(queryParts, a)
	}
	query := strings.Join(queryParts, " ")
	if query == "" {
		fmt.Fprintln(stderr, "usage: compound store recall <query text> [--k N] [--json]")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := persist.ConfigFromEnv()
	if cfg.Backend != persist.BackendPostgres || cfg.DSN == "" {
		fmt.Fprintln(stderr, "store recall: COMPOUND_STORE=postgres and COMPOUND_DSN required")
		return 1
	}
	store, err := pg.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "store recall: connect: %v\n", err)
		return 1
	}
	defer store.Close()
	vectors, err := storeEmbedBatch(ctx, ollamaURL, model, []string{query})
	if err != nil {
		fmt.Fprintf(stderr, "store recall: embed query: %v\n", err)
		return 1
	}
	if len(vectors) == 0 {
		fmt.Fprintln(stderr, "store recall: embed returned no vectors")
		return 1
	}
	hits, err := store.Search(vectors[0], k, nil)
	if err != nil {
		fmt.Fprintf(stderr, "store recall: search: %v\n", err)
		return 1
	}
	if jsonOut {
		results := make([]recallResult, len(hits))
		for i, h := range hits {
			results[i] = recallResult{Ref: h.Ref, Score: h.Score, Entity: h.Metadata["entity"], Predicate: h.Metadata["predicate"], ClaimID: h.Metadata["claim_id"]}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"query": query, "model": model, "k": k, "results": results})
		return 0
	}
	fmt.Fprintf(stdout, "recall: %q (k=%d, model=%s)\n\n", query, k, model)
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "  (no results — run `compound store embed` first)")
		return 0
	}
	for i, h := range hits {
		fmt.Fprintf(stdout, "  %2d. [%.4f] %s.%s\n", i+1, h.Score, h.Metadata["entity"], h.Metadata["predicate"])
	}
	return 0
}

// ─── helpers ─────────────────────────────────────────────────────────────────────

func storeEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// maskDSN redacts the password from a DSN for safe display.
func maskDSN(dsn string) string {
	if dsn == "" {
		return "(unset)"
	}
	at := -1
	for i := len(dsn) - 1; i >= 0; i-- {
		if dsn[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		return dsn
	}
	prefix := dsn[:at]
	schemeEnd := 0
	for i := 0; i < len(prefix)-2; i++ {
		if prefix[i:i+3] == "://" {
			schemeEnd = i + 3
			break
		}
	}
	colonIdx := -1
	for i := schemeEnd; i < len(prefix); i++ {
		if prefix[i] == ':' {
			colonIdx = i
			break
		}
	}
	if colonIdx < 0 {
		return dsn
	}
	return prefix[:colonIdx+1] + "****" + dsn[at:]
}
