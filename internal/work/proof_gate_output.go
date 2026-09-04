package work

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ProofGateFailureLineCap bounds the per-gate failure_lines array. A red go-test run over
// ~150 packages can emit dozens of FAIL lines per package; 200 keeps the report JSON bounded
// while still covering every named failing test in any realistic battery run.
const ProofGateFailureLineCap = 200

// proofGateFailureLinePatterns is the canonical set of markers extracted into FailureLines.
// Each entry is a Go regexp tested against a single line. The match-any semantics intentionally
// captures any of the four markers a triager searches for first: the test name (--- FAIL:),
// the package summary (FAIL\t), a panic stack head (panic:), or a go-test timeout notice.
var proofGateFailureLinePatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{name: "test-fail", pattern: regexp.MustCompile(`^--- FAIL:`)},
	{name: "package-fail", pattern: regexp.MustCompile(`^FAIL\t`)},
	{name: "panic", pattern: regexp.MustCompile(`^panic:`)},
	{name: "timeout", pattern: regexp.MustCompile(`timed out`)},
}

// ExtractGoTestFailureLines returns up to ProofGateFailureLineCap lines from output that match
// any proofGateFailureLinePatterns entry, preserving source order. The result is nil when
// nothing matches; an empty output also yields nil. Each returned line is the raw line without
// trailing newline, exactly as the test emitted it.
func ExtractGoTestFailureLines(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	var matched []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		for _, entry := range proofGateFailureLinePatterns {
			if entry.pattern.MatchString(line) {
				matched = append(matched, line)
				break
			}
		}
		if len(matched) >= ProofGateFailureLineCap {
			break
		}
	}
	if len(matched) == 0 {
		return nil
	}
	return matched
}

// PersistGateOutput writes the full output bytes next to the --out report at
// <reportPath>.gates/<safe-gate-name>.log and returns the absolute path. Returns "" with no
// error when reportPath is empty (a gate run without --out has no sibling directory to own) or
// when output is empty (nothing to persist). The safe filename drops any character that is not
// alphanumerics or [-_.] so a maliciously-named gate cannot escape the gates directory.
func PersistGateOutput(reportPath, gateName string, output []byte) (string, error) {
	if strings.TrimSpace(reportPath) == "" || len(output) == 0 {
		return "", nil
	}
	reportDir := filepath.Dir(reportPath)
	leaf := filepath.Base(reportPath)
	gatesDir := filepath.Join(reportDir, leaf+".gates")
	if err := os.MkdirAll(gatesDir, 0o755); err != nil {
		return "", fmt.Errorf("create gates directory: %w", err)
	}
	target := filepath.Join(gatesDir, proofGateSafeFileName(gateName)+".log")
	if err := os.WriteFile(target, output, 0o600); err != nil {
		return "", fmt.Errorf("write gate output: %w", err)
	}
	return target, nil
}

func proofGateSafeFileName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "gate"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "gate"
	}
	return b.String()
}
