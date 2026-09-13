package work

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A fresh linked worktree carries no ignored files. Before the fallback, every
// missing ceremony input was silently skipped and the battery went red later on
// gates unrelated to the change (leak scan unarmed, profiles drift, console deps).
func TestCopyProofCeremonyInputsFillsAFreshWorktreeFromTheMainCheckout(t *testing.T) {
	mainRepo, _ := initLandingRepo(t)
	writeLandingFile(t, filepath.Join(mainRepo, ".gitignore"), "docs_private/\n")
	landingGit(t, mainRepo, "add", ".gitignore")
	landingGit(t, mainRepo, "commit", "-m", "ignore private inputs")
	writeCeremonyInput(t, filepath.Join(mainRepo, "docs_private", "leak-tokens.txt"), "main tokens\n")
	writeCeremonyInput(t, filepath.Join(mainRepo, "docs_private", "profiles", "overlay.json"), "main overlay\n")

	worktree := filepath.Join(t.TempDir(), "worktree")
	landingGit(t, mainRepo, "worktree", "add", "--detach", worktree)
	writeCeremonyInput(t, filepath.Join(worktree, "docs_private", "profiles", "overlay.json"), "worktree overlay\n")

	sealed := t.TempDir()
	if err := CopyProofCeremonyInputs(context.Background(), worktree, sealed, exec.CommandContext); err != nil {
		t.Fatal(err)
	}
	for relativePath, want := range map[string]string{
		"docs_private/leak-tokens.txt":       "main tokens\n",
		"docs_private/profiles/overlay.json": "worktree overlay\n",
	} {
		got, err := os.ReadFile(filepath.Join(sealed, filepath.FromSlash(relativePath)))
		if err != nil || string(got) != want {
			t.Fatalf("sealed %s = %q (err %v), want %q", relativePath, got, err, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(sealed, "console", "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("an input neither checkout has must stay absent: %v", err)
	}
}

func writeCeremonyInput(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLandingFile(t, path, body)
}
