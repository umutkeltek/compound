package lanebeliefs

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/mem"
)

const (
	MIN_LANE_RECEIPTS       = 5
	MIN_CORROBORATION_WORKS = 2
	BETA_PRIOR_ALPHA        = 2
	BETA_PRIOR_BETA         = 2
	PROMOTE_POSTERIOR_FLOOR = 0.66
	PER_CYCLE_CANDIDATE_CAP = 50
	COOL_AFTER_FOLDS        = 3
)

const foldReportSchemaVersion = "compound.lanebeliefs.fold.v0.1"
const listReportSchemaVersion = "compound.lanebeliefs.list.v0.1"

type FoldOptions struct {
	DryRun bool
	Now    time.Time
}

type FoldReport struct {
	SchemaVersion             string          `json:"schema_version"`
	GeneratedAt               string          `json:"generated_at"`
	DryRun                    bool            `json:"dry_run"`
	CursorBefore              int64           `json:"cursor_before"`
	CursorAfter               int64           `json:"cursor_after"`
	RawReceiptCount           int             `json:"raw_receipt_count"`
	UnresolvedReceiptCount    int             `json:"unresolved_receipt_count"`
	IgnoredReceiptCount       int             `json:"ignored_receipt_count"`
	NewObservationCount       int             `json:"new_observation_count"`
	DuplicateObservationCount int             `json:"duplicate_observation_count"`
	CandidateCount            int             `json:"candidate_count"`
	ReinforcementCount        int             `json:"reinforcement_count"`
	OverflowCount             int             `json:"overflow_count"`
	CandidateIDs              []string        `json:"candidate_ids,omitempty"`
	Aggregates                []FoldAggregate `json:"aggregates,omitempty"`
}

type FoldAggregate struct {
	SubjectLaneNodeID   string  `json:"subject_lane_node_id"`
	Domain              string  `json:"domain"`
	WorkCount           int     `json:"work_count"`
	ReceiptCount        int     `json:"receipt_count"`
	OpportunityCount    int     `json:"opportunity_count"`
	HitCount            int     `json:"hit_count"`
	PosteriorMean       float64 `json:"posterior_mean"`
	PosteriorBand       string  `json:"posterior_band"`
	AbovePromotionFloor bool    `json:"above_promotion_floor"`
}

type ListReport struct {
	SchemaVersion  string          `json:"schema_version"`
	CandidateCount int             `json:"candidate_count"`
	Candidates     []mem.Candidate `json:"candidates"`
}

type foldCursor struct {
	MaxEventSeq int64  `json:"max_event_seq"`
	UpdatedAt   string `json:"updated_at"`
}

type workEvent struct {
	TS       string   `json:"ts"`
	WorkID   string   `json:"work_id"`
	To       string   `json:"to"`
	Attached []string `json:"attached"`
}

type sequencedEvent struct {
	Seq   int64
	Event workEvent
}

type observationRecord struct {
	DedupKey          string `json:"dedup_key"`
	GroupKey          string `json:"group_key"`
	SubjectLaneNodeID string `json:"subject_lane_node_id"`
	Domain            string `json:"domain"`
	WorkID            string `json:"work_id"`
	EvidenceEventID   string `json:"evidence_event_id"`
	MoveType          string `json:"move_type"`
	Outcome           string `json:"outcome"`
	ReceiptCount      int    `json:"receipt_count"`
	TransitionCount   int    `json:"transition_count"`
	FileChangeCount   int    `json:"file_change_count"`
	CreatedAt         string `json:"created_at"`
}

type receiptGroup struct {
	SubjectLaneNodeID string
	Domain            string
	WorkID            string
	EvidenceEventID   string
	MoveType          string
	Outcome           string
	ReceiptCount      int
	TransitionCount   int
	FileChangeCount   int
}

func Fold(homePath string, opts FoldOptions) (FoldReport, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := FoldReport{
		SchemaVersion: foldReportSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		DryRun:        opts.DryRun,
	}
	cursor, err := loadCursor(homePath)
	if err != nil {
		return report, err
	}
	report.CursorBefore = cursor.MaxEventSeq
	events, maxSeq, err := loadWorkEventsAfter(homePath, cursor.MaxEventSeq)
	if err != nil {
		return report, err
	}
	report.CursorAfter = maxSeq

	existing, err := loadObservations(homePath)
	if err != nil {
		return report, err
	}
	existingGroups := map[string]bool{}
	for _, obs := range existing {
		existingGroups[obs.GroupKey] = true
	}

	groups := map[string]*receiptGroup{}
	for _, ev := range events {
		if !laneBeliefTerminalState(ev.Event.To) {
			continue
		}
		transitionCount := countAttachedPrefix(ev.Event.Attached, "tr_")
		for _, id := range ev.Event.Attached {
			if !strings.HasPrefix(id, "rec_") {
				continue
			}
			rec, err := loadReceipt(homePath, id)
			if err != nil {
				return report, err
			}
			report.RawReceiptCount++
			resolution := ResolveReceiptLane(rec)
			if resolution.Outcome != LaneResolutionResolved {
				report.UnresolvedReceiptCount++
				continue
			}
			if strings.TrimSpace(rec.MoveType) == "audit_only" {
				report.IgnoredReceiptCount++
				continue
			}
			workID := firstNonEmpty(rec.IDs.WorkID, ev.Event.WorkID)
			if workID == "" {
				report.IgnoredReceiptCount++
				continue
			}
			domain := tendencyDomain(rec)
			groupKey := hashParts(resolution.NodeID, domain, workID)
			group := groups[groupKey]
			if group == nil {
				group = &receiptGroup{
					SubjectLaneNodeID: resolution.NodeID,
					Domain:            domain,
					WorkID:            workID,
					EvidenceEventID:   rec.ID,
					MoveType:          strings.TrimSpace(rec.MoveType),
					Outcome:           strings.TrimSpace(rec.Outcome),
				}
				groups[groupKey] = group
			}
			group.ReceiptCount++
			group.TransitionCount += transitionCount
			group.FileChangeCount += len(rec.StateDelta.FilesChanged)
		}
	}

	newObservations := make([]observationRecord, 0, len(groups))
	for _, key := range sortedReceiptGroupKeys(groups) {
		group := groups[key]
		if existingGroups[key] {
			report.DuplicateObservationCount++
			continue
		}
		obs := observationRecord{
			DedupKey:          hashParts(group.SubjectLaneNodeID, group.Domain, group.WorkID, group.EvidenceEventID),
			GroupKey:          key,
			SubjectLaneNodeID: group.SubjectLaneNodeID,
			Domain:            group.Domain,
			WorkID:            group.WorkID,
			EvidenceEventID:   group.EvidenceEventID,
			MoveType:          group.MoveType,
			Outcome:           group.Outcome,
			ReceiptCount:      group.ReceiptCount,
			TransitionCount:   group.TransitionCount,
			FileChangeCount:   group.FileChangeCount,
			CreatedAt:         now.UTC().Format(time.RFC3339),
		}
		newObservations = append(newObservations, obs)
	}
	report.NewObservationCount = len(newObservations)
	allObservations := append(append([]observationRecord{}, existing...), newObservations...)
	report.Aggregates = aggregateObservations(allObservations)

	if len(newObservations) > 0 {
		candidates, err := candidatesForNewObservations(newObservations, allObservations, now)
		if err != nil {
			return report, err
		}
		if len(candidates) > PER_CYCLE_CANDIDATE_CAP {
			report.OverflowCount = len(candidates) - PER_CYCLE_CANDIDATE_CAP
			candidates = candidates[:PER_CYCLE_CANDIDATE_CAP]
		}
		if opts.DryRun {
			report.CandidateCount = len(candidates)
			for _, candidate := range candidates {
				report.CandidateIDs = append(report.CandidateIDs, candidate.ID)
			}
		} else {
			store, err := mem.Open(homePath)
			if err != nil {
				return report, err
			}
			for _, candidate := range candidates {
				created, err := store.RecordStableCandidate(candidate)
				if err != nil {
					return report, err
				}
				if created {
					report.CandidateCount++
					report.CandidateIDs = append(report.CandidateIDs, candidate.ID)
				}
			}
		}
	}

	if !opts.DryRun {
		if err := appendObservations(homePath, newObservations); err != nil {
			return report, err
		}
		if maxSeq > cursor.MaxEventSeq {
			if err := saveCursor(homePath, foldCursor{MaxEventSeq: maxSeq, UpdatedAt: now.UTC().Format(time.RFC3339)}); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

func laneBeliefTerminalState(state string) bool {
	state = strings.TrimSpace(state)
	return state == "completed" || state == "failed" || state == "cancelled"
}

func List(homePath string) (ListReport, error) {
	store, err := mem.Open(homePath)
	if err != nil {
		return ListReport{}, err
	}
	all, err := store.AllCandidates()
	if err != nil {
		return ListReport{}, err
	}
	items := make([]mem.Candidate, 0, len(all))
	for _, candidate := range all {
		if hasTag(candidate.Tags, "lane-belief") {
			items = append(items, candidate)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return ListReport{SchemaVersion: listReportSchemaVersion, CandidateCount: len(items), Candidates: items}, nil
}

func candidatesForNewObservations(newObservations, allObservations []observationRecord, now time.Time) ([]mem.Candidate, error) {
	aggregates := aggregateObservationMap(allObservations)
	seenAggregate := map[string]bool{}
	out := []mem.Candidate{}
	for _, obs := range newObservations {
		aggKey := aggregateKey(obs.SubjectLaneNodeID, obs.Domain)
		if seenAggregate[aggKey] {
			continue
		}
		seenAggregate[aggKey] = true
		agg := aggregates[aggKey]
		if agg.ReceiptCount < MIN_LANE_RECEIPTS || agg.PosteriorMean < 0.50 {
			continue
		}
		candidate, err := NewTendencyBeliefCandidate(TendencyBeliefInput{
			SubjectLaneNodeID: obs.SubjectLaneNodeID,
			Domain:            obs.Domain,
			Object:            TendencyObjectObserved,
			Evidence: BeliefEvidence{
				WorkID:          obs.WorkID,
				EvidenceEventID: obs.EvidenceEventID,
				MoveType:        obs.MoveType,
				Outcome:         obs.Outcome,
				ReceiptCount:    agg.ReceiptCount,
				TransitionCount: obs.TransitionCount,
				FileChangeCount: obs.FileChangeCount,
			},
			Confidence:      agg.PosteriorMean,
			ObservationTime: now,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

func aggregateObservations(observations []observationRecord) []FoldAggregate {
	m := aggregateObservationMap(observations)
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]FoldAggregate, 0, len(keys))
	for _, key := range keys {
		out = append(out, m[key])
	}
	return out
}

func aggregateObservationMap(observations []observationRecord) map[string]FoldAggregate {
	byAggregate := map[string]*FoldAggregate{}
	works := map[string]map[string]bool{}
	for _, obs := range observations {
		key := aggregateKey(obs.SubjectLaneNodeID, obs.Domain)
		agg := byAggregate[key]
		if agg == nil {
			agg = &FoldAggregate{SubjectLaneNodeID: obs.SubjectLaneNodeID, Domain: obs.Domain}
			byAggregate[key] = agg
			works[key] = map[string]bool{}
		}
		agg.ReceiptCount += obs.ReceiptCount
		agg.HitCount++
		agg.OpportunityCount++
		works[key][obs.WorkID] = true
	}
	out := map[string]FoldAggregate{}
	for key, agg := range byAggregate {
		agg.WorkCount = len(works[key])
		agg.PosteriorMean = float64(BETA_PRIOR_ALPHA+agg.HitCount) / float64(BETA_PRIOR_ALPHA+BETA_PRIOR_BETA+agg.OpportunityCount)
		agg.AbovePromotionFloor = agg.PosteriorMean >= PROMOTE_POSTERIOR_FLOOR && agg.WorkCount >= MIN_CORROBORATION_WORKS
		switch {
		case agg.AbovePromotionFloor:
			agg.PosteriorBand = "propose"
		case agg.PosteriorMean >= 0.50:
			agg.PosteriorBand = "park"
		default:
			agg.PosteriorBand = "ignore"
		}
		out[key] = *agg
	}
	return out
}

func loadWorkEventsAfter(homePath string, cursor int64) ([]sequencedEvent, int64, error) {
	path := filepath.Join(homePath, "events", "work.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, cursor, nil
	}
	if err != nil {
		return nil, cursor, err
	}
	defer f.Close()
	var events []sequencedEvent
	seq := int64(0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		seq++
		if line == "" || seq <= cursor {
			continue
		}
		var ev workEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, seq, fmt.Errorf("parse work event seq %d: %w", seq, err)
		}
		events = append(events, sequencedEvent{Seq: seq, Event: ev})
	}
	if err := scanner.Err(); err != nil {
		return nil, seq, err
	}
	return events, seq, nil
}

func loadReceipt(homePath, id string) (envelope.Receipt, error) {
	var rec envelope.Receipt
	b, err := os.ReadFile(filepath.Join(homePath, "events", id+".json"))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, err
	}
	return rec, nil
}

func loadCursor(homePath string) (foldCursor, error) {
	var cursor foldCursor
	b, err := os.ReadFile(cursorPath(homePath))
	if os.IsNotExist(err) {
		return cursor, nil
	}
	if err != nil {
		return cursor, err
	}
	return cursor, json.Unmarshal(b, &cursor)
}

func saveCursor(homePath string, cursor foldCursor) error {
	if err := os.MkdirAll(laneBeliefsDir(homePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cursor, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cursorPath(homePath), append(b, '\n'), 0o644)
}

func loadObservations(homePath string) ([]observationRecord, error) {
	path := observationsPath(homePath)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []observationRecord
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obs observationRecord
		if err := json.Unmarshal([]byte(line), &obs); err != nil {
			return nil, fmt.Errorf("parse lane belief observation %d: %w", lineNo, err)
		}
		out = append(out, obs)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func appendObservations(homePath string, observations []observationRecord) error {
	if len(observations) == 0 {
		return nil
	}
	if err := os.MkdirAll(laneBeliefsDir(homePath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(observationsPath(homePath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, obs := range observations {
		b, err := json.Marshal(obs)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func tendencyDomain(rec envelope.Receipt) string {
	moveType := strings.TrimSpace(rec.MoveType)
	if moveType == "" {
		moveType = "unknown"
	}
	return slug("move-type-" + moveType)
}

func sortedReceiptGroupKeys(groups map[string]*receiptGroup) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func aggregateKey(subject, domain string) string { return subject + "\x00" + domain }

func countAttachedPrefix(values []string, prefix string) int {
	count := 0
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			count++
		}
	}
	return count
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(strings.TrimSpace(part)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func laneBeliefsDir(homePath string) string { return filepath.Join(homePath, "lanebeliefs") }
func cursorPath(homePath string) string {
	return filepath.Join(laneBeliefsDir(homePath), "cursor.json")
}
func observationsPath(homePath string) string {
	return filepath.Join(laneBeliefsDir(homePath), "observations.jsonl")
}
