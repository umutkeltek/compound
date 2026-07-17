package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"compound/internal/authority"
)

// Injected seams (package vars) so tests can substitute a fake Git + fake
// Liveness, mirroring worktree_retirement_cli.go's DI convention.
var worktreeInventoryCLIGit authority.WorktreeRetirementGit = authority.ExecWorktreeRetirementGit{}
var worktreeInventoryCLILiveness authority.WorktreeRetirementLiveness = authority.LSOFWorktreeRetirementLiveness{}

const executorsWorktreesUsage = "usage: compound executors worktrees [--json] [--repo PATH] [--activity-window-min N]"

func cmdExecutorsWorktrees(args []string) {
	// A per-worktree liveness probe shells out once per worktree; bound the
	// whole read so the command can never hang on a slow probe.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if code := runExecutorsWorktrees(ctx, args, os.Stdout, os.Stderr, time.Now()); code != 0 {
		os.Exit(code)
	}
}

func runExecutorsWorktrees(ctx context.Context, args []string, stdout, stderr io.Writer, now time.Time) int {
	jsonOut := false
	repo := ""
	windowMin := 60
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json":
			jsonOut = true
		case "--repo":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(stderr, executorsWorktreesUsage)
				return 2
			}
			repo = strings.TrimSpace(args[i])
		case "--activity-window-min":
			i++
			if i >= len(args) {
				fmt.Fprintln(stderr, executorsWorktreesUsage)
				return 2
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[i]))
			if err != nil || n <= 0 {
				fmt.Fprintln(stderr, executorsWorktreesUsage)
				return 2
			}
			windowMin = n
		default:
			fmt.Fprintln(stderr, executorsWorktreesUsage)
			return 2
		}
	}

	if repo == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		repo = wd
	}

	inv, err := authority.BuildWorktreeInventory(ctx, authority.WorktreeInventoryDependencies{
		Git:      worktreeInventoryCLIGit,
		Liveness: worktreeInventoryCLILiveness,
	}, repo, authority.WorktreeInventoryOptions{
		Now:            now,
		ActivityWindow: time.Duration(windowMin) * time.Minute,
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(inv); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	printWorktreeInventory(stdout, inv)
	return 0
}

func printWorktreeInventory(w io.Writer, inv authority.WorktreeInventory) {
	fmt.Fprintln(w, "# compound executors worktrees")
	fmt.Fprintf(w, "- repo: %s\n", inv.Repo)
	fmt.Fprintf(w, "- generated_at: %s\n", inv.GeneratedAt)
	fmt.Fprintf(w, "- authority: %s @ %s (%s)\n", inv.MainRef, shortWorktreeSHA(inv.MainTip), inv.AuthorityRef)
	fmt.Fprintf(w, "- counts: total=%d live=%d stalled_unlanded=%d landed_clean=%d probe=%d unknown=%d\n",
		inv.Counts.Total, inv.Counts.Live, inv.Counts.StalledUnlanded, inv.Counts.LandedClean, inv.Counts.Probe, inv.Counts.Unknown)
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLASS\tWORKTREE\tAHEAD\tDIRTY\tAGE\tNEXT ACTION")
	for _, row := range inv.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n",
			row.Class, worktreeRowLabel(row), row.AheadCommits, row.DirtyFiles, worktreeAgeLabel(row.AgeSeconds), row.NextAction)
	}
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintln(w, "reasons:")
	for _, row := range inv.Rows {
		fmt.Fprintf(w, "- %s [%s]: %s\n", worktreeRowLabel(row), row.Class, row.Reason)
	}
}

func worktreeRowLabel(row authority.WorktreeInventoryRow) string {
	if row.IsMain {
		return "(main)"
	}
	if row.Detached {
		return "(detached) " + filepath.Base(row.Path)
	}
	if row.Branch != "" {
		return row.Branch
	}
	return filepath.Base(row.Path)
}

func shortWorktreeSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "-"
	}
	return sha
}

func worktreeAgeLabel(sec *int64) string {
	if sec == nil {
		return "-"
	}
	s := *sec
	switch {
	case s < 90:
		return fmt.Sprintf("%ds", s)
	case s < 90*60:
		return fmt.Sprintf("%dm", s/60)
	case s < 48*3600:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dd", s/86400)
	}
}
