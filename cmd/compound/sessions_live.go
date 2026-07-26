package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

const sessionsLiveUsage = `usage: compound sessions live [--json] [--ghosts-only] [--home DIR]

Reconciles what run attempts CLAIM about their liveness against what is
OBSERVABLE right now, and names the disagreement.

  live         bound session is recorded AND observed running
  ghost        attempt is non-terminal but its recorded session is gone
  unbound      non-terminal execution with no recorded session binding
  reservation  claim-time placeholder; not a session, never judged as one
  unknown      the probe failed; no terminal state is guessed`

// sessionLiveRow is one reconciled run attempt.
//
// Liveness comes from the session name RECORDED AT SPAWN
// (DelegateBinding.Session, via DelegateStatus) and a direct probe of THAT
// name. It is never inferred from similarity between an actor name and a
// session name. A prototype that matched actor "claude-minimax:wave10-contract"
// against session "mmx-w10-contract" called four healthy lanes ghosts; the same
// guess, wired to a reaper, would have reclaimed leases from a live controller
// whose sessions are named after their work rather than their owner.
type sessionLiveRow struct {
	State        string  `json:"state"`
	WorkID       string  `json:"work_id"`
	RunAttemptID string  `json:"run_attempt_id"`
	Actor        string  `json:"actor,omitempty"`
	Harness      string  `json:"harness,omitempty"`
	Session      string  `json:"session,omitempty"`
	Status       string  `json:"run_attempt_status"`
	AgeHours     float64 `json:"age_hours"`
	PaneExists   bool    `json:"pane_exists"`
	PaneDead     bool    `json:"pane_dead"`
	Detail       string  `json:"detail,omitempty"`
}

type sessionsLiveReport struct {
	SchemaVersion string           `json:"schema_version"`
	GeneratedAt   string           `json:"generated_at"`
	Live          int              `json:"live"`
	Ghost         int              `json:"ghost"`
	Unbound       int              `json:"unbound"`
	Reservation   int              `json:"reservation"`
	Unknown       int              `json:"unknown"`
	Rows          []sessionLiveRow `json:"rows"`
}

func cmdSessionsLive(args []string) {
	if code := runSessionsLive(args, os.Stdout, os.Stderr, time.Now()); code != 0 {
		os.Exit(code)
	}
}

func runSessionsLive(args []string, stdout, stderr io.Writer, now time.Time) int {
	return runSessionsLiveWith(args, stdout, stderr, now, nil)
}

// runSessionsLiveWith takes the session prober so tests can drive every
// classification without a tmux server. nil means the real tmux runner.
func runSessionsLiveWith(args []string, stdout, stderr io.Writer, now time.Time, runner work.SessionRunner) int {
	jsonOut, ghostsOnly, homeFlag := false, false, ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json":
			jsonOut = true
		case "--ghosts-only":
			ghostsOnly = true
		case "--home":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(stderr, sessionsLiveUsage)
				return 2
			}
			homeFlag = strings.TrimSpace(args[i])
		case "-h", "--help":
			fmt.Fprintln(stdout, sessionsLiveUsage)
			return 0
		default:
			fmt.Fprintln(stderr, sessionsLiveUsage)
			return 2
		}
	}

	homePath, err := home.Resolve(homeFlag)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err, "(run `compound home init`)")
		return 1
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	// Non-terminal attempts are exactly the population that CLAIMS to be alive;
	// everything terminal has already settled and needs no reconciliation.
	attempts, err := runattempt.Open(homePath).ListNonTerminal()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	if runner == nil {
		runner = work.NewTmuxRunner()
	}
	report := sessionsLiveReport{
		SchemaVersion: "compound.sessions_live.v0.1",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
	}
	// One probe per work id: several attempts can share an envelope, and the
	// binding is a property of the envelope, not of the attempt.
	probed := map[string]work.DelegateStatusObservation{}

	for _, ra := range attempts {
		row := sessionLiveRow{
			WorkID:       ra.WorkID,
			RunAttemptID: ra.ID,
			Actor:        ra.Actor,
			Harness:      ra.Harness,
			Status:       ra.Status,
			AgeHours:     now.Sub(ra.CreatedAt).Hours(),
		}

		// A claim-time placeholder is not a session, and judging it as one is
		// how a single status came to mean both "died" and "worked".
		if ra.Origin == runattempt.OriginClaimReservation {
			row.State = "reservation"
			row.Detail = "claim-time placeholder; superseded when a real execution attaches"
			report.Reservation++
			report.Rows = append(report.Rows, row)
			continue
		}

		obs, ok := probed[ra.WorkID]
		if !ok {
			obs, err = store.DelegateStatus(ra.WorkID, runner)
			if err != nil {
				obs = work.DelegateStatusObservation{WorkID: ra.WorkID, ProbeError: err.Error()}
			}
			probed[ra.WorkID] = obs
		}
		row.Session = obs.Session
		row.PaneExists = obs.PaneStatus.Exists
		row.PaneDead = obs.PaneStatus.PaneDead
		row.State, row.Detail = classifySessionLiveness(ra.Status, obs)
		switch row.State {
		case "live":
			report.Live++
		case "ghost":
			report.Ghost++
		case "unbound":
			report.Unbound++
		case "unknown":
			report.Unknown++
		}
		report.Rows = append(report.Rows, row)
	}

	rank := map[string]int{"ghost": 0, "unknown": 1, "unbound": 2, "live": 3, "reservation": 4}
	sort.SliceStable(report.Rows, func(i, j int) bool {
		ri, rj := rank[report.Rows[i].State], rank[report.Rows[j].State]
		if ri != rj {
			return ri < rj
		}
		return report.Rows[i].AgeHours > report.Rows[j].AgeHours
	})

	if ghostsOnly {
		kept := make([]sessionLiveRow, 0, len(report.Rows))
		for _, r := range report.Rows {
			if r.State == "ghost" {
				kept = append(kept, r)
			}
		}
		report.Rows = kept
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}

	if len(report.Rows) == 0 {
		fmt.Fprintln(stdout, "no non-terminal run attempts")
		return 0
	}
	fmt.Fprintf(stdout, "%-12s %-16s %-30s %8s  %s\n", "STATE", "HARNESS", "SESSION", "AGE", "WORK")
	for _, r := range report.Rows {
		session := r.Session
		if session == "" {
			session = "-"
		}
		fmt.Fprintf(stdout, "%-12s %-16s %-30s %7.1fh  %s\n",
			r.State, clipRunes(r.Harness, 16), clipRunes(session, 30), r.AgeHours, clipRunes(r.WorkID, 44))
	}
	fmt.Fprintf(stdout, "\nlive=%d ghost=%d unbound=%d reservation=%d unknown=%d\n",
		report.Live, report.Ghost, report.Unbound, report.Reservation, report.Unknown)
	if report.Ghost > 0 {
		fmt.Fprintf(stdout, "\n%d attempt(s) claim to be running with no live session; each still holds its work lease.\n", report.Ghost)
		fmt.Fprintln(stdout, "Confirm the owner is really gone before reclaiming:")
		fmt.Fprintln(stdout, "  compound work delegate status <work_id>")
	}
	return 0
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// classifySessionLiveness decides one attempt's state from its recorded
// binding and a direct probe of the RECORDED session name.
//
// Order is load-bearing and each branch guards a way this has already gone
// wrong:
//   - a failed probe is an ABSENT measurement, never evidence of death. It is
//     checked first so a broken prober can never manufacture ghosts, which
//     wired to a reaper would reclaim leases from healthy owners.
//   - an unbound attempt has no session to be absent, so it cannot be a ghost.
//   - ghost is the DEFAULT only after all three are excluded: the binding
//     exists, the probe worked, and the recorded session is genuinely not
//     running.
func classifySessionLiveness(attemptStatus string, obs work.DelegateStatusObservation) (state, detail string) {
	switch {
	case obs.ProbeError != "":
		return "unknown", "probe failed: " + obs.ProbeError
	case !obs.Bound:
		return "unbound", "non-terminal execution with no recorded session binding"
	case obs.PaneStatus.Exists && !obs.PaneStatus.PaneDead:
		return "live", ""
	default:
		return "ghost", fmt.Sprintf(
			"recorded session %q is gone; attempt still reads %s and its work lease is still held",
			obs.Session, attemptStatus)
	}
}
