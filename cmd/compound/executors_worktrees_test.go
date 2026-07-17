package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/authority"
)

// W6: the CLI renders a versioned JSON envelope whose rows all carry
// class/reason/next_action, and text mode surfaces the plain-language
// next-action strings. Runs the real Git seam over a fixture with one
// landed_clean and one stalled_unlanded worktree so both action strings appear.
func TestExecutorsWorktreesCLIRendersJSONAndText(t *testing.T) {
	repo := newExecutorsWorktreesCLIFixture(t)

	previous := worktreeInventoryCLILiveness
	worktreeInventoryCLILiveness = cliUnusedWorktreeLiveness{}
	t.Cleanup(func() { worktreeInventoryCLILiveness = previous })

	// Runtime-relative future now so freshly created fixture files are far older
	// than the activity window (no live-via-activity, no fixed-date time bomb).
	now := time.Now().Add(48 * time.Hour)

	var stdout, stderr bytes.Buffer
	code := runExecutorsWorktrees(context.Background(), []string{"--json", "--repo", repo}, &stdout, &stderr, now)
	if code != 0 {
		t.Fatalf("json code=%d stderr=%s", code, stderr.String())
	}
	var inv authority.WorktreeInventory
	if err := json.Unmarshal(stdout.Bytes(), &inv); err != nil {
		t.Fatalf("decode json: %v\n%s", err, stdout.String())
	}
	if inv.SchemaVersion != authority.WorktreeInventorySchemaVersion {
		t.Fatalf("schema_version = %q", inv.SchemaVersion)
	}
	if inv.Repo == "" || inv.GeneratedAt == "" || inv.AuthorityRef == "" {
		t.Fatalf("envelope header incomplete: %+v", inv)
	}
	if len(inv.Rows) == 0 {
		t.Fatal("no rows rendered")
	}
	seen := map[string]bool{}
	for _, row := range inv.Rows {
		if row.Class == "" || row.Reason == "" || row.NextAction == "" {
			t.Fatalf("row missing class/reason/next_action: %+v", row)
		}
		seen[row.Class] = true
	}
	for _, want := range []string{authority.WorktreeClassMain, authority.WorktreeClassLandedClean, authority.WorktreeClassStalledUnlanded} {
		if !seen[want] {
			t.Fatalf("expected class %q among rows; got classes %v", want, seen)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = runExecutorsWorktrees(context.Background(), []string{"--repo", repo}, &stdout, &stderr, now)
	if code != 0 {
		t.Fatalf("text code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{
		"unlanded work — check lane liveness before salvage or keep",
		"retire via compound authority worktree-retire",
		"leave alone — primary checkout",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text mode missing next-action string %q\n%s", want, text)
		}
	}
}

func TestExecutorsWorktreesCLIRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runExecutorsWorktrees(context.Background(), []string{"--bogus"}, &stdout, &stderr, time.Now())
	if code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func newExecutorsWorktreesCLIFixture(t *testing.T) string {
	t.Helper()
	repo, _, _ := initLandingCLIRepo(t)
	parent := filepath.Dir(repo)

	// landed_clean: a branch at main, clean.
	runLandingCLIGit(t, repo, "branch", "lane-landed", "main")
	landedWT := filepath.Join(parent, "wt-landed")
	runLandingCLIGit(t, repo, "worktree", "add", landedWT, "lane-landed")

	// stalled_unlanded: the candidate branch (one commit ahead of main) plus a
	// dirty file.
	stalledWT := filepath.Join(parent, "wt-stalled")
	runLandingCLIGit(t, repo, "worktree", "add", stalledWT, "candidate")
	if err := os.WriteFile(filepath.Join(stalledWT, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}
