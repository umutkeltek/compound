package arch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBuildSHA(t *testing.T) {
	cases := map[string]string{
		"compound 0.1.0 (build a1b2c3d)\n": "a1b2c3d",
		"compound 0.1.0 (build dev)\n":     "dev",
		"compound 0.1.0\n":                 "", // pre-embed binary: no build segment at all
		"garbage output":                   "",
	}
	for in, want := range cases {
		if got := ParseBuildSHA(in); got != want {
			t.Errorf("ParseBuildSHA(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyBuildFreshness(t *testing.T) {
	cases := []struct {
		installed, head string
		want            BuildFreshnessStatus
	}{
		{"a1b2c3d", "a1b2c3d", BuildFresh},
		{"a1b2c3d", "a1b2c3dxx", BuildFresh}, // installed is a prefix of a longer HEAD sha
		{"a1b2c3d", "b2c3d4e", BuildStale},
		{"dev", "a1b2c3d", BuildUnknown},
		{"", "a1b2c3d", BuildUnknown},
		{"a1b2c3d", "", BuildUnknown},
	}
	for _, c := range cases {
		if got := classifyBuildFreshness(c.installed, c.head); got != c.want {
			t.Errorf("classifyBuildFreshness(%q,%q) = %q, want %q", c.installed, c.head, got, c.want)
		}
	}
}

// TestCheckBuildFreshnessAtMissing proves the reporter classifies a never-installed
// binary as missing, INFO (never Fail), with no real installed binary required — a
// nonexistent temp path stands in for the fixture.
func TestCheckBuildFreshnessAtMissing(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "compound-does-not-exist")
	called := false
	findings := checkBuildFreshnessAt(binaryPath,
		func() (string, error) { called = true; return "a1b2c3d", nil },
		func(string) (string, error) { called = true; return "", nil },
	)
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want exactly one", findings)
	}
	f := findings[0]
	if f.Severity == Fail {
		t.Fatalf("missing binary must never Fail the battery, got %+v", f)
	}
	if f.Severity != Info || !strings.Contains(f.Message, string(BuildMissing)) {
		t.Fatalf("finding = %+v, want Info mentioning %q", f, BuildMissing)
	}
	if called {
		t.Fatal("headFn/versionFn should not run when the binary is missing")
	}
}

// TestCheckBuildFreshnessAtFresh/Stale prove the fresh/stale split using a stub
// binaryPath (a fixture file that merely needs to exist for os.Stat — its content is
// never read) and injected headFn/versionFn, per the falsifier: no real installed
// compound binary required.
func TestCheckBuildFreshnessAtFresh(t *testing.T) {
	binaryPath := fakeBinary(t)
	findings := checkBuildFreshnessAt(binaryPath,
		func() (string, error) { return "a1b2c3d", nil },
		func(string) (string, error) { return "compound 0.1.0 (build a1b2c3d)\n", nil },
	)
	f := requireOneFinding(t, findings)
	if f.Severity == Fail {
		t.Fatalf("fresh must never Fail, got %+v", f)
	}
	if !strings.Contains(f.Message, string(BuildFresh)) {
		t.Fatalf("finding = %+v, want it to report %q", f, BuildFresh)
	}
}

func TestCheckBuildFreshnessAtStale(t *testing.T) {
	binaryPath := fakeBinary(t)
	findings := checkBuildFreshnessAt(binaryPath,
		func() (string, error) { return "deadbee", nil },
		func(string) (string, error) { return "compound 0.1.0 (build a1b2c3d)\n", nil },
	)
	f := requireOneFinding(t, findings)
	if f.Severity == Fail {
		t.Fatalf("stale must never Fail the proof-check battery (landing always outdates the installed binary), got %+v", f)
	}
	if f.Severity != Warn || !strings.Contains(f.Message, string(BuildStale)) {
		t.Fatalf("finding = %+v, want Warn mentioning %q", f, BuildStale)
	}
}

func TestCheckBuildFreshnessAtUnknownWhenSHAUnparseable(t *testing.T) {
	binaryPath := fakeBinary(t)
	findings := checkBuildFreshnessAt(binaryPath,
		func() (string, error) { return "a1b2c3d", nil },
		func(string) (string, error) { return "compound 0.1.0\n", nil }, // pre-embed binary
	)
	f := requireOneFinding(t, findings)
	if f.Severity == Fail {
		t.Fatalf("unparseable sha must never Fail, got %+v", f)
	}
	if !strings.Contains(f.Message, string(BuildUnknown)) {
		t.Fatalf("finding = %+v, want it to report %q", f, BuildUnknown)
	}
}

func TestCheckBuildFreshnessAtVersionCommandErrors(t *testing.T) {
	binaryPath := fakeBinary(t)
	findings := checkBuildFreshnessAt(binaryPath,
		func() (string, error) { return "a1b2c3d", nil },
		func(string) (string, error) { return "", errors.New("boom") },
	)
	f := requireOneFinding(t, findings)
	if f.Severity == Fail {
		t.Fatalf("a version-command error must never Fail, got %+v", f)
	}
	if f.Severity != Warn {
		t.Fatalf("finding = %+v, want Warn", f)
	}
}

func fakeBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compound")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireOneFinding(t *testing.T, findings []Finding) Finding {
	t.Helper()
	if len(findings) != 1 {
		t.Fatalf("findings = %v, want exactly one", findings)
	}
	return findings[0]
}

// TestCheckBuildFreshnessContractNeverFails proves the real (impure) wiring Doctor()
// calls also never returns Fail, regardless of what's actually installed on the machine
// running the test — the repo root here is this checkout, so gitHeadSHA resolves for
// real, but the installed binary at installedBinaryPath() may be absent, stale, or
// pre-embed; every one of those is a reported state, not a failure.
func TestCheckBuildFreshnessContractNeverFails(t *testing.T) {
	findings := checkBuildFreshnessContract(repoRoot)
	f := requireOneFinding(t, findings)
	if f.Severity == Fail {
		t.Fatalf("build-freshness must never Fail the doctor, got %+v", f)
	}
	if f.Check != "build-freshness" {
		t.Fatalf("finding.Check = %q, want build-freshness", f.Check)
	}
}
