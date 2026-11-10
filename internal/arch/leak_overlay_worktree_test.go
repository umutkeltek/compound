package arch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A lane is a linked worktree, and `docs_private/` is gitignored, so a lane never
// carries the operator's token overlays. The doctor used to look for them under
// the tree it was pointed at only: in a lane the operator token pass did not run,
// a private name passed the pre-landing proof, and the same doctor failed on main
// after the landing. The lane must apply the main checkout's overlays.
func TestDocLeakLinkedWorktreeBorrowsMainCheckoutOverlay(t *testing.T) {
	mainRepo, runGit := newDocLeakGitRepo(t)
	runGit("config", "user.email", "test@example.invalid")
	runGit("config", "user.name", "test")
	mustWriteArchFixture(t, mainRepo, ".gitignore", "docs_private/\n")
	mustWriteArchFixture(t, mainRepo, filepath.Join("docs", "guide.md"), "plain words\n")
	mustWriteArchFixture(t, mainRepo, filepath.Join("docs_private", "leak-tokens.txt"), "lane-doc-token\n")
	mustWriteArchFixture(t, mainRepo, filepath.Join("docs_private", "source-leak-tokens.txt"), "lane-source-token\n")
	runGit("add", ".gitignore", "docs/guide.md")
	runGit("commit", "--quiet", "-m", "base")

	lane := filepath.Join(t.TempDir(), "lane")
	runGit("worktree", "add", "--quiet", "-b", "lane", lane)
	if _, err := os.Stat(filepath.Join(lane, "docs_private")); !os.IsNotExist(err) {
		t.Fatalf("fixture is wrong: the lane carries docs_private (stat err %v)", err)
	}
	if got := mainWorktreeRoot(lane); !sameDir(t, got, mainRepo) {
		t.Fatalf("mainWorktreeRoot(lane) = %q, want the main checkout %q", got, mainRepo)
	}
	if got := mainWorktreeRoot(mainRepo); got != "" {
		t.Fatalf("mainWorktreeRoot(main checkout) = %q, want none", got)
	}

	// The lane's change names one token of each overlay, in a tracked file.
	mustWriteArchFixture(t, lane, filepath.Join("docs", "guide.md"), "plain words\nthis line names lane-doc-token\n")
	mustWriteArchFixture(t, lane, filepath.Join("internal", "probe", "probe.go"), "package probe // lane-source-token\n")
	gitIn(t, lane, "add", "docs/guide.md", "internal/probe/probe.go")

	findings := checkDocLeak(lane, true)
	if !hasDocLeakFailContaining(findings, "docs/guide.md", "lane-doc-token") {
		t.Fatalf("the lane's doctor did not apply the main checkout's doc overlay: %+v", findings)
	}
	if !hasDocLeakFailContaining(findings, "internal/probe/probe.go", "lane-source-token") {
		t.Fatalf("the lane's doctor did not apply the main checkout's source overlay: %+v", findings)
	}
	for _, finding := range findings {
		if strings.Contains(finding.Message, "leak-tokens.txt absent") {
			t.Fatalf("the lane still reports the overlay as absent: %+v", finding)
		}
	}

	// An overlay placed in the tree itself wins: that is how a sealed proof
	// checkout is armed, and it must not be second-guessed from outside.
	mustWriteArchFixture(t, lane, filepath.Join("docs_private", "leak-tokens.txt"), "some-other-token\n")
	if hasDocLeakFailContaining(checkDocLeak(lane, true), "docs/guide.md", "lane-doc-token") {
		t.Fatal("a tree with its own doc overlay still applied the main checkout's")
	}

	// A clone has its own repository and no main checkout to borrow from, so it
	// keeps the tracked baseline alone, exactly as before.
	clone := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "--quiet", mainRepo, clone).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	mustWriteArchFixture(t, clone, filepath.Join("docs", "guide.md"), "plain words\nthis line names lane-doc-token\n")
	gitIn(t, clone, "add", "docs/guide.md")
	if got := mainWorktreeRoot(clone); got != "" {
		t.Fatalf("mainWorktreeRoot(clone) = %q, want none", got)
	}
	cloneFindings := checkDocLeak(clone, true)
	if hasDocLeakFailContaining(cloneFindings, "lane-doc-token") {
		t.Fatalf("a clone applied an overlay it does not own: %+v", cloneFindings)
	}
	var absent bool
	for _, finding := range cloneFindings {
		absent = absent || strings.Contains(finding.Message, "leak-tokens.txt absent")
	}
	if !absent {
		t.Fatalf("a clone without an overlay should say so: %+v", cloneFindings)
	}
}

func gitIn(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %v: %v\n%s", repo, args, err, out)
	}
}

func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	if a == "" || b == "" {
		return false
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	return ra == rb
}
