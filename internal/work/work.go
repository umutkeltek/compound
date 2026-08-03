// Package work is the lifecycle store that threads ONE work_id through the whole
// chain: intent (WorkEnvelope) -> context (pack id) -> action (run ids) -> close,
// where close emits the Receipt and the apex Transition that prove the chain
// connected. State lives in COMPOUND_HOME (the runtime body), never the repo.
// See docs/architecture/transition.md and docs/architecture/contracts/README.md.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/runattempt"
)

// transitions is the work-state machine. Terminal states (completed/failed/cancelled)
// have no outgoing edges. `claimed` is reached only via Claim (claim-before-you-touch).
var transitions = map[string][]string{
	"proposed":  {"admitted", "ready", "held", "cancelled"},
	"admitted":  {"ready", "held", "cancelled"},
	"ready":     {"claimed", "held", "cancelled"},
	"claimed":   {"running", "ready", "held", "cancelled"},
	"running":   {"verifying", "blocked", "held", "failed", "completed"},
	"verifying": {"completed", "running", "failed"},
	"blocked":   {"running", "cancelled", "failed"},
	"held":      {"ready", "cancelled"},
}

// terminalStates may not advance further; close lands a work in one of them.
var terminalStates = map[string]bool{"completed": true, "failed": true, "cancelled": true}

const crossStateClaimedAdvanceRefusal = "claimed is reached only via 'compound work claim' (claim-before-you-touch); bare advance cannot bind authority owner+lease"

func terminalStateRequiresClose(action, state string) error {
	return fmt.Errorf("%s cannot enter terminal state %q; use Close so Receipt and Transition proof are emitted", action, state)
}

func crossStateClaimedAdvanceError(from, to string) error {
	if to == "claimed" && from != "claimed" {
		return errors.New(crossStateClaimedAdvanceRefusal)
	}
	return nil
}

func canTransition(from, to string) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Store is the work lifecycle store rooted at a COMPOUND_HOME.
type Store struct {
	home                  string
	events                *eventstore.Store
	readOnly              bool
	eventLogReads         atomic.Int64
	holdEvidenceFileReads atomic.Int64
}

func (s *Store) eventLogReadCount() int64 {
	return s.eventLogReads.Load()
}

// runAttemptStore is the RunAttempt persistence surface that the claim
// path depends on. *runattempt.Store satisfies it in production -- every
// method here is a verbatim signature of the concrete store -- so the
// runtime behavior is unchanged. The interface exists purely as a test
// seam: claim_runattempt_test substitutes a fault-injecting wrapper to
// exercise infra-failure return paths -- in particular a store whose
// SetOrigin fails while every other write (including the terminalizing
// Complete/CompleteWithReason) still lands, so the test can assert the
// freshly minted attempt is terminalized failed on disk rather than left
// stranded pending. The mirror of this seam in internal/harness
// (subprocess_runner.go) catches the same contract violation on the
// harness-execution lane.
type runAttemptStore interface {
	Mint(workID, actor, harness string, now time.Time) (runattempt.RunAttempt, error)
	SetOrigin(id, origin string) (runattempt.RunAttempt, error)
	SetPredecessor(id, predecessorID string) (runattempt.RunAttempt, error)
	Get(id string) (runattempt.RunAttempt, error)
	Complete(id, status string, now time.Time) (runattempt.RunAttempt, error)
	CompleteWithReason(id, status, reason string, now time.Time) (runattempt.RunAttempt, error)
}

// runAttemptsFactory is the test seam through which the claim path
// obtains its RunAttempt store. Production: runattempt.Open(home).
// Tests inject a wrapper that fails individual writes (e.g., SetOrigin)
// while letting terminalizing writes land.
var runAttemptsFactory = func(home string) runAttemptStore {
	return runattempt.Open(home)
}

// runAttempts returns the RunAttempt evidence store rooted at the same
// COMPOUND_HOME. C1 mints a RunAttempt on every claim so the execution
// organ fires in production, not just in panel.Apply tests. The store is
// cheap to construct (path only, no I/O); no caching needed. The
// run-observation sibling uses the concrete *runattempt.Store directly
// for its own typed test seams (see run_observation.go); the claim path
// reaches the same store through the runAttemptStore interface seam
// instead, so claim_runattempt_test can inject a fault-injecting
// wrapper for the SetOrigin-failure return path.
func (s *Store) runAttempts() *runattempt.Store {
	return runattempt.Open(s.home)
}

// runAttemptsClaim is the claim-path entry point for the RunAttempt
// store. It returns the runAttemptStore interface so the test seam can
// swap in a fault-injecting wrapper; production behavior is byte-
// identical to runAttempts() (the factory returns the same concrete
// store). Only the claim path uses this; the run-observation sibling
// continues to use the typed *runattempt.Store directly.
func (s *Store) runAttemptsClaim() runAttemptStore {
	return runAttemptsFactory(s.home)
}

func (s *Store) holdEvidenceFileReadCount() int64 {
	return s.holdEvidenceFileReads.Load()
}

// withLifecycleMutation is the outer transaction boundary for authoritative
// work writers. The lock order is always autonomy-admission -> work-event:
// validate the complete shared event log while holding admission, release the
// event lock, then let the mutation append under that same order. No caller may
// enter this helper while already holding the work-event lock.
func (s *Store) withLifecycleMutation(fn func() error) error {
	return home.WithAutonomyAdmissionLock(s.home, func() error {
		if _, err := s.eventsFor(""); err != nil {
			return fmt.Errorf("work lifecycle event log preflight: %w", err)
		}
		return fn()
	})
}

// Open roots a store at homePath and ensures the work/event habitat dirs exist.
func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	for _, d := range []string{"work/work", "work/leases", "work/land-checks"} {
		if err := os.MkdirAll(filepath.Join(homePath, d), 0o755); err != nil {
			return nil, err
		}
	}
	events, err := eventstore.Open(homePath)
	if err != nil {
		return nil, err
	}
	if err := events.Ensure(); err != nil {
		return nil, err
	}
	return &Store{home: homePath, events: events}, nil
}

// OpenReadOnly roots a store at homePath WITHOUT materializing any runtime
// state: it creates no directories and never calls events.Ensure. It exists so
// read-only projections (presence) can honor the registry's read-only contract
// — inspecting "who is here" must not itself leave footprints in COMPOUND_HOME.
// The read methods OpenReadOnly serves (ListLeases, readWorkLogTail,
// ListPresenceSessions) all treat missing dirs/files as "empty", so a fresh
// home yields an empty projection rather than an error. Any mutating method
// called on a Store opened this way fails at its own write boundary, exactly
// as it would against any unprepared home.
func OpenReadOnly(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	events, err := eventstore.Open(homePath)
	if err != nil {
		return nil, err
	}
	return &Store{home: homePath, events: events, readOnly: true}, nil
}

func (s *Store) workPath(id string) string  { return filepath.Join(s.home, "work/work", id+".json") }
func (s *Store) leasePath(id string) string { return filepath.Join(s.home, "work/leases", id+".json") }

// Event is one append-only lifecycle record.
type Event struct {
	TS                 string   `json:"ts"`
	WorkID             string   `json:"work_id"`
	From               string   `json:"from"`
	To                 string   `json:"to"`
	By                 string   `json:"by,omitempty"`
	Note               string   `json:"note,omitempty"`
	Attached           []string `json:"attached,omitempty"`
	PreviousWorkSHA256 string   `json:"previous_work_sha256,omitempty"`
	WorkSHA256         string   `json:"work_sha256,omitempty"`
	ReceiptSHA256      string   `json:"receipt_sha256,omitempty"`
}

// Lease records who holds a claimed work (claim-before-you-touch / D7).
type Lease struct {
	WorkID  string `json:"work_id"`
	By      string `json:"by"`
	ClaimAt string `json:"claimed_at"`
	// LaneID is the DECLARED lane's canonical agent-lane node id (resolved via
	// capgraph.ResolveLaneRef at claim/advance time), or empty when no lane was declared.
	// It is the single authoritative source for the lane stamped onto close-minted
	// receipts/transitions — never derived from actor/owner/harness (no-guessing, B0).
	LaneID     string `json:"lane_id,omitempty"`
	WorkSHA256 string `json:"work_sha256,omitempty"`
}

const claimPendingSchemaVersion = "compound.work_claim_pending.v0.1"

// claimPending is the durable transaction intent for a claim. It binds the
// pre/post WorkEnvelope versions, exact lease, exact lifecycle event, and the
// action/policy identities before any authoritative claim publication.
type claimPending struct {
	SchemaVersion      string                `json:"schema_version"`
	WorkID             string                `json:"work_id"`
	Actor              string                `json:"actor"`
	PreviousWorkSHA256 string                `json:"previous_work_sha256"`
	ClaimedWork        envelope.WorkEnvelope `json:"claimed_work"`
	ClaimedWorkSHA256  string                `json:"claimed_work_sha256"`
	Lease              Lease                 `json:"lease"`
	ActionIntentID     string                `json:"action_intent_id"`
	PolicyDecisionID   string                `json:"policy_decision_id"`
	// RunAttemptID is the ratt_* id minted at claim time (C1). Carried in the
	// pending so crash recovery can verify the attempt exists and matches the
	// envelope's RunAttemptIDs. Empty for pre-C1 pending files (backcompat).
	RunAttemptID    string `json:"run_attempt_id,omitempty"`
	RepairOwnerless bool   `json:"repair_ownerless,omitempty"`
	Event           Event  `json:"event"`
}

// --- create ---

type CreateInput struct {
	WorkID           string
	Owner            string
	Actor            string
	CanonicalSurface string
	TargetSurface    string
	SuccessPredicate string
	DesiredOutcome   string
	NonGoals         []string
	EvidenceRefs     []string
	ContextPackID    string
	RouteDecisionID  string
	GitDelivery      *envelope.GitDeliveryContract
	// ExpectedBaseSHA is retained only so legacy callers fail with an explicit
	// incomplete-contract error instead of silently losing their old field.
	ExpectedBaseSHA string
	State           string // default "ready"
	Risk            string // default "tier1"
	Privacy         string // default "private"
	// Uncertainty is the bounded derivation uncertainty preserved on a
	// proposed WorkEnvelope. A zero value keeps the established known default.
	Uncertainty envelope.Uncertainty
	// Prediction is an optional stake of how this work is expected to CLOSE (see
	// envelope.WorkPrediction). Staked at create time only -- there is no restaking at
	// Claim/Advance in v1.
	Prediction *envelope.WorkPrediction
	// RouteDeviation, when set, marks this work as an explicit deviation from the served policy
	// line and carries the risk grade admission binds on (see envelope.RouteDeviation). A
	// judge-grade (or ungraded) deviation is REFUSED at admission unless Prediction is also staked;
	// the refusal is fail-closed in envelope.WorkEnvelope.Validate, which every create/admission
	// path funnels through.
	RouteDeviation *envelope.RouteDeviation
	// PlanClass declares how much alignment this work owes before it may run (see
	// envelope.PlanClasses). Copied VERBATIM onto the envelope -- empty stays empty rather than
	// being defaulted to "oneshot", because the semantics "empty means oneshot" are enforced at
	// the guard, not baked into stored bytes (mirroring freeze's "absent means nothing gated").
	// An unrecognized value is refused by envelope.WorkEnvelope.Validate before persistence.
	PlanClass string
	// ReworkOfWorkID records that this work exists because an earlier landed work broke
	// something (see envelope.WorkEnvelope.ReworkOfWorkID). Copied VERBATIM onto the envelope.
	// A malformed or self-referential value is refused by envelope.WorkEnvelope.Validate, and a
	// value that does not resolve in this store is refused by validateReworkOfRef at admission
	// -- both BEFORE anything persists.
	ReworkOfWorkID string
}

// BuildWork constructs a WorkEnvelope WITHOUT persisting it — used both by Create (which
// then persists) and by callers that need a candidate (e.g. a UserPromptSubmit hook).
func BuildWork(in CreateInput, now time.Time) envelope.WorkEnvelope {
	workID := strings.TrimSpace(in.WorkID)
	if workID == "" {
		workID = idFor("work_", in.Owner, in.SuccessPredicate, ts(now))
	}
	var gitDelivery *envelope.GitDeliveryContract
	if in.GitDelivery != nil {
		copy := *in.GitDelivery
		gitDelivery = &copy
	}
	var prediction *envelope.WorkPrediction
	if in.Prediction != nil {
		copy := *in.Prediction
		prediction = &copy
	}
	var routeDeviation *envelope.RouteDeviation
	if in.RouteDeviation != nil {
		copy := *in.RouteDeviation
		routeDeviation = &copy
	}
	w := envelope.WorkEnvelope{
		Header:             newHeader(envelope.SVWork, workID, now, "", in.ContextPackID, orDefault(in.Privacy, "private"), orDefault(in.Risk, "tier1")),
		Owner:              in.Owner,
		CanonicalSurface:   strings.TrimSpace(in.CanonicalSurface),
		TargetSurface:      strings.TrimSpace(in.TargetSurface),
		State:              orDefault(in.State, "ready"),
		DesiredOutcome:     in.DesiredOutcome,
		SuccessPredicate:   in.SuccessPredicate,
		NonGoals:           orEmpty(in.NonGoals),
		ExpectedBaseSHA:    strings.TrimSpace(in.ExpectedBaseSHA),
		GitDelivery:        gitDelivery,
		RouteDecisionID:    strings.TrimSpace(in.RouteDecisionID),
		ActionIntentIDs:    []string{},
		ArtifactIDs:        []string{},
		VerificationIDs:    []string{},
		MemoryCandidateIDs: []string{},
		Prediction:         prediction,
		RouteDeviation:     routeDeviation,
		PlanClass:          in.PlanClass,
		ReworkOfWorkID:     strings.TrimSpace(in.ReworkOfWorkID),
	}
	if strings.TrimSpace(in.Uncertainty.Level) != "" || strings.TrimSpace(in.Uncertainty.Reason) != "" {
		w.Uncertainty = in.Uncertainty
	}
	w.EvidenceRefs = orEmpty(in.EvidenceRefs)
	return w
}

// Create writes a new WorkEnvelope and its first lifecycle event only after the
// admission ActionIntent is allowed by policy.
func (s *Store) Create(in CreateInput, now time.Time) (envelope.WorkEnvelope, error) {
	plan, err := s.CreatePlan(in, now, CreatePlanOptions{Actor: in.Actor})
	if err != nil {
		return plan.Work, err
	}
	attached := []string(nil)
	if in.ContextPackID != "" {
		attached = append(attached, in.ContextPackID)
	}
	return s.admitWork(plan, now, "created", attached)
}

func (s *Store) validateContextPackRef(contextPackID string) error {
	contextPackID = strings.TrimSpace(contextPackID)
	if contextPackID == "" {
		return nil
	}
	var pack envelope.ContextPack
	path := filepath.Join(s.home, "context", "packs", contextPackID+".json")
	if err := s.readJSON(path, &pack); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("context_pack_id %q does not resolve to %s", contextPackID, filepath.ToSlash(filepath.Join("context", "packs", contextPackID+".json")))
		}
		return fmt.Errorf("read context_pack_id %q: %w", contextPackID, err)
	}
	if pack.ID != contextPackID {
		return fmt.Errorf("context_pack_id %q does not match artifact id %q", contextPackID, pack.ID)
	}
	if err := pack.Validate(); err != nil {
		return fmt.Errorf("context_pack_id %q invalid: %w", contextPackID, err)
	}
	if err := s.validateContextPackContractRef(contextPackID, pack.IDs.ContractID); err != nil {
		return err
	}
	return nil
}

// validateReworkOfRef proves the rework lineage edge RESOLVES in this store -- no phantom links,
// mirroring runattempt.SetPredecessor. The envelope validator already fixed the SHAPE (and
// refused self-reference); this is the existence half, and it is fail-closed at admission: an
// edge to a work id that does not resolve refuses the create BEFORE anything persists, because a
// rework-rate fold that counts edges to works that never existed reports a number nobody can
// trace back.
//
// The check is intentionally NOT transitive: the target being itself a rework is fine, and no
// cycle walk is needed -- the edge is create-time-only against an already-existing target, so a
// cycle would require an id collision the store already refuses.
func (s *Store) validateReworkOfRef(w envelope.WorkEnvelope) error {
	reworkOf := strings.TrimSpace(w.ReworkOfWorkID)
	if reworkOf == "" {
		return nil
	}
	if _, err := s.Get(reworkOf); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rework_of_work_id %q does not resolve to an existing work", reworkOf)
		}
		return fmt.Errorf("read rework_of_work_id %q: %w", reworkOf, err)
	}
	return nil
}

func (s *Store) validateContextPackContractRef(contextPackID, contractID string) error {
	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		return nil
	}
	pattern := filepath.Join(s.home, "contracts", "effective", "*", contractID+".json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("context_pack_id %q contract_id %q lookup failed: %w", contextPackID, contractID, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("context_pack_id %q references contract_id %q but no %s artifact exists", contextPackID, contractID, filepath.ToSlash(filepath.Join("contracts", "effective", "*", contractID+".json")))
	}
	sort.Strings(matches)
	var ec envelope.EffectiveContract
	if err := s.readJSON(matches[len(matches)-1], &ec); err != nil {
		return fmt.Errorf("read context_pack_id %q contract_id %q: %w", contextPackID, contractID, err)
	}
	if ec.ID != contractID {
		return fmt.Errorf("context_pack_id %q contract_id %q does not match artifact id %q", contextPackID, contractID, ec.ID)
	}
	if err := ec.Validate(); err != nil {
		return fmt.Errorf("context_pack_id %q contract_id %q invalid: %w", contextPackID, contractID, err)
	}
	return nil
}

// --- show / list ---

func (s *Store) Get(workID string) (envelope.WorkEnvelope, error) {
	var w envelope.WorkEnvelope
	err := s.readJSON(s.workPath(workID), &w)
	if errors.Is(err, os.ErrNotExist) {
		if archivedPath, ok := s.archivedWorkPath(workID); ok {
			err = s.readJSON(archivedPath, &w)
		}
	}
	return w, err
}

// getWithArchiveIndex is Get for batch callers that already loaded the
// operational archive index once (e.g. the workbench report build) and must
// not trigger another LoadOperationalArchiveIndex per work item.
func (s *Store) getWithArchiveIndex(workID string, archiveIndex []home.OperationalArchiveEntry) (envelope.WorkEnvelope, error) {
	var w envelope.WorkEnvelope
	err := s.readJSON(s.workPath(workID), &w)
	if errors.Is(err, os.ErrNotExist) {
		if archivedPath, ok := s.archivedWorkPathWithIndex(workID, archiveIndex); ok {
			err = s.readJSON(archivedPath, &w)
		}
	}
	return w, err
}

// Show returns a work and its lifecycle log (oldest first).
func (s *Store) Show(workID string) (envelope.WorkEnvelope, []Event, error) {
	w, err := s.Get(workID)
	if err != nil {
		return w, nil, err
	}
	evs, err := s.eventsFor(workID)
	return w, evs, err
}

func (s *Store) List() ([]envelope.WorkEnvelope, error) {
	out, _, err := s.list(false)
	return out, err
}

func (s *Store) ListWithArchived() ([]envelope.WorkEnvelope, error) {
	out, _, err := s.list(true)
	return out, err
}

// list reads every work/work/<id>.json (plus archived entries when
// includeArchived is set) and returns the parsed envelopes alongside one
// integrity warning per item that failed to parse. A parse failure never
// aborts the read (callers like Workbench build a degrade-and-report
// projection over a directory of independently-written files, so one
// corrupt item must not blank the rest) but it must not vanish either: the
// old shape (`if s.readJSON(...) == nil { append }`) silently dropped
// corrupt items from every downstream count with no trace. The warning
// slice is the "we could not read N items" channel List/ListWithArchived
// callers don't need; Workbench threads it into report.Integrity, the
// existing degradation channel, rather than inventing a parallel one.
func (s *Store) list(includeArchived bool) ([]envelope.WorkEnvelope, []string, error) {
	dir := filepath.Join(s.home, "work/work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			entries = nil
		} else {
			return nil, nil, err
		}
	}
	var out []envelope.WorkEnvelope
	var warnings []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var w envelope.WorkEnvelope
		if err := s.readJSON(filepath.Join(dir, e.Name()), &w); err != nil {
			warnings = append(warnings, fmt.Sprintf("work/work/%s unreadable: %s", e.Name(), err.Error()))
			continue
		}
		out = append(out, w)
	}
	if includeArchived {
		archived, archivedWarnings, err := s.archivedWorks()
		if err != nil {
			return nil, nil, err
		}
		out = append(out, archived...)
		warnings = append(warnings, archivedWarnings...)
	}
	return out, warnings, nil
}

func (s *Store) archivedWorks() ([]envelope.WorkEnvelope, []string, error) {
	entries, err := home.LoadOperationalArchiveIndex(s.home)
	if err != nil {
		return nil, nil, err
	}
	var out []envelope.WorkEnvelope
	var warnings []string
	for _, entry := range entries {
		if entry.Kind != "work" || !strings.HasPrefix(entry.OriginalPath, "work/work/") || !strings.HasSuffix(entry.OriginalPath, ".json") {
			continue
		}
		var w envelope.WorkEnvelope
		if err := s.readJSON(filepath.Join(s.home, filepath.FromSlash(entry.ArchivePath)), &w); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s unreadable: %s", entry.OriginalPath, err.Error()))
			continue
		}
		out = append(out, w)
	}
	return out, warnings, nil
}

func (s *Store) archivedWorkPath(workID string) (string, bool) {
	entries, err := home.LoadOperationalArchiveIndex(s.home)
	if err != nil {
		return "", false
	}
	return s.archivedWorkPathWithIndex(workID, entries)
}

// archivedWorkPathWithIndex is archivedWorkPath against a caller-supplied,
// already-loaded archive index instead of re-reading it from disk.
func (s *Store) archivedWorkPathWithIndex(workID string, entries []home.OperationalArchiveEntry) (string, bool) {
	want := filepath.ToSlash(filepath.Join("work", "work", workID+".json"))
	for _, entry := range entries {
		if entry.Kind == "work" && entry.OriginalPath == want {
			return filepath.Join(s.home, filepath.FromSlash(entry.ArchivePath)), true
		}
	}
	return "", false
}

// --- claim ---

// Claim moves a ready work to claimed only after policy allows the claim
// ActionIntent, then writes the claim envelopes and lease. A second claimant is
// rejected (claim-before-you-touch prevents parallel agents colliding on the
// same surface).
func (s *Store) Claim(workID, by string, now time.Time) (envelope.WorkEnvelope, error) {
	return s.ClaimWithLane(workID, by, "", now)
}

// ClaimWithLane claims a work and, when laneRef is non-empty, declares its lane: the ref must
// resolve via capgraph.ResolveLaneRef (fail-closed at the gate, before any state mutation) and
// the resolved canonical node id is recorded on the lease. Claim is the laneRef=="" case; every
// existing Claim caller is byte-identical.
func (s *Store) ClaimWithLane(workID, by, laneRef string, now time.Time) (envelope.WorkEnvelope, error) {
	laneID, err := s.resolveDeclaredLane(laneRef)
	if err != nil {
		w, _ := s.Get(workID)
		return w, err
	}
	var out envelope.WorkEnvelope
	var claimErr error
	err = s.withLifecycleMutation(func() error {
		out, claimErr = s.claimLocked(workID, by, laneID, now)
		return claimErr
	})
	return out, err
}

// resolveDeclaredLane maps a declared --lane ref to its canonical agent-lane node id via the
// single no-guessing resolver (capgraph.ResolveLaneRef). Empty ref → ("", nil): no declaration.
// A non-empty ref that does not resolve is a fail-closed error naming the valid ref forms, so
// typos and bare-harness/actor guesses die at the write gate, never in the belief fold.
func (s *Store) resolveDeclaredLane(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	cg, err := capgraph.Open(s.home)
	if err != nil {
		return "", fmt.Errorf("resolve --lane %q: %w", ref, err)
	}
	nodeID, ok := cg.ResolveLaneRef(ref)
	if !ok {
		return "", fmt.Errorf("--lane %q does not resolve to an imported agent-lane node; pass an agent-lane node id or a short lane id (e.g. provider/model), not a bare harness or an actor string", ref)
	}
	return nodeID, nil
}

// recordDeclaredLaneOnLease writes the resolved canonical lane id onto workID's existing lease.
// The caller has already passed the holder-scoped advance gate, so this only reaches a lease the
// acting holder is entitled to mutate. An absent lease is an error: a lane can only be declared
// on a work that is currently claimed.
func (s *Store) recordDeclaredLaneOnLease(workID, laneID string) error {
	lease, err := s.readLease(workID)
	if err != nil {
		return fmt.Errorf("advance --lane requires a claimed work with a live lease: %w", err)
	}
	if strings.TrimSpace(lease.By) == "" {
		return fmt.Errorf("advance --lane requires a claimed work (no lease holder)")
	}
	lease.LaneID = laneID
	return s.publishClaimJSON(s.leasePath(workID), &lease)
}

func (s *Store) claimLocked(workID, by, laneID string, now time.Time) (envelope.WorkEnvelope, error) {
	w, err := s.Get(workID)
	if err != nil {
		return w, err
	}
	by = strings.TrimSpace(by)
	if by == "" {
		return w, fmt.Errorf("claim requires --by <actor>")
	}
	if recovered, ok, err := s.recoverPendingClaim(w); err != nil {
		return w, err
	} else if ok {
		holder := recovered.Authority.Owner
		if holder != by {
			return recovered, fmt.Errorf("work %s already claimed by %q", workID, holder)
		}
		return recovered, nil
	}
	from := w.State
	repairOwnerless := false
	if w.State == "claimed" {
		repairFrom, repair, err := s.ownerlessClaimRepairSource(w)
		if err != nil {
			return w, fmt.Errorf("work %s inspect ownerless-claim repair: %w", workID, err)
		}
		if repair {
			from = repairFrom
			repairOwnerless = true
		} else {
			holder, err := s.validateClaimAuthority(w, false)
			if err != nil {
				return w, fmt.Errorf("work %s claimed authority is inconsistent: %w", workID, err)
			}
			if holder != by {
				return w, fmt.Errorf("work %s already claimed by %q", workID, holder)
			}
			return w, nil
		}
	}
	if !repairOwnerless && !canTransition(w.State, "claimed") {
		return w, fmt.Errorf("cannot claim from state %q (must be ready)", w.State)
	}
	plan, err := s.ClaimPlan(workID, now, ClaimPlanOptions{Actor: by})
	if err != nil {
		return w, err
	}
	if !plan.Policy.Allowed {
		return w, fmt.Errorf("cannot claim work %s: policy %s (decision %s)", workID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	previousWorkSHA256, err := workEnvelopeSHA256(w)
	if err != nil {
		return w, err
	}
	w.State = "claimed"
	w.Authority.Owner = plan.Actor
	w.ActionIntentIDs = append(w.ActionIntentIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	if err := s.writeEventJSON(plan.ActionIntent.ID, &plan.ActionIntent); err != nil {
		return w, err
	}
	if err := s.writeEventJSON(plan.Decision.ID, &plan.Decision); err != nil {
		return w, err
	}
	// C1: mint a RunAttempt on every claim so the execution organ fires in
	// production. The attempt is minted BEFORE the claimed-work hash is
	// computed so the ratt_ id is part of the canonical envelope bytes.
	// Harness is the resolved lane id when declared, else the placeholder.
	raHarness := laneID
	if raHarness == "" {
		raHarness = runattempt.HarnessPlaceholder
	}
	ra, raErr := s.runAttemptsClaim().Mint(workID, plan.Actor, raHarness, now)
	if raErr != nil {
		return w, fmt.Errorf("claim %s: mint run attempt: %w", workID, raErr)
	}
	// F2: stamp provenance -- this is a claim-time placeholder reservation,
	// not a real execution. supersede-at-attach (in Advance) reads this to
	// terminalize the reservation once a harness_execution attempt attaches.
	//
	// Audit invariant (E14 twin): on a SetOrigin failure, the freshly
	// minted reservation attempt MUST be terminalized as `failed` with a
	// reason naming the origin-stamp failure -- never left `pending`. The
	// same invariant is enforced on the harness-execution lane by
	// subprocess_runner.go (slice/audit-e14-setorigin-20260724). A pending
	// ratt_ record stranded here would either be swept-as-stale by the
	// C3 sweeper (mislabeling a real infra failure as "no heartbeat"),
	// or worse, attach to a later envelope and propagate as a phantom
	// provenance. If the terminalization ITSELF fails, best-effort: we
	// return the original SetOrigin error (do not mask it).
	if stamped, oErr := s.runAttemptsClaim().SetOrigin(ra.ID, runattempt.OriginClaimReservation); oErr != nil {
		if _, tErr := s.runAttemptsClaim().CompleteWithReason(ra.ID, runattempt.StatusFailed, "stamp origin: "+oErr.Error(), now); tErr != nil {
			// best-effort: the terminalization failure is not reported --
			// the original SetOrigin error is the truth we want to surface.
			_ = tErr
		}
		return w, fmt.Errorf("claim %s: stamp run attempt origin: %w", workID, oErr)
	} else {
		ra = stamped
	}
	// C4: retry lineage — if this work already has attempts, the new one
	// chains to the most recent. Best-effort: a link failure does not abort
	// the claim (the attempt exists, lineage is evidence, not enforcement).
	if len(w.RunAttemptIDs) > 0 {
		prev := w.RunAttemptIDs[len(w.RunAttemptIDs)-1]
		if _, linkErr := s.runAttemptsClaim().SetPredecessor(ra.ID, prev); linkErr != nil {
			// Best-effort: log nothing, the claim proceeds.
			_ = linkErr
		}
	}
	w.RunAttemptIDs = append(w.RunAttemptIDs, ra.ID)
	claimedWorkSHA256, err := workEnvelopeSHA256(w)
	if err != nil {
		return w, err
	}
	lease := Lease{WorkID: workID, By: plan.Actor, ClaimAt: ts(now), LaneID: laneID, WorkSHA256: claimedWorkSHA256}
	note := "claimed (policy " + plan.Policy.Outcome + ")"
	if repairOwnerless {
		note = "ownerless-claim repair (policy " + plan.Policy.Outcome + ")"
	}
	event := Event{
		TS:                 lease.ClaimAt,
		WorkID:             workID,
		From:               from,
		To:                 "claimed",
		By:                 plan.Actor,
		Note:               note,
		Attached:           []string{plan.ActionIntent.ID, plan.Decision.ID, ra.ID},
		PreviousWorkSHA256: previousWorkSHA256,
		WorkSHA256:         claimedWorkSHA256,
	}
	pending := claimPending{
		SchemaVersion:      claimPendingSchemaVersion,
		WorkID:             workID,
		Actor:              plan.Actor,
		PreviousWorkSHA256: previousWorkSHA256,
		ClaimedWork:        w,
		ClaimedWorkSHA256:  claimedWorkSHA256,
		Lease:              lease,
		ActionIntentID:     plan.ActionIntent.ID,
		PolicyDecisionID:   plan.Decision.ID,
		RunAttemptID:       ra.ID,
		RepairOwnerless:    repairOwnerless,
		Event:              event,
	}
	if err := s.validatePendingClaim(pending); err != nil {
		return w, err
	}
	pendingPath := s.claimPendingPath(workID)
	stagedPending, err := s.stageJSON(pendingPath, &pending)
	if err != nil {
		return w, err
	}
	if err := commitStagedJSONExclusive(stagedPending, pendingPath); err != nil {
		_ = os.Remove(stagedPending)
		return w, err
	}
	return s.completePendingClaim(pending, true)
}

// ownerlessClaimRepairSource recognizes only the historical wedge minted by a bare
// advance: claimed state, no authority owner, no lease, and a current-hash-bound
// lifecycle tail that crossed a legal state-machine edge into claimed. The returned
// source state lets the repair claim append a historically replayable claim event.
func (s *Store) ownerlessClaimRepairSource(w envelope.WorkEnvelope) (string, bool, error) {
	if w.State != "claimed" || strings.TrimSpace(w.Authority.Owner) != "" {
		return "", false, nil
	}
	if _, err := s.readLease(w.ID); err == nil {
		return "", false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	workSHA256, err := workEnvelopeSHA256(w)
	if err != nil {
		return "", false, err
	}
	events, err := s.eventsFor(w.ID)
	if err != nil {
		return "", false, err
	}
	if len(events) == 0 {
		return "", false, nil
	}
	tail := events[len(events)-1]
	if tail.To != "claimed" || !canTransition(tail.From, "claimed") || tail.WorkSHA256 != workSHA256 {
		return "", false, nil
	}
	return tail.From, true, nil
}

// These synchronization seams model process death after each authoritative
// file publication. Production leaves them as no-ops.
var claimAfterWorkPublish = func() {}
var claimAfterLeasePublish = func() {}

func (s *Store) claimPendingPath(workID string) string {
	return s.workPath(workID) + ".claim-pending"
}

func (s *Store) recoverPendingClaim(current envelope.WorkEnvelope) (envelope.WorkEnvelope, bool, error) {
	var pending claimPending
	if err := s.readJSON(s.claimPendingPath(current.ID), &pending); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return current, false, nil
		}
		return current, false, fmt.Errorf("read pending claim for %s: %w", current.ID, err)
	}
	if err := s.validatePendingClaim(pending); err != nil {
		return current, false, fmt.Errorf("pending claim for %s invalid: %w", current.ID, err)
	}
	currentSHA256, err := workEnvelopeSHA256(current)
	if err != nil {
		return current, false, err
	}
	if currentSHA256 != pending.PreviousWorkSHA256 && currentSHA256 != pending.ClaimedWorkSHA256 {
		return current, false, fmt.Errorf("pending claim for %s matches neither current nor claimed WorkEnvelope", current.ID)
	}
	recovered, err := s.completePendingClaim(pending, false)
	if err != nil {
		return current, false, err
	}
	return recovered, true, nil
}

func (s *Store) recoverPendingClaimBeforeMutation(workID string) (envelope.WorkEnvelope, error) {
	current, err := s.Get(workID)
	if err != nil {
		return current, err
	}
	recovered, ok, err := s.recoverPendingClaim(current)
	if err != nil {
		return current, err
	}
	if ok {
		current = recovered
	}
	if current.State == "claimed" {
		if _, err := s.validateClaimAuthority(current, true); err != nil {
			return current, fmt.Errorf("work %s claimed authority is inconsistent: %w", workID, err)
		}
	}
	return current, nil
}

func (s *Store) completePendingClaim(pending claimPending, runCrashSeams bool) (envelope.WorkEnvelope, error) {
	if err := s.validatePendingClaim(pending); err != nil {
		return pending.ClaimedWork, fmt.Errorf("pending claim for %s invalid: %w", pending.WorkID, err)
	}
	err := s.withWorkEventLock(func() error {
		if err := s.ensureExactClaimEventUnlocked(pending); err != nil {
			return err
		}

		current, err := s.Get(pending.WorkID)
		if err != nil {
			return err
		}
		currentSHA256, err := workEnvelopeSHA256(current)
		if err != nil {
			return err
		}
		switch currentSHA256 {
		case pending.PreviousWorkSHA256:
			if pending.RepairOwnerless {
				if current.State != "claimed" || strings.TrimSpace(current.Authority.Owner) != "" {
					return fmt.Errorf("pending ownerless-claim repair for %s no longer matches an ownerless claimed WorkEnvelope", pending.WorkID)
				}
			} else if current.State != pending.Event.From {
				return fmt.Errorf("pending claim for %s previous state is %q, want %q", pending.WorkID, current.State, pending.Event.From)
			}
			if err := s.publishClaimJSON(s.workPath(pending.WorkID), &pending.ClaimedWork); err != nil {
				return err
			}
		case pending.ClaimedWorkSHA256:
			if !reflect.DeepEqual(current, pending.ClaimedWork) {
				return fmt.Errorf("pending claim for %s claimed WorkEnvelope bytes disagree", pending.WorkID)
			}
		default:
			return fmt.Errorf("pending claim for %s conflicts with current WorkEnvelope", pending.WorkID)
		}
		if runCrashSeams {
			claimAfterWorkPublish()
		}

		lease, leaseErr := s.readLease(pending.WorkID)
		if leaseErr != nil || !reflect.DeepEqual(lease, pending.Lease) {
			if err := s.publishClaimJSON(s.leasePath(pending.WorkID), &pending.Lease); err != nil {
				return err
			}
		}
		lease, err = s.readLease(pending.WorkID)
		if err != nil {
			return fmt.Errorf("read published claim lease for %s: %w", pending.WorkID, err)
		}
		if !reflect.DeepEqual(lease, pending.Lease) {
			return fmt.Errorf("published claim lease for %s does not match pending transaction", pending.WorkID)
		}
		if runCrashSeams {
			claimAfterLeasePublish()
		}
		if err := removeFileAndSyncDir(s.claimPendingPath(pending.WorkID)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return pending.ClaimedWork, err
	}
	return pending.ClaimedWork, nil
}

func (s *Store) ensureExactClaimEventUnlocked(pending claimPending) error {
	events, err := s.readEventsStrictUnlocked(pending.WorkID)
	if err != nil {
		return err
	}
	exact := 0
	for _, event := range events {
		if event.To != "claimed" || event.WorkSHA256 != pending.ClaimedWorkSHA256 {
			continue
		}
		if !reflect.DeepEqual(event, pending.Event) {
			return fmt.Errorf("pending claim for %s conflicts with durable claim event", pending.WorkID)
		}
		exact++
	}
	if exact > 1 {
		return fmt.Errorf("pending claim for %s has %d exact durable events, want one", pending.WorkID, exact)
	}
	if exact == 0 {
		before := len(events)
		if err := s.appendEventControlledUnlocked(pending.Event, nil, nil); err != nil {
			return err
		}
		events, err = s.readEventsStrictUnlocked(pending.WorkID)
		if err != nil {
			return err
		}
		if len(events) != before+1 || !reflect.DeepEqual(events[len(events)-1], pending.Event) {
			return fmt.Errorf("work event log did not preserve the exact claim event for %s", pending.WorkID)
		}
	}
	return nil
}

func (s *Store) publishClaimJSON(path string, value any) error {
	staged, err := s.stageJSON(path, value)
	if err != nil {
		return err
	}
	if err := commitStagedJSON(staged, path); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

func (s *Store) validatePendingClaim(pending claimPending) error {
	if pending.SchemaVersion != claimPendingSchemaVersion {
		return fmt.Errorf("schema_version must be %q", claimPendingSchemaVersion)
	}
	if strings.TrimSpace(pending.WorkID) == "" || pending.ClaimedWork.ID != pending.WorkID {
		return fmt.Errorf("pending Work identity mismatch")
	}
	if strings.TrimSpace(pending.Actor) == "" || pending.ClaimedWork.State != "claimed" || pending.ClaimedWork.Authority.Owner != pending.Actor {
		return fmt.Errorf("pending claimed Work owner/state mismatch")
	}
	if err := pending.ClaimedWork.Validate(); err != nil {
		return fmt.Errorf("claimed WorkEnvelope invalid: %w", err)
	}
	claimedSHA256, err := workEnvelopeSHA256(pending.ClaimedWork)
	if err != nil {
		return err
	}
	if strings.TrimSpace(pending.PreviousWorkSHA256) == "" || pending.ClaimedWorkSHA256 != claimedSHA256 {
		return fmt.Errorf("pending WorkEnvelope hashes do not bind the claimed Work")
	}
	if pending.Lease.WorkID != pending.WorkID || pending.Lease.By != pending.Actor || strings.TrimSpace(pending.Lease.ClaimAt) == "" ||
		pending.Lease.WorkSHA256 != pending.ClaimedWorkSHA256 {
		return fmt.Errorf("pending lease identity mismatch")
	}
	if strings.TrimSpace(pending.ActionIntentID) == "" || strings.TrimSpace(pending.PolicyDecisionID) == "" {
		return fmt.Errorf("pending claim must bind action and policy ids")
	}
	event := pending.Event
	// C1: the Attached list carries [action, policy] for pre-C1 pending
	// files and [action, policy, ratt_*] for C1+ files. Validate both
	// shapes so crash recovery works across the boundary.
	wantAttached := []string{pending.ActionIntentID, pending.PolicyDecisionID}
	if pending.RunAttemptID != "" {
		wantAttached = append(wantAttached, pending.RunAttemptID)
	}
	if event.WorkID != pending.WorkID || !canTransition(event.From, "claimed") || event.To != "claimed" || event.By != pending.Actor ||
		event.TS != pending.Lease.ClaimAt || event.PreviousWorkSHA256 != pending.PreviousWorkSHA256 || event.WorkSHA256 != pending.ClaimedWorkSHA256 ||
		!reflect.DeepEqual(event.Attached, wantAttached) {
		return fmt.Errorf("pending lifecycle event does not bind exact claim transaction")
	}
	if pending.RepairOwnerless != strings.HasPrefix(event.Note, "ownerless-claim repair (policy ") {
		return fmt.Errorf("pending ownerless-claim repair marker does not match lifecycle event")
	}
	if len(pending.ClaimedWork.ActionIntentIDs) == 0 ||
		pending.ClaimedWork.ActionIntentIDs[len(pending.ClaimedWork.ActionIntentIDs)-1] != pending.ActionIntentID ||
		pending.ClaimedWork.PolicyDecisionID != pending.PolicyDecisionID {
		return fmt.Errorf("pending claimed Work does not bind exact action and policy")
	}
	return s.validateClaimEventArtifacts(event, pending.ClaimedWork)
}

func (s *Store) validateClaimAuthority(claimed envelope.WorkEnvelope, allowLegacy bool) (string, error) {
	if err := claimed.Validate(); err != nil {
		return "", fmt.Errorf("claimed WorkEnvelope invalid: %w", err)
	}
	holder := strings.TrimSpace(claimed.Authority.Owner)
	if claimed.State != "claimed" || holder == "" {
		return "", fmt.Errorf("claimed WorkEnvelope has no exact authority owner")
	}
	lease, err := s.readLease(claimed.ID)
	if err != nil {
		return "", fmt.Errorf("read exact claim lease: %w", err)
	}
	events, err := s.eventsFor(claimed.ID)
	if err != nil {
		return "", err
	}
	matches := make([]Event, 0, 1)
	if strings.TrimSpace(lease.WorkSHA256) == "" {
		if !allowLegacy {
			return "", fmt.Errorf("lease does not bind a claimed WorkEnvelope hash")
		}
		for _, event := range events {
			if event.To == "claimed" && canTransition(event.From, "claimed") && event.TS == lease.ClaimAt &&
				event.WorkSHA256 == "" && event.PreviousWorkSHA256 == "" {
				matches = append(matches, event)
			}
		}
	} else {
		for _, event := range events {
			if event.To == "claimed" && event.WorkSHA256 == lease.WorkSHA256 {
				matches = append(matches, event)
			}
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("found %d durable events for current claimed WorkEnvelope, want exactly one", len(matches))
	}
	event := matches[0]
	if event.WorkID != claimed.ID || !canTransition(event.From, "claimed") ||
		event.TS != lease.ClaimAt || event.By != holder {
		return "", fmt.Errorf("durable claim event identity mismatch")
	}
	if lease.WorkSHA256 != "" && strings.TrimSpace(event.PreviousWorkSHA256) == "" {
		return "", fmt.Errorf("durable claim event does not bind the previous WorkEnvelope")
	}
	if lease.WorkID != claimed.ID || lease.By != holder {
		return "", fmt.Errorf("lease does not agree with claimed Work and event")
	}
	if err := s.validateClaimEventArtifacts(event, claimed); err != nil {
		return "", err
	}
	return holder, nil
}

func (s *Store) validateClaimEventArtifacts(event Event, claimed envelope.WorkEnvelope) error {
	// C1: claim events carry [action, policy] (pre-C1) or [action, policy,
	// ratt_*] (C1+). The first two are validated as artifacts; the third is
	// a RunAttempt id (evidence, not an envelope artifact — no readEventJSON).
	if len(event.Attached) < 2 || len(event.Attached) > 3 {
		return fmt.Errorf("claim event must bind exactly one action and policy")
	}
	if len(event.Attached) == 3 && !strings.HasPrefix(event.Attached[2], "ratt_") {
		return fmt.Errorf("claim event third attached id %q is not a ratt_ RunAttempt", event.Attached[2])
	}
	actionID, policyID := event.Attached[0], event.Attached[1]
	var action envelope.ActionIntent
	if err := s.readEventJSON(actionID, &action); err != nil {
		return fmt.Errorf("read claim action %s: %w", actionID, err)
	}
	if err := action.Validate(); err != nil {
		return fmt.Errorf("claim action %s invalid: %w", actionID, err)
	}
	var decision envelope.PolicyDecision
	if err := s.readEventJSON(policyID, &decision); err != nil {
		return fmt.Errorf("read claim policy %s: %w", policyID, err)
	}
	if err := decision.Validate(); err != nil {
		return fmt.Errorf("claim policy %s invalid: %w", policyID, err)
	}
	if action.ID != actionID || action.IDs.WorkID != claimed.ID || action.Actor != event.By ||
		action.Capability != "work.claim" || action.Action != "claim_work" ||
		action.Target.Kind != "work" || action.Target.ID != claimed.ID ||
		filepath.Clean(action.Target.Path) != filepath.Clean(s.workPath(claimed.ID)) {
		return fmt.Errorf("claim action %s conflicts with Work/event identity", actionID)
	}
	if decision.ID != policyID || decision.IDs.WorkID != claimed.ID || decision.ActionIntentID != actionID ||
		(decision.Outcome != "allow" && decision.Outcome != "allow_with_obligations") {
		return fmt.Errorf("claim policy %s conflicts with Work/event identity", policyID)
	}
	if !oneOf(actionID, claimed.ActionIntentIDs) {
		return fmt.Errorf("claimed Work does not retain its claim action")
	}
	normalNote := "claimed (policy " + decision.Outcome + ")"
	repairNote := "ownerless-claim repair (policy " + decision.Outcome + ")"
	if event.Note != normalNote && event.Note != repairNote {
		return fmt.Errorf("claim event policy outcome note mismatch")
	}
	return nil
}

// --- advance ---

type AdvanceInput struct {
	To    string
	Actor string
	// LaneRef optionally declares or corrects the work's lane mid-lifecycle. When set it must
	// resolve via capgraph.ResolveLaneRef (fail-closed) and is recorded as the canonical node
	// id on the holder's lease, under the same holder-scoped rules as any advance mutation.
	LaneRef     string
	GitDelivery *envelope.GitDeliveryContract
	// RunAttemptIDs attaches one or more RunAttempt ids (ratt_*) to the work envelope. S1
	// retires the pre-S1 `RunID` field, which silently appended non-`act_*` ids into
	// ActionIntentIDs. RunAttempt ids land on the dedicated slot so the validator can reject
	// non-ratt_ entries (envelope.validateRunAttemptIDs, pinned by
	// TestRunAttemptIDsRequireRattPrefix). Empty slice is a no-op.
	RunAttemptIDs  []string
	ArtifactID     string
	VerificationID string
	ContextPackID  string
	// EvidenceRefs are immutable proof attachments for a specialized admitted
	// transition (currently evidence-threshold autonomy). Generic CLI advances
	// leave this empty. applyAdvance persists them on both the WorkEnvelope and
	// the exact lifecycle event so replay does not depend on a mutable sidecar.
	EvidenceRefs []string
	Note         string
	// Steal and StealReason mirror CloseInput's lease escape hatch (see
	// leaseHolderAdvanceGate in lease_advance.go): advancing a work held by a different
	// actor is refused unless Steal is true AND StealReason is non-empty.
	Steal       bool
	StealReason string
}

// Advance moves a work along a legal edge of the state machine, optionally attaching
// linked envelope ids (runs = executed actions, artifacts = outputs).
func (s *Store) Advance(workID string, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
	var out envelope.WorkEnvelope
	var advanceErr error
	err := s.withLifecycleMutation(func() error {
		out, advanceErr = s.advanceLocked(workID, in, now)
		return advanceErr
	})
	return out, err
}

func (s *Store) advanceLocked(workID string, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return w, err
	}
	pendingActionID, err := s.pendingReceiptedActionID(workID)
	if err != nil {
		return w, err
	}
	if pendingActionID != "" {
		return w, fmt.Errorf("work %s has pending receipted action %s; recover it before another lifecycle mutation", workID, pendingActionID)
	}
	if terminalStates[w.State] {
		return w, fmt.Errorf("work %s is %s (terminal); cannot advance", workID, w.State)
	}
	if in.To == "" {
		return w, fmt.Errorf("advance requires --to <state>")
	}
	if terminalStates[in.To] {
		return w, terminalStateRequiresClose("advance", in.To)
	}
	if err := crossStateClaimedAdvanceError(w.State, in.To); err != nil {
		return w, err
	}
	if in.To != w.State && !canTransition(w.State, in.To) {
		return w, fmt.Errorf("illegal transition %s -> %s (allowed: %s)", w.State, in.To, strings.Join(transitions[w.State], ", "))
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "codex"
	}
	// Kill-switch (W9-J4), defense-in-depth: an autonomous (non-operator) admission of a
	// director-proposed work out of `proposed` is denied while the policy-owned autonomy
	// halt is engaged, even through this generic seam. Manual operator admit, normal
	// claim, and normal advance never enter this branch; when the halt is unset it is a
	// no-op, so behavior is unchanged.
	if err := s.autonomyHaltGuard(w, in.To, actor, now); err != nil {
		return w, err
	}
	// Editable-surfaces (EVOLVE-BLOCK), defense-in-depth: a system-originated admission of
	// a director-proposed work that declares a target_surface is denied through this generic
	// seam too, unless that surface is actively whitelisted. The halt guard above runs first,
	// so a halt wins; work without a target_surface is unaffected.
	if err := s.editableSurfaceGuard(w, in.To, actor, now); err != nil {
		return w, err
	}
	// Design-sketch gate (alignment depth scales with risk): a work that declared
	// plan_class=phased may not ENTER running until the sketch it promised is attached
	// to the envelope. Placed at this generic seam so every path into running -- plain
	// advance, --execute, dispatch-claim auto-advance -- passes it; work of any other
	// plan class (including empty, which means oneshot) is unaffected.
	if err := s.designSketchGuard(&w, in.To, actor, now); err != nil {
		return w, err
	}
	// Freeze (grants-as-gates, 2026-07-10), defense-in-depth: a system-originated
	// admission matching an active freeze decision record is denied through this
	// generic seam too, mirroring the halt/editable-surface guards above.
	// Operator-provenance advances are excluded by isAutonomousAdmission the same
	// way (Law 6: the operator can always act; the system cannot ignore its own
	// freeze).
	if err := s.freezeGuard(w, in.To, actor, now); err != nil {
		return w, err
	}
	if err := heldReleaseOperatorError(w.State, in.To, actor); err != nil {
		return w, err
	}
	if err := directorProposedReleaseOperatorError(w, in.To, actor); err != nil {
		return w, err
	}
	if in.GitDelivery != nil {
		normalized, err := s.preflightGitDeliveryBinding(w, actor, in.To, in.GitDelivery)
		if err != nil {
			return w, err
		}
		in.GitDelivery = normalized
	}
	// Resolve a declared/corrected lane fail-closed BEFORE any state mutation: a bad --lane ref
	// aborts the advance untouched (never records a refusal, never mutates the envelope).
	declaredLane, err := s.resolveDeclaredLane(in.LaneRef)
	if err != nil {
		return w, err
	}
	if err := s.leaseHolderAdvanceGate(w, actor, in, now); err != nil {
		return w, err
	}
	advanced, err := s.applyAdvance(w, in, now)
	if err != nil {
		return advanced, err
	}
	// C1: abandon terminality. When a claimed work is released back to ready
	// (the only path that produces claimed→ready), every RunAttempt minted at
	// claim time is terminalized as "abandoned". The advance is already
	// durable at this point; terminalization is best-effort evidence — a
	// failure here is a doctor-visible gap, not a rollback (C3 sweeps stuck
	// attempts).
	if w.State == "claimed" && in.To == "ready" {
		s.abandonRunAttempts(advanced.RunAttemptIDs, now)
	}
	// F2: supersede-at-attach. When an incoming harness_execution attempt
	// attaches to the envelope, any still-pending claim_reservation attempt
	// already on the envelope (w.RunAttemptIDs, captured before this
	// advance) is now stale -- a real execution superseded the claim-time
	// placeholder -- and is terminalized abandoned. Same best-effort
	// durability posture as abandonRunAttempts above.
	if len(in.RunAttemptIDs) > 0 {
		s.supersedeClaimReservations(w.RunAttemptIDs, in.RunAttemptIDs, now)
	}
	// The advance passed the holder-scoped gate; record the declared lane on the holder's lease.
	if strings.TrimSpace(in.LaneRef) != "" {
		if err := s.recordDeclaredLaneOnLease(w.ID, declaredLane); err != nil {
			return advanced, err
		}
	}
	return advanced, nil
}

// abandonRunAttempts terminalizes every non-terminal RunAttempt in ids as
// "abandoned". Best-effort: errors are silently dropped because the advance
// is already durable and the RunAttempt is evidence, not enforcement. C3
// (crash terminality) sweeps any attempts this path misses.
func (s *Store) abandonRunAttempts(ids []string, now time.Time) {
	store := s.runAttempts()
	for _, id := range ids {
		ra, err := store.Get(id)
		if err != nil {
			continue // not found or invalid — nothing to abandon
		}
		if runattempt.IsTerminal(ra.Status) {
			continue // already terminal (succeeded/failed/abandoned)
		}
		_, _ = store.Complete(id, runattempt.StatusAbandoned, now)
	}
}

// supersedeClaimReservations terminalizes every still-pending
// claim_reservation-origin attempt in existingIDs as "abandoned" once
// incomingIDs attaches at least one harness_execution-origin attempt (F2,
// settled design decision 2). Attempts with empty Origin (legacy, pre-F2)
// or any origin other than claim_reservation are never touched -- only a
// claim-time placeholder can be superseded, and only a real execution can
// supersede it. Best-effort: errors are silently dropped, same posture as
// abandonRunAttempts (the advance is already durable; terminalization here
// is evidence, not enforcement).
func (s *Store) supersedeClaimReservations(existingIDs, incomingIDs []string, now time.Time) {
	store := s.runAttempts()
	var execIDs []string
	for _, id := range incomingIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ra, err := store.Get(id)
		if err != nil {
			continue
		}
		if ra.Origin == runattempt.OriginHarnessExecution {
			execIDs = append(execIDs, id)
		}
	}
	if len(execIDs) == 0 {
		return
	}
	reason := "superseded by " + strings.Join(execIDs, ",")
	for _, id := range existingIDs {
		ra, err := store.Get(id)
		if err != nil {
			continue
		}
		if ra.Origin != runattempt.OriginClaimReservation {
			continue // legacy (empty origin) or non-claim origin — not touched
		}
		if runattempt.IsTerminal(ra.Status) {
			continue // already terminal
		}
		_, _ = store.CompleteWithReason(id, runattempt.StatusSuperseded, reason, now)
	}
}

// applyAdvance performs the state write for a legal, gate-cleared advance. It is shared
// by Advance (after its provenance/halt guards) and AdmitAuto (after its kill-switch and
// eligibility gates) so both mutate through one audited path.
// advanceBeforeWorkCommit is a test synchronization seam for the crash window
// after the lifecycle event is durable and before the WorkEnvelope rename.
var advanceBeforeWorkCommit = func() {}

// closeBeforeLeaseRelease is a test synchronization seam for the crash window
// after the terminal WorkEnvelope, receipt, transition and lifecycle event are
// durable and before the claim lease is released (see releaseLease).
var closeBeforeLeaseRelease = func() {}

func (s *Store) applyAdvance(w envelope.WorkEnvelope, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
	workID := w.ID
	if recovered, ok, err := s.recoverPendingAdvance(w, in.To); err != nil {
		return w, err
	} else if ok {
		return recovered, nil
	}
	plan, err := s.AdvancePlan(workID, now, AdvancePlanOptions{Actor: in.Actor, To: in.To, GitDelivery: in.GitDelivery})
	if err != nil {
		return w, err
	}
	if !plan.Policy.Allowed {
		return w, fmt.Errorf("cannot advance work %s: policy %s (decision %s)", workID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	if err := s.validateContextPackRef(in.ContextPackID); err != nil {
		return w, err
	}
	previousWorkSHA256, err := workEnvelopeSHA256(w)
	if err != nil {
		return w, err
	}
	from := w.State
	w.State = in.To
	if plan.GitDelivery != nil {
		contract := *plan.GitDelivery
		w.GitDelivery = &contract
		w.ExpectedBaseSHA = ""
	}
	w.ActionIntentIDs = append(w.ActionIntentIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	attached := []string{plan.ActionIntent.ID, plan.Decision.ID}
	if len(in.EvidenceRefs) > 0 {
		w.EvidenceRefs = mergeUnique(w.EvidenceRefs, in.EvidenceRefs)
		attached = append(attached, in.EvidenceRefs...)
	}
	if len(in.RunAttemptIDs) > 0 {
		for _, rid := range in.RunAttemptIDs {
			rid = strings.TrimSpace(rid)
			if rid == "" {
				continue
			}
			if !strings.HasPrefix(rid, "ratt_") {
				return envelope.WorkEnvelope{}, fmt.Errorf("advance RunAttemptIDs contains non-ratt_ id %q (settled in S1; see internal/runattempt.Prefix)", rid)
			}
			w.RunAttemptIDs = mergeUnique(w.RunAttemptIDs, []string{rid})
			attached = append(attached, rid)
		}
	}
	if in.ArtifactID != "" {
		w.ArtifactIDs = append(w.ArtifactIDs, in.ArtifactID)
		attached = append(attached, in.ArtifactID)
	}
	if in.VerificationID != "" {
		w.VerificationIDs = append(w.VerificationIDs, in.VerificationID)
		attached = append(attached, in.VerificationID)
	}
	if in.ContextPackID != "" {
		w.IDs.ContextPackID = in.ContextPackID
		attached = append(attached, in.ContextPackID)
	}
	if err := s.writeEventJSON(plan.ActionIntent.ID, &plan.ActionIntent); err != nil {
		return w, err
	}
	if err := s.writeEventJSON(plan.Decision.ID, &plan.Decision); err != nil {
		return w, err
	}
	workSHA256, err := workEnvelopeSHA256(w)
	if err != nil {
		return w, err
	}
	pendingPath := s.advancePendingPath(workID)
	stagedWork, err := s.stageJSON(pendingPath, &w)
	if err != nil {
		return w, err
	}
	if err := commitStagedJSONExclusive(stagedWork, pendingPath); err != nil {
		_ = os.Remove(stagedWork)
		return w, err
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "advanced (policy " + plan.Policy.Outcome + ")"
	} else {
		note += " (policy " + plan.Policy.Outcome + ")"
	}
	event := Event{
		TS:                 ts(now),
		WorkID:             workID,
		From:               from,
		To:                 in.To,
		By:                 plan.Actor,
		Note:               note,
		Attached:           attached,
		PreviousWorkSHA256: previousWorkSHA256,
		WorkSHA256:         workSHA256,
	}
	err = s.withWorkEventLock(func() error {
		before, err := s.readEventsStrictUnlocked(workID)
		if err != nil {
			return err
		}
		if err := s.appendEventUnlocked(event); err != nil {
			return err
		}
		after, err := s.readEventsStrictUnlocked(workID)
		if err != nil {
			return err
		}
		if len(after) != len(before)+1 || !reflect.DeepEqual(after[len(after)-1], event) {
			return fmt.Errorf("work event log did not preserve the exact advance event for %s", workID)
		}
		advanceBeforeWorkCommit()
		return commitStagedJSON(pendingPath, s.workPath(workID))
	})
	if err != nil {
		return w, err
	}
	return w, nil
}

func (s *Store) advancePendingPath(workID string) string {
	return s.workPath(workID) + ".advance-pending"
}

func (s *Store) recoverPendingAdvance(current envelope.WorkEnvelope, to string) (envelope.WorkEnvelope, bool, error) {
	currentSHA256, err := workEnvelopeSHA256(current)
	if err != nil {
		return current, false, err
	}
	pendingPath := s.advancePendingPath(current.ID)
	var recovered envelope.WorkEnvelope
	var didRecover bool
	err = s.withWorkEventLock(func() error {
		events, err := s.readEventsStrictUnlocked(current.ID)
		if err != nil {
			return err
		}
		var pending envelope.WorkEnvelope
		if err := s.readJSON(pendingPath, &pending); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("read pending advance for %s: %w", current.ID, err)
			}
			for _, event := range events {
				if event.From == current.State && event.PreviousWorkSHA256 == currentSHA256 && event.WorkSHA256 != "" {
					return fmt.Errorf("work %s has a durable advance event to %s but no pending WorkEnvelope", current.ID, event.To)
				}
			}
			return nil
		}
		if err := pending.Validate(); err != nil {
			return fmt.Errorf("pending advance for %s invalid: %w", current.ID, err)
		}
		if pending.ID != current.ID || pending.State != to {
			return fmt.Errorf("pending advance for %s conflicts with requested transition %s -> %s", current.ID, current.State, to)
		}
		pendingSHA256, err := workEnvelopeSHA256(pending)
		if err != nil {
			return err
		}
		postMatches := make([]Event, 0, 1)
		exactMatches := make([]Event, 0, 1)
		for _, event := range events {
			if event.To != pending.State || event.WorkSHA256 != pendingSHA256 {
				continue
			}
			postMatches = append(postMatches, event)
			if event.From == current.State && event.PreviousWorkSHA256 == currentSHA256 {
				exactMatches = append(exactMatches, event)
			}
		}
		if currentSHA256 == pendingSHA256 {
			if len(postMatches) != 1 {
				return fmt.Errorf("pending advance for %s has %d matching durable events, want exactly one", current.ID, len(postMatches))
			}
			if err := s.validatePendingAdvance(postMatches[0], pending); err != nil {
				return err
			}
			if err := removeFileAndSyncDir(pendingPath); err != nil {
				return err
			}
			recovered = current
			didRecover = true
			return nil
		}
		switch len(exactMatches) {
		case 0:
			if len(postMatches) > 0 {
				return fmt.Errorf("pending advance for %s does not match the current WorkEnvelope", current.ID)
			}
			return removeFileAndSyncDir(pendingPath)
		case 1:
			if err := s.validatePendingAdvance(exactMatches[0], pending); err != nil {
				return err
			}
			if err := commitStagedJSON(pendingPath, s.workPath(current.ID)); err != nil {
				return err
			}
			recovered = pending
			didRecover = true
			return nil
		default:
			return fmt.Errorf("pending advance for %s has %d matching durable events, want exactly one", current.ID, len(exactMatches))
		}
	})
	if err != nil {
		return current, false, err
	}
	return recovered, didRecover, nil
}

func (s *Store) validatePendingAdvance(event Event, pending envelope.WorkEnvelope) error {
	if event.PreviousWorkSHA256 == "" || event.WorkSHA256 == "" {
		return fmt.Errorf("pending advance event for %s does not bind both WorkEnvelope versions", pending.ID)
	}
	if event.WorkID != pending.ID || event.To != pending.State {
		return fmt.Errorf("pending advance event for %s conflicts with WorkEnvelope identity", pending.ID)
	}
	if len(event.Attached) < 2 {
		return fmt.Errorf("pending advance event for %s does not bind action and policy envelopes", pending.ID)
	}
	actionID, policyID := event.Attached[0], event.Attached[1]
	var action envelope.ActionIntent
	if err := s.readEventJSON(actionID, &action); err != nil {
		return fmt.Errorf("read pending advance action %s: %w", actionID, err)
	}
	if err := action.Validate(); err != nil {
		return fmt.Errorf("pending advance action %s invalid: %w", actionID, err)
	}
	var decision envelope.PolicyDecision
	if err := s.readEventJSON(policyID, &decision); err != nil {
		return fmt.Errorf("read pending advance policy %s: %w", policyID, err)
	}
	if err := decision.Validate(); err != nil {
		return fmt.Errorf("pending advance policy %s invalid: %w", policyID, err)
	}
	if strings.TrimSpace(event.By) == "" || action.ID != actionID || action.IDs.WorkID != pending.ID || action.Actor != event.By {
		return fmt.Errorf("pending advance action %s conflicts with lifecycle event identity", actionID)
	}
	if action.Capability != "work.advance" || action.Action != "advance_work_to_"+event.To ||
		action.Target.Kind != "work" || action.Target.ID != pending.ID ||
		filepath.Clean(action.Target.Path) != filepath.Clean(s.workPath(pending.ID)) {
		return fmt.Errorf("pending advance action %s does not describe transition to %s", actionID, event.To)
	}
	if decision.ID != policyID || decision.IDs.WorkID != pending.ID ||
		decision.ActionIntentID != actionID || pending.PolicyDecisionID != policyID {
		return fmt.Errorf("pending advance policy %s conflicts with lifecycle event identity", policyID)
	}
	if decision.Outcome != "allow" && decision.Outcome != "allow_with_obligations" {
		return fmt.Errorf("pending advance policy %s does not allow publication", policyID)
	}
	if !oneOf(actionID, pending.ActionIntentIDs) {
		return fmt.Errorf("pending WorkEnvelope %s does not attach action %s", pending.ID, actionID)
	}
	return nil
}

// --- act (governed mutation) ---

// RecordAction records a performed ActionIntent against a work — but ONLY if its
// PolicyDecision allows it. This is the enforcement seam: a mutating adapter cannot
// record an ungoverned mutation, so no model bypasses policy by prompt obedience.
// (Decoupled from the policy package on purpose: the store enforces on the decision's
// own outcome, so the gate holds even if a caller skips the evaluator.)
func (s *Store) RecordAction(workID string, ai envelope.ActionIntent, pd envelope.PolicyDecision, now time.Time) (envelope.WorkEnvelope, error) {
	var out envelope.WorkEnvelope
	var recordErr error
	err := s.withLifecycleMutation(func() error {
		out, recordErr = s.recordActionLocked(workID, ai, pd, now)
		return recordErr
	})
	return out, err
}

func (s *Store) recordActionLocked(workID string, ai envelope.ActionIntent, pd envelope.PolicyDecision, now time.Time) (envelope.WorkEnvelope, error) {
	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return w, err
	}
	pendingActionID, err := s.pendingReceiptedActionID(workID)
	if err != nil {
		return w, err
	}
	if pendingActionID != "" {
		return w, fmt.Errorf("work %s has pending receipted action %s; recover it before another lifecycle mutation", workID, pendingActionID)
	}
	if terminalStates[w.State] {
		return w, fmt.Errorf("work %s is %s (terminal); cannot record an action", workID, w.State)
	}
	if ai.IDs.WorkID != workID {
		return w, fmt.Errorf("action work_id %q does not match work %q", ai.IDs.WorkID, workID)
	}
	if pd.ActionIntentID != ai.ID {
		return w, fmt.Errorf("policy decision is for action %q, not %q", pd.ActionIntentID, ai.ID)
	}
	if err := ai.Validate(); err != nil {
		return w, fmt.Errorf("action intent invalid: %w", err)
	}
	if err := pd.Validate(); err != nil {
		return w, fmt.Errorf("policy decision invalid: %w", err)
	}
	// THE GATE: refuse to record a performed action unless policy allows it.
	if pd.Outcome != "allow" && pd.Outcome != "allow_with_obligations" {
		return w, fmt.Errorf("cannot record action %s: policy %s (a mutating adapter requires an allowing PolicyDecision)", ai.ID, pd.Outcome)
	}
	if err := s.writeEventJSON(ai.ID, &ai); err != nil {
		return w, err
	}
	if err := s.writeEventJSON(pd.ID, &pd); err != nil {
		return w, err
	}
	w.ActionIntentIDs = append(w.ActionIntentIDs, ai.ID) // the performed action is an action ref in the chain
	w.PolicyDecisionID = pd.ID
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return w, err
	}
	if err := s.appendEvent(Event{TS: ts(now), WorkID: workID, From: w.State, To: w.State, Note: "action recorded: " + ai.Action + " (policy " + pd.Outcome + ")", Attached: []string{ai.ID, pd.ID}}); err != nil {
		return w, err
	}
	// PLACEMENT_OVERRIDE observability: policy.go fail-opens the placement-guard
	// input on PLACEMENT_OVERRIDE (operator-ratified; see docs/architecture/runtime.md),
	// and already appends a visible reason string to the PolicyDecision. That
	// reason lives only inside this one decision unless we persist it here — this
	// is the smallest honest seam: RecordAction is the consumer that already
	// durably persists every PolicyDecision, so an override use gets one
	// additional observation Event, auditable beyond the in-decision reasons.
	if reason := placementOverrideReason(pd); reason != "" {
		if err := s.appendEvent(Event{TS: ts(now), WorkID: workID, From: w.State, To: w.State, Note: "placement_override observed: " + reason, Attached: []string{pd.ID}}); err != nil {
			return w, err
		}
	}
	return w, nil
}

// placementOverrideObservationMarker matches the exact reason suffix
// internal/policy appends to a PolicyDecision when PLACEMENT_OVERRIDE (or an
// override-flagged PlacementGuardContext) fail-opens the placement guard.
const placementOverrideObservationMarker = "PLACEMENT_OVERRIDE active, fail-open"

// placementOverrideReason returns the matching reason string if pd carries
// evidence that a placement-guard fail-open fired via PLACEMENT_OVERRIDE, or
// "" if it did not fire (so RecordAction persists nothing extra).
func placementOverrideReason(pd envelope.PolicyDecision) string {
	for _, reason := range pd.Reasons {
		if strings.Contains(reason, placementOverrideObservationMarker) {
			return reason
		}
	}
	return ""
}

// --- close ---

type CloseInput struct {
	Outcome  string // completed | failed | cancelled
	MoveType string // outward_delta | memory_delta | rule_delta | control_plane_repair | audit_only
	Actor    string
	LaneID   string
	Summary  string
	Changed  bool
	// Steal and StealReason are the close-gate lease escape hatch: closing a work held by a
	// different actor is refused unless Steal is true AND StealReason is non-empty. A steal
	// records the prior holder + reason + stealing actor on the envelope trail (see
	// leaseHolderStatusFor / recordLeaseSteal in lease_close.go). Coordination is
	// advisory-with-teeth, not a deadlock machine — a dead lane must never brick an envelope.
	Steal                      bool
	StealReason                string
	UsedCapabilities           []string
	MissedCapabilities         []string
	FalseTriggeredCapabilities []string
	ProofRefs                  []string
	MemoryCandidates           []string
	MemoryPromoted             []string
	MemoryParked               []string
	MemoryRejected             []string
	FailureSurfaceOutcome      string
	FailureMechanism           string
	// UsedDeclared stamps the emitted receipt's skills.used_declared. It is the honesty
	// flag the injected-ref reinforcement projection reads: true means this close made an
	// affirmative statement about what was used (the CLI passed --used, or a session-end
	// consumption detector ran), so an injected ref absent from Used is honestly ignored;
	// false (the default) leaves absence as unknown. Additive.
	UsedDeclared bool
	// ConsumedRefs are RAW injected-ref consumption signals (e.g. claim://<id>) merged
	// verbatim into the receipt's Skills.Used so the usage projection's pack.included↔used
	// join can score them CONSUMED. Unlike UsedCapabilities they are NOT capability-normalized
	// (a claim ref is not a capability node); they are kept out of the skill typed projections.
	ConsumedRefs []string
	// RunAttemptIDs attaches one or more RunAttempt ids (ratt_*) to the work
	// envelope at close -- the failed-harness-run forward link. Close is the
	// terminal transition for failed/cancelled work whose RunAttempt would
	// otherwise be orphaned (Advance already carries RunAttemptIDs for the
	// running->verifying path; this mirrors it for the close path so a failed
	// run is reachable from the envelope, not only from the run_attempts dir).
	// Same ratt_ prefix rule as AdvanceInput.RunAttemptIDs (enforced below).
	RunAttemptIDs []string
	// OpensBranches is the executor's declared future-branch set (the model
	// PROPOSES; envelope validation decides; the BranchCoverage read model
	// verifies over later flow). Empty means this close opens no checkable
	// branch — which, combined with an empty next_consumer, makes a
	// future_behavior_changed claim unverifiable by construction.
	// Additive; validated as part of the emitted Transition.
	OpensBranches []envelope.BranchRef
}

// CloseResult is the connected chain proof: the final work plus the Receipt and the
// apex Transition emitted at close, all carrying the same work_id.
type CloseResult struct {
	Work       envelope.WorkEnvelope `json:"work"`
	Receipt    envelope.Receipt      `json:"receipt"`
	Transition envelope.Transition   `json:"transition"`
}

var receiptOutcome = map[string]string{"completed": "completed", "failed": "failed", "cancelled": "no_op"}

// Close lands a work in a terminal state and emits the Receipt + Transition that tie the
// whole chain (context -> runs -> outputs) to this work_id.
func (s *Store) Close(workID string, in CloseInput, now time.Time) (CloseResult, error) {
	var out CloseResult
	var closeErr error
	err := s.withLifecycleMutation(func() error {
		out, closeErr = s.closeLocked(workID, in, now)
		return closeErr
	})
	return out, err
}

func (s *Store) closeLocked(workID string, in CloseInput, now time.Time) (CloseResult, error) {
	var res CloseResult
	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return res, err
	}
	pendingActionID, err := s.pendingReceiptedActionID(workID)
	if err != nil {
		return res, err
	}
	if pendingActionID != "" {
		return res, fmt.Errorf("work %s has pending receipted action %s; recover it before another lifecycle mutation", workID, pendingActionID)
	}
	if !terminalStates[in.Outcome] {
		return res, fmt.Errorf("close --outcome must be completed|failed|cancelled, got %q", in.Outcome)
	}
	if w.State != in.Outcome && !canTransition(w.State, in.Outcome) {
		return res, fmt.Errorf("cannot close %s from state %q to %q", workID, w.State, in.Outcome)
	}
	var landingProof *LandCheckReport
	deliveryProofRefs := mergeUnique(w.ArtifactIDs, w.VerificationIDs, in.ProofRefs, landCheckEvidenceRefs(w.EvidenceRefs))
	landingProof, err = s.gitDeliveryProofForClose(w, in.Outcome, in.MoveType, deliveryProofRefs, now)
	if err != nil {
		return res, err
	}
	// The lane stamped onto close-minted receipts is the DECLARED lane on the lease (a canonical
	// node id recorded at claim/advance time) or an explicit, resolver-gated in.LaneID override —
	// never the actor/owner/harness (no-guessing, B0). No declared lane → empty.
	producerLaneID, err := s.closeLaneID(w.ID, in.LaneID)
	if err != nil {
		return res, err
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "codex"
	}
	if err := heldReleaseOperatorError(w.State, in.Outcome, actor); err != nil {
		return res, err
	}
	if err := s.leaseHolderCloseGate(w, actor, in, now); err != nil {
		return res, err
	}
	if !oneOf(in.MoveType, envelope.MoveTypes) {
		return res, fmt.Errorf("close --move-type must be one of %v, got %q", envelope.MoveTypes, in.MoveType)
	}
	plan, err := s.ClosePlan(workID, now, ClosePlanOptions{Actor: in.Actor, Outcome: in.Outcome, MoveType: in.MoveType, ProofRefs: in.ProofRefs})
	if err != nil {
		return res, err
	}
	if !plan.Policy.Allowed {
		return res, fmt.Errorf("cannot close work %s: policy %s (decision %s)", workID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	w.ActionIntentIDs = append(w.ActionIntentIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	capabilityRefs, err := s.capabilityRefsForContextPack(w.IDs.ContextPackID)
	if err != nil {
		return res, err
	}
	usedCapabilitiesForRuns, err := s.usedCapabilitiesForRuns(w.ActionIntentIDs, capabilityRefs)
	if err != nil {
		return res, err
	}
	usedCapabilities := mergeUnique(normalizeCapabilityRefs(in.UsedCapabilities), usedCapabilitiesForRuns)
	missedCapabilities := normalizeCapabilityRefs(in.MissedCapabilities)
	falseTriggeredCapabilities := normalizeCapabilityRefs(in.FalseTriggeredCapabilities)
	skillProjectionRefs := mergeUnique(usedCapabilities, capabilityRefs, missedCapabilities, falseTriggeredCapabilities)
	proofRefs := mergeUnique(in.ProofRefs, w.ArtifactIDs, w.VerificationIDs, landCheckEvidenceRefs(w.EvidenceRefs))
	if err := validateProofMedium(w.ProofMedium, in.Outcome, proofRefs); err != nil {
		return res, err
	}
	memoryCandidates := mergeUnique(w.MemoryCandidateIDs, in.MemoryCandidates)
	surprise := surpriseOnClose(w, in)

	// Receipt — how the run is learned from.
	rOut := receiptOutcome[in.Outcome]
	rec := envelope.Receipt{
		Header:   newHeaderWithLane(envelope.SVReceipt, idFor("rec_", workID, ts(now)), now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk, producerLaneID),
		Outcome:  rOut,
		MoveType: in.MoveType,
		Surprise: surprise,
		StateDelta: envelope.StateDelta{
			Changed:      in.Changed,
			Summary:      in.Summary,
			FilesChanged: []string{},
			Artifacts:    orEmpty(w.ArtifactIDs),
			Verification: orEmpty(w.VerificationIDs),
		},
		Cost: envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{
			Used:                usedCapabilities,
			Missed:              missedCapabilities,
			FalseTriggered:      falseTriggeredCapabilities,
			Candidate:           capabilityRefs,
			CapabilitiesOffered: capabilityRefs,
			UsedDeclared:        in.UsedDeclared,
		},
		Memory: envelope.ReceiptMemory{
			Candidates: memoryCandidates,
			Promoted:   orEmpty(in.MemoryPromoted),
			Parked:     orEmpty(in.MemoryParked),
			Rejected:   orEmpty(in.MemoryRejected),
		},
		Next:      envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs: proofRefs,
	}
	if fc := failureCause(in.FailureSurfaceOutcome, in.FailureMechanism); fc != nil {
		rec.FailureCause = fc
	}
	rec.StateDelta.Changed = rec.EffectiveChanged()
	if err := rec.Validate(); err != nil {
		return res, fmt.Errorf("emitted receipt invalid: %w", err)
	}

	// Transition — the apex object: what changed across spaces, tied to this work_id.
	before := []string{}
	if w.IDs.ContextPackID != "" {
		before = []string{w.IDs.ContextPackID}
	}
	after := append(append([]string{}, orEmpty(w.ArtifactIDs)...), rec.ID)
	typedProjectionRefs := closeProjectionRefs(rec, w.IDs.ContextPackID)
	// Raw injected-ref consumption (e.g. session-end claim:// refs) lands in the receipt's
	// Skills.Used AFTER the skill typed projections are built, so it feeds the usage
	// projection's consumed join without masquerading as a used SKILL. Not capability-
	// normalized: a claim ref must match pack.included[].ref verbatim.
	if len(in.ConsumedRefs) > 0 {
		rec.Skills.Used = mergeUnique(rec.Skills.Used, in.ConsumedRefs)
	}
	tr := envelope.Transition{
		Header:     newHeaderWithLane(envelope.SVTransition, idFor("tr_", workID, ts(now)), now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk, producerLaneID),
		BeforeRefs: before,
		ActionRefs: orEmpty(w.ActionIntentIDs),
		AfterRefs:  after,
		StateDelta: in.Summary,
		Projections: envelope.Projections{
			Memory: []string{}, Skill: skillProjectionRefs, Product: []string{}, Cost: []string{},
			Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
			Typed: typedProjectionRefs,
		},
		SecondOrder: envelope.SecondOrder{
			FutureBehaviorChanged: in.MoveType != "audit_only",
			ShouldHarden:          shouldHardenOnClose(w.TargetSurface, in.Outcome, in.ProofRefs),
		},
		OpensBranches: in.OpensBranches,
	}
	if err := tr.Validate(); err != nil {
		return res, fmt.Errorf("emitted transition invalid: %w", err)
	}
	var referenceTransaction gitReferenceTransaction
	if landingProof != nil {
		currentProof, err := s.revalidateLandedProof(w, *landingProof, now)
		if err != nil {
			return res, err
		}
		expectations := []gitReferenceExpectation{{
			Ref: currentProof.TargetBranchRef,
			SHA: currentProof.TargetSHA,
		}}
		if currentProof.PublicationMode == "required" {
			expectations = append(expectations, gitReferenceExpectation{
				Ref: currentProof.PublicationRef,
				SHA: currentProof.PublicationSHA,
			})
		}
		referenceTransaction, err = startGitReferenceTransaction(currentProof.RepoPath, expectations)
		if err != nil {
			return res, fmt.Errorf("prepare Git reference transaction: %w", err)
		}
		// Git's transaction makes only the target/publication ref values atomic.
		// Revalidate repository identity once more while those locks are held and
		// before the first terminal byte; Git does not lock config or object roots.
		if err := s.validateBoundGitDelivery(w); err != nil {
			identityErr := fmt.Errorf("final repository identity revalidation: %w", err)
			if abortErr := referenceTransaction.Abort(); abortErr != nil {
				return res, errors.Join(identityErr, fmt.Errorf("abort Git reference transaction: %w", abortErr))
			}
			return res, identityErr
		}
	}
	referenceTransactionFinished := false
	defer func() {
		// An unexpected panic must not strand Git ref locks. Ordinary persistence
		// failures abort explicitly below so an abort failure can be reported.
		if referenceTransaction != nil && !referenceTransactionFinished {
			_ = referenceTransaction.Abort()
		}
	}()
	abortReferenceTransaction := func(cause error) error {
		if referenceTransaction == nil {
			return cause
		}
		abortErr := referenceTransaction.Abort()
		referenceTransactionFinished = true
		if abortErr != nil {
			return errors.Join(cause, fmt.Errorf("abort Git reference transaction: %w", abortErr))
		}
		return cause
	}

	// Persist the emitted envelopes, then land the work terminal and link the transition.
	if err := s.writeEventJSON(plan.ActionIntent.ID, &plan.ActionIntent); err != nil {
		return res, abortReferenceTransaction(err)
	}
	if err := s.writeEventJSON(plan.Decision.ID, &plan.Decision); err != nil {
		return res, abortReferenceTransaction(err)
	}
	if err := s.writeEventJSON(rec.ID, &rec); err != nil {
		return res, abortReferenceTransaction(err)
	}
	if err := s.writeEventJSON(tr.ID, &tr); err != nil {
		return res, abortReferenceTransaction(err)
	}
	from := w.State
	existingRunAttemptIDs := append([]string{}, w.RunAttemptIDs...)
	// Attach close-time RunAttempt ids (the failed-harness-run forward link)
	// before the envelope is written. Same ratt_ prefix rule as Advance; a
	// non-ratt_ id is a caller bug, not a retry, so it aborts the close.
	for _, rid := range in.RunAttemptIDs {
		rid = strings.TrimSpace(rid)
		if rid == "" {
			continue
		}
		if !strings.HasPrefix(rid, "ratt_") {
			return res, fmt.Errorf("close RunAttemptIDs contains non-ratt_ id %q (settled in S1; see internal/runattempt.Prefix)", rid)
		}
		w.RunAttemptIDs = mergeUnique(w.RunAttemptIDs, []string{rid})
	}
	w.State = in.Outcome
	w.IDs.TransitionID = tr.ID
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return res, abortReferenceTransaction(err)
	}
	workSHA256, err := workEnvelopeSHA256(w)
	if err != nil {
		return res, abortReferenceTransaction(err)
	}
	receiptSHA256, err := receiptEnvelopeSHA256(rec)
	if err != nil {
		return res, abortReferenceTransaction(err)
	}
	if err := s.appendEvent(Event{TS: ts(now), WorkID: workID, From: from, To: in.Outcome, Note: "closed: " + in.MoveType + " (policy " + plan.Policy.Outcome + ")", Attached: []string{plan.ActionIntent.ID, plan.Decision.ID, rec.ID, tr.ID}, WorkSHA256: workSHA256, ReceiptSHA256: receiptSHA256}); err != nil {
		return res, abortReferenceTransaction(err)
	}
	if referenceTransaction != nil {
		if err := referenceTransaction.Commit(); err != nil {
			referenceTransactionFinished = true
			return res, fmt.Errorf("commit Git reference transaction: %w", err)
		}
		referenceTransactionFinished = true
	}
	// I1: a lease must not outlive its work's terminal state. Close is the ONLY
	// path into a terminal state (Advance refuses one — terminalStateRequiresClose),
	// so releasing here retires the whole lease lifetime rather than deferring it
	// to a sweep that may not run for weeks.
	//
	// Ordering is the invariant, not an implementation detail. The release runs
	// only AFTER the terminal WorkEnvelope, receipt, transition, lifecycle event
	// and Git reference transaction are durable, mirroring abandonRunAttempts'
	// posture below: at this point the close cannot be rolled back, so cleanup
	// is best-effort and a failure is a doctor-visible gap, not a lost close.
	// The reverse order would be a real regression — a crash after an early
	// release would leave a still-live claimed work with no lease, silently
	// disarming leaseHolderCloseGate/leaseHolderAdvanceGate for whoever runs
	// next. The residue this ordering can leave (terminal work + lease) is
	// exactly the pair internal/home's sweep already backstops.
	closeBeforeLeaseRelease()
	s.releaseLease(workID)
	// A failed governed execution attaches its harness_execution attempt through
	// Close rather than Advance. Retire any still-pending claim reservation only
	// after the terminal work/event/ref transaction is durable, mirroring
	// Advance's supersede-at-attach posture without abandoning a reservation for
	// a close that later fails.
	if len(in.RunAttemptIDs) > 0 {
		s.supersedeClaimReservations(existingRunAttemptIDs, in.RunAttemptIDs, now)
	}
	return CloseResult{Work: w, Receipt: rec, Transition: tr}, nil
}

// releaseLease deletes workID's claim lease, if one is still held. Called only
// from closeLocked, after the terminal state is durable (see the invariant
// comment there).
//
// An absent lease is the normal no-op: close has always been legal for work
// that was never claimed, and re-closing already-terminal work must stay
// idempotent.
//
// The result is deliberately dropped rather than returned, mirroring
// abandonRunAttempts: the close is already committed by the time this runs, so
// surfacing a removal failure would report a close that actually succeeded as
// failed. The failure is not silent — a lease that survives its terminal work
// is exactly what home.OperationalHygiene counts in StaleLeases (doctor gate
// hygiene:no-stale-leases) and what `home archive-history --kind lease` sweeps
// as the orphan backstop.
func (s *Store) releaseLease(workID string) {
	_ = removeFileAndSyncDir(s.leasePath(workID))
}

// WriteResidualLeaseForTerminalWork plants the lease residue a terminal work can
// still carry after I1, for callers that need to construct that state rather than
// wait for it to occur.
//
// It exists because I1 made the state unreachable through the public API: Advance
// refuses terminal states (terminalStateRequiresClose) and Close now releases the
// lease in the same publish, so no ordinary sequence leaves a terminal work holding
// one. The residue is still REAL — a home written before that release existed, a
// crash inside the close's release window, and work archived out from under its
// lease all produce it — and the sweep and the proof-check agents gate both have to
// exercise a non-empty residue population to assert anything at all. Without this,
// a fixture that used to get residue from a plain close silently gets an empty set,
// and the gates compare nothing.
//
// The lease layout stays owned here; callers pass intent, not paths.
func (s *Store) WriteResidualLeaseForTerminalWork(workID, by string, at time.Time) error {
	w, err := s.Get(workID)
	if err != nil {
		return err
	}
	if !terminalStates[w.State] {
		return fmt.Errorf("residual-lease fixture needs terminal work, %s is %q", workID, w.State)
	}
	lease := Lease{WorkID: workID, By: by, ClaimAt: ts(at)}
	return s.writeJSON(s.leasePath(workID), &lease)
}

func landCheckEvidenceRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, ok := landCheckIDFromEvidenceRef(ref); ok {
			out = append(out, ref)
		}
	}
	return out
}

// --- persistence helpers ---

func (s *Store) writeJSON(path string, v any) error {
	if strings.HasPrefix(path, "compound://events/") {
		ref, err := eventstore.ParseReference(path)
		if err != nil {
			return err
		}
		return s.writeEventJSON(ref.ID(), v)
	}
	stagedPath, err := s.stageJSON(path, v)
	if err != nil {
		return err
	}
	if err := commitStagedJSON(stagedPath, path); err != nil {
		_ = os.Remove(stagedPath)
		return err
	}
	return nil
}

func (s *Store) writeEventJSON(id string, v any) error {
	_, err := s.events.PutJSON(id, v, "  ")
	return err
}

var writeStagedJSONFile = func(file *os.File, data []byte) error {
	_, err := file.Write(data)
	return err
}

func (s *Store) stageJSON(path string, v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".stage-")
	if err != nil {
		return "", err
	}
	stagedPath := file.Name()
	cleanup := func(cause error) (string, error) {
		_ = file.Close()
		_ = os.Remove(stagedPath)
		return "", cause
	}
	if err := file.Chmod(0o644); err != nil {
		return cleanup(err)
	}
	if err := writeStagedJSONFile(file, append(data, '\n')); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return "", err
	}
	return stagedPath, nil
}

func commitStagedJSON(stagedPath, targetPath string) error {
	if err := os.Rename(stagedPath, targetPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(targetPath))
}

func commitStagedJSONExclusive(stagedPath, targetPath string) error {
	if err := os.Link(stagedPath, targetPath); err != nil {
		return err
	}
	if err := os.Remove(stagedPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(targetPath))
}

func removeFileAndSyncDir(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}

func workEnvelopeSHA256(work envelope.WorkEnvelope) (string, error) {
	return canonicalEnvelopeSHA256(work)
}

func receiptEnvelopeSHA256(receipt envelope.Receipt) (string, error) {
	return canonicalEnvelopeSHA256(receipt)
}

func canonicalEnvelopeSHA256(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s *Store) capabilityRefsForContextPack(contextPackID string) ([]string, error) {
	if strings.TrimSpace(contextPackID) == "" {
		return []string{}, nil
	}
	path := filepath.Join(s.home, "context", "packs", contextPackID+".json")
	var pack envelope.ContextPack
	if err := s.readJSON(path, &pack); err != nil {
		if strings.HasPrefix(err.Error(), "not found:") {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read context pack %s for capability refs: %w", contextPackID, err)
	}
	if err := pack.Validate(); err != nil {
		return nil, fmt.Errorf("context pack %s invalid: %w", contextPackID, err)
	}
	seen := map[string]bool{}
	refs := []string{}
	for _, offer := range pack.CapabilityOffers {
		ref := normalizeCapabilityRef(offer.Ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, included := range pack.Included {
		ref := normalizeCapabilityRef(included.Ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs, nil
}

func (s *Store) usedCapabilitiesForRuns(runIDs, offeredRefs []string) ([]string, error) {
	seen := map[string]bool{}
	used := []string{}
	offeredByNode := capabilityRefsByNodeID(offeredRefs)
	for _, runID := range runIDs {
		var ai envelope.ActionIntent
		if err := s.readEventJSON(runID, &ai); err != nil {
			if strings.HasPrefix(err.Error(), "not found:") {
				continue
			}
			return nil, fmt.Errorf("read action intent %s for used capabilities: %w", runID, err)
		}
		if err := ai.Validate(); err != nil {
			return nil, fmt.Errorf("action intent %s invalid: %w", runID, err)
		}
		if strings.TrimSpace(ai.Capability) == "" || internalLifecycleCapability(ai.Capability) {
			continue
		}
		ref := normalizeCapabilityRef(ai.Capability)
		if ref == "" {
			continue
		}
		if offeredRef := offeredByNode[capabilityNodeID(ref)]; offeredRef != "" {
			ref = offeredRef
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		used = append(used, ref)
	}
	return used, nil
}

func normalizeCapabilityRefs(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		ref := normalizeCapabilityRef(value)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

func normalizeCapabilityRef(value string) string {
	nodeID := capabilityNodeID(value)
	if !realCapabilityNodeID(nodeID) {
		return ""
	}
	return "capability://" + nodeID
}

func capabilityRefsByNodeID(refs []string) map[string]string {
	out := map[string]string{}
	for _, ref := range refs {
		normalized := normalizeCapabilityRef(ref)
		if normalized == "" {
			continue
		}
		out[capabilityNodeID(normalized)] = normalized
	}
	return out
}

func capabilityNodeID(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "capability://")
	return strings.TrimSpace(ref)
}

func realCapabilityNodeID(nodeID string) bool {
	for _, prefix := range []string{
		"lens:",
		"procedure:",
		"script:",
		"tool:",
		"mcp:",
		"model_profile:",
		"harness:",
		"eval:",
		"workflow:",
		"adapter:",
		"doctrine:",
		"taste_standard:",
		"skill:",
		"instruction_rule:",
	} {
		if strings.HasPrefix(nodeID, prefix) && strings.TrimSpace(strings.TrimPrefix(nodeID, prefix)) != "" {
			return true
		}
	}
	return false
}

func internalLifecycleCapability(capability string) bool {
	switch capability {
	case "work.create", "work.claim", "work.advance", "work.close":
		return true
	default:
		return false
	}
}

func validateProofMedium(medium, outcome string, proofRefs []string) error {
	medium = strings.TrimSpace(strings.ToLower(medium))
	if medium == "" || medium == "none" || outcome != "completed" {
		return nil
	}
	switch medium {
	case "web-ui":
		if !containsProofText(proofRefs, "screenshot") || !containsAnyProofText(proofRefs, []string{"design-review", "design_review"}) {
			return fmt.Errorf("proof_medium web-ui requires screenshot artifact and design-review verdict proof refs")
		}
	case "ios":
		if !containsProofText(proofRefs, "screenshot") {
			return fmt.Errorf("proof_medium ios requires screenshot proof ref")
		}
	case "cli", "api":
		if !containsAnyProofText(proofRefs, []string{"command-output", "command_output", "test", "tests"}) {
			return fmt.Errorf("proof_medium %s requires command-output or test proof ref", medium)
		}
	default:
		return fmt.Errorf("proof_medium %q is not supported", medium)
	}
	return nil
}

func containsProofText(values []string, needle string) bool {
	return containsAnyProofText(values, []string{needle})
}

func containsAnyProofText(values []string, needles []string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func mergeUnique(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range groups {
		for _, v := range group {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func closeProjectionRefs(rec envelope.Receipt, contextPackID string) []envelope.ProjectionRef {
	var out []envelope.ProjectionRef
	add := func(kind, ref, role, evidence string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		out = append(out, envelope.ProjectionRef{Kind: kind, Ref: ref, Role: role, EvidenceRef: evidence})
	}
	for _, ref := range rec.Skills.Candidate {
		add("skill", ref, "offered", contextPackID)
	}
	for _, ref := range rec.Skills.Used {
		add("skill", ref, "used", rec.ID)
	}
	for _, ref := range rec.Skills.Missed {
		add("skill", ref, "missed", rec.ID)
	}
	for _, ref := range rec.Skills.FalseTriggered {
		add("skill", ref, "false_triggered", rec.ID)
	}
	for _, ref := range rec.Memory.Candidates {
		add("memory", ref, "candidate", rec.ID)
	}
	for _, ref := range rec.Memory.Promoted {
		add("memory", ref, "promoted", rec.ID)
	}
	for _, ref := range rec.Memory.Parked {
		add("memory", ref, "parked", rec.ID)
	}
	for _, ref := range rec.Memory.Rejected {
		add("memory", ref, "rejected", rec.ID)
	}
	for _, ref := range rec.ProofRefs {
		if strings.HasPrefix(ref, "failure:") || strings.HasPrefix(ref, "failure_pattern:") || strings.HasPrefix(ref, "rule:") || strings.HasPrefix(ref, "check:") || strings.HasPrefix(ref, "guard:") {
			add("rule", ref, "proof", rec.ID)
			continue
		}
		add("control_plane", ref, "proof", rec.ID)
	}
	return out
}

func (s *Store) readJSON(path string, v any) error {
	if strings.HasPrefix(path, "compound://events/") {
		return s.readEventJSON(path, v)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not found: %s: %w", filepath.Base(path), err)
		}
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) readEventJSON(ref string, v any) error {
	record, err := s.events.Read(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			id := ref
			if parsed, parseErr := eventstore.ParseReference(ref); parseErr == nil {
				id = parsed.ID()
			}
			return fmt.Errorf("not found: %s.json: %w", id, err)
		}
		return err
	}
	return json.Unmarshal(record.Bytes, v)
}

func (s *Store) readLease(workID string) (Lease, error) {
	var l Lease
	err := s.readJSON(s.leasePath(workID), &l)
	return l, err
}

// ListLeases returns the read-only lease projection for claimed work. The work
// package owns the lease files; operator surfaces should consume this projection
// instead of reading COMPOUND_HOME/work/leases directly.
func (s *Store) ListLeases() ([]Lease, error) {
	dir := filepath.Join(s.home, "work", "leases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	leases := make([]Lease, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var lease Lease
		if err := s.readJSON(filepath.Join(dir, entry.Name()), &lease); err != nil {
			return nil, err
		}
		if strings.TrimSpace(lease.WorkID) == "" {
			lease.WorkID = strings.TrimSuffix(entry.Name(), ".json")
		}
		leases = append(leases, lease)
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].ClaimAt == leases[j].ClaimAt {
			return leases[i].WorkID < leases[j].WorkID
		}
		return leases[i].ClaimAt < leases[j].ClaimAt
	})
	return leases, nil
}

var writeWorkEvent = func(file *os.File, data []byte) (int, error) { return file.Write(data) }

func (s *Store) appendEvent(e Event) error {
	return s.withWorkEventLock(func() error {
		if _, err := s.readEventsStrictUnlocked(""); err != nil {
			return err
		}
		return s.appendEventUnlocked(e)
	})
}

func (s *Store) appendEventUnlocked(e Event) error {
	b, err := marshalWorkEvent(e)
	if err != nil {
		return err
	}
	// The caller holds the stable sidecar lock before eventstore opens the
	// stream, so controlled publication cannot replace the inode while waiting.
	return s.events.AppendWorkLog(b, writeWorkEventThrough)
}

// appendEventControlled publishes one JSONL append by atomically replacing the
// canonical log. The receipted-action transaction uses this rarer O(n) path so
// a killed writer can leave only a disposable temp file, never a torn canonical
// tail. beforePublish runs after the temp file is durable but before rename;
// afterPublish runs after rename and directory sync.
func (s *Store) appendEventControlled(e Event, beforePublish, afterPublish func() error) error {
	return s.withWorkEventLock(func() error {
		return s.appendEventControlledUnlocked(e, beforePublish, afterPublish)
	})
}

func (s *Store) appendEventControlledUnlocked(e Event, beforePublish, afterPublish func() error) error {
	b, err := marshalWorkEvent(e)
	if err != nil {
		return err
	}
	current, err := s.events.ReadWorkLog()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := decodeAdmissionEvents(current, ""); err != nil {
		return err
	}
	return s.events.ReplaceWorkLog(current, b, eventstore.WorkLogReplaceOptions{
		WriteSuffix:   writeWorkEventThrough,
		BeforePublish: beforePublish,
		AfterPublish:  afterPublish,
	})
}

func marshalWorkEvent(e Event) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func (s *Store) withWorkEventLock(fn func() error) error {
	if s.readOnly {
		return s.events.WithWorkLogReadLock(fn)
	}
	return s.events.WithWorkLogLock(fn)
}

func writeWorkEventThrough(writer io.Writer, data []byte) (int, error) {
	file, ok := writer.(*os.File)
	if !ok {
		return 0, fmt.Errorf("work event writer is %T, want *os.File", writer)
	}
	return writeWorkEvent(file, data)
}

func (s *Store) eventsFor(workID string) ([]Event, error) {
	var events []Event
	err := s.withWorkEventLock(func() error {
		var err error
		events, err = s.readEventsStrictUnlocked(workID)
		return err
	})
	return events, err
}

func (s *Store) readEventsStrictUnlocked(workID string) ([]Event, error) {
	s.eventLogReads.Add(1)
	b, err := s.events.ReadWorkLog()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return decodeAdmissionEvents(b, workID)
}

// eventsByWorkID reads and strictly decodes the whole event log ONCE, under
// the same work-event lock as eventsFor, and groups the result by work_id.
// It exists for batch callers (the workbench report build) that would
// otherwise call eventsFor once per work item and pay a full log
// re-read+re-decode per item; eventsFor stays the correct call for existing
// single-work callers (dispatch, continue, show) and is untouched.
func (s *Store) eventsByWorkID() (map[string][]Event, error) {
	var all []Event
	err := s.withWorkEventLock(func() error {
		var err error
		all, err = s.readAllEventsStrictUnlocked()
		return err
	})
	if err != nil {
		return nil, err
	}
	byWorkID := make(map[string][]Event, len(all))
	for _, e := range all {
		byWorkID[e.WorkID] = append(byWorkID[e.WorkID], e)
	}
	return byWorkID, nil
}

func (s *Store) readAllEventsStrictUnlocked() ([]Event, error) {
	s.eventLogReads.Add(1)
	b, err := s.events.ReadWorkLog()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return decodeAdmissionEvents(b, "")
}

// --- small helpers ---

func newHeader(sv, id string, now time.Time, workID, contextPackID, privacy, risk string) envelope.Header {
	return newHeaderWithLane(sv, id, now, workID, contextPackID, privacy, risk, "")
}

// newHeaderWithLane is newHeader plus a declared lane id stamped into Producer.LaneID. laneID
// is a canonical agent-lane node id (resolved via capgraph.ResolveLaneRef) or empty. With
// laneID=="" the Producer is byte-identical to newHeader's (lane_id is omitempty), so every
// existing newHeader call site is unchanged; only the close-minted Receipt/Transition pass a
// non-empty lane, and only when one was declared on the lifecycle (no-guessing, B0).
func newHeaderWithLane(sv, id string, now time.Time, workID, contextPackID, privacy, risk, laneID string) envelope.Header {
	return envelope.Header{
		SchemaVersion: sv,
		ID:            id,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Producer:      envelope.Producer{Kind: "cli", Name: "compound", LaneID: laneID},
		IDs:           envelope.IDs{WorkID: workID, ContextPackID: contextPackID},
		Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       orDefault(privacy, "private"),
		Risk:          orDefault(risk, "tier1"),
		EvidenceRefs:  []string{},
		Uncertainty:   envelope.Uncertainty{Level: "known"},
	}
}

// closeLaneID resolves the lane stamped onto close-minted receipts/transitions. Precedence:
// an explicit in.LaneID override (resolver-gated, fail-closed) wins; otherwise the DECLARED
// lane recorded on the work's lease at claim/advance time (already a canonical node id). No
// override and no lease-declared lane → empty. Never derived from actor/owner/harness.
func (s *Store) closeLaneID(workID, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return s.resolveDeclaredLane(override)
	}
	lease, err := s.readLease(workID)
	if err != nil {
		return "", nil // no lease = no declared lane = empty (no-guessing)
	}
	return strings.TrimSpace(lease.LaneID), nil
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func ts(now time.Time) string { return strconv.FormatInt(now.UTC().UnixNano(), 10) }

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// surpriseOnClose diffs a staked WorkPrediction (envelope.WorkEnvelope.Prediction) against the
// actual close, mirroring shouldHardenOnClose as a small pure close-time helper. Nil when the
// work staked no prediction. When staked, it ALWAYS returns a signal -- Diverged=false (a
// confirmed prediction) is signal too, not merely the absence of a divergence flag. A prediction
// dimension left unstaked (empty MoveType) is not compared and can never itself cause
// Diverged=true.
func surpriseOnClose(w envelope.WorkEnvelope, in CloseInput) *envelope.SurpriseSignal {
	p := w.Prediction
	if p == nil {
		return nil
	}
	diverged := p.Outcome != in.Outcome
	if !diverged && p.MoveType != "" {
		diverged = p.MoveType != in.MoveType
	}
	return &envelope.SurpriseSignal{
		PredictedOutcome:  p.Outcome,
		PredictedMoveType: p.MoveType,
		ActualOutcome:     in.Outcome,
		ActualMoveType:    in.MoveType,
		Diverged:          diverged,
	}
}

// shouldHardenOnClose reports whether a close should stamp
// SecondOrder.ShouldHarden=true. It is true only on an ACCEPTED close (outcome
// "completed") of an explicit harness-edit envelope — one that declared a
// TargetSurface (EVOLVE-BLOCK) — AND that close carries a bench-compare ACCEPT
// proof ref, so "completed" cannot self-certify a harness edit. A rejected/failed/
// cancelled close of the same envelope stamps false but still emits its Receipt +
// Transition, so "rejections are logged without changing the active harness." An
// ordinary work (no TargetSurface) never hardens on close. Derived, not a new
// CloseInput flag: the close outcome already carries whether the harness edit was
// accepted; the proof ref carries whether the gate actually accepted it.
func shouldHardenOnClose(targetSurface, outcome string, proofRefs []string) bool {
	return strings.TrimSpace(targetSurface) != "" &&
		outcome == "completed" &&
		hasBenchCompareAcceptProof(proofRefs)
}

// hasBenchCompareAcceptProof reports whether the close's proof refs contain a
// bench-compare accept record. Accepted spellings include bench_compare_accept:<id>
// and bench_compare:accept:<id>; matched case-insensitively on a ref that contains
// both "bench_compare" and "accept". A plain bench_report:... ref does not qualify —
// the harness edit must prove the gate accepted, not merely that a report exists.
func hasBenchCompareAcceptProof(refs []string) bool {
	for _, r := range refs {
		r = strings.ToLower(strings.TrimSpace(r))
		if strings.Contains(r, "bench_compare") && strings.Contains(r, "accept") {
			return true
		}
	}
	return false
}

// failureCause builds the optional causal-failure annotation; nil when both
// fields are empty so the receipt stays byte-identical to pre-field behavior.
func failureCause(surfaceOutcome, mechanism string) *envelope.FailureCause {
	surfaceOutcome = strings.TrimSpace(surfaceOutcome)
	mechanism = strings.TrimSpace(mechanism)
	if surfaceOutcome == "" && mechanism == "" {
		return nil
	}
	return &envelope.FailureCause{SurfaceOutcome: surfaceOutcome, Mechanism: mechanism}
}
