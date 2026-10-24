package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Direct cache tests deliberately clear the shared-cache opt-out. Their user
// cache roots must therefore be disposable too, including when run inside a
// canonical proof whose inherited GOCACHE points somewhere safe.
func isolateProofCacheTest(t *testing.T) {
	t.Helper()
	proofTestHome := isolatedTestUserHome(t)
	t.Setenv("HOME", proofTestHome)
	t.Setenv("USERPROFILE", proofTestHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(proofTestHome, "xdg-cache"))
	t.Setenv("LocalAppData", filepath.Join(proofTestHome, "local-cache"))
}

func TestProofCacheFixturesIgnoreUnavailableAmbientCache(t *testing.T) {
	ambientHome := isolatedTestUserHome(t)
	t.Setenv("HOME", ambientHome)
	t.Setenv("USERPROFILE", ambientHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(ambientHome, "xdg-cache"))
	t.Setenv("LocalAppData", filepath.Join(ambientHome, "local-cache"))

	// A regular file at the pool root makes an accidental ambient cache write
	// fail immediately on every supported UserCacheDir layout. This reproduces
	// the host-cache dependency without using or blocking a real operator path.
	const sentinel = "synthetic unavailable ambient cache; preserve me\n"
	var sentinels []string
	for _, root := range []string{
		filepath.Join(ambientHome, "Library", "Caches"),
		filepath.Join(ambientHome, "xdg-cache"),
		filepath.Join(ambientHome, "local-cache"),
	} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "compound-proof-gocache")
		if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
			t.Fatal(err)
		}
		sentinels = append(sentinels, path)
	}

	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"per-worktree", TestProofCheckGoCacheIsPerWorktree},
		{"stable", TestProofCheckGoCacheIsStableForOneWorktree},
		{"outside-worktree", TestProofCheckGoCacheLivesOutsideWorktree},
		{"shared-opt-out", TestProofCheckGoCacheHonorsSharedOptOut},
		{"ordinary-consumer", TestRunProofCheckStepInjectsWorktreeGoCache},
		{"bun-consumer", TestRunProofCheckBunToolsInjectsWorktreeGoCache},
		{"sealed-origin", TestProofCommandsPreserveCallerCacheAcrossSealedWorkspaces},
		{"sealed-source", TestCanonicalProofGatesReadSealedHeadDuringTransientCallerMutation},
	} {
		t.Run(tc.name, tc.run)
	}
	for _, path := range sentinels {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != sentinel {
			t.Errorf("ambient cache sentinel changed: %s: %q, %v", path, body, err)
		}
	}
}
