package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

const orientOperatorCursorSchemaVersion = "compound.orient_operator_cursor.v0.1"
const orientOperatorReportSchemaVersion = "compound.orient_operator.v0.1"

type orientOperatorReport struct {
	SchemaVersion string            `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	NeedsMe       orientNeedsMe     `json:"needs_me"`
	Running       orientRunning     `json:"running"`
	Changed       orientChanged     `json:"changed"`
	DrillDown     []orientDrillDown `json:"drill_down"`
	Warnings      []string          `json:"warnings,omitempty"`
}

type orientNeedsMe struct {
	CandidateTotal         int                    `json:"candidate_total"`
	CandidateActionable    int                    `json:"candidate_actionable"`
	CandidateInboxes       []orientCandidateInbox `json:"candidate_inboxes"`
	ReviewRequired         int                    `json:"review_required"`
	ReadyToApply           int                    `json:"ready_to_apply"`
	PendingPanelNeedsHuman int                    `json:"pending_panel_needs_human"`
	OldestItem             *orientAgedItem        `json:"oldest_item,omitempty"`
	OldestActionableItem   *orientAgedItem        `json:"oldest_actionable_item,omitempty"`
}

type orientCandidateInbox struct {
	Kind            string         `json:"kind"`
	Dir             string         `json:"dir"`
	Count           int            `json:"count"`
	ActionableCount int            `json:"actionable_count"`
	StateCounts     map[string]int `json:"state_counts,omitempty"`
}

type orientAgedItem struct {
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	Path       string `json:"path,omitempty"`
	At         string `json:"at"`
	AgeSeconds int64  `json:"age_seconds"`
	Age        string `json:"age"`
}

type orientRunning struct {
	OpenCount           int                  `json:"open_count"`
	InProgress          int                  `json:"in_progress"`
	NonStaleLeaseCount  int                  `json:"non_stale_lease_count"`
	LastDispatchStepRun *orientAgedItem      `json:"last_dispatch_step_run,omitempty"`
	OpenWork            []orientOpenWorkItem `json:"open_work"`
}

type orientOpenWorkItem struct {
	WorkID            string   `json:"work_id"`
	State             string   `json:"state"`
	Actor             string   `json:"actor,omitempty"`
	ClaimedAt         string   `json:"claimed_at,omitempty"`
	ClaimedAgeSeconds int64    `json:"claimed_age_seconds,omitempty"`
	ClaimedAge        string   `json:"claimed_age,omitempty"`
	NextAction        string   `json:"next_action,omitempty"`
	ObservatoryTags   []string `json:"observatory_tags,omitempty"`
	Surface           string   `json:"surface,omitempty"`
}

type orientChanged struct {
	CurrentClaimSeq int64                    `json:"current_claim_seq"`
	LastSeenSeq     int64                    `json:"last_seen_seq"`
	ClaimsSeqDelta  int64                    `json:"claims_seq_delta"`
	FirstRun        bool                     `json:"first_run"`
	CursorPath      string                   `json:"cursor_path"`
	Recent          []orientRecentChangeItem `json:"recent"`
}

type orientRecentChangeItem struct {
	WorkID       string   `json:"work_id"`
	ReceiptID    string   `json:"receipt_id,omitempty"`
	TransitionID string   `json:"transition_id,omitempty"`
	MoveType     string   `json:"move_type,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Changed      bool     `json:"changed"`
	CreatedAt    string   `json:"created_at,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type orientDrillDown struct {
	Label   string   `json:"label"`
	Command []string `json:"command"`
}

type orientOperatorCursor struct {
	SchemaVersion string `json:"schema_version"`
	UpdatedAt     string `json:"updated_at"`
	LastSeenSeq   int64  `json:"last_seen_seq"`
}

func cmdOrientOperator(homePath string, args []string) {
	jsonOut := false
	recent := 5
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--operator":
		case "--json":
			jsonOut = true
		case "--recent":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound orient --operator [--json] [--recent N]")
				os.Exit(2)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "error: --recent must be a non-negative integer")
				os.Exit(2)
			}
			recent = n
		default:
			fmt.Fprintln(os.Stderr, "usage: compound orient --operator [--json] [--recent N]")
			os.Exit(2)
		}
	}
	report, err := buildOrientOperatorReport(homePath, recent, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "orient --operator:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	printOrientOperatorReport(report)
}

func buildOrientOperatorReport(homePath string, recent int, now time.Time) (orientOperatorReport, error) {
	if recent <= 0 {
		recent = 5
	}
	store, err := work.Open(homePath)
	if err != nil {
		return orientOperatorReport{}, err
	}
	workbench, err := store.Workbench(now, work.WorkbenchOptions{RecentLimit: recent, Actor: "codex"})
	if err != nil {
		return orientOperatorReport{}, err
	}
	reviews, err := buildReviewQueueReport(homePath, "", false)
	if err != nil {
		return orientOperatorReport{}, err
	}
	currentSeq, err := maxClaimSeq(homePath)
	if err != nil {
		return orientOperatorReport{}, err
	}
	cursor, cursorPath, firstRun, err := readOrientOperatorCursor(homePath)
	if err != nil {
		return orientOperatorReport{}, err
	}
	report := orientOperatorReport{
		SchemaVersion: orientOperatorReportSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		NeedsMe:       buildOrientNeedsMe(homePath, reviews, now),
		Running:       buildOrientRunning(homePath, workbench, now),
		Changed:       buildOrientChanged(homePath, workbench, currentSeq, cursor.LastSeenSeq, firstRun, cursorPath),
		DrillDown: []orientDrillDown{
			{Label: "workbench", Command: []string{"compound", "workbench"}},
			{Label: "reviews", Command: []string{"compound", "reviews"}},
			{Label: "memory doctor", Command: []string{"compound", "memory", "doctor"}},
		},
	}
	if err := writeOrientOperatorCursor(homePath, currentSeq, now); err != nil {
		return orientOperatorReport{}, err
	}
	return report, nil
}

func buildOrientNeedsMe(homePath string, reviews reviewartifact.QueueReport, now time.Time) orientNeedsMe {
	out := orientNeedsMe{
		CandidateInboxes: []orientCandidateInbox{},
		ReviewRequired:   reviews.Counts.ReviewRequired,
		ReadyToApply:     reviews.Counts.ReadyToApply,
	}
	var oldest *orientAgedItem
	var oldestActionable *orientAgedItem
	for _, kind := range candidates.AllKinds() {
		dir := candidates.InboxDir(kind)
		summary := candidateInboxSummary(homePath, dir, string(kind), now)
		out.CandidateTotal += summary.Count
		out.CandidateActionable += summary.ActionableCount
		out.CandidateInboxes = append(out.CandidateInboxes, orientCandidateInbox{
			Kind:            string(kind),
			Dir:             dir,
			Count:           summary.Count,
			ActionableCount: summary.ActionableCount,
			StateCounts:     summary.StateCounts,
		})
		oldest = olderAgedItem(oldest, summary.Oldest)
		oldestActionable = olderAgedItem(oldestActionable, summary.OldestActionable)
	}
	for _, item := range reviews.Items {
		if item.Area == "panel-verdicts" && item.Readiness == "needs_review" && strings.Contains(item.NextAction, "needs_human") {
			out.PendingPanelNeedsHuman++
		}
		if reviewOldest := reviewQueueAgedItem(item, now); reviewOldest != nil {
			oldest = olderAgedItem(oldest, reviewOldest)
		}
	}
	out.OldestItem = oldest
	out.OldestActionableItem = oldestActionable
	return out
}

func buildOrientRunning(homePath string, report work.WorkbenchReport, now time.Time) orientRunning {
	openIDs := map[string]bool{}
	out := orientRunning{
		OpenCount:  report.Counts.Open,
		InProgress: report.Counts.InProgress,
		OpenWork:   []orientOpenWorkItem{},
	}
	for _, item := range report.Open {
		openIDs[item.WorkID] = true
		actor := strings.TrimSpace(item.ClaimedBy)
		open := orientOpenWorkItem{
			WorkID:          item.WorkID,
			State:           item.State,
			Actor:           actor,
			ClaimedAt:       item.ClaimedAt,
			NextAction:      item.NextAction,
			ObservatoryTags: append([]string{}, item.ObservatoryTags...),
			Surface:         item.CanonicalSurface,
		}
		if claimedAt, ok := parseCompoundTime(item.ClaimedAt); ok {
			open.ClaimedAgeSeconds = ageSeconds(now, claimedAt)
			open.ClaimedAge = formatDuration(open.ClaimedAgeSeconds)
		}
		out.OpenWork = append(out.OpenWork, open)
	}
	out.NonStaleLeaseCount = activeLeaseCount(homePath, openIDs)
	for _, run := range report.DispatchStepRuns {
		if !orientOperatorDispatchStepRunActive(run) {
			continue
		}
		if at, ok := parseCompoundTime(run.GeneratedAt); ok {
			out.LastDispatchStepRun = &orientAgedItem{
				Kind:       "dispatch_step_run",
				ID:         run.ID,
				Path:       run.ResultPath,
				At:         at.UTC().Format(time.RFC3339),
				AgeSeconds: ageSeconds(now, at),
				Age:        formatDuration(ageSeconds(now, at)),
			}
		}
		break
	}
	return out
}

func orientOperatorDispatchStepRunActive(run work.DispatchStepRunSummary) bool {
	if strings.TrimSpace(run.Status) == "resolved" {
		return false
	}
	switch strings.TrimSpace(run.WaitReason) {
	case "no_open_work", "source_packet_terminal", "terminal_work":
		return false
	default:
		return true
	}
}

func buildOrientChanged(homePath string, report work.WorkbenchReport, currentSeq, lastSeenSeq int64, firstRun bool, cursorPath string) orientChanged {
	delta := currentSeq - lastSeenSeq
	if firstRun {
		delta = currentSeq
	}
	if delta < 0 {
		delta = 0
	}
	out := orientChanged{
		CurrentClaimSeq: currentSeq,
		LastSeenSeq:     lastSeenSeq,
		ClaimsSeqDelta:  delta,
		FirstRun:        firstRun,
		CursorPath:      cursorPath,
		Recent:          []orientRecentChangeItem{},
	}
	for _, item := range report.Recent {
		change := orientRecentChangeItem{
			WorkID:       item.WorkID,
			ReceiptID:    item.ReceiptID,
			TransitionID: item.TransitionID,
			MoveType:     item.MoveType,
			Changed:      item.Changed,
			EvidenceRefs: append([]string{}, item.EvidenceRefs...),
		}
		if item.ReceiptID != "" {
			if rec, err := readReceipt(homePath, item.ReceiptID); err == nil {
				change.MoveType = rec.MoveType
				change.Summary = rec.StateDelta.Summary
				change.Changed = rec.EffectiveChanged()
				change.CreatedAt = rec.CreatedAt
			}
		}
		out.Recent = append(out.Recent, change)
	}
	return out
}

type orientCandidateInboxSummary struct {
	Count            int
	ActionableCount  int
	StateCounts      map[string]int
	Oldest           *orientAgedItem
	OldestActionable *orientAgedItem
}

func candidateInboxSummary(homePath, dir, kind string, now time.Time) orientCandidateInboxSummary {
	out := orientCandidateInboxSummary{StateCounts: map[string]int{}}
	entries, err := os.ReadDir(filepath.Join(homePath, dir))
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		out.Count++
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fullPath := filepath.Join(homePath, path)
		state := candidateReadinessState(fullPath)
		out.StateCounts[state]++
		actionable := candidateStateActionable(state)
		if actionable {
			out.ActionableCount++
		}
		at := info.ModTime()
		aged := &orientAgedItem{
			Kind:       "candidate:" + kind,
			ID:         strings.TrimSuffix(entry.Name(), ".json"),
			Path:       filepath.ToSlash(path),
			At:         at.UTC().Format(time.RFC3339),
			AgeSeconds: ageSeconds(now, at),
			Age:        formatDuration(ageSeconds(now, at)),
		}
		out.Oldest = olderAgedItem(out.Oldest, aged)
		if actionable {
			out.OldestActionable = olderAgedItem(out.OldestActionable, aged)
		}
	}
	return out
}

func candidateReadinessState(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer file.Close()
	var payload struct {
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(file).Decode(&payload); err != nil {
		return "unreadable"
	}
	state := strings.TrimSpace(payload.State)
	if state == "" {
		state = strings.TrimSpace(payload.Status)
	}
	if state == "" {
		return "unspecified"
	}
	return state
}

func candidateStateActionable(state string) bool {
	switch strings.TrimSpace(state) {
	case "", "unspecified", "proposed", "pending", "needs_review", "review_required", "blocked":
		return true
	case "promoted", "rejected", "parked", "done", "recorded", "resolved", "accepted", "applied", "ok", "no_promote":
		return false
	default:
		return true
	}
}

func reviewQueueAgedItem(item reviewartifact.QueueItem, now time.Time) *orientAgedItem {
	if item.Status == "ok" || item.Status == "applied" || item.Status == "resolved" || item.Status == "accepted" || item.Status == "recorded" {
		return nil
	}
	var at time.Time
	if parsed, ok := parseCompoundTime(item.CreatedAt); ok {
		at = parsed
	} else if item.JSONPath != "" {
		if info, err := os.Stat(item.JSONPath); err == nil {
			at = info.ModTime()
		}
	}
	if at.IsZero() {
		return nil
	}
	return &orientAgedItem{
		Kind:       "review:" + item.Area,
		ID:         item.ID,
		Path:       item.JSONPath,
		At:         at.UTC().Format(time.RFC3339),
		AgeSeconds: ageSeconds(now, at),
		Age:        formatDuration(ageSeconds(now, at)),
	}
}

func activeLeaseCount(homePath string, openIDs map[string]bool) int {
	entries, err := os.ReadDir(filepath.Join(homePath, "work", "leases"))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if openIDs[id] {
			count++
		}
	}
	return count
}

func maxClaimSeq(homePath string) (int64, error) {
	path := filepath.Join(homePath, "ledger", "claims.jsonl")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var max int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var claim mem.Claim
		if err := json.Unmarshal(scanner.Bytes(), &claim); err != nil {
			return 0, fmt.Errorf("parse %s: %w", path, err)
		}
		if claim.Seq > max {
			max = claim.Seq
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return max, nil
}

func readOrientOperatorCursor(homePath string) (orientOperatorCursor, string, bool, error) {
	path := orientOperatorCursorPath(homePath)
	var cursor orientOperatorCursor
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cursor, path, true, nil
	}
	if err != nil {
		return cursor, path, false, err
	}
	if err := json.Unmarshal(data, &cursor); err != nil {
		return cursor, path, false, err
	}
	return cursor, path, false, nil
}

func writeOrientOperatorCursor(homePath string, seq int64, now time.Time) error {
	path := orientOperatorCursorPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cursor := orientOperatorCursor{
		SchemaVersion: orientOperatorCursorSchemaVersion,
		UpdatedAt:     now.UTC().Format(time.RFC3339),
		LastSeenSeq:   seq,
	}
	data, err := json.MarshalIndent(cursor, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func orientOperatorCursorPath(homePath string) string {
	return filepath.Join(homePath, "projections", "orient-operator-last-seq.json")
}

func readReceipt(homePath, id string) (envelope.Receipt, error) {
	var rec envelope.Receipt
	data, err := os.ReadFile(filepath.Join(homePath, "events", id+".json"))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, err
	}
	return rec, nil
}

func olderAgedItem(current, candidate *orientAgedItem) *orientAgedItem {
	if candidate == nil {
		return current
	}
	if current == nil {
		return candidate
	}
	if candidate.AgeSeconds > current.AgeSeconds {
		return candidate
	}
	return current
}

func parseCompoundTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(0, n), true
	}
	return time.Time{}, false
}

func ageSeconds(now, at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	seconds := int64(now.Sub(at).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func formatDuration(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	return fmt.Sprintf("%dd", days)
}

func printOrientOperatorReport(report orientOperatorReport) {
	fmt.Printf("# operator orient %s\n", report.GeneratedAt)
	fmt.Println()
	fmt.Println("NEEDS-ME")
	fmt.Printf("- candidates: actionable=%d total=%d oldest_actionable=%s oldest_raw=%s\n",
		report.NeedsMe.CandidateActionable,
		report.NeedsMe.CandidateTotal,
		agedLabel(report.NeedsMe.OldestActionableItem),
		agedLabel(report.NeedsMe.OldestItem),
	)
	for _, inbox := range report.NeedsMe.CandidateInboxes {
		if inbox.Count == 0 {
			continue
		}
		fmt.Printf("  - %s: actionable=%d total=%d (%s)\n", inbox.Kind, inbox.ActionableCount, inbox.Count, inbox.Dir)
	}
	fmt.Printf("- reviews: review_required=%d ready_to_apply=%d pending_panel_needs_human=%d\n",
		report.NeedsMe.ReviewRequired,
		report.NeedsMe.ReadyToApply,
		report.NeedsMe.PendingPanelNeedsHuman,
	)
	fmt.Println()
	fmt.Println("RUNNING")
	fmt.Printf("- open=%d in_progress=%d non_stale_leases=%d last_dispatch_step_run=%s\n",
		report.Running.OpenCount,
		report.Running.InProgress,
		report.Running.NonStaleLeaseCount,
		agedLabel(report.Running.LastDispatchStepRun),
	)
	if len(report.Running.OpenWork) == 0 {
		fmt.Println("- open_work: none")
	} else {
		for _, item := range report.Running.OpenWork {
			actor := reviewartifact.FirstNonEmpty(item.Actor, "-")
			claimed := "-"
			if item.ClaimedAge != "" {
				claimed = item.ClaimedAge
			}
			fmt.Printf("- %s state=%s actor=%s claimed_age=%s next=%s\n", item.WorkID, item.State, actor, claimed, item.NextAction)
		}
	}
	fmt.Println()
	fmt.Println("CHANGED")
	fmt.Printf("- claims_seq: current=%d delta=%d last_seen=%d first_run=%t\n",
		report.Changed.CurrentClaimSeq,
		report.Changed.ClaimsSeqDelta,
		report.Changed.LastSeenSeq,
		report.Changed.FirstRun,
	)
	if len(report.Changed.Recent) == 0 {
		fmt.Println("- recent: none")
	} else {
		for _, item := range report.Changed.Recent {
			fmt.Printf("- %s receipt=%s transition=%s move=%s changed=%t summary=%s\n",
				item.WorkID,
				emptyDash(item.ReceiptID),
				emptyDash(item.TransitionID),
				emptyDash(item.MoveType),
				item.Changed,
				reviewartifact.FirstNonEmpty(item.Summary, "-"),
			)
		}
	}
	fmt.Println()
	fmt.Println("DRILL-DOWN")
	for _, drill := range report.DrillDown {
		fmt.Printf("- %s: %s\n", drill.Label, strings.Join(drill.Command, " "))
	}
}

func agedLabel(item *orientAgedItem) string {
	if item == nil {
		return "none"
	}
	parts := []string{item.Age}
	if item.Kind != "" {
		parts = append(parts, item.Kind)
	}
	if item.ID != "" {
		parts = append(parts, item.ID)
	}
	return strings.Join(parts, " ")
}
