package authority

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/mem"
)

func TestParsePorcelainCounts(t *testing.T) {
	text := "## main...origin/main [ahead 1]\n M README.md\nA  staged.txt\n?? scratch.txt\nR  old.txt -> new.txt\n"
	staged, unstaged, untracked := parsePorcelainCounts(text)
	if staged != 2 || unstaged != 1 || untracked != 1 {
		t.Fatalf("counts = staged:%d unstaged:%d untracked:%d", staged, unstaged, untracked)
	}
}

func TestImportGitSnapshotCapturesDirtyRepoState(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "changed\n")
	writeFile(t, filepath.Join(repo, "staged.txt"), "staged\n")
	runGit(t, repo, "add", "staged.txt")
	writeFile(t, filepath.Join(repo, "scratch.txt"), "scratch\n")

	snap, entity, facts, report, err := ImportGitSnapshot(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != SourceControlSchemaVersion {
		t.Fatalf("schema = %q", snap.SchemaVersion)
	}
	if snap.Status != "dirty" || snap.Staged != 1 || snap.Unstaged != 1 || snap.Untracked != 1 || snap.ChangedPaths != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Branch != "main" || snap.Head == "" {
		t.Fatalf("branch/head = %q/%q", snap.Branch, snap.Head)
	}
	if entity.Kind != "repo" || entity.Slug != snap.RepoID {
		t.Fatalf("entity = %+v snap=%+v", entity, snap)
	}
	if !hasString(entity.Identifiers, snap.GitRoot) {
		t.Fatalf("entity identifiers missing git root: %+v", entity.Identifiers)
	}
	for _, want := range []struct {
		predicate string
		object    string
	}{
		{"source_control_status", "dirty"},
		{"current_branch", "main"},
		{"staged_count", "1"},
		{"unstaged_count", "1"},
		{"untracked_count", "1"},
		{"changed_path_count", "3"},
	} {
		if !hasCandidate(facts, snap.RepoID, want.predicate, want.object) {
			t.Fatalf("missing candidate %s=%s in %+v", want.predicate, want.object, facts)
		}
	}
	if report.Status != "dirty" || report.CandidateCount != len(facts) {
		t.Fatalf("report = %+v facts=%d", report, len(facts))
	}
	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"changed_state_fingerprint":"sha256:`) {
		t.Fatalf("snapshot must carry a content/index fingerprint, got %s", encoded)
	}
}

func TestReadGitSnapshotReportsRequestedTargetStatus(t *testing.T) {
	repo := initGitRepo(t)
	dirtyTarget := filepath.Join(repo, "README.md")
	writeFile(t, dirtyTarget, "changed\n")
	cleanTarget := filepath.Join(repo, "clean-target.md")

	dirty, err := ReadGitSnapshot(dirtyTarget)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := ReadGitSnapshot(cleanTarget)
	if err != nil {
		t.Fatal(err)
	}
	dirtyJSON, err := json.Marshal(dirty)
	if err != nil {
		t.Fatal(err)
	}
	cleanJSON, err := json.Marshal(clean)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dirtyJSON), `"target_status":"dirty"`) {
		t.Fatalf("dirty target snapshot missing target_status=dirty: %s", dirtyJSON)
	}
	if !strings.Contains(string(cleanJSON), `"target_status":"clean"`) {
		t.Fatalf("clean target snapshot missing target_status=clean: %s", cleanJSON)
	}
}

func TestSourceControlFingerprintChangesWithContentPathAndIndexState(t *testing.T) {
	repo := initGitRepo(t)
	readme := filepath.Join(repo, "README.md")
	writeFile(t, readme, "alpha\n")
	alpha, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, readme, "beta\n")
	beta, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, readme, "alpha\n")
	alphaAgain, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if alpha.EvidenceRef == beta.EvidenceRef || alpha.EvidenceRef != alphaAgain.EvidenceRef {
		t.Fatalf("content fingerprint is not deterministic/sensitive: alpha=%q beta=%q alpha_again=%q", alpha.EvidenceRef, beta.EvidenceRef, alphaAgain.EvidenceRef)
	}

	runGit(t, repo, "add", "README.md")
	staged, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged.EvidenceRef == alpha.EvidenceRef {
		t.Fatalf("same bytes with different index state reused fingerprint %q", staged.EvidenceRef)
	}

	runGit(t, repo, "reset", "HEAD", "README.md")
	writeFile(t, readme, "initial\n")
	writeFile(t, filepath.Join(repo, "OTHER.md"), "alpha\n")
	otherPath, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if otherPath.EvidenceRef == alpha.EvidenceRef {
		t.Fatalf("same changed-path count/content on another path reused fingerprint %q", otherPath.EvidenceRef)
	}
}

func TestImportGitSnapshotAcceptsFileAndFuturePath(t *testing.T) {
	repo := initGitRepo(t)
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(repo, "docs", "new", "guide.md")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filePath, "draft\n")

	snap, entity, _, report, err := ImportGitSnapshot(filePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.GitRoot != canonicalRepo {
		t.Fatalf("git_root = %q, want canonical %q", snap.GitRoot, canonicalRepo)
	}
	if snap.RequestedPath != filePath {
		t.Fatalf("requested_path = %q, want file path %q", snap.RequestedPath, filePath)
	}
	if snap.Status != "dirty" || snap.Untracked != 1 {
		t.Fatalf("snapshot = %+v, want dirty untracked file", snap)
	}
	if report.EvidenceRef == "" || report.EvidenceRef != snap.EvidenceReference() {
		t.Fatalf("evidence ref report=%q snap=%q", report.EvidenceRef, snap.EvidenceReference())
	}
	if !hasString(entity.Identifiers, filePath) || !hasString(entity.Identifiers, canonicalRepo) {
		t.Fatalf("identifiers = %+v, want file path and repo root", entity.Identifiers)
	}

	futurePath := filepath.Join(repo, "docs", "future", "later.md")
	future, _, _, _, err := ImportGitSnapshot(futurePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if future.GitRoot != canonicalRepo || future.RequestedPath != futurePath {
		t.Fatalf("future snapshot = %+v", future)
	}
}

func TestGitRepositoryIdentityIsStableAcrossDescendantsAndBindsRemote(t *testing.T) {
	repo := initGitRepo(t)
	runGit(t, repo, "remote", "add", "origin", "https://source.example.invalid/acme/product.git")

	before, err := ReadGitRepositoryIdentity(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint == "" || before.ObjectFormat == "" || len(before.LineageRoots) != 1 || before.RemoteIdentity == "" {
		t.Fatalf("initial repository identity incomplete: %+v", before)
	}

	writeFile(t, filepath.Join(repo, "descendant.txt"), "descendant\n")
	runGit(t, repo, "add", "descendant.txt")
	runGit(t, repo, "commit", "-m", "descendant")
	after, err := ReadGitRepositoryIdentity(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if after.Fingerprint != before.Fingerprint {
		t.Fatalf("descendant commit changed stable repository fingerprint: before=%+v after=%+v", before, after)
	}

	runGit(t, repo, "remote", "set-url", "origin", "https://other.example.invalid/acme/product.git")
	replaced, err := ReadGitRepositoryIdentity(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Fingerprint == before.Fingerprint {
		t.Fatalf("different canonical origin retained repository fingerprint: before=%+v replaced=%+v", before, replaced)
	}

	snapshot, err := ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RepositoryFingerprint != replaced.Fingerprint {
		t.Fatalf("source-control snapshot fingerprint = %q, want owner identity %q", snapshot.RepositoryFingerprint, replaced.Fingerprint)
	}
}

func TestSourceControlEntityMergesExistingRemoteIdentity(t *testing.T) {
	snap := SourceControlSnapshot{
		SchemaVersion: SourceControlSchemaVersion,
		RepoID:        "repo:demo",
		GitRoot:       "/work/new/demo",
		Remote:        "https://example.invalid/demo.git",
		Status:        "clean",
	}
	existing := []mem.Entity{{
		Slug:        "repo:demo",
		Kind:        "repo",
		Identifiers: []string{"/work/old/demo"},
	}}
	entity := mergeSourceControlEntity(snap, existing)
	for _, want := range []string{"/work/old/demo", "/work/new/demo", "https://example.invalid/demo.git", "git_remote:https://example.invalid/demo.git"} {
		if !hasString(entity.Identifiers, want) {
			t.Fatalf("missing identifier %q: %+v", want, entity.Identifiers)
		}
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Compound Test")
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
