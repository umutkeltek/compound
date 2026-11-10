package sourceobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CaptureOptions struct {
	// Refuse each link without reading it, while capturing regular files.
	ContinueAfterRefusedSymlink bool
}

type CaptureBatchReport struct {
	RefusedSymlinks int
	Oversized       int
	Deferred        int
	Missing         int
	SourceManifestReport
	Captured  int
	Unchanged int
	Pending   int
}

// CaptureApprovedDirectoryBatch prioritizes recent changes, bounds each pass,
// and skips already-custodied unchanged files using size + nanosecond mtime.
// This is an incremental freshness check, not a content-integrity audit.
func CaptureApprovedDirectoryBatch(ctx context.Context, homePath, family, root, format string, now time.Time) (CaptureBatchReport, error) {
	return CaptureApprovedDirectoryBatchWithOptions(ctx, homePath, family, root, format, now, CaptureOptions{})
}

// CaptureApprovedDirectoryBatchWithOptions makes partial capture an explicit choice.
// The default entry point continues to refuse the entire batch on a symlink.
func CaptureApprovedDirectoryBatchWithOptions(ctx context.Context, homePath, family, root, format string, now time.Time, opts CaptureOptions) (CaptureBatchReport, error) {
	result := CaptureBatchReport{}
	existing, loadErr := LoadSourceManifest(homePath, family)
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return result, loadErr
	}
	current := map[string]SourceManifestItem{}
	for _, item := range existing.Items {
		if item.VersionState != "superseded" {
			current[item.SourceKey] = item
		}
	}
	selectedBytes := int64(0)
	selected := 0
	seen := map[string]bool{}
	choose := func(relative string, info os.FileInfo) bool {
		key := idFor("source_", family, filepath.ToSlash(relative))
		seen[key] = true
		if info.Size() > 1<<30 {
			result.Oversized++
			return false
		}
		if old, ok := current[key]; ok && old.BodyPolicy == "owned_snapshot" && old.BodyBytes == info.Size() && old.SnapshotTime == info.ModTime().UTC().Format(time.RFC3339Nano) {
			result.Unchanged++
			return false
		}
		if old, ok := current[key]; ok && old.BodyPolicy == "owned_snapshot" && !ownedVersionDue(old, info, now) {
			result.Pending++
			return false
		}
		if selected >= 128 || (selected > 0 && selectedBytes+info.Size() > 128<<20) {
			result.Pending++
			return false
		}
		selected++
		selectedBytes += info.Size()
		return true
	}
	ext := ".md"
	switch format {
	case "markdown":
	case "codex", "claude":
		ext = ".jsonl"
	default:
		return CaptureBatchReport{}, fmt.Errorf("unsupported source format")
	}
	if strings.TrimSpace(family) == "" {
		return CaptureBatchReport{}, fmt.Errorf("source family required")
	}
	absolute, batch, err := snapshotSourceManifestDirectoryFormat(ctx, root, ext, true, choose, opts.ContinueAfterRefusedSymlink)
	if err != nil {
		return CaptureBatchReport{}, err
	}
	defer batch.Close()
	result.Deferred = batch.deferred
	result.RefusedSymlinks = batch.refusedSymlinks
	if format != "markdown" {
		valid := batch.files[:0]
		for _, snapshot := range batch.files {
			if err := validateConversationSnapshot(ctx, snapshot.spoolPath); err != nil {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				result.Deferred++
				continue
			}
			valid = append(valid, snapshot)
		}
		batch.files = valid
	}
	for key := range current {
		if !seen[key] {
			result.Missing++
		}
	}
	if len(batch.files) == 0 {
		result.SourceManifestReport = SourceManifestReport{ManifestID: existing.ID, ManifestPath: SourceManifestPath(homePath, family), AuthorityRef: SourceManifestAuthorityRef(family), ItemCount: len(existing.Items)}
		return result, nil
	}
	manifest, err := buildSourceManifestSnapshots(family, batch.files, now, nil)
	if err != nil {
		return CaptureBatchReport{}, err
	}
	snapshots := map[string]sourceManifestFileSnapshot{}
	for _, snap := range batch.files {
		snapshots[snap.path] = snap
	}
	for i := range manifest.Items {
		item := &manifest.Items[i]
		rel, err := filepath.Rel(absolute, item.SourceRef)
		if err != nil {
			return CaptureBatchReport{}, err
		}
		item.SnapshotTime = snapshots[item.SourceRef].modTime.UTC().Format(time.RFC3339Nano)
		item.SourceKey = idFor("source_", family, filepath.ToSlash(rel))
		if format != "markdown" {
			item.SourceKind = format + "_transcript_v1"
		}
		// Capture does not infer project identity or promote anything.
		item.CompoundEntityRefs = nil
	}
	var report SourceManifestReport
	err = withSourceManifestAuthorityLock(homePath, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		owned, err := captureOwnedSourceManifest(homePath, manifest, batch.files, nil)
		if err != nil {
			return err
		}
		// The same bytes can be observed again after a correction. Body identity
		// remains content-addressed; the new occurrence must not resurrect an old ID.
		latest, loadErr := LoadSourceManifest(homePath, family)
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return loadErr
		}
		byKey := map[string]SourceManifestItem{}
		for _, prior := range latest.Items {
			if prior.VersionState != "superseded" {
				byKey[prior.SourceKey] = prior
			}
		}
		for i := range owned.Items {
			item := &owned.Items[i]
			if prior, ok := byKey[item.SourceKey]; ok {
				if prior.FileHash == item.FileHash {
					item.ID = prior.ID
				} else {
					item.ID = idFor("srcitem_", family, item.SourceKey, item.FileHash, prior.ID)
				}
			}
		}
		report, err = recordSourceManifestUnlocked(homePath, owned)
		return err
	})
	result.SourceManifestReport = report
	result.Captured = len(batch.files)
	return result, err
}

const (
	ownedVersionSmallBytes = int64(1 << 20)
	ownedVersionQuietAfter = time.Hour
)

// ownedVersionDue decides whether a changed file earns a new owned version now.
// Each version is a whole body, and a live transcript changes on every turn: one
// session stored 122 versions, 4 GiB for a 46 MB file. So a large file waits until
// it has grown by a quarter or gone quiet for an hour. Its last owned version stays
// readable meanwhile and the pass reports the file as pending. Small files and
// rewrites that shrink are always due: nothing here can hold back a correction.
func ownedVersionDue(old SourceManifestItem, info os.FileInfo, now time.Time) bool {
	size := info.Size()
	if size < ownedVersionSmallBytes || size < old.BodyBytes {
		return true
	}
	if size-old.BodyBytes >= old.BodyBytes/4 {
		return true
	}
	return now.Sub(info.ModTime()) >= ownedVersionQuietAfter
}

// A live harness can stop between writes of one JSONL record. Keep the last
// complete owned version until the entire next snapshot is valid, so an
// unfinished record cannot poison every future index rebuild through history.
func validateConversationSnapshot(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<30)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' || !json.Valid(line) {
			return fmt.Errorf("incomplete transcript record")
		}
	}
	return scanner.Err()
}
