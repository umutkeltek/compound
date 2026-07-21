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
//     regardless. Slice 1 deliberately does NOT mint a delegate-specific
//     RunAttempt terminalization -- that is reported as next slice because
//     current file boundaries exclude it.
//   - One live session per work. A second delegate on a live session
//     REFUSES with reason=session_live and the existing session returned.
//   - Lifecycle events, not logs. Each verb emits a structured eventstore
//     record under compound.work_delegate_event.v0.1 with kind ∈
//     work.delegate.{spawned,refused,steered,exited}. Event payloads NEVER
//     carry prompt text or command stdout (privacy law of the packet).
//   - Dry-run runs the guard + binding + tmux-available checks, prints the
//     decision, and writes NO event record (preview-only).
//   - Named lane aliases (claude-zai, claude-qwen, claude-minimax, claude,
//     codex) resolve to the same wrapper executable name via a static
//     function table -- NO durable registry under COMPOUND_HOME.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	delegateEventSchemaVersion = "compound.work_delegate_event.v0.1"
	delegateSpawnFailureNote   = "session runner could not create the governed session"
)

// SessionRunner is the seam between the orchestrator and any backing
// supervised-execution backend (tmux in production; a fake in tests).
type SessionRunner interface {
	Available() error
	Has(name string) (bool, error)
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

type DelegateResult struct {
	SchemaVersion string                   `json:"schema_version"`
	WorkID        string                   `json:"work_id"`
	Lane          string                   `json:"lane,omitempty"`
	Status        string                   `json:"status"` // spawned|refused|preview
	Binding       *DelegateBinding         `json:"binding,omitempty"`
	Refusal       *DelegateRefusal         `json:"refusal,omitempty"`
	Decision      *LaneCapacityGuardResult `json:"decision,omitempty"`
	EventID       string                   `json:"event_id,omitempty"`
	GeneratedAt   string                   `json:"generated_at"`
}

// DelegateBinding is the SYNTHESIZED view of the bound session. It is built
// from the newest attached work.delegate.spawned eventstore record (not
// from a sidecar file). The orchestrator returns it to the verb layer so
// the operator can print a single "session: <name>" hint.
type DelegateBinding struct {
	WorkID        string `json:"work_id"`
	Session       string `json:"session"`
	Harness       string `json:"harness"`
	Lane          string `json:"lane,omitempty"`
	Holder        string `json:"holder,omitempty"`
	CapacityState string `json:"capacity_state,omitempty"`
	RecordedAt    string `json:"recorded_at,omitempty"`
	WorkSHA256    string `json:"work_sha256,omitempty"`
}

type DelegateRefusal struct {
	Reason  string `json:"reason"` // walled|dead|expiring_unconfirmed|not_claimed|session_live|tmux_absent|gate_absent|harness_unknown|spawn_failed
	Message string `json:"message,omitempty"`
	Session string `json:"session,omitempty"` // session_live: the existing session name
}

// Harness wrapper aliases (named-lane aliases). The map is a static
// function-table inside the orchestrator; it is NEVER persisted under
// COMPOUND_HOME -- this is the "no second durable registry" invariant.
var harnessWrapperAliases = map[string]string{
	"claude-zai":     "claude-zai",
	"claude_zai":     "claude-zai",
	"claude-qwen":    "claude-qwen",
	"claude_qwen":    "claude-qwen",
	"claude-minimax": "claude-minimax",
	"claude_minimax": "claude-minimax",
	"claude":         "claude",
	"codex":          "codex",
}

func resolveHarnessWrapper(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	if cmd, ok := harnessWrapperAliases[name]; ok {
		return cmd, true
	}
	return "", false
}

// ResolveDelegateHarness maps a user-facing lane alias to the executable
// wrapper Compound is allowed to launch. It is the cmd layer's only alias
// surface, keeping the function table owned here instead of duplicating it.
func ResolveDelegateHarness(name string) (string, error) {
	if command, ok := resolveHarnessWrapper(name); ok {
		return command, nil
	}
	return "", fmt.Errorf("harness %q does not resolve to a registered wrapper (allowed: %s)", name, knownHarnessNames())
}

func knownHarnessNames() string {
	out := make([]string, 0, len(harnessWrapperAliases))
	for k := range harnessWrapperAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
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
type delegateEvent struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	TS            string `json:"ts"`
	WorkID        string `json:"work_id"`
	Harness       string `json:"harness"`
	Lane          string `json:"lane,omitempty"`
	Holder        string `json:"holder,omitempty"`
	CapacityState string `json:"capacity_state,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Message       string `json:"message,omitempty"`
	Confirm       bool   `json:"confirm,omitempty"`
	Session       string `json:"session,omitempty"`
	WorkSHA256    string `json:"work_sha256,omitempty"`
}

func delegateEventID(kind, workID string, now time.Time) string {
	h := sha256.Sum256([]byte(strings.Join([]string{kind, workID, ts(now)}, "|")))
	return "wde_" + hex.EncodeToString(h[:])[:16]
}

// recoverBoundSession walks the work's lifecycle events oldest-first,
// reading every attached wde_* record through the existing eventstore API,
// and synthesizes the current binding. A work.delegate.spawned sets the
// binding; a work.delegate.exited clears it. After the walk, a non-nil
// binding is alive BY EVENTLOG; the caller still probes SessionRunner.Has
// because the runner is the liveness source of truth.
//
// Returns (nil, false, nil) when no binding exists. The second return is
// the alive flag (eventlog view); Has at use time may still differ.
func (s *Store) recoverBoundSession(workID string) (*DelegateBinding, bool, error) {
	events, err := s.eventsFor(workID)
	if err != nil {
		return nil, false, err
	}
	var binding *DelegateBinding
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
				}
			case "work.delegate.exited":
				binding = nil // any active binding dies
			case "work.delegate.refused", "work.delegate.steered":
				// These are binding-neutral but still valid delegate events.
			}
		}
	}
	if binding == nil {
		return nil, false, nil
	}
	return binding, true, nil
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
	if strings.TrimSpace(opts.Command) == "" {
		return out, errors.New("delegate: Command is required")
	}
	if runner == nil {
		return out, errors.New("delegate: SessionRunner is nil")
	}
	resolvedHarness, ok := resolveHarnessWrapper(opts.Harness)
	if !ok {
		out.Refusal = &DelegateRefusal{
			Reason:  "harness_unknown",
			Message: fmt.Sprintf("harness %q does not resolve to a registered wrapper (allowed: %s)", opts.Harness, knownHarnessNames()),
		}
		return out, nil
	}
	out.Lane = resolvedHarness

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
	laneForGuard := resolvedHarness
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

	decision, err := runLaneCapacityGuard(LaneCapacityGuard, LaneCapacityGuardRequest{
		HomePath:        s.home,
		Lane:            laneForGuard,
		WorkID:          out.WorkID,
		ExpectedMinutes: dispatchExpectedMinutes(opts.ExpectedMinutes),
		Confirm:         opts.Confirm,
		ReadOnly:        opts.DryRun,
		Now:             opts.Now,
	})
	if err != nil {
		return out, fmt.Errorf("delegate: guard lane capacity: %w", err)
	}
	out.Decision = &decision

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
	// Existing binding? Probe liveness; if live, refuse session_live; if
	// not live, record .exited and proceed with a fresh spawn on the SAME
	// session name (deterministic so a steer-while-dead can re-attach).
	existing, _, rerr := s.recoverBoundSession(out.WorkID)
	if rerr != nil {
		return out, rerr
	}
	if existing != nil {
		live, hasErr := runner.Has(existing.Session)
		if hasErr != nil {
			return out, fmt.Errorf("delegate: probe session %s: %w", existing.Session, hasErr)
		}
		if live {
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
		if _, err := s.recordAndAttachDelegateExited(out.WorkID, w.State, existing, holder, opts.Now); err != nil {
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

	if opts.DryRun {
		out.Status = "preview"
		return out, nil
	}
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

	workSHA, shaErr := workEnvelopeSHA256(w)
	if shaErr != nil {
		return out, shaErr
	}
	if err := runner.Create(sessionName, workspace, opts.Command, opts.Args); err != nil {
		// SessionRunner is an adapter seam; do not trust its error text to be
		// metadata-only. Some process libraries echo argv, which may contain
		// the operator's complete prompt. Keep the durable refusal opaque.
		evID, eerr := s.recordDelegateRefused(out.WorkID, resolvedHarness, laneForGuard, holder, "spawn_failed", delegateSpawnFailureNote, opts.Now)
		if eerr != nil {
			return out, eerr
		}
		if err := s.appendDelegateLifecycleEvent(out.WorkID, w.State, holder, evID, "delegate refused reason=spawn_failed", opts.Now); err != nil {
			return out, err
		}
		out.EventID = evID
		out.Refusal = &DelegateRefusal{Reason: "spawn_failed", Message: delegateSpawnFailureNote}
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
		WorkSHA256:    workSHA,
	}
	evID, evErr := s.recordDelegateSpawned(out.WorkID, binding, opts.Confirm, opts.Now)
	if evErr != nil {
		return out, killSpawnedSession(runner, sessionName, evErr)
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
		return out, killSpawnedSession(runner, sessionName, err)
	}
	out.Status = "spawned"
	out.Binding = binding
	out.EventID = evID
	return out, nil
}

// --- record helpers (writeEventJSON, D7 contract) ---

func (s *Store) recordDelegateSpawned(workID string, b *DelegateBinding, confirm bool, now time.Time) (string, error) {
	id := delegateEventID("work.delegate.spawned", workID, now)
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

func (s *Store) recordDelegateExited(workID string, b *DelegateBinding, now time.Time) (string, error) {
	id := delegateEventID("work.delegate.exited", workID, now)
	if err := s.writeEventJSON(id, delegateEvent{
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
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) recordAndAttachDelegateExited(workID, state string, b *DelegateBinding, observer string, now time.Time) (string, error) {
	id, err := s.recordDelegateExited(workID, b, now)
	if err != nil {
		return "", err
	}
	if err := s.appendDelegateLifecycleEvent(workID, state, observer, id, fmt.Sprintf("delegate exited session %s", b.Session), now); err != nil {
		return "", err
	}
	return id, nil
}

func killSpawnedSession(runner SessionRunner, session string, cause error) error {
	if killErr := runner.Kill(session); killErr != nil {
		return errors.Join(cause, fmt.Errorf("delegate: kill unrecorded session %s: %w", session, killErr))
	}
	return cause
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
	live, hasErr := runner.Has(binding.Session)
	if hasErr != nil {
		return out, fmt.Errorf("steer: probe session %s: %w", binding.Session, hasErr)
	}
	if !live {
		w, werr := s.Get(out.WorkID)
		if werr != nil {
			return out, werr
		}
		if _, exitErr := s.recordAndAttachDelegateExited(out.WorkID, w.State, binding, holder, opts.Now); exitErr != nil {
			return out, exitErr
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
	live, hasErr := runner.Has(binding.Session)
	if hasErr != nil {
		return out, fmt.Errorf("attach: probe session %s: %w", binding.Session, hasErr)
	}
	out.Live = live
	if !live {
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
