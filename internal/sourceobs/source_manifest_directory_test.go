package sourceobs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
