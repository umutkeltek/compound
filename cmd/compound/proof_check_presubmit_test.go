package main

import (
	"compound/internal/proofscope"
	"compound/internal/work"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newProofCheckPresubmitRepo is a CLI-shaped module named "compound" so the
// real proofscope derivation runs: cmd/compound imports internal/work, which
// imports internal/landlease; internal/arch reads the repository root.
func newProofCheckPresubmitRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	proofCheckDevelopmentGit(t, repo, "init", "-q")
	for name, body := range map[string]string{
		"go.mod":                        "module compound\n\ngo 1.23\n",
		"AGENTS.md":                     "# agents\n",
		"skills/compound-land/SKILL.md": "# land\n",
		"cmd/compound/main.go":          "package main\n\nimport _ \"compound/internal/work\"\n\nfunc main() {}\n",
		"internal/work/work.go":         "package work\n\nimport _ \"compound/internal/landlease\"\n",
		"internal/landlease/lease.go":   "package landlease\n",
		"internal/arch/arch.go":         "package arch\n",
		"internal/arch/arch_test.go":    "package arch\n\nimport (\n\t\"runtime\"\n\t\"testing\"\n)\n\nfunc TestRoot(t *testing.T) { _, _, _, _ = runtime.Caller(0) }\n",
		"internal/policy/policy.go":     "package policy\n",
	} {
		proofCheckDevelopmentWriteFile(t, filepath.Join(repo, name), body)
	}
	return repo, proofCheckPresubmitCommit(t, repo, "base")
}

func proofCheckPresubmitCommit(t *testing.T, repo, message string) string {
	t.Helper()
	identity := []string{"-c", "user.name=Compound Test", "-c", "user.email=test@example.invalid"}
	proofCheckDevelopmentGit(t, repo, append(identity, "add", "-A", ".")...)
	proofCheckDevelopmentGit(t, repo, append(identity, "commit", "-qm", message)...)
	return strings.TrimSpace(proofCheckDevelopmentGit(t, repo, "rev-parse", "HEAD"))
}

func proofCheckPresubmitDryRun(t *testing.T, repo, base string) (proofCheckReport, string) {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "presubmit.json")
	stdout := captureStdout(t, func() {
		cmdProofCheck([]string{"--presubmit", "--dry-run", "--repo", repo, "--base", base, "--out", outPath})
	})
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read --out report: %v\n%s", err, stdout)
	}
	var report proofCheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("parse --out report: %v\n%s", err, body)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	return report, lines[len(lines)-1]
}

func proofCheckPresubmitGoTests(report proofCheckReport) map[string][]string {
	runs := map[string][]string{}
	for _, step := range report.Steps {
		if strings.HasPrefix(step.Name, "go-test") {
			runs[step.Name] = step.Command
		}
	}
	return runs
}

func TestProofCheckPresubmitDerivesGatesFromTheDiff(t *testing.T) {
	repo, base := newProofCheckPresubmitRepo(t)
	proofCheckDevelopmentWriteFile(t, filepath.Join(repo, "internal/landlease/lease.go"), "package landlease\n\nconst TTL = 1\n")
	head := proofCheckPresubmitCommit(t, repo, "landlease change")

	report, verdict := proofCheckPresubmitDryRun(t, repo, base)
	if report.SchemaVersion != work.ProofCheckPresubmitSchemaVersion || report.ProfileKind != work.ProofCheckProfilePresubmit {
		t.Fatalf("schema/profile = %q/%q", report.SchemaVersion, report.ProfileKind)
	}
	if report.ArtifactRef != "" || report.CIOnly || report.CIConsole {
		t.Fatalf("presubmit report claimed canonical evidence: %+v", report)
	}
	if report.Presubmit == nil || report.Presubmit.Head != head || report.BaseRef != base {
		t.Fatalf("presubmit derivation = %+v base=%q; want head %s", report.Presubmit, report.BaseRef, head)
	}
	var names []string
	for _, step := range report.Steps {
		names = append(names, step.Name)
		if step.Status != "planned" || !strings.HasPrefix(step.ProofRef, "presubmit:") {
			t.Fatalf("step %s status=%s proof_ref=%s", step.Name, step.Status, step.ProofRef)
		}
	}
	wantNames := []string{"head-clean", "go-build", "go-vet", "gofmt", "diff-check", "architecture-doctor", "go-test", "redact-public"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("steps = %v; want %v", names, wantNames)
	}
	wantTests := map[string][]string{
		"go-test": {"go", "test", "-short", "-timeout=30m", "compound/cmd/compound", "compound/internal/arch", "compound/internal/landlease", "compound/internal/work"},
	}
	if got := proofCheckPresubmitGoTests(report); !reflect.DeepEqual(got, wantTests) {
		t.Fatalf("go-test runs = %v; want %v", got, wantTests)
	}
	if diffCheck := report.Steps[4].Command; !reflect.DeepEqual(diffCheck, []string{"git", "diff", "--check", base + "...HEAD"}) {
		t.Fatalf("diff-check = %v", diffCheck)
	}
	if !strings.HasPrefix(verdict, "presubmit: planned non_certifying=true scope=affected reason=diff packages=4 deferred=0 planned=8 total=8 out=") {
		t.Fatalf("verdict line = %q", verdict)
	}
}

func TestProofCheckPresubmitDocsOnlyRunsDoctrineReaders(t *testing.T) {
	repo, base := newProofCheckPresubmitRepo(t)
	proofCheckDevelopmentWriteFile(t, filepath.Join(repo, "skills/compound-land/SKILL.md"), "# land\n\nPresubmit first.\n")
	proofCheckDevelopmentWriteFile(t, filepath.Join(repo, "AGENTS.md"), "# agents\n\nProof discipline.\n")
	proofCheckPresubmitCommit(t, repo, "docs only")

	report, verdict := proofCheckPresubmitDryRun(t, repo, base)
	wantTests := map[string][]string{
		"go-test":             {"go", "test", "-short", "-timeout=30m", "compound/internal/arch"},
		"go-test-cli-readers": {"go", "test", "-short", "-timeout=30m", "-run", proofscope.PresubmitCLIReaders, "compound/cmd/compound"},
	}
	if got := proofCheckPresubmitGoTests(report); !reflect.DeepEqual(got, wantTests) {
		t.Fatalf("go-test runs = %v; want %v", got, wantTests)
	}
	if !strings.Contains(verdict, " scope=affected reason=diff packages=2 ") {
		t.Fatalf("verdict line = %q", verdict)
	}
}

func TestProofCheckPresubmitRunsWholeModuleForModuleAndRemovedPackages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(t *testing.T, repo string)
		reason string
	}{
		{"go.mod", func(t *testing.T, repo string) {
			proofCheckDevelopmentWriteFile(t, filepath.Join(repo, "go.mod"), "module compound\n\ngo 1.24\n")
		}, "module-files:go.mod"},
		{"removed package", func(t *testing.T, repo string) {
			if err := os.RemoveAll(filepath.Join(repo, "internal", "policy")); err != nil {
				t.Fatal(err)
			}
		}, "package-removed:internal/policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, base := newProofCheckPresubmitRepo(t)
			tc.edit(t, repo)
			proofCheckPresubmitCommit(t, repo, tc.name)
			report, verdict := proofCheckPresubmitDryRun(t, repo, base)
			want := map[string][]string{"go-test": {"go", "test", "-short", "-timeout=30m", "./..."}}
			if got := proofCheckPresubmitGoTests(report); !reflect.DeepEqual(got, want) {
				t.Fatalf("go-test runs = %v; want %v", got, want)
			}
			if !strings.Contains(verdict, " scope=full reason="+tc.reason+" packages=all ") {
				t.Fatalf("verdict line = %q", verdict)
			}
		})
	}
}

func TestProofCheckPresubmitRefusesMovingOrForeignBases(t *testing.T) {
	repo, base := newProofCheckPresubmitRepo(t)
	proofCheckDevelopmentWriteFile(t, filepath.Join(repo, "internal/policy/policy.go"), "package policy\n\nconst P = 1\n")
	head := proofCheckPresubmitCommit(t, repo, "policy change")
	proofCheckDevelopmentGit(t, repo, "checkout", "-q", "-b", "side", base)
	proofCheckDevelopmentWriteFile(t, filepath.Join(repo, "internal/arch/arch.go"), "package arch\n\nconst A = 1\n")
	side := proofCheckPresubmitCommit(t, repo, "side change")
	proofCheckDevelopmentGit(t, repo, "checkout", "-q", head)

	for _, tc := range []struct{ base, want string }{
		{"main", "full lowercase commit SHA"},
		{base[:12], "full lowercase commit SHA"},
		{strings.ToUpper(base), "full lowercase commit SHA"},
		{side, "is not an ancestor of HEAD"},
	} {
		if _, err := runNonCertifyingProofCheck(repo, true, tc.base, time.Now(), "--presubmit"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("base %q: err = %v; want %q", tc.base, err, tc.want)
		}
	}
	if _, err := runNonCertifyingProofCheck(repo, true, base, time.Now(), "--presubmit"); err != nil {
		t.Fatalf("exact ancestor base refused: %v", err)
	}
}

func TestProofCheckPresubmitArgsAreStrict(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("a", 40)
	for _, args := range [][]string{
		{"--presubmit"},
		{"--presubmit", "--dev", "--base", sha},
		{"--presubmit", "--base", sha, "--work", "work_example"},
		{"--presubmit", "--base", sha, "--ci"},
		{"--presubmit=true", "--base", sha},
		{"--presubmit", "--presubmit", "--base", sha},
	} {
		if err := validateProofCheckDevelopmentArgs(args); err == nil {
			t.Fatalf("presubmit args %v accepted", args)
		}
	}
	if err := validateProofCheckDevelopmentArgs([]string{"--presubmit", "--base", sha, "--out", "r.json", "--json", "--dry-run"}); err != nil {
		t.Fatalf("valid presubmit args rejected: %v", err)
	}
	if !proofCheckDevelopmentRequested([]string{"--presubmit=true"}) || !work.ProofCheckLightweightMode([]string{"--presubmit"}) {
		t.Fatal("assignment-form or lightweight detection would let --presubmit reach the certifying path")
	}
	if _, err := work.ParseProofCheckRouteOptions([]string{"--presubmit", "--redact-review", "act_example"}); err == nil {
		t.Fatal("reviewed redaction accepted in a presubmit")
	}
}
