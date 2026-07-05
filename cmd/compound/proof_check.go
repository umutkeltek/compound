package main

import (
	"bytes"
	"compound/internal/home"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const proofCheckSchemaVersion = "compound.proof_check_report.v0.1"

type proofCheckReport struct {
	SchemaVersion string           `json:"schema_version"`
	Status        string           `json:"status"`
	GeneratedAt   string           `json:"generated_at"`
	Repo          string           `json:"repo"`
	DryRun        bool             `json:"dry_run"`
	ArtifactRef   string           `json:"artifact_ref,omitempty"`
	Warnings      []string         `json:"warnings,omitempty"`
	ProofRefs     []string         `json:"proof_refs"`
	Steps         []proofCheckStep `json:"steps"`
}

type proofCheckStep struct {
	Name          string   `json:"name"`
	Command       []string `json:"command"`
	Dir           string   `json:"dir,omitempty"`
	SkipIfMissing string   `json:"skip_if_missing,omitempty"`
	ProofRef      string   `json:"proof_ref"`
	Status        string   `json:"status"`
	ExitCode      int      `json:"exit_code,omitempty"`
	DurationMS    int64    `json:"duration_ms,omitempty"`
	OutputTail    string   `json:"output_tail,omitempty"`
}

func cmdProofCheck(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Println("usage: compound proof-check [--json] [--dry-run] [--repo P]")
		return
	}
	jsonOut := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run")
	repoPath := flagValue(args, "--repo")
	report, err := runProofCheck(repoPath, dryRun, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "proof-check:", err)
		os.Exit(2)
	}
	if !dryRun && report.Status == "ok" {
		homePath, err := home.Resolve("")
		if err != nil {
			report.Warnings = append(report.Warnings, "proof artifact not recorded: "+err.Error())
		} else {
			var recordErr error
			report, recordErr = recordProofCheckRuntimeArtifact(homePath, report)
			if recordErr != nil {
				report.Warnings = append(report.Warnings, "proof artifact not recorded: "+recordErr.Error())
			}
		}
	}
	if jsonOut {
		printJSON(report)
	} else {
		printProofCheck(report)
	}
	if report.Status != "ok" && !dryRun {
		os.Exit(1)
	}
}

func runProofCheck(repoPath string, dryRun bool, now time.Time) (proofCheckReport, error) {
	repo, err := resolveProofCheckRepo(repoPath)
	if err != nil {
		return proofCheckReport{}, err
	}
	steps, err := proofCheckSteps()
	if err != nil {
		return proofCheckReport{}, err
	}
	report := proofCheckReport{
		SchemaVersion: proofCheckSchemaVersion,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Repo:          repo,
		DryRun:        dryRun,
		Steps:         make([]proofCheckStep, 0, len(steps)),
	}
	for _, step := range steps {
		result := step
		if dryRun {
			result.Status = "planned"
		} else {
			result = runProofCheckStep(repo, step)
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

func recordProofCheckRuntimeArtifact(homePath string, report proofCheckReport) (proofCheckReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return report, fmt.Errorf("COMPOUND_HOME is empty")
	}
	generatedAt, err := time.Parse(time.RFC3339, report.GeneratedAt)
	if err != nil {
		generatedAt = time.Now().UTC()
	}
	date := generatedAt.UTC().Format("20060102")
	refsDir := filepath.Join(homePath, "artifacts", "refs")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		return report, err
	}
	name := "proof-check-installed-runtime-" + date + ".md"
	ref := "artifact:artifacts/refs/" + name
	var b strings.Builder
	fmt.Fprintf(&b, "# Proof-check installed runtime proof %s\n\n", generatedAt.UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "`proof-check-installed-runtime` adjacent Workbench action verified installed runtime proof.\n")
	fmt.Fprintf(&b, "`architecture-doctor-strict` adjacent Workbench action verified through the proof-check architecture-doctor step.\n")
	fmt.Fprintf(&b, "`workbench-projection-refresh` adjacent Workbench action verified through proof-check workbench projection step.\n")
	fmt.Fprintf(&b, "`codex-pretooluse-wrapper-proof` adjacent Workbench action verified through proof-check synthetic live PreToolUse wrapper step.\n")
	fmt.Fprintf(&b, "Do not rerun these adjacent Workbench actions as the next move unless proof-check, strict architecture doctor, Workbench projection, Codex PreToolUse wrapper proof, or source state regresses.\n\n")
	fmt.Fprintf(&b, "- schema: %s\n", report.SchemaVersion)
	fmt.Fprintf(&b, "- status: %s\n", report.Status)
	fmt.Fprintf(&b, "- generated_at: %s\n", report.GeneratedAt)
	fmt.Fprintf(&b, "- repo: %s\n", report.Repo)
	fmt.Fprintf(&b, "- proof_refs: %s\n", strings.Join(report.ProofRefs, ","))
	for _, step := range report.Steps {
		fmt.Fprintf(&b, "- step: %s proof_ref=%s status=%s\n", step.Name, step.ProofRef, step.Status)
	}
	if err := os.WriteFile(filepath.Join(refsDir, name), []byte(b.String()), 0o644); err != nil {
		return report, err
	}
	report.ArtifactRef = ref
	return report, nil
}

func resolveProofCheckRepo(repoPath string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		repoPath = cwd
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--repo must be a directory, got %s", abs)
	}
	return abs, nil
}

func proofCheckSteps() ([]proofCheckStep, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []proofCheckStep{
		{Name: "go-test", Command: []string{"go", "test", "-count=1", "./..."}, ProofRef: "go-test-count-1-all"},
		{Name: "go-vet", Command: []string{"go", "vet", "./..."}, ProofRef: "go-vet-all"},
		{Name: "go-build", Command: []string{"go", "build", "./..."}, ProofRef: "go-build-all"},
		{Name: "diff-check", Command: []string{"git", "diff", "--check"}, ProofRef: "diff-check"},
		{Name: "console-tsc", Command: []string{"./node_modules/.bin/tsc", "--noEmit"}, Dir: "console", ProofRef: "console-tsc-noemit"},
		{Name: "console-smoke", Command: []string{"node", "scripts/smoke.mjs"}, Dir: "console", ProofRef: "console-smoke"},
		{Name: "console-mutation-gate-smoke", Command: []string{"node", "scripts/mutation-guard-smoke.mjs"}, Dir: "console", ProofRef: "console-mutation-gate-smoke"},
		{Name: "console-private-fallback-smoke", Command: []string{"node", "scripts/private-fallback-smoke.mjs"}, Dir: "console", ProofRef: "console-private-fallback-smoke"},
		{Name: "console-move-type-drift-smoke", Command: []string{"node", "scripts/move-type-drift-smoke.mjs"}, Dir: "console", ProofRef: "console-move-type-drift-smoke"},
		{Name: "bun-tools-test", Command: []string{"bun", "test", "<tools/*.test.ts>"}, SkipIfMissing: "bun", ProofRef: "bun-tools-test"},
		{Name: "architecture-doctor", Command: []string{exe, "architecture", "doctor", "--strict"}, ProofRef: "architecture-doctor-strict"},
		{Name: "workbench-projection", Command: []string{exe, "workbench", "--json"}, ProofRef: "workbench-projection-refresh"},
		{Name: "codex-pretooluse-wrapper", Command: []string{exe, "hook", "pre-tool-use", "--live", "--harness", "codex"}, ProofRef: "codex-pretooluse-wrapper-proof"},
		{Name: "memory-doctor", Command: []string{exe, "memory", "doctor"}, ProofRef: "memory-doctor"},
		{Name: "redact-public", Command: []string{"redact", "--repo-visibility", "public", "--from-file", "<changed files>"}, ProofRef: "redact-public-clean"},
	}, nil
}

func runProofCheckStep(repo string, step proofCheckStep) proofCheckStep {
	start := time.Now()
	result := step
	result.Status = "ok"
	var output []byte
	var err error
	if step.SkipIfMissing != "" {
		if _, lookErr := exec.LookPath(step.SkipIfMissing); lookErr != nil {
			result.Status = "skipped"
			result.OutputTail = fmt.Sprintf("%s missing; skipped %s\n", step.SkipIfMissing, step.Name)
			result.DurationMS = time.Since(start).Milliseconds()
			return result
		}
	}
	if step.Name == "redact-public" {
		output, err = runProofCheckRedact(repo)
	} else if step.Name == "codex-pretooluse-wrapper" {
		output, err = runProofCheckCodexPreToolUseWrapper(repo, step)
	} else if step.Name == "bun-tools-test" {
		output, err = runProofCheckBunTools(repo)
	} else {
		cmd := exec.Command(step.Command[0], step.Command[1:]...)
		cmd.Dir = repo
		if step.Dir != "" {
			cmd.Dir = filepath.Join(repo, step.Dir)
		}
		cmd.Env = os.Environ()
		if proofCheckUsesGo(step) {
			cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
		}
		output, err = cmd.CombinedOutput()
	}
	result.DurationMS = time.Since(start).Milliseconds()
	result.OutputTail = proofCheckTail(output, 4000)
	if err != nil {
		result.Status = "failed"
		result.ExitCode = proofCheckExitCode(err)
	}
	return result
}

func runProofCheckCodexPreToolUseWrapper(repo string, step proofCheckStep) ([]byte, error) {
	tmpHome, err := os.MkdirTemp("", "compound-proofcheck-home-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpHome)
	tmpRepo, err := os.MkdirTemp("", "compound-proofcheck-repo-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpRepo)
	input := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"sess_codex_wrapper_probe","cwd":%q,"tool_name":"Write","tool_input":{"file_path":%q}}`, tmpRepo, filepath.Join(tmpRepo, "README.md"))
	cmd := exec.Command(step.Command[0], step.Command[1:]...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+tmpHome)
	cmd.Stdin = strings.NewReader(input)
	return cmd.CombinedOutput()
}

func runProofCheckBunTools(repo string) ([]byte, error) {
	matches, err := filepath.Glob(filepath.Join(repo, "tools", "*.test.ts"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no tools/*.test.ts files found")
	}
	args := []string{"test"}
	for _, match := range matches {
		rel, err := filepath.Rel(repo, match)
		if err != nil {
			return nil, err
		}
		args = append(args, "."+string(filepath.Separator)+rel)
	}
	cmd := exec.Command("bun", args...)
	cmd.Dir = repo
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

func proofCheckUsesGo(step proofCheckStep) bool {
	return len(step.Command) > 0 && step.Command[0] == "go"
}

func proofCheckExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func runProofCheckRedact(repo string) ([]byte, error) {
	files, err := proofCheckChangedFiles(repo)
	if err != nil {
		return nil, err
	}
	var combined bytes.Buffer
	for _, file := range files {
		cmd := exec.Command("redact", "--repo-visibility", "public", "--from-file", file)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		combined.Write(out)
		if err != nil {
			return combined.Bytes(), err
		}
	}
	if len(files) == 0 {
		combined.WriteString("redact scan skipped: no changed files\n")
	}
	return combined.Bytes(), nil
}

func proofCheckChangedFiles(repo string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(paths []string) {
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
		}
	}
	for _, args := range [][]string{
		{"diff", "--name-only", "--diff-filter=ACM", "-z"},
		{"diff", "--cached", "--name-only", "--diff-filter=ACM", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		paths, err := proofCheckGitNULList(repo, args...)
		if err != nil {
			return nil, err
		}
		add(paths)
	}
	return files, nil
}

func proofCheckGitNULList(repo string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := bytes.Split(out, []byte{0})
	paths := make([]string, 0, len(raw))
	for _, part := range raw {
		if len(part) == 0 {
			continue
		}
		paths = append(paths, string(part))
	}
	return paths, nil
}

func proofCheckTail(output []byte, max int) string {
	out := strings.TrimSpace(string(output))
	if len(out) <= max {
		return out
	}
	return out[len(out)-max:]
}

func printProofCheck(report proofCheckReport) {
	fmt.Printf("# proof-check\n")
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- repo: %s\n", report.Repo)
	fmt.Printf("- dry_run: %t\n", report.DryRun)
	for _, step := range report.Steps {
		fmt.Printf("- step: %s status=%s proof=%s command=%s\n", step.Name, step.Status, step.ProofRef, strings.Join(step.Command, " "))
	}
	if len(report.ProofRefs) > 0 {
		fmt.Printf("- proof_refs: %s\n", strings.Join(report.ProofRefs, ","))
	}
	if report.ArtifactRef != "" {
		fmt.Printf("- artifact_ref: %s\n", report.ArtifactRef)
	}
	for _, warning := range report.Warnings {
		fmt.Printf("- warning: %s\n", warning)
	}
}
