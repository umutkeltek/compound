package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

const sessionMiningReportSchemaVersion = "compound.sessions_mine_report.v0.1"

type sessionMiningReport struct {
	SchemaVersion         string                 `json:"schema_version"`
	Status                string                 `json:"status"`
	GeneratedAt           string                 `json:"generated_at"`
	Since                 string                 `json:"since"`
	SinceCutoff           string                 `json:"since_cutoff"`
	DryRun                bool                   `json:"dry_run"`
	SessionCount          int                    `json:"session_count"`
	HarnessCounts         map[string]int         `json:"harness_counts"`
	HookCounts            map[string]int         `json:"hook_counts"`
	PreferencesFound      []sessionMiningFinding `json:"preferences_found"`
	StuckPoints           []sessionMiningFinding `json:"stuck_points"`
	RepeatedReinventions  []sessionMiningFinding `json:"repeated_reinventions"`
	CandidateSkillUpdates []sessionMiningFinding `json:"candidate_skill_updates"`
	RulesToRetire         []sessionMiningFinding `json:"rules_to_retire"`
	MemoryCandidates      []sessionMiningFinding `json:"memory_candidates"`
	ProofRequired         []string               `json:"proof_required"`
	Checks                []sessionMiningCheck   `json:"checks"`
	NextCommand           []string               `json:"next_command"`
}

type sessionMiningFinding struct {
	Kind         string   `json:"kind"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Confidence   float64  `json:"confidence,omitempty"`
}

type sessionMiningCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type sessionHookEvent struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Harness   string `json:"harness"`
	HookName  string `json:"hook_name"`
	Status    string `json:"status"`
	IDs       struct {
		SessionID string `json:"session_id"`
		WorkID    string `json:"work_id"`
	} `json:"ids"`
}

func cmdSessions(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, sessionsUsage())
		os.Exit(2)
	}
	switch args[0] {
	case "mine":
		cmdSessionsMine(args[1:])
	default:
		fmt.Fprintln(os.Stderr, sessionsUsage())
		os.Exit(2)
	}
}

func sessionsUsage() string {
	return "usage: compound sessions mine --dry-run [--since 24h] [--json]"
}

func cmdSessionsMine(args []string) {
	since := "24h"
	jsonOut := false
	dryRun := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--since":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, sessionsUsage())
				os.Exit(2)
			}
			since = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, sessionsUsage())
			os.Exit(2)
		}
	}
	if !dryRun {
		fmt.Fprintln(os.Stderr, "sessions mine: only --dry-run is supported")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	report, err := buildSessionMiningReport(homePath, since, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sessions mine:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("# session mining dry-run\n\n")
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- since: %s\n", report.Since)
	fmt.Printf("- sessions: %d\n", report.SessionCount)
	fmt.Printf("- stuck_points: %d\n", len(report.StuckPoints))
	fmt.Printf("- memory_candidates: %d\n", len(report.MemoryCandidates))
	fmt.Printf("- skill_updates: %d\n", len(report.CandidateSkillUpdates))
	for _, proof := range report.ProofRequired {
		fmt.Printf("- proof_required: %s\n", proof)
	}
}

func buildSessionMiningReport(homePath, since string, now time.Time) (sessionMiningReport, error) {
	if now.IsZero() {
		now = time.Now()
	}
	window, err := parseSessionMiningSince(since)
	if err != nil {
		return sessionMiningReport{}, err
	}
	cutoff := now.Add(-window)
	report := sessionMiningReport{
		SchemaVersion: sessionMiningReportSchemaVersion,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Since:         since,
		SinceCutoff:   cutoff.UTC().Format(time.RFC3339),
		DryRun:        true,
		HarnessCounts: map[string]int{},
		HookCounts:    map[string]int{},
		NextCommand:   []string{"compound", "sessions", "mine", "--since", since, "--dry-run", "--json"},
		ProofRequired: []string{
			"dry-run only; no session import, no memory promotion, no skill promotion",
			"every candidate must keep evidence refs before review",
			"one promoted memory must later prove recall changed a ContextPack or work result",
		},
	}

	events, warnings, err := loadRecentSessionHookEvents(homePath, cutoff)
	if err != nil {
		return report, err
	}
	report.SessionCount = len(events)
	for _, ev := range events {
		if ev.Harness != "" {
			report.HarnessCounts[ev.Harness]++
		}
		if ev.HookName != "" {
			report.HookCounts[ev.HookName]++
		}
	}
	if len(events) == 0 {
		report.Checks = append(report.Checks, sessionMiningCheck{Name: "recent_session_events", Status: "warn", Detail: "no hook events in window"})
	} else {
		report.Checks = append(report.Checks, sessionMiningCheck{Name: "recent_session_events", Status: "ok", Detail: fmt.Sprintf("%d events", len(events))})
	}
	for _, warning := range warnings {
		report.Checks = append(report.Checks, sessionMiningCheck{Name: "hook_event_warning", Status: "warn", Detail: warning})
	}

	store, err := work.Open(homePath)
	if err != nil {
		return report, err
	}
	workbench, err := store.Workbench(now, work.WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		return report, err
	}
	report.StuckPoints = sessionMiningStuckPoints(workbench)
	report.MemoryCandidates = sessionMiningMemoryCandidates(workbench)
	report.CandidateSkillUpdates = sessionMiningSkillUpdates(events)
	report.RepeatedReinventions = sessionMiningRepeatedReinventions(workbench)
	report.RulesToRetire = sessionMiningRulesToRetire(workbench)
	report.PreferencesFound = []sessionMiningFinding{}
	return report, nil
}

func parseSessionMiningSince(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 24 * time.Hour, nil
	}
	if strings.HasSuffix(value, "d") {
		days := strings.TrimSuffix(value, "d")
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q", value)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --since %q", value)
	}
	return d, nil
}

func loadRecentSessionHookEvents(homePath string, cutoff time.Time) ([]sessionHookEvent, []string, error) {
	path := filepath.Join(homePath, "events", "hook-events.ndjson")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read hook events: %w", err)
	}
	defer f.Close()
	var out []sessionHookEvent
	var warnings []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev sessionHookEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			warnings = append(warnings, fmt.Sprintf("line %d unreadable", lineNo))
			continue
		}
		created, err := time.Parse(time.RFC3339, ev.CreatedAt)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("line %d missing created_at", lineNo))
			continue
		}
		if created.Before(cutoff) {
			continue
		}
		out = append(out, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, warnings, fmt.Errorf("scan hook events: %w", err)
	}
	return out, warnings, nil
}

func sessionMiningStuckPoints(workbench work.WorkbenchReport) []sessionMiningFinding {
	var out []sessionMiningFinding
	seen := map[string]bool{}
	for _, item := range workbench.Open {
		if item.State != "held" && item.State != "blocked" {
			continue
		}
		summary := strings.TrimSpace(item.NextAction)
		if summary == "" {
			summary = strings.TrimSpace(item.SuccessPredicate)
		}
		if summary == "" {
			summary = "work is waiting for input"
		}
		key := item.WorkID + ":" + summary
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, sessionMiningFinding{
			Kind:         "held_work",
			Summary:      summary,
			EvidenceRefs: []string{"work:" + item.WorkID},
			Confidence:   0.8,
		})
	}
	return out
}

func sessionMiningMemoryCandidates(workbench work.WorkbenchReport) []sessionMiningFinding {
	var out []sessionMiningFinding
	seen := map[string]bool{}
	for _, item := range workbench.Open {
		for _, card := range item.ReviewCards {
			if card.Kind != "memory_candidate_adjudication" || card.CandidateID == "" || seen[card.CandidateID] {
				continue
			}
			seen[card.CandidateID] = true
			summary := firstNonEmpty(card.CandidateObject, card.Title, "memory candidate needs review")
			out = append(out, sessionMiningFinding{
				Kind:         "memory_candidate",
				Summary:      summary,
				EvidenceRefs: compactSessionRefs("work:"+item.WorkID, "memory_candidate:"+card.CandidateID, "panel_run:"+card.PanelRunID),
				Confidence:   0.72,
			})
		}
	}
	return out
}

func compactSessionRefs(values ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func sessionMiningSkillUpdates(events []sessionHookEvent) []sessionMiningFinding {
	if len(events) == 0 {
		return nil
	}
	byHarness := map[string]int{}
	for _, ev := range events {
		if ev.Harness != "" {
			byHarness[ev.Harness]++
		}
	}
	var out []sessionMiningFinding
	for harness, count := range byHarness {
		if count < 2 {
			continue
		}
		out = append(out, sessionMiningFinding{
			Kind:       "session_pattern_review",
			Summary:    fmt.Sprintf("Review %d recent %s hook events for repeated tool gaps or reusable skill updates.", count, harness),
			Confidence: 0.35,
		})
	}
	return out
}

func sessionMiningRepeatedReinventions(workbench work.WorkbenchReport) []sessionMiningFinding {
	if workbench.Counts.ContinueRuns+workbench.Counts.DispatchStepRuns < 3 {
		return nil
	}
	return []sessionMiningFinding{{
		Kind:       "control_plane_movement",
		Summary:    "Multiple recent continue/dispatch runs exist; inspect whether repeated lifecycle movement should become a smoother control.",
		Confidence: 0.4,
	}}
}

func sessionMiningRulesToRetire(workbench work.WorkbenchReport) []sessionMiningFinding {
	if workbench.Counts.NeedsReview == 0 {
		return nil
	}
	return []sessionMiningFinding{{
		Kind:       "review_friction",
		Summary:    "Review-held work exists; retire or hide any rule that only creates a decision card without changing future execution.",
		Confidence: 0.3,
	}}
}
