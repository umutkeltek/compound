package sourceobs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func TestEnumerateSourceManifestDirectoryIsRecursiveLexicalAndMarkdownOnly(t *testing.T) {
	root := t.TempDir()
	rootFile := writeSourceManifestDirectoryFixture(t, root, "00-root.md", "# Root\n")
	firstNested := writeSourceManifestDirectoryFixture(t, root, "a/10-first.md", "# First\n")
	secondNested := writeSourceManifestDirectoryFixture(t, root, "a/deeper/20-second.md", "# Second\n")
	lastFile := writeSourceManifestDirectoryFixture(t, root, "z-last.md", "# Last\n")
	writeSourceManifestDirectoryFixture(t, root, "a/deeper/ignored.txt", "not markdown\n")
	writeSourceManifestDirectoryFixture(t, root, "ignored.markdown", "not eligible\n")
	writeSourceManifestDirectoryFixture(t, root, "ignored.MD", "not eligible\n")

	got, err := EnumerateSourceManifestDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{rootFile, firstNested, secondNested, lastFile}
	if !slices.Equal(got, want) {
		t.Fatalf("enumerated paths = %q, want lexical relative-path order %q", got, want)
	}
	for _, path := range got {
		if !filepath.IsAbs(path) {
			t.Fatalf("enumerated path is not absolute: %q", path)
		}
	}
}

func TestEnumerateSourceManifestDirectoryRefusesSymlinkEscapesAndCycles(t *testing.T) {
	outside := writeSourceManifestDirectoryFixture(t, t.TempDir(), "outside.md", "# Outside\n")

	tests := []struct {
		name   string
		target func(root string) string
	}{
		{name: "escape", target: func(string) string { return outside }},
		{name: "cycle", target: func(root string) string { return root }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceManifestDirectoryFixture(t, root, "before.md", "# Before\n")
			if err := os.Symlink(tt.target(root), filepath.Join(root, "linked.md")); err != nil {
				t.Fatal(err)
			}
			paths, err := EnumerateSourceManifestDirectory(root)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("symlink enumeration error = %v, paths = %q", err, paths)
			}
			if len(paths) != 0 {
				t.Fatalf("symlink refusal returned partial paths: %q", paths)
			}
		})
	}
}

func TestEnumerateSourceManifestDirectoryRefusesUnreadableMarkdown(t *testing.T) {
	root := t.TempDir()
	path := writeSourceManifestDirectoryFixture(t, root, "unreadable.md", "# Private\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if file, err := os.Open(path); err == nil {
		_ = file.Close()
		t.Skip("current user can read mode-000 files")
	}

	paths, err := EnumerateSourceManifestDirectory(root)
	if err == nil || !strings.Contains(err.Error(), "read markdown source") {
		t.Fatalf("unreadable enumeration error = %v, paths = %q", err, paths)
	}
	if len(paths) != 0 {
		t.Fatalf("unreadable refusal returned partial paths: %q", paths)
	}
}

func TestEnumerateSourceManifestDirectoryRefusesInvalidAndEmptyRoots(t *testing.T) {
	parent := t.TempDir()
	nonDirectory := writeSourceManifestDirectoryFixture(t, parent, "single.md", "# Single\n")
	empty := filepath.Join(parent, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		root string
		want string
	}{
		{name: "missing", root: filepath.Join(parent, "missing"), want: "stat source directory"},
		{name: "non-directory", root: nonDirectory, want: "is not a directory"},
		{name: "empty", root: empty, want: "contains no regular .md files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, err := EnumerateSourceManifestDirectory(tt.root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("EnumerateSourceManifestDirectory(%q) error = %v, want %q", tt.root, err, tt.want)
			}
			if len(paths) != 0 {
				t.Fatalf("invalid root returned partial paths: %q", paths)
			}
		})
	}
}

func TestBuildSourceManifestRefusesDuplicateNormalizedPaths(t *testing.T) {
	root := t.TempDir()
	path := writeSourceManifestDirectoryFixture(t, root, "duplicate.md", "# Duplicate\n")
	alias := root + string(os.PathSeparator) + "." + string(os.PathSeparator) + "duplicate.md"

	_, err := BuildSourceManifest("duplicate_normalized_paths", []string{path, alias}, time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "duplicate normalized source path") {
		t.Fatalf("duplicate normalized path error = %v", err)
	}
}

func TestBuildOwnedSourceManifestDirectoryUsesOneRootConfinedSnapshot(t *testing.T) {
	root := t.TempDir()
	originalBody := []byte("# Original\n")
	sourcePath := writeSourceManifestDirectoryFixture(t, root, "source.md", string(originalBody))
	originalInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	outsidePath := writeSourceManifestDirectoryFixture(t, t.TempDir(), "outside.md", "# Replacement\n")
	if err := os.Chmod(outsidePath, 0o644); err != nil {
		t.Fatal(err)
	}

	homePath := t.TempDir()
	manifest, err := buildOwnedSourceManifestDirectory(homePath, "snapshot_once", root, time.Now(), sourceManifestDirectoryBuildHooks{
		afterSnapshot: func(snapshots []sourceManifestFileSnapshot) error {
			if len(snapshots) != 1 {
				return fmt.Errorf("snapshots = %d, want 1", len(snapshots))
			}
			if err := os.Remove(sourcePath); err != nil {
				return err
			}
			return os.Symlink(outsidePath, sourcePath)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(manifest.Items))
	}
	item := manifest.Items[0]
	if item.FileHash != hashBytes(originalBody) || item.BodyBytes != int64(len(originalBody)) {
		t.Fatalf("snapshot metadata changed after path replacement: %+v", item)
	}
	if item.FileMode != "0600" || item.SnapshotTime != originalInfo.ModTime().UTC().Format(time.RFC3339) {
		t.Fatalf("snapshot file metadata came from replacement: %+v", item)
	}
	store, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := store.Get(item.BodyRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(captured, originalBody) {
		t.Fatalf("captured body = %q, want original %q", captured, originalBody)
	}
}

func TestBuildOwnedSourceManifestDirectoryRollsBackOnlyNewArtifactsOnLaterFailure(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		t.Run(fmt.Sprintf("preexisting=%t", preexisting), func(t *testing.T) {
			root := t.TempDir()
			firstBody := []byte("# Shared\n")
			secondBody := []byte("# Second\n")
			if preexisting {
				secondBody = firstBody
			}
			writeSourceManifestDirectoryFixture(t, root, "first.md", string(firstBody))
			writeSourceManifestDirectoryFixture(t, root, "second.md", string(secondBody))

			homePath := t.TempDir()
			store, err := home.NewArtifactStore(homePath)
			if err != nil {
				t.Fatal(err)
			}
			preexistingRef := ""
			if preexisting {
				preexistingRef, err = store.Put(firstBody)
				if err != nil {
					t.Fatal(err)
				}
			}
			putCalls := 0
			_, err = buildOwnedSourceManifestDirectory(homePath, "atomic_custody", root, time.Now(), sourceManifestDirectoryBuildHooks{
				putArtifact: func(store *home.ArtifactStore, body []byte) (string, error) {
					putCalls++
					if putCalls == 2 {
						return "", errors.New("injected later artifact failure")
					}
					return store.Put(body)
				},
			})
			if err == nil || !strings.Contains(err.Error(), "injected later artifact failure") {
				t.Fatalf("later custody failure = %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(homePath, "artifacts", "sha256"))
			if err != nil {
				t.Fatal(err)
			}
			wantEntries := 0
			if preexisting {
				wantEntries = 1
			}
			if len(entries) != wantEntries {
				t.Fatalf("artifact entries after rollback = %d, want %d: %v", len(entries), wantEntries, entries)
			}
			if preexisting {
				body, err := store.Get(preexistingRef)
				if err != nil {
					t.Fatalf("preexisting artifact was removed: %v", err)
				}
				if !bytes.Equal(body, firstBody) {
					t.Fatalf("preexisting artifact changed: %q", body)
				}
			}
		})
	}
}

func TestBuildSourceManifestDirectoryResolvesGitRevisionOnce(t *testing.T) {
	root := t.TempDir()
	for i := range 64 {
		writeSourceManifestDirectoryFixture(t, root, fmt.Sprintf("nested/%03d.md", i), "# Page\n")
	}
	originalCommand := sourceManifestGitRevisionCommand
	calls := 0
	sourceManifestGitRevisionCommand = func(dir string) ([]byte, error) {
		calls++
		if filepath.Clean(dir) != filepath.Clean(root) {
			t.Fatalf("git revision dir = %q, want root %q", dir, root)
		}
		return []byte("abcdef12\n"), nil
	}
	t.Cleanup(func() { sourceManifestGitRevisionCommand = originalCommand })

	manifest, err := BuildSourceManifestDirectory("git_revision_once", root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("git revision subprocess calls = %d, want 1 for %d files", calls, len(manifest.Items))
	}
	for _, item := range manifest.Items {
		if item.GitRevision != "abcdef12" {
			t.Fatalf("item git revision = %q, want cached revision", item.GitRevision)
		}
	}
}

func writeSourceManifestDirectoryFixture(t *testing.T, root, relativePath, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(absolute)
}
