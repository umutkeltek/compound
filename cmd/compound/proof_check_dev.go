package main

import (
	"compound/internal/home"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	proofCheckProfileDevelopment       = "development"
	proofCheckDevelopmentSchemaVersion = "compound.proof_check_development_report.v0.1"
)

func proofCheckDevelopmentRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--dev" || strings.HasPrefix(arg, "--dev=") {
			return true
		}
	}
	return false
}

func validateProofCheckDevelopmentArgs(args []string) error {
	governanceFlags := map[string]bool{
		"--ci":         true,
		"--ci-console": true,
		"--work":       true,
		"--actor":      true,
		"--ttl":        true,
		"--steal":      true,
		"--reason":     true,
	}
	booleanFlags := map[string]bool{
		"--dev":     true,
		"--json":    true,
		"--dry-run": true,
	}
	valueFlags := map[string]bool{
		"--repo": true,
		"--base": true,
		"--out":  true,
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		flag := arg
		if eq := strings.IndexByte(flag, '='); eq >= 0 {
			flag = flag[:eq]
		}
		if governanceFlags[flag] {
			return fmt.Errorf("--dev cannot be combined with %s", flag)
		}
		if booleanFlags[flag] {
			if arg != flag {
				return fmt.Errorf("%s does not accept a value", flag)
			}
			if seen[flag] {
				return fmt.Errorf("%s may be provided only once", flag)
			}
			seen[flag] = true
			continue
		}
		if valueFlags[flag] {
			if arg != flag {
				return fmt.Errorf("%s requires a separate value", flag)
			}
			if seen[flag] {
				return fmt.Errorf("%s may be provided only once", flag)
			}
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || strings.HasPrefix(args[i+1], "--") {
				return fmt.Errorf("%s requires a non-empty value", flag)
			}
			seen[flag] = true
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("unsupported --dev flag %s", arg)
		}
		return fmt.Errorf("unexpected --dev argument %q", arg)
	}
	return nil
}

func cmdProofCheckDevelopment(args []string) {
	if err := validateProofCheckDevelopmentArgs(args); err != nil {
		fmt.Fprintln(os.Stderr, "proof-check:", err)
		os.Exit(2)
	}
	jsonOut := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run")
	repoPath := flagValue(args, "--repo")
	baseRef := flagValue(args, "--base")
	outPath := strings.TrimSpace(flagValue(args, "--out"))
	if outPath != "" {
		if err := proofCheckValidateDevelopmentOutPath(outPath); err != nil {
			fmt.Fprintln(os.Stderr, "proof-check: --out path not writable:", err)
			os.Exit(2)
		}
	}

	started := time.Now()
	report, err := runDevelopmentProofCheck(repoPath, dryRun, baseRef, started)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proof-check:", err)
		os.Exit(2)
	}
	// The development lane records into the SAME history file but under its own ProfileKind, so
	// the operator gets drift visibility on the fast lane too while its cached `-short` runs stay
	// structurally unable to contaminate the certifying lane's baseline.
	if !dryRun {
		if homePath, homeErr := home.Resolve(""); homeErr == nil {
			if warning, timingErr := recordProofCheckTimingHistory(homePath, report, time.Since(started), time.Now()); timingErr == nil && warning != "" {
				report.Warnings = append(report.Warnings, warning)
			}
		}
	}
	if outPath != "" {
		if err := proofCheckWriteDevelopmentReportFile(outPath, report); err != nil {
			fmt.Fprintln(os.Stderr, "proof-check: writing --out file:", err)
			os.Exit(2)
		}
	}
	if jsonOut {
		printJSON(report)
	} else {
		printProofCheck(report)
	}
	if outPath != "" || !jsonOut {
		fmt.Println(proofCheckVerdictLine(report, outPath))
	}
	if report.Status != "ok" && !dryRun {
		os.Exit(1)
	}
}

func runDevelopmentProofCheck(repoPath string, dryRun bool, baseRef string, now time.Time) (proofCheckReport, error) {
	repo, err := resolveProofCheckRepo(repoPath)
	if err != nil {
		return proofCheckReport{}, err
	}
	baseRef = strings.TrimSpace(baseRef)
	steps, err := proofCheckDevelopmentSteps(repo, baseRef)
	if err != nil {
		return proofCheckReport{}, err
	}
	report := proofCheckReport{
		SchemaVersion: proofCheckDevelopmentSchemaVersion,
		ProfileKind:   proofCheckProfileDevelopment,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Repo:          repo,
		BaseRef:       baseRef,
		DryRun:        dryRun,
		Steps:         make([]proofCheckStep, 0, len(steps)),
	}
	if dryRun {
		report.Status = "planned"
	}
	for _, step := range steps {
		result := step
		if dryRun {
			result.Status = "planned"
		} else {
			result = runProofCheckStep(repo, step, baseRef)
			if result.Status == "failed" {
				report.Status = "failed"
			}
		}
		if result.Status == "ok" || result.Status == "planned" {
			report.ProofRefs = append(report.ProofRefs, result.ProofRef)
		}
		report.Steps = append(report.Steps, result)
	}
	return report, nil
}

func proofCheckValidateDevelopmentOutPath(path string) error {
	target, err := proofCheckDevelopmentResolvedOutPath(path)
	if err != nil {
		return err
	}
	probe, err := os.CreateTemp(filepath.Dir(target), ".compound-dev-report-check-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return err
	}
	return os.Remove(probePath)
}

func proofCheckDevelopmentResolvedOutPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("development report target cannot be a symlink")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(resolvedParent, filepath.Base(abs))
	for _, candidate := range []string{abs, resolved} {
		if err := proofCheckDevelopmentRejectRuntimePath(candidate); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

func proofCheckDevelopmentRejectRuntimePath(path string) error {
	parts := strings.Split(filepath.Clean(path), string(os.PathSeparator))
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "artifacts" && parts[i+1] == "refs" {
			return fmt.Errorf("development report cannot target a canonical artifacts/refs directory")
		}
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		manifest, err := home.LoadManifest(dir)
		if err == nil && manifest.SchemaVersion == home.SchemaVersion {
			return fmt.Errorf("development report cannot be written under COMPOUND_HOME %s", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return nil
}

func proofCheckWriteDevelopmentReportFile(path string, report proofCheckReport) error {
	target, err := proofCheckDevelopmentResolvedOutPath(path)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	keep = true
	return nil
}

func proofCheckDevelopmentSteps(repo, baseRef string) ([]proofCheckStep, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	baseRef = strings.TrimSpace(baseRef)
	redactCommand := []string{"redact", "--repo-visibility", "public", "--from-file", "<changed files>"}
	if baseRef != "" {
		redactCommand = []string{"redact", "--repo-visibility", "public", "--from-file", "<files changed in " + baseRef + "...HEAD>"}
	}
	steps := []proofCheckStep{
		{
			Name:     "go-test",
			Command:  []string{"go", "test", "-short", "-timeout=10m", "./..."},
			ProofRef: "development:go-test-short-cached-all",
		},
		{
			Name:     "gofmt",
			Command:  []string{"gofmt", "-l", "<tracked *.go>"},
			ProofRef: "development:gofmt-check",
		},
		{
			Name:     "diff-check",
			Command:  []string{"git", "diff", "--check"},
			ProofRef: "development:diff-check",
		},
		{
			Name:     "architecture-doctor",
			Command:  []string{exe, "architecture", "doctor", "--strict"},
			ProofRef: "development:architecture-doctor-strict",
		},
	}

	// Console relevance includes deletions and both sides of renames. Moving a
	// console file away can break imports even though redact scans only extant bodies.
	changedFiles, err := proofCheckChangedPaths(repo, baseRef, "ACMDR")
	if err != nil {
		return nil, err
	}
	for _, path := range changedFiles {
		if path == "console" || strings.HasPrefix(path, "console/") {
			steps = append(steps, proofCheckStep{
				Name:     "console-tsc",
				Command:  []string{"./node_modules/.bin/tsc", "--noEmit"},
				Dir:      "console",
				ProofRef: "development:console-tsc-noemit",
			})
			break
		}
	}
	steps = append(steps, proofCheckStep{
		Name:     "redact-public",
		Command:  redactCommand,
		ProofRef: "development:redact-public-clean",
	})
	return steps, nil
}
