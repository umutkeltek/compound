package gitrepository

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// The identity session amortizes the ref-INDEPENDENT half of an identity read.
// Two things must hold for it to be safe:
//
//  1. it changes NO identity value -- the fingerprint, object format, lineage
//     roots and remote identity must be identical to a standalone read of the
//     same repository and ref; and
//  2. it actually removes Git processes -- the object format and the canonical
//     origin are repository properties, so a session that read them once per
//     repository must issue one process for them, not one per ref.
//
// The lineage roots are deliberately NOT shared: a different base can name a
// different lineage, and the identity must fail closed on that.

func identityTestRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	snapshotRunGit(t, repo, "init", "-b", "main")
	snapshotRunGit(t, repo, "config", "user.email", "test@example.invalid")
	snapshotRunGit(t, repo, "config", "user.name", "Compound Test")
	snapshotRunGit(t, repo, "remote", "add", "origin", "https://example.invalid/compound/identity-synthetic.git")
	writeSnapshotFile(t, repo, "seed.txt", "seed\n")
	snapshotRunGit(t, repo, "add", "seed.txt")
	snapshotRunGit(t, repo, "commit", "-m", "seed")
	base := snapshotGitOutput(t, repo, "rev-parse", "HEAD")
	writeSnapshotFile(t, repo, "second.txt", "second\n")
	snapshotRunGit(t, repo, "add", "second.txt")
	snapshotRunGit(t, repo, "commit", "-m", "second")
	return repo, base
}

// TestIdentitySessionMatchesStandaloneRead is the differential proof: the
// session must produce exactly the identity a standalone read produces.
func TestIdentitySessionMatchesStandaloneRead(t *testing.T) {
	repo, base := identityTestRepo(t)
	head := snapshotGitOutput(t, repo, "rev-parse", "HEAD")

	for _, ref := range []string{base, head} {
		want, err := ReadIdentity(repo, ref)
		if err != nil {
			t.Fatalf("standalone identity at %s: %v", ref, err)
		}
		got, err := NewIdentitySession().ReadIdentityContext(context.Background(), repo, ref)
		if err != nil {
			t.Fatalf("session identity at %s: %v", ref, err)
		}
		if got.Fingerprint != want.Fingerprint ||
			got.ObjectFormat != want.ObjectFormat ||
			got.RemoteIdentity != want.RemoteIdentity ||
			strings.Join(got.LineageRoots, ",") != strings.Join(want.LineageRoots, ",") {
			t.Fatalf("session identity at %s = %+v, standalone = %+v", ref, got, want)
		}
	}
}

// TestIdentitySessionReadsRepositoryFactsOnce is the cost proof: reading the
// same repository at several refs through one session must issue ONE
// object-format read and ONE origin read, while the lineage read stays per
// distinct ref.
func TestIdentitySessionReadsRepositoryFactsOnce(t *testing.T) {
	repo, base := identityTestRepo(t)
	head := snapshotGitOutput(t, repo, "rev-parse", "HEAD")

	var lines []string
	ctx := ContextWithCommandFactory(context.Background(), func(ctx context.Context, name string, args ...string) *exec.Cmd {
		lines = append(lines, strings.Join(args, " "))
		return exec.CommandContext(ctx, name, args...)
	})
	count := func(prefix string) int {
		n := 0
		for _, line := range lines {
			if strings.Contains(line, prefix) {
				n++
			}
		}
		return n
	}

	session := NewIdentitySession()
	for _, ref := range []string{base, head, base, head} {
		if _, err := session.ReadIdentityContext(ctx, repo, ref); err != nil {
			t.Fatalf("session identity at %s: %v", ref, err)
		}
	}
	if got := count("rev-parse --show-object-format"); got != 1 {
		t.Fatalf("object format read %d times, want 1 (recorded: %v)", got, lines)
	}
	if got := count("config --get remote.origin.url"); got != 1 {
		t.Fatalf("origin read %d times, want 1 (recorded: %v)", got, lines)
	}
	// The lineage stays per distinct ref: two refs, two lineage reads.
	if got := count("rev-list --max-parents=0"); got != 2 {
		t.Fatalf("lineage read %d times, want 2 (one per distinct ref; recorded: %v)", got, lines)
	}
}

// TestIdentitySessionKeepsLineagePerRef proves the session did not weaken the
// base-dependent half.
//
// A repository can carry MORE THAN ONE root lineage -- an orphan commit is a
// second root. Two refs of that repository then name different lineages, and a
// session that shared one lineage across refs would hand both refs the same
// identity. The session must keep each ref's own lineage, and the identity it
// produces must equal the standalone identity for that ref.
func TestIdentitySessionKeepsLineagePerRef(t *testing.T) {
	repo, base := identityTestRepo(t)
	// An orphan commit is a second root lineage of the same repository.
	snapshotRunGit(t, repo, "checkout", "--orphan", "other")
	snapshotRunGit(t, repo, "commit", "--allow-empty", "-m", "orphan")
	orphan := snapshotGitOutput(t, repo, "rev-parse", "HEAD")

	session := NewIdentitySession()
	for _, ref := range []string{base, orphan} {
		want, err := ReadIdentity(repo, ref)
		if err != nil {
			t.Fatalf("standalone identity at %s: %v", ref, err)
		}
		got, err := session.ReadIdentityContext(context.Background(), repo, ref)
		if err != nil {
			t.Fatalf("session identity at %s: %v", ref, err)
		}
		if got.Fingerprint != want.Fingerprint {
			t.Fatalf("session identity at %s = %+v, standalone = %+v", ref, got, want)
		}
	}
	baseIdentity, err := session.ReadIdentityContext(context.Background(), repo, base)
	if err != nil {
		t.Fatal(err)
	}
	orphanIdentity, err := session.ReadIdentityContext(context.Background(), repo, orphan)
	if err != nil {
		t.Fatal(err)
	}
	if baseIdentity.Fingerprint == orphanIdentity.Fingerprint {
		t.Fatal("two refs naming different lineage roots produced one identity")
	}
}

// TestIdentityWithOriginReproducesOnlyTheSameLineage pins the commitment the
// origin-rename migration relies on: re-deriving an identity under its own
// origin reproduces it exactly, a different origin changes only the
// fingerprint, and another lineage under the original origin never reproduces
// the original fingerprint.
func TestIdentityWithOriginReproducesOnlyTheSameLineage(t *testing.T) {
	repo, base := identityTestRepo(t)
	head := snapshotGitOutput(t, repo, "rev-parse", "HEAD")
	original, err := ReadIdentity(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	same, err := original.WithOrigin(original.RemoteIdentity)
	if err != nil || same.Fingerprint != original.Fingerprint {
		t.Fatalf("identity under its own origin = %+v, %v; want %s", same, err, original.Fingerprint)
	}

	snapshotRunGit(t, repo, "remote", "set-url", "origin", "https://example.invalid/compound/renamed-synthetic.git")
	renamed, err := ReadIdentity(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Fingerprint == original.Fingerprint {
		t.Fatal("a renamed origin must change the fingerprint")
	}
	back, err := renamed.WithOrigin(original.RemoteIdentity)
	if err != nil || back.Fingerprint != original.Fingerprint || back.RemoteIdentity != original.RemoteIdentity {
		t.Fatalf("renamed identity under the previous origin = %+v, %v; want %s", back, err, original.Fingerprint)
	}

	emptyTree := snapshotGitOutput(t, repo, "hash-object", "-w", "-t", "tree", "/dev/null")
	otherRoot := snapshotGitOutput(t, repo, "commit-tree", emptyTree, "-m", "unrelated lineage")
	snapshotRunGit(t, repo, "replace", "--graft", base, otherRoot)
	grafted, err := ReadIdentity(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := grafted.WithOrigin(original.RemoteIdentity)
	if err != nil || foreign.Fingerprint == original.Fingerprint || strings.Join(foreign.LineageRoots, ",") != otherRoot {
		t.Fatalf("another lineage must not reproduce the original fingerprint: %+v, %v", foreign, err)
	}
	if _, err := ReadIdentity(repo, head); err != nil {
		t.Fatal(err)
	}
}
