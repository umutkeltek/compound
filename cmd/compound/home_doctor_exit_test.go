package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
)

const homeDoctorExitChildEnv = "COMPOUND_TEST_HOME_DOCTOR_EXIT_CHILD"

func TestHomeDoctorJSONFailureExitsNonZero(t *testing.T) {
	t.Parallel()
	if os.Getenv(homeDoctorExitChildEnv) == "1" {
		cmdHome([]string{"doctor", "--json"})
		return
	}

	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(homePath, "global", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(homePath, "invented-feature-root"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHomeDoctorJSONFailureExitsNonZero$")
	cmd.Env = append(os.Environ(), homeDoctorExitChildEnv+"=1", "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("home doctor --json failure exit = %v, want exit 1; output:\n%s", err, out)
	}
	var report homeDoctorJSONReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\n%s", err, out)
	}
	if report.Verdict != "fail" || report.Habitat.OK {
		t.Fatalf("doctor failure report = %+v", report)
	}
}

const homeDoctorTextExitChildEnv = "COMPOUND_TEST_HOME_DOCTOR_TEXT_EXIT_CHILD"

// A global home inside a git repository is the generated-state-in-source
// smell. Text mode prints a failing verdict for it; the exit code must follow
// the printed verdict, exactly as the --json path does. Before this guard the
// text path printed `verdict: MISSING` and exited 0.
func TestHomeDoctorTextRepoSourceSmellExitsNonZero(t *testing.T) {
	t.Parallel()
	if os.Getenv(homeDoctorTextExitChildEnv) == "1" {
		cmdHome([]string{"doctor"})
		return
	}

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(repo, ".compound")
	if err := home.Init(homePath, "global", cliTestNow); err != nil {
		t.Fatal(err)
	}
	// The smell must be the only failing input, or an unrelated doctor failure
	// would make this test pass without the exit-code rule.
	report := home.DoctorWithOptions(homePath, home.DoctorOptions{})
	if !report.InsideGitRepo || !report.OK || !report.PrivateCustody.OK {
		t.Fatalf("fixture must fail on the repo-source smell alone: inside_git_repo=%v ok=%v private_custody=%v",
			report.InsideGitRepo, report.OK, report.PrivateCustody.OK)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHomeDoctorTextRepoSourceSmellExitsNonZero$")
	cmd.Env = append(os.Environ(), homeDoctorTextExitChildEnv+"=1", "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if !strings.Contains(text, "generated_state_in_repo_source: true") || !strings.Contains(text, "verdict: "+homeOKStr(false)) {
		t.Fatalf("text doctor did not print the failing smell verdict:\n%s", text)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("home doctor printed a failing verdict but exit = %v, want exit 1; output:\n%s", err, text)
	}
}
