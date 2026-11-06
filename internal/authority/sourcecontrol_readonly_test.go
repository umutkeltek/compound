package authority

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadGitSnapshotDoesNotRefreshIndex(t *testing.T) {
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	repo := initGitRepo(t)
	// Only metadata changes: status stays clean but ordinary Git refreshes the index.
	stamp := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "README.md"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != "clean" {
		t.Fatalf("metadata-only change reported dirty: %+v", snap)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !os.SameFile(beforeInfo, afterInfo) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("source-control read rewrote Git index")
	}
	if _, err := os.Lstat(index + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("source-control read left an index lock or unknown state: %v", err)
	}
	// Positive control: this fixture must actually exercise Git's optional refresh.
	runGit(t, repo, "status", "--porcelain")
	refreshed, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, refreshed) {
		t.Fatal("fixture did not exercise optional index refresh")
	}
}

func TestReadGitSnapshotIgnoresAmbientRepositorySelectors(t *testing.T) {
	repo := initGitRepo(t)
	foreign := initGitRepo(t)
	writeFile(t, filepath.Join(foreign, "foreign.txt"), "foreign staged state\n")
	runGit(t, foreign, "add", "foreign.txt")
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(foreign, ".git", "index"))
	snap, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if snap.GitRoot != expected || snap.Status != "clean" {
		t.Fatalf("ambient Git selectors redirected source-control read: %+v", snap)
	}
}
