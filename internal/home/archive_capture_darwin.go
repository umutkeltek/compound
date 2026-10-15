//go:build darwin

package home

import (
	"archive/tar"
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// Capture is the source-consistency boundary. Admission fences cooperating
// writers only; event files and other independently locked families remain
// per-file observations. No live source is consulted after capture succeeds.
var archiveCaptureBeforeClone = func(string) error { return nil }
var archiveCaptureClone = unix.Fclonefileat
var archiveStreamCaptureReady = func(*capturedArchive) error { return nil }

const archiveCaptureRecordBytes = 128 << 10

type archiveCaptureStamp struct {
	Device   uint64
	Inode    uint64
	Size     int64
	Mode     fs.FileMode
	Modified time.Time
	Changed  time.Time
}

func captureStamp(info fs.FileInfo) archiveCaptureStamp {
	stat := info.Sys().(*syscall.Stat_t)
	return archiveCaptureStamp{Device: uint64(stat.Dev), Inode: stat.Ino, Size: info.Size(), Mode: info.Mode(),
		Modified: info.ModTime(), Changed: time.Unix(stat.Ctimespec.Sec, stat.Ctimespec.Nsec)}
}
func (s archiveCaptureStamp) matches(info fs.FileInfo) bool {
	other := captureStamp(info)
	return s.Device == other.Device && s.Inode == other.Inode && s.Size == other.Size &&
		s.Mode == other.Mode && s.Modified.Equal(other.Modified) && s.Changed.Equal(other.Changed)
}

type archiveCaptureRecord struct {
	Header tar.Header
	Link   *archiveLink
	Body   int
	Stamp  archiveCaptureStamp
}

func marshalArchiveCaptureRecord(record archiveCaptureRecord) ([]byte, error) {
	// JSON replaces invalid UTF-8; refuse rather than silently rename a path.
	if !utf8.ValidString(record.Header.Name) {
		return nil, fmt.Errorf("capture path must be valid UTF-8")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(data)+1 > archiveCaptureRecordBytes {
		return nil, fmt.Errorf("capture record exceeds limit")
	}
	return append(data, '\n'), nil
}

type capturedArchive struct {
	root, tmp, stage   *os.Root
	stageName          string
	stageInfo, tmpInfo fs.FileInfo
	journalInfo        fs.FileInfo
	manifest           []byte
	homeSchema         string
	records, bodies    int
	journalHash        string
	closed             bool
}

func captureBodyName(n int) string { return fmt.Sprintf("body-%09d", n) }

func backupCapturedStreamedArchive(homePath, targetPath string, now time.Time, schema string) (report ArchiveReport, resultErr error) {
	var captured *capturedArchive
	err := WithAutonomyAdmissionLock(homePath, func() error {
		if err := prepareArchiveTarget(homePath, targetPath, includeBackupPath); err != nil {
			return err
		}
		var err error
		captured, err = captureStreamedArchive(homePath)
		return err
	})
	if err != nil {
		return ArchiveReport{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, captured.close()) }()
	// This hook is also the falsifier seam: compression has no admission lock.
	if err := archiveStreamCaptureReady(captured); err != nil {
		return ArchiveReport{}, err
	}
	source := &archiveStreamSource{
		manifest: captured.manifest, homeSchema: captured.homeSchema,
		walk: captured.walk, close: captured.close,
	}
	return writeStreamedArchiveSource(homePath, targetPath, now, schema, source)
}

func captureStreamedArchive(homePath string) (_ *capturedArchive, resultErr error) {
	if err := validateArchiveLinkSourceHome(homePath); err != nil {
		return nil, err
	}
	root, err := openStableArchiveRoot(homePath)
	if err != nil {
		return nil, err
	}
	c := &capturedArchive{root: root}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, c.close())
		}
	}()
	if err := archiveStreamRootOpened(homePath); err != nil {
		return nil, err
	}
	if err := validateStreamedSourceHealth(root); err != nil {
		return nil, err
	}
	c.manifest, err = readRootedHomeManifestBounded(root)
	if err != nil {
		return nil, err
	}
	manifest, err := decodeHomeManifest(c.manifest)
	if err != nil {
		return nil, err
	}
	c.homeSchema = manifest.SchemaVersion
	if err := archiveAfterHomeManifestPinned(filepath.Join(homePath, ManifestName)); err != nil {
		return nil, err
	}
	if err := c.createStage(); err != nil {
		return nil, err
	}
	journal, err := c.stage.OpenFile("journal", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer journal.Close()
	digest := sha256.New()
	bounded := &boundedArchiveOutput{writer: io.MultiWriter(journal, digest), remaining: archiveStreamTotalMeta, limit: archiveStreamTotalMeta, kind: "capture journal"}
	budget := archiveEntryBudget{limits: archiveStreamTarLimits()}
	links := 0
	err = walkArchiveRootBatched(root, ".", func(rel string, info fs.FileInfo) error {
		if rel == ManifestName {
			return nil
		}
		if !includeBackupPath(rel, info) {
			if info.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("archive included source entry must be regular file or directory: %s", rel)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		// Check bounds before doing any native clone work.
		if err := budget.observe(header); err != nil {
			return err
		}
		record := archiveCaptureRecord{Header: *header}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > archiveStreamMaxLinks {
				return fmt.Errorf("archive link count exceeds %d", archiveStreamMaxLinks)
			}
			link, err := captureArchiveLink(root, homePath, rel, info)
			if err != nil {
				return err
			}
			record.Link = &link
		} else if info.Mode().IsRegular() {
			c.bodies++
			record.Body = c.bodies
			metadata, stamp, err := c.clone(rel, info, captureBodyName(record.Body))
			if err != nil {
				return err
			}
			record.Header, record.Stamp = *metadata, stamp
			// Appends may enlarge an event between traversal and its clone.
			delta := record.Header.Size - header.Size
			if delta > 0 {
				if record.Header.Size > budget.limits.MaxEntryBytes || budget.expandedBytes > budget.limits.MaxExpandedBytes-delta {
					return fmt.Errorf("captured event exceeds archive byte budget")
				}
				budget.expandedBytes += delta
			}
		}
		data, err := marshalArchiveCaptureRecord(record)
		if err != nil {
			return err
		}
		if _, err := bounded.Write(data); err != nil {
			return err
		}
		c.records++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := journal.Close(); err != nil {
		return nil, err
	}
	c.journalInfo, err = c.stage.Lstat("journal")
	if err != nil {
		return nil, err
	}
	c.journalHash = hex.EncodeToString(digest.Sum(nil))
	if err := c.validateStage(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *capturedArchive) createStage() error {
	var err error
	c.tmpInfo, err = c.root.Lstat("tmp")
	if err != nil {
		return err
	}
	if !c.tmpInfo.IsDir() || c.tmpInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("capture temporary root must be a regular directory")
	}
	c.tmp, err = c.root.OpenRoot("tmp")
	if err != nil {
		return err
	}
	opened, err := c.tmp.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(c.tmpInfo, opened) {
		return fmt.Errorf("capture temporary root changed")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	c.stageName = "home-backup-snapshot-" + hex.EncodeToString(nonce[:])
	if err := c.tmp.Mkdir(c.stageName, 0o700); err != nil {
		return err
	}
	c.stageInfo, err = c.tmp.Lstat(c.stageName)
	if err != nil {
		return err
	}
	c.stage, err = c.tmp.OpenRoot(c.stageName)
	if err != nil {
		return err
	}
	return c.validateStage()
}

func (c *capturedArchive) validateStage() error {
	tmp, err := c.root.Lstat("tmp")
	if err != nil {
		return err
	}
	stage, err := c.tmp.Lstat(c.stageName)
	if err != nil {
		return err
	}
	opened, err := c.stage.Stat(".")
	if err != nil {
		return err
	}
	if !tmp.IsDir() || tmp.Mode()&os.ModeSymlink != 0 || !os.SameFile(c.tmpInfo, tmp) ||
		!stage.IsDir() || stage.Mode()&os.ModeSymlink != 0 || stage.Mode().Perm() != 0o700 ||
		!os.SameFile(c.stageInfo, stage) || !os.SameFile(c.stageInfo, opened) {
		return fmt.Errorf("capture stage identity or permissions changed")
	}
	return nil
}

func sameCaptureSource(before, after fs.FileInfo, event bool) bool {
	return after.Mode().IsRegular() && os.SameFile(before, after) &&
		(event || captureStamp(before).matches(after))
}
func (c *capturedArchive) clone(rel string, before fs.FileInfo, name string) (_ *tar.Header, _ archiveCaptureStamp, resultErr error) {
	source, err := c.root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	defer source.Close()
	event := strings.HasPrefix(rel, "events/")
	opened, err := source.Stat()
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	current, err := c.root.Lstat(filepath.FromSlash(rel))
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	if !sameCaptureSource(before, opened, event) || !sameCaptureSource(before, current, event) {
		return nil, archiveCaptureStamp{}, fmt.Errorf("capture source identity or metadata changed: %s", rel)
	}
	if err := archiveCaptureBeforeClone(rel); err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	dir, err := c.stage.Open(".")
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	cloneErr := archiveCaptureClone(int(source.Fd()), int(dir.Fd()), name, 0)
	if err := errors.Join(cloneErr, dir.Close()); err != nil {
		return nil, archiveCaptureStamp{}, fmt.Errorf("immutable capture clone unavailable for %s: %w", rel, err)
	}
	after, err := c.root.Lstat(filepath.FromSlash(rel))
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	end, err := source.Stat()
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	if !sameCaptureSource(before, after, event) || !sameCaptureSource(before, end, event) {
		return nil, archiveCaptureStamp{}, fmt.Errorf("capture source changed during clone: %s", rel)
	}
	metadata, err := c.stage.Lstat(name)
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	if !metadata.Mode().IsRegular() || (!event && (metadata.Size() != before.Size() || !metadata.ModTime().Equal(before.ModTime()))) {
		return nil, archiveCaptureStamp{}, fmt.Errorf("capture clone metadata changed: %s", rel)
	}
	header, err := tar.FileInfoHeader(metadata, "")
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	header.Name = rel
	if err := c.stage.Chmod(name, 0o600); err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	sealed, err := c.stage.Lstat(name)
	if err != nil {
		return nil, archiveCaptureStamp{}, err
	}
	if !os.SameFile(metadata, sealed) || sealed.Size() != metadata.Size() || !sealed.ModTime().Equal(metadata.ModTime()) {
		return nil, archiveCaptureStamp{}, fmt.Errorf("capture clone changed while sealing: %s", rel)
	}
	return header, captureStamp(sealed), nil
}

func (c *capturedArchive) walk(visit func(archiveStreamItem) error) error {
	if err := c.validateStage(); err != nil {
		return err
	}
	journal, err := c.stage.Open("journal")
	if err != nil {
		return err
	}
	defer journal.Close()
	opened, err := journal.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(c.journalInfo, opened) || !captureStamp(c.journalInfo).matches(opened) {
		return fmt.Errorf("capture journal changed before replay")
	}
	digest := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(io.LimitReader(journal, c.journalInfo.Size()+1), digest))
	scanner.Buffer(make([]byte, 4096), archiveCaptureRecordBytes)
	count, bodies := 0, 0
	for scanner.Scan() {
		count++
		if count > c.records {
			return fmt.Errorf("capture journal record count changed")
		}
		var record archiveCaptureRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return err
		}
		item := archiveStreamItem{rel: record.Header.Name, info: record.Header.FileInfo()}
		if record.Header.Typeflag == tar.TypeSymlink {
			if record.Link == nil || record.Link.Path != item.rel || record.Body != 0 {
				return fmt.Errorf("invalid captured link")
			}
			item.link = func() (archiveLink, error) { return *record.Link, nil }
		} else if record.Header.Typeflag == tar.TypeReg {
			if record.Link != nil || record.Header.Size != record.Stamp.Size {
				return fmt.Errorf("invalid captured body")
			}
			bodies++
			if record.Body != bodies {
				return fmt.Errorf("capture body order changed")
			}
			name := captureBodyName(bodies)
			before, err := c.stage.Lstat(name)
			if err != nil {
				return err
			}
			if !record.Stamp.matches(before) {
				return fmt.Errorf("capture body changed before replay: %s", item.rel)
			}
			item.write = func(tw archiveEntryWriter) (ArchiveEntry, error) {
				return writeRootedArchiveFileAt(tw, c.stage, name, item.rel, before, record.Header.FileInfo())
			}
		} else if record.Header.Typeflag != tar.TypeDir || record.Link != nil || record.Body != 0 {
			return fmt.Errorf("invalid capture entry type")
		}
		if err := visit(item); err != nil {
			return err
		}
		if record.Body != 0 {
			after, err := c.stage.Lstat(captureBodyName(record.Body))
			if err != nil {
				return err
			}
			if !record.Stamp.matches(after) {
				return fmt.Errorf("capture body changed during replay: %s", item.rel)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	after, err := c.stage.Lstat("journal")
	if err != nil {
		return err
	}
	end, err := journal.Stat()
	if err != nil {
		return err
	}
	if count != c.records || bodies != c.bodies || hex.EncodeToString(digest.Sum(nil)) != c.journalHash ||
		!os.SameFile(c.journalInfo, after) || !captureStamp(c.journalInfo).matches(after) || !captureStamp(c.journalInfo).matches(end) {
		return fmt.Errorf("capture journal changed during replay")
	}
	if err := c.validateStage(); err != nil {
		return err
	}
	if err := journal.Close(); err != nil {
		return err
	}
	// Reclaim our private inputs before publication, including on cleanup error.
	return c.close()
}

func (c *capturedArchive) close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	var result error
	if c.stage != nil {
		for n := 0; n <= c.bodies; n++ {
			name := "journal"
			if n > 0 {
				name = captureBodyName(n)
			}
			if err := c.stage.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				result = errors.Join(result, err)
			}
		}
		result = errors.Join(result, c.stage.Close())
	}
	if c.tmp != nil {
		if c.stageInfo != nil {
			current, err := c.tmp.Lstat(c.stageName)
			if err == nil && os.SameFile(c.stageInfo, current) && current.IsDir() {
				result = errors.Join(result, c.tmp.Remove(c.stageName))
			} else {
				result = errors.Join(result, fmt.Errorf("capture stage identity changed during cleanup"), err)
			}
		}
		result = errors.Join(result, c.tmp.Close())
	}
	if c.root != nil {
		result = errors.Join(result, c.root.Close())
	}
	return result
}
