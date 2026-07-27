package policy

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newScanRepo builds a real git repo, because the undeclared-accumulator discriminator is
// `git check-ignore` and a fake directory tree would not exercise it.
func newScanRepo(t *testing.T, gitignore string) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable (%v): %s", err, out)
		}
	}
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatalf("write .gitignore: %v", err)
		}
	}
	return root
}

// writeDirOfSize creates dir containing one file of exactly size bytes.
func writeDirOfSize(t *testing.T, dir string, size int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), make([]byte, size), 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
}

func TestScanClaimsKnownBuildCaches(t *testing.T) {
	root := newScanRepo(t, "target/\n")
	writeDirOfSize(t, filepath.Join(root, "target"), 4096)

	report := ScanRetention([]string{root})
	if len(report.Declared) != 1 {
		t.Fatalf("want 1 declared instance, got %d (%+v)", len(report.Declared), report.Declared)
	}
	if report.Declared[0].ClassID != "worktree-build-cache" {
		t.Fatalf("class = %q", report.Declared[0].ClassID)
	}
	if !report.Declared[0].Rebuildable || report.ReclaimableBytes == 0 {
		t.Fatalf("a build cache must count as reclaimable: %+v", report.Declared[0])
	}
	if len(report.Undeclared) != 0 {
		t.Fatalf("a claimed cache must not be undeclared: %+v", report.Undeclared)
	}
}

// THE point of the package. An earlier version of this scan looked only at an allow-list of
// cache names, which meant `Undeclared` could never be populated and the check could never
// fail — a silent-green gate, the exact failure mode this registry exists to prevent. If this
// test regresses, the doctor has stopped being able to report a new accumulator at all.
func TestScanReportsUnknownIgnoredAccumulatorAsUndeclared(t *testing.T) {
	root := newScanRepo(t, "mystery-corpus/\n")
	// A shape no allow-list anticipates, large enough to matter.
	writeDirOfSize(t, filepath.Join(root, "mystery-corpus"), undeclaredAccumulatorBytes+1)

	report := ScanRetention([]string{root})
	if len(report.Undeclared) != 1 {
		t.Fatalf("want the unknown accumulator reported, got %d (%+v)", len(report.Undeclared), report.Undeclared)
	}
	if got := filepath.Base(report.Undeclared[0].Path); got != "mystery-corpus" {
		t.Fatalf("undeclared path = %q", got)
	}
	if report.Undeclared[0].ClassID != "" {
		t.Fatalf("an undeclared instance must carry no class, got %q", report.Undeclared[0].ClassID)
	}
}

// Tracked source is bounded by the code and must never be reported, or the check becomes noise
// and people stop reading it.
func TestScanIgnoresTrackedSourceDirectories(t *testing.T) {
	root := newScanRepo(t, "")
	writeDirOfSize(t, filepath.Join(root, "internal"), undeclaredAccumulatorBytes+1)

	report := ScanRetention([]string{root})
	if len(report.Undeclared) != 0 {
		t.Fatalf("tracked source must not be flagged: %+v", report.Undeclared)
	}
}

// Small ignored directories are ordinary (editor state, a stray log). Flagging them would bury
// the multi-gigabyte findings that actually matter.
func TestScanIgnoresSmallIgnoredDirectories(t *testing.T) {
	root := newScanRepo(t, "scratch/\n")
	writeDirOfSize(t, filepath.Join(root, "scratch"), 1024)

	report := ScanRetention([]string{root})
	if len(report.Undeclared) != 0 {
		t.Fatalf("small ignored dir must not be flagged: %+v", report.Undeclared)
	}
}

func TestScanFindsCachesInsideWorktrees(t *testing.T) {
	root := newScanRepo(t, "")
	writeDirOfSize(t, filepath.Join(root, ".claude", "worktrees", "task-a", "target"), 8192)

	report := ScanRetention([]string{root})
	// BOTH are legitimately reported: the container under agent-worktree, and the cache inside
	// it under worktree-build-cache. They overlap in bytes on purpose — the container total is
	// what makes 28 GB of un-retired worktrees visible — so the invariant that matters is that
	// only the REBUILDABLE instance counts toward reclaimable bytes. Double-counting there
	// would promise an operator space that reclaiming cannot actually deliver.
	byClass := map[string]RetentionInstance{}
	for _, item := range report.Declared {
		byClass[item.ClassID] = item
	}
	cache, ok := byClass["worktree-build-cache"]
	if !ok {
		t.Fatalf("want the worktree cache found, got %+v", report.Declared)
	}
	if _, ok := byClass["agent-worktree"]; !ok {
		t.Fatalf("want the container reported too, got %+v", report.Declared)
	}
	if report.ReclaimableBytes != cache.Bytes {
		t.Fatalf("reclaimable must count the cache ONLY, got %d want %d", report.ReclaimableBytes, cache.Bytes)
	}
}

// A report that cannot distinguish "clean" from "never looked" is the silent-green failure
// again, one level up.
func TestScanRecordsWhatItScanned(t *testing.T) {
	root := newScanRepo(t, "")
	report := ScanRetention([]string{root})
	if len(report.Scanned) != 1 || report.Scanned[0] != root {
		t.Fatalf("scanned = %+v, want [%s]", report.Scanned, root)
	}

	empty := ScanRetention([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	if len(empty.Scanned) != 0 {
		t.Fatalf("a missing root must not be reported as scanned: %+v", empty.Scanned)
	}
}

func TestScanDisclosesWhetherFixedPathCachesWereIncluded(t *testing.T) {
	repoOnly := ScanRetentionWithOptions(nil, RetentionScanOptions{})
	if repoOnly.FixedPathCachesIncluded {
		t.Fatal("repo-only scan reported fixed caches included")
	}

	withFixed := ScanRetentionWithOptions(nil, RetentionScanOptions{FixedPathCaches: true})
	if !withFixed.FixedPathCachesIncluded {
		t.Fatal("fixed-cache scan did not disclose its expanded scope")
	}
}

func TestScanDoesNotFollowSymlinks(t *testing.T) {
	root := newScanRepo(t, "target/\n")
	outside := t.TempDir()
	writeDirOfSize(t, filepath.Join(outside, "big"), 1<<20)
	if err := os.MkdirAll(filepath.Join(root, "target"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "big"), filepath.Join(root, "target", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	report := ScanRetention([]string{root})
	for _, item := range report.Declared {
		if item.Bytes >= 1<<20 {
			t.Fatalf("symlinked bytes were counted: %+v", item)
		}
	}
}

// Registry invariants. A class missing an owner or a retention rule is undeclared in
// everything but name — it would show up green while telling nobody what to do.
func TestRetentionClassesAreWellFormed(t *testing.T) {
	classes := RetentionClasses()
	if len(classes) == 0 {
		t.Fatal("registry must not be empty; an empty registry silently protects nothing")
	}
	seen := map[string]bool{}
	for _, class := range classes {
		if class.ID == "" || class.Owner == "" || class.Description == "" || class.Retention == "" {
			t.Fatalf("class is missing required fields: %+v", class)
		}
		if class.Mode != ReclaimAutomatic && class.Mode != ReclaimOperatorGated {
			t.Fatalf("class %q has invalid mode %q", class.ID, class.Mode)
		}
		if seen[class.ID] {
			t.Fatalf("duplicate class id %q", class.ID)
		}
		seen[class.ID] = true
	}
}

// An automatic reclaimer must have something to run. A class marked automatic with no command
// is a promise nothing keeps — which is precisely how the storage guard reported healthy while
// executing zero cleanups across 60 cycles.
func TestAutomaticClassesHaveAReclaimCommand(t *testing.T) {
	for _, class := range RetentionClasses() {
		if class.Mode == ReclaimAutomatic && class.ReclaimCommand == "" {
			t.Fatalf("class %q is automatic but has no reclaim command", class.ID)
		}
	}
}

func TestAutomaticClassesOwnStructuredReclaimArgv(t *testing.T) {
	for _, class := range RetentionClasses() {
		if class.Mode != ReclaimAutomatic {
			continue
		}
		data, err := json.Marshal(class)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`"reclaim_command_argv"`)) {
			t.Fatalf("automatic class %q has no structured argv owned by the registry: %s", class.ID, data)
		}
	}
}

// Classes with no mechanism must be surfaced rather than quietly omitted.
func TestClassesWithoutMechanismAreReported(t *testing.T) {
	without := RetentionClassesWithoutMechanism()
	if len(without) == 0 {
		t.Skip("no mechanism-less classes declared")
	}
	for _, class := range without {
		if class.ReclaimCommand != "" {
			t.Fatalf("class %q has a command and must not be listed", class.ID)
		}
		if class.Mode == ReclaimAutomatic {
			t.Fatalf("class %q cannot be automatic with no command", class.ID)
		}
	}
}

func TestRetentionClassByIDIsExactAndFailsClosed(t *testing.T) {
	if _, ok := RetentionClassByID("worktree-build-cache"); !ok {
		t.Fatal("known class must resolve")
	}
	if _, ok := RetentionClassByID("no-such-class"); ok {
		t.Fatal("unknown class must not resolve")
	}
}
