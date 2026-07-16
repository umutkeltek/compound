package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/distiller"
	"compound/internal/eventstore"
	"compound/internal/harvest"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

const sessionMiningReportSchemaVersion = "compound.sessions_mine_report.v0.1"
const sessionMiningApplyReportSchemaVersion = "compound.sessions_mine_apply_report.v0.1"

type sessionMiningReport struct {
	SchemaVersion         string                    `json:"schema_version"`
	Status                string                    `json:"status"`
	GeneratedAt           string                    `json:"generated_at"`
	Since                 string                    `json:"since"`
	SinceCutoff           string                    `json:"since_cutoff"`
	DryRun                bool                      `json:"dry_run"`
	SessionCount          int                       `json:"session_count"`
	HarnessCounts         map[string]int            `json:"harness_counts"`
	HookCounts            map[string]int            `json:"hook_counts"`
	PreferencesFound      []sessionMiningFinding    `json:"preferences_found"`
	StuckPoints           []sessionMiningFinding    `json:"stuck_points"`
	RepeatedReinventions  []sessionMiningFinding    `json:"repeated_reinventions"`
	CandidateSkillUpdates []sessionMiningFinding    `json:"candidate_skill_updates"`
	RulesToRetire         []sessionMiningFinding    `json:"rules_to_retire"`
	MemoryCandidates      []sessionMiningFinding    `json:"memory_candidates"`
	ProofRequired         []string                  `json:"proof_required"`
	Checks                []sessionMiningCheck      `json:"checks"`
	NextCommand           []string                  `json:"next_command"`
	Apply                 *sessionMiningApplyReport `json:"apply,omitempty"`
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

type sessionMiningApplyReport struct {
	SchemaVersion          string                        `json:"schema_version"`
	Status                 string                        `json:"status"`
	CreatedAt              string                        `json:"created_at"`
	DryRun                 bool                          `json:"dry_run"`
	PreferenceCandidateIDs []string                      `json:"preference_candidate_ids,omitempty"`
	RetireProposalIDs      []string                      `json:"retire_proposal_ids,omitempty"`
	SkillUpdateProposalIDs []string                      `json:"skill_update_proposal_ids,omitempty"`
	RoutedArtifacts        []sessionMiningRoutedArtifact `json:"routed_artifacts,omitempty"`
	Warnings               []string                      `json:"warnings,omitempty"`
}

type sessionMiningRoutedArtifact struct {
	Route          string   `json:"route"`
	ID             string   `json:"id"`
	CandidateID    string   `json:"candidate_id,omitempty"`
	PlanPath       string   `json:"plan_path,omitempty"`
	ReviewPath     string   `json:"review_path,omitempty"`
	ReviewYAMLPath string   `json:"review_yaml_path,omitempty"`
	EvidenceRefs   []string `json:"evidence_refs"`
}

type sessionHookEvent struct {
	ID             string   `json:"id"`
	CreatedAt      string   `json:"created_at"`
	Harness        string   `json:"harness"`
	HookName       string   `json:"hook_name"`
	HookCapability string   `json:"hook_capability,omitempty"`
	InputHash      string   `json:"input_hash,omitempty"`
	SideEffects    []string `json:"side_effects,omitempty"`
	OutputRef      string   `json:"output_ref,omitempty"`
	Status         string   `json:"status"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
	IDs            struct {
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
	case "harvest":
		cmdSessionsHarvest(args[1:])
	case "backfill":
		cmdSessionsBackfill(args[1:])
	default:
		fmt.Fprintln(os.Stderr, sessionsUsage())
		os.Exit(2)
	}
}

func sessionsUsage() string {
	return "usage:\n" +
		"  compound sessions mine [--dry-run|--apply] [--since 24h] [--json]\n" +
		"  compound sessions harvest --transcript <path> [--dry-run] [--json]\n" +
		"  compound sessions backfill --ledger-dir <dir> [--dry-run|--apply] [--limit N] [--json]"
}

// cmdSessionsBackfill runs the ruling harvester BACKWARDS over the historical
// decision ledgers: it parses every decision-ledger.generated.md into candidate
// rows, routes each (system ruling / product decision / ephemeral one-off),
// reconciles system rulings against the accepted decision records FIRST
// (restatements corroborate, reversals conflict, novel rulings cluster among
// themselves), and emits ONE parked candidate per novel cluster up to --limit.
// --dry-run is read-only (the live-proof path for falsifier f); --apply appends
// corroboration evidence + writes parked candidates and per-product reports.
func cmdSessionsBackfill(args []string) {
	ledgerDir := ""
	jsonOut := false
	dryRun := false
	apply := false
	limit := harvest.DefaultNovelCap
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ledger-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, sessionsUsage())
				os.Exit(2)
			}
			ledgerDir = args[i+1]
			i++
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, sessionsUsage())
				os.Exit(2)
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &limit); err != nil || limit < 0 {
				fmt.Fprintln(os.Stderr, "sessions backfill: --limit must be a non-negative integer")
				os.Exit(2)
			}
			i++
		case "--dry-run":
			dryRun = true
		case "--apply":
			apply = true
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, sessionsUsage())
			os.Exit(2)
		}
	}
	if dryRun && apply {
		fmt.Fprintln(os.Stderr, "sessions backfill: choose --dry-run or --apply")
		os.Exit(2)
	}
	// Default is read-only: applying side effects requires an explicit --apply.
	if !apply {
		dryRun = true
	}
	if strings.TrimSpace(ledgerDir) == "" {
		fmt.Fprintln(os.Stderr, "sessions backfill: --ledger-dir <dir> required")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sessions backfill:", err)
		os.Exit(1)
	}
	report, err := harvest.Backfill(store, homePath, harvest.BackfillOptions{
		LedgerDir: ledgerDir,
		DryRun:    dryRun,
		NovelCap:  limit,
		Now:       time.Now(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sessions backfill:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("# session ruling backfill%s\n\n", map[bool]string{true: " (dry-run)"}[report.DryRun])
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- ledger_dir: %s\n", report.LedgerDir)
	fmt.Printf("- files_parsed: %d\n", report.FilesParsed)
	fmt.Printf("- entries_total: %d\n", report.EntriesTotal)
	fmt.Printf("- accepted_records: %d\n", report.AcceptedRecords)
	fmt.Printf("- route[system/product/ephemeral]: %d / %d / %d\n", report.RouteCounts.System, report.RouteCounts.Product, report.RouteCounts.Ephemeral)
	fmt.Printf("- corroborated_records: %d\n", len(report.Corroborations))
	fmt.Printf("- conflicts: %d\n", len(report.Conflicts))
	fmt.Printf("- novel_clusters_found: %d (emitted %d, skipped_by_cap %d)\n", report.NovelClustersFound, report.NovelEmitted, report.NovelSkippedByCap)
	fmt.Printf("- parse_issues: %d\n", report.ParseIssueCount)
	for _, cr := range report.Corroborations {
		fmt.Printf("- corroboration [%s via %s] %s x%d\n", cr.ClaimID, cr.Via, cr.DecisionID, cr.Count)
	}
	for i, c := range report.NovelSamples {
		fmt.Printf("- novel#%d (x%d) %s\n", i+1, c.CorroborationCount, clipHarvestQuote(c.Head))
	}
}

// cmdSessionsHarvest turns operator utterances in one session transcript into
// decision-record candidates through the existing machinery: restatements of
// accepted records append corroboration evidence (no duplicate), novel rulings
// land as proposed-only decision candidates. --dry-run is read-only (the live
// proof path); default applies the corroborations/candidates.
func cmdSessionsHarvest(args []string) {
	transcript := ""
	jsonOut := false
	dryRun := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--transcript":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, sessionsUsage())
				os.Exit(2)
			}
			transcript = args[i+1]
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
	if strings.TrimSpace(transcript) == "" {
		fmt.Fprintln(os.Stderr, "sessions harvest: --transcript <path> required")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sessions harvest:", err)
		os.Exit(1)
	}
	report, err := harvest.Run(store, homePath, harvest.Options{
		TranscriptPath: transcript,
		DryRun:         dryRun,
		Now:            time.Now(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sessions harvest:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("# session ruling harvest%s\n\n", map[bool]string{true: " (dry-run)"}[dryRun])
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- transcript: %s\n", report.TranscriptPath)
	fmt.Printf("- session: %s\n", report.SessionID)
	fmt.Printf("- utterances: %d\n", report.Utterances)
	fmt.Printf("- classified: %d\n", report.Classified)
	fmt.Printf("- corroborations: %d\n", report.Corroborations)
	fmt.Printf("- conflicts: %d\n", report.Conflicts)
	fmt.Printf("- novel: %d\n", report.Novel)
	for _, item := range report.Items {
		matched := ""
		if item.Match != nil {
			matched = fmt.Sprintf(" -> %s (%.2f)", item.Match.ClaimID, item.Match.Score)
		}
		fmt.Printf("- [%s/%s L%d]%s %s\n", item.Outcome, item.Kind, item.LineNo, matched, clipHarvestQuote(item.Quote))
	}
}

func clipHarvestQuote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= 100 {
		return s
	}
	return string([]rune(s)[:97]) + "..."
}

func cmdSessionsMine(args []string) {
	since := "24h"
	jsonOut := false
	dryRun := false
	apply := false
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
		case "--apply":
			apply = true
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, sessionsUsage())
			os.Exit(2)
		}
	}
	if dryRun && apply {
		fmt.Fprintln(os.Stderr, "sessions mine: choose --dry-run or --apply")
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
	if apply {
		applyReport, err := applySessionMiningReport(homePath, &report, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "sessions mine apply:", err)
			os.Exit(1)
		}
		report.Apply = &applyReport
		report.DryRun = false
		report.Status = applyReport.Status
		report.NextCommand = []string{"compound", "reviews", "--json"}
	} else if !dryRun {
		report.NextCommand = []string{"compound", "sessions", "mine", "--since", since, "--apply", "--json"}
	}
	if jsonOut {
		printJSON(report)
		return
	}
	if apply {
		fmt.Printf("# session mining apply\n\n")
	} else {
		fmt.Printf("# session mining dry-run\n\n")
	}
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- since: %s\n", report.Since)
	fmt.Printf("- sessions: %d\n", report.SessionCount)
	fmt.Printf("- stuck_points: %d\n", len(report.StuckPoints))
	fmt.Printf("- memory_candidates: %d\n", len(report.MemoryCandidates))
	fmt.Printf("- skill_updates: %d\n", len(report.CandidateSkillUpdates))
	fmt.Printf("- preferences_found: %d\n", len(report.PreferencesFound))
	if report.Apply != nil {
		fmt.Printf("- preference_candidates: %d\n", len(report.Apply.PreferenceCandidateIDs))
		fmt.Printf("- retire_proposals: %d\n", len(report.Apply.RetireProposalIDs))
		fmt.Printf("- skill_update_proposals: %d\n", len(report.Apply.SkillUpdateProposalIDs))
	}
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
	report.PreferencesFound = sessionMiningPreferences(events, workbench)
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
	store, err := eventstore.Open(homePath)
	if err != nil {
		return nil, nil, err
	}
	// Read authoritative hook_ records via the Scan seam (hook-events.ndjson is
	// frozen post-migration). Session mining groups by harness / preference signal
	// and accumulates evidence refs in event order, so records are sorted
	// deterministically by (created_at, id) below — reproducing the prior append
	// order on a clean home while staying stable across the deduped store.
	entries, err := store.Scan(eventstore.ScanOptions{Prefixes: []string{"hook_"}, RegularOnly: true})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read hook events: %w", err)
	}
	var out []sessionHookEvent
	var warnings []string
	for _, entry := range entries {
		rec, err := entry.Read()
		if err != nil {
			return nil, nil, fmt.Errorf("read hook events: %w", err)
		}
		var ev sessionHookEvent
		if err := json.Unmarshal(rec.Bytes, &ev); err != nil {
			warnings = append(warnings, fmt.Sprintf("record %s unreadable", entry.ID))
			continue
		}
		created, err := time.Parse(time.RFC3339, ev.CreatedAt)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("record %s missing created_at", entry.ID))
			continue
		}
		if created.Before(cutoff) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
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
	byHarness := map[string][]sessionHookEvent{}
	for _, ev := range events {
		if ev.Harness != "" {
			byHarness[ev.Harness] = append(byHarness[ev.Harness], ev)
		}
	}
	var out []sessionMiningFinding
	for harness, grouped := range byHarness {
		count := len(grouped)
		if count < 2 {
			continue
		}
		out = append(out, sessionMiningFinding{
			Kind:         "session_pattern_review",
			Summary:      fmt.Sprintf("Review %d recent %s hook events for repeated tool gaps or reusable skill updates.", count, harness),
			EvidenceRefs: sessionEventGroupEvidenceRefs(grouped),
			Confidence:   0.35,
		})
	}
	sortSessionFindings(out)
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
	refs := []string{}
	for _, item := range workbench.Open {
		if item.State != "held" && item.State != "blocked" {
			continue
		}
		refs = compactSessionRefs(append([]string{"work:" + item.WorkID}, item.EvidenceRefs...)...)
		if len(refs) > 0 {
			break
		}
	}
	return []sessionMiningFinding{{
		Kind:         "review_friction",
		Summary:      "Review-held work exists; retire or hide any rule that only creates a decision card without changing future execution.",
		EvidenceRefs: refs,
		Confidence:   0.3,
	}}
}

type sessionPreferenceSignal struct {
	Kind         string
	Label        string
	EvidenceRefs []string
}

type sessionPreferenceGroup struct {
	Kind         string
	Label        string
	Count        int
	EvidenceRefs []string
}

func sessionMiningPreferences(events []sessionHookEvent, workbench work.WorkbenchReport) []sessionMiningFinding {
	groups := map[string]*sessionPreferenceGroup{}
	add := func(sig sessionPreferenceSignal) {
		if sig.Kind == "" || sig.Label == "" || len(sig.EvidenceRefs) == 0 {
			return
		}
		key := sig.Kind + "\x00" + strings.ToLower(strings.TrimSpace(sig.Label))
		group := groups[key]
		if group == nil {
			group = &sessionPreferenceGroup{Kind: sig.Kind, Label: sig.Label}
			groups[key] = group
		}
		group.Count++
		group.EvidenceRefs = compactSessionRefs(append(group.EvidenceRefs, sig.EvidenceRefs...)...)
	}
	for _, ev := range events {
		for _, sig := range sessionPreferenceSignalsFromEvent(ev) {
			add(sig)
		}
	}
	for _, sig := range sessionPreferenceSignalsFromWorkbench(workbench) {
		add(sig)
	}
	out := make([]sessionMiningFinding, 0, len(groups))
	for _, group := range groups {
		if group.Count < 2 {
			continue
		}
		out = append(out, sessionMiningFinding{
			Kind:         group.Kind,
			Summary:      sessionPreferenceSummary(group.Kind, group.Label, group.Count),
			EvidenceRefs: group.EvidenceRefs,
			Confidence:   sessionPreferenceConfidence(group.Kind, group.Count),
		})
	}
	sortSessionFindings(out)
	return out
}

func sessionPreferenceSignalsFromEvent(ev sessionHookEvent) []sessionPreferenceSignal {
	refs := sessionEventEvidenceRefs(ev)
	if len(refs) == 0 {
		return nil
	}
	var out []sessionPreferenceSignal
	for _, effect := range ev.SideEffects {
		kind, label, ok := sessionPreferenceSignalFromSideEffect(effect, ev.HookName)
		if !ok {
			continue
		}
		out = append(out, sessionPreferenceSignal{Kind: kind, Label: label, EvidenceRefs: refs})
	}
	return out
}

func sessionPreferenceSignalFromSideEffect(effect, hookName string) (string, string, bool) {
	raw := strings.TrimSpace(effect)
	if raw == "" {
		return "", "", false
	}
	lower := strings.ToLower(raw)
	prefixes := []struct {
		Prefix string
		Kind   string
	}{
		{"operator_correction:", "operator_correction"},
		{"correction:", "operator_correction"},
		{"manual_override:", "manual_override"},
		{"operator_override:", "manual_override"},
		{"override:", "manual_override"},
		{"tool_choice:", "repeated_tool_choice"},
		{"tool:", "repeated_tool_choice"},
		{"lane_choice:", "repeated_lane_choice"},
		{"route_lane:", "repeated_lane_choice"},
		{"lane:", "repeated_lane_choice"},
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix.Prefix) {
			label := strings.TrimSpace(raw[len(prefix.Prefix):])
			if label == "" {
				return "", "", false
			}
			return prefix.Kind, label, true
		}
	}
	if strings.EqualFold(strings.TrimSpace(hookName), "PostToolUse") && plainSessionToken(raw) {
		return "repeated_tool_choice", raw, true
	}
	return "", "", false
}

func sessionPreferenceSignalsFromWorkbench(workbench work.WorkbenchReport) []sessionPreferenceSignal {
	var out []sessionPreferenceSignal
	items := append([]work.WorkbenchItem{}, workbench.Open...)
	items = append(items, workbench.Recent...)
	for _, item := range items {
		for _, obs := range item.RunObservations {
			for _, raw := range []string{obs.Note, obs.State} {
				kind, label, ok := sessionPreferenceSignalFromSideEffect(raw, "")
				if !ok {
					continue
				}
				out = append(out, sessionPreferenceSignal{
					Kind:         kind,
					Label:        label,
					EvidenceRefs: compactSessionRefs("work:"+item.WorkID, "run_observation:"+obs.ID, obs.ActionIntentID, obs.PolicyDecisionID),
				})
			}
		}
		for _, route := range item.RouteOutcomes {
			label := strings.Join(compactSessionRefs(route.Provider, route.Model, route.Protocol), "/")
			if label == "" {
				continue
			}
			out = append(out, sessionPreferenceSignal{
				Kind:         "repeated_lane_choice",
				Label:        label,
				EvidenceRefs: compactSessionRefs("work:"+item.WorkID, "receipt:"+route.ReceiptID, route.ReviewRef, route.RouteEvidenceID),
			})
		}
	}
	return out
}

func sessionPreferenceSummary(kind, label string, count int) string {
	switch kind {
	case "operator_correction":
		return fmt.Sprintf("Operator repeated the same correction %q across %d session evidence refs.", label, count)
	case "manual_override":
		return fmt.Sprintf("Operator repeatedly overrode the same suggestion %q across %d session evidence refs.", label, count)
	case "repeated_tool_choice":
		return fmt.Sprintf("Operator or harness repeatedly chose tool %q across %d session evidence refs.", label, count)
	case "repeated_lane_choice":
		return fmt.Sprintf("Operator or harness repeatedly chose lane %q across %d session evidence refs.", label, count)
	default:
		return fmt.Sprintf("Repeated session preference %q across %d evidence refs.", label, count)
	}
}

func sessionPreferenceConfidence(kind string, count int) float64 {
	base := map[string]float64{
		"operator_correction":  0.72,
		"manual_override":      0.7,
		"repeated_tool_choice": 0.62,
		"repeated_lane_choice": 0.62,
	}
	conf := base[kind]
	if conf == 0 {
		conf = 0.55
	}
	conf += float64(count-2) * 0.05
	if conf > 0.9 {
		return 0.9
	}
	return conf
}

func plainSessionToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, ":") || strings.Contains(value, " ") {
		return false
	}
	return len(value) <= 48
}

func sessionEventEvidenceRefs(ev sessionHookEvent) []string {
	refs := []string{}
	if ev.ID != "" {
		refs = append(refs, "hook:"+ev.ID)
	}
	if ev.IDs.SessionID != "" {
		refs = append(refs, "session:"+ev.IDs.SessionID)
	}
	if ev.IDs.WorkID != "" {
		refs = append(refs, "work:"+ev.IDs.WorkID)
	}
	if ev.OutputRef != "" {
		refs = append(refs, ev.OutputRef)
	}
	refs = append(refs, ev.EvidenceRefs...)
	return compactSessionRefs(refs...)
}

func sessionEventGroupEvidenceRefs(events []sessionHookEvent) []string {
	refs := []string{}
	for _, ev := range events {
		refs = append(refs, sessionEventEvidenceRefs(ev)...)
	}
	return compactSessionRefs(refs...)
}

func sortSessionFindings(findings []sessionMiningFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		return findings[i].Summary < findings[j].Summary
	})
}

func applySessionMiningReport(homePath string, report *sessionMiningReport, now time.Time) (sessionMiningApplyReport, error) {
	out := sessionMiningApplyReport{
		SchemaVersion: sessionMiningApplyReportSchemaVersion,
		Status:        "review_required",
		CreatedAt:     now.UTC().Format(time.RFC3339),
		DryRun:        false,
	}
	for _, finding := range report.PreferencesFound {
		candidateID, err := recordSessionPreferenceCandidate(homePath, finding, now)
		if err != nil {
			return out, err
		}
		out.PreferenceCandidateIDs = append(out.PreferenceCandidateIDs, candidateID)
		out.RoutedArtifacts = append(out.RoutedArtifacts, sessionMiningRoutedArtifact{
			Route:        "memory_candidate",
			ID:           candidateID,
			EvidenceRefs: finding.EvidenceRefs,
		})
	}
	for _, finding := range report.RulesToRetire {
		artifact, err := recordSessionRuleRetireProposal(homePath, finding, now)
		if err != nil {
			return out, err
		}
		out.RetireProposalIDs = append(out.RetireProposalIDs, artifact.ID)
		out.RoutedArtifacts = append(out.RoutedArtifacts, artifact)
	}
	for _, finding := range report.CandidateSkillUpdates {
		artifact, err := recordSessionSkillLifecycleProposal(homePath, finding, now)
		if err != nil {
			return out, err
		}
		out.SkillUpdateProposalIDs = append(out.SkillUpdateProposalIDs, artifact.ID)
		out.RoutedArtifacts = append(out.RoutedArtifacts, artifact)
	}
	if len(out.PreferenceCandidateIDs) == 0 && len(out.RetireProposalIDs) == 0 && len(out.SkillUpdateProposalIDs) == 0 {
		out.Status = "ok"
		out.Warnings = append(out.Warnings, "no routed findings met deterministic thresholds")
	}
	sort.Strings(out.PreferenceCandidateIDs)
	sort.Strings(out.RetireProposalIDs)
	sort.Strings(out.SkillUpdateProposalIDs)
	return out, nil
}

func recordSessionPreferenceCandidate(homePath string, finding sessionMiningFinding, now time.Time) (string, error) {
	if err := requireSessionFindingEvidence(finding); err != nil {
		return "", err
	}
	store, err := mem.Open(homePath)
	if err != nil {
		return "", err
	}
	candidates, err := store.Capture([]mem.Candidate{{
		Entity:          "operator",
		Predicate:       "preference." + reviewartifact.Slugify(finding.Kind, 32),
		Object:          finding.Summary,
		ObservationDate: now.UTC().Format(time.RFC3339),
		Confidence:      finding.Confidence,
		Derivation:      "deterministic",
		ProvenanceLane:  "deterministic",
		ClaimType:       "preference",
		Qualifiers: map[string]string{
			"domain":       "behavioral",
			"source":       "sessions_mine",
			"finding_kind": finding.Kind,
		},
		SourceTraceRef: finding.EvidenceRefs[0],
		SnippetHash:    "sha256:" + sessionMiningDigest(finding.Summary, strings.Join(finding.EvidenceRefs, "\n")),
		Visibility:     "contextual",
		Reason:         "session-mined behavioral preference candidate; requires adjudication before recall",
	}}, now)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("preference candidate was not recorded for finding %q", finding.Summary)
	}
	return candidates[0].ID, nil
}

func recordSessionRuleRetireProposal(homePath string, finding sessionMiningFinding, now time.Time) (sessionMiningRoutedArtifact, error) {
	if err := requireSessionFindingEvidence(finding); err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	candidateID := "session_rule_retire_" + reviewartifact.Digest12(finding.Kind+finding.Summary+strings.Join(finding.EvidenceRefs, "\x00"))
	candidate := distiller.Candidate{
		ID:            candidateID,
		Kind:          distiller.KindPolicyPatch,
		Subject:       "session-mined rule retirement proposal",
		Statement:     finding.Summary,
		Detail:        "Review whether this rule only creates review friction without changing future execution. Do not retire any rule without an operator-recorded review.",
		Confidence:    finding.Confidence,
		Derivation:    "deterministic",
		EvidenceRefs:  compactSessionRefs(finding.EvidenceRefs...),
		PolicyVersion: distiller.DefaultPolicy().Stamp(),
		Verdict:       candidates.NoPromote,
	}
	if err := candidates.WriteCandidate(homePath, candidates.PolicyPatch, candidateID, candidate); err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	plan, err := distiller.BuildImprovementPlan(homePath, candidateID, "", now)
	if err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	paths, err := distiller.RecordImprovementPlan(homePath, plan)
	if err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	return sessionMiningRoutedArtifact{
		Route:          "distiller_improvement_plan",
		ID:             plan.ID,
		CandidateID:    candidateID,
		PlanPath:       paths.PlanPath,
		ReviewPath:     paths.ReviewPath,
		ReviewYAMLPath: paths.ReviewYAMLPath,
		EvidenceRefs:   plan.EvidenceRefs,
	}, nil
}

func recordSessionSkillLifecycleProposal(homePath string, finding sessionMiningFinding, now time.Time) (sessionMiningRoutedArtifact, error) {
	if err := requireSessionFindingEvidence(finding); err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	proposal, err := capgraph.BuildCapabilityLifecycleProposal(capgraph.CapabilityLifecycleProposalInput{
		ProposalKind: "revise",
		Subject:      "session-mined skill update proposal",
		Statement:    finding.Summary,
		Detail:       "Review whether the repeated session pattern should revise, narrow, or retire an existing capability. Do not install or mutate skills from this proposal.",
		Confidence:   finding.Confidence,
		EvidenceRefs: finding.EvidenceRefs,
	}, now)
	if err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	paths, err := capgraph.RecordCapabilityLifecycleProposal(homePath, proposal)
	if err != nil {
		return sessionMiningRoutedArtifact{}, err
	}
	return sessionMiningRoutedArtifact{
		Route:        "capability_lifecycle_proposal",
		ID:           proposal.ID,
		PlanPath:     paths.PlanPath,
		ReviewPath:   paths.ReviewPath,
		EvidenceRefs: proposal.EvidenceRefs,
	}, nil
}

func requireSessionFindingEvidence(finding sessionMiningFinding) error {
	if len(compactSessionRefs(finding.EvidenceRefs...)) == 0 {
		return fmt.Errorf("finding %q missing session evidence refs", finding.Summary)
	}
	return nil
}

func sessionMiningDigest(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}
