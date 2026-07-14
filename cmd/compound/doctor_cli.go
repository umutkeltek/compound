package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"compound/internal/finding"
	"compound/internal/home"
	"compound/internal/hookplan"
	"compound/internal/landlease"
	"compound/internal/policy"
)

// cmdDoctor is the single legible operator health surface. The newest organs each expose real state,
// but scattered behind their own subcommands or raw JSON — so a first-time operator cannot answer
// "did my install work / who holds the landing lease / is anything waiting for me?" from one place.
// doctor composes the states that already exist — per-harness hook install/drift/absent, the
// land-lease (free/held), pending operational findings, and the placement guard mode — into one
// read-only report. It writes nothing and makes no provider call.
func cmdDoctor(args []string) {
	if code := runDoctor(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

type doctorHook struct {
	Harness    string `json:"harness"`
	Status     string `json:"status"` // installed|ok|drift|absent
	ConfigPath string `json:"config_path"`
}

type doctorReport struct {
	Home               string       `json:"home"`
	Hooks              []doctorHook `json:"hooks"`
	HookHint           string       `json:"hook_hint,omitempty"`
	LeaseHeld          bool         `json:"lease_held"`
	LeaseHolder        string       `json:"lease_holder,omitempty"`
	LeaseExpiresAt     string       `json:"lease_expires_at,omitempty"`
	LeaseStealReason   string       `json:"lease_steal_reason,omitempty"`
	FindingsTotal      int          `json:"findings_total"`
	FindingsLive       int          `json:"findings_live"`
	FindingsSuperseded int          `json:"findings_superseded"`
	PlacementMode      string       `json:"placement_guard_mode"`
	PlacementNote      string       `json:"placement_note,omitempty"`
}

func doctorUsage() string {
	return "usage: compound doctor [--base DIR] [--binary PATH] [--home P] [--json]\n" +
		"  read-only operator health: per-harness hook install/drift, land-lease state, pending findings, placement guard mode.\n" +
		"  --base/--binary scope the hook check (default: user home + this binary); writes nothing."
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	if hasUsageFlag(args) {
		fmt.Fprintln(stderr, doctorUsage())
		return 2
	}
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	rep := doctorReport{Home: homePath}

	// 1) Hooks — per-harness installed/ok/drift/absent via the install CHECK path (writes nothing).
	base := strings.TrimSpace(flagValue(args, "--base"))
	if base == "" {
		if h, herr := os.UserHomeDir(); herr == nil {
			base = h
		}
	}
	binary := strings.TrimSpace(flagValue(args, "--binary"))
	if binary == "" {
		if exe, eerr := os.Executable(); eerr == nil {
			binary = exe
		}
	}
	if base != "" && binary != "" {
		if hookRep, herr := hookplan.InstallHooks(base, binary, "", nil, true); herr == nil {
			for _, e := range hookRep.Entries {
				rep.Hooks = append(rep.Hooks, doctorHook{Harness: e.Harness, Status: e.Action, ConfigPath: e.ConfigPath})
			}
		}
	}
	for _, h := range rep.Hooks {
		if h.Status == "absent" || h.Status == "drift" {
			rep.HookHint = "run `compound hook install` to (re)wire the guard"
			break
		}
	}

	// 2) Land-lease — who (if anyone) holds the right to land right now.
	if st, lerr := landlease.Status(homePath, time.Now()); lerr == nil {
		rep.LeaseHeld = st.Held
		rep.LeaseHolder = st.Holder
		rep.LeaseExpiresAt = st.ExpiresAt
		rep.LeaseStealReason = st.StealReason
	}

	// 3) Findings — total, superseded (any id named by another finding's Supersedes), live.
	if fs, ferr := finding.List(homePath, ""); ferr == nil {
		superseded := map[string]bool{}
		for _, f := range fs {
			if s := strings.TrimSpace(f.Supersedes); s != "" {
				superseded[s] = true
			}
		}
		rep.FindingsTotal = len(fs)
		for _, f := range fs {
			if superseded[f.ID] {
				rep.FindingsSuperseded++
			} else {
				rep.FindingsLive++
			}
		}
	}

	// 4) Placement guard mode — observe is fail-open (record-only).
	if cfg, _, cerr := policy.LoadConfig(homePath); cerr == nil {
		rep.PlacementMode = cfg.PlacementGuardMode
		if cfg.PlacementGuardMode == "observe" {
			rep.PlacementNote = "observe = record-only (a mis-placed write is NOT blocked); set to block to enforce"
		}
	}

	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, rep); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	printDoctor(stdout, rep)
	return 0
}

func printDoctor(stdout io.Writer, rep doctorReport) {
	fmt.Fprintln(stdout, "# compound doctor")
	fmt.Fprintf(stdout, "home: %s\n", rep.Home)

	fmt.Fprintln(stdout, "\n## hooks (PreToolUse guard)")
	if len(rep.Hooks) == 0 {
		fmt.Fprintln(stdout, "- (could not read harness configs)")
	}
	for _, h := range rep.Hooks {
		fmt.Fprintf(stdout, "- %-12s %-9s %s\n", h.Harness, h.Status, h.ConfigPath)
	}
	if rep.HookHint != "" {
		fmt.Fprintf(stdout, "  -> %s\n", rep.HookHint)
	}

	fmt.Fprintln(stdout, "\n## land-lease (concurrent landing)")
	if rep.LeaseHeld {
		fmt.Fprintf(stdout, "- HELD by %q until %s\n", rep.LeaseHolder, rep.LeaseExpiresAt)
		fmt.Fprintln(stdout, "  -> wait, or `compound land-lease steal --reason ...` if stale")
	} else {
		fmt.Fprintln(stdout, "- free")
	}
	if rep.LeaseStealReason != "" {
		fmt.Fprintf(stdout, "  last steal: %s\n", rep.LeaseStealReason)
	}

	fmt.Fprintln(stdout, "\n## findings (operational evidence)")
	fmt.Fprintf(stdout, "- %d total (%d live, %d superseded)\n", rep.FindingsTotal, rep.FindingsLive, rep.FindingsSuperseded)
	if rep.FindingsTotal > 0 {
		fmt.Fprintln(stdout, "  -> `compound finding list` to review, `compound finding doctor` for stale/superseded")
	}

	fmt.Fprintln(stdout, "\n## placement guard")
	fmt.Fprintf(stdout, "- mode: %s\n", rep.PlacementMode)
	if rep.PlacementNote != "" {
		fmt.Fprintf(stdout, "  note: %s\n", rep.PlacementNote)
	}
}
