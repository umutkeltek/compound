package main

import (
	"bytes"
	"compound/internal/home"
	"compound/internal/policy"
	"encoding/json"
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
	BaseRef       string           `json:"base_ref,omitempty"`
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
	SkipReason    string   `json:"skip_reason,omitempty"`
	ProofRef      string   `json:"proof_ref"`
	Status        string   `json:"status"`
	ExitCode      int      `json:"exit_code,omitempty"`
	DurationMS    int64    `json:"duration_ms,omitempty"`
	OutputTail    string   `json:"output_tail,omitempty"`
}

func cmdProofCheck(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Println("usage: compound proof-check [--json] [--dry-run] [--repo P] [--base REF]")
		return
	}
	jsonOut := hasFlag(args, "--json")
	dryRun := hasFlag(args, "--dry-run")
	repoPath := flagValue(args, "--repo")
	baseRef := flagValue(args, "--base")
	report, err := runProofCheck(repoPath, dryRun, baseRef, time.Now())
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

func runProofCheck(repoPath string, dryRun bool, baseRef string, now time.Time) (proofCheckReport, error) {
	repo, err := resolveProofCheckRepo(repoPath)
	if err != nil {
		return proofCheckReport{}, err
	}
	baseRef = strings.TrimSpace(baseRef)
	steps, err := proofCheckSteps(baseRef)
	if err != nil {
		return proofCheckReport{}, err
	}
	report := proofCheckReport{
		SchemaVersion: proofCheckSchemaVersion,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Repo:          repo,
		BaseRef:       baseRef,
		DryRun:        dryRun,
		Steps:         make([]proofCheckStep, 0, len(steps)),
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
	fmt.Fprintf(&b, "`codex-pretooluse-native-gate-proof` adjacent Workbench action verified through proof-check's native allow-and-deny PreToolUse step.\n")
	fmt.Fprintf(&b, "Do not rerun these adjacent Workbench actions as the next move unless proof-check, strict architecture doctor, Workbench projection, the Codex native PreToolUse gate, or source state regresses.\n\n")
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

func proofCheckSteps(baseRef string) ([]proofCheckStep, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	redactCommand := []string{"redact", "--repo-visibility", "public", "--from-file", "<changed files>"}
	if strings.TrimSpace(baseRef) != "" {
		redactCommand = []string{"redact", "--repo-visibility", "public", "--from-file", "<files changed in " + strings.TrimSpace(baseRef) + "...HEAD>"}
	}
	return []proofCheckStep{
		{Name: "go-test", Command: []string{"go", "test", "-count=1", "./..."}, ProofRef: "go-test-count-1-all"},
		{Name: "go-vet", Command: []string{"go", "vet", "./..."}, ProofRef: "go-vet-all"},
		{Name: "go-build", Command: []string{"go", "build", "./..."}, ProofRef: "go-build-all"},
		{Name: "gofmt", Command: []string{"gofmt", "-l", "<tracked *.go>"}, ProofRef: "gofmt-check"},
		{Name: "diff-check", Command: []string{"git", "diff", "--check"}, ProofRef: "diff-check"},
		{Name: "console-tsc", Command: []string{"./node_modules/.bin/tsc", "--noEmit"}, Dir: "console", ProofRef: "console-tsc-noemit"},
		{Name: "console-smoke", Command: consoleSmokeCommand("scripts/smoke.mjs"), Dir: "console", ProofRef: "console-smoke"},
		{Name: "console-mutation-gate-smoke", Command: consoleSmokeCommand("scripts/mutation-guard-smoke.mjs"), Dir: "console", ProofRef: "console-mutation-gate-smoke"},
		{Name: "console-instance-pin-smoke", Command: consoleSmokeCommand("scripts/instance-pin-smoke.mjs"), Dir: "console", ProofRef: "console-instance-pin-smoke"},
		{Name: "console-continue-button-smoke", Command: consoleBunSmokeCommand("scripts/continue-button-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-continue-button-smoke"},
		{Name: "console-work-action-row-smoke", Command: consoleBunSmokeCommand("scripts/work-action-row-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-work-action-row-smoke"},
		{Name: "console-mutation-readiness-consumers-smoke", Command: consoleSmokeCommand("scripts/mutation-readiness-consumers-smoke.mjs"), Dir: "console", ProofRef: "console-mutation-readiness-consumers-smoke"},
		{Name: "console-private-fallback-smoke", Command: consoleSmokeCommand("scripts/private-fallback-smoke.mjs"), Dir: "console", ProofRef: "console-private-fallback-smoke"},
		{Name: "console-private-runtime-guard-smoke", Command: consoleSmokeCommand("scripts/private-runtime-guard-smoke.mjs"), Dir: "console", ProofRef: "console-private-runtime-guard-smoke"},
		{Name: "console-move-type-drift-smoke", Command: consoleSmokeCommand("scripts/move-type-drift-smoke.mjs"), Dir: "console", ProofRef: "console-move-type-drift-smoke"},
		{Name: "console-ai-boundary-smoke", Command: consoleSmokeCommand("scripts/ai-boundary-smoke.mjs"), Dir: "console", ProofRef: "console-ai-boundary-smoke"},
		{Name: "console-client-bundle-guard-smoke", Command: consoleSmokeCommand("scripts/client-bundle-guard-smoke.mjs"), Dir: "console", ProofRef: "console-client-bundle-guard-smoke"},
		{Name: "console-work-trace-smoke", Command: consoleBunSmokeCommand("scripts/work-trace-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-work-trace-smoke"},
		{Name: "console-generic-actions-smoke", Command: consoleBunSmokeCommand("scripts/generic-actions-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-generic-actions-smoke"},
		{Name: "console-view-registry-smoke", Command: consoleBunSmokeCommand("scripts/view-registry-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-view-registry-smoke"},
		{Name: "console-decide-brief-smoke", Command: consoleBunSmokeCommand("scripts/decide-model-brief-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-decide-brief-smoke"},
		{Name: "console-decision-execute-smoke", Command: consoleBunSmokeCommand("scripts/decision-execute-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-decision-execute-smoke"},
		{Name: "console-standing-law-smoke", Command: consoleBunSmokeCommand("scripts/standing-law-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-standing-law-smoke"},
		{Name: "console-attention-consistency-smoke", Command: consoleSmokeCommand("scripts/attention-consistency-smoke.mjs"), Dir: "console", SkipIfMissing: "compound", ProofRef: "console-attention-consistency-smoke"},
		{Name: "console-work-language-smoke", Command: consoleBunSmokeCommand("scripts/work-language-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-work-language-smoke"},
		{Name: "console-ask-triage-smoke", Command: consoleBunSmokeCommand("scripts/ask-triage-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-ask-triage-smoke"},
		{Name: "console-representation-falsifiers-smoke", Command: consoleBunSmokeCommand("scripts/representation-falsifiers-smoke.mjs"), Dir: "console", SkipIfMissing: "bun", ProofRef: "console-representation-falsifiers-smoke"},
		{Name: "console-representation-cluster-guard-smoke", Command: consoleSmokeCommand("scripts/representation-cluster-guard-smoke.mjs"), Dir: "console", ProofRef: "console-representation-cluster-guard-smoke"},
		{Name: "bun-tools-test", Command: []string{"bun", "test", "<tools/*.test.ts>"}, SkipIfMissing: "bun", ProofRef: "bun-tools-test"},
		{Name: "architecture-doctor", Command: []string{exe, "architecture", "doctor", "--strict"}, ProofRef: "architecture-doctor-strict"},
		{Name: "profiles-check", Command: []string{exe, "profiles", "check", "--installed-only"}, ProofRef: "profiles-check-installed-only"},
		{Name: "workbench-projection", Command: []string{exe, "workbench", "--json"}, ProofRef: "workbench-projection-refresh"},
		{Name: "codex-pretooluse-native-gate", Command: []string{exe, "hook", "pre-tool-use", "--live", "--harness", "codex"}, ProofRef: "codex-pretooluse-native-gate-proof"},
		{Name: "memory-doctor", Command: []string{exe, "memory", "doctor"}, ProofRef: "memory-doctor"},
		{Name: "redact-public", Command: redactCommand, ProofRef: "redact-public-clean"},
	}, nil
}

func consoleSmokeCommand(script string) []string {
	return []string{"node", "--experimental-strip-types", script}
}

func consoleBunSmokeCommand(script string) []string {
	return []string{"bun", script}
}

func runProofCheckStep(repo string, step proofCheckStep, baseRef string) proofCheckStep {
	start := time.Now()
	result := step
	result.Status = "ok"
	var output []byte
	var err error
	if step.SkipIfMissing != "" {
		if _, lookErr := exec.LookPath(step.SkipIfMissing); lookErr != nil {
			result.Status = "skipped"
			result.SkipReason = fmt.Sprintf("required executable %s not found", step.SkipIfMissing)
			result.OutputTail = result.SkipReason + "; skipped " + step.Name + "\n"
			result.DurationMS = time.Since(start).Milliseconds()
			return result
		}
	}
	if step.Name == "redact-public" {
		output, err = runProofCheckRedact(repo, baseRef)
	} else if step.Name == "gofmt" {
		output, err = runProofCheckGofmt(repo)
	} else if step.Name == "codex-pretooluse-native-gate" {
		output, err = runProofCheckCodexPreToolUseNativeGate(repo, step)
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

func runProofCheckCodexPreToolUseNativeGate(repo string, step proofCheckStep) ([]byte, error) {
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
	if err := home.Init(tmpHome, "proof-check", time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("initialize proof-check home: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpHome, "config", "policy.yaml"), []byte("placement_guard_mode: block\n"), 0o644); err != nil {
		return nil, fmt.Errorf("enable placement block mode: %w", err)
	}
	// Augment the synthetic fixture with a block-confidence governed root for THIS home so the
	// open-world case can be proven natively (the synthesized default root is warn, which records
	// but does not hold). The allow-set is the home's own typed habitat dirs.
	fixtureData, err := os.ReadFile(filepath.Join(repo, "fixtures", "policy", "placement-rules.json"))
	if err != nil {
		return nil, fmt.Errorf("read placement fixture: %w", err)
	}
	var rules policy.PlacementRules
	if err := json.Unmarshal(fixtureData, &rules); err != nil {
		return nil, fmt.Errorf("parse placement fixture: %w", err)
	}
	allowGlobs := make([]string, 0, len(home.Dirs))
	for _, d := range home.Dirs {
		allowGlobs = append(allowGlobs, filepath.Join(tmpHome, d, "**"))
	}
	rules.GovernedRoots = append(rules.GovernedRoots, policy.GovernedRoot{
		ID:         "compound-home",
		RootGlobs:  []string{filepath.Join(tmpHome, "**")},
		AllowGlobs: allowGlobs,
		Confidence: "block",
		Redirect:   "compound placement target --kind operational-finding",
	})
	augmented, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("marshal augmented placement rules: %w", err)
	}
	rulesSrc := filepath.Join(tmpRepo, "proofcheck-placement-rules.json")
	if err := os.WriteFile(rulesSrc, augmented, 0o644); err != nil {
		return nil, fmt.Errorf("write augmented placement rules: %w", err)
	}
	if _, err := policy.ImportPlacementRules(tmpHome, rulesSrc, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("import synthetic placement rules: %w", err)
	}

	runPatch := func(sessionID, patch string) (claudeLiveHookResponse, []byte, error) {
		input := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":%q,"cwd":%q,"tool_name":"apply_patch","tool_input":{"command":%q}}`, sessionID, tmpRepo, patch)
		cmd := exec.Command(step.Command[0], step.Command[1:]...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "COMPOUND_HOME="+tmpHome, "HOME="+tmpRepo, "PLACEMENT_GUARD_MODE=block")
		cmd.Stdin = strings.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		evidence := bytes.Join([][]byte{stdout, stderr.Bytes()}, nil)
		if err != nil {
			return claudeLiveHookResponse{}, evidence, fmt.Errorf("Codex native PreToolUse command: %w", err)
		}
		var response claudeLiveHookResponse
		if err := json.Unmarshal(stdout, &response); err != nil {
			return claudeLiveHookResponse{}, evidence, fmt.Errorf("parse Codex native PreToolUse response: %w", err)
		}
		return response, evidence, nil
	}

	allowedPatch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch"
	allowed, allowedOut, err := runPatch("sess_codex_native_allow", allowedPatch)
	if err != nil {
		return allowedOut, err
	}
	if decision := allowed.HookSpecificOutput.PermissionDecision; decision != "" && decision != "allow" {
		return allowedOut, fmt.Errorf("valid Codex apply_patch decision = %q, want allow", decision)
	}

	blockedPatch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** Add File: DOC-0001.md\n+misplaced canon\n*** End Patch"
	blocked, blockedOut, err := runPatch("sess_codex_native_block", blockedPatch)
	combined := bytes.Join([][]byte{[]byte("allow:\n"), allowedOut, []byte("\nblock:\n"), blockedOut}, nil)
	if err != nil {
		return combined, err
	}
	if blocked.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(strings.ToLower(blocked.HookSpecificOutput.PermissionDecisionReason), "placement") {
		return combined, fmt.Errorf("misplaced multi-target Codex apply_patch did not return native placement deny")
	}

	// Open-world case: a novel filename in an UNCLAIMED location under COMPOUND_HOME. It matches
	// no owner deny/canon rule, so pre-fix it sailed through; the governed-root check must now hold
	// it and hand back the redirect.
	unownedTarget := filepath.Join(tmpHome, "private", "stray-scaffold.md")
	unownedPatch := "*** Begin Patch\n*** Add File: " + unownedTarget + "\n+stray agent scaffolding\n*** End Patch"
	unowned, unownedOut, err := runPatch("sess_codex_native_unowned", unownedPatch)
	combined = bytes.Join([][]byte{combined, []byte("\nunowned:\n"), unownedOut}, nil)
	if err != nil {
		return combined, err
	}
	unownedReason := strings.ToLower(unowned.HookSpecificOutput.PermissionDecisionReason)
	if unowned.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(unownedReason, "unowned-under-governed-root") {
		return combined, fmt.Errorf("unowned write under COMPOUND_HOME governed root did not return native placement deny")
	}
	return combined, nil
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

func runProofCheckGofmt(repo string) ([]byte, error) {
	files, err := proofCheckGitNULList(repo, "ls-files", "*.go", "-z")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return []byte("no tracked Go files\n"), nil
	}
	args := append([]string{"-l"}, files...)
	cmd := exec.Command("gofmt", args...)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, err
	}
	if strings.TrimSpace(string(output)) != "" {
		return output, fmt.Errorf("gofmt reported unformatted tracked Go files")
	}
	return []byte("all tracked Go files gofmt-clean\n"), nil
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

func runProofCheckRedact(repo, baseRef string) ([]byte, error) {
	files, err := proofCheckChangedFiles(repo, baseRef)
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
		if strings.TrimSpace(baseRef) != "" {
			combined.WriteString("redact scan skipped: no files changed in " + strings.TrimSpace(baseRef) + "...HEAD and no dirty files\n")
		} else {
			combined.WriteString("redact scan skipped: no changed files\n")
		}
	}
	return combined.Bytes(), nil
}

func proofCheckChangedFiles(repo, baseRef string) ([]string, error) {
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
	if baseRef = strings.TrimSpace(baseRef); baseRef != "" {
		paths, err := proofCheckGitNULList(repo, "diff", "--name-only", "--diff-filter=ACM", "-z", baseRef+"...HEAD")
		if err != nil {
			return nil, err
		}
		add(paths)
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
		if step.SkipReason != "" {
			fmt.Printf("  skip_reason: %s\n", step.SkipReason)
		}
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
