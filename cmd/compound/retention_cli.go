package main

// `compound retention` — the read-only surface a storage controller calls.
//
// The controller supplies the consumer's published thresholds. Compound inventories declared
// artifact classes and returns a plan; it never reclaims anything itself.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"compound/internal/policy"
)

func retentionUsage() string {
	return "usage: compound retention <classes|scan|plan> [--json]\n" +
		"  classes            declared artifact classes: owner, retention rule, reclaim command\n" +
		"  scan               what is accumulating in this repo, and whether a class claims it\n" +
		"  plan               ordered reclaim plan from caller-supplied consumer thresholds\n" +
		"    --consumer NAME        required consumer identity\n" +
		"    --free-bytes N         override the observed free bytes\n" +
		"    --capacity-bytes N     override the observed capacity\n" +
		"    --required-bytes N     override the consumer's byte floor\n" +
		"    --required-bps N       override the consumer's ratio floor (basis points)\n" +
		"  plans only; never reclaims."
}

func cmdRetention(args []string) {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, retentionUsage())
		os.Exit(2)
	}
	asJSON := hasFlag(args, "--json")

	switch args[0] {
	case "classes":
		classes := policy.RetentionClasses()
		if asJSON {
			printJSON(classes)
			return
		}
		fmt.Println("# retention classes")
		for _, class := range classes {
			fmt.Printf("- %s  (owner: %s, %s%s)\n", class.ID, class.Owner, class.Mode,
				map[bool]string{true: ", rebuildable", false: ""}[class.Rebuildable])
			fmt.Printf("    %s\n", class.Description)
			fmt.Printf("    retention: %s\n", class.Retention)
			if class.ReclaimCommand == "" {
				fmt.Println("    reclaim:   NONE — no mechanism exists for this class")
			} else {
				fmt.Printf("    reclaim:   %s\n", class.ReclaimCommand)
			}
		}
		return

	case "scan":
		scan := policy.ScanRetentionWithOptions(resolveRetentionRoots(args[1:]), policy.RetentionScanOptions{FixedPathCaches: true})
		if asJSON {
			printJSON(scan)
			return
		}
		fmt.Println("# retention scan")
		for _, item := range scan.Declared {
			fmt.Printf("- %-24s %10s  %s\n", item.ClassID, humanBytes(item.Bytes), item.Path)
		}
		for _, item := range scan.Undeclared {
			fmt.Printf("- %-24s %10s  %s\n", "UNDECLARED", humanBytes(item.Bytes), item.Path)
		}
		fmt.Printf("reclaimable now: %s\n", humanBytes(scan.ReclaimableBytes))
		return

	case "plan":
		target, err := resolveReclaimTarget(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "retention plan:", err)
			os.Exit(1)
		}
		scan := policy.ScanRetentionWithOptions(resolveRetentionRoots(args[1:]), policy.RetentionScanOptions{FixedPathCaches: true})
		plan := policy.PlanReclaim(scan, target)
		if asJSON {
			printJSON(plan)
			return
		}
		fmt.Println("# retention plan")
		fmt.Printf("- consumer: %s\n", plan.Target.Consumer)
		fmt.Printf("- free: %s of %s (needs >= %s AND >= %d bps)\n",
			humanBytes(plan.Target.FreeBytes), humanBytes(plan.Target.CapacityBytes),
			humanBytes(plan.Target.RequiredFreeBytes), plan.Target.RequiredFreeBps)
		fmt.Printf("- shortfall: %s\n", humanBytes(plan.ShortfallBytes))
		for i, step := range plan.Steps {
			fmt.Printf("  %d. %-24s %10s  %s\n", i+1, step.ClassID, humanBytes(step.Bytes), step.Path)
		}
		if len(plan.OperatorGated) > 0 {
			fmt.Println("- operator-gated (never executed automatically):")
			for _, step := range plan.OperatorGated {
				fmt.Printf("     %-24s %10s  %s\n", step.ClassID, humanBytes(step.Bytes), step.Path)
			}
		}
		fmt.Printf("- sufficient: %v — %s\n", plan.Sufficient, plan.Note)
		if !plan.Sufficient {
			// Exit 1 so a scheduled caller can branch on "automation cannot fix this".
			os.Exit(1)
		}
		return
	}

	fmt.Fprintln(os.Stderr, retentionUsage())
	os.Exit(2)
}

// resolveReclaimTarget requires the caller to pass the consumer's observed capacity and
// published thresholds. Compound refuses to invent defaults or invoke a private integration.
func resolveReclaimTarget(args []string) (policy.ReclaimTarget, error) {
	target := policy.ReclaimTarget{Consumer: strings.TrimSpace(valueFlag(args, "--consumer"))}
	if target.Consumer == "" {
		return policy.ReclaimTarget{}, fmt.Errorf("--consumer is required")
	}

	for _, override := range []struct {
		flag string
		dst  *int64
	}{
		{"--free-bytes", &target.FreeBytes},
		{"--capacity-bytes", &target.CapacityBytes},
		{"--required-bytes", &target.RequiredFreeBytes},
		{"--required-bps", &target.RequiredFreeBps},
	} {
		raw := strings.TrimSpace(valueFlag(args, override.flag))
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return policy.ReclaimTarget{}, fmt.Errorf("%s: %w", override.flag, err)
		}
		*override.dst = parsed
	}

	if target.RequiredFreeBytes == 0 && target.RequiredFreeBps == 0 {
		// Refuse to invent a threshold. A planner that guesses is the failure being fixed.
		return policy.ReclaimTarget{}, fmt.Errorf(
			"no published thresholds for consumer %q and none supplied; pass --required-bytes/--required-bps",
			target.Consumer)
	}
	return target, nil
}

// resolveRetentionRoots decides which repositories to scan.
//
// Explicit --root flags win. Otherwise it DISCOVERS sibling repositories rather than scanning
// sibling repositories. Discovery is bounded so the scan remains a health check rather than a
// home-directory crawl.
func resolveRetentionRoots(args []string) []string {
	var roots []string
	for i, arg := range args {
		if arg == "--root" && i+1 < len(args) {
			roots = append(roots, args[i+1])
		}
	}
	if len(roots) > 0 {
		return roots
	}

	current := resolveRetentionScanRoot()
	if current == "" {
		return nil
	}
	roots = append(roots, current)

	// ~/Projects/systems/compound -> ~/Projects
	base := filepath.Dir(filepath.Dir(current))
	if base == "" || base == "/" || base == current {
		return roots
	}
	seen := map[string]bool{current: true}
	for _, group := range readSubdirs(base) {
		for _, candidate := range readSubdirs(group) {
			if seen[candidate] {
				continue
			}
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err != nil {
				continue
			}
			seen[candidate] = true
			roots = append(roots, candidate)
		}
	}
	return roots
}

func readSubdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			out = append(out, filepath.Join(dir, entry.Name()))
		}
	}
	return out
}
