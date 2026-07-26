package main

import (
	"compound/internal/home"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// The battery's per-gate DurationMS has always been captured per run and thrown away, so every
// budget in this repo was tuned from prose in a handoff note rather than from a distribution. The
// 40m go-test timeout is the standing example: it went 10m -> 20m -> 40m across three separate
// "contention-induced false red" incidents while nobody could answer "which gate costs what, and
// how variable is it?" from data. This store is the answer to that question, and it is deliberately
// the FIRST thing landed in the battery rework: no later change may claim a win without a
// before/after from here.
//
// Telemetry must never red a gate. Every failure path in this file degrades to a warning on the
// report and lets the battery's real verdict stand.
const proofCheckTimingsSchemaVersion = "compound.proof_check_timings.v0.1"

// Rolling window. Fifty canonical runs is several weeks of landing activity — enough for a stable
// p90 while keeping the body small enough to rewrite atomically on every run.
const proofCheckTimingsMaxRuns = 50

const (
	proofCheckTimingsLockRelPath  = "lock/proof-check-timings.lock"
	proofCheckTimingsStateRelPath = "state/proof-check-timings.json"
)

type proofCheckTimingsStore struct {
	SchemaVersion string                `json:"schema_version"`
	Runs          []proofCheckTimingRun `json:"runs"`
}

type proofCheckTimingRun struct {
	GeneratedAt string `json:"generated_at"`
	// ProfileKind mirrors the report's: "" for the certifying battery, "development" for --dev.
	ProfileKind string `json:"profile_kind,omitempty"`
	CIOnly      bool   `json:"ci_only,omitempty"`
	CIConsole   bool   `json:"ci_console,omitempty"`
	// Repo is the tree the battery measured. Without it the history silently mixes
	// incomparable runs: a `--ci` against a one-test fixture module records go-test at ~441ms,
	// which would sit in the same baseline as this repo's ~450s and drag any percentile derived
	// from it toward nonsense. Found by exactly that accident during verification.
	Repo   string `json:"repo,omitempty"`
	Status string `json:"status"`
	// WallMS is the real elapsed time of the whole battery. StepsTotalMS is the sum of the
	// individual gate durations. They are equal only while gates run sequentially; once the
	// non-go-test gates run concurrently the gap between them IS the concurrency win, which is
	// why both are recorded rather than one derived from the other.
	WallMS       int64                  `json:"wall_ms"`
	StepsTotalMS int64                  `json:"steps_total_ms"`
	Steps        []proofCheckTimingStep `json:"steps"`
}

type proofCheckTimingStep struct {
	Name string `json:"name"`
	// ProofRef, not Name, is the identity key for a gate's timing history. --dev's first gate is
	// also called "go-test" but runs `go test -short` with the cache enabled; blending its ~seconds
	// into the certifying gate's ~600s would corrupt every percentile derived from it. ProofRef is
	// already lane-qualified ("development:go-test-short-cached-all" vs "go-test-count-1-all") and
	// already pinned by the inventory tests, so it is both unique and stable.
	ProofRef   string `json:"proof_ref"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

// recordProofCheckTimingHistory appends one run to the rolling history.
//
// Returns (warning, error): a non-empty warning means the run WAS recorded but something the
// operator should know happened on the way (e.g. an unparseable history was reset). An error means
// nothing was recorded. Neither is ever fatal to the battery — the caller renders both onto
// report.Warnings and lets the gates decide the exit code.
func recordProofCheckTimingHistory(homePath string, report proofCheckReport, wall time.Duration, now time.Time) (string, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return "", fmt.Errorf("COMPOUND_HOME required")
	}

	run := proofCheckTimingRun{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		ProfileKind: report.ProfileKind,
		CIOnly:      report.CIOnly,
		CIConsole:   report.CIConsole,
		Repo:        report.Repo,
		Status:      report.Status,
		WallMS:      wall.Milliseconds(),
	}
	for _, step := range report.Steps {
		// Only executed gates carry a timing signal. A skipped gate (bun absent) or a gate that
		// never ran because an earlier one failed would otherwise enter the history as a 0ms
		// sample and drag every percentile toward zero.
		if step.Status != "ok" && step.Status != "failed" {
			continue
		}
		run.StepsTotalMS += step.DurationMS
		run.Steps = append(run.Steps, proofCheckTimingStep{
			Name:       step.Name,
			ProofRef:   step.ProofRef,
			Status:     step.Status,
			DurationMS: step.DurationMS,
		})
	}
	if len(run.Steps) == 0 {
		// A dry run, or a battery that failed before executing anything. Nothing to learn.
		return "", nil
	}

	var warning string
	err := withProofCheckTimingsLock(homePath, func() error {
		statePath := filepath.Join(homePath, proofCheckTimingsStateRelPath)
		store, readWarning, err := readProofCheckTimings(statePath)
		if err != nil {
			return err
		}
		warning = readWarning

		store.SchemaVersion = proofCheckTimingsSchemaVersion
		store.Runs = append(store.Runs, run)
		if len(store.Runs) > proofCheckTimingsMaxRuns {
			store.Runs = store.Runs[len(store.Runs)-proofCheckTimingsMaxRuns:]
		}

		body, err := json.MarshalIndent(store, "", "  ")
		if err != nil {
			return err
		}
		body = append(body, '\n')
		return home.WriteFileAtomic(statePath, body, 0o644)
	})
	if err != nil {
		return "", err
	}
	return warning, nil
}

// readProofCheckTimings loads the history. A missing file is an empty store, not an error.
//
// An unparseable file is deliberately NOT a hard error, which is where this diverges from
// internal/landlease's readLease: a corrupt lease must be surfaced because acting on it could
// double-land, whereas a corrupt timing history has no correctness consequence at all. Failing
// hard here would mean a damaged telemetry file permanently blocks its own repair — every
// subsequent run would fail to write and the history could never recover. So it resets and reports.
func readProofCheckTimings(statePath string) (proofCheckTimingsStore, string, error) {
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return proofCheckTimingsStore{SchemaVersion: proofCheckTimingsSchemaVersion}, "", nil
	}
	if err != nil {
		return proofCheckTimingsStore{}, "", err
	}
	var store proofCheckTimingsStore
	if err := json.Unmarshal(data, &store); err != nil {
		return proofCheckTimingsStore{SchemaVersion: proofCheckTimingsSchemaVersion},
			fmt.Sprintf("proof-check timing history at %s was unreadable and has been reset: %v", statePath, err),
			nil
	}
	if store.SchemaVersion != "" && store.SchemaVersion != proofCheckTimingsSchemaVersion {
		return proofCheckTimingsStore{SchemaVersion: proofCheckTimingsSchemaVersion},
			fmt.Sprintf("proof-check timing history schema %s superseded by %s; history reset", store.SchemaVersion, proofCheckTimingsSchemaVersion),
			nil
	}
	return store, "", nil
}

// withProofCheckTimingsLock serializes the read-modify-write across concurrent harnesses on the
// same home. Mirrors internal/landlease's withLock — advisory flock, released with the descriptor
// even on a crash. This machine runs several agent harnesses at once by design, so two batteries
// finishing together is a normal event, not an edge case.
func withProofCheckTimingsLock(homePath string, fn func() error) error {
	lockPath := filepath.Join(homePath, proofCheckTimingsLockRelPath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}

type proofCheckTimingStat struct {
	Name       string `json:"name"`
	ProofRef   string `json:"proof_ref"`
	Samples    int    `json:"samples"`
	P50MS      int64  `json:"p50_ms"`
	P90MS      int64  `json:"p90_ms"`
	LastMS     int64  `json:"last_ms"`
	LastStatus string `json:"last_status"`
}

// proofCheckTimingStats folds the history into a per-gate distribution.
//
// Pure: it takes the store rather than a home path, so it is testable without touching a
// filesystem — the same separation this whole rework is about. profileKind selects the lane ("" for
// the certifying battery); repo restricts to one measured tree ("" means every tree, which is only
// meaningful for inspection, never for deriving a budget); window caps how many of the most recent
// matching runs are folded.
func proofCheckTimingStats(store proofCheckTimingsStore, profileKind, repo string, window int) []proofCheckTimingStat {
	if window <= 0 {
		window = 20
	}
	repo = strings.TrimSpace(repo)
	matched := make([]proofCheckTimingRun, 0, len(store.Runs))
	for _, run := range store.Runs {
		if run.ProfileKind != profileKind {
			continue
		}
		// A run against a different tree is not a sample of THIS tree's cost. Mixing them is
		// how a baseline quietly becomes fiction.
		if repo != "" && run.Repo != "" && run.Repo != repo {
			continue
		}
		matched = append(matched, run)
	}
	if len(matched) > window {
		matched = matched[len(matched)-window:]
	}

	type acc struct {
		name       string
		proofRef   string
		durations  []int64
		lastMS     int64
		lastStatus string
	}
	byRef := map[string]*acc{}
	order := []string{}
	for _, run := range matched {
		for _, step := range run.Steps {
			key := step.ProofRef
			if key == "" {
				key = step.Name
			}
			entry, ok := byRef[key]
			if !ok {
				entry = &acc{name: step.Name, proofRef: step.ProofRef}
				byRef[key] = entry
				order = append(order, key)
			}
			// Only successful runs describe what a gate normally costs. A failed gate's
			// duration is the cost of failing, which is a different number and would make the
			// baseline meaningless for regression detection.
			if step.Status == "ok" {
				entry.durations = append(entry.durations, step.DurationMS)
			}
			entry.lastMS = step.DurationMS
			entry.lastStatus = step.Status
		}
	}

	stats := make([]proofCheckTimingStat, 0, len(order))
	for _, key := range order {
		entry := byRef[key]
		stats = append(stats, proofCheckTimingStat{
			Name:       entry.name,
			ProofRef:   entry.proofRef,
			Samples:    len(entry.durations),
			P50MS:      percentileMS(entry.durations, 50),
			P90MS:      percentileMS(entry.durations, 90),
			LastMS:     entry.lastMS,
			LastStatus: entry.lastStatus,
		})
	}
	// Most expensive first — the operator's question is always "what is costing me the time".
	sort.SliceStable(stats, func(i, j int) bool { return stats[i].P50MS > stats[j].P50MS })
	return stats
}

// percentileMS is nearest-rank on a copy (the caller's slice order is meaningful elsewhere).
func percentileMS(values []int64, p int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (p*len(sorted) + 99) / 100 // ceil(p/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// cmdProofCheckTimings serves `proof-check --timings`.
func cmdProofCheckTimings(args []string) {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "proof-check --timings:", err)
		os.Exit(2)
	}
	statePath := filepath.Join(homePath, proofCheckTimingsStateRelPath)
	store, warning, err := readProofCheckTimings(statePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proof-check --timings:", err)
		os.Exit(2)
	}
	profileKind := ""
	if hasFlag(args, "--dev") {
		profileKind = "development"
	}
	// Default to the tree the operator is standing in. Reporting a budget derived from a
	// different repo's runs would be worse than reporting nothing.
	repo := ""
	if !hasFlag(args, "--all-repos") {
		if resolved, resolveErr := resolveProofCheckRepo(flagValue(args, "--repo")); resolveErr == nil {
			repo = resolved
		}
	}
	stats := proofCheckTimingStats(store, profileKind, repo, proofCheckTimingsWindow(args))

	if hasFlag(args, "--json") {
		printJSON(struct {
			SchemaVersion string                 `json:"schema_version"`
			ProfileKind   string                 `json:"profile_kind,omitempty"`
			Repo          string                 `json:"repo,omitempty"`
			Runs          int                    `json:"runs"`
			Warning       string                 `json:"warning,omitempty"`
			Gates         []proofCheckTimingStat `json:"gates"`
		}{proofCheckTimingsSchemaVersion, profileKind, repo, len(store.Runs), warning, stats})
		return
	}

	if warning != "" {
		fmt.Println("warning:", warning)
	}
	if len(stats) == 0 {
		fmt.Println("proof-check timings: no recorded runs yet for this lane; run the battery once.")
		return
	}
	fmt.Printf("proof-check timings (%d runs recorded, lane %q)\n", len(store.Runs), laneLabel(profileKind))
	fmt.Printf("%-44s %7s %10s %10s %10s  %s\n", "GATE", "N", "P50", "P90", "LAST", "LAST STATUS")
	for _, stat := range stats {
		fmt.Printf("%-44s %7d %10s %10s %10s  %s\n",
			stat.Name, stat.Samples,
			formatMS(stat.P50MS), formatMS(stat.P90MS), formatMS(stat.LastMS),
			stat.LastStatus)
	}
}

func laneLabel(profileKind string) string {
	if profileKind == "" {
		return "certifying"
	}
	return profileKind
}

func proofCheckTimingsWindow(args []string) int {
	raw := strings.TrimSpace(flagValue(args, "--window"))
	if raw == "" {
		return 20
	}
	var window int
	if _, err := fmt.Sscanf(raw, "%d", &window); err != nil || window <= 0 {
		return 20
	}
	return window
}

func formatMS(ms int64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}
