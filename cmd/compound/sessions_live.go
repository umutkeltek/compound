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
//
// Each row carries Provenance so the operator can tell which observation
// source contributed which columns:
//
//	"compound"    — the row's state and identity come from a recorded
//	                 RunAttempt binding. CWD / LastActivity / ResumeCmd may
//	                 be enriched from the multiplexer source when the same
//	                 session name appears there.
//	"multiplexer" — the row exists only because the terminal multiplexer
//	                 (tmux) reported a session that compound never recorded.
//	                 WorkID and RunAttemptID are empty; the row's identity
//	                 is the session name itself.
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

	// Provenance is "compound" for rows whose state comes from a recorded
	// RunAttempt, "multiplexer" for rows that exist only because the
	// terminal multiplexer reported the session. Always present.
	Provenance string `json:"provenance"`
	// Lane is the best-effort lane identity. For compound rows it is
	// "<harness>/<actor>"; for multiplexer rows it falls back to the
	// session name, which is the only signal the multiplexer itself
	// carries. Empty string is allowed for unbound compound rows.
	Lane string `json:"lane,omitempty"`
	// CWD is the focused pane's working directory, observed via the
	// multiplexer source. Empty for compound rows whose session the
	// multiplexer did not see (a server crash, a different tmux server,
	// etc).
	CWD string `json:"cwd,omitempty"`
	// LastActivity is the multiplexer's session_activity, formatted as
	// RFC3339 in UTC. Empty when the multiplexer did not observe the
	// pane (same conditions as CWD).
	LastActivity string `json:"last_activity,omitempty"`
	// ResumeCmd is the argv an operator can paste to put this pane back
	// on screen. For compound-bound rows this is the compound attach
	// command (`compound work delegate attach <work_id>` when a binding
	// exists); for multiplexer-only rows it is `tmux attach-session -t
	// <name>`. Empty for unbound compound rows with no multiplexer match.
	ResumeCmd string `json:"resume_cmd,omitempty"`
}

type sessionsLiveReport struct {
	SchemaVersion string `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	Live          int    `json:"live"`
	Ghost         int    `json:"ghost"`
	Unbound       int    `json:"unbound"`
	Reservation   int    `json:"reservation"`
	Unknown       int    `json:"unknown"`
	// Multiplexer counts rows whose identity came from the terminal
	// multiplexer source rather than a recorded RunAttempt. These panes
	// are not compound-spawned, so they are NOT counted in Live/Ghost/
	// Unbound/Reservation/Unknown — they are a separate population with
	// a separate provenance.
	Multiplexer int              `json:"multiplexer"`
	Rows        []sessionLiveRow `json:"rows"`
}

func cmdSessionsLive(args []string) {
	if code := runSessionsLive(args, os.Stdout, os.Stderr, time.Now()); code != 0 {
		os.Exit(code)
	}
}

func runSessionsLive(args []string, stdout, stderr io.Writer, now time.Time) int {
	return runSessionsLiveWith(args, stdout, stderr, now, nil, nil)
}

// runSessionsLiveWith takes the session prober and the multiplexer
// observer so tests can drive every classification against private tmux
// servers without touching the operator's real tmux. nil runner means a
// real tmux runner; nil mux means a real multiplexer source bound to
// that runner (so socketPrefix / test seams still apply).
func runSessionsLiveWith(args []string, stdout, stderr io.Writer, now time.Time, runner work.SessionRunner, mux work.MultiplexerSource) int {
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
	// Default the multiplexer source to a tmux-backed one bound to the
	// same runner so a test that injects a private socket (and so a
	// pinned server) sees multiplexer observations from THAT server,
	// not the operator's. nil mux is "use the runner's Multiplexer()";
	// a non-nil mux (e.g. a stub in a unit test) wins.
	if mux == nil {
		if tmuxRunner, ok := runner.(*work.TmuxRunner); ok {
			mux = tmuxRunner.Multiplexer()
		}
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
			Provenance:   "compound",
		}
		row.Lane = compoundLaneIdentity(ra.Harness, ra.Actor)

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

	// Second observation source: every tmux session the multiplexer itself
	// can see. Rows that match a recorded binding by session name are
	// merged INTO the existing compound row (provenance stays "compound";
	// cwd / last_activity / resume_cmd come from the multiplexer). Rows
	// the multiplexer sees but compound never recorded become their own
	// rows with provenance="multiplexer" — these are the operator's own
	// panes (claude, codex, manual work) that the report used to miss.
	//
	// The merge is deliberately one-row-per-pane. Two rows for the same
	// pane would force the operator to pick which one to act on, and at
	// least one pick would be wrong.
	if mux != nil {
		observations, muxErr := mux.List()
		if muxErr != nil {
			// A broken multiplexer source is a probe failure for the
			// whole multiplexer column, NOT a reason to abort the report.
			// Report the failure once via a multiplexer-only row whose
			// detail names the underlying error so the operator sees
			// "the multiplexer did not respond" instead of "nothing
			// multiplexer-reported".
			report.Rows = append(report.Rows, sessionLiveRow{
				State:      "multiplexer_unknown",
				Provenance: "multiplexer",
				Session:    "-",
				Detail:     "multiplexer source failed: " + muxErr.Error(),
			})
			report.Multiplexer++
		} else {
			matched := map[string]bool{}
			for _, obs := range observations {
				idx := findRowBySession(report.Rows, obs.Name)
				if idx >= 0 {
					applyMultiplexerToCompoundRow(&report.Rows[idx], obs)
					matched[obs.Name] = true
					continue
				}
				report.Rows = append(report.Rows, multiplexerOnlyRow(obs))
				report.Multiplexer++
			}
			_ = matched // reserved for future "matched but unbound" reporting
		}
	}

	rank := map[string]int{"ghost": 0, "unknown": 1, "unbound": 2, "live": 3, "reservation": 4, "multiplexer": 5, "multiplexer_unknown": 6}
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
	fmt.Fprintf(stdout, "%-12s %-16s %-30s %8s  %-44s\n", "STATE", "HARNESS", "SESSION", "AGE", "WORK")
	for _, r := range report.Rows {
		session := r.Session
		if session == "" {
			session = "-"
		}
		harness := r.Harness
		if harness == "" && r.Provenance == "multiplexer" {
			harness = r.Lane
		}
		age := fmt.Sprintf("%7.1fh", r.AgeHours)
		if r.AgeHours == 0 && r.LastActivity != "" {
			if parsed, err := time.Parse(time.RFC3339, r.LastActivity); err == nil {
				age = fmt.Sprintf("%7.1fh", now.Sub(parsed).Hours())
			} else {
				age = "-"
			}
		}
		work := r.WorkID
		if work == "" {
			work = "-"
		}
		fmt.Fprintf(stdout, "%-12s %-16s %-30s %s  %-44s\n",
			r.State, clipRunes(harness, 16), clipRunes(session, 30), age, clipRunes(work, 44))
		if r.CWD != "" || r.LastActivity != "" || r.ResumeCmd != "" {
			parts := []string{}
			if r.CWD != "" {
				parts = append(parts, "cwd="+r.CWD)
			}
			if r.LastActivity != "" {
				parts = append(parts, "last="+r.LastActivity)
			}
			if r.ResumeCmd != "" {
				parts = append(parts, "resume="+r.ResumeCmd)
			}
			fmt.Fprintf(stdout, "             └─ %s\n", strings.Join(parts, " "))
		}
	}
	fmt.Fprintf(stdout, "\nlive=%d ghost=%d unbound=%d reservation=%d unknown=%d multiplexer=%d\n",
		report.Live, report.Ghost, report.Unbound, report.Reservation, report.Unknown, report.Multiplexer)
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

// compoundLaneIdentity builds the lane string shown on every compound row
// from the harness and actor recorded at spawn. Empty parts are dropped
// so a row with no actor is not "codex/" with a trailing slash.
func compoundLaneIdentity(harness, actor string) string {
	harness = strings.TrimSpace(harness)
	actor = strings.TrimSpace(actor)
	switch {
	case harness == "" && actor == "":
		return ""
	case actor == "":
		return harness
	case harness == "":
		return actor
	default:
		return harness + "/" + actor
	}
}

// findRowBySession returns the index of the first row whose Session
// matches the given name, or -1 if none. Used by the multiplexer merge
// to pair multiplexer observations with compound rows by session name.
// It is intentionally substring-free — the prototype of this tool matched
// actor name against session name and called healthy lanes ghosts.
func findRowBySession(rows []sessionLiveRow, name string) int {
	name = strings.TrimSpace(name)
	if name == "" {
		return -1
	}
	for i := range rows {
		if rows[i].Session == name {
			return i
		}
	}
	return -1
}

// applyMultiplexerToCompoundRow enriches an existing compound row with
// the columns only the multiplexer source can answer: cwd, last activity,
// and the resume command. Provenance stays "compound" — the row's
// identity still comes from the recorded RunAttempt; the multiplexer
// just observed the same pane.
//
// Existing values are not overwritten: a compound row's ResumeCmd may
// already point at `compound work delegate attach <work_id>`, which is a
// higher-level command than the multiplexer-resume `tmux attach-session
// -t <name>` and is the one an operator under compound control wants.
func applyMultiplexerToCompoundRow(row *sessionLiveRow, obs work.MultiplexerObservation) {
	if row == nil {
		return
	}
	if row.CWD == "" {
		row.CWD = obs.CWD
	}
	if row.LastActivity == "" && !obs.LastActivity.IsZero() {
		row.LastActivity = obs.LastActivity.UTC().Format(time.RFC3339)
	}
	if row.ResumeCmd == "" && obs.Name != "" {
		row.ResumeCmd = "tmux attach-session -t " + obs.Name
	}
}

// multiplexerOnlyRow builds the row emitted for a tmux session that
// compound never recorded. State is "multiplexer" (a separate bucket
// that does NOT inflate Live/Ghost/Unbound/Reservation/Unknown), and the
// ResumeCmd is the raw `tmux attach-session -t <name>` the operator can
// paste to put the pane back.
func multiplexerOnlyRow(obs work.MultiplexerObservation) sessionLiveRow {
	row := sessionLiveRow{
		State:      "multiplexer",
		Provenance: "multiplexer",
		Session:    obs.Name,
		Lane:       obs.Name,
		CWD:        obs.CWD,
	}
	if !obs.LastActivity.IsZero() {
		row.LastActivity = obs.LastActivity.UTC().Format(time.RFC3339)
	}
	if obs.Name != "" {
		row.ResumeCmd = "tmux attach-session -t " + obs.Name
	}
	return row
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
