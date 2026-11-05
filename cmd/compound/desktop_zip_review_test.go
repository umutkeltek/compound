package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopZipSecondOpinionKeepsSourceAndRecentEvidenceWithTruthfulManifest(t *testing.T) {
	t.Parallel()
	script, source, desktop := desktopReviewFixture(t)
	files := map[string]string{
		"main.go":                             "package sample\n",
		"docs/design.md":                      "current public design\n",
		"docs_private/architecture/design.md": "current private design\n",
		"docs_private/plan/product-loop/artifacts/alpha/old/report.json": `{"outcome":"succeeded"}`,
		"docs_private/plan/product-loop/artifacts/alpha/new/report.json": `{"outcome":"failed","details":"unresolved"}`,
		"docs_private/plan/product-loop/artifacts/alpha/new/image.png":   "image bytes",
		"docs_private/plan/product-loop/artifacts/beta/new/large.json":   strings.Repeat("x", 2*1024*1024),
		"docs_private/custom/keep.md":                                    "explicit evidence\n",
		"docs_private/custom/drop.md":                                    "explicit but filtered\n",
		"docs_private/archive/stale.md":                                  "old copy\n",
		"docs_private/worktrees/duplicate/main.go":                       "duplicate checkout\n",
		"home/duplicate/main.go":                                         "duplicate home checkout\n",
		"memory/raw/corpus.json":                                         "raw corpus\n",
	}
	for name, body := range files {
		desktopReviewWrite(t, source, name, body)
	}
	if err := os.Symlink("docs", filepath.Join(source, "docs-link")); err != nil {
		t.Fatal(err)
	}
	desktopReviewGit(t, source, "add", "main.go", "docs/design.md", "docs-link", ".gitignore")
	desktopReviewGit(t, source, "-c", "user.name=Synthetic Reviewer", "-c", "user.email=reviewer@example.invalid", "commit", "-qm", "fixture")
	desktopReviewWrite(t, source, "local.go", "uncommitted local source\n")
	old := filepath.Join(source, "docs_private/plan/product-loop/artifacts/alpha/old")
	then := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "--repo", source, "--desktop", desktop,
		"--name", "snapshot", "--zip", "snapshot.zip", "--second-opinion", "--review-data-mb", "1",
		"--review-evidence", "docs_private/custom", "--exclude", "/docs_private/custom/drop.md")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second-opinion fixture: %v\n%s", err, out)
	}
	stage := filepath.Join(desktop, "snapshot")
	for _, name := range []string{"main.go", "local.go", "docs/design.md", "docs-link", "docs_private/architecture/design.md",
		"docs_private/custom/keep.md", "docs_private/plan/product-loop/artifacts/alpha/new/report.json",
		"REVIEW-START-HERE.md", "RECENT-COMMITS.txt", "AUDIT-NOTES.md"} {
		if _, err := os.Lstat(filepath.Join(stage, name)); err != nil {
			t.Fatalf("missing relevant file %s: %v", name, err)
		}
	}
	for _, name := range []string{".git", "home", "memory", "docs_private/archive", "docs_private/worktrees",
		"docs_private/custom/drop.md", "docs_private/plan/product-loop/artifacts/alpha/old",
		"docs_private/plan/product-loop/artifacts/alpha/new/image.png", "docs_private/plan/product-loop/artifacts/beta"} {
		if _, err := os.Lstat(filepath.Join(stage, name)); !os.IsNotExist(err) {
			t.Fatalf("irrelevant/over-budget file %s survived: %v", name, err)
		}
	}
	var manifest struct {
		SelectedDataBytes int64 `json:"selected_data_bytes"`
		Files             []struct{ Path, SHA256, Kind string }
		Omissions         []struct{ Path, Reason string }
	}
	body, err := os.ReadFile(filepath.Join(stage, "REVIEW-MANIFEST.json"))
	if err != nil || json.Unmarshal(body, &manifest) != nil {
		t.Fatalf("unreadable manifest: %v\n%s", err, body)
	}
	if manifest.SelectedDataBytes > 1024*1024 || manifest.SelectedDataBytes == 0 {
		t.Fatalf("invalid data budget accounting: %d", manifest.SelectedDataBytes)
	}
	seen := map[string]bool{}
	for _, entry := range manifest.Files {
		seen[entry.Path] = true
		var data []byte
		if entry.Kind == "symlink target" {
			link, err := os.Readlink(filepath.Join(stage, entry.Path))
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(link)
		} else {
			var err error
			data, err = os.ReadFile(filepath.Join(stage, entry.Path))
			if err != nil {
				t.Fatal(err)
			}
		}
		digest := sha256.Sum256(data)
		if entry.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("manifest digest wrong for %s", entry.Path)
		}
	}
	if !seen["docs-link"] || !seen["local.go"] {
		t.Fatal("manifest missed source or directory symlink")
	}
	omissions := map[string]string{}
	for _, omitted := range manifest.Omissions {
		omissions[omitted.Path] = omitted.Reason
	}
	if !strings.Contains(omissions["docs_private/custom/drop.md"], "filtered") ||
		!strings.Contains(omissions["docs_private/plan/product-loop/artifacts/beta/new"], "whole text group") {
		t.Fatalf("manifest failed to disclose omissions: %v", omissions)
	}
	got, _ := os.ReadFile(filepath.Join(stage, "docs_private/plan/product-loop/artifacts/alpha/new/report.json"))
	if string(got) != files["docs_private/plan/product-loop/artifacts/alpha/new/report.json"] {
		t.Fatal("failed evidence was truncated or altered")
	}
}

func TestDesktopZipSecondOpinionRefusalPreservesExistingSnapshot(t *testing.T) {
	t.Parallel()
	script, source, desktop := desktopReviewFixture(t)
	desktopReviewWrite(t, source, "docs_private/explicit/large.json", strings.Repeat("x", 2*1024*1024))
	desktopReviewWrite(t, desktop, "snapshot/keep.txt", "existing stage")
	desktopReviewWrite(t, desktop, "snapshot.zip", "existing zip")
	for _, flags := range [][]string{
		{"--second-opinion", "--review-data-mb", "1", "--review-evidence", "docs_private/explicit"},
		{"--second-opinion", "--review-evidence", "../outside"},
		{"--second-opinion", "--upload-safe"},
		{"--second-opinion", "--include-media"},
		{"--review-evidence", "docs_private/explicit"},
	} {
		args := append([]string{script, "--repo", source, "--desktop", desktop, "--name", "snapshot", "--zip", "snapshot.zip"}, flags...)
		if out, err := exec.Command("bash", args...).CombinedOutput(); err == nil {
			t.Fatalf("invalid request accepted: %v\n%s", flags, out)
		}
		for name, expected := range map[string]string{"snapshot/keep.txt": "existing stage", "snapshot.zip": "existing zip"} {
			got, err := os.ReadFile(filepath.Join(desktop, name))
			if err != nil || string(got) != expected {
				t.Fatalf("refusal modified %s: %v", name, err)
			}
		}
	}
}

func TestDesktopZipSecondOpinionRefusesEvidenceGrowthDuringCopy(t *testing.T) {
	t.Parallel()
	script, source, _ := desktopReviewFixture(t)
	helper := filepath.Join(filepath.Dir(script), "desktop_zip_review.py")
	selection, stage := t.TempDir(), t.TempDir()
	desktopReviewWrite(t, source, "docs_private/architecture/proof.json", `{"generation":1}`)
	if out, err := exec.Command("python3", helper, "select", "--repo", source, "--out", selection, "--data-mb", "1").CombinedOutput(); err != nil {
		t.Fatalf("selection: %v\n%s", err, out)
	}
	// A writer can grow a selected body after selection, before rsync captures it.
	// The final manifest must meter copied bytes, not trust the earlier stat size.
	desktopReviewWrite(t, stage, "docs_private/architecture/proof.json", strings.Repeat("x", 2*1024*1024))
	if out, err := exec.Command("python3", helper, "manifest", "--stage", stage, "--selection", filepath.Join(selection, "selection.json")).CombinedOutput(); err == nil || !strings.Contains(string(out), "grew during copying") {
		t.Fatalf("growing evidence was not refused: %v\n%s", err, out)
	}
}

func desktopReviewFixture(t *testing.T) (string, string, string) {
	t.Helper()
	for _, command := range []string{"bash", "git", "rsync", "zip", "python3"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s required", command)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "desktop-compound-zip.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source, desktop := t.TempDir(), t.TempDir()
	desktopReviewGit(t, source, "init", "-q")
	desktopReviewWrite(t, source, ".gitignore", "docs_private/\nmemory/\nhome/\n")
	return script, source, desktop
}

func desktopReviewWrite(t *testing.T, root, name, body string) {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func desktopReviewGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("fixture git %v: %v\n%s", args, err, out)
	}
}
