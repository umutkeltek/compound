package arch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// buildFreshnessTimeout bounds EACH external call (git + the installed binary). Both are
// sequential in checkBuildFreshnessAt, so worst case is 2x this — kept well under the
// freshness check's <2s repo-side budget (see W2 H0: hooks/consumers run the installed
// binary with no way to tell it is stale; this check is INFO/WARN, never a failing gate).
const buildFreshnessTimeout = 700 * time.Millisecond

// buildFreshnessVersionRE extracts the embedded build SHA from `compound version` output
// (format: "compound <version> (build <sha|dev>)" — see cmd/compound/main.go's buildSHA
// ldflags var). Liberal on purpose so a version-string reformat doesn't silently break
// parsing into a false "missing SHA" verdict.
var buildFreshnessVersionRE = regexp.MustCompile(`\(build ([0-9a-fA-F]+|dev)\)`)

// BuildFreshnessStatus classifies an installed binary against the repo's current HEAD.
type BuildFreshnessStatus string

const (
	BuildFresh   BuildFreshnessStatus = "fresh"   // installed SHA matches (a prefix of) HEAD
	BuildStale   BuildFreshnessStatus = "stale"   // installed SHA differs from HEAD
	BuildMissing BuildFreshnessStatus = "missing" // no binary at the installed path
	BuildUnknown BuildFreshnessStatus = "unknown" // binary present but no parseable embedded SHA, or HEAD unresolvable
)

// ParseBuildSHA extracts the embedded build SHA from `compound version` output. Returns
// "" if the output doesn't carry a parseable "(build <sha>)" segment.
func ParseBuildSHA(versionOutput string) string {
	m := buildFreshnessVersionRE.FindStringSubmatch(versionOutput)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// classifyBuildFreshness is the PURE decision at the center of the check: given an
// already-extracted installed SHA ("" if unparseable) and the repo HEAD SHA, decide
// fresh/stale/unknown. No filesystem or process access — BuildMissing is decided by the
// caller (the binary path didn't exist at all), never here.
func classifyBuildFreshness(installedSHA, headSHA string) BuildFreshnessStatus {
	installedSHA = strings.TrimSpace(installedSHA)
	headSHA = strings.TrimSpace(headSHA)
	if installedSHA == "" || strings.EqualFold(installedSHA, "dev") || headSHA == "" {
		return BuildUnknown
	}
	if strings.HasPrefix(headSHA, installedSHA) || strings.HasPrefix(installedSHA, headSHA) {
		return BuildFresh
	}
	return BuildStale
}

// checkBuildFreshnessAt is the testable seam: binaryPath is stat'd directly (a fixture
// path, never resolved here), and headFn/versionFn are injected so tests never need a
// real git repo or a real installed compound binary — fakes/stubs only. Always returns
// exactly one Finding, and NEVER Fail: a stale/missing installed binary is expected
// mid-session (landing always outdates it until `make install` reruns) and must not
// flip the proof-check battery red.
func checkBuildFreshnessAt(binaryPath string, headFn func() (string, error), versionFn func(string) (string, error)) []Finding {
	if _, err := os.Stat(binaryPath); err != nil {
		return []Finding{{
			Check:    "build-freshness",
			Severity: Info,
			Message:  fmt.Sprintf("no installed binary at %s (run `make install`) — status=%s", binaryPath, BuildMissing),
		}}
	}
	headSHA, err := headFn()
	if err != nil || strings.TrimSpace(headSHA) == "" {
		return []Finding{{
			Check:    "build-freshness",
			Severity: Info,
			Message:  fmt.Sprintf("could not resolve HEAD sha — status=%s", BuildUnknown),
		}}
	}
	verOut, err := versionFn(binaryPath)
	if err != nil {
		return []Finding{{
			Check:    "build-freshness",
			Severity: Warn,
			Message:  fmt.Sprintf("installed binary %s did not respond to `version`: %v — status=%s", binaryPath, err, BuildUnknown),
		}}
	}
	installedSHA := ParseBuildSHA(verOut)
	status := classifyBuildFreshness(installedSHA, headSHA)
	switch status {
	case BuildFresh:
		return []Finding{{
			Check:    "build-freshness",
			Severity: Info,
			Message:  fmt.Sprintf("installed binary %s matches HEAD %s — status=%s", binaryPath, headSHA, status),
		}}
	case BuildStale:
		return []Finding{{
			Check:    "build-freshness",
			Severity: Warn,
			Message:  fmt.Sprintf("installed binary %s is build %s, HEAD is %s — run `make install` — status=%s", binaryPath, installedSHA, headSHA, status),
		}}
	default:
		return []Finding{{
			Check:    "build-freshness",
			Severity: Info,
			Message:  fmt.Sprintf("installed binary %s has no embedded build SHA (predates this feature or built without ldflags) — status=%s", binaryPath, status),
		}}
	}
}

// checkBuildFreshnessContract is the real (impure) wiring Doctor() calls: resolves the
// installed binary path the same way install.sh does, git HEAD in repoRoot, and the
// installed binary's own `version` output.
func checkBuildFreshnessContract(repoRoot string) []Finding {
	return checkBuildFreshnessAt(installedBinaryPath(), func() (string, error) { return gitHeadSHA(repoRoot) }, runInstalledVersion)
}

// installedBinaryPath mirrors install.sh: $GOBIN/compound if GOBIN is set, else
// $HOME/.local/bin/compound.
func installedBinaryPath() string {
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		return filepath.Join(gobin, "compound")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".local", "bin", "compound")
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "bin", "compound")
}

func gitHeadSHA(repoRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), buildFreshnessTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runInstalledVersion(binaryPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), buildFreshnessTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, binaryPath, "version").Output()
	return string(out), err
}
