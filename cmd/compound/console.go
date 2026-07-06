package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/home"
)

const consoleLaunchSchemaVersion = "compound.console_launch.v0.1"

type consoleLaunchPlan struct {
	SchemaVersion    string            `json:"schema_version"`
	GeneratedAt      string            `json:"generated_at"`
	Mode             string            `json:"mode"`
	ConsoleDir       string            `json:"console_dir"`
	Command          []string          `json:"command"`
	Env              map[string]string `json:"env"`
	MutationsEnabled bool              `json:"mutations_enabled"`
	PrivateFallbacks bool              `json:"private_fallbacks"`
	DryRun           bool              `json:"dry_run"`
}

func cmdConsole(args []string) {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, "usage: compound console --operator [--console-dir DIR] [--private-fallbacks] [--dry-run] [--json]")
		os.Exit(2)
	}
	operator := false
	privateFallbacks := false
	dryRun := false
	jsonOut := false
	consoleDirFlag := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--operator":
			operator = true
		case "--private-fallbacks":
			privateFallbacks = true
		case "--dry-run":
			dryRun = true
		case "--json":
			jsonOut = true
		case "--console-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound console --operator [--console-dir DIR] [--private-fallbacks] [--dry-run] [--json]")
				os.Exit(2)
			}
			i++
			consoleDirFlag = args[i]
		default:
			fmt.Fprintf(os.Stderr, "unknown console option %q\n", args[i])
			os.Exit(2)
		}
	}
	if !operator {
		fmt.Fprintln(os.Stderr, "usage: compound console --operator [--console-dir DIR] [--private-fallbacks] [--dry-run] [--json]")
		os.Exit(2)
	}
	plan, err := buildConsoleLaunchPlan(consoleDirFlag, privateFallbacks, dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(plan)
		if dryRun {
			return
		}
	}
	if dryRun {
		fmt.Printf("console operator dry-run: cd %s && %s\n", plan.ConsoleDir, strings.Join(plan.Command, " "))
		return
	}
	if _, err := exec.LookPath(plan.Command[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s not found; install Bun or run from a shell with Bun on PATH\n", plan.Command[0])
		os.Exit(1)
	}
	fmt.Println("console operator: local console on port 3777")
	cmd := exec.Command(plan.Command[0], plan.Command[1:]...)
	cmd.Dir = plan.ConsoleDir
	cmd.Env = consoleLaunchEnv(plan.Env)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func buildConsoleLaunchPlan(consoleDirFlag string, privateFallbacks, dryRun bool) (consoleLaunchPlan, error) {
	consoleDir, err := resolveConsoleDir(consoleDirFlag)
	if err != nil {
		return consoleLaunchPlan{}, err
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return consoleLaunchPlan{}, fmt.Errorf("resolve COMPOUND_HOME: %w", err)
	}
	compoundExe, _ := os.Executable()
	compoundBin := resolveConsoleCompoundBin(compoundExe, exec.LookPath)
	env := map[string]string{
		"COMPOUND_BIN":               compoundBin,
		"COMPOUND_CONSOLE_MUTATIONS": "1",
		"COMPOUND_HOME":              homePath,
	}
	if privateFallbacks {
		env["COMPOUND_CONSOLE_PRIVATE_FALLBACKS"] = "1"
	}
	return consoleLaunchPlan{
		SchemaVersion:    consoleLaunchSchemaVersion,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Mode:             "operator",
		ConsoleDir:       consoleDir,
		Command:          []string{"bun", "run", "dev"},
		Env:              env,
		MutationsEnabled: true,
		PrivateFallbacks: privateFallbacks,
		DryRun:           dryRun,
	}, nil
}

func resolveConsoleCompoundBin(currentExe string, lookPath func(string) (string, error)) string {
	currentExe = strings.TrimSpace(currentExe)
	if currentExe == "" || consoleExecutableIsTemporary(currentExe) {
		if stable, err := lookPath("compound"); err == nil && strings.TrimSpace(stable) != "" {
			return stable
		}
	}
	if currentExe != "" {
		return currentExe
	}
	if stable, err := lookPath("compound"); err == nil && strings.TrimSpace(stable) != "" {
		return stable
	}
	return "compound"
}

func consoleExecutableIsTemporary(path string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == "" {
		return false
	}
	tempRoots := []string{filepath.Clean(os.TempDir()), string(filepath.Separator) + "tmp"}
	for _, root := range tempRoots {
		if cleaned == root || strings.HasPrefix(cleaned, root+string(filepath.Separator)) {
			return true
		}
	}
	base := filepath.Base(cleaned)
	return strings.Contains(cleaned, string(filepath.Separator)+"go-build") ||
		strings.HasPrefix(base, "compound-dashboard")
}

func resolveConsoleDir(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return validateConsoleDir(explicit)
	}
	if fromEnv := strings.TrimSpace(os.Getenv("COMPOUND_CONSOLE_DIR")); fromEnv != "" {
		return validateConsoleDir(fromEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if candidate, err := validateConsoleDir(filepath.Join(dir, "console")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", fmt.Errorf("console directory not found; run from the compound repo or pass --console-dir")
}

func validateConsoleDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Join(abs, "package.json"))
	if err != nil {
		return "", fmt.Errorf("console package.json not found at %s: %w", abs, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("console package.json is a directory at %s", abs)
	}
	return abs, nil
}

func consoleLaunchEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		prefix := key + "="
		replaced := false
		for i, entry := range env {
			if strings.HasPrefix(entry, prefix) {
				env[i] = prefix + value
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, prefix+value)
		}
	}
	return env
}
