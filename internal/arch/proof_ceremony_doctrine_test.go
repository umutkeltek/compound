package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var proofCeremonyCanonicalClauses = []string{
	"a rebase, replay, or new head invalidates prior proof and requires a fresh battery.",
	"for source-controlled work, a committed stored successful proof is required: land-check candidate tip, current attached commit candidate, and stored proof head must match; close refuses a missing, mismatched, or rebased proof.",
}

var (
	fullProofBaseSHA = regexp.MustCompile(`^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)
	shellAssignment  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$`)
	baseArgument     = regexp.MustCompile(`(?:^|\s)--base(?:=|\s+)(\S+)`)
	devArgument      = regexp.MustCompile(`(?:^|\s)--dev(?:\s|$)`)
	proofTerm        = regexp.MustCompile(`(?i)\b(?:proof-check|certifying proof|canonical full profile|full profile)\b`)
	terminalTerm     = regexp.MustCompile(`(?i)\b(?:terminal|tty|pty)\b`)
	terminalRequired = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:proof-check|certifying proof|canonical full profile|full profile)\b[^.;\n]{0,100}\b(?:requires?|must use|must run|only from)\s+(?:a\s+)?(?:real\s+)?(?:terminal|tty|pty)\b`),
		regexp.MustCompile(`(?i)\b(?:real\s+)?(?:terminal|tty|pty)\s+is required\b[^.;\n]{0,80}\b(?:proof-check|certifying proof|canonical full profile|full profile)\b`),
	}
	staleProofStreamDoctrine = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:run|use)\b[^\n]{0,40}\b(?:terminal|tty|pty)\b[^\n]{0,80}\bstdout\s+direct\b`),
		regexp.MustCompile(`(?i)\b(?:pipes?|redirections?)\b[^\n]{0,100}\b(?:refus|unsupported|admission fails?)`),
		regexp.MustCompile(`(?i)\b(?:refus|unsupported|admission fails?)[^\n]{0,100}\b(?:pipes?|redirections?)\b`),
	}
)

func TestProofCeremonyDoctrineContract(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	paths := []string{
		"CONTRIBUTING.md",
		"skills/compound-land/SKILL.md",
		"skills/compound-verify/SKILL.md",
	}
	for _, rel := range paths {
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("read served proof doctrine %s: %v", rel, err)
			}
			body := string(raw)
			if missing := missingProofCeremonyCanonicalClauses(body); len(missing) > 0 {
				t.Errorf("%s missing canonical proof clauses: %v", rel, missing)
			}
			for _, finding := range staleProofTerminalFindings(body) {
				t.Errorf("%s: %s", rel, finding)
			}
			canonicalCount, findings := certifyingProofCommandFindings(body)
			if canonicalCount == 0 {
				t.Errorf("%s must show at least one canonical local proof-check command", rel)
			}
			for _, finding := range findings {
				t.Errorf("%s: %s", rel, finding)
			}
		})
	}
}

func TestProofCeremonySemanticGuardRejectsNegatedOppositeMeaning(t *testing.T) {
	tests := []struct {
		name        string
		replacement string
	}{
		{
			name:        "rebase does not invalidate",
			replacement: "A rebase invalidates nothing; a fresh battery is not required.",
		},
		{
			name:        "tips need not match",
			replacement: "Tips need not match; close does not refuse a missing or mismatched proof.",
		},
	}
	complete := strings.Join(proofCeremonyCanonicalClauses, "\n\n")
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			drifted := strings.Replace(complete, proofCeremonyCanonicalClauses[i], tc.replacement, 1)
			missing := missingProofCeremonyCanonicalClauses(drifted)
			if len(missing) != 1 || missing[0] != proofCeremonyCanonicalClauses[i] {
				t.Fatalf("negated doctrine missing clauses = %v", missing)
			}
		})
	}
}

func TestProofCeremonyCommandGuardRejectsMovingCanonicalBases(t *testing.T) {
	for _, base := range []string{"main", "HEAD", "refs/heads/main"} {
		t.Run(base, func(t *testing.T) {
			body := "```bash\ncompound proof-check --base " + base + " --work work_example\n```"
			count, findings := certifyingProofCommandFindings(body)
			if count != 1 || len(findings) != 1 {
				t.Fatalf("canonical moving base count=%d findings=%v", count, findings)
			}
		})
	}
}

func TestProofCeremonyCommandGuardAcceptsImmutableBasesAndDevMain(t *testing.T) {
	body := "```bash\n" +
		"compound proof-check --dev --base main\n" +
		"compound proof-check --base 0123456789abcdef0123456789abcdef01234567 --work work_sha1\n" +
		"compound proof-check --base 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef --work work_sha256\n" +
		"BASE_SHA=$(git rev-parse --verify \"main^{commit}\")\n" +
		"compound proof-check --base \"$BASE_SHA\" --work work_captured\n" +
		"```"
	count, findings := certifyingProofCommandFindings(body)
	if count != 3 || len(findings) != 0 {
		t.Fatalf("immutable canonical command count=%d findings=%v", count, findings)
	}
}

func TestProofCeremonyTerminalGuardAllowsExplicitNonRequirement(t *testing.T) {
	if findings := staleProofTerminalFindings("Certifying proof is agent-invocable; a real TTY is not required."); len(findings) != 0 {
		t.Fatalf("explicit non-requirement findings = %v", findings)
	}
	for _, stale := range []string{
		"Certifying proof requires a real TTY.",
		"A real terminal is required for canonical full profile proof.",
	} {
		if findings := staleProofTerminalFindings(stale); len(findings) == 0 {
			t.Fatalf("stale terminal doctrine accepted: %q", stale)
		}
	}
}

func missingProofCeremonyCanonicalClauses(body string) []string {
	normalized := normalizeProofDoctrine(body)
	var missing []string
	for _, clause := range proofCeremonyCanonicalClauses {
		if !strings.Contains(normalized, clause) {
			missing = append(missing, clause)
		}
	}
	return missing
}

func normalizeProofDoctrine(body string) string {
	body = strings.ToLower(strings.ReplaceAll(body, "`", ""))
	return strings.Join(strings.Fields(body), " ")
}

func staleProofTerminalFindings(body string) []string {
	var findings []string
	for _, clause := range strings.FieldsFunc(body, func(r rune) bool {
		return r == '\n' || r == '.' || r == ';'
	}) {
		lower := strings.ToLower(strings.Join(strings.Fields(clause), " "))
		if !proofTerm.MatchString(lower) || !terminalTerm.MatchString(lower) {
			continue
		}
		if strings.Contains(lower, "not required") ||
			strings.Contains(lower, "does not require") ||
			strings.Contains(lower, "do not require") {
			continue
		}
		for _, stale := range terminalRequired {
			if stale.MatchString(lower) {
				findings = append(findings, "stale terminal-dependent proof doctrine: "+strings.TrimSpace(clause))
				break
			}
		}
	}
	for _, stale := range staleProofStreamDoctrine {
		if match := stale.FindString(body); match != "" {
			findings = append(findings, "stale stream-dependent proof doctrine: "+match)
		}
	}
	return findings
}

func certifyingProofCommandFindings(markdown string) (int, []string) {
	canonicalCount := 0
	var findings []string
	for _, block := range markdownCodeBlocks(markdown) {
		assignments := make(map[string]bool)
		for _, command := range shellLogicalLines(block) {
			if match := shellAssignment.FindStringSubmatch(command); match != nil {
				assignments[match[1]] = immutableProofBaseAssignment(match[2])
				continue
			}
			if strings.HasPrefix(command, "#") || !strings.Contains(command, "proof-check") {
				continue
			}
			if devArgument.MatchString(command) {
				continue
			}
			canonicalCount++
			match := baseArgument.FindStringSubmatch(command)
			if match == nil {
				findings = append(findings, "canonical proof-check command lacks --base: "+command)
				continue
			}
			base := strings.Trim(match[1], "\"'")
			if !immutableProofBaseArgument(base, assignments) {
				findings = append(findings, "canonical proof-check --base must be a lowercase full SHA or a captured full-SHA variable: "+command)
			}
		}
	}
	return canonicalCount, findings
}

func markdownCodeBlocks(markdown string) [][]string {
	var blocks [][]string
	var current []string
	inFence := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				blocks = append(blocks, current)
				current = nil
			}
			inFence = !inFence
			continue
		}
		if inFence {
			current = append(current, line)
		}
	}
	return blocks
}

func shellLogicalLines(lines []string) []string {
	var logical []string
	for i := 0; i < len(lines); i++ {
		command := strings.TrimSpace(lines[i])
		for strings.HasSuffix(command, "\\") && i+1 < len(lines) {
			command = strings.TrimSpace(strings.TrimSuffix(command, "\\")) + " " + strings.TrimSpace(lines[i+1])
			i++
		}
		if command != "" {
			logical = append(logical, command)
		}
	}
	return logical
}

func immutableProofBaseAssignment(value string) bool {
	value = strings.TrimSpace(strings.Trim(value, "\"'"))
	if fullProofBaseSHA.MatchString(value) {
		return true
	}
	if !strings.HasPrefix(value, "$(git rev-parse --verify ") || !strings.HasSuffix(value, ")") {
		return false
	}
	ref := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "$(git rev-parse --verify "), ")"))
	ref = strings.Trim(ref, "\"'")
	return strings.HasSuffix(ref, "^{commit}") && len(strings.TrimSuffix(ref, "^{commit}")) > 0
}

func immutableProofBaseArgument(value string, assignments map[string]bool) bool {
	if fullProofBaseSHA.MatchString(value) {
		return true
	}
	var name string
	switch {
	case strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"):
		name = strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	case strings.HasPrefix(value, "$"):
		name = strings.TrimPrefix(value, "$")
	default:
		return false
	}
	return assignments[name]
}
