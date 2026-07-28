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

	combined := strings.ToLower(strings.Join([]string{
		docs["CONTRIBUTING.md"],
		docs["skills/compound-land/SKILL.md"],
		docs["skills/compound-verify/SKILL.md"],
	}, "\n"))
	requireSemanticRelation(t, combined, "rebase or replay invalidates prior proof",
		`(?s)\b(?:rebase|replay|new head|new candidate tip)\b.{0,240}\b(?:new battery|fresh battery|new proof|re-run)\b`,
	)
	requireSemanticRelation(t, combined, "landing binds proof HEAD to candidate tip and close",
		`(?s)\bland-check\b.{0,160}\bcandidate tip\b.{0,240}\battached commit\b.{0,240}\bproof head\b.{0,400}\bclose refuses\b`,
		`(?s)\bland-check\b.{0,160}\bcandidate tip\b.{0,240}\bproof head\b.{0,240}\battached commit\b.{0,400}\bclose refuses\b`,
	)
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

func requireSemanticRelation(t *testing.T, body, doctrine string, alternatives ...string) {
	t.Helper()
	for _, expression := range alternatives {
		if regexp.MustCompile(expression).MatchString(body) {
			return
		}
	}
	t.Errorf("served proof doctrine must state a local semantic relation: %s", doctrine)
}
