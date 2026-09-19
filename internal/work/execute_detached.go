package work

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// R6P (2026-09-11): detached unattended launch.
//
// The synchronous seam runs the lane INSIDE the heartbeat tick, so a job had
// to finish inside the launch wire's 5-minute budget and the tick's own 600 s
// ceiling. Detached mode hands the launch to a separate governed-door process
// (`compound work advance --execute`, the same door every human-dispatched
// lane uses) in its own process group, bounded by its own budget, and returns
// at once. Ownership stays single and durable:
//
//   - the seam CLAIMS the envelope (ready -> claimed) under the tick actor and
//     writes one sidecar record (DetachedLaunch) BEFORE spawning;
//   - the door advances claimed -> running, mints the RunAttempt, runs the
//     lane, and is the ONLY writer of the terminal running -> verifying/failed
//     transition (FinalizeGovernedHarnessRun) — the seam never finalizes;
//   - later ticks reconcile: a door that exited after finalizing is recorded
//     as door_finalized; a door that died while the envelope was still
//     claimed (never reached running) is recorded as door_lost and the
//     envelope closed failed (one bad launch fails that envelope only); a
//     door that died while running is left to the RunAttempt sweep, which
//     already owns stale-attempt recovery, and is reported as door_lost_running.
//
// Why a second tick cannot double-launch: the seam only ever picks `ready`
// candidates, and the envelope leaves `ready` under the lifecycle lock at
// claim time, before the door exists. Why a crash cannot double-finalize: the
// seam has no finalize path in detached mode, and the door's finalize is the
// existing CAS-guarded one.

// DetachedLaunchSchemaVersion is the sidecar record schema.
const DetachedLaunchSchemaVersion = "compound.detached_launch.v0.1"

// Detached-launch outcomes. `reserved` is written BEFORE the door process
// starts (the durable launch identity); `running` after a successful start;
// the rest are stamped by ReconcileDetachedLaunches.
const (
	DetachedOutcomeReserved        = "reserved"
	DetachedOutcomeRunning         = "running"
	DetachedOutcomeDoorFinalized   = "door_finalized"
	DetachedOutcomeDoorLost        = "door_lost"
	DetachedOutcomeDoorLostRunning = "door_lost_running"
)

// DetachedDoorStartedLogLine is the log prefix the launcher appends AFTER
// cmd.Start succeeded, carrying the pid/pgid. It is the recovery evidence a
// reconcile tick adopts when the sidecar write failed after start: the log
// file handle was opened before the start, so the pid line usually survives
// the same filesystem failure that ate the sidecar.
const DetachedDoorStartedLogLine = "detached door started pid="

// DetachedDoorLaunchMarker is the per-launch boundary the launcher writes
// to the log BEFORE the started-pid line. A new launcher appends one
// before each start. It was the whole binding in the 2026-09-16 repair —
// recovery scoped its pid read to lines after the most recent marker — and
// it is still the binding for a sidecar written before launch ids existed.
// Marker order alone cannot say WHICH launch a pid belongs to, so a marker
// now also carries DetachedDoorLaunchIDField.
const DetachedDoorLaunchMarker = "detached door launch marker work="

// DetachedDoorLaunchIDField is the log field that both the launch marker and
// the started-pid line carry: the ONE identity minted when the reservation is
// written. The marker alone only ever said "some launch of this workID
// started here", which is the wrong launch whenever a reconcile lands between
// a new reservation and its launcher's first log line — so recovery binds a
// pid to this id instead (audit 2026-09-19).
const DetachedDoorLaunchIDField = "launch="

// NewDetachedLaunchID mints the identity one detached launch carries from
// reservation through start to recovery. It must be unique per launch and a
// single whitespace-free log field: it is read back out of a log line by
// position-independent field match.
func NewDetachedLaunchID(now time.Time) string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		// A clock-only id still separates two launches of the same work:
		// the reservation is written before the start, never concurrently.
		return fmt.Sprintf("dl_%d", now.UTC().UnixNano())
	}
	return fmt.Sprintf("dl_%d_%x", now.UTC().UnixNano(), suffix[:])
}

// DetachedReservationGrace bounds how long a reserved sidecar can sit
// without a started pid before the reconciler is allowed to classify
// the envelope as door_lost. The grace window must outlast a normal
// launcher start (which holds the door log fd open, spawns the door
// process, and writes the running sidecar), so a launcher mid-flight
// is not closed by its own reconcile (audit 2026-09-16 finding 5).
const DetachedReservationGrace = 5 * time.Minute

// DetachedLaunchRecordError is the typed outcome for a door process that DID
// start but whose sidecar could not be recorded. A started pid is never a
// no-side-effect refusal: the caller must treat the door as owning the
// envelope (it finalizes it) and must not close the work as failed-to-spawn.
// The launch carries the observed identity so the caller and later ticks can
// observe it even without a durable sidecar.
type DetachedLaunchRecordError struct {
	Launch DetachedLaunch
	Err    error
}

func (e *DetachedLaunchRecordError) Error() string {
	return fmt.Sprintf("detached launch: door pid %d started but the sidecar could not be written: %v", e.Launch.PID, e.Err)
}

func (e *DetachedLaunchRecordError) Unwrap() error { return e.Err }

// DetachedLaunch is the durable sidecar the seam writes for one detached
// launch. It is the runtime identity a human needs to observe or cancel the
// run without the heartbeat: pid, process group, log path, exact argv.
type DetachedLaunch struct {
	SchemaVersion string `json:"schema_version"`
	WorkID        string `json:"work_id"`
	// LaunchID is the identity minted with the reservation and stamped into
	// the door log, so a start, a recovery and the launcher's own start guard
	// all name the same launch. Empty on sidecars written before 2026-09-19:
	// those keep marker-scoped recovery.
	LaunchID      string   `json:"launch_id,omitempty"`
	Lane          string   `json:"lane"`
	Actor         string   `json:"actor"`
	PID           int      `json:"pid"`
	PGID          int      `json:"pgid"`
	Command       []string `json:"command"`
	LogPath       string   `json:"log_path"`
	BudgetSeconds int      `json:"budget_seconds,omitempty"`
	StartedAt     string   `json:"started_at"`
	// Stamped by reconciliation; empty while the door is alive.
	ExitObservedAt string `json:"exit_observed_at,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	OutcomeDetail  string `json:"outcome_detail,omitempty"`
}

// DetachedReconcile is one reconciliation verdict, surfaced on the tick.
type DetachedReconcile struct {
	WorkID  string `json:"work_id"`
	PID     int    `json:"pid"`
	Outcome string `json:"outcome"`
	State   string `json:"state,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// executeDetachedLaunchFn spawns the detached door for an already-claimed
// envelope and returns the sidecar identity. nil keeps the synchronous seam.
type executeDetachedLaunchFn func(store *Store, workID, lane, prompt, actor string, now time.Time) (DetachedLaunch, error)

var executeStageDetachedLaunchFn executeDetachedLaunchFn

// SwapExecuteStageDetachedLaunch installs fn as the seam's detached launcher
// (nil restores the synchronous launch) and returns a restore function.
func SwapExecuteStageDetachedLaunch(fn executeDetachedLaunchFn) func() {
	prev := executeStageDetachedLaunchFn
	executeStageDetachedLaunchFn = fn
	return func() { executeStageDetachedLaunchFn = prev }
}

// DetachedLaunchDir is where the seam custodies detached-launch sidecars.
// It is a sibling of the RunAttempt launch-lifecycle evidence directory
// (`run_attempts/launch/`), never inside it: the attempt reader walks that
// directory for its own content-free launch events and treats a foreign
// file there as unreadable evidence.
func DetachedLaunchDir(home string) string {
	return filepath.Join(home, "run_attempts", "detached")
}

func detachedLaunchPath(home, workID string) string {
	return filepath.Join(DetachedLaunchDir(home), workID+".json")
}

// WriteDetachedLaunch persists the sidecar. The record is written whole and
// renamed into place so a reader never sees a torn file.
func WriteDetachedLaunch(home string, launch DetachedLaunch) error {
	if strings.TrimSpace(launch.WorkID) == "" {
		return errors.New("detached launch: work_id is required")
	}
	if launch.SchemaVersion == "" {
		launch.SchemaVersion = DetachedLaunchSchemaVersion
	}
	dir := DetachedLaunchDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(launch, "", "  ")
	if err != nil {
		return err
	}
	tmp := detachedLaunchPath(home, launch.WorkID) + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, detachedLaunchPath(home, launch.WorkID))
}

// ReadDetachedLaunch loads one sidecar.
func ReadDetachedLaunch(home, workID string) (DetachedLaunch, error) {
	var launch DetachedLaunch
	body, err := os.ReadFile(detachedLaunchPath(home, workID))
	if err != nil {
		return launch, err
	}
	if err := json.Unmarshal(body, &launch); err != nil {
		return launch, fmt.Errorf("detached launch %s: %w", workID, err)
	}
	return launch, nil
}

// ListDetachedLaunches returns every sidecar, sorted by work id.
func ListDetachedLaunches(home string) ([]DetachedLaunch, error) {
	entries, err := os.ReadDir(DetachedLaunchDir(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []DetachedLaunch
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		launch, err := ReadDetachedLaunch(home, strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		out = append(out, launch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkID < out[j].WorkID })
	return out, nil
}

// detachedProcessAlive answers whether pid still exists. EPERM means it
// exists under another user, which for our purposes is "alive".
var detachedProcessAlive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// detachedDoorStartedPID recovers the (pid, pgid) a launcher logged after a
// successful start, from the door log. ok=false when the log carries no
// started line (the start never happened, or the log write failed too).
//
// The log is opened with O_APPEND, so a stale "started pid=" line from a
// prior launch can survive. launchID is the reservation's own identity and is
// the exact binding (audit 2026-09-19): when it is set, only a started-pid
// line stamped with that same id may be adopted, so a previous launch's pid
// cannot land in a new reservation no matter how the two interleave — and a
// reservation whose launcher has not logged anything yet simply recovers
// nothing and stays inside its grace window.
//
// An empty launchID is a sidecar written before the id existed. Those keep the
// 2026-09-16 behaviour: scope to the most recent per-launch boundary marker
// and ignore any "started pid=" line that precedes it. The log may by then be
// written by a newer launcher, so the id on those lines is not required to
// match anything — the marker is still the only boundary such a sidecar has.
func detachedDoorStartedPID(logPath, launchID string) (int, int, bool) {
	body, err := os.ReadFile(logPath)
	if err != nil {
		return 0, 0, false
	}
	launchID = strings.TrimSpace(launchID)
	scoped := string(body)
	if launchID == "" {
		scoped = scopeToLastLaunchMarker(scoped)
	}
	for _, line := range strings.Split(scoped, "\n") {
		idx := strings.Index(line, DetachedDoorStartedLogLine)
		if idx < 0 {
			continue
		}
		fields := strings.Fields(line[idx+len(DetachedDoorStartedLogLine):])
		if len(fields) == 0 {
			continue
		}
		if launchID != "" && detachedLogLaunchID(fields) != launchID {
			continue // another launch's pid; this reservation does not own it
		}
		pid, perr := strconv.Atoi(fields[0])
		if perr != nil || pid <= 0 {
			continue
		}
		pgid := pid
		if len(fields) >= 3 && fields[1] == "pgid" {
			if g, gerr := strconv.Atoi(fields[2]); gerr == nil && g > 0 {
				pgid = g
			}
		}
		return pid, pgid, true
	}
	return 0, 0, false
}

// detachedLogLaunchID reads the launch= field off the tail of a door-log line.
// The field is position-independent so the line's older fields keep their
// meaning; it is absent on lines written before 2026-09-19, which read as "no
// identity" and therefore match no reservation that has one.
func detachedLogLaunchID(fields []string) string {
	for _, f := range fields {
		if strings.HasPrefix(f, DetachedDoorLaunchIDField) {
			return strings.TrimPrefix(f, DetachedDoorLaunchIDField)
		}
	}
	return ""
}

// AssertDetachedReservationHeld reports whether launchID still holds workID's
// reservation on disk. The launcher calls this immediately before it starts
// the child, because elapsed time cannot prove a delayed launcher stopped: in
// the gap between the reserved write and the start, a reconcile can classify
// the reservation lost (and close the envelope) or a newer launcher can take
// the workID. Starting a child after either is a door nobody owns, so the
// sidecar — not the clock — decides. A reservation with no id (written before
// 2026-09-19) is held as long as it still reads reserved.
func AssertDetachedReservationHeld(home, workID, launchID string) error {
	held, err := ReadDetachedLaunch(home, workID)
	if err != nil {
		return fmt.Errorf("detached launch: the reservation for %s is gone or unreadable: %w", workID, err)
	}
	if held.Outcome != DetachedOutcomeReserved {
		return fmt.Errorf("detached launch: the reservation for %s is now %q — recovery or another launcher owns it", workID, held.Outcome)
	}
	if launchID != "" && held.LaunchID != launchID {
		return fmt.Errorf("detached launch: the reservation for %s now belongs to launch %q, not %q", workID, held.LaunchID, launchID)
	}
	return nil
}

// scopeToLastLaunchMarker returns the slice of logBody starting at the
// last "detached door launch marker work=<id>" line. Older content
// (including a prior launch's started pid= line) is excluded so a
// failed-sidecar reconcile cannot adopt a stale identity (audit
// 2026-09-16 finding 5). Returns body unchanged when no marker is
// present, so a log without the new marker still recovers a pid when
// the very first launch ever appended one.
func scopeToLastLaunchMarker(logBody string) string {
	idx := strings.LastIndex(logBody, DetachedDoorLaunchMarker)
	if idx < 0 {
		return logBody
	}
	if nl := strings.LastIndex(logBody[:idx], "\n"); nl >= 0 {
		idx = nl + 1
	}
	return logBody[idx:]
}

// reservationInGrace reports whether a reserved sidecar's elapsed time
// since reservation is still inside the launcher grace window. A
// missing StartedAt is treated as in-grace so a malformed sidecar
// never races its own launcher; the operator inspects the sidecar
// before the next reconcile.
func reservationInGrace(launch DetachedLaunch, now time.Time) bool {
	started, err := time.Parse(time.RFC3339, strings.TrimSpace(launch.StartedAt))
	if err != nil {
		return true
	}
	return now.Sub(started) <= DetachedReservationGrace
}

// ReconcileDetachedLaunches walks every open sidecar and records what became
// of its door. It runs at the top of each unattended tick so the lifecycle of
// a launch handed off on tick N is visible on tick N+1 without a human
// reading a log. It mutates an envelope in exactly one case: the door died
// before it ever advanced the envelope to running, which leaves a claimed
// envelope nobody will finish — that envelope is closed failed (audit_only)
// so the bench stays honest and the next tick moves on.
//
// A `reserved` sidecar (written before the start, whose post-start update
// failed) is adopted here: the reconcile reads the started pid line from the
// door log, re-stamps the sidecar, and treats the door as running. A
// reserved record with NO started line and a still-claimed envelope is a
// start that never happened (or whose log line was lost too) — it closes the
// envelope failed exactly like a lost door.
func (s *Store) ReconcileDetachedLaunches(now time.Time) []DetachedReconcile {
	launches, err := ListDetachedLaunches(s.home)
	if err != nil || len(launches) == 0 {
		return nil
	}
	var out []DetachedReconcile
	for _, launch := range launches {
		if launch.Outcome == DetachedOutcomeDoorFinalized || launch.Outcome == DetachedOutcomeDoorLost || launch.Outcome == DetachedOutcomeDoorLostRunning {
			continue // already reconciled
		}
		verdict := DetachedReconcile{WorkID: launch.WorkID, PID: launch.PID}
		if launch.Outcome == DetachedOutcomeReserved && launch.PID <= 0 {
			pid, pgid, ok := detachedDoorStartedPID(launch.LogPath, launch.LaunchID)
			if ok {
				launch.PID, launch.PGID = pid, pgid
				launch.Outcome = DetachedOutcomeRunning
				verdict.PID = pid
				if writeErr := WriteDetachedLaunch(s.home, launch); writeErr != nil {
					verdict.Detail = "adopted started pid from the door log but the sidecar still cannot be written: " + writeErr.Error()
				}
			}
		}
		w, loadErr := s.Get(launch.WorkID)
		if loadErr == nil {
			verdict.State = w.State
		}
		// Reserved launches stay in their reservation grace window. The
		// launcher writes the reserved sidecar BEFORE the door process
		// exists, then opens the log fd, then spawns the door. A reconcile
		// tick that lands inside this window is racing the launcher;
		// treating the envelope as lost here would close a live launch's
		// claim. Only a reservation whose grace has expired (or whose
		// launcher is provably gone) is fair game for door_lost
		// (audit 2026-09-16 finding 5).
		if launch.Outcome == DetachedOutcomeReserved && launch.PID <= 0 && reservationInGrace(launch, now) {
			verdict.Outcome = DetachedOutcomeReserved
			verdict.Detail = fmt.Sprintf("reserved sidecar within grace (%s); launcher in flight, not yet classified lost", DetachedReservationGrace)
			out = append(out, verdict)
			continue
		}
		if detachedProcessAlive(launch.PID) {
			verdict.Outcome = DetachedOutcomeRunning
			verdict.Detail = fmt.Sprintf("door pid %d alive; envelope %s", launch.PID, verdict.State)
			out = append(out, verdict)
			continue
		}
		launch.ExitObservedAt = now.UTC().Format(time.RFC3339)
		switch {
		case loadErr != nil:
			launch.Outcome = DetachedOutcomeDoorLost
			launch.OutcomeDetail = "envelope unreadable: " + loadErr.Error()
		case w.State == "verifying" || w.State == "completed" || w.State == "failed" || w.State == "cancelled":
			launch.Outcome = DetachedOutcomeDoorFinalized
			launch.OutcomeDetail = "door exited after finalizing; envelope " + w.State
		case w.State == "running":
			// The door minted an attempt and died before finalizing. The
			// RunAttempt sweep owns stale-attempt recovery; the seam only
			// names the loss so the operator can run the exact recovery.
			launch.Outcome = DetachedOutcomeDoorLostRunning
			launch.OutcomeDetail = fmt.Sprintf("door pid %d exited while envelope still running; attempts=%v — recover with the run-attempt sweep for the exact attempt", launch.PID, w.RunAttemptIDs)
		default:
			// claimed (or any pre-running state): the door never took the
			// envelope. Close it failed so the bench is not blocked by a
			// claim nobody will finish. Same shape as a synchronous launch
			// failure (one bad launch fails that envelope only).
			launch.Outcome = DetachedOutcomeDoorLost
			launch.OutcomeDetail = fmt.Sprintf("door pid %d exited while envelope still %s (never reached running); log %s", launch.PID, w.State, launch.LogPath)
			if closeErr := s.failClaimedUnattendedEnvelope(launch.WorkID, launch.Actor, "unattended detached launch lost: "+launch.OutcomeDetail, "execute_stage_detached_door_lost", now); closeErr != nil {
				launch.OutcomeDetail += "; close refused: " + closeErr.Error()
			} else {
				verdict.State = "failed"
			}
		}
		_ = WriteDetachedLaunch(s.home, launch)
		verdict.Outcome = launch.Outcome
		verdict.Detail = launch.OutcomeDetail
		out = append(out, verdict)
	}
	return out
}

// detachedReconcileSummary renders reconciliation verdicts for the Human line.
func detachedReconcileSummary(items []DetachedReconcile) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s=%s(pid %d, %s)", item.WorkID, item.Outcome, item.PID, item.State))
	}
	return "detached: " + strings.Join(parts, ", ") + "; "
}

// failClaimedUnattendedEnvelope records a launch that will never run on a
// CLAIMED envelope. The lifecycle table has no claimed -> failed edge (a
// claim is not an execution), so this takes the same two steps the
// synchronous seam takes around a launch failure: the running reservation
// under the same actor, then the failed close with the proof ref. Idempotent
// on an envelope that is already past claimed.
func (s *Store) failClaimedUnattendedEnvelope(workID, actor, summary, proofRef string, now time.Time) error {
	w, err := s.Get(workID)
	if err != nil {
		return err
	}
	if strings.HasPrefix(w.State, "claimed") {
		if _, err := s.Advance(workID, AdvanceInput{To: "running", Actor: actor, Note: "execute stage: running (recording a launch that never ran)"}, now); err != nil {
			return err
		}
	}
	_, err = s.Close(workID, CloseInput{
		Outcome:   "failed",
		MoveType:  "audit_only",
		Actor:     actor,
		Summary:   summary,
		ProofRefs: []string{proofRef},
	}, now)
	return err
}

// DetachedDoorOwns reports whether an ALIVE detached door currently owns the
// envelope: an open sidecar whose pid still exists. The tick's own lifecycle
// stepping (DispatchStep) must not touch such an envelope — the door expects
// to find it exactly as the seam left it (claimed) and refuses otherwise. A
// dead door is not "owning": ReconcileDetachedLaunches records it first.
//
// A `reserved` sidecar (post-start record still missing) reads as OWNED:
// the pid is unknown but a start was armed, and touching the envelope on the
// strength of a missing pid is the one wrong side to fail on.
func DetachedDoorOwns(home, workID string) (DetachedLaunch, bool) {
	launch, err := ReadDetachedLaunch(home, strings.TrimSpace(workID))
	if err != nil {
		return DetachedLaunch{}, false
	}
	if launch.Outcome == DetachedOutcomeReserved {
		return launch, true
	}
	if launch.Outcome != "" && launch.Outcome != DetachedOutcomeRunning {
		return launch, false
	}
	return launch, detachedProcessAlive(launch.PID)
}
