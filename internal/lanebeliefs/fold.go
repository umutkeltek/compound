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

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/mem"
)

// The lane-belief fold is the ONE belief producer over lane receipts (B2). It keeps the legacy
// organ — event cursoring, receipt grouping, observation aggregation, CLI — and rewires its two
// former duplicate layers onto their owners: lane IDENTITY resolves ONLY through the B0 store
// door (internal/capgraph, via resolveReceiptLane), and belief SHAPES are built ONLY by the B1
// constructors and pass the B1 valve (internal/mem, via draftForObservation). It is PROPOSE-ONLY
// (binding taxonomy: B3 cross-lane promotion + B4 cooling are out of scope) and read-only in
// influence: it writes its own cursor + observations + memory candidates, and NOTHING else.
//
// Authority: docs_private/plan/packets/2026-07-09-agent-lane-beliefs.md BINDING TAXONOMY.

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

// foldProvenanceTag is the fold's OWN provenance tag (rule 4: provenance_lane is the fold's tag,
// not the subject, not the evidence producer). It is a lowercase slug (the B1 valve refuses a
// free-text provenance) and it is the single tag-free discriminator List uses to find fold output.
const foldProvenanceTag = "lanebeliefs-fold"

// belief kinds + the closed extractor vocabulary the fold maps closed receipt fields onto.
const (
	beliefKindTendency   = "tendency"
	beliefKindCapability = "capability"

	tendencyReceiptFaithful = "receipt-faithful" // completed WITH proof — standing-raising
	tendencyCeremonySkipper = "ceremony-skipper" // completed WITHOUT proof — standing-lowering
	tendencyObjectObserved  = "observed"
)

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
	BelowFloorLaneCount       int             `json:"below_floor_lane_count"`
	ValveDroppedCount         int             `json:"valve_dropped_count"`
	OverflowCount             int             `json:"overflow_count"`
	CandidateIDs              []string        `json:"candidate_ids,omitempty"`
	ValveDrops                []ValveDrop     `json:"valve_drops,omitempty"`
	Aggregates                []FoldAggregate `json:"aggregates,omitempty"`
}

// ValveDrop records a belief draft the B1 valve refused: a first-class, counted outcome, never a
// silent swallow (a draft that fails the valve is DROPPED, never proposed — falsifier valve).
type ValveDrop struct {
	SubjectLaneNodeID string `json:"subject_lane_node_id"`
	Domain            string `json:"domain"`
	Kind              string `json:"kind"`
	Reason            string `json:"reason"`
}

type FoldAggregate struct {
	SubjectLaneNodeID   string  `json:"subject_lane_node_id"`
	Domain              string  `json:"domain"`
	Kind                string  `json:"kind"`
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

// observationRecord is one (lane, domain, kind, work) trial: the atom that satisfies rule 6
// (ONE observation per lane/domain/work — receipt spam within a work does not inflate n). It
// carries the closed structured counts B3's Beta will need (Hits/Opportunities per work-trial;
// ReceiptCount raw volume for the noise floor) — the fold RECORDS these rates, it does not
// compute promotion posteriors.
type observationRecord struct {
	DedupKey          string `json:"dedup_key"`
	GroupKey          string `json:"group_key"`
	SubjectLaneNodeID string `json:"subject_lane_node_id"`
	Domain            string `json:"domain"`
	Kind              string `json:"kind"`
	Object            string `json:"object"`
	WorkID            string `json:"work_id"`
	EvidenceEventID   string `json:"evidence_event_id"`
	MoveType          string `json:"move_type"`
	Outcome           string `json:"outcome"`
	ReceiptCount      int    `json:"receipt_count"`
	Hits              int    `json:"hits"`
	Opportunities     int    `json:"opportunities"`
	TransitionCount   int    `json:"transition_count"`
	FileChangeCount   int    `json:"file_change_count"`
	CreatedAt         string `json:"created_at"`
}

type receiptGroup struct {
	SubjectLaneNodeID string
	Domain            string
	Kind              string
	Object            string
	WorkID            string
	EvidenceEventID   string
	MoveType          string
	Outcome           string
	ReceiptCount      int
	Hits              int
	Opportunities     int
	TransitionCount   int
	FileChangeCount   int
}

// beliefSignal is a single (kind, domain, object) belief the extractor reads off ONE receipt's
// closed structured fields. A receipt may yield several (a capability + a tendency); each becomes
// its own observation trial.
type beliefSignal struct {
	Kind   string
	Domain string
	Object string
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
	resolver, err := openLaneResolver(homePath)
	if err != nil {
		return report, err
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
			resolution := resolveReceiptLane(resolver, rec)
			if !resolution.Resolved() {
				// Rule 1 + falsifier l: an unattributable receipt is a first-class zero-evidence
				// outcome, counted here and dropped — never guessed onto a synthetic node.
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
			signals := beliefSignalsFromReceipt(rec)
			if len(signals) == 0 {
				report.IgnoredReceiptCount++
				continue
			}
			for _, sig := range signals {
				groupKey := hashParts(resolution.NodeID, sig.Kind, sig.Domain, workID)
				group := groups[groupKey]
				if group == nil {
					group = &receiptGroup{
						SubjectLaneNodeID: resolution.NodeID,
						Domain:            sig.Domain,
						Kind:              sig.Kind,
						Object:            sig.Object,
						WorkID:            workID,
						EvidenceEventID:   rec.ID,
						MoveType:          strings.TrimSpace(rec.MoveType),
						Outcome:           strings.TrimSpace(rec.Outcome),
						// rule 6: one work is one Beta trial (a hit that was observed). Receipt spam
						// inside the work bumps ReceiptCount (volume) but NEVER n (Hits/Opportunities).
						Hits:          1,
						Opportunities: 1,
					}
					groups[groupKey] = group
				}
				group.ReceiptCount++
				group.TransitionCount += transitionCount
				group.FileChangeCount += len(rec.StateDelta.FilesChanged)
			}
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
			// rule 5: the per-observation dedup key hash(subject_lane, domain, work_id,
			// evidence_event_id); GroupKey (lane, kind, domain, work) is the rule-6 identity the
			// dedup check runs on, so a re-fold over unchanged events adds no observation.
			DedupKey:          hashParts(group.SubjectLaneNodeID, group.Domain, group.WorkID, group.EvidenceEventID),
			GroupKey:          key,
			SubjectLaneNodeID: group.SubjectLaneNodeID,
			Domain:            group.Domain,
			Kind:              group.Kind,
			Object:            group.Object,
			WorkID:            group.WorkID,
			EvidenceEventID:   group.EvidenceEventID,
			MoveType:          group.MoveType,
			Outcome:           group.Outcome,
			ReceiptCount:      group.ReceiptCount,
			Hits:              group.Hits,
			Opportunities:     group.Opportunities,
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
		candidates, drops, belowFloor, err := candidatesForNewObservations(resolver, newObservations, allObservations, now)
		if err != nil {
			return report, err
		}
		report.ValveDrops = drops
		report.ValveDroppedCount = len(drops)
		report.BelowFloorLaneCount = belowFloor
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

// beliefSignalsFromReceipt derives lane-belief signals from a receipt's CLOSED structured fields
// ONLY (move_type, outcome, proof_refs PRESENCE) — never free text (rule 7: no StateDelta.Summary,
// FailureCause, snippets). A completed terminal receipt yields (a) a capability signal for the
// task class it completed and (b) exactly one tendency signal keyed on proof discipline:
// proof-bearing → the standing-raising `receipt-faithful` tendency, proof-absent → the
// standing-lowering `ceremony-skipper` tendency (self-incriminating and admissible per the
// taxonomy's valence rule). A non-completed terminal receipt (failed/cancelled/no_op) yields no
// positive belief signal — the fold does not infer a capability or a raising tendency from a
// non-completion.
func beliefSignalsFromReceipt(rec envelope.Receipt) []beliefSignal {
	if strings.TrimSpace(rec.Outcome) != "completed" {
		return nil
	}
	tendency := tendencyCeremonySkipper
	if len(rec.ProofRefs) > 0 {
		tendency = tendencyReceiptFaithful
	}
	return []beliefSignal{
		{Kind: beliefKindCapability, Domain: taskClassDomain(rec), Object: mem.CapabilityObject},
		{Kind: beliefKindTendency, Domain: tendency, Object: tendencyObjectObserved},
	}
}

// taskClassDomain is the capability domain: the move_type this lane completed, as a lowercase
// task-class slug. move_type is a closed enum field, never free text.
func taskClassDomain(rec envelope.Receipt) string {
	return slug(strings.TrimSpace(rec.MoveType))
}

func laneBeliefTerminalState(state string) bool {
	state = strings.TrimSpace(state)
	return state == "completed" || state == "failed" || state == "cancelled"
}

// List returns the fold's proposed lane-belief candidates. Discrimination is TAG-FREE: the B1
// valve refuses tags, so List cannot filter by tag; it filters by the fold's own provenance tag
// (rule 4) intersected with the belief predicates — the fold is the only writer of that tag.
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
		if isLaneBeliefCandidate(candidate) {
			items = append(items, candidate)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return ListReport{SchemaVersion: listReportSchemaVersion, CandidateCount: len(items), Candidates: items}, nil
}

func isLaneBeliefCandidate(c mem.Candidate) bool {
	if c.ProvenanceLane != foldProvenanceTag {
		return false
	}
	for _, p := range mem.BeliefPredicates {
		if c.Predicate == p {
			return true
		}
	}
	return false
}

// candidatesForNewObservations turns new observation trials into proposed belief candidates,
// building each draft ONLY via the B1 constructors + valve (draftForObservation). A lane/domain
// below the noise floor yields no candidate (counted in belowFloor); a draft the valve refuses is
// DROPPED and counted with its reason (never proposed, never silently swallowed).
func candidatesForNewObservations(resolver mem.LaneResolver, newObservations, allObservations []observationRecord, now time.Time) (cands []mem.Candidate, drops []ValveDrop, belowFloor int, err error) {
	aggregates := aggregateObservationMap(allObservations)
	seen := map[string]bool{}
	for _, obs := range newObservations {
		aggKey := aggregateKey(obs.SubjectLaneNodeID, obs.Kind, obs.Domain)
		if seen[aggKey] {
			continue
		}
		seen[aggKey] = true
		agg := aggregates[aggKey]
		if agg.ReceiptCount < MIN_LANE_RECEIPTS {
			// Noise floor (falsifier floor): a lane below MIN_LANE_RECEIPTS yields no candidate.
			belowFloor++
			continue
		}
		draft, buildErr := draftForObservation(resolver, obs, agg, now)
		if buildErr != nil {
			drops = append(drops, ValveDrop{
				SubjectLaneNodeID: obs.SubjectLaneNodeID,
				Domain:            obs.Domain,
				Kind:              obs.Kind,
				Reason:            buildErr.Error(),
			})
			continue
		}
		cands = append(cands, draft)
	}
	return cands, drops, belowFloor, nil
}

// draftForObservation builds one belief draft through the B1 door (mem.TendencyBeliefDraft /
// mem.CapabilityBeliefDraft — which mint the entity ONLY via the resolver and self-validate), then
// stamps the fold's deterministic identity + evidence refs + observation dates and runs the
// exported valve one final time before the draft is proposed. Propose-only: promotion_basis stays
// EMPTY (promotion is B3). A valve refusal is returned as an error and counted upstream.
func draftForObservation(resolver mem.LaneResolver, obs observationRecord, agg FoldAggregate, now time.Time) (mem.Candidate, error) {
	var (
		draft mem.Candidate
		err   error
	)
	switch obs.Kind {
	case beliefKindTendency:
		draft, err = mem.TendencyBeliefDraft(resolver, obs.SubjectLaneNodeID, obs.Domain, obs.Object, foldProvenanceTag)
	case beliefKindCapability:
		draft, err = mem.CapabilityBeliefDraft(resolver, obs.SubjectLaneNodeID, obs.Domain, foldProvenanceTag)
	default:
		return mem.Candidate{}, fmt.Errorf("belief draft: unknown kind %q", obs.Kind)
	}
	if err != nil {
		return mem.Candidate{}, err
	}
	draft.ID = beliefCandidateID(draft)
	draft.Confidence = agg.PosteriorMean
	draft.ObservationDate = now.UTC().Format(time.RFC3339)
	draft.CreatedAt = now.UTC().Format(time.RFC3339)
	if ref := strings.TrimSpace(obs.EvidenceEventID); ref != "" {
		draft.EvidenceRefs = []string{ref}
	}
	// Final gate before proposing (defense in depth after the fold's stamps): the valve is law.
	if err := mem.ValidateBeliefDraft(draft); err != nil {
		return mem.Candidate{}, err
	}
	return draft, nil
}

// beliefCandidateID is the deterministic candidate id for a (lane, predicate, domain) belief slot,
// stable across folds so RecordStableCandidate is idempotent. Object is excluded so a future
// stance change supersedes in-slot rather than minting a new candidate.
func beliefCandidateID(c mem.Candidate) string {
	h := hashParts(c.Entity, c.Predicate, c.Qualifiers[beliefDomainKey])
	return "cand_lanebelief_" + h[:20]
}

const beliefDomainKey = "domain"

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
		key := aggregateKey(obs.SubjectLaneNodeID, obs.Kind, obs.Domain)
		agg := byAggregate[key]
		if agg == nil {
			agg = &FoldAggregate{SubjectLaneNodeID: obs.SubjectLaneNodeID, Kind: obs.Kind, Domain: obs.Domain}
			byAggregate[key] = agg
			works[key] = map[string]bool{}
		}
		agg.ReceiptCount += obs.ReceiptCount
		agg.HitCount += obs.Hits
		agg.OpportunityCount += obs.Opportunities
		works[key][obs.WorkID] = true
	}
	out := map[string]FoldAggregate{}
	for key, agg := range byAggregate {
		agg.WorkCount = len(works[key])
		agg.PosteriorMean = float64(BETA_PRIOR_ALPHA+agg.HitCount) / float64(BETA_PRIOR_ALPHA+BETA_PRIOR_BETA+agg.OpportunityCount)
		// Bands are DISPLAY only — the fold does NOT gate promotion on the posterior (that is B3).
		// Candidate emission gates on the noise floor + the valve, never on this band.
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

// openLaneResolver returns the B0 identity door for this home: a read-only capability-graph store
// when one exists, else a resolver that resolves nothing (a fresh home with no imported lanes
// attributes every receipt to nothing — zero evidence, never a guess). It never creates capgraph
// state (rule 8: read-only influence).
func openLaneResolver(homePath string) (mem.LaneResolver, error) {
	if !capgraph.Exists(homePath) {
		return nullLaneResolver{}, nil
	}
	return capgraph.OpenReadOnly(homePath)
}

type nullLaneResolver struct{}

func (nullLaneResolver) ResolveLaneRef(string) (string, bool) { return "", false }

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

func sortedReceiptGroupKeys(groups map[string]*receiptGroup) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func aggregateKey(subject, kind, domain string) string {
	return subject + "\x00" + kind + "\x00" + domain
}

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
