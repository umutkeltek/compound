package home

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var syncRootedDirectoryFile = func(_ string, file *os.File) error { return file.Sync() }

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

// RootedDirectoryExists reports whether an owned directory exists only when
// every path component beneath home is stable, directory-backed, and contains
// no symbolic links. A genuinely missing path component reports false.
func RootedDirectoryExists(homePath, path, kind string) (bool, error) {
	root, relativePath, displayPath, err := openRootedPath(homePath, path, kind)
	if err != nil {
		return false, err
	}
	defer root.Close()
	return rootedDirectoryExists(root, relativePath, displayPath, kind)
}

// EnsureRootedDirectoryDurable creates and syncs an owned directory hierarchy
// without traversing symbolic links or accepting an ambiguous EEXIST race.
func EnsureRootedDirectoryDurable(homePath, path, kind string, perm os.FileMode) error {
	root, relativePath, displayPath, err := openRootedPath(homePath, path, kind)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := ensureRootedDirectories(root, relativePath, displayPath, kind, perm); err != nil {
		return err
	}
	exists, err := rootedDirectoryExists(root, relativePath, displayPath, kind)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s directory was not durably created beneath compound home: %s", kind, displayPath)
	}
	return nil
}

func rootedDirectoryExists(root *os.Root, relativePath, displayPath, kind string) (bool, error) {
	before, err := lstatRootedDirectoryPath(root, relativePath, displayPath, kind, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	directory, err := root.Open(relativePath)
	if err != nil {
		return false, err
	}
	opened, err := directory.Stat()
	if err != nil {
		_ = directory.Close()
		return false, err
	}
	after, err := lstatRootedDirectoryPath(root, relativePath, displayPath, kind, false)
	if err != nil {
		_ = directory.Close()
		return false, err
	}
	closeErr := directory.Close()
	if closeErr != nil {
		return false, closeErr
	}
	leaf := before[len(before)-1]
	if !sameRootedPath(before, after) || !opened.IsDir() || !os.SameFile(leaf, opened) {
		return false, fmt.Errorf("%s directory must be stable beneath compound home: %s", kind, displayPath)
	}
	return true, nil
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
	return RemoveRootedRegularFileDurableValidated(homePath, path, kind, nil)
}

// removeRootedBeforeUnlink is a test seam invoked after quarantine validation
// and immediately before the final unlink, so falsifiers can prove the unlink
// is bound to the validated inode rather than to a predictable pathname.
var removeRootedBeforeUnlink func(quarantinePath string) error

// RemoveRootedRegularFileDurableValidated removes an owned regular file only
// when validator accepts the exact content and inode moved into quarantine.
// The final unlink re-verifies the quarantined inode identity: a foreign file
// swapped onto the quarantine pathname after validation is never deleted.
func RemoveRootedRegularFileDurableValidated(homePath, path, kind string, validator func([]byte) error) error {
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
	data, err := io.ReadAll(file)
	if err != nil {
		_ = file.Close()
		return err
	}
	if validator != nil {
		if err := validator(data); err != nil {
			_ = file.Close()
			return err
		}
	}

	parentPath := filepath.Dir(relativePath)
	parents, err := lstatRootedDirectoryPath(root, parentPath, displayPath, kind, false)
	if err != nil {
		_ = file.Close()
		return err
	}
	if parentPath != "." && !sameRootedPath(before[:len(before)-1], parents) {
		_ = file.Close()
		return fmt.Errorf("%s path must be stable beneath compound home: %s", kind, displayPath)
	}
	quarantinePath := relativePath + ".remove-pending"
	if _, err := root.Lstat(quarantinePath); err == nil {
		_ = file.Close()
		return fmt.Errorf("%s removal quarantine already exists: %s", kind, displayPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = file.Close()
		return err
	}
	if err := root.Rename(relativePath, quarantinePath); err != nil {
		_ = file.Close()
		return err
	}
	restore := func(cause error) error {
		_ = file.Close()
		if _, err := root.Lstat(relativePath); err == nil {
			return fmt.Errorf("%w; %s replacement retained at quarantine because original path is occupied", cause, kind)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w; inspect %s original path before quarantine restore: %v", cause, kind, err)
		}
		if err := root.Rename(quarantinePath, relativePath); err != nil {
			return fmt.Errorf("%w; restore %s quarantine: %v", cause, kind, err)
		}
		if err := syncRootedDirectory(root, parentPath, displayPath, kind); err != nil {
			return fmt.Errorf("%w; sync restored %s quarantine: %v", cause, kind, err)
		}
		return cause
	}

	quarantined, err := root.Lstat(quarantinePath)
	if err != nil {
		return restore(err)
	}
	quarantineFile, err := root.Open(quarantinePath)
	if err != nil {
		return restore(err)
	}
	quarantineOpened, err := quarantineFile.Stat()
	if err != nil {
		_ = quarantineFile.Close()
		return restore(err)
	}
	quarantineData, err := io.ReadAll(quarantineFile)
	if err != nil {
		_ = quarantineFile.Close()
		return restore(err)
	}
	if err := quarantineFile.Close(); err != nil {
		return restore(err)
	}
	if quarantined.Mode()&os.ModeSymlink != 0 || !quarantined.Mode().IsRegular() ||
		!quarantineOpened.Mode().IsRegular() || !os.SameFile(opened, quarantineOpened) || !bytes.Equal(data, quarantineData) {
		return restore(fmt.Errorf("%s file changed after validation: %s", kind, displayPath))
	}
	if err := file.Close(); err != nil {
		return restore(err)
	}
	if removeRootedBeforeUnlink != nil {
		if err := removeRootedBeforeUnlink(displayPath + ".remove-pending"); err != nil {
			return restore(err)
		}
	}
	final, err := root.Lstat(quarantinePath)
	if err != nil {
		return restore(err)
	}
	if final.Mode()&os.ModeSymlink != 0 || !final.Mode().IsRegular() || !os.SameFile(quarantined, final) {
		return restore(fmt.Errorf("%s file changed after validation: %s", kind, displayPath))
	}
	if err := root.Remove(quarantinePath); err != nil {
		return restore(err)
	}
	return syncRootedDirectory(root, parentPath, displayPath, kind)
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
	existing, err := lstatRootedDirectoryPath(root, relativePath, displayPath, kind, true)
	if err != nil {
		return nil, err
	}
	if relativePath == "." {
		return existing, nil
	}
	components := strings.Split(relativePath, string(filepath.Separator))
	for index := len(existing); index < len(components); index++ {
		componentPath := filepath.Join(components[:index+1]...)
		created := false
		if err := root.Mkdir(componentPath, perm); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		} else {
			created = true
		}
		parentPath := filepath.Dir(componentPath)
		if err := syncRootedDirectory(root, parentPath, displayPath, kind); err != nil {
			if created {
				_ = root.Remove(componentPath)
				_ = syncRootedDirectory(root, parentPath, displayPath, kind)
			}
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
	syncErr := syncRootedDirectoryFile(relativePath, dir)
	closeErr := dir.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}
