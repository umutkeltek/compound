package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

// The retained failed proof route restores the original owned report through the EXISTING
// --out custody: the same rooted, CAS-guarded write the battery uses. A recovery that wrote
// the report by another route would not be the same evidence projection.
func TestProofCheckRestoreRetainedReportUsesTheExistingOutCustody(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, home.HomeKindGlobal, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schema_version":"compound.proof_check_report.v0.6","status":"failed","steps":[]}`)
	ref, err := artifacts.Put(body)
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "failed-proof.json")
	report, err := proofCheckRestoreRetainedReport(homePath, ref, outPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" {
		t.Fatalf("the restored report is not the retained failed verdict: %+v", report)
	}
	published, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(published, body) {
		t.Fatal("the restored --out bytes differ from the immutable owned artifact")
	}
	if _, err := proofCheckRestoreRetainedReport(homePath, ref, outPath); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, body) {
		t.Fatal("a repeated restore changed the restored --out bytes")
	}
}

// The command boundary detects the EXACT recovery tokens only. Both spellings of the retained
// route must be recognised, and a longer token that merely shares the prefix is not a recovery
// flag: it must never silently enter the recovery route.
func TestProofCheckRecoveryFlagDetectsExactTokensOnly(t *testing.T) {
	for name, testCase := range map[string]struct {
		args []string
		want bool
	}{
		"spaced":            {[]string{"--resume-retained", "pfr_x"}, true},
		"equals":            {[]string{"--resume-retained=pfr_x"}, true},
		"equals_empty":      {[]string{"--resume-retained="}, true},
		"near_miss_prefix":  {[]string{"--resume-retained-extra", "pfr_x"}, false},
		"near_miss_suffix":  {[]string{"--resume_retained", "pfr_x"}, false},
		"unrelated":         {[]string{"--out", "x.json"}, false},
		"quiescent_spaced":  {[]string{"--reconcile-quiescent", "pfr_x"}, true},
		"quiescent_equals":  {[]string{"--reconcile-quiescent=pfr_x"}, true},
		"quiescent_nearmis": {[]string{"--reconcile-quiescent-extra"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			flag := "--resume-retained"
			if strings.Contains(name, "quiescent") {
				flag = "--reconcile-quiescent"
			}
			if got := work.HasProofRecoveryFlag(testCase.args, flag); got != testCase.want {
				t.Fatalf("recovery token detection for %v = %v, want %v", testCase.args, got, testCase.want)
			}
		})
	}
}

// The real command boundary refuses a retained recovery that matches nothing. It never writes
// the --out path, never restores a report and never admits a battery: the refusal happens
// before any source capture, so a moved or missing checkout cannot change the outcome.
func TestProofCheckRetainedRecoveryCommandBoundaryRefusesWithoutCaptureOrAdmission(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, home.HomeKindGlobal, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// The original source checkout moved: it no longer exists at its recorded path.
	movedCheckout := filepath.Join(t.TempDir(), "moved-source-checkout")
	if err := os.MkdirAll(movedCheckout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(movedCheckout); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "never-written.json")
	for name, args := range map[string][]string{
		"spaced": {
			"--resume-retained", "pfr_synthetic",
			"--work", "work_synthetic",
			"--actor", "codex:proof-alpha",
			"--expected-pending-sha256", strings.Repeat("a", 64),
			"--out", outPath,
			"--home", homePath,
		},
		"equals": {
			"--resume-retained=pfr_synthetic",
			"--work", "work_synthetic",
			"--actor", "codex:proof-alpha",
			"--expected-pending-sha256", strings.Repeat("a", 64),
			"--out", outPath,
			"--home", homePath,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := work.RunProofRecoveryCLI(args, &stdout, &stderr, proofCheckRestoreRetainedReport)
			if code != 2 {
				t.Fatalf("a retained recovery that matches nothing must refuse: %d (%s)", code, stdout.String())
			}
			if !strings.Contains(stderr.String(), "no exact retained failed proof completion") &&
				!strings.Contains(stderr.String(), "read retained proof reservation") {
				t.Fatalf("the refusal did not name the missing retained completion: %q", stderr.String())
			}
			if _, err := os.Stat(outPath); !os.IsNotExist(err) {
				t.Fatal("a refused retained recovery wrote the --out path")
			}
			if stdout.Len() != 0 {
				t.Fatalf("a refused retained recovery published output: %q", stdout.String())
			}
		})
	}
	// No work was created and no proof resource was admitted by the refused route.
	entries, err := os.ReadDir(filepath.Join(homePath, "work", "work"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("a refused retained recovery created work: %d entries", len(entries))
	}
	// The recovery route takes no checkout at all: a source argument is not even a defined
	// option, so a moved or missing source checkout can neither be captured nor block it.
	var stdout, stderr bytes.Buffer
	code := work.RunProofRecoveryCLI([]string{
		"--resume-retained", "pfr_synthetic",
		"--work", "work_synthetic",
		"--actor", "codex:proof-alpha",
		"--expected-pending-sha256", strings.Repeat("a", 64),
		"--out", filepath.Join(t.TempDir(), "never-written.json"),
		"--home", homePath,
		"--repo", movedCheckout,
	}, &stdout, &stderr, proofCheckRestoreRetainedReport)
	if code != 2 || !strings.Contains(stderr.String(), "not defined") {
		t.Fatalf("the recovery route accepted a source checkout argument: %d (%q)", code, stderr.String())
	}
}

// Two recovery routes in one invocation are contradictory. The command boundary refuses them
// before it captures anything.
func TestProofCheckConflictingRecoveryFlagsRefuse(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, home.HomeKindGlobal, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "never-written.json")
	var stdout, stderr bytes.Buffer
	code := work.RunProofRecoveryCLI([]string{
		"--resume-retained", "pfr_synthetic",
		"--reconcile-quiescent", "pfr_synthetic",
		"--work", "work_synthetic",
		"--actor", "codex:proof-alpha",
		"--expected-pending-sha256", strings.Repeat("a", 64),
		"--out", outPath,
		"--home", homePath,
	}, &stdout, &stderr, proofCheckRestoreRetainedReport)
	if code != 2 {
		t.Fatalf("conflicting recovery flags must refuse: %d", code)
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("the refusal did not name the conflicting recovery routes: %q", stderr.String())
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatal("a refused conflicting invocation wrote the --out path")
	}
}
