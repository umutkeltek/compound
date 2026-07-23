// Package-level governed execution substrate (slice 1): `compound work
// delegate|steer|attach`. The pure orchestrator lives here; the thin real
// tmux adapter lives in tmux_runner.go behind the SessionRunner seam.
//
// Design rules pinned by the packet "2026-07-20-governed-execution-delegate-
// steer-attach.md":
//   - NO new gate. Delegate routes through the same LaneCapacityGuard seam
//     dispatch_plan.go uses. A walled lane REFUSES even with --confirm; a
//     confirmation-required lane proceeds only with --confirm. A nil guard
//     fails CLOSED.
//   - tmux, wrapped. SessionRunner is an interface; only tmux_runner.go (in
//     this same allowed-file set) shells out. Pure orchestrator tests never
//     touch a real tmux server.
//   - One meaning owner. The binding (work ↔ tmux session) lives in the
//     EXISTING eventstore as a structured work.delegate.spawned record, NOT
//     as a sidecar file under COMPOUND_HOME. Recovery walks the work's
//     lifecycle attachments newest-first and reads the attached eventstore
//     objects through the same eventstore API. A later work.delegate.exited
//     record kills the binding; steer/attach probe SessionRunner.HasSession
//     regardless. A delegate RunAttempt is minted before the first spawn side
//     effect, marked running only after the session exists, heartbeated while
//     live, and terminalized from an observed pane exit or disappearance.
//   - One live session per work. A second delegate on a live session
//     REFUSES with reason=session_live and the existing session returned.
//   - Lifecycle events, not logs. Each verb emits a structured eventstore
//     record under compound.work_delegate_event.v0.1 with kind ∈
//     work.delegate.{spawned,refused,steered,exited}. Event payloads NEVER
//     carry prompt text or command stdout (privacy law of the packet).
//   - Dry-run runs the guard + binding + tmux-available checks, prints the
//     decision, and writes NO event record (preview-only).
//   - Named lane aliases resolve through Compound's injected LaneTuple
//     resolver. Governed activation admits only an executable attested by the
//     readiness adapter; a raw override remains parseable for compatibility
//     but fails closed unless an authority can attest it.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/runattempt"
)

const (
	delegateEventSchemaVersion = "compound.work_delegate_event.v0.1"
	delegateSpawnFailureNote   = "session runner could not create the governed session"
	delegateAmbiguousNote      = "Create failed but the named session exists; classified as spawn_ambiguous (the session may be a stranger's); no kill performed"
	delegateProbeUnknownNote   = "Create failed and session ownership could not be determined; classified as spawn_ambiguous; no kill performed"
)

type delegateAuthorityChangedError struct {
	detail string
}

func (e *delegateAuthorityChangedError) Error() string {
	return "delegate authority changed during admission: " + e.detail
}

// SessionStatus captures the supervised pane's liveness and (when
// available) its exit code/signal. The orchestrator treats this -- not bare
// Has -- as the source of truth for whether a previously-spawned session
// is still running, because tmux keeps dead sessions around with
// remain-on-exit.
//
// ExitCode is the observed exit status; -1 means the runner has no
// observable value yet (server crash, pane_dead set without metadata, the
// pane is live, etc). The orchestrator treats -1 as "unknown / cannot
// conclude exit" rather than guessing the value 0.
type SessionStatus struct {
	Exists   bool   `json:"exists"`           // session exists in the server's namespace
	PaneDead bool   `json:"pane_dead"`        // pane process is dead (remain-on-exit set)
	ExitCode int    `json:"exit_code"`        // observed exit status; -1 means unknown / not observed
	Signal   string `json:"signal,omitempty"` // non-empty when killed by a signal (e.g. "TERM")
	// UnknownReason is set when the runner cannot determine the status
	// (exec failure, malformed output, permissions, etc). The orchestrator
	// propagates the uncertainty; it never guesses a terminal status.
	UnknownReason string `json:"unknown_reason,omitempty"`
}

// SessionRunner is the seam between the orchestrator and any backing
// supervised-execution backend (tmux in production; a fake in tests).
type SessionRunner interface {
	Available() error
	Has(name string) (bool, error)
	SessionStatus(name string) (SessionStatus, error)
	Create(name, dir, command string, args []string) error
	SendKeys(name, text string, enter bool) error
	Kill(name string) error
	AttachArgv(name string) []string
}

type DelegateOptions struct {
	WorkID          string
	Harness         string
	Session         string // optional override; default = delegateSessionName(WorkID)
	Actor           string
	Command         string
	Args            []string
	Workspace       string
	ExpectedMinutes int
	Confirm         bool
	DryRun          bool
	Now             time.Time
}

type DelegateHarnessResolveRequest struct {
	HomePath        string
	Harness         string
	CommandOverride string
	Now             time.Time
}

type DelegateHarnessResolution struct {
	Harness                string
	Command                string
	LaneID                 string
	RequestedModelID       string
	RequestedProviderRoute string
	CapacityLane           string
	TupleBacked            bool
	CapacitySource         *LaneCapacitySource
}

type DelegateHarnessResolverFunc func(DelegateHarnessResolveRequest) (DelegateHarnessResolution, error)

// DelegateHarnessResolver is installed by routeevidence, the owner of the
// Compound model-router authority. Injection avoids a work↔routeevidence
// import cycle while keeping a single tuple resolver.
var DelegateHarnessResolver DelegateHarnessResolverFunc

type DelegateHarnessReadinessRequest struct {
	HomePath               string
	HarnessHome            string
	WorkID                 string
	ContextPackID          string
	Workspace              string
	Harness                string
	LaneID                 string
	RequestedModelID       string
	RequestedProviderRoute string
	Command                string
	Args                   []string
	CommandOverridden      bool
	Now                    time.Time
}

type DelegateHarnessReadinessEvidence struct {
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Revision string `json:"revision,omitempty"`
}

type DelegateHarnessReadinessResult struct {
	Ready            bool                               `json:"ready"`
	SnapshotRevision string                             `json:"snapshot_revision,omitempty"`
	Details          []string                           `json:"details,omitempty"`
	Evidence         []DelegateHarnessReadinessEvidence `json:"evidence,omitempty"`
}

type DelegateHarnessReadinessFunc func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error)

// DelegateHarnessReadiness is installed by the executable composition root.
// It reads the permission, hook, and instruction authorities without mutating
// them. A missing guard fails closed: tuple/executable/capacity evidence alone
// is not proof that a harness is safe to activate.
var DelegateHarnessReadiness DelegateHarnessReadinessFunc

type DelegateResult struct {
	SchemaVersion string                          `json:"schema_version"`
	WorkID        string                          `json:"work_id"`
	Lane          string                          `json:"lane,omitempty"`
	Status        string                          `json:"status"` // spawned|refused|preview
	Binding       *DelegateBinding                `json:"binding,omitempty"`
	Refusal       *DelegateRefusal                `json:"refusal,omitempty"`
	Readiness     *DelegateHarnessReadinessResult `json:"readiness,omitempty"`
	Decision      *LaneCapacityGuardResult        `json:"decision,omitempty"`
	// RunAttemptID is the ratt_<UTC>_<hex> that was minted before Create
	// and marked running after the supervised spawn succeeded. Always
	// populated on Status="spawned"; older callers that recover a
	// pre-slice binding decode this as empty.
	RunAttemptID string `json:"run_attempt_id,omitempty"`
	EventID      string `json:"event_id,omitempty"`
	GeneratedAt  string `json:"generated_at"`
}

// DelegateBinding is the SYNTHESIZED view of the bound session. It is built
// from the newest attached work.delegate.spawned eventstore record (not
// from a sidecar file). The orchestrator returns it to the verb layer so
// the operator can print a single "session: <name>" hint.
type DelegateBinding struct {
	WorkID        string                          `json:"work_id"`
	Session       string                          `json:"session"`
	Harness       string                          `json:"harness"`
	Lane          string                          `json:"lane,omitempty"`
	Holder        string                          `json:"holder,omitempty"`
	CapacityState string                          `json:"capacity_state,omitempty"`
	RecordedAt    string                          `json:"recorded_at,omitempty"`
	WorkSHA256    string                          `json:"work_sha256,omitempty"`
	Readiness     *DelegateHarnessReadinessResult `json:"readiness,omitempty"`
	// RunAttemptID is the ratt_<UTC>_<hex> the orchestrator minted BEFORE
	// the supervised spawn. Older bindings (pre-slice) leave it empty
	// and remain backward-compatible: recovery still returns the
	// binding, sweep falls back to "no probe target".
	RunAttemptID string `json:"run_attempt_id,omitempty"`
}

type DelegateRefusal struct {
	Reason  string `json:"reason"` // readiness_unmet|authority_changed|walled|dead|expiring_unconfirmed|not_claimed|session_live|tmux_absent|gate_absent|harness_unknown|mint_failed|spawn_failed
	Message string `json:"message,omitempty"`
	Session string `json:"session,omitempty"` // session_live: the existing session name
}

func delegateSessionName(workID string) string { return "compound-" + workID }

func resolveDelegateSessionName(workID, override string) string {
	if o := strings.TrimSpace(override); o != "" {
		return o
	}
	return delegateSessionName(workID)
}

// delegateEvent is the structured record stored in the existing eventstore
// under compound.work_delegate_event.v0.1. It is the slice-1 single
// meaning owner for the binding (work ↔ tmux session).
//
// RunAttemptID carries the exact ratt_<UTC>_<hex> the orchestrator minted
// BEFORE the first supervised spawn side effect, so a later recovery or
// sweep can resolve from this binding back to its attempt without a side
// index. Older records (no RunAttemptID field) decode as empty string and
// remain backward-compatible: recovery still returns the binding, and
// sweep falls back to "no probe target".
//
// ExitCode / Signal carry the OBSERVED exit when present on a
// work.delegate.exited event. Older exited events (no fields) still
// serialize cleanly: the orchestrator accepts either shape.
type delegateEvent struct {
	SchemaVersion string                          `json:"schema_version"`
	ID            string                          `json:"id"`
	Kind          string                          `json:"kind"`
	TS            string                          `json:"ts"`
	WorkID        string                          `json:"work_id"`
	Harness       string                          `json:"harness"`
	Lane          string                          `json:"lane,omitempty"`
	Holder        string                          `json:"holder,omitempty"`
	CapacityState string                          `json:"capacity_state,omitempty"`
	Reason        string                          `json:"reason,omitempty"`
	Message       string                          `json:"message,omitempty"`
	Confirm       bool                            `json:"confirm,omitempty"`
	Session       string                          `json:"session,omitempty"`
	WorkSHA256    string                          `json:"work_sha256,omitempty"`
	RunAttemptID  string                          `json:"run_attempt_id,omitempty"`
	Readiness     *DelegateHarnessReadinessResult `json:"readiness,omitempty"`
	ExitCode      *int                            `json:"exit_code,omitempty"`
	Signal        string                          `json:"signal,omitempty"`
	ObservedExit  bool                            `json:"observed_exit,omitempty"`
}

func delegateEventID(kind, workID string, now time.Time) string {
	h := sha256.Sum256([]byte(strings.Join([]string{kind, workID, ts(now)}, "|")))
	return "wde_" + hex.EncodeToString(h[:])[:16]
}

func delegateBindingEventID(kind, workID string, b *DelegateBinding, now time.Time) string {
	identity := ""
	if b != nil {
		identity = strings.Join([]string{b.Session, b.RunAttemptID}, "|")
	}
	h := sha256.Sum256([]byte(strings.Join([]string{kind, workID, identity, ts(now)}, "|")))
	return "wde_" + hex.EncodeToString(h[:])[:16]
}

// latestDelegateBinding walks the work's lifecycle events oldest-first,
// reading every attached wde_* record through the existing eventstore API,
// and synthesizes the latest binding. A work.delegate.spawned sets the
// binding (and copies RunAttemptID so terminalization is keyed to the
// exact ratt); a work.delegate.exited retains that identity but marks it
// inactive so a partial cleanup can be retried without inventing a second
// exit event. recoverBoundSession wraps this helper and exposes only active
// bindings. Callers still probe SessionRunner.SessionStatus because the
// runner is the process-liveness source of truth.
//
// Returns (nil, false, nil) when no binding has ever existed. The second
// return is the eventlog-alive flag; SessionStatus at use time may still
// differ (pane dead under remain-on-exit).
func (s *Store) latestDelegateBinding(workID string) (*DelegateBinding, bool, error) {
	events, err := s.eventsFor(workID)
	if err != nil {
		return nil, false, err
	}
	var binding *DelegateBinding
	alive := false
	for _, e := range events {
		for _, att := range e.Attached {
			if !strings.HasPrefix(att, "wde_") {
				continue
			}
			var dev delegateEvent
			if rerr := s.readEventJSON(att, &dev); rerr != nil {
				return nil, false, fmt.Errorf("recover delegate binding %s: %w", att, rerr)
			}
			if verr := validateAttachedDelegateEvent(att, workID, dev); verr != nil {
				return nil, false, fmt.Errorf("recover delegate binding %s: %w", att, verr)
			}
			switch dev.Kind {
			case "work.delegate.spawned":
				binding = &DelegateBinding{
					WorkID:        dev.WorkID,
					Session:       dev.Session,
					Harness:       dev.Harness,
					Lane:          dev.Lane,
					Holder:        dev.Holder,
					CapacityState: dev.CapacityState,
					RecordedAt:    dev.TS,
					WorkSHA256:    dev.WorkSHA256,
					Readiness:     dev.Readiness,
					// Older spawned events (pre-slice) decode with empty
					// RunAttemptID; the rest of the orchestrator treats
					// empty as "no exact attempt" and stays backward-
					// compatible (terminalization is skipped; console
					// shows an empty ratt).
					RunAttemptID: dev.RunAttemptID,
				}
				alive = true
			case "work.delegate.exited":
				alive = false
			case "work.delegate.refused", "work.delegate.steered":
				// These are binding-neutral but still valid delegate events.
			}
		}
	}
	return binding, alive, nil
}

func (s *Store) recoverBoundSession(workID string) (*DelegateBinding, bool, error) {
	binding, alive, err := s.latestDelegateBinding(workID)
	if err != nil || !alive {
		return nil, false, err
	}
	return binding, true, nil
}

// recordedDelegateExit returns the latest exit attached after the matching
// spawned binding. Matching the exact RunAttemptID prevents an old exit from
// a reused session name from making a new generation look finalized. Legacy
// bindings without a ratt fall back to session identity and reset on the next
// matching spawn.
func (s *Store) recordedDelegateExit(workID string, b *DelegateBinding) (*delegateEvent, error) {
	events, err := s.eventsFor(workID)
	if err != nil {
		return nil, err
	}
	var matchedSpawn bool
	var found *delegateEvent
	for _, e := range events {
		for _, att := range e.Attached {
			if !strings.HasPrefix(att, "wde_") {
				continue
			}
			var dev delegateEvent
			if err := s.readEventJSON(att, &dev); err != nil {
				return nil, fmt.Errorf("recover delegate exit %s: %w", att, err)
			}
			if err := validateAttachedDelegateEvent(att, workID, dev); err != nil {
				return nil, fmt.Errorf("recover delegate exit %s: %w", att, err)
			}
			identityMatches := dev.Session == b.Session && (b.RunAttemptID == "" || dev.RunAttemptID == b.RunAttemptID)
			switch dev.Kind {
			case "work.delegate.spawned":
				if identityMatches {
					matchedSpawn = true
					found = nil
				}
			case "work.delegate.exited":
				if matchedSpawn && identityMatches {
					copy := dev
					found = &copy
				}
			}
		}
	}
	return found, nil
}

func validateAttachedDelegateEvent(eventID, workID string, event delegateEvent) error {
	if event.SchemaVersion != delegateEventSchemaVersion || event.ID != eventID || event.WorkID != workID {
		return errors.New("invalid delegate event identity")
	}
	switch event.Kind {
	case "work.delegate.spawned":
		if strings.TrimSpace(event.Session) == "" || strings.TrimSpace(event.Harness) == "" {
			return errors.New("spawned delegate event requires session and harness")
		}
	case "work.delegate.exited", "work.delegate.steered":
		if strings.TrimSpace(event.Session) == "" {
			return fmt.Errorf("%s delegate event requires session", event.Kind)
		}
	case "work.delegate.refused":
		if strings.TrimSpace(event.Reason) == "" {
			return errors.New("refused delegate event requires reason")
		}
	default:
		return fmt.Errorf("unknown kind %q", event.Kind)
	}
	return nil
}

// runLaneCapacityGuard is the nil-safe wrapper. A nil guard returns a
// synthetic gate_absent verdict (D3 contract); any panic inside the
// underlying guard is converted to the same refusal envelope.
func runLaneCapacityGuard(guard LaneCapacityGuardFunc, req LaneCapacityGuardRequest) (LaneCapacityGuardResult, error) {
	if guard == nil {
		return LaneCapacityGuardResult{
			Capacity: DispatchLaneCapacity{State: "absent"},
			Refused:  true,
			Message:  "no capacity gate installed (LaneCapacityGuard is nil)",
		}, nil
	}
	res, err := guard(req)
	if err != nil {
		return res, err
	}
	if res.Capacity.State == "" {
		res.Capacity.State = "unknown"
	}
	return res, nil
}

// completeDelegateReadinessSnapshot binds the harness-readiness evidence and
// the exact capacity verdict into one canonical revision. The resulting
// snapshot is persisted on the delegate binding; a second evaluation must
// produce the same revision immediately before RunAttempt mint.
func completeDelegateReadinessSnapshot(readiness DelegateHarnessReadinessResult, lane string, decision LaneCapacityGuardResult) (DelegateHarnessReadinessResult, error) {
	capacityMaterial, err := json.Marshal(struct {
		Lane                 string               `json:"lane"`
		Capacity             DispatchLaneCapacity `json:"capacity"`
		Refused              bool                 `json:"refused"`
		ConfirmationRequired bool                 `json:"confirmation_required"`
		Message              string               `json:"message,omitempty"`
	}{
		Lane:                 strings.TrimSpace(lane),
		Capacity:             decision.Capacity,
		Refused:              decision.Refused,
		ConfirmationRequired: decision.ConfirmationRequired,
		Message:              decision.Message,
	})
	if err != nil {
		return DelegateHarnessReadinessResult{}, fmt.Errorf("delegate: encode capacity readiness: %w", err)
	}
	capacitySum := sha256.Sum256(capacityMaterial)
	evidence := append([]DelegateHarnessReadinessEvidence(nil), readiness.Evidence...)
	evidence = append(evidence, DelegateHarnessReadinessEvidence{
		Kind:     "lane_capacity",
		Ref:      "lane-capacity://" + strings.TrimSpace(lane) + "/" + strings.TrimSpace(decision.Capacity.State),
		Revision: "sha256:" + hex.EncodeToString(capacitySum[:]),
	})
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Kind != evidence[j].Kind {
			return evidence[i].Kind < evidence[j].Kind
		}
		if evidence[i].Ref != evidence[j].Ref {
			return evidence[i].Ref < evidence[j].Ref
		}
		return evidence[i].Revision < evidence[j].Revision
	})
	details := append([]string(nil), readiness.Details...)
	sort.Strings(details)
	snapshotMaterial, err := json.Marshal(struct {
		Ready    bool                               `json:"ready"`
		Details  []string                           `json:"details,omitempty"`
		Evidence []DelegateHarnessReadinessEvidence `json:"evidence"`
	}{
		Ready:    readiness.Ready,
		Details:  details,
		Evidence: evidence,
	})
	if err != nil {
		return DelegateHarnessReadinessResult{}, fmt.Errorf("delegate: encode readiness snapshot: %w", err)
	}
	snapshotSum := sha256.Sum256(snapshotMaterial)
	readiness.Details = details
	readiness.Evidence = evidence
	readiness.SnapshotRevision = "sha256:" + hex.EncodeToString(snapshotSum[:])
	return readiness, nil
}

// Delegate is the governed execution entry: routes through the existing
// LaneCapacityGuard (or refuses closed when no guard is installed),
// refuses on walled/death, requires --confirm on expiring/unknown, then
// records a work.delegate.spawned eventstore record + a work lifecycle
// event that attaches it. Dry-run runs the gate without spawning and
// writes no event.
func (s *Store) Delegate(opts DelegateOptions, runner SessionRunner) (DelegateResult, error) {
	out := DelegateResult{
		SchemaVersion: delegateEventSchemaVersion,
		WorkID:        strings.TrimSpace(opts.WorkID),
		Status:        "refused",
		GeneratedAt:   opts.Now.UTC().Format(time.RFC3339),
	}
	if out.WorkID == "" {
		return out, errors.New("delegate: WorkID is required")
	}
	if strings.TrimSpace(opts.Harness) == "" {
		return out, errors.New("delegate: Harness is required")
	}
	if runner == nil {
		return out, errors.New("delegate: SessionRunner is nil")
	}
	resolution := DelegateHarnessResolution{
		Harness:      strings.TrimSpace(opts.Harness),
		Command:      strings.TrimSpace(opts.Command),
		LaneID:       strings.TrimSpace(opts.Harness),
		CapacityLane: strings.TrimSpace(opts.Harness),
	}
	if DelegateHarnessResolver != nil {
		resolved, resolveErr := DelegateHarnessResolver(DelegateHarnessResolveRequest{
			HomePath: s.home, Harness: opts.Harness, CommandOverride: opts.Command, Now: opts.Now,
		})
		if resolveErr != nil {
			out.Refusal = &DelegateRefusal{Reason: "harness_unknown", Message: resolveErr.Error()}
			return out, nil
		}
		resolution = resolved
	}
	resolvedHarness := strings.TrimSpace(resolution.Harness)
	command := strings.TrimSpace(resolution.Command)
	laneForGuard := strings.TrimSpace(resolution.CapacityLane)
	if laneForGuard == "" {
		laneForGuard = strings.TrimSpace(resolution.LaneID)
	}
	if resolvedHarness == "" || command == "" || laneForGuard == "" {
		out.Refusal = &DelegateRefusal{
			Reason:  "harness_unknown",
			Message: fmt.Sprintf("harness %q did not resolve a complete executable tuple", opts.Harness),
		}
		return out, nil
	}
	out.Lane = strings.TrimSpace(resolution.LaneID)
	if out.Lane == "" {
		out.Lane = laneForGuard
	}

	w, err := s.Get(out.WorkID)
	if err != nil {
		return out, fmt.Errorf("delegate: load work %s: %w", out.WorkID, err)
	}
	if w.State != "claimed" && w.State != "running" {
		out.Refusal = &DelegateRefusal{
			Reason:  "not_claimed",
			Message: fmt.Sprintf("work is in state %q, must be claimed or running", w.State),
		}
		return out, nil
	}
	lease, err := s.readLease(out.WorkID)
	if err != nil {
		out.Refusal = &DelegateRefusal{Reason: "not_claimed", Message: err.Error()}
		return out, nil
	}
	leaseHolder := strings.TrimSpace(lease.By)
	holder := strings.TrimSpace(opts.Actor)
	if holder == "" {
		holder = leaseHolder
	}
	if holder != leaseHolder {
		message := fmt.Sprintf("work lease is held by %q; actor %q cannot delegate it", leaseHolder, holder)
		if opts.DryRun {
			out.Refusal = &DelegateRefusal{Reason: "lease_held", Message: message}
			return out, nil
		}
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "lease_held", message, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=lease_held", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "lease_held", Message: message}
		return out, nil
	}
	authorizedWorkSHA, shaErr := workEnvelopeSHA256(w)
	if shaErr != nil {
		return out, shaErr
	}
	authorizedLease := lease

	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" && w.GitDelivery != nil {
		workspace = strings.TrimSpace(w.GitDelivery.TargetWorktreeRoot)
	}
	if workspace == "" {
		workspace, err = os.Getwd()
		if err != nil {
			return out, fmt.Errorf("delegate: resolve workspace: %w", err)
		}
	}
	if strings.TrimSpace(workspace) == "" {
		return out, errors.New("delegate: no workspace resolved from --workspace, git_delivery.target_worktree_root, or cwd")
	}
	activationArgs := append([]string(nil), opts.Args...)
	readinessRequest := DelegateHarnessReadinessRequest{
		HomePath:               s.home,
		WorkID:                 out.WorkID,
		ContextPackID:          w.IDs.ContextPackID,
		Workspace:              workspace,
		Harness:                resolvedHarness,
		LaneID:                 out.Lane,
		RequestedModelID:       resolution.RequestedModelID,
		RequestedProviderRoute: resolution.RequestedProviderRoute,
		Command:                command,
		Args:                   append([]string(nil), activationArgs...),
		CommandOverridden:      strings.TrimSpace(opts.Command) != "",
		Now:                    opts.Now,
	}
	readinessGuard := DelegateHarnessReadiness

	readiness := DelegateHarnessReadinessResult{}
	var readinessErr error
	if readinessGuard == nil {
		readiness.Details = []string{"no delegate harness readiness guard is installed"}
	} else {
		readiness, readinessErr = readinessGuard(readinessRequest)
	}
	if readinessErr != nil {
		readiness.Ready = false
		readiness.Details = append(readiness.Details, "readiness probe failed: "+readinessErr.Error())
	}
	out.Readiness = &readiness
	if !readiness.Ready {
		if len(readiness.Details) == 0 {
			readiness.Details = []string{"harness readiness was not established"}
		}
		out.Refusal = &DelegateRefusal{
			Reason:  "readiness_unmet",
			Message: strings.Join(readiness.Details, "; "),
		}
		return out, nil
	}

	capacitySource := resolution.CapacitySource
	if capacitySource != nil {
		copied := *capacitySource
		capacitySource = &copied
	}
	capacityRequest := LaneCapacityGuardRequest{
		HomePath:        s.home,
		Lane:            laneForGuard,
		WorkID:          out.WorkID,
		ExpectedMinutes: dispatchExpectedMinutes(opts.ExpectedMinutes),
		Confirm:         opts.Confirm,
		ReadOnly:        opts.DryRun,
		Now:             opts.Now,
		TupleBacked:     resolution.TupleBacked,
		CapacitySource:  capacitySource,
	}
	capacityGuard := LaneCapacityGuard
	decision, err := runLaneCapacityGuard(capacityGuard, capacityRequest)
	if err != nil {
		return out, fmt.Errorf("delegate: guard lane capacity: %w", err)
	}
	out.Decision = &decision
	readiness, err = completeDelegateReadinessSnapshot(readiness, laneForGuard, decision)
	if err != nil {
		return out, err
	}
	out.Readiness = &readiness

	// Dry-run: surface the verdict WITHOUT refusing and WITHOUT persisting
	// any event. The decision carries enough for the operator to decide
	// whether to re-run with --confirm.
	if opts.DryRun {
		out.Status = "preview"
		return out, nil
	}

	// D3: when the guard is nil, runLaneCapacityGuard synthesizes a
	// gate_absent verdict (Refused=true). Treat that as the documented
	// gate_absent refusal so the audit trail carries the canonical reason.
	if decision.Refused && strings.TrimSpace(decision.Capacity.State) == "absent" {
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "gate_absent", decision.Message, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=gate_absent", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "gate_absent", Message: decision.Message}
		return out, nil
	}
	if decision.Refused {
		reason := strings.TrimSpace(decision.Capacity.State)
		if reason == "" {
			reason = "walled"
		}
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, reason, decision.Message, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, fmt.Sprintf("delegate refused reason=%s", reason), opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: reason, Message: decision.Message}
		return out, nil
	}
	if decision.ConfirmationRequired && !opts.Confirm {
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "expiring_unconfirmed", decision.Message, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=expiring_unconfirmed", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "expiring_unconfirmed", Message: decision.Message}
		return out, nil
	}

	sessionName := resolveDelegateSessionName(out.WorkID, opts.Session)
	// Existing binding? Probe liveness via SessionStatus (NOT Has, which
	// is false evidence once a pane dies under remain-on-exit); if
	// exists+pane_dead, record .exited and proceed with a fresh spawn on
	// the SAME session name (deterministic so a steer-while-dead can
	// re-attach); if exists+alive, refuse session_live.
	existing, _, rerr := s.recoverBoundSession(out.WorkID)
	if rerr != nil {
		return out, rerr
	}
	if existing != nil {
		st, sessErr := runner.SessionStatus(existing.Session)
		if sessErr != nil {
			return out, fmt.Errorf("delegate: probe session %s: %w", existing.Session, sessErr)
		}
		if st.Exists && !st.PaneDead {
			evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "session_live", "a live bound session already exists for this work", opts.Now)
			if eerr != nil {
				return out, eerr
			}
			if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=session_live", opts.Now); err != nil {
				return out, err
			}
			out.EventID = evID
			out.Refusal = &DelegateRefusal{
				Reason:  "session_live",
				Message: "a live bound session already exists for this work",
				Session: existing.Session,
			}
			return out, nil
		}
		// Dead or gone: finalize via the unified DelegateSweep helper
		// (terminalize the attempt, record the exited artifact, kill the
		// finished tmux pane if any). This MUST happen BEFORE respawning
		// on the same session name -- otherwise the next Create sees the
		// orphan pane and classifies spawn_ambiguous.
		if _, err := s.DelegateSweep(existing, st, runner, holder, opts.Now); err != nil {
			return out, err
		}
	}

	if availErr := runner.Available(); availErr != nil {
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "tmux_absent", availErr.Error(), opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=tmux_absent", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "tmux_absent", Message: availErr.Error()}
		return out, nil
	}

	// MINT BEFORE CREATE: the orchestrator mints the delegate RunAttempt
	// with origin/lane/workspace/wrapper command only -- NEVER prompt or
	// argv, which may contain operator secrets. RunAttempt persistence is
	// independent of the spawn side effect, so a Mint failure is a
	// refuse -- ZERO Create side effects, ZERO session exists.
	raStore := runattempt.Open(s.home)
	var minted runattempt.RunAttempt
	mintErr := s.withLifecycleMutation(func() error {
		current, err := s.Get(out.WorkID)
		if err != nil {
			return &delegateAuthorityChangedError{detail: "work envelope is no longer readable"}
		}
		currentSHA, err := workEnvelopeSHA256(current)
		if err != nil {
			return err
		}
		if currentSHA != authorizedWorkSHA {
			return &delegateAuthorityChangedError{detail: "work envelope changed"}
		}
		if current.State != "claimed" && current.State != "running" {
			return &delegateAuthorityChangedError{detail: "work state is no longer claimed or running"}
		}
		currentLease, err := s.readLease(out.WorkID)
		if err != nil {
			return &delegateAuthorityChangedError{detail: "work lease is no longer readable"}
		}
		if currentLease != authorizedLease || strings.TrimSpace(currentLease.By) != holder {
			return &delegateAuthorityChangedError{detail: "work lease changed"}
		}
		revalidatedReadiness, err := readinessGuard(readinessRequest)
		if err != nil {
			return &delegateAuthorityChangedError{detail: "harness readiness revalidation failed"}
		}
		if !revalidatedReadiness.Ready {
			return &delegateAuthorityChangedError{detail: "harness readiness is no longer satisfied"}
		}
		recheckCapacity := capacityRequest
		// Revalidation is evidence-only: it must not emit a duplicate
		// capacity-refusal observation while deciding whether the original
		// activation snapshot still holds.
		recheckCapacity.ReadOnly = true
		revalidatedCapacity, err := runLaneCapacityGuard(capacityGuard, recheckCapacity)
		if err != nil {
			return &delegateAuthorityChangedError{detail: "lane capacity revalidation failed"}
		}
		revalidatedSnapshot, err := completeDelegateReadinessSnapshot(revalidatedReadiness, laneForGuard, revalidatedCapacity)
		if err != nil {
			return err
		}
		if revalidatedSnapshot.SnapshotRevision != readiness.SnapshotRevision {
			return &delegateAuthorityChangedError{
				detail: fmt.Sprintf(
					"readiness snapshot changed (admitted=%s current=%s)",
					readiness.SnapshotRevision,
					revalidatedSnapshot.SnapshotRevision,
				),
			}
		}
		minted, err = raStore.Mint(out.WorkID, holder, resolvedHarness, opts.Now)
		return err
	})
	var authorityChanged *delegateAuthorityChangedError
	if errors.As(mintErr, &authorityChanged) {
		out.Refusal = &DelegateRefusal{
			Reason:  "authority_changed",
			Message: authorityChanged.Error(),
		}
		return out, nil
	}
	if mintErr != nil {
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "mint_failed", "RunAttempt mint failed before any spawn side effect", opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=mint_failed", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "mint_failed", Message: "RunAttempt mint failed before any spawn side effect"}
		return out, nil
	}
	runAttemptID := minted.ID
	// Stamp the run-attempt envelope with origin / lane / workspace /
	// wrapper command only -- prompt text / args must NEVER reach
	// RunAttempt on disk. Stamp failures terminalize the mint (the
	// attempt is the only thing that exists at this point) and refuse.
	stamp := func(field, msg string) (DelegateResult, error) {
		if _, terr := raStore.Complete(runAttemptID, runattempt.StatusFailed, opts.Now); terr != nil {
			return out, terr
		}
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "mint_failed", msg, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=mint_failed ("+field+")", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "mint_failed", Message: msg}
		return out, nil
	}
	if _, settErr := raStore.SetOrigin(runAttemptID, runattempt.OriginHarnessExecution); settErr != nil {
		return stamp("origin", "RunAttempt origin stamp failed")
	}
	if _, settErr := raStore.SetWorkspace(runAttemptID, workspace); settErr != nil {
		return stamp("workspace", "RunAttempt workspace stamp failed")
	}
	if _, settErr := raStore.SetCommand(runAttemptID, command); settErr != nil {
		return stamp("command", "RunAttempt command stamp failed")
	}
	if _, settErr := raStore.SetDispatchIntent(runAttemptID, resolution.LaneID, resolution.RequestedModelID, resolution.RequestedProviderRoute); settErr != nil {
		return stamp("attribution", "RunAttempt attribution stamp failed")
	}

	if err := runner.Create(sessionName, workspace, command, activationArgs); err != nil {
		// Create failed. Probe to detect partial / ambiguous: if the
		// session exists on the server we DO NOT kill it (it may be a
		// stranger's session) and we DO NOT emit a spawned binding. We
		// terminalize the mint as failed and record
		// work.delegate.refused(spawn_ambiguous).
		probe, probeErr := runner.SessionStatus(sessionName)
		reason := "spawn_failed"
		msg := delegateSpawnFailureNote
		if probeErr != nil || probe.UnknownReason != "" {
			reason = "spawn_ambiguous"
			msg = delegateProbeUnknownNote
		} else if probe.Exists {
			reason = "spawn_ambiguous"
			msg = delegateAmbiguousNote
		}
		if _, terr := raStore.Complete(runAttemptID, runattempt.StatusFailed, opts.Now); terr != nil {
			return out, terr
		}
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, reason, msg, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason="+reason, opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: reason, Message: msg}
		return out, nil
	}

	binding := &DelegateBinding{
		WorkID:        out.WorkID,
		Session:       sessionName,
		Harness:       resolvedHarness,
		Lane:          laneForGuard,
		Holder:        holder,
		CapacityState: decision.Capacity.State,
		RecordedAt:    ts(opts.Now),
		WorkSHA256:    authorizedWorkSHA,
		Readiness:     out.Readiness,
		RunAttemptID:  runAttemptID,
	}
	// APPROVED ORDER: Create success -> record+attach spawned binding ->
	// MarkRunning. Any failure AFTER the spawn + binding is a
	// finalize-spawn-failure: kill the definitely-owned session,
	// terminalize the attempt, and record a truthful exited artifact.
	evID, evErr := s.recordDelegateSpawned(out.WorkID, binding, opts.Confirm, runAttemptID, opts.Now)
	if evErr != nil {
		// recordDelegateSpawned failed: no recoverable binding yet.
		// Kill definitely-owned session + terminalize attempt + return.
		// Do NOT write a work.delegate.exited (that would create an
		// exit-without-spawn).
		return out, s.finalizeSpawnFailureAfterBinding(runner, out.WorkID, binding, holder, w.State, evErr, false, opts.Now)
	}
	if err := s.appendEvent(Event{
		TS:       ts(opts.Now),
		WorkID:   out.WorkID,
		From:     w.State,
		To:       w.State,
		By:       holder,
		Note:     fmt.Sprintf("delegate spawned session %s harness=%s lane=%s", binding.Session, binding.Harness, binding.Lane),
		Attached: []string{evID},
	}); err != nil {
		// Lifecycle append failed: same as recordDelegateSpawned failure
		// -- no recoverable binding, no exited artifact.
		return out, s.finalizeSpawnFailureAfterBinding(runner, out.WorkID, binding, holder, w.State, err, false, opts.Now)
	}
	// Forward-link the exact delegate attempt from the WorkEnvelope through
	// the existing governed same-state Advance seam. The binding is already
	// durable, so an attach failure is a post-spawn failure: finalize the
	// definitely-owned session and the attempt rather than leaving a ratt
	// reachable only from run_attempts/ and the delegate event.
	if _, advanceErr := s.Advance(out.WorkID, AdvanceInput{
		To:            w.State,
		Actor:         holder,
		RunAttemptIDs: []string{runAttemptID},
		Note:          "delegate run attempt attached",
	}, opts.Now); advanceErr != nil {
		return out, s.finalizeSpawnFailureAfterBinding(runner, out.WorkID, binding, holder, w.State, advanceErr, true, opts.Now)
	}
	if _, mErr := raStore.MarkRunning(runAttemptID, opts.Now); mErr != nil {
		// MarkRunning failure: binding IS attached in the eventstore
		// (so a future recovery could return a stale live binding). We
		// write the truthful work.delegate.exited here, kill the
		// session, and terminalize. Without this, the orchestrator
		// could later recover a binding with no matching run attempt.
		return out, s.finalizeSpawnFailureAfterBinding(runner, out.WorkID, binding, holder, w.State, mErr, true, opts.Now)
	}
	out.Status = "spawned"
	out.Binding = binding
	out.EventID = evID
	out.RunAttemptID = runAttemptID
	return out, nil
}

// finalizeSpawnFailureAfterBinding is the post-Create failure-finalization
// helper. bindingAttached controls whether the caller had already attached
// the spawned lifecycle binding BEFORE the failure:
//
//	false -- recordDelegateSpawned failed, or the lifecycle append failed:
//	  no binding is recoverable from the eventstore. Kill the session,
//	  terminalize the attempt, and DO NOT write a work.delegate.exited
//	  (emitting one here would create an exit-without-spawn).
//	true  -- MarkRunning failed AFTER the binding was attached. Kill the
//	  session, terminalize the attempt, AND write a truthful
//	  work.delegate.exited so a future recovery does not surface a
//	  stale live binding with no matching run attempt.
//
// In both branches the cause is returned unchanged. The cause's Error()
// is NEVER persisted -- adapter errors may echo prompt argv.
func (s *Store) finalizeSpawnFailureAfterBinding(runner SessionRunner, workID string, b *DelegateBinding, holder, state string, cause error, bindingAttached bool, now time.Time) error {
	if b == nil {
		return cause
	}
	cleanupErrs := []error{cause}
	if runner != nil {
		if killErr := runner.Kill(b.Session); killErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delegate: kill definitely-owned session %s: %w", b.Session, killErr))
		}
	}
	raStore := runattempt.Open(s.home)
	if _, terr := raStore.Complete(b.RunAttemptID, runattempt.StatusFailed, now); terr != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delegate: terminalize spawned attempt %s: %w", b.RunAttemptID, terr))
	}
	if !bindingAttached {
		return errors.Join(cleanupErrs...)
	}
	ev := delegateEvent{
		SchemaVersion: delegateEventSchemaVersion,
		ID:            delegateBindingEventID("work.delegate.exited", workID, b, now),
		Kind:          "work.delegate.exited",
		TS:            ts(now),
		WorkID:        workID,
		Harness:       b.Harness,
		Lane:          b.Lane,
		Holder:        holder,
		Session:       b.Session,
		WorkSHA256:    b.WorkSHA256,
		RunAttemptID:  b.RunAttemptID,
		Message:       "spawn_finalize_failure",
		Reason:        "spawn_finalize_failure",
	}
	if err := s.writeEventJSON(ev.ID, ev); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delegate: record spawn-finalize exit: %w", err))
	} else if err := s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   workID,
		From:     state,
		To:       state,
		By:       holder,
		Note:     fmt.Sprintf("delegate exited session %s (spawn_finalize_failure)", b.Session),
		Attached: []string{ev.ID},
	}); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delegate: attach spawn-finalize exit: %w", err))
	}
	return errors.Join(cleanupErrs...)
}

// --- record helpers (writeEventJSON, D7 contract) ---

func (s *Store) recordDelegateSpawned(workID string, b *DelegateBinding, confirm bool, runAttemptID string, now time.Time) (string, error) {
	id := delegateBindingEventID("work.delegate.spawned", workID, b, now)
	if err := s.writeEventJSON(id, delegateEvent{
		SchemaVersion: delegateEventSchemaVersion,
		ID:            id,
		Kind:          "work.delegate.spawned",
		TS:            ts(now),
		WorkID:        workID,
		Harness:       b.Harness,
		Lane:          b.Lane,
		Holder:        b.Holder,
		CapacityState: b.CapacityState,
		Confirm:       confirm,
		Session:       b.Session,
		WorkSHA256:    b.WorkSHA256,
		RunAttemptID:  runAttemptID,
		Readiness:     b.Readiness,
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) recordDelegateRefused(workID, harness, lane, holder, reason, message string, now time.Time) (string, error) {
	id := delegateEventID("work.delegate.refused", workID, now)
	if err := s.writeEventJSON(id, delegateEvent{
		SchemaVersion: delegateEventSchemaVersion,
		ID:            id,
		Kind:          "work.delegate.refused",
		TS:            ts(now),
		WorkID:        workID,
		Harness:       harness,
		Lane:          lane,
		Holder:        holder,
		Reason:        reason,
		Message:       message,
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) recordDelegateSteered(workID string, b *DelegateBinding, holder, text string, enter bool, now time.Time) (string, error) {
	id := delegateEventID("work.delegate.steered", workID, now)
	if err := s.writeEventJSON(id, delegateEvent{
		SchemaVersion: delegateEventSchemaVersion,
		ID:            id,
		Kind:          "work.delegate.steered",
		TS:            ts(now),
		WorkID:        workID,
		Harness:       b.Harness,
		Lane:          b.Lane,
		Holder:        holder,
		CapacityState: b.CapacityState,
		Session:       b.Session,
		WorkSHA256:    b.WorkSHA256,
		Message:       fmt.Sprintf("steered len=%d enter=%v", len(text), enter),
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) recordDelegateExited(workID string, b *DelegateBinding, observed *SessionStatus, now time.Time) (string, error) {
	id := delegateBindingEventID("work.delegate.exited", workID, b, now)
	ev := delegateEvent{
		SchemaVersion: delegateEventSchemaVersion,
		ID:            id,
		Kind:          "work.delegate.exited",
		TS:            ts(now),
		WorkID:        workID,
		Harness:       b.Harness,
		Lane:          b.Lane,
		Holder:        b.Holder,
		Session:       b.Session,
		WorkSHA256:    b.WorkSHA256,
		Message:       "session no longer live at delegate-time; orchestrator observed death",
	}
	// Carry observed exit when known. Older exited events without
	// observed fields still serialize cleanly (omitempty / nil), so old
	// records decode unchanged.
	//
	// ExitCode pointer is set ONLY when an observed value >= 0 exists
	// -- a signal-only exit must NOT serialize -1 (that would lie).
	if observed != nil {
		if observed.ExitCode >= 0 {
			code := observed.ExitCode
			ev.ExitCode = &code
			ev.ObservedExit = true
		}
		if observed.Signal != "" {
			ev.Signal = observed.Signal
			ev.ObservedExit = true
		}
	}
	// Carry the exact ratt so the exit artifact is keyed to its
	// attempt. Older bindings (empty RunAttemptID) decode unchanged.
	if b != nil {
		ev.RunAttemptID = b.RunAttemptID
	}
	if err := s.writeEventJSON(id, ev); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) recordAndAttachDelegateExited(workID, state string, b *DelegateBinding, observer string, observed SessionStatus, now time.Time) (string, error) {
	id, err := s.recordDelegateExited(workID, b, &observed, now)
	if err != nil {
		return "", err
	}
	if err := s.appendDelegateLifecycleEvent(workID, state, observer, id, fmt.Sprintf("delegate exited session %s", b.Session), now); err != nil {
		return "", err
	}
	return id, nil
}

// --- steer ---

type SteerOptions struct {
	WorkID string
	Actor  string
	Text   string
	Enter  bool
	Now    time.Time
}

type SteerResult struct {
	SchemaVersion string `json:"schema_version"`
	WorkID        string `json:"work_id"`
	Status        string `json:"status"`
	Session       string `json:"session"`
	EventID       string `json:"event_id,omitempty"`
	GeneratedAt   string `json:"generated_at"`
}

func (s *Store) Steer(opts SteerOptions, runner SessionRunner) (SteerResult, error) {
	out := SteerResult{
		SchemaVersion: delegateEventSchemaVersion,
		WorkID:        strings.TrimSpace(opts.WorkID),
		GeneratedAt:   opts.Now.UTC().Format(time.RFC3339),
	}
	if out.WorkID == "" {
		return out, errors.New("steer: WorkID is required")
	}
	if strings.TrimSpace(opts.Text) == "" {
		return out, errors.New("steer: --text is required")
	}
	if runner == nil {
		return out, errors.New("steer: SessionRunner is nil")
	}
	binding, _, err := s.recoverBoundSession(out.WorkID)
	if err != nil {
		return out, err
	}
	if binding == nil {
		return out, fmt.Errorf("steer: work %s has no bound session (call `compound work delegate` first)", out.WorkID)
	}
	holder := strings.TrimSpace(opts.Actor)
	if holder == "" {
		holder = binding.Holder
	}
	if holder == "" {
		holder = "codex"
	}
	st, stErr := runner.SessionStatus(binding.Session)
	if stErr != nil {
		return out, fmt.Errorf("steer: probe session %s: %w", binding.Session, stErr)
	}
	if !st.Exists || st.PaneDead {
		if _, sweepErr := s.DelegateSweep(binding, st, runner, holder, opts.Now); sweepErr != nil {
			return out, sweepErr
		}
		return out, fmt.Errorf("steer: session %s for work %s is no longer live; spawn a new delegate", binding.Session, out.WorkID)
	}
	if err := runner.SendKeys(binding.Session, opts.Text, opts.Enter); err != nil {
		return out, fmt.Errorf("steer: send-keys %s: %w", binding.Session, err)
	}
	evID, evErr := s.recordDelegateSteered(out.WorkID, binding, holder, opts.Text, opts.Enter, opts.Now)
	if evErr != nil {
		return out, evErr
	}
	w, werr := s.Get(out.WorkID)
	if werr != nil {
		return out, werr
	}
	if err := s.appendEvent(Event{
		TS:       ts(opts.Now),
		WorkID:   out.WorkID,
		From:     w.State,
		To:       w.State,
		By:       holder,
		Note:     fmt.Sprintf("delegate steered session %s (len=%d)", binding.Session, len(opts.Text)),
		Attached: []string{evID},
	}); err != nil {
		return out, err
	}
	out.Status = "steered"
	out.Session = binding.Session
	out.EventID = evID
	return out, nil
}

// --- attach ---

type AttachOptions struct {
	WorkID string
	Actor  string
}

type AttachResult struct {
	SchemaVersion string   `json:"schema_version"`
	WorkID        string   `json:"work_id"`
	Session       string   `json:"session"`
	AttachArgv    []string `json:"attach_argv"`
	Live          bool     `json:"live"`
}

func (s *Store) Attach(opts AttachOptions, runner SessionRunner) (AttachResult, error) {
	out := AttachResult{
		SchemaVersion: delegateEventSchemaVersion,
		WorkID:        strings.TrimSpace(opts.WorkID),
	}
	if out.WorkID == "" {
		return out, errors.New("attach: WorkID is required")
	}
	if runner == nil {
		return out, errors.New("attach: SessionRunner is nil")
	}
	binding, _, err := s.recoverBoundSession(out.WorkID)
	if err != nil {
		return out, err
	}
	if binding == nil {
		return out, fmt.Errorf("attach: work %s has no bound session (call `compound work delegate` first)", out.WorkID)
	}
	out.Session = binding.Session
	out.AttachArgv = runner.AttachArgv(binding.Session)
	st, stErr := runner.SessionStatus(binding.Session)
	if stErr != nil {
		return out, fmt.Errorf("attach: probe session %s: %w", binding.Session, stErr)
	}
	out.Live = st.Exists && !st.PaneDead
	if !out.Live {
		return out, fmt.Errorf("attach: session %s for work %s is no longer live; spawn a new delegate", binding.Session, out.WorkID)
	}
	return out, nil
}

// appendDelegateLifecycleEvent appends a no-state-change work lifecycle
// event whose Attached[] carries one delegate event id. Slice 1 contract:
// every structured delegate event (spawned, refused, steered, exited) MUST
// attach to a lifecycle event so the audit chain points at it.
func (s *Store) appendDelegateLifecycleEvent(workID, fromState, by, evID, note string, now time.Time) error {
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   workID,
		From:     fromState,
		To:       fromState,
		By:       by,
		Note:     note,
		Attached: []string{evID},
	})
}

// DelegateSweep finalizes an exact binding: maps the observed exit
// status to the right RunAttempt terminal status, kills the finished tmux
// pane (NOT a live one -- bare Has is false evidence once remain-on-exit
// keeps a dead pane alive; SessionStatus is the unified source of truth
// for any pane that has actually died), and records work.delegate.exited.
// Used by:
//   - the explicit `compound work delegate sweep` path,
//   - Steer when the bound pane is dead (instead of just appending an
//     exited lifecycle note, the exact ratt terminalizes and the pane
//     disappears too),
//   - the existing-dead-binding respawn path (BEFORE same-name respawn,
//     otherwise Create deterministically becomes spawn_ambiguous).
//
// Exit mapping (from observed SessionStatus):
//   - observed.PaneDead + ExitCode == 0 -> succeeded
//   - observed.PaneDead + ExitCode > 0 OR signal != "" -> failed
//   - observed.UnknownReason != "" -> UNCERTAIN: surface as a sweep
//     error; do NOT claim failed/abandoned/succeeded; the probe did
//     not conclude. Caller propagates the error.
//   - observed.Exists == false (no probe error) -> abandoned
//
// The RunAttemptID is what makes terminalization exact: only THIS ratt_
// is completed, no sibling claim-reservation attempt is touched. When
// the binding is older than slice-1 (RunAttemptID empty) terminalization
// is skipped, but the killed pane and exited artifact are still emitted.
// The lifecycle-self-transition uses the work envelope's CURRENT state
// (loaded via s.Get), not "".
func (s *Store) DelegateSweep(b *DelegateBinding, observed SessionStatus, runner SessionRunner, observer string, now time.Time) (DelegateSweepOutcome, error) {
	if b == nil {
		return DelegateSweepOutcome{}, fmt.Errorf("delegate sweep: nil binding")
	}
	out := DelegateSweepOutcome{Session: b.Session, WorkID: b.WorkID, RunAttemptID: b.RunAttemptID}
	if observed.UnknownReason != "" {
		// Probe error: do NOT claim an exit. Operator must re-run after
		// the runner is fixed.
		return out, fmt.Errorf("delegate sweep: probe uncertainty: %s", observed.UnknownReason)
	}
	recordedExit, err := s.recordedDelegateExit(b.WorkID, b)
	if err != nil {
		return out, err
	}
	// LIVE pane -- heartbeat the exact ratt (when present) and return
	// WITHOUT writing an event, terminalizing the attempt, or killing.
	// The sweep was not designed to abandon a healthy binding; the
	// generic stale sweep protects it elsewhere.
	if observed.Exists && !observed.PaneDead {
		if recordedExit != nil {
			return out, fmt.Errorf("delegate sweep: session %s is live after its exit was already recorded; refusing to touch a potentially foreign pane", b.Session)
		}
		if b.RunAttemptID != "" {
			raStore := runattempt.Open(s.home)
			if _, herr := raStore.Heartbeat(b.RunAttemptID, now); herr != nil {
				return out, herr
			}
			if cur, gerr := raStore.Get(b.RunAttemptID); gerr == nil {
				out.RunAttemptStatus = cur.Status
			}
		}
		out.ObservedExit = "live"
		out.TerminalStatus = "live"
		return out, nil
	}
	var status, observedLabel string
	if recordedExit != nil {
		switch {
		case recordedExit.Reason == "spawn_finalize_failure":
			status, observedLabel = runattempt.StatusFailed, "failed"
		case recordedExit.ExitCode != nil && *recordedExit.ExitCode == 0:
			status, observedLabel = runattempt.StatusSucceeded, "succeeded"
		case recordedExit.ExitCode != nil && *recordedExit.ExitCode > 0:
			status, observedLabel = runattempt.StatusFailed, "failed"
		case recordedExit.Signal != "":
			status, observedLabel = runattempt.StatusFailed, "failed"
		default:
			status, observedLabel = runattempt.StatusAbandoned, "vanished"
		}
	} else {
		switch {
		case observed.Exists && observed.PaneDead && observed.ExitCode == 0:
			status = runattempt.StatusSucceeded
			observedLabel = "succeeded"
		case observed.Exists && observed.PaneDead && observed.ExitCode > 0:
			status = runattempt.StatusFailed
			observedLabel = "failed"
		case observed.Exists && observed.PaneDead && observed.Signal != "":
			status = runattempt.StatusFailed
			observedLabel = "failed"
		case observed.Exists && observed.PaneDead:
			// Pane dead but no observable exit value: be honest with "unknown"
			// rather than guessing succeeded / failed.
			return out, fmt.Errorf("delegate sweep: pane is dead but observed exit is unknown (ExitCode=%d)", observed.ExitCode)
		default:
			status = runattempt.StatusAbandoned
			observedLabel = "vanished"
		}
	}
	// Lifecycle self-transition needs the envelope's CURRENT state. A
	// failed load is propagated -- we never append state="" (that would
	// silently corrupt the eventlog).
	if recordedExit == nil {
		w, gerr := s.Get(b.WorkID)
		if gerr != nil {
			return out, fmt.Errorf("delegate sweep: load work %s: %w", b.WorkID, gerr)
		}
		if _, err := s.recordAndAttachDelegateExited(b.WorkID, w.State, b, observer, observed, now); err != nil {
			return out, err
		}
	}
	if b.RunAttemptID != "" {
		raStore := runattempt.Open(s.home)
		cur, gerr := raStore.Get(b.RunAttemptID)
		if gerr != nil {
			return out, gerr
		}
		if runattempt.IsTerminal(cur.Status) {
			if cur.Status != status {
				return out, fmt.Errorf("delegate sweep: attempt %s already terminal as %s, observed %s", b.RunAttemptID, cur.Status, status)
			}
		} else if _, terr := raStore.Complete(b.RunAttemptID, status, now); terr != nil {
			return out, terr
		}
		out.RunAttemptStatus = status
	}
	if runner != nil && observed.PaneDead {
		if err := runner.Kill(b.Session); err != nil {
			out.KillError = err.Error()
			return out, fmt.Errorf("delegate sweep: remove finished pane %s: %w", b.Session, err)
		}
	}
	out.ObservedExit = observedLabel
	out.TerminalStatus = status
	return out, nil
}

// RecoverBoundSession is the exported form of recoverBoundSession. It
// returns the current binding (or nil + no error for "no binding
// exists") plus the eventlog-derived alive flag. Callers that need to
// probe the runner themselves use this to look up the binding; it does
// NOT touch tmux.
func (s *Store) RecoverBoundSession(workID string) (*DelegateBinding, bool, error) {
	return s.recoverBoundSession(workID)
}

// DelegateSweepResolve is a convenience wrapper that returns the current
// binding (or nil + no error for "no binding exists") for the explicit
// sweep CLI verb. It does NOT probe the runner; the caller owns the
// session-status read so a missing pane surfaces immediately.
func (s *Store) DelegateSweepResolve(workID string) (*DelegateBinding, error) {
	b, _, err := s.latestDelegateBinding(workID)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// DelegateStatus returns a read-only, write-free observation of the
// binding and its RunAttempt. It performs no kill, no event write, and
// no terminalization -- the dry-run-capable sibling of DelegateSweep.
func (s *Store) DelegateStatus(workID string, runner SessionRunner) (DelegateStatusObservation, error) {
	out := DelegateStatusObservation{WorkID: workID}
	b, _, err := s.recoverBoundSession(workID)
	if err != nil {
		return out, err
	}
	if b == nil {
		return out, nil
	}
	out.Bound = true
	out.Session = b.Session
	out.Harness = b.Harness
	out.Lane = b.Lane
	out.RunAttemptID = b.RunAttemptID
	if runner != nil {
		st, stErr := runner.SessionStatus(b.Session)
		if stErr != nil {
			out.ProbeError = stErr.Error()
		} else {
			out.PaneStatus = st
		}
	}
	if b.RunAttemptID != "" {
		raStore := runattempt.Open(s.home)
		ra, gerr := raStore.Get(b.RunAttemptID)
		if gerr != nil {
			return out, fmt.Errorf("delegate status: load run attempt %s: %w", b.RunAttemptID, gerr)
		}
		out.RunAttemptStatus = ra.Status
		out.Workspace = ra.Workspace
		// Resolve where this run's transcript lives while we already hold the
		// binding and the workspace. Without this the operator is left to guess
		// which profile home holds it, and a wrong guess reports a completed run
		// as having produced nothing.
		if DelegateSessionStoreResolver != nil {
			root, rErr := DelegateSessionStoreResolver(b.Harness, b.Lane)
			if rErr != nil {
				out.TranscriptError = rErr.Error()
			} else {
				dir, files, exists, tErr := TranscriptsFor(root, ra.Workspace)
				out.TranscriptDir = dir
				out.Transcripts = files
				out.TranscriptDirExists = exists
				if tErr != nil {
					out.TranscriptError = tErr.Error()
				}
			}
		} else {
			out.TranscriptError = ErrSessionStoreRootUnknown.Error() + " (no resolver installed)"
		}
	}
	return out, nil
}

// DelegateStatusObservation is the read-only, JSON-friendly payload of
// `compound work delegate status`.
type DelegateStatusObservation struct {
	WorkID           string        `json:"work_id"`
	Bound            bool          `json:"bound"`
	Session          string        `json:"session,omitempty"`
	Harness          string        `json:"harness,omitempty"`
	Lane             string        `json:"lane,omitempty"`
	RunAttemptID     string        `json:"run_attempt_id,omitempty"`
	RunAttemptStatus string        `json:"run_attempt_status,omitempty"`
	PaneStatus       SessionStatus `json:"pane_status"`
	ProbeError       string        `json:"probe_error,omitempty"`
	// Workspace is the filesystem root the run used; it is also what keys the
	// harness's per-project transcript directory.
	Workspace string `json:"workspace,omitempty"`
	// TranscriptDir / Transcripts answer "where did this delegated run's output
	// go" as a lookup. TranscriptDirExists separates "ran and wrote nothing"
	// from "we are looking in the wrong place"; TranscriptError carries the
	// reason when the lane's profile home cannot be named, because guessing one
	// misreports a completed run as empty.
	TranscriptDir       string   `json:"transcript_dir,omitempty"`
	Transcripts         []string `json:"transcripts,omitempty"`
	TranscriptDirExists bool     `json:"transcript_dir_exists"`
	TranscriptError     string   `json:"transcript_error,omitempty"`
}

// DelegateProgressResult is the content-free progress receipt returned after
// resolving a work's active delegated binding to its exact RunAttempt.
type DelegateProgressResult struct {
	WorkID           string                 `json:"work_id"`
	RunAttemptID     string                 `json:"run_attempt_id"`
	Progress         runattempt.RunProgress `json:"progress"`
	HeartbeatAt      string                 `json:"heartbeat_at"`
	RunAttemptStatus string                 `json:"run_attempt_status"`
}

// DelegateProgress records cumulative progress on the active bound attempt.
// Callers identify work, never a raw ratt id, so sibling claim reservations
// cannot receive a delegated worker's heartbeat.
func (s *Store) DelegateProgress(workID string, progress runattempt.RunProgress, now time.Time) (DelegateProgressResult, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return DelegateProgressResult{}, errors.New("delegate progress: WorkID is required")
	}
	binding, alive, err := s.recoverBoundSession(workID)
	if err != nil {
		return DelegateProgressResult{}, err
	}
	if !alive || binding == nil || strings.TrimSpace(binding.RunAttemptID) == "" {
		return DelegateProgressResult{}, fmt.Errorf("delegate progress: no active bound RunAttempt for work %s", workID)
	}
	raStore := runattempt.Open(s.home)
	ra, err := raStore.Get(binding.RunAttemptID)
	if err != nil {
		return DelegateProgressResult{}, fmt.Errorf("delegate progress: load RunAttempt %s: %w", binding.RunAttemptID, err)
	}
	if ra.Status != runattempt.StatusRunning {
		return DelegateProgressResult{}, fmt.Errorf("delegate progress: RunAttempt %s is %s, want running", ra.ID, ra.Status)
	}
	updated, err := raStore.HeartbeatProgress(ra.ID, now, progress)
	if err != nil {
		return DelegateProgressResult{}, err
	}
	return DelegateProgressResult{
		WorkID:           workID,
		RunAttemptID:     updated.ID,
		Progress:         *updated.Progress,
		HeartbeatAt:      updated.LastHeartbeatAt.UTC().Format(time.RFC3339Nano),
		RunAttemptStatus: updated.Status,
	}, nil
}

// DelegateSweepOutcome is the per-binding result of DelegateSweep.
type DelegateSweepOutcome struct {
	WorkID           string `json:"work_id"`
	Session          string `json:"session"`
	RunAttemptID     string `json:"run_attempt_id,omitempty"`
	RunAttemptStatus string `json:"run_attempt_status,omitempty"`
	ObservedExit     string `json:"observed_exit"`
	TerminalStatus   string `json:"terminal_status"`
	KillError        string `json:"kill_error,omitempty"`
}
