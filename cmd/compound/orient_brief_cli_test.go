package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
)

// `compound orient --brief` must reach the renderer through the real command:
// plain text, the checkout's facts first, inside the cap, and the full-pack
// command (same arguments, no --brief) on the last line. The renderer's own
// tests cannot see a missing dispatch line; this one does.
func TestCmdOrientBriefServesRepoFactsAsPlainText(t *testing.T) {
	homePath, cwd := initBodyPackTestHome(t)
	projection := contract.RepoBriefs{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Briefs: []contract.RepoBrief{{
			RepoRoot: cwd,
			Title:    "Example Lantern",
			Lines:    []string{"State: LIVE 2.3.4", "Pending build: 2.3.5 (9) VALID, not attached"},
		}},
	}
	data, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(homePath, filepath.FromSlash(contract.RepoBriefsRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", cwd, "--task", "prepare the next release", "--brief"})
	})
	if len(out) > contract.OrientBriefMaxBytes {
		t.Fatalf("brief is %d bytes, cap %d", len(out), contract.OrientBriefMaxBytes)
	}
	if json.Valid([]byte(out)) {
		t.Fatalf("--brief must print plain text, not the JSON pack:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "Example Lantern (product repo)") || lines[1] != "- State: LIVE 2.3.4" || lines[2] != "- Pending build: 2.3.5 (9) VALID, not attached" {
		t.Fatalf("brief must open with the repo facts:\n%s", out)
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "Full pack: compound orient --harness codex --cwd ") || strings.Contains(last, "--brief") || !strings.Contains(last, "ctx_") {
		t.Fatalf("last line must give the full-pack command and the recorded pack id, got %q", last)
	}

	full := captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", cwd, "--task", "prepare the next release"})
	})
	if !json.Valid([]byte(full)) {
		t.Fatalf("without --brief orient must still print the JSON pack:\n%.400s", full)
	}
}
