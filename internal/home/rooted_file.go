package home

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ReadRootedRegularFile reads an owned file only when every path component
// beneath home is stable and contains no symbolic links.
func ReadRootedRegularFile(homePath, path, kind string) ([]byte, error) {
	root, relativePath, displayPath, err := openRootedPath(homePath, path, kind)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	before, err := lstatRootedRegularPath(root, relativePath, displayPath, kind)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	after, err := lstatRootedRegularPath(root, relativePath, displayPath, kind)
	if err != nil {
		return nil, err
	}
	if !sameRootedPath(before, after) {
		return nil, fmt.Errorf("%s path must be stable beneath compound home: %s", kind, displayPath)
	}
	leaf := before[len(before)-1]
	if !opened.Mode().IsRegular() || !os.SameFile(leaf, opened) {
		return nil, fmt.Errorf("%s file must be a stable regular file: %s", kind, displayPath)
	}
	return io.ReadAll(file)
}

// WriteRootedNewFileDurable creates and syncs a new owned file without
// traversing symbolic links beneath home.
func WriteRootedNewFileDurable(homePath, path, kind string, data []byte, perm os.FileMode) error {
	root, relativePath, displayPath, err := openRootedPath(homePath, path, kind)
	if err != nil {
		return err
	}
	defer root.Close()

	parentPath := filepath.Dir(relativePath)
	beforeParents, err := ensureRootedDirectories(root, parentPath, displayPath, kind, 0o755)
	if err != nil {
		return err
	}
	if info, err := root.Lstat(relativePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s file must be a regular file: %s", kind, displayPath)
		}
		return fmt.Errorf("%s file already exists: %s", kind, displayPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	file, err := root.OpenFile(relativePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	cleanup := func(cause error) error {
		_ = file.Close()
		_ = root.Remove(relativePath)
		_ = syncRootedDirectory(root, parentPath, displayPath, kind)
		return cause
	}
	opened, err := file.Stat()
	if err != nil {
		return cleanup(err)
	}
	afterParents, err := lstatRootedDirectoryPath(root, parentPath, displayPath, kind, false)
	if err != nil {
		return cleanup(err)
	}
	if !sameRootedPath(beforeParents, afterParents) {
		return cleanup(fmt.Errorf("%s path must be stable beneath compound home: %s", kind, displayPath))
	}
	leaf, err := root.Lstat(relativePath)
	if err != nil {
		return cleanup(err)
	}
	if leaf.Mode()&os.ModeSymlink != 0 || !leaf.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(leaf, opened) {
		return cleanup(fmt.Errorf("%s file must be a stable regular file: %s", kind, displayPath))
	}
	if n, err := file.Write(data); err != nil {
		return cleanup(err)
	} else if n != len(data) {
		return cleanup(io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(relativePath)
		return err
	}
	return syncRootedDirectory(root, parentPath, displayPath, kind)
}

// RemoveRootedRegularFileDurable removes and directory-syncs an owned regular
// file only when no path component beneath home is a symbolic link.
func RemoveRootedRegularFileDurable(homePath, path, kind string) error {
	root, relativePath, displayPath, err := openRootedPath(homePath, path, kind)
	if err != nil {
		return err
	}
	defer root.Close()

	before, err := lstatRootedRegularPath(root, relativePath, displayPath, kind)
	if err != nil {
		return err
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	after, err := lstatRootedRegularPath(root, relativePath, displayPath, kind)
	if err != nil {
		_ = file.Close()
		return err
	}
	leaf := before[len(before)-1]
	if !sameRootedPath(before, after) || !opened.Mode().IsRegular() || !os.SameFile(leaf, opened) {
		_ = file.Close()
		return fmt.Errorf("%s file must be a stable regular file: %s", kind, displayPath)
	}
	if err := root.Remove(relativePath); err != nil {
		_ = file.Close()
		return err
	}
	closeErr := file.Close()
	syncErr := syncRootedDirectory(root, filepath.Dir(relativePath), displayPath, kind)
	if closeErr != nil {
		return closeErr
	}
	return syncErr
}

func openRootedPath(homePath, path, kind string) (*os.Root, string, string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "owned"
	}
	homePath, err := filepath.Abs(filepath.Clean(homePath))
	if err != nil {
		return nil, "", "", err
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, "", "", err
	}
	relativePath, err := filepath.Rel(homePath, path)
	if err != nil {
		return nil, "", "", fmt.Errorf("%s file path is not relative to compound home: %w", kind, err)
	}
	if relativePath == "." || !filepath.IsLocal(relativePath) {
		return nil, "", "", fmt.Errorf("%s file must remain beneath compound home: %s", kind, path)
	}
	root, err := os.OpenRoot(homePath)
	if err != nil {
		return nil, "", "", err
	}
	return root, relativePath, path, nil
}

func lstatRootedRegularPath(root *os.Root, relativePath, displayPath, kind string) ([]os.FileInfo, error) {
	components := strings.Split(relativePath, string(filepath.Separator))
	infos := make([]os.FileInfo, 0, len(components))
	for index := range components {
		componentPath := filepath.Join(components[:index+1]...)
		info, err := root.Lstat(componentPath)
		if err != nil {
			return nil, err
		}
		leaf := index == len(components)-1
		if info.Mode()&os.ModeSymlink != 0 {
			if leaf {
				return nil, fmt.Errorf("%s file must be a regular file: %s", kind, displayPath)
			}
			return nil, fmt.Errorf("%s path must not contain symlink beneath compound home: %s", kind, displayPath)
		}
		if leaf {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%s file must be a regular file: %s", kind, displayPath)
			}
		} else if !info.IsDir() {
			return nil, fmt.Errorf("%s path ancestor must be a directory beneath compound home: %s", kind, displayPath)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func ensureRootedDirectories(root *os.Root, relativePath, displayPath, kind string, perm os.FileMode) ([]os.FileInfo, error) {
	if _, err := lstatRootedDirectoryPath(root, relativePath, displayPath, kind, true); err != nil {
		return nil, err
	}
	if relativePath != "." {
		if err := root.MkdirAll(relativePath, perm); err != nil {
			return nil, err
		}
	}
	return lstatRootedDirectoryPath(root, relativePath, displayPath, kind, false)
}

func lstatRootedDirectoryPath(root *os.Root, relativePath, displayPath, kind string, allowMissing bool) ([]os.FileInfo, error) {
	if relativePath == "." {
		info, err := root.Stat(".")
		if err != nil {
			return nil, err
		}
		return []os.FileInfo{info}, nil
	}
	components := strings.Split(relativePath, string(filepath.Separator))
	infos := make([]os.FileInfo, 0, len(components))
	for index := range components {
		componentPath := filepath.Join(components[:index+1]...)
		info, err := root.Lstat(componentPath)
		if err != nil {
			if allowMissing && errors.Is(err, os.ErrNotExist) {
				return infos, nil
			}
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s path must not contain symlink beneath compound home: %s", kind, displayPath)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s path ancestor must be a directory beneath compound home: %s", kind, displayPath)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func sameRootedPath(before, after []os.FileInfo) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if !os.SameFile(before[index], after[index]) {
			return false
		}
	}
	return true
}

func syncRootedDirectory(root *os.Root, relativePath, displayPath, kind string) error {
	before, err := lstatRootedDirectoryPath(root, relativePath, displayPath, kind, false)
	if err != nil {
		return err
	}
	dir, err := root.Open(relativePath)
	if err != nil {
		return err
	}
	opened, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return err
	}
	after, err := lstatRootedDirectoryPath(root, relativePath, displayPath, kind, false)
	if err != nil {
		_ = dir.Close()
		return err
	}
	if !sameRootedPath(before, after) || !opened.IsDir() || !os.SameFile(before[len(before)-1], opened) {
		_ = dir.Close()
		return fmt.Errorf("%s path must be stable beneath compound home: %s", kind, displayPath)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}
