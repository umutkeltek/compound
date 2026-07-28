package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var staleProofTerminalDoctrine = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\breal\s+(?:terminal|tty|pty)\b`),
	regexp.MustCompile(`(?i)\b(?:proof-check|certifying proof|canonical full profile|full profile)\b[^\n]{0,100}\b(?:requires?|must use|must run|only from)\b[^\n]{0,60}\b(?:terminal|tty|pty)\b`),
	regexp.MustCompile(`(?i)\b(?:terminal|tty|pty)\b[^\n]{0,60}\b(?:is required|required for)\b[^\n]{0,80}\b(?:proof-check|certifying proof|canonical full profile|full profile)\b`),
	regexp.MustCompile(`(?i)\b(?:run|use)\b[^\n]{0,40}\b(?:terminal|tty|pty)\b[^\n]{0,80}\bstdout\s+direct\b`),
	regexp.MustCompile(`(?i)\b(?:pipes?|redirections?)\b[^\n]{0,100}\b(?:refus|unsupported|admission fails?)`),
	regexp.MustCompile(`(?i)\b(?:refus|unsupported|admission fails?)[^\n]{0,100}\b(?:pipes?|redirections?)\b`),
}

type proofCeremonySemanticRule struct {
	name   string
	groups [][]string
}

var proofCeremonySemanticRules = []proofCeremonySemanticRule{
	{
		name: "rebase or replay invalidates prior proof and requires a fresh battery",
		groups: [][]string{
			{"rebase", "replay", "new head", "new candidate tip", "changed head"},
			{"invalidates", "requires", "needs", "must"},
			{"fresh battery", "new battery", "new proof", "re-run", "rerun"},
		},
	},
	{
		name: "land-check candidate tip binds proof HEAD, attached commit, and close",
		groups: [][]string{
			{"land-check"},
			{"candidate tip", "land tip", "landing tip"},
			{"proof head", "proved head", "sealed head"},
			{"attached commit", "commit candidate", "attached candidate"},
			{"equal", "match", "identical", "bind", "same"},
			{"close"},
			{"refus", "reject", "block", "non-certifying", "cannot close"},
		},
	},
}

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
	docs := make(map[string]string, len(paths))
	for _, rel := range paths {
		raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read served proof doctrine %s: %v", rel, err)
		}
		docs[rel] = string(raw)
	}

	for _, rel := range paths {
		body := docs[rel]
		t.Run(rel, func(t *testing.T) {
			for _, stale := range staleProofTerminalDoctrine {
				if stale.MatchString(body) {
					t.Errorf("%s revives terminal-dependent proof admission with %q", rel, stale.FindString(body))
				}
			}

			commands := certifyingProofCommands(body)
			if len(commands) == 0 {
				t.Fatalf("%s must show at least one certifying local proof-check command", rel)
			}
			for _, command := range commands {
				if !strings.Contains(command, "--base ") {
					t.Errorf("%s certifying proof-check command must bind an explicit immutable base: %q", rel, command)
				}
			}
		})
	}

	for surface, findings := range proofCeremonySemanticFindings(docs) {
		for _, finding := range findings {
			t.Errorf("%s: %s", surface, finding)
		}
	}
}

func TestProofCeremonySemanticGuardRejectsOneDriftedSurface(t *testing.T) {
	const complete = `
A rebase, replay, or new HEAD invalidates prior proof and requires a fresh battery.
The land-check candidate tip must equal the stored proof HEAD and attached commit;
close refuses a mismatch.
`
	docs := map[string]string{
		"complete.md": complete,
		"drifted.md":  "Certifying proof is important, but this surface omits its identity rules.",
	}

	findings := proofCeremonySemanticFindings(docs)
	if got := len(findings["drifted.md"]); got != len(proofCeremonySemanticRules) {
		t.Fatalf("drifted surface findings = %v, want all %d semantic rules; one complete document must not mask another", findings, len(proofCeremonySemanticRules))
	}
	if got := findings["complete.md"]; len(got) != 0 {
		t.Fatalf("complete surface findings = %v, want none", got)
	}
}

func TestProofCeremonySemanticGuardAllowsNaturalWordingVariants(t *testing.T) {
	const variant = `
After a replay, the new proof must be rerun before certification.

The sealed HEAD, commit candidate, and landing tip in land-check bind to one identity;
close is blocked when they diverge.
`
	if findings := missingProofCeremonySemantics(variant); len(findings) != 0 {
		t.Fatalf("natural doctrine variant findings = %v, want none", findings)
	}
}

func certifyingProofCommands(markdown string) []string {
	var commands []string
	lines := strings.Split(markdown, "\n")
	inFence := false
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if !inFence || !strings.Contains(trimmed, "proof-check") {
			continue
		}

		command := trimmed
		for strings.HasSuffix(strings.TrimSpace(command), `\`) && i+1 < len(lines) {
			i++
			command += " " + strings.TrimSpace(lines[i])
		}
		if strings.Contains(command, "--work ") && !strings.Contains(command, "--dev") {
			commands = append(commands, command)
		}
	}
	return commands
}

func proofCeremonySemanticFindings(docs map[string]string) map[string][]string {
	findings := make(map[string][]string)
	for name, body := range docs {
		if missing := missingProofCeremonySemantics(body); len(missing) > 0 {
			findings[name] = missing
		}
	}
	return findings
}

func missingProofCeremonySemantics(body string) []string {
	var missing []string
	for _, rule := range proofCeremonySemanticRules {
		if !proofCeremonyParagraphMatches(body, rule.groups) {
			missing = append(missing, rule.name)
		}
	}
	return missing
}

func proofCeremonyParagraphMatches(body string, groups [][]string) bool {
	for _, paragraph := range strings.Split(strings.ToLower(body), "\n\n") {
		paragraph = strings.ReplaceAll(paragraph, "\n", " ")
		matches := true
		for _, alternatives := range groups {
			found := false
			for _, term := range alternatives {
				if strings.Contains(paragraph, term) {
					found = true
					break
				}
			}
			if !found {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
