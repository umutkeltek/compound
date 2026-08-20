package home

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
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
	"sort"
	"strings"
	"syscall"
	"time"

	"compound/internal/candidates"

	"gopkg.in/yaml.v3"
)

const (
	ArchiveSchemaVersion     = "compound.home_archive.v0.1"
	ArchiveManifestName      = "compound-home-archive.json"
	PublicFixtureSummaryName = "compound-home-public-fixture.json"

	ArchiveKindBackup        = "backup"
	ArchiveKindPublicFixture = "public_fixture"
	ArchiveKindOperational   = "operational_history"

	archiveManifestMaxBytes       = 64 << 20
	archiveMaxSourceBytes         = int64(64 << 30)
	archiveMaxEntries             = 250_000
	archiveMaxPathBytes           = 4 << 10
	archiveMaxPathDepth           = 64
	archiveMaxEntryBytes    int64 = 8 << 30
	archiveMaxExpandedBytes int64 = 64 << 30
	archiveMaxDecodedBytes  int64 = 72 << 30
)

type archiveReadLimits struct {
	MaxSourceBytes   int64
	MaxEntries       int
	MaxPathBytes     int
	MaxPathDepth     int
	MaxEntryBytes    int64
	MaxExpandedBytes int64
	MaxDecodedBytes  int64
}

var defaultArchiveReadLimits = archiveReadLimits{
	MaxSourceBytes:   archiveMaxSourceBytes,
	MaxEntries:       archiveMaxEntries,
	MaxPathBytes:     archiveMaxPathBytes,
	MaxPathDepth:     archiveMaxPathDepth,
	MaxEntryBytes:    archiveMaxEntryBytes,
	MaxExpandedBytes: archiveMaxExpandedBytes,
	MaxDecodedBytes:  archiveMaxDecodedBytes,
}

// archiveAfterHomeManifestPinned is a deterministic test seam. Production
// leaves it inert; backup tests use it to replace the manifest pathname after
// the writer is expected to have captured the exact identity bytes.
var archiveAfterHomeManifestPinned = func(string) error { return nil }

// Compatibility projection for archive manifests and existing callers. The
// HabitatCatalog owns inclusion policy, including protected descendants under
// otherwise rebuildable roots such as projections.
var backupExcludedRoots = habitatBackupExcludedRoots()

// ArchiveEntry records one checksummed file inside a home archive.
type ArchiveEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ArchiveManifest is stored inside every home archive.
type ArchiveManifest struct {
	SchemaVersion        string         `json:"schema_version"`
	Kind                 string         `json:"kind"`
	CreatedAt            string         `json:"created_at"`
	SourceHome           string         `json:"source_home,omitempty"`
	HomeSchemaVersion    string         `json:"home_schema_version,omitempty"`
	ExcludedRoots        []string       `json:"excluded_roots,omitempty"`
	Entries              []ArchiveEntry `json:"entries"`
	embeddedHomeManifest []byte
}

type ArchiveReport struct {
	Kind        string
	ArchivePath string
	Files       int
	Bytes       int64
	Skipped     []string
}

type VerifyReport struct {
	Kind        string
	ArchivePath string
	Files       int
	Bytes       int64
	OK          bool
}

type RestoreReport struct {
	ArchivePath string
	TargetHome  string
	Kind        string
	Files       int
	Bytes       int64
}

type CompactOptions struct {
	DryRun      bool
	Rebuildable bool
}

type CompactReport struct {
	Home         string
	DryRun       bool
	ClearedDirs  []string
	RemovedFiles int
	RemovedBytes int64
}

const (
	OperationalArchiveSchemaVersion = "compound.operational_archive.v0.1"
	OperationalArchiveIndexRel      = "archives/operational/index.jsonl"
	OperationalArchiveFilesRel      = "archives/operational/files"
)

type OperationalArchiveOptions struct {
	DryRun bool
	Kinds  []string
	Paths  []string
}

type OperationalRestoreOptions struct {
	DryRun bool
	All    bool
	IDs    []string
}

type OperationalArchiveEntry struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Operation     string `json:"operation"`
	CreatedAt     string `json:"created_at"`
	Kind          string `json:"kind"`
	Status        string `json:"status,omitempty"`
	OriginalPath  string `json:"original_path"`
	ArchivePath   string `json:"archive_path"`
	Size          int64  `json:"size,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
}

type OperationalArchiveKeptEntry struct {
	Kind         string `json:"kind"`
	Status       string `json:"status,omitempty"`
	OriginalPath string `json:"original_path"`
	KeptReason   string `json:"kept_reason"`
}

type OperationalArchiveReport struct {
	SchemaVersion string                        `json:"schema_version"`
	Home          string                        `json:"home"`
	DryRun        bool                          `json:"dry_run"`
	ArchivedAt    string                        `json:"archived_at,omitempty"`
	RestoredAt    string                        `json:"restored_at,omitempty"`
	ArchivedCount int                           `json:"archived_count,omitempty"`
	RestoredCount int                           `json:"restored_count,omitempty"`
	Counts        map[string]int                `json:"counts,omitempty"`
	Entries       []OperationalArchiveEntry     `json:"entries"`
	Kept          []OperationalArchiveKeptEntry `json:"kept,omitempty"`
}

type operationalCandidate struct {
	Kind         string
	Status       string
	OriginalPath string
	KeptReason   string
}

// Backup writes a durable COMPOUND_HOME archive. Transient and rebuildable roots
// are excluded so backup size tracks owned evidence, not cache mass.
func Backup(homePath, targetPath string, now time.Time) (ArchiveReport, error) {
	homePath, targetPath, err := normalizeArchiveInputs(homePath, targetPath, ArchiveKindBackup, now)
	if err != nil {
		return ArchiveReport{}, err
	}
	var report ArchiveReport
	err = WithAutonomyAdmissionLock(homePath, func() error {
		if err := prepareArchiveTarget(homePath, targetPath, includeBackupPath); err != nil {
			return err
		}
		if err := requireHealthyHome(homePath); err != nil {
			return err
		}
		var writeErr error
		report, writeErr = writeHomeArchive(homePath, targetPath, ArchiveKindBackup, backupExcludedRoots, now, includeBackupPath)
		return writeErr
	})
	if err != nil {
		return ArchiveReport{}, err
	}
	return report, nil
}

// ExportPublicFixture writes a redacted fixture archive: habitat shape and
// sanitized manifest only, no runtime ledger/events/context/private files.
func ExportPublicFixture(homePath, targetPath string, now time.Time) (ArchiveReport, error) {
	homePath, targetPath, err := normalizeArchiveInputs(homePath, targetPath, ArchiveKindPublicFixture, now)
	if err != nil {
		return ArchiveReport{}, err
	}
	var report ArchiveReport
	err = WithAutonomyAdmissionLock(homePath, func() error {
		if err := prepareArchiveTarget(homePath, targetPath, func(string, fs.FileInfo) bool { return false }); err != nil {
			return err
		}
		if err := requireHealthyHome(homePath); err != nil {
			return err
		}
		var writeErr error
		report, writeErr = writePublicFixtureArchive(homePath, targetPath, now)
		return writeErr
	})
	if err != nil {
		return ArchiveReport{}, err
	}
	return report, nil
}

func writePublicFixtureArchive(homePath, targetPath string, now time.Time) (ArchiveReport, error) {
	aw, err := beginArchivePublication(homePath, targetPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer aw.cleanup()
	gz := gzip.NewWriter(aw.file)
	tw := tar.NewWriter(gz)

	entries := []ArchiveEntry{}
	var bytesWritten int64
	dirs := HabitatDirs()
	for _, dir := range dirs {
		if err := writeDirEntry(tw, filepath.ToSlash(dir), archiveHabitatDirMode(dir)); err != nil {
			return ArchiveReport{}, err
		}
	}
	fixtureManifest := Manifest{
		SchemaVersion: SchemaVersion,
		Kind:          ManifestKind,
		HomeKind:      ArchiveKindPublicFixture,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		ActiveModules: []string{},
	}
	manifestBytes, err := yaml.Marshal(fixtureManifest)
	if err != nil {
		return ArchiveReport{}, err
	}
	entry, err := writeBytesEntry(tw, ManifestName, manifestBytes, 0o644)
	if err != nil {
		return ArchiveReport{}, err
	}
	entries = append(entries, entry)
	bytesWritten += entry.Size

	summaryBytes, err := json.MarshalIndent(map[string]any{
		"schema_version":    "compound.home_public_fixture.v0.1",
		"created_at":        now.UTC().Format(time.RFC3339),
		"home_schema":       SchemaVersion,
		"dirs":              dirs,
		"private_data_note": "runtime files are intentionally omitted; this archive is safe as a public fixture shape",
	}, "", "  ")
	if err != nil {
		return ArchiveReport{}, err
	}
	entry, err = writeBytesEntry(tw, PublicFixtureSummaryName, summaryBytes, 0o644)
	if err != nil {
		return ArchiveReport{}, err
	}
	entries = append(entries, entry)
	bytesWritten += entry.Size

	archiveManifest := ArchiveManifest{
		SchemaVersion:     ArchiveSchemaVersion,
		Kind:              ArchiveKindPublicFixture,
		CreatedAt:         now.UTC().Format(time.RFC3339),
		HomeSchemaVersion: SchemaVersion,
		ExcludedRoots:     []string{"all runtime files"},
		Entries:           sortedArchiveEntries(entries),
	}
	if err := writeArchiveManifest(tw, archiveManifest); err != nil {
		return ArchiveReport{}, err
	}
	if err := closeArchiveWriters(tw, gz); err != nil {
		return ArchiveReport{}, err
	}
	if err := aw.publish(); err != nil {
		return ArchiveReport{}, err
	}
	return ArchiveReport{
		Kind:        ArchiveKindPublicFixture,
		ArchivePath: targetPath,
		Files:       len(entries),
		Bytes:       bytesWritten,
		Skipped:     archiveManifest.ExcludedRoots,
	}, nil
}

// VerifyArchive checks the archive manifest and file checksums.
func VerifyArchive(archivePath string) (VerifyReport, error) {
	archivePath, err := filepath.Abs(expandUser(archivePath))
	if err != nil {
		return VerifyReport{}, err
	}
	manifest, actual, err := readArchive(archivePath, nil)
	if err != nil {
		return VerifyReport{}, err
	}
	return verifyArchiveManifest(archivePath, manifest, actual)
}

func verifyArchiveManifest(archivePath string, manifest ArchiveManifest, actual map[string]ArchiveEntry) (VerifyReport, error) {
	return verifyArchiveManifestWithLimits(archivePath, manifest, actual, defaultArchiveReadLimits)
}

func verifyArchiveManifestWithLimits(archivePath string, manifest ArchiveManifest, actual map[string]ArchiveEntry, limits archiveReadLimits) (VerifyReport, error) {
	if err := validateArchiveReadLimits(limits); err != nil {
		return VerifyReport{}, err
	}
	if manifest.SchemaVersion != ArchiveSchemaVersion {
		return VerifyReport{}, asArchiveCompatibilityError(fmt.Errorf("archive manifest schema_version must be %s, got %q", ArchiveSchemaVersion, manifest.SchemaVersion))
	}
	switch manifest.Kind {
	case ArchiveKindBackup, ArchiveKindPublicFixture:
	default:
		return VerifyReport{}, asArchiveCompatibilityError(fmt.Errorf("archive kind must be %s or %s, got %q", ArchiveKindBackup, ArchiveKindPublicFixture, manifest.Kind))
	}
	// The raw manifest byte ceiling bounds JSON decode allocation. Apply the
	// semantic count ceiling before allocating the expected-entry map.
	if len(manifest.Entries) > limits.MaxEntries {
		return VerifyReport{}, fmt.Errorf("archive manifest entry count exceeds %d", limits.MaxEntries)
	}
	if len(actual) > limits.MaxEntries {
		return VerifyReport{}, fmt.Errorf("archive actual entry count exceeds %d", limits.MaxEntries)
	}
	expected := map[string]ArchiveEntry{}
	var declaredBytes int64
	for _, entry := range manifest.Entries {
		if err := validateArchivePath(entry.Path); err != nil || strings.HasSuffix(entry.Path, "/") {
			return VerifyReport{}, fmt.Errorf("archive manifest contains invalid file path %q", entry.Path)
		}
		if len(entry.Path) > limits.MaxPathBytes {
			return VerifyReport{}, fmt.Errorf("archive manifest path exceeds %d bytes: %s", limits.MaxPathBytes, entry.Path)
		}
		if depth := len(strings.Split(entry.Path, "/")); depth > limits.MaxPathDepth {
			return VerifyReport{}, fmt.Errorf("archive manifest path depth exceeds %d: %s", limits.MaxPathDepth, entry.Path)
		}
		if entry.Size < 0 || entry.Size > limits.MaxEntryBytes || len(entry.SHA256) != sha256.Size*2 {
			return VerifyReport{}, fmt.Errorf("archive manifest contains invalid metadata for %s", entry.Path)
		}
		if declaredBytes > limits.MaxExpandedBytes-entry.Size {
			return VerifyReport{}, fmt.Errorf("archive manifest expanded bytes exceed %d", limits.MaxExpandedBytes)
		}
		declaredBytes += entry.Size
		if digest, err := hex.DecodeString(entry.SHA256); err != nil || len(digest) != sha256.Size || entry.SHA256 != strings.ToLower(entry.SHA256) {
			return VerifyReport{}, fmt.Errorf("archive manifest contains invalid checksum for %s", entry.Path)
		}
		if entry.Path != ManifestName {
			if _, ok := HabitatForPath(entry.Path); !ok {
				return VerifyReport{}, fmt.Errorf("archive contains unknown habitat entry %s", entry.Path)
			}
		}
		if _, exists := expected[entry.Path]; exists {
			return VerifyReport{}, fmt.Errorf("archive manifest contains duplicate entry %s", entry.Path)
		}
		expected[entry.Path] = entry
	}
	if manifest.HomeSchemaVersion != SchemaVersion {
		return VerifyReport{}, asArchiveCompatibilityError(fmt.Errorf("archive home_schema_version must be %s, got %q", SchemaVersion, manifest.HomeSchemaVersion))
	}
	if len(expected) != len(actual) {
		return VerifyReport{}, fmt.Errorf("archive entry count mismatch: manifest=%d actual=%d", len(expected), len(actual))
	}
	var bytesTotal int64
	for path, want := range expected {
		got, ok := actual[path]
		if !ok {
			return VerifyReport{}, fmt.Errorf("archive missing %s", path)
		}
		if got.Size != want.Size || got.SHA256 != want.SHA256 {
			return VerifyReport{}, fmt.Errorf("archive checksum mismatch for %s", path)
		}
		if bytesTotal > limits.MaxExpandedBytes-got.Size {
			return VerifyReport{}, fmt.Errorf("archive verified bytes exceed %d", limits.MaxExpandedBytes)
		}
		bytesTotal += got.Size
	}
	if _, ok := expected[ManifestName]; !ok || len(manifest.embeddedHomeManifest) == 0 {
		return VerifyReport{}, fmt.Errorf("archive embedded home manifest is missing")
	}
	embedded, err := decodeHomeManifest(manifest.embeddedHomeManifest)
	if err != nil {
		return VerifyReport{}, err
	}
	if embedded.SchemaVersion != manifest.HomeSchemaVersion {
		return VerifyReport{}, asArchiveCompatibilityError(fmt.Errorf(
			"archive home_schema_version %q does not match embedded manifest schema_version %q",
			manifest.HomeSchemaVersion,
			embedded.SchemaVersion,
		))
	}
	return VerifyReport{
		Kind:        manifest.Kind,
		ArchivePath: archivePath,
		Files:       len(actual),
		Bytes:       bytesTotal,
		OK:          true,
	}, nil
}

// Compact clears only paths HabitatCatalog classifies as transient or, when
// requested, rebuildable. A behavior-bearing exception remains protected even
// when its historical parent is named projections.
func Compact(homePath string, opts CompactOptions) (CompactReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return CompactReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return CompactReport{}, err
	}
	dirs := habitatCompactRoots(opts.Rebuildable)
	report := CompactReport{Home: homePath, DryRun: opts.DryRun, ClearedDirs: append([]string(nil), dirs...)}
	for _, dir := range dirs {
		full := filepath.Join(homePath, dir)
		if !isUnderHome(homePath, full) {
			return CompactReport{}, fmt.Errorf("refusing compact path outside home: %s", full)
		}
		paths, files, bytes, err := compactablePaths(homePath, full, opts.Rebuildable)
		if err != nil {
			return CompactReport{}, err
		}
		report.RemovedFiles += files
		report.RemovedBytes += bytes
		if opts.DryRun {
			continue
		}
		for _, path := range paths {
			if err := os.Remove(path); err != nil {
				return CompactReport{}, err
			}
		}
		if err := removeEmptyCompactDirs(full); err != nil {
			return CompactReport{}, err
		}
	}
	return report, nil
}

func compactablePaths(homePath, root string, rebuildable bool) ([]string, int, int64, error) {
	var paths []string
	var bytes int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path == root || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return err
		}
		if !habitatCompactable(filepath.ToSlash(rel), rebuildable) {
			return nil
		}
		paths = append(paths, path)
		bytes += info.Size()
		return nil
	})
	return paths, len(paths), bytes, err
}

func removeEmptyCompactDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		if err := os.Remove(dir); err != nil && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return err
		}
	}
	return nil
}

func OperationalArchiveIndexPath(homePath string) string {
	return filepath.Join(homePath, OperationalArchiveIndexRel)
}

func OperationalArchiveFilesRoot(homePath string) string {
	return filepath.Join(homePath, OperationalArchiveFilesRel)
}

func LoadOperationalArchiveIndex(homePath string) ([]OperationalArchiveEntry, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return nil, err
	}
	data, err := ReadRootedRegularFile(homePath, OperationalArchiveIndexPath(homePath), "operational archive index")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("operational archive index must end with newline")
	}
	active := map[string]OperationalArchiveEntry{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry OperationalArchiveEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse operational archive index line %d: %w", i+1, err)
		}
		if entry.SchemaVersion != OperationalArchiveSchemaVersion {
			return nil, fmt.Errorf("operational archive index line %d schema_version must be %s, got %q", i+1, OperationalArchiveSchemaVersion, entry.SchemaVersion)
		}
		switch entry.Operation {
		case "archived":
			if prior, ok := active[entry.ID]; ok {
				return nil, fmt.Errorf("operational archive index line %d has repeated active archived operation %s (prior path %s)", i+1, entry.ID, prior.OriginalPath)
			}
			active[entry.ID] = entry
		case "restored":
			delete(active, entry.ID)
		default:
			return nil, fmt.Errorf("operational archive index line %d has unknown operation %q", i+1, entry.Operation)
		}
	}
	out := make([]OperationalArchiveEntry, 0, len(active))
	for _, entry := range active {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].OriginalPath < out[j].OriginalPath
	})
	return out, nil
}

func ArchiveOperationalHistory(homePath string, opts OperationalArchiveOptions, now time.Time) (OperationalArchiveReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	var report OperationalArchiveReport
	var archiveErr error
	err = WithAutonomyAdmissionLock(homePath, func() error {
		report, archiveErr = archiveOperationalHistoryLocked(homePath, opts, now)
		return archiveErr
	})
	return report, err
}

func archiveOperationalHistoryLocked(homePath string, opts OperationalArchiveOptions, now time.Time) (OperationalArchiveReport, error) {
	if err := requireHealthyHome(homePath); err != nil {
		return OperationalArchiveReport{}, err
	}
	report := OperationalArchiveReport{
		SchemaVersion: OperationalArchiveSchemaVersion,
		Home:          homePath,
		DryRun:        opts.DryRun,
		ArchivedAt:    now.UTC().Format(time.RFC3339),
		Counts:        map[string]int{},
	}
	active, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	alreadyArchived := map[string]bool{}
	for _, entry := range active {
		alreadyArchived[entry.OriginalPath] = true
	}
	candidates, err := operationalArchiveCandidates(homePath, opts, now)
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	for _, cand := range candidates {
		if cand.KeptReason != "" {
			report.Kept = append(report.Kept, OperationalArchiveKeptEntry{
				Kind:         cand.Kind,
				Status:       cand.Status,
				OriginalPath: cand.OriginalPath,
				KeptReason:   cand.KeptReason,
			})
			continue
		}
		if alreadyArchived[cand.OriginalPath] {
			continue
		}
		entry, err := archiveOperationalFile(homePath, cand, now, opts.DryRun)
		if err != nil {
			return OperationalArchiveReport{}, err
		}
		report.Entries = append(report.Entries, entry)
		report.Counts[entry.Kind]++
	}
	report.ArchivedCount = len(report.Entries)
	if !opts.DryRun && len(report.Entries) > 0 {
		if err := commitOperationalArchiveTransaction(homePath, report.Entries); err != nil {
			return OperationalArchiveReport{}, err
		}
	}
	return report, nil
}

func RestoreOperationalArchive(homePath string, opts OperationalRestoreOptions, now time.Time) (OperationalArchiveReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	var report OperationalArchiveReport
	var restoreErr error
	err = WithAutonomyAdmissionLock(homePath, func() error {
		report, restoreErr = restoreOperationalArchiveLocked(homePath, opts, now)
		return restoreErr
	})
	return report, err
}

func restoreOperationalArchiveLocked(homePath string, opts OperationalRestoreOptions, now time.Time) (OperationalArchiveReport, error) {
	if err := requireHealthyHome(homePath); err != nil {
		return OperationalArchiveReport{}, err
	}
	active, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return OperationalArchiveReport{}, err
	}
	selected := selectOperationalArchiveEntries(active, opts)
	if len(selected) == 0 && !opts.All && len(opts.IDs) == 0 {
		return OperationalArchiveReport{}, fmt.Errorf("restore requires --all or at least one --id")
	}
	report := OperationalArchiveReport{
		SchemaVersion: OperationalArchiveSchemaVersion,
		Home:          homePath,
		DryRun:        opts.DryRun,
		RestoredAt:    now.UTC().Format(time.RFC3339),
		Counts:        map[string]int{},
	}
	for _, entry := range selected {
		restored, err := restoreOperationalFile(homePath, entry, now, opts.DryRun)
		if err != nil {
			return OperationalArchiveReport{}, err
		}
		report.Entries = append(report.Entries, restored)
		report.Counts[restored.Kind]++
	}
	report.RestoredCount = len(report.Entries)
	if !opts.DryRun && len(report.Entries) > 0 {
		if err := commitOperationalArchiveTransaction(homePath, report.Entries); err != nil {
			return OperationalArchiveReport{}, err
		}
	}
	return report, nil
}

// operationalArchiveBeforeIndexCommit is a deterministic test seam at the
// exact state the admission lock protects: files have moved, while the
// archive index still describes the prior location. Production is a no-op.
var operationalArchiveBeforeIndexCommit = func(_ string, _ []OperationalArchiveEntry) {}

// operationalArchiveBeforeIndexReplace is a deterministic test seam after the
// canonical preimage has been validated but before publication.
var operationalArchiveBeforeIndexReplace = func(_ string) {}

func operationalArchiveCandidates(homePath string, opts OperationalArchiveOptions, now time.Time) ([]operationalCandidate, error) {
	kinds, err := operationalArchiveKinds(opts.Kinds)
	if err != nil {
		return nil, err
	}
	paths, err := operationalArchivePathFilter(opts.Paths)
	if err != nil {
		return nil, err
	}
	var out []operationalCandidate
	if kinds["work"] {
		candidates, err := terminalWorkCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["lease"] {
		candidates, err := staleLeaseCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["review"] {
		candidates, err := terminalReviewCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["comparison"] {
		candidates, err := terminalComparisonCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if kinds["create-plan"] {
		candidates, err := staleCreatePlanCandidates(homePath, now)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
		if len(paths) > 0 {
			candidates, err := explicitCreatePlanCandidates(homePath, paths, now)
			if err != nil {
				return nil, err
			}
			out = append(out, candidates...)
		}
	}
	if kinds["memory-candidate"] {
		candidates, err := terminalMemoryCandidateCandidates(homePath)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if len(paths) > 0 {
		filtered := out[:0]
		for _, candidate := range out {
			if paths[candidate.OriginalPath] {
				filtered = append(filtered, candidate)
			}
		}
		out = filtered
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OriginalPath < out[j].OriginalPath
	})
	return out, nil
}

func operationalArchiveKinds(values []string) (map[string]bool, error) {
	// memory-candidate is opt-in only (landfill compaction); not in the default
	// archive-history mix so a routine work/lease sweep never sweeps the inbox.
	allowed := map[string]bool{"work": true, "lease": true, "review": true, "comparison": true, "create-plan": true, "memory-candidate": true}
	out := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				if !allowed[part] {
					return nil, fmt.Errorf("unknown operational archive kind %q", part)
				}
				out[part] = true
			}
		}
	}
	if len(out) == 0 {
		out["work"] = true
		out["lease"] = true
		out["review"] = true
		out["comparison"] = true
		out["create-plan"] = true
	}
	return out, nil
}

// terminalMemoryCandidateCandidates selects memory inbox rows that are fully
// terminal for landfill compaction: no_promote, rejected, promoted.
// Parked/proposed stay hot (corroboration / operator gate). Claims ledger is
// never touched — provenance stays in ledger/claims.jsonl.
func terminalMemoryCandidateCandidates(homePath string) ([]operationalCandidate, error) {
	dirName := candidates.InboxDir(candidates.Memory)
	if dirName == "" {
		return nil, nil
	}
	dir := filepath.Join(homePath, dirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(dirName, entry.Name()))
		status, err := jsonStringField(filepath.Join(homePath, filepath.FromSlash(rel)), "state")
		if err != nil {
			return nil, err
		}
		if landfillMemoryCandidateStatus(status) {
			out = append(out, operationalCandidate{Kind: "memory-candidate", Status: status, OriginalPath: rel})
		}
	}
	return out, nil
}

func landfillMemoryCandidateStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "no_promote", "rejected", "promoted":
		return true
	default:
		return false
	}
}

func operationalArchivePathFilter(values []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = filepath.ToSlash(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			part = strings.TrimPrefix(part, "./")
			if err := validateArchivePath(part); err != nil {
				return nil, err
			}
			out[part] = true
		}
	}
	return out, nil
}

func terminalWorkCandidates(homePath string) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "work", "work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("work", "work", entry.Name()))
		status, err := jsonStringField(filepath.Join(homePath, filepath.FromSlash(rel)), "state")
		if err != nil {
			return nil, err
		}
		if terminalWorkStatus(status) {
			out = append(out, operationalCandidate{Kind: "work", Status: status, OriginalPath: rel})
		}
	}
	return out, nil
}

// staleLeaseCandidates finds work/leases/*.json whose work_id no longer maps to
// a live WorkEnvelope: the hot work is terminal, already archived, or missing
// entirely (an orphan). Leases for live (non-terminal, hot) work are never
// candidates.
//
// This is the ORPHAN BACKSTOP, not the primary release path. internal/work's
// Close releases a work's own lease inside the same terminal publish (see
// Store.releaseLease), so an ordinary close leaves nothing here. What still
// reaches this sweep is residue the owner cannot retire itself: a lease whose
// work was archived or deleted out from under it, a home written before that
// release existed, and a crash inside the close's release window. The candidate
// predicates are unchanged — the set they select simply stops being fed by
// every normal close.
//
// Liveness override (work_9d627f1cb724): a lease whose work item carries a
// non-terminal RunAttempt with a fresh heartbeat is a live delegate pane,
// even when the hot work envelope has reached a terminal state or vanished
// out from under the pane. Such a lease must NOT be a candidate — archiving
// it would silently kill the pane's claim. The check is read-only and
// best-effort; a probe failure reads as "no live pane" so the orphan backstop
// still fires on homes that never had a run_attempts/ directory.
func staleLeaseCandidates(homePath string) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "work", "leases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var archivedWork map[string]bool
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		workID := strings.TrimSuffix(entry.Name(), ".json")
		rel := filepath.ToSlash(filepath.Join("work", "leases", entry.Name()))
		if liveRunAttemptForWork(homePath, workID, time.Now()) {
			// live delegate pane for this work item; lease survives.
			continue
		}
		state, err := jsonStringField(filepath.Join(homePath, "work", "work", workID+".json"), "state")
		switch {
		case err == nil && !terminalWorkStatus(state):
			continue // live work: its lease must survive the sweep
		case err == nil:
			out = append(out, operationalCandidate{Kind: "lease", Status: state, OriginalPath: rel})
		case os.IsNotExist(err):
			if archivedWork == nil {
				ids, idsErr := archivedWorkIDs(homePath)
				if idsErr != nil {
					return nil, idsErr
				}
				archivedWork = ids
			}
			status := "orphaned"
			if archivedWork[workID] {
				status = "work_archived"
			}
			out = append(out, operationalCandidate{Kind: "lease", Status: status, OriginalPath: rel})
		default:
			return nil, err
		}
	}
	return out, nil
}

const staleCreatePlanAfter = 24 * time.Hour

func operationalCreatePlanSchema(schemaVersion string) bool {
	return schemaVersion == "compound.work_create_plan.v0.1" || schemaVersion == "compound.work_create_plan.v0.2"
}

func explicitCreatePlanCandidates(homePath string, paths map[string]bool, now time.Time) ([]operationalCandidate, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var out []operationalCandidate
	for rel := range paths {
		if !strings.HasPrefix(rel, "create-plans/") || filepath.Ext(rel) != ".json" {
			continue
		}
		candidate, ok, err := explicitCreatePlanCandidate(homePath, rel, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, candidate)
		}
	}
	return out, nil
}

func explicitCreatePlanCandidate(homePath, rel string, now time.Time) (operationalCandidate, bool, error) {
	path := filepath.Join(homePath, filepath.FromSlash(rel))
	var plan struct {
		SchemaVersion string `json:"schema_version"`
		ID            string `json:"id"`
		GeneratedAt   string `json:"generated_at"`
		Status        string `json:"status"`
		WorkID        string `json:"work_id"`
		Policy        struct {
			Allowed bool `json:"allowed"`
		} `json:"policy"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return operationalCandidate{}, false, nil
		}
		return operationalCandidate{}, false, err
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return operationalCandidate{}, false, fmt.Errorf("parse create-plan %s: %w", path, err)
	}
	if !operationalCreatePlanSchema(plan.SchemaVersion) ||
		strings.TrimSpace(plan.Status) != "create_policy_allowed" ||
		!plan.Policy.Allowed ||
		strings.TrimSpace(plan.ID) == "" ||
		strings.TrimSpace(plan.WorkID) == "" {
		return operationalCandidate{}, false, nil
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "work", plan.WorkID+".json")); err == nil {
		return operationalCandidate{}, false, nil
	} else if !os.IsNotExist(err) {
		return operationalCandidate{}, false, err
	}
	generatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(plan.GeneratedAt))
	if err != nil {
		return operationalCandidate{}, false, fmt.Errorf("parse create-plan generated_at %s: %w", path, err)
	}
	if now.UTC().Sub(generatedAt.UTC()) >= staleCreatePlanAfter {
		return operationalCandidate{}, false, nil
	}
	return operationalCandidate{Kind: "create-plan", Status: "explicit_unadmitted", OriginalPath: rel}, true, nil
}

func staleCreatePlanCandidates(homePath string, now time.Time) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "create-plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var plan struct {
			SchemaVersion string `json:"schema_version"`
			ID            string `json:"id"`
			GeneratedAt   string `json:"generated_at"`
			Status        string `json:"status"`
			WorkID        string `json:"work_id"`
			Policy        struct {
				Allowed bool `json:"allowed"`
			} `json:"policy"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &plan); err != nil {
			return nil, fmt.Errorf("parse create-plan %s: %w", path, err)
		}
		if !operationalCreatePlanSchema(plan.SchemaVersion) ||
			strings.TrimSpace(plan.Status) != "create_policy_allowed" ||
			!plan.Policy.Allowed ||
			strings.TrimSpace(plan.ID) == "" ||
			strings.TrimSpace(plan.WorkID) == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(homePath, "work", "work", plan.WorkID+".json")); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		generatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(plan.GeneratedAt))
		if err != nil {
			return nil, fmt.Errorf("parse create-plan generated_at %s: %w", path, err)
		}
		if now.UTC().Sub(generatedAt.UTC()) < staleCreatePlanAfter {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("create-plans", entry.Name()))
		out = append(out, operationalCandidate{Kind: "create-plan", Status: "stale_unadmitted", OriginalPath: rel})
	}
	return out, nil
}

// archivedWorkIDs lists work_ids whose WorkEnvelope already moved into the
// operational archive — their leftover leases are stale by definition.
func archivedWorkIDs(homePath string) (map[string]bool, error) {
	entries, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind != "work" || !strings.HasPrefix(entry.OriginalPath, "work/work/") || !strings.HasSuffix(entry.OriginalPath, ".json") {
			continue
		}
		out[strings.TrimSuffix(strings.TrimPrefix(entry.OriginalPath, "work/work/"), ".json")] = true
	}
	return out, nil
}

// OperationalHygieneReport is the swept-state doctor view: terminal work must
// not sit hot in work/work, and work/leases must only hold leases for live
// work. Both lists reuse the sweep's own candidate predicates, so the doctor
// and `home archive-history` can never disagree about what counts as dirty.
type OperationalHygieneReport struct {
	Home            string
	TerminalHotWork []string // hot work/work envelopes in a terminal state
	StaleLeases     []string // leases without a live (non-terminal, hot) work item
	OK              bool
}

// OperationalHygiene inspects the hot work surfaces without mutating anything.
// It deliberately does not require a healthy home: it feeds Doctor, and the
// archive sweep that remedies a dirty home must still be allowed to run.
func OperationalHygiene(homePath string) (OperationalHygieneReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return OperationalHygieneReport{}, err
	}
	report := OperationalHygieneReport{Home: homePath}
	workCandidates, err := terminalWorkCandidates(homePath)
	if err != nil {
		return report, err
	}
	for _, cand := range workCandidates {
		report.TerminalHotWork = append(report.TerminalHotWork, cand.OriginalPath)
	}
	leaseCandidates, err := staleLeaseCandidates(homePath)
	if err != nil {
		return report, err
	}
	for _, cand := range leaseCandidates {
		report.StaleLeases = append(report.StaleLeases, cand.OriginalPath)
	}
	report.OK = len(report.TerminalHotWork) == 0 && len(report.StaleLeases) == 0
	return report, nil
}

// hygieneChecks folds OperationalHygiene into doctor CheckItems. Hygiene is
// reported, not fatal: Report.OK must keep gating on manifest+dirs only,
// because the archive sweep that cleans a dirty home refuses to run on an
// unhealthy home — gating OK on hygiene would deadlock the remedy. A scan
// error reads as not-OK (dirty until proven swept); Doctor has no error path.
func hygieneChecks(homePath string) ([]CheckItem, bool) {
	hygiene, err := OperationalHygiene(homePath)
	if err != nil {
		return []CheckItem{{Name: "hygiene:scan", OK: false}}, false
	}
	return []CheckItem{
		{Name: "hygiene:no-terminal-work-hot", OK: len(hygiene.TerminalHotWork) == 0},
		{Name: "hygiene:no-stale-leases", OK: len(hygiene.StaleLeases) == 0},
	}, hygiene.OK
}

func terminalReviewCandidates(homePath string) ([]operationalCandidate, error) {
	root := filepath.Join(homePath, "reviews")
	var out []operationalCandidate
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" || !operationalCoreReviewJSON(path) {
			return nil
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		status, err := jsonFirstStringField(path, "status", "verdict", "result")
		if err != nil {
			return err
		}
		operatorPending, err := operatorPendingReviewDecision(path)
		if err != nil {
			return err
		}
		if operatorPending && !hasOperatorDispositionSidecar(path) {
			out = append(out, operationalCandidate{
				Kind:         "review",
				Status:       firstNonEmptyString(status, "operator_pending"),
				OriginalPath: rel,
				KeptReason:   "operator_pending_without_reviewed_by",
			})
			return nil
		}
		derivedStatus, derivedTerminal, err := derivedTerminalReviewStatus(homePath, path)
		if err != nil {
			return err
		}
		if derivedTerminal {
			status = firstNonEmptyString(status, derivedStatus)
		}
		if terminalReviewStatus(status) || derivedTerminal || hasTerminalReviewSidecar(path) {
			status = firstNonEmptyString(status, "recorded")
			out = append(out, operationalCandidate{Kind: "review", Status: status, OriginalPath: rel})
			siblings, err := operationalReviewSiblingPaths(homePath, path)
			if err != nil {
				return err
			}
			for _, sibling := range siblings {
				out = append(out, operationalCandidate{Kind: "review", Status: status, OriginalPath: sibling})
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

func terminalComparisonCandidates(homePath string) ([]operationalCandidate, error) {
	dir := filepath.Join(homePath, "inbox", "comparisons")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []operationalCandidate
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := jsonStringField(path, "state")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(state) != "judged" {
			continue
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return nil, err
		}
		out = append(out, operationalCandidate{
			Kind:         "comparison",
			Status:       state,
			OriginalPath: filepath.ToSlash(rel),
		})
	}
	return out, nil
}

func derivedTerminalReviewStatus(homePath, path string) (string, bool, error) {
	var payload map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false, err
	}
	if jsonMapString(payload, "schema_version") != "compound.panel_verdict.v0.1" {
		return "", false, nil
	}
	target, ok := payload["target"].(map[string]any)
	if !ok {
		return "", false, nil
	}
	switch jsonMapString(target, "kind") {
	case "comparison":
		ref := jsonMapString(target, "ref")
		if ref == "" {
			return "", false, nil
		}
		judged, err := comparisonCandidateTerminal(homePath, ref)
		if err != nil {
			return "", false, err
		}
		if judged {
			return "applied", true, nil
		}
	case "memory_candidate":
		ref := jsonMapString(target, "ref")
		if ref == "" {
			return "", false, nil
		}
		terminal, err := candidateTerminal(homePath, candidates.Memory, ref)
		if err != nil {
			return "", false, err
		}
		if terminal {
			return "applied", true, nil
		}
	case "capability_observation":
		ref := jsonMapString(target, "ref")
		if ref == "" {
			return "", false, nil
		}
		terminal, err := candidateTerminal(homePath, candidates.CapabilityObs, ref)
		if err != nil {
			return "", false, err
		}
		if terminal {
			return "applied", true, nil
		}
	}
	return "", false, nil
}

func comparisonCandidateTerminal(homePath, ref string) (bool, error) {
	rel := filepath.ToSlash(filepath.Join("inbox", "comparisons", ref+".json"))
	path := filepath.Join(homePath, filepath.FromSlash(rel))
	if state, err := jsonStringField(path, "state"); err == nil {
		return strings.TrimSpace(state) == "judged", nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	entries, err := LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Kind == "comparison" && entry.OriginalPath == rel && entry.Status == "judged" {
			return true, nil
		}
	}
	return false, nil
}

func candidateTerminal(homePath string, kind candidates.Kind, ref string) (bool, error) {
	dir := candidates.InboxDir(kind)
	if dir == "" {
		return false, nil
	}
	path := filepath.Join(homePath, dir, ref+".json")
	status, err := jsonFirstStringField(path, "state", "verdict", "status", "result")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	switch strings.TrimSpace(status) {
	case "park", "parked", "promote", "promoted", "reject", "rejected", "no_promote", "operationalize", "operationalized":
		return true, nil
	default:
		return false, nil
	}
}

func archiveOperationalFile(homePath string, cand operationalCandidate, now time.Time, _ bool) (OperationalArchiveEntry, error) {
	rel := filepath.ToSlash(cand.OriginalPath)
	if err := validateArchivePath(rel); err != nil {
		return OperationalArchiveEntry{}, err
	}
	source := filepath.Join(homePath, filepath.FromSlash(rel))
	raw, err := ReadRootedRegularFile(homePath, source, "operational archive source")
	if err != nil {
		return OperationalArchiveEntry{}, err
	}
	archiveRel := filepath.ToSlash(filepath.Join(OperationalArchiveFilesRel, rel))
	entry := OperationalArchiveEntry{
		SchemaVersion: OperationalArchiveSchemaVersion,
		ID:            operationalArchiveID(rel),
		Operation:     "archived",
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Kind:          cand.Kind,
		Status:        cand.Status,
		OriginalPath:  rel,
		ArchivePath:   archiveRel,
		Size:          int64(len(raw)),
		SHA256:        operationalArchiveBytesSHA256(raw),
	}
	return entry, nil
}

func restoreOperationalFile(homePath string, entry OperationalArchiveEntry, now time.Time, _ bool) (OperationalArchiveEntry, error) {
	source := filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath))
	if !isUnderHome(homePath, source) || !isUnderHome(homePath, filepath.Join(homePath, filepath.FromSlash(entry.OriginalPath))) {
		return OperationalArchiveEntry{}, fmt.Errorf("operational archive restore path escapes home: %s", entry.ID)
	}
	raw, err := ReadRootedRegularFile(homePath, source, "operational archive restore source")
	if err != nil {
		return OperationalArchiveEntry{}, err
	}
	if int64(len(raw)) != entry.Size || operationalArchiveBytesSHA256(raw) != strings.TrimSpace(entry.SHA256) {
		return OperationalArchiveEntry{}, fmt.Errorf("operational archive restore source does not match index entry %s", entry.ID)
	}
	restored := entry
	restored.Operation = "restored"
	restored.CreatedAt = now.UTC().Format(time.RFC3339)
	return restored, nil
}

func selectOperationalArchiveEntries(entries []OperationalArchiveEntry, opts OperationalRestoreOptions) []OperationalArchiveEntry {
	if opts.All {
		return append([]OperationalArchiveEntry(nil), entries...)
	}
	wanted := map[string]bool{}
	for _, raw := range opts.IDs {
		for _, id := range strings.Split(raw, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				wanted[id] = true
			}
		}
	}
	var out []OperationalArchiveEntry
	for _, entry := range entries {
		if wanted[entry.ID] {
			out = append(out, entry)
		}
	}
	return out
}

const operationalArchiveIndexName = "index.jsonl"

type operationalArchiveMove struct {
	entry           OperationalArchiveEntry
	sourceRel       string
	destinationRel  string
	sourceName      string
	destinationName string
	sourceRoot      *os.Root
	destinationRoot *os.Root
	sourceData      []byte
	sourceMode      os.FileMode
	sourceSHA256    string
	moved           bool
}

type operationalArchiveTransaction struct {
	homePath           string
	operation          string
	homeRoot           *os.Root
	parentRoots        map[string]*os.Root
	parentInfos        map[string][]os.FileInfo
	moves              []operationalArchiveMove
	indexParentRel     string
	indexRoot          *os.Root
	indexParentInfos   []os.FileInfo
	indexBefore        []byte
	indexBeforeInfo    os.FileInfo
	indexBeforeMode    os.FileMode
	indexAfter         []byte
	indexPublished     bool
	indexPublishedInfo os.FileInfo
	indexPublication   *RootedFileCAS
}

// commitOperationalArchiveTransaction is the single mutation seam for an
// archive/restore batch. It guarantees same-process rollback, including when
// the canonical archive ancestor is replaced after the moves. It deliberately
// has no durable journal: a process or power loss before the index commit may
// leave moved/unindexed state, which existing readers must reject fail-closed
// and an operator must repair.
func commitOperationalArchiveTransaction(homePath string, entries []OperationalArchiveEntry) error {
	tx, err := prepareOperationalArchiveTransaction(homePath, entries)
	if err != nil {
		return err
	}
	defer tx.close()
	return tx.commit(entries)
}

func prepareOperationalArchiveTransaction(homePath string, entries []OperationalArchiveEntry) (*operationalArchiveTransaction, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("operational archive transaction requires entries")
	}
	absHome, err := filepath.Abs(filepath.Clean(homePath))
	if err != nil {
		return nil, err
	}
	homeRoot, err := os.OpenRoot(absHome)
	if err != nil {
		return nil, err
	}
	tx := &operationalArchiveTransaction{
		homePath:        absHome,
		operation:       entries[0].Operation,
		homeRoot:        homeRoot,
		parentRoots:     map[string]*os.Root{},
		parentInfos:     map[string][]os.FileInfo{},
		indexParentRel:  filepath.Dir(filepath.FromSlash(OperationalArchiveIndexRel)),
		indexBeforeMode: 0o644,
	}
	fail := func(err error) (*operationalArchiveTransaction, error) {
		tx.close()
		return nil, err
	}

	seenSource := map[string]bool{}
	seenDestination := map[string]bool{}
	for _, entry := range entries {
		sourceRel, destinationRel, err := operationalArchiveEntryMovePaths(entry, tx.operation)
		if err != nil {
			return fail(err)
		}
		if seenSource[sourceRel] || seenDestination[destinationRel] {
			return fail(fmt.Errorf("operational archive transaction has duplicate move %s -> %s", sourceRel, destinationRel))
		}
		seenSource[sourceRel] = true
		seenDestination[destinationRel] = true
		sourceParentRel := filepath.Dir(sourceRel)
		sourceRoot, err := tx.pinParent(sourceParentRel, false)
		if err != nil {
			return fail(err)
		}
		data, mode, _, err := readOperationalArchiveRootFile(sourceRoot, filepath.Base(sourceRel), "operational archive source")
		if err != nil {
			return fail(err)
		}
		sha := operationalArchiveBytesSHA256(data)
		if int64(len(data)) != entry.Size || sha != strings.TrimSpace(entry.SHA256) {
			return fail(fmt.Errorf("operational archive source snapshot does not match entry %s", entry.ID))
		}
		tx.moves = append(tx.moves, operationalArchiveMove{
			entry:           entry,
			sourceRel:       sourceRel,
			destinationRel:  destinationRel,
			sourceName:      filepath.Base(sourceRel),
			destinationName: filepath.Base(destinationRel),
			sourceRoot:      sourceRoot,
			sourceData:      data,
			sourceMode:      mode,
			sourceSHA256:    sha,
		})
	}

	for i := range tx.moves {
		move := &tx.moves[i]
		destinationRoot, err := tx.pinParent(filepath.Dir(move.destinationRel), true)
		if err != nil {
			return fail(err)
		}
		if _, err := destinationRoot.Lstat(move.destinationName); err == nil {
			return fail(fmt.Errorf("operational archive destination already exists: %s", move.destinationRel))
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
		move.destinationRoot = destinationRoot
	}

	indexRoot, err := tx.pinParent(tx.indexParentRel, true)
	if err != nil {
		return fail(err)
	}
	tx.indexRoot = indexRoot
	tx.indexParentInfos = append([]os.FileInfo(nil), tx.parentInfos[tx.indexParentRel]...)
	if err := tx.captureIndex(entries); err != nil {
		return fail(err)
	}
	return tx, nil
}

func operationalArchiveEntryMovePaths(entry OperationalArchiveEntry, operation string) (string, string, error) {
	if entry.SchemaVersion != OperationalArchiveSchemaVersion {
		return "", "", fmt.Errorf("operational archive entry %s has unsupported schema %q", entry.ID, entry.SchemaVersion)
	}
	if operation != "archived" && operation != "restored" {
		return "", "", fmt.Errorf("operational archive transaction operation must be archived|restored, got %q", operation)
	}
	if entry.Operation != operation {
		return "", "", fmt.Errorf("operational archive transaction mixes %q and %q", operation, entry.Operation)
	}
	original := filepath.ToSlash(strings.TrimSpace(entry.OriginalPath))
	archive := filepath.ToSlash(strings.TrimSpace(entry.ArchivePath))
	if err := validateArchivePath(original); err != nil {
		return "", "", err
	}
	if err := validateArchivePath(archive); err != nil {
		return "", "", err
	}
	wantArchive := filepath.ToSlash(filepath.Join(OperationalArchiveFilesRel, filepath.FromSlash(original)))
	if archive != wantArchive {
		return "", "", fmt.Errorf("operational archive entry %s archive path does not match original path", entry.ID)
	}
	if entry.ID != operationalArchiveID(original) || strings.TrimSpace(entry.SHA256) == "" || entry.Size < 0 {
		return "", "", fmt.Errorf("operational archive entry %s has incomplete identity proof", entry.ID)
	}
	originalRel := filepath.FromSlash(original)
	archiveRel := filepath.FromSlash(archive)
	if operation == "archived" {
		return originalRel, archiveRel, nil
	}
	return archiveRel, originalRel, nil
}

func (tx *operationalArchiveTransaction) pinParent(relativePath string, create bool) (*os.Root, error) {
	if root := tx.parentRoots[relativePath]; root != nil {
		return root, nil
	}
	displayPath := filepath.Join(tx.homePath, relativePath)
	var before []os.FileInfo
	var err error
	if create {
		existing, existingErr := lstatRootedDirectoryPath(tx.homeRoot, relativePath, displayPath, "operational archive transaction", true)
		if existingErr != nil {
			return nil, existingErr
		}
		before, err = ensureRootedDirectories(tx.homeRoot, relativePath, displayPath, "operational archive transaction", 0o755)
		if err == nil {
			err = syncOperationalArchiveCreatedParents(tx.homeRoot, relativePath, len(existing))
		}
	} else {
		before, err = lstatRootedDirectoryPath(tx.homeRoot, relativePath, displayPath, "operational archive transaction", false)
	}
	if err != nil {
		return nil, err
	}
	root, err := tx.homeRoot.OpenRoot(relativePath)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	after, err := lstatRootedDirectoryPath(tx.homeRoot, relativePath, displayPath, "operational archive transaction", false)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if !sameRootedPath(before, after) || !opened.IsDir() || !os.SameFile(after[len(after)-1], opened) {
		_ = root.Close()
		return nil, fmt.Errorf("operational archive transaction parent must remain stable beneath compound home: %s", displayPath)
	}
	tx.parentRoots[relativePath] = root
	tx.parentInfos[relativePath] = append([]os.FileInfo(nil), after...)
	return root, nil
}

func syncOperationalArchiveCreatedParents(homeRoot *os.Root, relativePath string, existingComponents int) error {
	components := strings.Split(relativePath, string(filepath.Separator))
	for index := existingComponents; index < len(components); index++ {
		parentPath := "."
		if index > 0 {
			parentPath = filepath.Join(components[:index]...)
		}
		if err := syncRootedDirectory(homeRoot, parentPath, relativePath, "operational archive transaction"); err != nil {
			return err
		}
	}
	return nil
}

func (tx *operationalArchiveTransaction) captureIndex(entries []OperationalArchiveEntry) error {
	data, mode, info, err := readOperationalArchiveRootFile(tx.indexRoot, operationalArchiveIndexName, "operational archive index")
	if errors.Is(err, os.ErrNotExist) {
		data = nil
		mode = 0o644
		info = nil
	} else if err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return fmt.Errorf("operational archive index must end with newline")
	}
	tx.indexBefore = append([]byte(nil), data...)
	tx.indexBeforeInfo = info
	tx.indexBeforeMode = mode
	tx.indexAfter = append([]byte(nil), data...)
	for _, entry := range entries {
		row, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		tx.indexAfter = append(tx.indexAfter, row...)
		tx.indexAfter = append(tx.indexAfter, '\n')
	}
	return nil
}

func readOperationalArchiveRootFile(root *os.Root, name, kind string) ([]byte, os.FileMode, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, 0, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, 0, nil, fmt.Errorf("%s file must be a regular file: %s", kind, name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, 0, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, 0, nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, 0, nil, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !opened.Mode().IsRegular() ||
		!os.SameFile(before, after) || !os.SameFile(after, opened) {
		return nil, 0, nil, fmt.Errorf("%s file must remain a stable regular file: %s", kind, name)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, nil, err
	}
	final, err := root.Lstat(name)
	if err != nil {
		return nil, 0, nil, err
	}
	if !os.SameFile(opened, final) {
		return nil, 0, nil, fmt.Errorf("%s file changed while it was read: %s", kind, name)
	}
	return data, opened.Mode().Perm(), opened, nil
}

func operationalArchiveBytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (tx *operationalArchiveTransaction) commit(entries []OperationalArchiveEntry) error {
	if err := tx.moveAll(); err != nil {
		return tx.rollbackAfter(err)
	}
	operationalArchiveBeforeIndexCommit(tx.operation, entries)
	if err := tx.validateCanonicalIndex(tx.indexBeforeInfo); err != nil {
		return tx.rollbackAfter(err)
	}
	if err := tx.publishIndex(); err != nil {
		if tx.indexPublication != nil && tx.indexPublication.State() == RootedFileCASAmbiguous {
			return fmt.Errorf("operational archive index publication requires manual repair: %w", err)
		}
		return tx.rollbackAfter(err)
	}
	if err := tx.validateCanonicalIndex(tx.indexPublishedInfo); err != nil {
		return tx.rollbackAfter(err)
	}
	if err := tx.commitIndexPublication(); err != nil {
		return fmt.Errorf("operational archive index publication requires manual repair: %w", err)
	}
	return nil
}

func (tx *operationalArchiveTransaction) moveAll() error {
	for i := range tx.moves {
		move := &tx.moves[i]
		if err := tx.homeRoot.Rename(move.sourceRel, move.destinationRel); err != nil {
			return fmt.Errorf("move operational archive file %s -> %s: %w", move.sourceRel, move.destinationRel, err)
		}
		move.moved = true
		data, _, _, err := readOperationalArchiveRootFile(move.destinationRoot, move.destinationName, "moved operational archive")
		if err != nil {
			return err
		}
		if int64(len(data)) != move.entry.Size || operationalArchiveBytesSHA256(data) != move.sourceSHA256 {
			return fmt.Errorf("moved operational archive file does not match source snapshot: %s", move.destinationRel)
		}
	}
	return syncOperationalArchiveRoots(tx.moveRoots()...)
}

func (tx *operationalArchiveTransaction) validateCanonicalIndex(expected os.FileInfo) error {
	displayPath := OperationalArchiveIndexPath(tx.homePath)
	parents, err := lstatRootedDirectoryPath(tx.homeRoot, tx.indexParentRel, displayPath, "operational archive index", false)
	if err != nil {
		return err
	}
	pinned, err := tx.indexRoot.Stat(".")
	if err != nil {
		return err
	}
	if !sameRootedPath(tx.indexParentInfos, parents) || !os.SameFile(parents[len(parents)-1], pinned) {
		return fmt.Errorf("operational archive index parent changed before commit: %s", displayPath)
	}
	info, err := tx.homeRoot.Lstat(filepath.FromSlash(OperationalArchiveIndexRel))
	if expected == nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("operational archive index appeared before commit: %s", displayPath)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return fmt.Errorf("operational archive index changed before commit: %s", displayPath)
	}
	return nil
}

func (tx *operationalArchiveTransaction) publishIndex() error {
	info, publication, err := atomicRewriteOperationalArchiveRootFile(
		tx.indexRoot,
		operationalArchiveIndexName,
		tx.indexAfter,
		tx.indexBeforeMode,
		"commit",
		tx.indexBefore,
		tx.indexBeforeInfo,
	)
	tx.indexPublication = publication
	tx.indexPublished = publication != nil && publication.State() == RootedFileCASPublished
	tx.indexPublishedInfo = info
	return err
}

func (tx *operationalArchiveTransaction) commitIndexPublication() error {
	if !tx.indexPublished || tx.indexPublication == nil {
		return fmt.Errorf("operational archive index publication is missing")
	}
	if err := tx.indexPublication.Commit(); err != nil {
		return err
	}
	tx.indexPublication = nil
	return nil
}

func (tx *operationalArchiveTransaction) restoreIndex() error {
	if !tx.indexPublished {
		return nil
	}
	if tx.indexPublication == nil {
		return fmt.Errorf("operational archive index publication cannot be rolled back")
	}
	if err := tx.indexPublication.Rollback(); err != nil {
		return err
	}
	tx.indexPublished = false
	tx.indexPublication = nil
	return nil
}

func (tx *operationalArchiveTransaction) rollbackAfter(cause error) error {
	indexErr := tx.restoreIndex()
	if indexErr != nil {
		return fmt.Errorf("operational archive transaction failed and index rollback is incomplete; moved files retained for manual repair: %w", errors.Join(cause, indexErr))
	}
	moveErr := tx.rollbackMoves()
	if moveErr != nil {
		return fmt.Errorf("operational archive transaction failed and rollback is incomplete: %w", errors.Join(cause, moveErr))
	}
	return cause
}

func (tx *operationalArchiveTransaction) rollbackMoves() error {
	var rollbackErrors []error
	for i := len(tx.moves) - 1; i >= 0; i-- {
		move := &tx.moves[i]
		if !move.moved {
			continue
		}
		if err := writeNewOperationalArchiveRootFile(move.sourceRoot, move.sourceName, move.sourceData, move.sourceMode); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore operational archive source %s: %w", move.sourceRel, err))
			continue
		}
		data, _, _, err := readOperationalArchiveRootFile(move.destinationRoot, move.destinationName, "operational archive rollback destination")
		if err != nil {
			rollbackErrors = append(rollbackErrors, err)
			continue
		}
		if int64(len(data)) != move.entry.Size || operationalArchiveBytesSHA256(data) != move.sourceSHA256 {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("refusing to remove changed operational archive rollback destination %s", move.destinationRel))
			continue
		}
		if err := move.destinationRoot.Remove(move.destinationName); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove operational archive rollback destination %s: %w", move.destinationRel, err))
			continue
		}
		move.moved = false
	}
	if err := syncOperationalArchiveRoots(tx.moveRoots()...); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	return errors.Join(rollbackErrors...)
}

func (tx *operationalArchiveTransaction) moveRoots() []*os.Root {
	roots := make([]*os.Root, 0, len(tx.moves)*2)
	for i := range tx.moves {
		roots = append(roots, tx.moves[i].sourceRoot, tx.moves[i].destinationRoot)
	}
	return roots
}

func (tx *operationalArchiveTransaction) close() {
	for path, root := range tx.parentRoots {
		_ = root.Close()
		delete(tx.parentRoots, path)
	}
	if tx.homeRoot != nil {
		_ = tx.homeRoot.Close()
		tx.homeRoot = nil
	}
}

func atomicRewriteOperationalArchiveRootFile(root *os.Root, name string, data []byte, mode os.FileMode, purpose string, expectedData []byte, expectedInfo os.FileInfo) (os.FileInfo, *RootedFileCAS, error) {
	digest := operationalArchiveBytesSHA256(data)
	tempName := fmt.Sprintf(".%s.%d.%s.%s.tmp", name, os.Getpid(), purpose, digest[:12])
	file, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return nil, nil, err
	}
	cleanup := func(cause error) (os.FileInfo, *RootedFileCAS, error) {
		_ = file.Close()
		_ = root.Remove(tempName)
		return nil, nil, cause
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
		_ = root.Remove(tempName)
		return nil, nil, err
	}
	tempData, _, tempInfo, err := readOperationalArchiveRootFile(root, tempName, "operational archive index replacement")
	if err != nil {
		return cleanup(err)
	}
	if !bytes.Equal(tempData, data) {
		return cleanup(fmt.Errorf("operational archive index replacement changed before publication: %s", name))
	}
	operationalArchiveBeforeIndexReplace(purpose)
	publication, err := BeginRootedFileCAS(root, name, tempName, expectedData, expectedInfo, data, tempInfo)
	if err != nil {
		if publication == nil || publication.State() == RootedFileCASNotPublished {
			_ = root.Remove(tempName)
		}
		return nil, publication, err
	}
	publishedData, _, info, err := readOperationalArchiveRootFile(root, name, "published operational archive index")
	if err != nil {
		rollbackErr := publication.Rollback()
		return nil, publication, errors.Join(err, rollbackErr)
	}
	if !bytes.Equal(publishedData, data) {
		rollbackErr := publication.Rollback()
		return info, publication, errors.Join(fmt.Errorf("published operational archive index does not match intended postimage: %s", name), rollbackErr)
	}
	return info, publication, nil
}

func writeNewOperationalArchiveRootFile(root *os.Root, name string, data []byte, mode os.FileMode) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	cleanup := func(cause error) error {
		_ = file.Close()
		_ = root.Remove(name)
		return cause
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
		_ = root.Remove(name)
		return err
	}
	return nil
}

func syncOperationalArchiveRoots(roots ...*os.Root) error {
	seen := map[*os.Root]bool{}
	var syncErrors []error
	for _, root := range roots {
		if root == nil || seen[root] {
			continue
		}
		seen[root] = true
		if err := syncOperationalArchiveRoot(root); err != nil {
			syncErrors = append(syncErrors, err)
		}
	}
	return errors.Join(syncErrors...)
}

func syncOperationalArchiveRoot(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	if info, err := dir.Stat(); err != nil {
		_ = dir.Close()
		return err
	} else if !info.IsDir() {
		_ = dir.Close()
		return fmt.Errorf("operational archive transaction sync target is not a directory")
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	// Some supported Darwin filesystems report EINVAL for directory fsync.
	// Matching the existing home durability seam keeps that platform usable;
	// on such filesystems crash durability remains best-effort, never claimed
	// as crash-atomic by this transaction.
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}

func writeHomeArchive(homePath, targetPath, kind string, excluded []string, now time.Time, include func(string, fs.FileInfo) bool) (ArchiveReport, error) {
	aw, err := beginArchivePublication(homePath, targetPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer aw.cleanup()
	gz := gzip.NewWriter(aw.file)
	tw := tar.NewWriter(gz)
	manifestPath := filepath.Join(homePath, ManifestName)
	pinnedManifestBytes, err := readManifestNoFollowBounded(manifestPath)
	if err != nil {
		return ArchiveReport{}, fmt.Errorf("pin home manifest for archive: %w", err)
	}
	pinnedManifest, err := decodeHomeManifest(pinnedManifestBytes)
	if err != nil {
		return ArchiveReport{}, err
	}
	if err := archiveAfterHomeManifestPinned(filepath.Join(homePath, ManifestName)); err != nil {
		return ArchiveReport{}, err
	}

	manifestEntry, err := writeBytesEntry(tw, ManifestName, pinnedManifestBytes, 0o644)
	if err != nil {
		return ArchiveReport{}, err
	}
	entries := []ArchiveEntry{manifestEntry}
	bytesWritten := manifestEntry.Size
	err = filepath.WalkDir(homePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == homePath {
			return nil
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ManifestName {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !include(rel, info) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return writeDirEntry(tw, rel, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		entry, err := writeFileEntry(tw, path, rel, info)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		bytesWritten += entry.Size
		return nil
	})
	if err != nil {
		return ArchiveReport{}, err
	}
	archiveManifest := ArchiveManifest{
		SchemaVersion:     ArchiveSchemaVersion,
		Kind:              kind,
		CreatedAt:         now.UTC().Format(time.RFC3339),
		SourceHome:        homePath,
		HomeSchemaVersion: pinnedManifest.SchemaVersion,
		ExcludedRoots:     append([]string(nil), excluded...),
		Entries:           sortedArchiveEntries(entries),
	}
	if err := writeArchiveManifest(tw, archiveManifest); err != nil {
		return ArchiveReport{}, err
	}
	if err := closeArchiveWriters(tw, gz); err != nil {
		return ArchiveReport{}, err
	}
	if err := aw.publish(); err != nil {
		return ArchiveReport{}, err
	}
	return ArchiveReport{
		Kind:        kind,
		ArchivePath: targetPath,
		Files:       len(entries),
		Bytes:       bytesWritten,
		Skipped:     archiveManifest.ExcludedRoots,
	}, nil
}

type archivePublication struct {
	targetPath string
	tempPath   string
	tempName   string
	targetName string
	parentRoot *os.Root
	file       *os.File
	published  bool
}

var (
	archiveFileSync               = func(f *os.File) error { return f.Sync() }
	archiveParentSync             = syncArchiveParentDir
	archiveBeforePublish          = func(string, string) {}
	archiveBeforeOpenExportParent = func(string) {}
)

func prepareArchiveTarget(homePath, targetPath string, include func(string, fs.FileInfo) bool) error {
	if targetPath == "" {
		return fmt.Errorf("archive target required")
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive target must not already exist as a symlink: %s", targetPath)
		}
		return fmt.Errorf("archive target already exists: %s", targetPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(targetPath)
	exports := filepath.Join(homePath, "exports")
	if parent == exports || isUnderPath(exports, parent) {
		if info, err := os.Lstat(exports); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("home exports directory must not be a symlink: %s", exports)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if isUnderPath(homePath, targetPath) {
		rel, err := filepath.Rel(homePath, targetPath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "exports/") || rel == "exports" {
			return nil
		}
		if entry, ok := HabitatForPath(rel); ok && entry.Backup {
			return fmt.Errorf("archive target is inside included home root %s: %s", entry.Prefix, targetPath)
		}
		if info, err := os.Stat(parent); err == nil && include(rel, info) {
			return fmt.Errorf("archive target overlaps included home snapshot: %s", targetPath)
		}
	}
	return nil
}

func beginArchivePublication(homePath, targetPath string) (*archivePublication, error) {
	if exportRel, ok, err := exportTargetParentRel(homePath, targetPath); err != nil {
		return nil, err
	} else if ok {
		return beginRootedExportArchivePublication(homePath, targetPath, exportRel)
	}
	parent := filepath.Dir(targetPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	if err := archiveParentSync(parent); err != nil {
		return nil, err
	}
	base := filepath.Base(targetPath)
	for i := 0; i < 16; i++ {
		tempPath := filepath.Join(parent, fmt.Sprintf(".%s.%d.%s.tmp", base, os.Getpid(), randomArchiveSuffix()))
		f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &archivePublication{targetPath: targetPath, tempPath: tempPath, targetName: base, file: f}, nil
	}
	return nil, fmt.Errorf("could not allocate archive temp file for %s", targetPath)
}

func exportTargetParentRel(homePath, targetPath string) (string, bool, error) {
	exports := filepath.Join(homePath, "exports")
	parent := filepath.Dir(targetPath)
	if parent != exports && !isUnderPath(exports, parent) {
		return "", false, nil
	}
	rel, err := filepath.Rel(homePath, parent)
	if err != nil {
		return "", false, err
	}
	return filepath.Clean(rel), true, nil
}

func beginRootedExportArchivePublication(homePath, targetPath, parentRel string) (*archivePublication, error) {
	homeRoot, err := os.OpenRoot(homePath)
	if err != nil {
		return nil, err
	}
	defer homeRoot.Close()
	displayPath := filepath.Dir(targetPath)
	infos, err := ensureRootedDirectories(homeRoot, parentRel, displayPath, "archive export target", 0o755)
	if err != nil {
		return nil, err
	}
	if err := syncRootedDirectory(homeRoot, parentRel, displayPath, "archive export target"); err != nil {
		return nil, err
	}
	archiveBeforeOpenExportParent(parentRel)
	parentRoot, err := homeRoot.OpenRoot(parentRel)
	if err != nil {
		return nil, err
	}
	opened, err := parentRoot.Stat(".")
	if err != nil {
		_ = parentRoot.Close()
		return nil, err
	}
	after, err := lstatRootedDirectoryPath(homeRoot, parentRel, displayPath, "archive export target", false)
	if err != nil {
		_ = parentRoot.Close()
		return nil, err
	}
	if !sameRootedPath(infos, after) || !opened.IsDir() || !os.SameFile(after[len(after)-1], opened) {
		_ = parentRoot.Close()
		return nil, fmt.Errorf("archive export target parent changed before publication: %s", displayPath)
	}
	base := filepath.Base(targetPath)
	if info, err := parentRoot.Lstat(base); err == nil {
		_ = parentRoot.Close()
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("archive target must not already exist as a symlink: %s", targetPath)
		}
		return nil, fmt.Errorf("archive target already exists: %s", targetPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = parentRoot.Close()
		return nil, err
	}
	for i := 0; i < 16; i++ {
		tempName := fmt.Sprintf(".%s.%d.%s.tmp", base, os.Getpid(), randomArchiveSuffix())
		f, err := parentRoot.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			_ = parentRoot.Close()
			return nil, err
		}
		return &archivePublication{targetPath: targetPath, tempPath: filepath.Join(displayPath, tempName), tempName: tempName, targetName: base, parentRoot: parentRoot, file: f}, nil
	}
	_ = parentRoot.Close()
	return nil, fmt.Errorf("could not allocate archive temp file for %s", targetPath)
}

func closeArchiveWriters(tw *tar.Writer, gz *gzip.Writer) error {
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return nil
}

func (aw *archivePublication) publish() error {
	if aw == nil || aw.file == nil {
		return fmt.Errorf("archive publication missing temp file")
	}
	if err := archiveFileSync(aw.file); err != nil {
		return err
	}
	if err := aw.file.Close(); err != nil {
		aw.file = nil
		return err
	}
	aw.file = nil
	archiveBeforePublish(aw.tempPath, aw.targetPath)
	if aw.parentRoot != nil {
		if err := aw.parentRoot.Link(aw.tempName, aw.targetName); err != nil {
			if _, statErr := aw.parentRoot.Lstat(aw.targetName); statErr == nil {
				return fmt.Errorf("archive publication ambiguous after target appeared; verify before trusting %s: %w", aw.targetPath, err)
			}
			return err
		}
	} else if err := os.Link(aw.tempPath, aw.targetPath); err != nil {
		if _, statErr := os.Lstat(aw.targetPath); statErr == nil {
			return fmt.Errorf("archive publication ambiguous after target appeared; verify before trusting %s: %w", aw.targetPath, err)
		}
		return err
	}
	aw.published = true
	if err := aw.syncParent(); err != nil {
		return fmt.Errorf("archive publication ambiguous after target link; verify before trusting %s: %w", aw.targetPath, err)
	}
	if aw.parentRoot != nil {
		if err := aw.parentRoot.Remove(aw.tempName); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := os.Remove(aw.tempPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := aw.syncParent(); err != nil {
		return fmt.Errorf("archive publication ambiguous after target cleanup; verify before trusting %s: %w", aw.targetPath, err)
	}
	return nil
}

func (aw *archivePublication) cleanup() {
	if aw == nil {
		return
	}
	if aw.file != nil {
		_ = aw.file.Close()
		aw.file = nil
	}
	if !aw.published && aw.tempPath != "" {
		if aw.parentRoot != nil && aw.tempName != "" {
			_ = aw.parentRoot.Remove(aw.tempName)
		} else {
			_ = os.Remove(aw.tempPath)
		}
	}
	if aw.parentRoot != nil {
		_ = aw.parentRoot.Close()
		aw.parentRoot = nil
	}
}

func (aw *archivePublication) syncParent() error {
	if aw.parentRoot != nil {
		return syncOperationalArchiveRoot(aw.parentRoot)
	}
	return archiveParentSync(filepath.Dir(aw.targetPath))
}

func syncArchiveParentDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}

func isUnderPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func includeBackupPath(rel string, info fs.FileInfo) bool {
	return habitatBackupIncluded(rel, info.IsDir())
}

func writeFileEntry(tw *tar.Writer, path, rel string, info fs.FileInfo) (ArchiveEntry, error) {
	if err := validateArchivePath(rel); err != nil {
		return ArchiveEntry{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ArchiveEntry{}, err
	}
	defer f.Close()
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return ArchiveEntry{}, err
	}
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return ArchiveEntry{}, err
	}
	sum := sha256.New()
	n, err := io.Copy(tw, io.TeeReader(f, sum))
	if err != nil {
		return ArchiveEntry{}, err
	}
	return ArchiveEntry{Path: rel, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

func writeBytesEntry(tw *tar.Writer, rel string, data []byte, mode int64) (ArchiveEntry, error) {
	if err := validateArchivePath(rel); err != nil {
		return ArchiveEntry{}, err
	}
	hdr := &tar.Header{Name: rel, Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := tw.WriteHeader(hdr); err != nil {
		return ArchiveEntry{}, err
	}
	if _, err := tw.Write(data); err != nil {
		return ArchiveEntry{}, err
	}
	sum := sha256.Sum256(data)
	return ArchiveEntry{Path: rel, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func writeDirEntry(tw *tar.Writer, rel string, mode fs.FileMode) error {
	if err := validateArchivePath(rel); err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return nil
	}
	return tw.WriteHeader(&tar.Header{Name: strings.TrimSuffix(rel, "/") + "/", Typeflag: tar.TypeDir, Mode: int64(mode.Perm()), ModTime: time.Unix(0, 0).UTC()})
}

func archiveHabitatDirMode(rel string) fs.FileMode {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "private" || strings.HasPrefix(rel, "private/") {
		return 0o700
	}
	return 0o755
}

func writeArchiveManifest(tw *tar.Writer, manifest ArchiveManifest) error {
	manifest.Entries = sortedArchiveEntries(manifest.Entries)
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	_, err = writeBytesEntry(tw, ArchiveManifestName, b, 0o644)
	return err
}

func readArchive(archivePath string, restore *archiveRestoreTarget) (ArchiveManifest, map[string]ArchiveEntry, error) {
	return readArchiveWithLimits(archivePath, restore, defaultArchiveReadLimits)
}

func readArchiveWithLimits(archivePath string, restore *archiveRestoreTarget, limits archiveReadLimits) (ArchiveManifest, map[string]ArchiveEntry, error) {
	archivePath, err := filepath.Abs(expandUser(archivePath))
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	if err := validateArchiveReadLimits(limits); err != nil {
		return ArchiveManifest{}, nil, err
	}
	f, err := openArchiveNoFollow(archivePath, limits.MaxSourceBytes)
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	defer f.Close()
	if restore != nil && restoreArchiveOpened != nil {
		if err := restoreArchiveOpened(archivePath); err != nil {
			return ArchiveManifest{}, nil, err
		}
	}
	return readOpenedArchiveWithLimits(archivePath, f, restore, limits, nil)
}

type verifiedBackupArchive struct {
	path            string
	file            *os.File
	limits          archiveReadLimits
	preflightDigest []byte
}

func openVerifiedBackupArchive(archivePath string, limits archiveReadLimits) (*verifiedBackupArchive, error) {
	archivePath, err := filepath.Abs(expandUser(archivePath))
	if err != nil {
		return nil, err
	}
	if err := validateArchiveReadLimits(limits); err != nil {
		return nil, err
	}
	f, err := openArchiveNoFollow(archivePath, limits.MaxSourceBytes)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*verifiedBackupArchive, error) {
		return nil, errors.Join(cause, f.Close())
	}
	if restoreArchiveOpened != nil {
		if err := restoreArchiveOpened(archivePath); err != nil {
			return fail(err)
		}
	}

	preflightDigest := sha256.New()
	manifest, actual, err := readOpenedArchiveWithLimits(archivePath, f, nil, limits, preflightDigest)
	if err != nil {
		return fail(err)
	}
	if manifest.Kind != ArchiveKindBackup {
		return fail(asArchiveCompatibilityError(fmt.Errorf("restore requires archive kind %s, got %q", ArchiveKindBackup, manifest.Kind)))
	}
	if _, err := verifyArchiveManifestWithLimits(archivePath, manifest, actual, limits); err != nil {
		return fail(err)
	}
	return &verifiedBackupArchive{path: archivePath, file: f, limits: limits, preflightDigest: preflightDigest.Sum(nil)}, nil
}

func (a *verifiedBackupArchive) extract(restore *archiveRestoreTarget) (ArchiveManifest, map[string]ArchiveEntry, error) {
	if a == nil || a.file == nil {
		return ArchiveManifest{}, nil, fmt.Errorf("verified backup archive is closed")
	}
	if _, err := a.file.Seek(0, io.SeekStart); err != nil {
		return ArchiveManifest{}, nil, fmt.Errorf("rewind verified archive source: %w", err)
	}
	restoreDigest := sha256.New()
	restoredManifest, restoredActual, err := readOpenedArchiveWithLimits(a.path, a.file, restore, a.limits, restoreDigest)
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	if !bytes.Equal(a.preflightDigest, restoreDigest.Sum(nil)) {
		return ArchiveManifest{}, nil, fmt.Errorf("archive source changed between preflight and restore extraction: %s", a.path)
	}
	return restoredManifest, restoredActual, nil
}

func (a *verifiedBackupArchive) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}

func readOpenedArchiveWithLimits(archivePath string, f *os.File, restore *archiveRestoreTarget, limits archiveReadLimits, digest io.Writer) (ArchiveManifest, map[string]ArchiveEntry, error) {
	var sourceReader io.Reader = f
	if digest != nil {
		sourceReader = io.TeeReader(f, digest)
	}
	source := newBoundedArchiveSource(sourceReader, limits.MaxSourceBytes)
	buffered := bufio.NewReader(source)
	gz, err := gzip.NewReader(buffered)
	if err != nil {
		if limitErr := source.limitError(archivePath); limitErr != nil {
			return ArchiveManifest{}, nil, limitErr
		}
		return ArchiveManifest{}, nil, fmt.Errorf("open gzip archive: %w", err)
	}
	gz.Multistream(false)
	defer gz.Close()
	decoded := newBoundedArchiveSource(gz, limits.MaxDecodedBytes)
	tr := tar.NewReader(decoded)
	actual := map[string]ArchiveEntry{}
	seen := map[string]struct{}{}
	directories := map[string]struct{}{}
	var manifest ArchiveManifest
	var embeddedHomeManifest []byte
	manifestSeen := false
	entryCount := 0
	var expandedBytes int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if limitErr := decoded.limitErrorFor("decoded archive", "tar stream"); limitErr != nil {
				return ArchiveManifest{}, nil, limitErr
			}
			if limitErr := source.limitError(archivePath); limitErr != nil {
				return ArchiveManifest{}, nil, limitErr
			}
			return ArchiveManifest{}, nil, err
		}
		name := filepath.ToSlash(hdr.Name)
		if err := validateArchivePath(name); err != nil {
			return ArchiveManifest{}, nil, err
		}
		entryCount++
		if entryCount > limits.MaxEntries {
			return ArchiveManifest{}, nil, fmt.Errorf("archive entry count exceeds %d", limits.MaxEntries)
		}
		canonicalName := strings.TrimSuffix(name, "/")
		if len(name) > limits.MaxPathBytes {
			return ArchiveManifest{}, nil, fmt.Errorf("archive path exceeds %d bytes: %s", limits.MaxPathBytes, name)
		}
		if depth := len(strings.Split(canonicalName, "/")); depth > limits.MaxPathDepth {
			return ArchiveManifest{}, nil, fmt.Errorf("archive path depth exceeds %d: %s", limits.MaxPathDepth, name)
		}
		if hdr.Size < 0 || hdr.Size > limits.MaxEntryBytes {
			return ArchiveManifest{}, nil, fmt.Errorf("archive entry %s exceeds %d bytes", name, limits.MaxEntryBytes)
		}
		if expandedBytes > limits.MaxExpandedBytes-hdr.Size {
			return ArchiveManifest{}, nil, fmt.Errorf("archive expanded bytes exceed %d", limits.MaxExpandedBytes)
		}
		expandedBytes += hdr.Size
		if _, exists := seen[canonicalName]; exists {
			return ArchiveManifest{}, nil, fmt.Errorf("archive contains duplicate entry %s", name)
		}
		if parent := filepath.ToSlash(filepath.Dir(canonicalName)); parent != "." {
			if _, exists := directories[parent]; !exists {
				return ArchiveManifest{}, nil, fmt.Errorf("archive directory parent must precede child %s", name)
			}
		}
		seen[canonicalName] = struct{}{}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if hdr.Size != 0 {
				return ArchiveManifest{}, nil, fmt.Errorf("archive directory entry %s declares %d bytes", name, hdr.Size)
			}
			if restore != nil {
				if err := restore.ensureDirectory(name, fs.FileMode(hdr.Mode)); err != nil {
					return ArchiveManifest{}, nil, err
				}
			}
			directories[canonicalName] = struct{}{}
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return ArchiveManifest{}, nil, fmt.Errorf("archive contains unsupported entry %s", name)
		}
		if name == ArchiveManifestName {
			if manifestSeen {
				return ArchiveManifest{}, nil, fmt.Errorf("archive contains duplicate manifest")
			}
			manifestSeen = true
			if hdr.Size > archiveManifestMaxBytes {
				return ArchiveManifest{}, nil, fmt.Errorf("archive manifest exceeds %d bytes", archiveManifestMaxBytes)
			}
			data, err := io.ReadAll(io.LimitReader(tr, archiveManifestMaxBytes+1))
			if err != nil {
				if limitErr := decoded.limitErrorFor("decoded archive", "tar stream"); limitErr != nil {
					return ArchiveManifest{}, nil, limitErr
				}
				if limitErr := source.limitError(archivePath); limitErr != nil {
					return ArchiveManifest{}, nil, limitErr
				}
				return ArchiveManifest{}, nil, err
			}
			if len(data) > archiveManifestMaxBytes {
				return ArchiveManifest{}, nil, fmt.Errorf("archive manifest exceeds %d bytes", archiveManifestMaxBytes)
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return ArchiveManifest{}, nil, fmt.Errorf("parse archive manifest: %w", err)
			}
			continue
		}
		sum := sha256.New()
		entryDigestWriter := io.Writer(sum)
		var homeManifestBytes bytes.Buffer
		if name == ManifestName {
			if hdr.Size > manifestMaxBytes {
				return ArchiveManifest{}, nil, fmt.Errorf("embedded home manifest exceeds %d bytes", manifestMaxBytes)
			}
			entryDigestWriter = io.MultiWriter(sum, &homeManifestBytes)
		}
		var n int64
		if restore != nil {
			out, err := restore.createFile(name, fs.FileMode(hdr.Mode))
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			n, err = io.CopyN(out.file, io.TeeReader(tr, entryDigestWriter), hdr.Size)
			if err != nil {
				if limitErr := decoded.limitErrorFor("decoded archive", "tar stream"); limitErr != nil {
					return ArchiveManifest{}, nil, out.abort(limitErr)
				}
				if limitErr := source.limitError(archivePath); limitErr != nil {
					return ArchiveManifest{}, nil, out.abort(limitErr)
				}
				return ArchiveManifest{}, nil, out.abort(err)
			}
			if err := out.commit(); err != nil {
				return ArchiveManifest{}, nil, err
			}
		} else {
			n, err = io.CopyN(entryDigestWriter, tr, hdr.Size)
			if err != nil {
				if limitErr := decoded.limitErrorFor("decoded archive", "tar stream"); limitErr != nil {
					return ArchiveManifest{}, nil, limitErr
				}
				if limitErr := source.limitError(archivePath); limitErr != nil {
					return ArchiveManifest{}, nil, limitErr
				}
				return ArchiveManifest{}, nil, err
			}
		}
		if name == ManifestName {
			embeddedHomeManifest = append([]byte(nil), homeManifestBytes.Bytes()...)
		}
		actual[name] = ArchiveEntry{Path: name, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	}
	if err := rejectTrailingDecodedArchiveData(decoded); err != nil {
		if limitErr := decoded.limitErrorFor("decoded archive", "tar stream"); limitErr != nil {
			return ArchiveManifest{}, nil, limitErr
		}
		if limitErr := source.limitError(archivePath); limitErr != nil {
			return ArchiveManifest{}, nil, limitErr
		}
		return ArchiveManifest{}, nil, err
	}
	if _, err := buffered.Peek(1); err == nil {
		return ArchiveManifest{}, nil, fmt.Errorf("archive contains trailing compressed data")
	} else if !errors.Is(err, io.EOF) {
		if limitErr := source.limitError(archivePath); limitErr != nil {
			return ArchiveManifest{}, nil, limitErr
		}
		return ArchiveManifest{}, nil, fmt.Errorf("inspect archive trailing data: %w", err)
	}
	if limitErr := source.limitError(archivePath); limitErr != nil {
		return ArchiveManifest{}, nil, limitErr
	}
	if limitErr := decoded.limitErrorFor("decoded archive", "tar stream"); limitErr != nil {
		return ArchiveManifest{}, nil, limitErr
	}
	if !manifestSeen {
		return ArchiveManifest{}, nil, fmt.Errorf("archive manifest is missing")
	}
	if manifest.SchemaVersion != ArchiveSchemaVersion {
		return ArchiveManifest{}, nil, asArchiveCompatibilityError(fmt.Errorf("archive manifest schema_version must be %s, got %q", ArchiveSchemaVersion, manifest.SchemaVersion))
	}
	manifest.embeddedHomeManifest = embeddedHomeManifest
	return manifest, actual, nil
}

type boundedArchiveSource struct {
	reader   io.Reader
	limit    int64
	consumed int64
	exceeded bool
}

func newBoundedArchiveSource(reader io.Reader, limit int64) *boundedArchiveSource {
	return &boundedArchiveSource{reader: reader, limit: limit}
}

func (r *boundedArchiveSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.consumed >= r.limit {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			r.exceeded = true
			return 0, errArchiveSourceReadLimit
		}
		return 0, err
	}
	remaining := r.limit - r.consumed
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.reader.Read(p)
	r.consumed += int64(n)
	return n, err
}

func (r *boundedArchiveSource) limitError(path string) error {
	return r.limitErrorFor("archive source", path)
}

func (r *boundedArchiveSource) limitErrorFor(kind, path string) error {
	if r.exceeded {
		return fmt.Errorf("%s exceeds %d bytes while reading: %s", kind, r.limit, path)
	}
	return nil
}

var errArchiveSourceReadLimit = errors.New("archive source read limit exceeded")

func rejectTrailingDecodedArchiveData(reader io.Reader) error {
	trailingDecoded, err := io.CopyN(io.Discard, reader, 1)
	if trailingDecoded != 0 {
		return fmt.Errorf("archive contains trailing decoded data")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("verify gzip archive footer: %w", err)
	}
	return nil
}

func validateArchiveReadLimits(limits archiveReadLimits) error {
	if limits.MaxSourceBytes < 1 || limits.MaxEntries < 1 || limits.MaxPathBytes < 1 || limits.MaxPathDepth < 1 || limits.MaxEntryBytes < 1 || limits.MaxExpandedBytes < 1 || limits.MaxDecodedBytes < 1 {
		return fmt.Errorf("archive read limits must all be positive")
	}
	if limits.MaxEntryBytes > limits.MaxExpandedBytes {
		return fmt.Errorf("archive max entry bytes cannot exceed max expanded bytes")
	}
	if limits.MaxDecodedBytes < limits.MaxExpandedBytes {
		return fmt.Errorf("archive max decoded bytes cannot be less than max expanded bytes")
	}
	return nil
}

func normalizeArchiveInputs(homePath, targetPath, kind string, now time.Time) (string, string, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(targetPath) == "" {
		targetPath = defaultArchivePath(homePath, kind, now)
	}
	targetPath, err = filepath.Abs(expandUser(targetPath))
	if err != nil {
		return "", "", err
	}
	return homePath, targetPath, nil
}

func defaultArchivePath(homePath, kind string, now time.Time) string {
	prefix := "compound-home"
	if kind == ArchiveKindPublicFixture {
		prefix = "compound-home-public-fixture"
	}
	return filepath.Join(homePath, "exports", fmt.Sprintf("%s-%s-%09d-%s.tar.gz", prefix, now.UTC().Format("20060102T150405Z"), now.UTC().Nanosecond(), randomArchiveSuffix()))
}

func randomArchiveSuffix() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("pid%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

func requireHealthyHome(homePath string) error {
	r := Doctor(homePath)
	if !r.OK {
		if !r.HabitatOK {
			parts := append([]string{}, r.Habitat.UnknownRoots...)
			parts = append(parts, r.Habitat.UnknownEntries...)
			return fmt.Errorf("home doctor failed for %s: unknown habitat entries: %s", homePath, strings.Join(parts, ","))
		}
		return fmt.Errorf("home doctor failed for %s", homePath)
	}
	return nil
}

func validateArchivePath(name string) error {
	name = filepath.ToSlash(name)
	if name == "" || strings.TrimSpace(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid archive path %q", name)
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] == 0x7f {
			return fmt.Errorf("invalid archive path %q", name)
		}
	}
	base := strings.TrimSuffix(name, "/")
	clean := filepath.ToSlash(filepath.Clean(base))
	if clean == "." || clean != base {
		return fmt.Errorf("invalid archive path %q", name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid archive path %q", name)
		}
	}
	return nil
}

func terminalWorkStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// terminalReviewStatus deliberately re-lists what reviewartifact.TerminalStatus
// owns. It cannot call the owner: reviewartifact imports internal/home (see
// reviewartifact/queue.go), so depending upward would be an import cycle.
// Keep this set in sync with reviewartifact.terminalStatuses — "completed" was
// missing from every copy of this literal until 2026-08-18, which made archive
// skip finished review artifacts.
func terminalReviewStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "ok", "applied", "resolved", "accepted", "recorded", "candidate_recorded", "completed":
		return true
	default:
		return false
	}
}

func operationalCoreReviewJSON(path string) bool {
	name := filepath.Base(path)
	for _, suffix := range []string{".apply.json", ".decision.json", ".reviewed.json"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

func hasTerminalReviewSidecar(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	dir := filepath.Dir(path)
	for _, suffix := range []string{".reviewed.json", ".apply.json", ".decision.json"} {
		if _, err := os.Stat(filepath.Join(dir, stem+suffix)); err == nil {
			return true
		}
	}
	// A producer that joined a plan id with an identical review id recorded its
	// disposition at a doubled `<stem>.<stem>.reviewed.json` name. Those files
	// are immutable evidence, so the legacy stem stays selectable here and the
	// existing backlog drains without a migration pass.
	if _, err := os.Stat(filepath.Join(dir, stem+"."+stem+".reviewed.json")); err == nil {
		return true
	}
	return false
}

func operatorPendingReviewDecision(path string) (bool, error) {
	var payload map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, err
	}
	if operatorPendingDecision(jsonMapString(payload, "status")) ||
		operatorPendingDecision(jsonMapString(payload, "verdict")) ||
		operatorPendingDecision(jsonMapString(payload, "result")) {
		return true, nil
	}
	synthesis, ok := payload["synthesis"].(map[string]any)
	if !ok {
		return false, nil
	}
	return operatorPendingDecision(jsonMapString(synthesis, "decision")), nil
}

func operatorPendingDecision(decision string) bool {
	switch strings.TrimSpace(decision) {
	case "needs_human", "needs_trial":
		return true
	default:
		return false
	}
}

func hasOperatorDispositionSidecar(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	sidecarPath := filepath.Join(filepath.Dir(path), stem+".reviewed.json")
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		return false
	}
	var sidecar map[string]any
	if err := json.Unmarshal(data, &sidecar); err != nil {
		return false
	}
	return strings.TrimSpace(jsonMapString(sidecar, "reviewed_by")) != ""
}

func operationalReviewSiblingPaths(homePath, corePath string) ([]string, error) {
	stem := strings.TrimSuffix(filepath.Base(corePath), ".json")
	dir := filepath.Dir(corePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, stem+".") {
			continue
		}
		path := filepath.Join(dir, name)
		if path == corePath {
			continue
		}
		rel, err := filepath.Rel(homePath, path)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}

func jsonStringField(path, key string) (string, error) {
	return jsonFirstStringField(path, key)
}

func jsonFirstStringField(path string, keys ...string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", nil
}

func jsonMapString(payload map[string]any, key string) string {
	if value, ok := payload[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func operationalArchiveID(originalPath string) string {
	sum := sha256.Sum256([]byte(filepath.ToSlash(originalPath)))
	return "oparch_" + hex.EncodeToString(sum[:])[:12]
}

func sortedArchiveEntries(entries []ArchiveEntry) []ArchiveEntry {
	out := append([]ArchiveEntry(nil), entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func measureTree(root string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	return files, bytes, err
}

func isUnderHome(homePath, path string) bool {
	homePath = filepath.Clean(homePath)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(homePath, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func expandUser(path string) string {
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
