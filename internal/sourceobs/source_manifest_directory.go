package sourceobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/home"
)

type sourceManifestFileSnapshot struct {
	path      string
	spoolPath string
	fileHash  string
	bodyBytes int64
	modTime   time.Time
	mode      os.FileMode
}

type sourceManifestSnapshotBatch struct {
	refusedSymlinks int
	deferred        int
	files           []sourceManifestFileSnapshot
	spoolDir        string
}

func (b *sourceManifestSnapshotBatch) Close() error {
	if b == nil || b.spoolDir == "" {
		return nil
	}
	err := os.RemoveAll(b.spoolDir)
	b.spoolDir = ""
	b.files = nil
	return err
}

type sourceManifestDirectoryCandidate struct {
	relative string
	absolute string
	info     os.FileInfo
}

type sourceManifestDirectoryBuildHooks struct {
	afterSnapshot func([]sourceManifestFileSnapshot) error
	putArtifact   sourceManifestArtifactPutter
	record        func(string, SourceManifest) (SourceManifestReport, error)
}

// EnumerateSourceManifestDirectory returns regular .md files beneath root as
// absolute paths in lexical relative-path order. It validates readability by
// streaming each file into a private temporary spool and retains no source body.
func EnumerateSourceManifestDirectory(root string) ([]string, error) {
	_, batch, err := snapshotSourceManifestDirectory(root)
	if err != nil {
		return nil, err
	}
	defer batch.Close()
	paths := make([]string, len(batch.files))
	for i := range batch.files {
		paths[i] = batch.files[i].path
	}
	return paths, nil
}

// BuildSourceManifestDirectory builds source-in-place items from one
// root-confined snapshot of every eligible markdown file.
func BuildSourceManifestDirectory(sourceFamily, root string, now time.Time) (SourceManifest, error) {
	if strings.TrimSpace(sourceFamily) == "" {
		return SourceManifest{}, fmt.Errorf("source family required")
	}
	absoluteRoot, batch, err := snapshotSourceManifestDirectory(root)
	if err != nil {
		return SourceManifest{}, err
	}
	defer batch.Close()
	revision := gitRevisionDirectory(absoluteRoot)
	return buildSourceManifestSnapshots(sourceFamily, batch.files, now, func(string) string { return revision })
}

// CreateSourceManifestDirectory owns the directory capture transaction used by
// the CLI. Authority never references partial custody; a failure after artifact
// publication retains safe content-addressed orphans for grace-period GC.
func CreateSourceManifestDirectory(homePath, sourceFamily, root string, sourceInPlace bool, now time.Time) (SourceManifestReport, SourceManifest, error) {
	return createSourceManifestDirectory(homePath, sourceFamily, root, sourceInPlace, now, sourceManifestDirectoryBuildHooks{})
}

func createSourceManifestDirectory(homePath, sourceFamily, root string, sourceInPlace bool, now time.Time, hooks sourceManifestDirectoryBuildHooks) (SourceManifestReport, SourceManifest, error) {
	if strings.TrimSpace(sourceFamily) == "" {
		return SourceManifestReport{}, SourceManifest{}, fmt.Errorf("source family required")
	}
	absoluteRoot, batch, err := snapshotSourceManifestDirectory(root)
	if err != nil {
		return SourceManifestReport{}, SourceManifest{}, err
	}
	defer batch.Close()
	if hooks.afterSnapshot != nil {
		if err := hooks.afterSnapshot(batch.files); err != nil {
			return SourceManifestReport{}, SourceManifest{}, fmt.Errorf("source directory snapshot hook: %w", err)
		}
	}
	revision := gitRevisionDirectory(absoluteRoot)
	manifest, err := buildSourceManifestSnapshots(sourceFamily, batch.files, now, func(string) string { return revision })
	if err != nil {
		return SourceManifestReport{}, SourceManifest{}, err
	}
	recorder := hooks.record
	if recorder == nil {
		recorder = recordSourceManifestUnlocked
	}

	var report SourceManifestReport
	err = withSourceManifestAuthorityLock(homePath, func() error {
		if sourceInPlace {
			var recordErr error
			report, recordErr = recorder(homePath, manifest)
			return recordErr
		}
		owned, captureErr := captureOwnedSourceManifest(homePath, manifest, batch.files, hooks.putArtifact)
		if captureErr != nil {
			return captureErr
		}
		manifest = owned
		var recordErr error
		report, recordErr = recorder(homePath, manifest)
		if recordErr != nil {
			return fmt.Errorf("%w; content-addressed custody retained as unreferenced orphan evidence", recordErr)
		}
		return nil
	})
	if err != nil {
		return SourceManifestReport{}, SourceManifest{}, err
	}
	return report, manifest, nil
}

func snapshotSourceManifestDirectory(root string) (string, *sourceManifestSnapshotBatch, error) {
	return snapshotSourceManifestDirectoryFormat(context.Background(), root, ".md", false, nil, false)
}

func snapshotSourceManifestDirectoryFormat(ctx context.Context, root, extension string, allowEmpty bool, selectFile func(string, os.FileInfo) bool, continueAfterRefusedSymlink bool) (string, *sourceManifestSnapshotBatch, error) {
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

	refusedSymlinks := 0
	candidates := make([]sourceManifestDirectoryCandidate, 0)
	seen := make(map[string]string)
	err = filepath.WalkDir(absoluteRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			if filepath.Ext(entry.Name()) == extension {
				if continueAfterRefusedSymlink {
					refusedSymlinks++
					return nil
				}
				return fmt.Errorf("source directory %s contains markdown symlink %s; symlinks are refused", absoluteRoot, relative)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat source directory entry %s: %w", path, err)
		}
		if info.IsDir() || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != extension {
			return nil
		}
		normalized := filepath.Clean(path)
		if previous, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate normalized source path %s (%s and %s)", normalized, previous, relative)
		}
		seen[normalized] = relative
		candidates = append(candidates, sourceManifestDirectoryCandidate{relative: relative, absolute: normalized, info: info})
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if len(candidates) == 0 && !allowEmpty {
		return "", nil, fmt.Errorf("source directory %s contains no regular .md files", absoluteRoot)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if selectFile != nil && !candidates[i].info.ModTime().Equal(candidates[j].info.ModTime()) {
			return candidates[i].info.ModTime().After(candidates[j].info.ModTime())
		}
		return candidates[i].relative < candidates[j].relative
	})

	batch, err := newSourceManifestSnapshotBatch(len(candidates))
	if err != nil {
		return "", nil, err
	}
	batch.refusedSymlinks = refusedSymlinks
	for _, candidate := range candidates {
		if selectFile != nil && !selectFile(candidate.relative, candidate.info) {
			continue
		}
		if err := ctx.Err(); err != nil {
			batch.Close()
			return "", nil, err
		}
		file, err := rootHandle.Open(filepath.FromSlash(candidate.relative))
		if err != nil {
			batch.Close()
			return "", nil, fmt.Errorf("read markdown source %s: %w", candidate.absolute, err)
		}
		pathInfo, lstatErr := os.Lstat(candidate.absolute)
		if lstatErr != nil {
			_ = file.Close()
			batch.Close()
			return "", nil, fmt.Errorf("stat markdown source %s: %w", candidate.absolute, lstatErr)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			batch.Close()
			return "", nil, fmt.Errorf("stat markdown source %s: %w", candidate.absolute, statErr)
		}
		if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(pathInfo, openedInfo) {
			_ = file.Close()
			batch.Close()
			return "", nil, fmt.Errorf("markdown source %s changed to a symlink or different file during snapshot", candidate.absolute)
		}
		snapshot, spoolErr := spoolSourceManifestSnapshotContext(ctx, file, candidate.absolute, openedInfo, batch.spoolDir)
		closeErr := file.Close()
		if spoolErr != nil {
			if selectFile != nil && ctx.Err() == nil {
				batch.deferred++
				continue
			}
			batch.Close()
			return "", nil, spoolErr
		}
		if closeErr != nil {
			batch.Close()
			return "", nil, fmt.Errorf("read markdown source %s: %w", candidate.absolute, closeErr)
		}
		batch.files = append(batch.files, snapshot)
	}
	return absoluteRoot, batch, nil
}

func snapshotSourceManifestPaths(paths []string) (*sourceManifestSnapshotBatch, error) {
	batch, err := newSourceManifestSnapshotBatch(len(paths))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		snapshot, err := snapshotSourceManifestFile(path, batch.spoolDir)
		if err != nil {
			batch.Close()
			return nil, err
		}
		batch.files = append(batch.files, snapshot)
	}
	return batch, nil
}

func newSourceManifestSnapshotBatch(capacity int) (*sourceManifestSnapshotBatch, error) {
	spoolDir, err := os.MkdirTemp("", ".compound-source-manifest-*")
	if err != nil {
		return nil, fmt.Errorf("create source manifest snapshot spool: %w", err)
	}
	if err := os.Chmod(spoolDir, 0o700); err != nil {
		os.RemoveAll(spoolDir)
		return nil, fmt.Errorf("secure source manifest snapshot spool: %w", err)
	}
	return &sourceManifestSnapshotBatch{files: make([]sourceManifestFileSnapshot, 0, capacity), spoolDir: spoolDir}, nil
}

func snapshotSourceManifestFile(sourcePath, spoolDir string) (sourceManifestFileSnapshot, error) {
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
	snapshot, spoolErr := spoolSourceManifestSnapshot(file, sourcePath, info, spoolDir)
	closeErr := file.Close()
	if spoolErr != nil {
		return sourceManifestFileSnapshot{}, spoolErr
	}
	if closeErr != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("read source %s: %w", sourcePath, closeErr)
	}
	return snapshot, nil
}

func spoolSourceManifestSnapshot(file *os.File, sourcePath string, before os.FileInfo, spoolDir string) (sourceManifestFileSnapshot, error) {
	return spoolSourceManifestSnapshotContext(context.Background(), file, sourcePath, before, spoolDir)
}

type sourceContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r sourceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func spoolSourceManifestSnapshotContext(ctx context.Context, file *os.File, sourcePath string, before os.FileInfo, spoolDir string) (sourceManifestFileSnapshot, error) {
	if !before.Mode().IsRegular() {
		return sourceManifestFileSnapshot{}, fmt.Errorf("source %s is not a regular file", sourcePath)
	}
	spool, err := os.CreateTemp(spoolDir, "body-*")
	if err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("spool source %s: %w", sourcePath, err)
	}
	spoolPath := spool.Name()
	removeSpool := true
	defer func() {
		_ = spool.Close()
		if removeSpool {
			_ = os.Remove(spoolPath)
		}
	}()
	if err := spool.Chmod(0o600); err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("secure source snapshot %s: %w", sourcePath, err)
	}
	hasher := sha256.New()
	written, err := fillSourceSpool(ctx, file, spool, spoolDir, hasher)
	if err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("read source %s: %w", sourcePath, err)
	}
	after, err := file.Stat()
	if err != nil {
		return sourceManifestFileSnapshot{}, fmt.Errorf("stat source %s after read: %w", sourcePath, err)
	}
	if !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || after.Size() != written {
		return sourceManifestFileSnapshot{}, fmt.Errorf("source %s changed during snapshot", sourcePath)
	}
	removeSpool = false
	return sourceManifestFileSnapshot{
		path:      sourcePath,
		spoolPath: spoolPath,
		fileHash:  "sha256:" + hex.EncodeToString(hasher.Sum(nil)),
		bodyBytes: written,
		modTime:   after.ModTime(),
		mode:      after.Mode(),
	}, nil
}

// fillSourceSpool pins the source's bytes in spool. A clone is atomic and shares
// the source's blocks; hashing then reads the pinned clone. The stream copy is the
// fallback for another volume or platform.
func fillSourceSpool(ctx context.Context, file, spool *os.File, spoolDir string, hasher io.Writer) (int64, error) {
	spoolPath := spool.Name()
	if err := spool.Close(); err != nil {
		return 0, err
	}
	if err := os.Remove(spoolPath); err != nil {
		return 0, err
	}
	if err := home.CloneFileInto(file, spoolDir, filepath.Base(spoolPath)); err == nil {
		if err := os.Chmod(spoolPath, 0o600); err != nil {
			return 0, err
		}
		pinned, err := os.Open(spoolPath)
		if err != nil {
			return 0, err
		}
		defer pinned.Close()
		return io.Copy(hasher, sourceContextReader{ctx: ctx, r: pinned})
	}
	copied, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer copied.Close()
	written, err := io.Copy(io.MultiWriter(copied, hasher), sourceContextReader{ctx: ctx, r: file})
	if err != nil {
		return 0, err
	}
	return written, copied.Close()
}

func readSourceManifestSnapshotBody(snapshot sourceManifestFileSnapshot) ([]byte, error) {
	body, err := os.ReadFile(snapshot.spoolPath)
	if err != nil {
		return nil, fmt.Errorf("read spooled source %s: %w", snapshot.path, err)
	}
	if int64(len(body)) != snapshot.bodyBytes || hashBytes(body) != snapshot.fileHash {
		return nil, fmt.Errorf("spooled source %s changed after snapshot", snapshot.path)
	}
	return body, nil
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
