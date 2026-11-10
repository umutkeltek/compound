package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
)

// A mistyped flag must stop a mutating home command. The shared home argument
// loop pockets every "-*" token it does not know, so `--dry-rnu` used to be
// ignored: the command the caller wanted to preview ran for real and exited 0.
// Proved through the real binary, because the refusal is an exit code.
func TestHomeMutationsRefuseUnknownFlags(t *testing.T) {
	t.Parallel()
	bin := compoundTestBinary(t)
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	seedArchivableReview(t, homePath, "pv_typo_guard")
	hot := filepath.Join(homePath, "reviews", "panel-verdicts", "pv_typo_guard.md")
	if _, err := os.Stat(hot); err != nil {
		t.Fatalf("fixture is not hot to begin with: %v", err)
	}

	for _, tc := range []struct {
		name   string
		args   []string
		report string
	}{
		{"archive-history", []string{"home", "archive-history", "--kind", "review", "--dry-rnu"}, "# home archive-history"},
		{"restore-history", []string{"home", "restore-history", "--all", "--dry-rnu"}, "# home restore-history"},
		{"compact", []string{"home", "compact", "--dry-rnu"}, "# home compact"},
	} {
		out, code := runHomeCLI(t, bin, homePath, tc.args...)
		if code != 2 {
			t.Errorf("%s with a mistyped flag: exit = %d, want 2 (refusal); output:\n%s", tc.name, code, out)
		}
		if !strings.Contains(out, "unknown flag --dry-rnu") {
			t.Errorf("%s did not name the unknown flag:\n%s", tc.name, out)
		}
		if strings.Contains(out, tc.report) {
			t.Errorf("%s ran although its flag was not understood:\n%s", tc.name, out)
		}
	}
	if _, err := os.Stat(hot); err != nil {
		t.Fatalf("a refused command moved the review artifact: %v", err)
	}
	if index, err := home.LoadOperationalArchiveIndex(homePath); err != nil || len(index) != 0 {
		t.Fatalf("a refused command wrote archive entries: %+v (err %v)", index, err)
	}

	// The documented flags keep working: a guard that broke the real commands
	// would be worse than the typo it catches.
	out, code := runHomeCLI(t, bin, homePath, "home", "compact", "--dry-run", "--rebuildable")
	if code != 0 || !strings.Contains(out, "dry_run: true") {
		t.Fatalf("home compact --dry-run --rebuildable: exit = %d, output:\n%s", code, out)
	}
	out, code = runHomeCLI(t, bin, homePath, "home", "archive-history", "--kind", "review", "--max-files", "5", "--dry-run")
	if code != 0 || !strings.Contains(out, "# home archive-history") {
		t.Fatalf("home archive-history --kind review --max-files 5 --dry-run: exit = %d, output:\n%s", code, out)
	}
	if _, err := os.Stat(hot); err != nil {
		t.Fatalf("a dry run moved the review artifact: %v", err)
	}
	out, code = runHomeCLI(t, bin, homePath, "home", "archive-history", "--kind", "review")
	if code != 0 {
		t.Fatalf("home archive-history --kind review: exit = %d, output:\n%s", code, out)
	}
	if _, err := os.Stat(hot); !os.IsNotExist(err) {
		t.Fatalf("home archive-history --kind review did not archive the review artifact (stat err %v)", err)
	}
	out, code = runHomeCLI(t, bin, homePath, "home", "restore-history", "--all", "--dry-run")
	if code != 0 || !strings.Contains(out, "# home restore-history") {
		t.Fatalf("home restore-history --all --dry-run: exit = %d, output:\n%s", code, out)
	}
}

func runHomeCLI(t *testing.T, bin, homePath string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %v: %v\n%s", args, err, out)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}
