package policy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RetentionCLI is the body of the `compound retention` verb. Exported so
// cmd/compound can keep a thin wrapper that delegates to it; the policy
// package owns the body so the budget gate (`cmd-compound-budget`) only
// ratchets DOWN — bodies move to existing internal/ organs, not raise
// the ceiling.
//
// args is the full argv slice AFTER the subcommand (first element is
// "classes"/"scan"/"plan", followed by flags). stdout/stderr receive the
// user-facing output. The returned int is the process exit code.
func RetentionCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || containsFlag(args, "--help") || containsFlag(args, "-h") {
		fmt.Fprint(stdout, retentionUsage())
		return 0
	}
	sub := args[0]
	if err := validateRetentionFlagsLocal(sub, args[1:]); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		fmt.Fprintln(stderr, retentionUsage())
		return 2
	}
	asJSON := containsFlag(args[1:], "--json")
	switch sub {
	case "classes":
		return retentionClassesBody(stdout, asJSON)
	case "scan":
		return retentionScanBody(stdout, stderr, args[1:], asJSON)
	case "plan":
		return retentionPlanBody(stdout, stderr, args[1:], asJSON)
	default:
		fmt.Fprintln(stderr, "error: unknown retention subcommand ", sub)
		fmt.Fprintln(stderr, retentionUsage())
		return 2
	}
}

// RetentionUsageForTest exposes the usage text for tests in cmd/compound.
func RetentionUsageForTest() string { return retentionUsage() }

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

// retention flag helpers — package-local, NOT exported. The cmd/compound
// package has its own helpers; we don't share to keep the policy package
// free of cmd/compound dependencies.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func valueFlagLocal(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func validateRetentionFlagsLocal(subcommand string, args []string) error {
	boolFlags := map[string]bool{"--json": true, "-h": true, "--help": true}
	valueFlags := map[string]bool{}
	switch subcommand {
	case "classes":
	case "scan":
		valueFlags["--root"] = true
	case "plan":
		for _, flag := range []string{
			"--root", "--consumer",
			"--free-bytes", "--capacity-bytes",
			"--required-bytes", "--required-bps",
		} {
			valueFlags[flag] = true
		}
	default:
		return fmt.Errorf("unknown subcommand %q", subcommand)
	}
	seen := map[string]bool{}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "" {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return fmt.Errorf("unexpected positional argument %q", a)
		}
		if valueFlags[a] {
			seen[a] = true
			i += 2 // skip the value
			continue
		}
		if boolFlags[a] {
			seen[a] = true
			i++
			continue
		}
		all := append([]string{}, allKeysSorted(valueFlags)...)
		all = append(all, allKeysSorted(boolFlags)...)
		return fmt.Errorf("unknown flag %s (allowed: %v)", a, all)
	}
	return nil
}

func allKeysSorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ResolveReclaimTargetForTest exposes the target resolver for tests in
// cmd/compound. The body stays package-local so cmd/compound does not
// grow a public surface.
func ResolveReclaimTargetForTest(args []string) (ReclaimTarget, error) {
	return resolveRetentionTargetLocal(args)
}

// resolveRetentionTargetLocal mirrors cmd/compound/retention_cli.go's
// resolveReclaimTarget (same field set, same fail-closed thresholds).
func resolveRetentionTargetLocal(args []string) (ReclaimTarget, error) {
	target := ReclaimTarget{Consumer: strings.TrimSpace(valueFlagLocal(args, "--consumer"))}
	if target.Consumer == "" {
		return ReclaimTarget{}, fmt.Errorf("--consumer is required")
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
		raw := strings.TrimSpace(valueFlagLocal(args, override.flag))
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return ReclaimTarget{}, fmt.Errorf("%s: %w", override.flag, err)
		}
		*override.dst = parsed
	}
	if target.RequiredFreeBytes == 0 && target.RequiredFreeBps == 0 {
		return ReclaimTarget{}, fmt.Errorf(
			"no published thresholds for consumer %q and none supplied; pass --required-bytes/--required-bps",
			target.Consumer)
	}
	return target, nil
}

// resolveRetentionRootsLocal decides which repositories to scan.
// Explicit --root flags win. Otherwise it DISCOVERS sibling repositories
// rather than scanning sibling repositories. Discovery is bounded so
// the scan remains a health check rather than a home-directory crawl.
func resolveRetentionRootsLocal(args []string) []string {
	var roots []string
	for i, arg := range args {
		if arg == "--root" && i+1 < len(args) {
			roots = append(roots, args[i+1])
		}
	}
	if len(roots) > 0 {
		return roots
	}

	current := resolveRetentionScanRootLocal()
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
	for _, group := range readRetentionSubdirs(base) {
		for _, candidate := range readRetentionSubdirs(group) {
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

// resolveRetentionScanRootLocal finds the repo root for retention scans.
// Mirrors cmd/compound/doctor_cli.go's resolveRetentionScanRoot. The
// original walks cwd → parent until it finds a repo; we walk HOME
// ancestry instead (the policy package must not assume a cwd), and
// accept cwd via $COMPOUND_HOME or fall through.
func resolveRetentionScanRootLocal() string {
	if h := os.Getenv("COMPOUND_HOME"); h != "" {
		// Walk up from COMPOUND_HOME looking for .git or go.mod
		dir := h
		for i := 0; i < 8 && dir != "" && dir != "/"; i++ {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	// Fall back to HOME ancestry.
	if h := os.Getenv("HOME"); h != "" {
		dir := h
		for i := 0; i < 8 && dir != "" && dir != "/"; i++ {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

func readRetentionSubdirs(dir string) []string {
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

// --- verb bodies ---

func retentionClassesBody(stdout io.Writer, asJSON bool) int {
	classes := RetentionClasses()
	if asJSON {
		return writeRetentionJSON(stdout, classes)
	}
	fmt.Fprintln(stdout, "# retention classes")
	for _, class := range classes {
		fmt.Fprintf(stdout, "- %s  (owner: %s, %s%s)\n", class.ID, class.Owner, class.Mode,
			map[bool]string{true: ", rebuildable", false: ""}[class.Rebuildable])
		fmt.Fprintf(stdout, "    %s\n", class.Description)
		fmt.Fprintf(stdout, "    retention: %s\n", class.Retention)
		if class.ReclaimCommand == "" {
			fmt.Fprintln(stdout, "    reclaim:   NONE — no mechanism exists for this class")
		} else {
			fmt.Fprintf(stdout, "    reclaim:   %s\n", class.ReclaimCommand)
		}
	}
	return 0
}

func retentionScanBody(stdout, stderr io.Writer, args []string, asJSON bool) int {
	scan := ScanRetentionWithOptions(resolveRetentionRootsLocal(args), RetentionScanOptions{FixedPathCaches: true})
	if asJSON {
		return writeRetentionJSON(stdout, scan)
	}
	fmt.Fprintln(stdout, "# retention scan")
	for _, item := range scan.Declared {
		fmt.Fprintf(stdout, "- %-24s %10s  %s\n", item.ClassID, humanBytes(item.Bytes), item.Path)
	}
	for _, item := range scan.Undeclared {
		fmt.Fprintf(stdout, "- %-24s %10s  %s\n", "UNDECLARED", humanBytes(item.Bytes), item.Path)
	}
	fmt.Fprintf(stdout, "reclaimable now: %s\n", humanBytes(scan.ReclaimableBytes))
	return 0
}

func retentionPlanBody(stdout, stderr io.Writer, args []string, asJSON bool) int {
	target, err := resolveRetentionTargetLocal(args)
	if err != nil {
		fmt.Fprintln(stderr, "retention plan:", err)
		return 1
	}
	scan := ScanRetentionWithOptions(resolveRetentionRootsLocal(args), RetentionScanOptions{FixedPathCaches: true})
	plan := PlanReclaim(scan, target)
	if asJSON {
		if err := writeRetentionJSON(stdout, plan); err != 0 {
			return err
		}
		if !plan.Sufficient {
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "# retention plan")
	fmt.Fprintf(stdout, "- consumer: %s\n", plan.Target.Consumer)
	fmt.Fprintf(stdout, "- free: %s of %s (needs >= %s AND >= %d bps)\n",
		humanBytes(plan.Target.FreeBytes), humanBytes(plan.Target.CapacityBytes),
		humanBytes(plan.Target.RequiredFreeBytes), plan.Target.RequiredFreeBps)
	fmt.Fprintf(stdout, "- shortfall: %s\n", humanBytes(plan.ShortfallBytes))
	for i, step := range plan.Steps {
		fmt.Fprintf(stdout, "  %d. %-24s %10s  %s\n", i+1, step.ClassID, humanBytes(step.Bytes), step.Path)
	}
	if len(plan.OperatorGated) > 0 {
		fmt.Fprintln(stdout, "- operator-gated (never executed automatically):")
		for _, step := range plan.OperatorGated {
			fmt.Fprintf(stdout, "     %-24s %10s  %s\n", step.ClassID, humanBytes(step.Bytes), step.Path)
		}
	}
	fmt.Fprintf(stdout, "- sufficient: %v — %s\n", plan.Sufficient, plan.Note)
	if !plan.Sufficient {
		return 1
	}
	return 0
}

// writeRetentionJSON is the thin wrapper that delegates to the
// impl defined in retention_cli_helpers.go (so this file stays free
// of encoding/json at the top — the policy package already imports it).
func writeRetentionJSON(w io.Writer, v any) int {
	return writeRetentionJSONImpl(w, v)
}
