package sourceobs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sourceManifestFileSnapshot struct {
	path    string
	body    []byte
	modTime time.Time
	mode    os.FileMode
}

type sourceManifestDirectoryCandidate struct {
	relative string
	absolute string
}

type sourceManifestDirectoryBuildHooks struct {
	afterSnapshot func([]sourceManifestFileSnapshot) error
	putArtifact   sourceManifestArtifactPutter
}

// EnumerateSourceManifestDirectory returns regular .md files beneath root as
// absolute paths in lexical relative-path order. It snapshots each eligible
// file through a root-confined handle and never follows a symlink outside root.
func EnumerateSourceManifestDirectory(root string) ([]string, error) {
	_, snapshots, err := snapshotSourceManifestDirectory(root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(snapshots))
	for i := range snapshots {
		paths[i] = snapshots[i].path
	}
	return paths, nil
}

// BuildSourceManifestDirectory builds source-in-place items from one
// root-confined snapshot of every eligible markdown file.
func BuildSourceManifestDirectory(sourceFamily, root string, now time.Time) (SourceManifest, error) {
	if strings.TrimSpace(sourceFamily) == "" {
		return SourceManifest{}, fmt.Errorf("source family required")
	}
	absoluteRoot, snapshots, err := snapshotSourceManifestDirectory(root)
	if err != nil {
		return SourceManifest{}, err
	}
	revision := gitRevisionDirectory(absoluteRoot)
	return buildSourceManifestSnapshots(sourceFamily, snapshots, now, func(string) string { return revision })
}

// BuildOwnedSourceManifestDirectory captures the same single-read snapshots
// into the existing SourceManifest artifact custody contract.
func BuildOwnedSourceManifestDirectory(homePath, sourceFamily, root string, now time.Time) (SourceManifest, error) {
	return buildOwnedSourceManifestDirectory(homePath, sourceFamily, root, now, sourceManifestDirectoryBuildHooks{})
}

func buildOwnedSourceManifestDirectory(homePath, sourceFamily, root string, now time.Time, hooks sourceManifestDirectoryBuildHooks) (SourceManifest, error) {
	if strings.TrimSpace(sourceFamily) == "" {
		return SourceManifest{}, fmt.Errorf("source family required")
	}
	absoluteRoot, snapshots, err := snapshotSourceManifestDirectory(root)
	if err != nil {
		return SourceManifest{}, err
	}
	if hooks.afterSnapshot != nil {
		if err := hooks.afterSnapshot(snapshots); err != nil {
			return SourceManifest{}, fmt.Errorf("source directory snapshot hook: %w", err)
		}
	}
	revision := gitRevisionDirectory(absoluteRoot)
	manifest, err := buildSourceManifestSnapshots(sourceFamily, snapshots, now, func(string) string { return revision })
	if err != nil {
		return SourceManifest{}, err
	}
	return captureOwnedSourceManifest(homePath, manifest, snapshots, hooks.putArtifact)
}

func snapshotSourceManifestDirectory(root string) (string, []sourceManifestFileSnapshot, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil, fmt.Errorf("source directory required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("resolve source directory %s: %w", root, err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return "", nil, fmt.Errorf("stat source directory %s: %w", absoluteRoot, err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("source directory %s is a symlink; symlinks are refused", absoluteRoot)
	}
	if !rootInfo.IsDir() {
		return "", nil, fmt.Errorf("source directory %s is not a directory", absoluteRoot)
	}
	rootHandle, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return "", nil, fmt.Errorf("open source directory %s: %w", absoluteRoot, err)
	}
	defer rootHandle.Close()

	candidates := make([]sourceManifestDirectoryCandidate, 0)
	seen := make(map[string]string)
	err = filepath.WalkDir(absoluteRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("read source directory entry %s: %w", path, walkErr)
		}
		relative, err := filepath.Rel(absoluteRoot, path)
		if err != nil {
			return fmt.Errorf("resolve source directory entry %s: %w", path, err)
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source directory %s contains symlink %s; symlinks are refused", absoluteRoot, relative)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat source directory entry %s: %w", path, err)
		}
		if info.IsDir() || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		normalized := filepath.Clean(path)
		if previous, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate normalized source path %s (%s and %s)", normalized, previous, relative)
		}
		seen[normalized] = relative
		candidates = append(candidates, sourceManifestDirectoryCandidate{relative: relative, absolute: normalized})
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("source directory %s contains no regular .md files", absoluteRoot)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].relative < candidates[j].relative
	})

	snapshots := make([]sourceManifestFileSnapshot, 0, len(candidates))
	for _, candidate := range candidates {
		file, err := rootHandle.Open(filepath.FromSlash(candidate.relative))
		if err != nil {
			return "", nil, fmt.Errorf("read markdown source %s: %w", candidate.absolute, err)
		}
		pathInfo, lstatErr := os.Lstat(candidate.absolute)
		if lstatErr != nil {
			_ = file.Close()
			return "", nil, fmt.Errorf("stat markdown source %s: %w", candidate.absolute, lstatErr)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return "", nil, fmt.Errorf("stat markdown source %s: %w", candidate.absolute, statErr)
		}
		if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(pathInfo, openedInfo) {
			_ = file.Close()
			return "", nil, fmt.Errorf("markdown source %s changed to a symlink or different file during snapshot", candidate.absolute)
		}
		snapshot, readErr := readSourceManifestSnapshot(file, candidate.absolute, openedInfo)
		closeErr := file.Close()
		if readErr != nil {
			return "", nil, readErr
		}
		if closeErr != nil {
			return "", nil, fmt.Errorf("read markdown source %s: %w", candidate.absolute, closeErr)
		}
		snapshots = append(snapshots, snapshot)
	}
	return absoluteRoot, snapshots, nil
}

func snapshotSourceManifestPaths(paths []string) ([]sourceManifestFileSnapshot, error) {
	snapshots := make([]sourceManifestFileSnapshot, 0, len(paths))
	for _, path := range paths {
		snapshot, err := snapshotSourceManifestFile(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func snapshotSourceManifestFile(sourcePath string) (sourceManifestFileSnapshot, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return sourceManifestFileSnapshot{}, fmt.Errorf("source path required")
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("read source %s: %w", sourcePath, err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return sourceManifestFileSnapshot{}, fmt.Errorf("stat source %s: %w", sourcePath, statErr)
	}
	snapshot, readErr := readSourceManifestSnapshot(file, sourcePath, info)
	closeErr := file.Close()
	if readErr != nil {
		return sourceManifestFileSnapshot{}, readErr
	}
	if closeErr != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("read source %s: %w", sourcePath, closeErr)
	}
	return snapshot, nil
}

func readSourceManifestSnapshot(file *os.File, sourcePath string, before os.FileInfo) (sourceManifestFileSnapshot, error) {
	if !before.Mode().IsRegular() {
		return sourceManifestFileSnapshot{}, fmt.Errorf("source %s is not a regular file", sourcePath)
	}
	body, err := io.ReadAll(file)
	if err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("read source %s: %w", sourcePath, err)
	}
	after, err := file.Stat()
	if err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("stat source %s after read: %w", sourcePath, err)
	}
	if !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || after.Size() != int64(len(body)) {
		return sourceManifestFileSnapshot{}, fmt.Errorf("source %s changed during snapshot", sourcePath)
	}
	return sourceManifestFileSnapshot{path: sourcePath, body: body, modTime: after.ModTime(), mode: after.Mode()}, nil
}

func validateUniqueSourceManifestPaths(paths []string) error {
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			return fmt.Errorf("source path required")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("normalize source path %s: %w", path, err)
		}
		normalized := filepath.Clean(absolute)
		if previous, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate normalized source path %s (%s and %s)", normalized, previous, path)
		}
		seen[normalized] = path
	}
	return nil
}
