package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// testRepoRoot is resolved exactly once, before any test in this package runs. This
// ordering is load-bearing: at least one test in this package calls os.Chdir, so a
// lazily-resolved cwd could end up building from the wrong directory.
var testRepoRoot string

// testBinDir holds the binaries built once per `go test` run by compoundTestBinary and
// compoundMockEngineTestBinary. Removed after m.Run() returns.
var testBinDir string

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testmain: os.Getwd: %v\n", err)
		os.Exit(1)
	}
	testRepoRoot = filepath.Join(wd, "..", "..")

	dir, err := os.MkdirTemp("", "compound-test-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testmain: create temp bin dir: %v\n", err)
		os.Exit(1)
	}
	testBinDir = dir

	code := m.Run()
	os.RemoveAll(testBinDir)
	os.Exit(code)
}

var (
	compoundBinOnce sync.Once
	compoundBinPath string
	compoundBinErr  error

	compoundMockEngineBinOnce sync.Once
	compoundMockEngineBinPath string
	compoundMockEngineBinErr  error
)

// compoundTestBinary builds ./cmd/compound exactly once per test binary run and returns
// the path to the built executable. It is lazy (behind sync.Once): a `go test -run
// TestOneThing` invocation that never execs the compound binary must not pay the build
// cost. Every caller in this package gets the same binary, replacing what used to be a
// fresh `go run`/`go build` per call site.
func compoundTestBinary(t *testing.T) string {
	t.Helper()
	compoundBinOnce.Do(func() {
		exe := filepath.Join(testBinDir, "compound")
		cmd := exec.Command("go", "build", "-o", exe, "./cmd/compound")
		cmd.Dir = testRepoRoot
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			compoundBinErr = fmt.Errorf("build ./cmd/compound: %w\n%s", err, out)
			return
		}
		compoundBinPath = exe
	})
	if compoundBinErr != nil {
		t.Fatalf("%v", compoundBinErr)
	}
	return compoundBinPath
}

// compoundMockEngineTestBinary builds ../compound-mock-engine (relative to cmd/compound,
// i.e. ./cmd/compound-mock-engine relative to the repo root) exactly once per test binary
// run and returns the path to the built executable. Lazy for the same reason as
// compoundTestBinary.
func compoundMockEngineTestBinary(t *testing.T) string {
	t.Helper()
	compoundMockEngineBinOnce.Do(func() {
		exe := filepath.Join(testBinDir, "compound-mock-engine")
		cmd := exec.Command("go", "build", "-o", exe, "./cmd/compound-mock-engine")
		cmd.Dir = testRepoRoot
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			compoundMockEngineBinErr = fmt.Errorf("build ./cmd/compound-mock-engine: %w\n%s", err, out)
			return
		}
		compoundMockEngineBinPath = exe
	})
	if compoundMockEngineBinErr != nil {
		t.Fatalf("%v", compoundMockEngineBinErr)
	}
	return compoundMockEngineBinPath
}
