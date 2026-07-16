package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/activity"
	"compound/internal/home"
	"compound/internal/policy"
)

// cmdActivity is the operator door onto the Tier-0 ambient-activity family (internal/activity):
// enable/disable the sensor through the governed act seam, inspect status, drain the collector's
// NDJSON spool into segments+spans (grant-gated, fail-closed), and sweep old files. It NEVER runs a
// daemon (a blind cron is a liar with a schedule) — the collector is an external launchd agent; drain
// is event-driven and operator/agent-invoked.
func cmdActivity(args []string) {
	if code := runActivity(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func activityUsage() string {
	return "usage:\n" +
		"  compound activity status [--json] [--home P]                      sensor enabled state + storage summary\n" +
		"  compound activity enable  [--work W] [--home P]                   governed enable (needs a matching standing grant)\n" +
		"  compound activity disable [--work W] [--home P]                   governed disable\n" +
		"  compound activity drain   [--spool DIR] [--work W] [--json] [--home P]  ingest spool -> segments+spans (grant-gated, fail-closed)\n" +
		"  compound activity sweep   [--json] [--home P]                     delete spans/segments older than 30d, report reclaimed bytes"
}

func runActivity(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(stderr, activityUsage())
		return 2
	}
	switch args[0] {
	case "status":
		return runActivityStatus(args[1:], stdout, stderr)
	case "enable":
		return runActivityToggle(args[1:], stdout, stderr, true)
	case "disable":
		return runActivityToggle(args[1:], stdout, stderr, false)
	case "drain":
		return runActivityDrain(args[1:], stdout, stderr)
	case "sweep":
		return runActivitySweep(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, activityUsage())
		return 2
	}
}

func runActivityStatus(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := activity.EnsureConfigSeeded(homePath); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	enabled, err := activity.Enabled(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	out := map[string]any{
		"enabled":       enabled,
		"config_path":   activity.ConfigPath(homePath),
		"activity_root": activity.ActivityRoot(homePath),
		"spool_dir":     defaultSpoolDir(),
	}
	if hasFlag(args, "--json") {
		return writeJSON(stdout, stderr, out)
	}
	fmt.Fprintf(stdout, "activity sensor enabled=%v\n", enabled)
	fmt.Fprintf(stdout, "  config: %s\n", activity.ConfigPath(homePath))
	fmt.Fprintf(stdout, "  store:  %s\n", activity.ActivityRoot(homePath))
	fmt.Fprintf(stdout, "  spool:  %s\n", defaultSpoolDir())
	return 0
}

func runActivityToggle(args []string, stdout, stderr io.Writer, enable bool) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	work := activityWorkID(args, "toggle")
	res, err := activity.SetEnabled(homePath, cfg, work, enable, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if res.Refused {
		fmt.Fprintf(stderr, "refused: %s (needs a standing grant for %q)\n", res.Reason, activity.ToggleCapability)
		return 1
	}
	if hasFlag(args, "--json") {
		return writeJSON(stdout, stderr, res)
	}
	verb := "disabled"
	if enable {
		verb = "enabled"
	}
	fmt.Fprintf(stdout, "activity sensor %s (receipt=%s transition=%s)\n", verb, res.ReceiptID, res.TransitionID)
	return 0
}

func runActivityDrain(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	spool := strings.TrimSpace(flagValue(args, "--spool"))
	if spool == "" {
		spool = defaultSpoolDir()
	}
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	work := activityWorkID(args, "drain")
	rep, err := activity.Drain(homePath, spool, cfg, work, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		code := 0
		if rep.Refused {
			code = 1
		}
		if writeJSON(stdout, stderr, rep) != 0 {
			return 1
		}
		return code
	}
	if rep.Refused {
		fmt.Fprintf(stderr, "drain refused: %s\n", rep.Reason)
		return 1
	}
	fmt.Fprintf(stdout, "drain: ingested=%d refused_lines=%d days=%s bytes=%d\n",
		rep.SegmentLinesAdded, rep.RefusedLines, strings.Join(rep.DaysWritten, ","), rep.BytesUnderRoot)
	return 0
}

func runActivitySweep(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	rep, err := activity.Sweep(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		return writeJSON(stdout, stderr, rep)
	}
	fmt.Fprintf(stdout, "sweep: deleted=%d reclaimed_bytes=%d\n", rep.FilesDeleted, rep.BytesReclaimed)
	return 0
}

// activityWorkID reads --work or synthesizes a deterministic-enough default (a governed act belongs
// to a work; a standalone operator toggle/drain still needs one).
func activityWorkID(args []string, verb string) string {
	if w := strings.TrimSpace(flagValue(args, "--work")); w != "" {
		return w
	}
	return "work_activity_" + verb
}

// defaultSpoolDir is the collector's NDJSON output dir: a generic application-support path under the
// user's home (never a hardcoded personal path). The Swift collector writes here; drain reads here.
func defaultSpoolDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("Library", "Application Support", "compound-activity", "spool")
	}
	return filepath.Join(h, "Library", "Application Support", "compound-activity", "spool")
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
