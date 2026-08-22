package sourceobs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sourceManifestDirectoryPath struct {
	relative string
	absolute string
}

// EnumerateSourceManifestDirectory returns regular .md files beneath root as
// absolute paths in lexical relative-path order. It never follows symlinks: a
// symlink anywhere beneath the root is an error, so escapes and cycles fail
// closed before manifest custody begins.
func EnumerateSourceManifestDirectory(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("source directory required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve source directory %s: %w", root, err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("stat source directory %s: %w", absoluteRoot, err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source directory %s is a symlink; symlinks are refused", absoluteRoot)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("source directory %s is not a directory", absoluteRoot)
	}

	candidates := make([]sourceManifestDirectoryPath, 0)
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
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source directory %s contains symlink %s; symlinks are refused", absoluteRoot, filepath.ToSlash(relative))
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
			return fmt.Errorf("duplicate normalized source path %s (%s and %s)", normalized, previous, filepath.ToSlash(relative))
		}
		file, err := os.Open(normalized)
		if err != nil {
			return fmt.Errorf("read markdown source %s: %w", normalized, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("read markdown source %s: %w", normalized, err)
		}
		seen[normalized] = filepath.ToSlash(relative)
		candidates = append(candidates, sourceManifestDirectoryPath{
			relative: filepath.ToSlash(relative),
			absolute: normalized,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("source directory %s contains no regular .md files", absoluteRoot)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].relative < candidates[j].relative
	})
	paths := make([]string, len(candidates))
	for i := range candidates {
		paths[i] = candidates[i].absolute
	}
	return paths, nil
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
