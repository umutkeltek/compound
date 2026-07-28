package runattempt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status enumerates the lifecycle of a RunAttempt. S1 only ever produces "pending" /
// "running" / "succeeded" / "failed"; later slices add heartbeat-driven transitions and
// terminal proof attachment. C1 adds "abandoned" for claim-release (claimed→ready):
// the attempt was minted at claim but the work was released without execution.
// Abandoned is terminal (same as succeeded/failed) — a released attempt cannot be
// re-entered; a fresh claim mints a fresh RunAttempt.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusAbandoned = "abandoned"
	// StatusSuperseded is the terminal status of a claim_reservation placeholder
	// that a real harness_execution replaced (F2, supersede-at-attach). It is NOT
	// a failure: it is the ordinary success path of the reservation protocol.
	//
	// It exists because "abandoned" was carrying both meanings, and the benign one
	// is ~10x more common: on a live home 330 of 400 sampled abandoned attempts
	// were superseded placeholders and only 38 were harness_execution attempts
	// that actually died. A ledger that is 83% benign-labelled-as-failure trains
	// its readers to ignore the label, which is how a genuinely dead controller
	// sat unnoticed holding 183 leases.
	StatusSuperseded = "superseded"
)

// Origin records which of the three RunAttempt producers created this record:
// a claim-time placeholder reservation, a harness that actually executed, or
// a post-hoc observation of external evidence (e.g. a codex job file). Empty
// on pre-F2 records -- additive field, no migration (omitempty keeps old
// records marshaling byte-identical). F2 (supersede-at-attach) reads Origin
// to terminalize a stale claim_reservation attempt when a harness_execution
// attempt attaches to the same work envelope; attempts with empty Origin
// (legacy) are never touched by that logic.
const (
	OriginClaimReservation = "claim_reservation"
	OriginHarnessExecution = "harness_execution"
	OriginObservedExternal = "observed_external"
)

// HarnessPlaceholder is the stand-in harness identifier used by writers that do not yet have a
// real harness adapter (today: panel.Apply). It is a named constant rather than an open string so
// the future harness-adapter slice has a single replacement site; the deferred context_pack_ref /
// workspace / command / heartbeat / transcript_ref / diff_ref / test_ref / proof_refs fields
// (see the package doc in id.go) land with that adapter, not before.
const HarnessPlaceholder = "fake"

// ErrNotFound is returned when a Get lookup cannot resolve the requested id.
var ErrNotFound = errors.New("runattempt not found")

// ErrAlreadyBound is returned by AttachActionIntent when the RunAttempt is already bound to a
// DIFFERENT ActionIntent. Attaching the same intent twice is a no-op success (retry-safe); a
// mismatched rebind is a bug, not a refresh.
var ErrAlreadyBound = errors.New("runattempt already bound to a different action_intent_id")

// RunAttempt is the S1 type. Fields are deliberately minimal: enough to bind a RunAttempt to
// a work_id and to record who/what ran and when. Transcript, diff, test, proof refs are not
// part of S1 -- they land when a real harness adapter actually produces them.
//
// S2 (Slice B) adds the eight references the reviewer's full RunAttempt shape called for:
// context_pack_ref / workspace / command / last_heartbeat_at / transcript_ref / diff_ref /
// test_ref / proof_refs. Every new field is omitempty so pre-S2 records marshal byte-identical
// when re-decoded and re-marshaled (pinned by TestRunAttemptHashNeutralityForPreS2Records).
// The setters refuse on a terminal RunAttempt (succeeded|failed) -- completed attempts cannot
// be retroactively rewritten. Today panel.Apply wires three of these (workspace, command,
// proof_refs); the rest wait for a real harness adapter to populate them.
type RunAttempt struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	WorkID        string `json:"work_id"`
	// ActionIntentID binds this RunAttempt to the ActionIntent that authorized and represents it,
	// closing the attempt→intent→policy-decision audit chain. Empty on Mint (Mint runs before the
	// Advance that creates the ActionIntent — see panel.Apply); attached post-Advance via
	// AttachActionIntent. omitempty so pre-existing ratt_ records re-marshal byte-identical.
	ActionIntentID string `json:"action_intent_id,omitempty"`
	Actor          string `json:"actor"`
	Harness        string `json:"harness"`
	// Origin is the provenance of this attempt's mint: OriginClaimReservation,
	// OriginHarnessExecution, or OriginObservedExternal. Empty on pre-F2
	// records (backward compat; omitempty so they re-marshal byte-identical).
	Origin      string    `json:"origin,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`

	// --- S2 fields (every one omitempty; setters refuse on terminal status) ---

	// ContextPackRef is the id of the ContextRequest envelope the harness loaded for this
	// attempt. Set before the first side effect so the audit can trace which context pack
	// produced which decision.
	ContextPackRef string `json:"context_pack_ref,omitempty"`
	// Workspace is the filesystem root the harness ran inside (repo path, scratch dir, etc.).
	Workspace string `json:"workspace,omitempty"`
	// Command is the actual command or function call the harness executed. Free-text by design;
	// the audit reads it as opaque context, not as a re-runnable spec.
	Command string `json:"command,omitempty"`
	// LastHeartbeatAt is the latest in-progress activity timestamp. Set by Heartbeat, which
	// advances only forward (a stale heartbeat is a no-op). Combined with Status it tells an
	// operator whether a still-"running" RunAttempt is genuinely progressing or has stalled.
	// Pointer so `omitempty` actually works -- encoding/json treats `time.Time{}` (zero value)
	// as non-empty because it is a struct. Caught by TestRunAttemptHashNeutralityForPreS2Records
	// when this was a value field.
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	// Progress is cumulative supervised-work progress. Completed buckets only
	// move upward and Remaining only moves downward, so out-of-order workers
	// cannot make an operator dashboard regress.
	Progress *RunProgress `json:"progress,omitempty"`
	// TranscriptRef is the path (relative to COMPOUND_HOME) of the harness's run transcript.
	// For panel.Apply this is the panel run record; for a real adapter it is the model's
	// output stream.
	TranscriptRef string `json:"transcript_ref,omitempty"`
	// DiffRef is the path of the diff artifact (if any) the attempt produced.
	DiffRef string `json:"diff_ref,omitempty"`
	// TestRef is the path of the test-result artifact (if any) the attempt produced.
	TestRef string `json:"test_ref,omitempty"`
	// ProofRefs are the paths or ids of proof artifacts (receipts, transitions, external
	// verifications) that corroborate the attempt's outcome. Empty slice clears (intentional,
	// see TestRunAttemptSetProofRefsClearsOnEmpty).
	ProofRefs []string `json:"proof_refs,omitempty"`

	// SweepReason records WHY a sweep terminalized this attempt (C3). Set only by
	// SweepStale / CompleteWithReason; empty on normally-completed attempts.
	// omitempty so pre-C3 records re-marshal byte-identical.
	SweepReason string `json:"sweep_reason,omitempty"`

	// PredecessorID links this attempt to the one it retries (C4: retry
	// lineage). When a work is re-claimed after a failed/abandoned attempt,
	// the new attempt's PredecessorID points to the previous one, forming a
	// chain. Empty on the first attempt. omitempty so pre-C4 records
	// re-marshal byte-identical. The work state machine is untouched —
	// this is evidence, not enforcement.
	PredecessorID string `json:"predecessor_id,omitempty"`

	// --- S3 fields (every one omitempty; setters refuse on terminal status) ---

	// LaneID is the adapter lane / actor identity that drove this run (e.g.
	// "codex", "claude-zai"). Stamped by the dispatching context only where it
	// actually knows the lane (observe-run flags today). omitempty so pre-S3
	// records re-marshal byte-identical (pinned by the pre-S3 case of
	// TestRunAttemptHashNeutralityForPreS2Records).
	LaneID string `json:"lane_id,omitempty"`
	// RequestedModelID and RequestedProviderRoute capture the Compound lane
	// tuple selected before launch. They are dispatch intent, not evidence that
	// a concrete provider/model consumed the run.
	RequestedModelID       string `json:"requested_model_id,omitempty"`
	RequestedProviderRoute string `json:"requested_provider_route,omitempty"`
	// ExecutionProfilePlanID and the requested client/transport/effort fields
	// identify the exact semantic execution plan selected before launch. They
	// are intent, not observed route evidence, and therefore never populate
	// ModelID or ProviderRoute.
	ExecutionProfilePlanID   string `json:"execution_profile_plan_id,omitempty"`
	RequestedClientHarnessID string `json:"requested_client_harness_id,omitempty"`
	RequestedTransportID     string `json:"requested_transport_id,omitempty"`
	RequestedNativeEffort    string `json:"requested_native_effort,omitempty"`
	// ModelID is the concrete model the run consumed (e.g. "model-x"). Empty
	// when the dispatching context does not know the model -- never guessed.
	ModelID string `json:"model_id,omitempty"`
	// ProviderRoute is the provider route / endpoint family the run used (e.g.
	// "provider-y"). Empty when unknown. omitempty so pre-S3 records re-marshal
	// byte-identical.
	ProviderRoute string `json:"provider_route,omitempty"`
	// TokensIn is the prompt token usage. Zero ≡ unknown (the dispatching
	// context did not report it). Plain int64 (not pointer) is sufficient:
	// encoding/json omits the zero value for numbers, so omitempty works
	// without the *time.Time workaround LastHeartbeatAt needs.
	TokensIn int64 `json:"tokens_in,omitempty"`
	// TokensOut is the completion token usage. Zero ≡ unknown; see TokensIn.
	// omitempty so pre-S3 records re-marshal byte-identical.
	TokensOut int64 `json:"tokens_out,omitempty"`
}

// RunProgress is the reduced, content-free progress shape shared by
// delegated workers, the CLI, and projections. It deliberately carries no
// prompt, filenames, or model output.
type RunProgress struct {
	Done      int `json:"done"`
	Empty     int `json:"empty"`
	Failed    int `json:"failed"`
	Remaining int `json:"remaining"`
}

// Store persists RunAttempts under <home>/run_attempts/<id>.json with atomic rename and a
// per-process index cache so multiple Get calls in one tick don't re-read disk.
type Store struct {
	home string

	mu    sync.RWMutex
	cache map[string]RunAttempt
}

// Open returns a Store rooted at home. It does NOT create directories -- Get / Complete
// lazily mkdir on first write, matching the pattern used by internal/landlease and
// internal/work so a fresh home stays clean until something is actually persisted.

// Home returns the home path the Store was opened with. Useful for callers
// that need to colocate side artifacts (e.g. a transcript) with the run
// JSON files. Pinned by S3 (harness adapter) consumers.
func (s *Store) Home() string { return s.home }
func Open(home string) *Store {
	return &Store{home: home, cache: map[string]RunAttempt{}}
}

// Mint creates and persists a new RunAttempt bound to workID, returning the stored artifact.
// The id format is enforced by Valid -- if Mint ever produces a non-conforming id (regression
// guard), the validator will reject it before it reaches the envelope.
func (s *Store) Mint(workID, actor, harness string, now time.Time) (RunAttempt, error) {
	if strings.TrimSpace(workID) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: work_id is required")
	}
	if strings.TrimSpace(actor) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: actor is required")
	}
	if strings.TrimSpace(harness) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: harness is required")
	}
	id, err := Mint(now)
	if err != nil {
		return RunAttempt{}, err
	}
	ra := RunAttempt{
		SchemaVersion: SchemaVersion,
		ID:            id,
		WorkID:        workID,
		Actor:         actor,
		Harness:       harness,
		Status:        StatusPending,
		CreatedAt:     now.UTC(),
	}
	if err := s.write(ra); err != nil {
		return RunAttempt{}, err
	}
	return ra, nil
}

// MintWithID creates a RunAttempt with a caller-supplied id (must pass
// Valid). If a record with that id already exists on disk, it is returned
// unchanged (idempotent create-or-retrieve). This is the deterministic
// counterpart to Mint: callers that derive an id from a stable seed (e.g.
// MintDeterministic(jobID, now)) get idempotency without a dedup index.
func (s *Store) MintWithID(id, workID, actor, harness string, now time.Time) (RunAttempt, error) {
	if !Valid(id) {
		return RunAttempt{}, fmt.Errorf("runattempt mint: id %q does not match %s pattern", id, Prefix)
	}
	if strings.TrimSpace(workID) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: work_id is required")
	}
	if strings.TrimSpace(actor) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: actor is required")
	}
	if strings.TrimSpace(harness) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: harness is required")
	}
	// Idempotent: if the record already exists AND matches the caller's
	// identity, return it. A mismatch is fail-closed (F4): the deterministic
	// id (e.g. from a codex job id) is globally meaningful, so a different
	// work/actor/harness reusing the same id is a split-truth bug, not a
	// legitimate idempotent retrieve.
	if existing, err := s.Get(id); err == nil {
		if existing.WorkID != workID {
			return RunAttempt{}, fmt.Errorf("run attempt %s already belongs to work %s (caller: %s)", id, existing.WorkID, workID)
		}
		if existing.Actor != actor {
			return RunAttempt{}, fmt.Errorf("run attempt %s already belongs to actor %s (caller: %s)", id, existing.Actor, actor)
		}
		if existing.Harness != harness {
			return RunAttempt{}, fmt.Errorf("run attempt %s already belongs to harness %s (caller: %s)", id, existing.Harness, harness)
		}
		return existing, nil
	}
	ra := RunAttempt{
		SchemaVersion: SchemaVersion,
		ID:            id,
		WorkID:        workID,
		Actor:         actor,
		Harness:       harness,
		Status:        StatusPending,
		CreatedAt:     now.UTC(),
	}
	if err := s.write(ra); err != nil {
		return RunAttempt{}, err
	}
	return ra, nil
}

// Get returns the RunAttempt with the given id, loading from disk if not yet cached.
func (s *Store) Get(id string) (RunAttempt, error) {
	if !Valid(id) {
		return RunAttempt{}, fmt.Errorf("runattempt get: id %q does not match %s pattern", id, Prefix)
	}
	s.mu.RLock()
	if ra, ok := s.cache[id]; ok {
		s.mu.RUnlock()
		return ra, nil
	}
	s.mu.RUnlock()
	path := s.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RunAttempt{}, ErrNotFound
		}
		return RunAttempt{}, fmt.Errorf("runattempt get %s: %w", id, err)
	}
	var ra RunAttempt
	if err := json.Unmarshal(data, &ra); err != nil {
		return RunAttempt{}, fmt.Errorf("runattempt get %s: decode: %w", id, err)
	}
	s.mu.Lock()
	s.cache[id] = ra
	s.mu.Unlock()
	return ra, nil
}

// IsTerminal reports whether status is a terminal RunAttempt status
// (succeeded, failed, or abandoned). Terminal attempts cannot be mutated,
// re-completed, or re-entered.
func IsTerminal(status string) bool {
	return status == StatusSucceeded || status == StatusFailed ||
		status == StatusAbandoned || status == StatusSuperseded
}

// Complete transitions a RunAttempt to terminal status (succeeded, failed, or
// abandoned) and records the completion timestamp. Pending and running are not
// terminal and Complete refuses them. C1 adds abandoned for claim-release
// (claimed→ready): the attempt was minted at claim but the work was released
// without execution.
func (s *Store) Complete(id string, status string, now time.Time) (RunAttempt, error) {
	if !IsTerminal(status) {
		return RunAttempt{}, fmt.Errorf("runattempt complete %s: status %q is not terminal", id, status)
	}
	ra, err := s.Get(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if IsTerminal(ra.Status) {
		return RunAttempt{}, fmt.Errorf("runattempt complete %s: already terminal (%s)", id, ra.Status)
	}
	ra.Status = status
	ra.CompletedAt = now.UTC()
	if err := s.write(ra); err != nil {
		return RunAttempt{}, err
	}
	return ra, nil
}

// CompleteWithReason transitions a RunAttempt to terminal status and records
// WHY. Used by SweepStale (C3) to leave an audit trail: a swept attempt
// carries sweep_reason="stale: no heartbeat for 30m" (or similar) so an
// operator reading the JSON can distinguish a normally-completed attempt from
// one the sweeper collected. The reason is recorded in SweepReason (omitempty,
// hash-neutral). Refuses if already terminal, same as Complete.
func (s *Store) CompleteWithReason(id, status, reason string, now time.Time) (RunAttempt, error) {
	if !IsTerminal(status) {
		return RunAttempt{}, fmt.Errorf("runattempt complete %s: status %q is not terminal", id, status)
	}
	ra, err := s.Get(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if IsTerminal(ra.Status) {
		return RunAttempt{}, fmt.Errorf("runattempt complete %s: already terminal (%s)", id, ra.Status)
	}
	ra.Status = status
	ra.CompletedAt = now.UTC()
	ra.SweepReason = strings.TrimSpace(reason)
	if err := s.write(ra); err != nil {
		return RunAttempt{}, err
	}
	return ra, nil
}

// MarkRunning transitions a RunAttempt from pending to running (F5). This is
// the setter observe-run's "running"/"in_progress" job-status branch was
// missing -- without it a running job's RunAttempt silently stayed pending
// forever, contradicting the Status doc comment. Terminal-guarded like every
// other setter (refuses if already succeeded/failed/abandoned); idempotent
// no-op if already running -- repeated "still running" observations of the
// same job must not error or reset CreatedAt/heartbeat state.
func (s *Store) MarkRunning(id string, now time.Time) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if ra.Status == StatusRunning {
		return ra, nil
	}
	ra.Status = StatusRunning
	return ra, s.write(ra)
}

// AttachActionIntent binds a RunAttempt to the ActionIntent that authorized it, closing the
// attempt→intent→policy-decision audit chain (the RunAttempt otherwise binds to WorkID only).
//
// It is separate from Complete on purpose: binding is retry-idempotent and may run more than once
// (panel.Apply recovers the intent id from the advanced envelope's last ActionIntentIDs element),
// whereas Complete is a one-shot terminal transition. Mint stays before Advance (panel/apply.go),
// preserving the intentional ordering that puts the run id on the envelope before any artifact is
// written; AttachActionIntent is the post-Advance step that adds the binding.
//
// Behavior:
//   - id must be a Valid ratt_ id (else a wrapped error).
//   - actionIntentID must have the act_ prefix (mirrors the ratt_ rule philosophy).
//   - If the RunAttempt is already bound to the SAME actionIntentID, this is a no-op success.
//   - If it is bound to a DIFFERENT actionIntentID, returns ErrAlreadyBound.
//   - If the RunAttempt is already terminal (succeeded/failed), returns an error — binding after
//     terminalization is a bug.
//
// Known limitation: a single Advance carrying multiple RunAttemptIDs would bind all of them to
// the same act_ id. Today only panel.Apply writes RunAttempts and it mints one per Advance, so
// this is a non-issue; revisit when a real multi-attempt harness adapter lands.
func (s *Store) AttachActionIntent(id, actionIntentID string) error {
	if !Valid(id) {
		return fmt.Errorf("runattempt attach %s: id does not match %s pattern", id, Prefix)
	}
	if !strings.HasPrefix(actionIntentID, "act_") {
		return fmt.Errorf("runattempt attach %s: action_intent_id %q must start with act_", id, actionIntentID)
	}
	ra, err := s.Get(id)
	if err != nil {
		return err
	}
	if IsTerminal(ra.Status) {
		return fmt.Errorf("runattempt attach %s: already terminal (%s); binding after terminalization is a bug", id, ra.Status)
	}
	if ra.ActionIntentID == actionIntentID {
		return nil // idempotent retry
	}
	if ra.ActionIntentID != "" {
		return fmt.Errorf("runattempt attach %s: %w (have %q, want %q)", id, ErrAlreadyBound, ra.ActionIntentID, actionIntentID)
	}
	ra.ActionIntentID = actionIntentID
	return s.write(ra)
}

// ---------------------------------------------------------------------------
// S2: RunAttempt full-shape accessors
//
// Every setter follows the same pattern: Get the record (which validates the
// id shape and returns ErrNotFound on miss), refuse if the RunAttempt is
// terminal (StatusSucceeded | StatusFailed), update the field, atomic write.
// A helper updateField carries the refusal logic so each setter stays a
// single line of intent.
// ---------------------------------------------------------------------------

// terminalRefusal is the error returned by every S2 setter when the target
// RunAttempt is already terminal (succeeded, failed, or abandoned). A terminal
// attempt cannot be retroactively rewritten -- that is the audit invariant the
// refusal pins.
var terminalRefusal = errors.New("runattempt is terminal; cannot mutate")

func (s *Store) requireMutable(id string) (RunAttempt, error) {
	if !Valid(id) {
		return RunAttempt{}, fmt.Errorf("runattempt set %s: id does not match %s pattern", id, Prefix)
	}
	ra, err := s.Get(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if IsTerminal(ra.Status) {
		return RunAttempt{}, fmt.Errorf("runattempt %s: %w (status=%s)", id, terminalRefusal, ra.Status)
	}
	return ra, nil
}

// SetOrigin stamps the provenance of an attempt's mint (F2 groundwork). Called
// once, immediately after Mint/MintWithID at each of the three producer call
// sites (claim, harness execution, observe-run), before any other state
// change. Refuses on terminal exactly like every other S2/S3 setter, via
// requireMutable, so provenance can never be rewritten after the fact.
func (s *Store) SetOrigin(id, origin string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.Origin = strings.TrimSpace(origin)
	return ra, s.write(ra)
}

// SetContextPack records which ContextRequest envelope this attempt loaded.
// Pinned by TestRunAttemptRoundTripsNewFields/context_pack and refused on
// terminal attempts by TestRunAttemptSettersRefuseOnTerminal.
func (s *Store) SetContextPack(id, ref string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.ContextPackRef = strings.TrimSpace(ref)
	return ra, s.write(ra)
}

// SetWorkspace records the filesystem root the attempt ran inside. Free-text
// by design; the audit reads it as opaque context.
func (s *Store) SetWorkspace(id, workspace string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.Workspace = strings.TrimSpace(workspace)
	return ra, s.write(ra)
}

// SetCommand records the actual command or function call the harness
// executed. Treated as opaque context, not a re-runnable spec.
func (s *Store) SetCommand(id, command string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.Command = strings.TrimSpace(command)
	return ra, s.write(ra)
}

// Heartbeat advances LastHeartbeatAt to now, but ONLY if now is strictly
// later than the current value. Stale heartbeats are no-ops. The monotonic
// rule is pinned by TestRunAttemptHeartbeatAdvancesOnlyForward. Heartbeat
// also refuses on terminal attempts -- a heartbeat on a "succeeded" run
// would imply the attempt is still running, contradicting the terminal status.
func (s *Store) Heartbeat(id string, now time.Time) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	t := now.UTC()
	if ra.LastHeartbeatAt != nil && !t.After(*ra.LastHeartbeatAt) {
		return ra, nil
	}
	ra.LastHeartbeatAt = &t
	return ra, s.write(ra)
}

// HeartbeatProgress advances liveness and cumulative progress atomically.
// Completed buckets may only increase and Remaining may only decrease.
func (s *Store) HeartbeatProgress(id string, now time.Time, progress RunProgress) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if progress.Done < 0 || progress.Empty < 0 || progress.Failed < 0 || progress.Remaining < 0 {
		return RunAttempt{}, errors.New("runattempt progress counts must be non-negative")
	}
	if previous := ra.Progress; previous != nil {
		if progress.Done < previous.Done || progress.Empty < previous.Empty || progress.Failed < previous.Failed || progress.Remaining > previous.Remaining {
			return RunAttempt{}, fmt.Errorf("runattempt progress must be monotonic: previous=%+v next=%+v", *previous, progress)
		}
	}
	t := now.UTC()
	if ra.LastHeartbeatAt != nil && t.Before(*ra.LastHeartbeatAt) {
		return RunAttempt{}, fmt.Errorf("runattempt progress heartbeat is stale: previous=%s next=%s", ra.LastHeartbeatAt.UTC().Format(time.RFC3339Nano), t.Format(time.RFC3339Nano))
	}
	ra.LastHeartbeatAt = &t
	copy := progress
	ra.Progress = &copy
	return ra, s.write(ra)
}

// SetTranscript records the run transcript path. Single string by design --
// a harness has one transcript.
func (s *Store) SetTranscript(id, ref string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.TranscriptRef = strings.TrimSpace(ref)
	return ra, s.write(ra)
}

// SetDiff records the diff artifact path (if any).
func (s *Store) SetDiff(id, ref string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.DiffRef = strings.TrimSpace(ref)
	return ra, s.write(ra)
}

// SetTest records the test-result artifact path (if any).
func (s *Store) SetTest(id, ref string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.TestRef = strings.TrimSpace(ref)
	return ra, s.write(ra)
}

// SetProofRefs replaces the proof-refs slice. Pass nil to clear (pinned by
// TestRunAttemptSetProofRefsClearsOnEmpty). Pass a non-empty slice to
// overwrite. Order is preserved as-given; the audit does not sort.
func (s *Store) SetProofRefs(id string, refs []string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if len(refs) == 0 {
		ra.ProofRefs = nil
	} else {
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			r = strings.TrimSpace(r)
			if r != "" {
				out = append(out, r)
			}
		}
		ra.ProofRefs = out
	}
	return ra, s.write(ra)
}

// ---------------------------------------------------------------------------
// S3: model / provider / lane / token attribution
//
// SetAttribution and SetTokenUsage follow the exact pattern of every S2
// setter above: Get (which validates the id), refuse on terminal via the
// shared requireMutable helper (same terminalRefusal error wording as
// SetContextPack), update, atomic write. A terminal attempt cannot be
// retroactively re-attributed -- that is the same audit invariant the S2
// setters pin.
// ---------------------------------------------------------------------------

// SetDispatchIntent records the known execution lane plus the model-router
// tuple requested before launch. It deliberately does not populate ModelID or
// ProviderRoute: only observed route evidence may attest those fields.
func (s *Store) SetDispatchIntent(id, laneID, requestedModelID, requestedProviderRoute string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.LaneID = strings.TrimSpace(laneID)
	ra.RequestedModelID = strings.TrimSpace(requestedModelID)
	ra.RequestedProviderRoute = strings.TrimSpace(requestedProviderRoute)
	return ra, s.write(ra)
}

// SetExecutionProfileIntent records the exact requested semantic execution
// identity before the harness is spawned. It deliberately leaves observed
// ModelID and ProviderRoute empty; those fields require route evidence.
func (s *Store) SetExecutionProfileIntent(id, planID, clientHarnessID, transportID, nativeEffort string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.ExecutionProfilePlanID = strings.TrimSpace(planID)
	ra.RequestedClientHarnessID = strings.TrimSpace(clientHarnessID)
	ra.RequestedTransportID = strings.TrimSpace(transportID)
	ra.RequestedNativeEffort = strings.TrimSpace(nativeEffort)
	return ra, s.write(ra)
}

// SetAttribution records which lane / model / provider route drove this run.
// Empty strings are valid (the dispatching context did not know that
// dimension); the omitempty tags drop them from the JSON, so passing all-empty
// changes nothing on a fresh mint. Stamped by the dispatching context only
// where it actually knows the values -- never guessed.
func (s *Store) SetAttribution(id, laneID, modelID, providerRoute string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.LaneID = strings.TrimSpace(laneID)
	ra.ModelID = strings.TrimSpace(modelID)
	ra.ProviderRoute = strings.TrimSpace(providerRoute)
	return ra, s.write(ra)
}

// SetTokenUsage records the prompt / completion token counts. Zero ≡ unknown
// (see the TokensIn field comment); pass the counts the dispatching context
// actually observed. Refuses on terminal, same as every S2/S3 setter.
func (s *Store) SetTokenUsage(id string, in, out int64) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	ra.TokensIn = in
	ra.TokensOut = out
	return ra, s.write(ra)
}

// ---------------------------------------------------------------------------
// C4: retry lineage — PredecessorID
//
// When a work is re-claimed after a failed/abandoned attempt, the new
// attempt's PredecessorID points to the previous one, forming a chain.
// The work state machine is untouched — this is evidence, not enforcement.
// NO new edges, NO move_type on Transition (that lives on Receipt).
// ---------------------------------------------------------------------------

// SetPredecessor links this attempt to the one it retries. The predecessor
// must be a valid ratt_ id that exists on disk (you cannot link to a phantom).
// Setting the same predecessor twice is idempotent (retry-safe). Changing to
// a DIFFERENT predecessor is refused (a retry chain is append-only). Refuses
// on terminal attempts (a swept/completed attempt cannot be retroactively
// linked). Pinned by TestSetPredecessorChains and TestSetPredecessorRefuses.
func (s *Store) SetPredecessor(id, predecessorID string) (RunAttempt, error) {
	ra, err := s.requireMutable(id)
	if err != nil {
		return RunAttempt{}, err
	}
	predecessorID = strings.TrimSpace(predecessorID)
	if predecessorID == "" {
		return RunAttempt{}, fmt.Errorf("runattempt set-predecessor %s: predecessor_id is required", id)
	}
	if !Valid(predecessorID) {
		return RunAttempt{}, fmt.Errorf("runattempt set-predecessor %s: predecessor %q does not match %s pattern", id, predecessorID, Prefix)
	}
	// The predecessor must exist on disk — no phantom links.
	if _, err := s.Get(predecessorID); err != nil {
		return RunAttempt{}, fmt.Errorf("runattempt set-predecessor %s: predecessor %s: %w", id, predecessorID, err)
	}
	// Idempotent: same predecessor again is a no-op.
	if ra.PredecessorID == predecessorID {
		return ra, nil
	}
	// Append-only: changing to a different predecessor is a bug.
	if ra.PredecessorID != "" {
		return RunAttempt{}, fmt.Errorf("runattempt set-predecessor %s: already linked to %s (append-only chain)", id, ra.PredecessorID)
	}
	ra.PredecessorID = predecessorID
	return ra, s.write(ra)
}

// ---------------------------------------------------------------------------
// C3: crash terminality — List, ListNonTerminal, SweepStale
//
// A RunAttempt whose producer is gone (PID dead, no heartbeat, job file
// terminal) must not stay pending/running forever. SweepStale walks the
// store, finds non-terminal attempts whose last activity (heartbeat or
// created_at) is older than the threshold, and terminalizes them to
// abandoned with a recorded sweep_reason. Doctor surfaces the stuck count
// until swept. RunAttempt is evidence, not enforcement: sweeping an attempt
// has NO effect on the work state machine.
// ---------------------------------------------------------------------------

// SweepReport is the outcome of one SweepStale pass. Pinned by C3 tests.
type SweepReport struct {
	Scanned    int      `json:"scanned"` // total non-terminal attempts examined
	Swept      int      `json:"swept"`   // terminalized this pass
	Skipped    int      `json:"skipped"` // fresh enough to keep
	SweptIDs   []string `json:"swept_ids,omitempty"`
	SkippedIDs []string `json:"skipped_ids,omitempty"`
	// Protected counts stale candidates whose probe declared live=true
	// and which SweepStaleWithProbe heartbeated without terminalizing.
	// Always 0 for the no-probe SweepStale path; surfaced separately so
	// generic and probe-aware passes share the same on-disk JSON shape
	// for the headline counters.
	Protected int `json:"protected,omitempty"`
}

// DefaultSweepThreshold is the staleness cutoff used when no explicit
// threshold is supplied. A pending/running attempt with no activity for this
// long is considered orphaned. 30 minutes matches the codex job heartbeat
// cadence (the plugin pings every ~5 minutes; six missed pings = dead).
const DefaultSweepThreshold = 30 * time.Minute

// List returns every RunAttempt on disk, sorted by CreatedAt ascending.
// A missing run_attempts/ directory is not an error (fresh home). Torn or
// foreign files are skipped, never fatal — matching the read-only tolerance
// of doctor's contract walks.
func (s *Store) List() ([]RunAttempt, error) {
	dir := filepath.Join(s.home, "run_attempts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("runattempt list: %w", err)
	}
	var out []RunAttempt
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		ra, gerr := s.Get(id)
		if gerr != nil {
			continue // torn or foreign — skip, never fatal
		}
		out = append(out, ra)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// ListNonTerminal returns only pending/running RunAttempts — the set a
// sweep would examine. Sorted by CreatedAt ascending (List order).
func (s *Store) ListNonTerminal() ([]RunAttempt, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []RunAttempt
	for _, ra := range all {
		if !IsTerminal(ra.Status) {
			out = append(out, ra)
		}
	}
	return out, nil
}

// SweepStale terminalizes every non-terminal RunAttempt whose last activity
// is older than threshold. "Last activity" is LastHeartbeatAt when present,
// otherwise CreatedAt (an attempt that never received a heartbeat is judged
// from its birth). Swept attempts transition to abandoned with a
// sweep_reason recording the staleness. Terminal attempts are untouched.
//
// threshold <= 0 uses DefaultSweepThreshold.
//
// The sweep is best-effort per attempt: a write failure on one attempt does
// not abort the pass (the next sweep retries it). RunAttempt is evidence,
// not enforcement — sweeping has NO effect on the work state machine.
func (s *Store) SweepStale(threshold time.Duration, now time.Time) (SweepReport, error) {
	if threshold <= 0 {
		threshold = DefaultSweepThreshold
	}
	stuck, err := s.ListNonTerminal()
	if err != nil {
		return SweepReport{}, err
	}
	var rep SweepReport
	rep.Scanned = len(stuck)
	for _, ra := range stuck {
		lastActivity := ra.CreatedAt
		if ra.LastHeartbeatAt != nil {
			lastActivity = *ra.LastHeartbeatAt
		}
		age := now.Sub(lastActivity)
		if age < threshold {
			rep.Skipped++
			rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
			continue
		}
		reason := fmt.Sprintf("stale: no activity for %s (threshold %s)", age.Round(time.Second), threshold)
		if _, cerr := s.CompleteWithReason(ra.ID, StatusAbandoned, reason, now); cerr != nil {
			// Best-effort: a write failure on one attempt does not abort the
			// pass. The next sweep retries it.
			rep.Skipped++
			rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
			continue
		}
		rep.Swept++
		rep.SweptIDs = append(rep.SweptIDs, ra.ID)
	}
	return rep, nil
}

// LivenessProbe abstracts the behind-the-scenes check a SweepStaleWithProbe
// pass performs before terminalizing a candidate. The contract:
//   - nil probe is byte-identical to SweepStale (see nilProbeMatchesSweepStale).
//   - probe(ra) returns:
//   - live=true → heartbeat refresh and protect (no terminalization).
//   - live=false (vanished/dead/unbound) → ordinary stale abandonment.
//     A prior DelegateSweep that already mapped observed exit would have
//     terminalized this attempt; the generic pass does not duplicate that.
//   - err != nil → SKIP the candidate. Do NOT abandon on a transient probe
//     fault -- the next sweep retries.
//
// The probe is consulted ONLY on stale candidates. Probe receives the
// full RunAttempt so callers can do work-owned binding lookup (the
// runattempt store never persists a SessionName field on disk).
type LivenessProbe func(ra RunAttempt) (live bool, err error)

// SweepStaleWithProbe is the probe-aware sweep. With probe == nil it falls
// through to SweepStale unchanged (same on-disk bytes; this is the assertion
// that preserves compatibility for direct store callers). The CLI always
// supplies the work-owned probe. With probe != nil:
//
//   - probe errors skip the candidate (no terminalization, no heartbeat),
//     so a transient probe fault does not strand an attempt.
//   - probe returns live=true → heartbeat refresh + protect (counts as
//     Skipped in the report, with a Protected++ side-count so callers can
//     distinguish kept-due-to-live from kept-due-to-fresh-heartbeat).
//   - probe returns live=false (panes have died or vanished, OR the
//     candidate has no bound pane -- in which case live defaults to
//     false) → ordinary stale abandonment, identical to SweepStale. A
//     DelegateSweep that has already mapped observed exit for this exact
//     attempt would have terminalized it earlier; here we just release the
//     attempt as abandoned, no kill, no heartbeat.
//
// Probe receives the full RunAttempt so callers can apply work-owned
// binding lookup (e.g. work/delegate.lookupSessionFor(ra)) instead of
// having runattempt guess at a SessionName field. The orchestrator owns
// the seam.
func (s *Store) SweepStaleWithProbe(threshold time.Duration, now time.Time, probe LivenessProbe) (SweepReport, error) {
	if probe == nil {
		return s.SweepStale(threshold, now)
	}
	if threshold <= 0 {
		threshold = DefaultSweepThreshold
	}
	stuck, err := s.ListNonTerminal()
	if err != nil {
		return SweepReport{}, err
	}
	var rep SweepReport
	rep.Scanned = len(stuck)
	for _, ra := range stuck {
		lastActivity := ra.CreatedAt
		if ra.LastHeartbeatAt != nil {
			lastActivity = *ra.LastHeartbeatAt
		}
		age := now.Sub(lastActivity)
		if age < threshold {
			rep.Skipped++
			rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
			continue
		}
		live, perr := probe(ra)
		if perr != nil {
			rep.Skipped++
			rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
			continue
		}
		if live {
			if _, herr := s.Heartbeat(ra.ID, now); herr != nil {
				// Heartbeat-refresh write failures are best-effort per
				// attempt (same shape as the probe-error branch above and
				// the CompleteWithReason branch below): skip this candidate
				// and let later stale candidates be processed by the same
				// pass. Aborting the sweep on a single transient failure
				// would strand every subsequent candidate.
				rep.Skipped++
				rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
				continue
			}
			rep.Protected++
			rep.Skipped++
			rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
			continue
		}
		// Vanished or dead (and not previously swept by DelegateSweep) →
		// ordinary stale abandonment.
		reason := fmt.Sprintf("stale: no activity for %s (threshold %s)", age.Round(time.Second), threshold)
		if _, cerr := s.CompleteWithReason(ra.ID, StatusAbandoned, reason, now); cerr != nil {
			rep.Skipped++
			rep.SkippedIDs = append(rep.SkippedIDs, ra.ID)
			continue
		}
		rep.Swept++
		rep.SweptIDs = append(rep.SweptIDs, ra.ID)
	}
	return rep, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.home, "run_attempts", id+".json")
}

// write persists ra atomically (write to .tmp, fsync, rename) so a partial write cannot
// leave a half-decoded file in place. Same shape as internal/work.eventLogWrites and
// internal/landlease.writeAtomic so a crash mid-write loses at most one RunAttempt.
// C5: the fsync is real now (previously the comment claimed it but the code did not).
// The store is an evidence organ — durability matters.
func (s *Store) write(ra RunAttempt) error {
	dir := filepath.Join(s.home, "run_attempts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("runattempt write: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(ra, "", "  ")
	if err != nil {
		return fmt.Errorf("runattempt write: encode %s: %w", ra.ID, err)
	}
	tmp := s.path(ra.ID) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("runattempt write: create %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("runattempt write: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("runattempt write: fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("runattempt write: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path(ra.ID)); err != nil {
		return fmt.Errorf("runattempt write: rename %s: %w", tmp, err)
	}
	s.mu.Lock()
	s.cache[ra.ID] = ra
	s.mu.Unlock()
	return nil
}
