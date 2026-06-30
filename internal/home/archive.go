package home

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ArchiveSchemaVersion     = "compound.home_archive.v0.1"
	ArchiveManifestName      = "compound-home-archive.json"
	PublicFixtureSummaryName = "compound-home-public-fixture.json"

	ArchiveKindBackup        = "backup"
	ArchiveKindPublicFixture = "public_fixture"
)

var backupExcludedRoots = []string{"exports", "indexes", "lock", "projections", "tmp"}

// ArchiveEntry records one checksummed file inside a home archive.
type ArchiveEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ArchiveManifest is stored inside every home archive.
type ArchiveManifest struct {
	SchemaVersion     string         `json:"schema_version"`
	Kind              string         `json:"kind"`
	CreatedAt         string         `json:"created_at"`
	SourceHome        string         `json:"source_home,omitempty"`
	HomeSchemaVersion string         `json:"home_schema_version,omitempty"`
	ExcludedRoots     []string       `json:"excluded_roots,omitempty"`
	Entries           []ArchiveEntry `json:"entries"`
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

// Backup writes a durable COMPOUND_HOME archive. Transient and rebuildable roots
// are excluded so backup size tracks owned evidence, not cache mass.
func Backup(homePath, targetPath string, now time.Time) (ArchiveReport, error) {
	homePath, targetPath, err := normalizeArchiveInputs(homePath, targetPath, ArchiveKindBackup, now)
	if err != nil {
		return ArchiveReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return ArchiveReport{}, err
	}
	report, err := writeHomeArchive(homePath, targetPath, ArchiveKindBackup, backupExcludedRoots, now, includeBackupPath)
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
	if err := requireHealthyHome(homePath); err != nil {
		return ArchiveReport{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ArchiveReport{}, err
	}
	f, err := os.Create(targetPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	entries := []ArchiveEntry{}
	var bytesWritten int64
	for _, dir := range Dirs {
		if err := writeDirEntry(tw, filepath.ToSlash(dir)); err != nil {
			return ArchiveReport{}, err
		}
	}
	fixtureManifest := Manifest{
		SchemaVersion: SchemaVersion,
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
		"dirs":              Dirs,
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
		SourceHome:        homePath,
		HomeSchemaVersion: SchemaVersion,
		ExcludedRoots:     []string{"all runtime files"},
		Entries:           sortedArchiveEntries(entries),
	}
	if err := writeArchiveManifest(tw, archiveManifest); err != nil {
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
	manifest, actual, err := readArchive(archivePath, false, "")
	if err != nil {
		return VerifyReport{}, err
	}
	expected := map[string]ArchiveEntry{}
	for _, entry := range manifest.Entries {
		expected[entry.Path] = entry
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
		bytesTotal += got.Size
	}
	return VerifyReport{
		Kind:        manifest.Kind,
		ArchivePath: archivePath,
		Files:       len(actual),
		Bytes:       bytesTotal,
		OK:          true,
	}, nil
}

// RestoreArchive extracts a verified archive into an empty target home.
func RestoreArchive(archivePath, targetHome string, now time.Time) (RestoreReport, error) {
	if strings.TrimSpace(targetHome) == "" {
		return RestoreReport{}, fmt.Errorf("target home required")
	}
	targetHome, err := filepath.Abs(expandUser(targetHome))
	if err != nil {
		return RestoreReport{}, err
	}
	if ok, err := dirEmptyOrMissing(targetHome); err != nil {
		return RestoreReport{}, err
	} else if !ok {
		return RestoreReport{}, fmt.Errorf("target home %s is not empty", targetHome)
	}
	verify, err := VerifyArchive(archivePath)
	if err != nil {
		return RestoreReport{}, err
	}
	if _, _, err := readArchive(archivePath, true, targetHome); err != nil {
		return RestoreReport{}, err
	}
	if err := Init(targetHome, "restored", now); err != nil {
		return RestoreReport{}, err
	}
	if !Doctor(targetHome).OK {
		return RestoreReport{}, fmt.Errorf("restored home failed doctor")
	}
	return RestoreReport{
		ArchivePath: archivePath,
		TargetHome:  targetHome,
		Kind:        verify.Kind,
		Files:       verify.Files,
		Bytes:       verify.Bytes,
	}, nil
}

// Compact clears owned transient dirs. With Rebuildable it also clears cache-like
// indexes/projections that can be regenerated from ledger/events.
func Compact(homePath string, opts CompactOptions) (CompactReport, error) {
	homePath, err := filepath.Abs(expandUser(homePath))
	if err != nil {
		return CompactReport{}, err
	}
	if err := requireHealthyHome(homePath); err != nil {
		return CompactReport{}, err
	}
	dirs := []string{"lock", "tmp"}
	if opts.Rebuildable {
		dirs = append(dirs, "indexes", "projections")
	}
	report := CompactReport{Home: homePath, DryRun: opts.DryRun, ClearedDirs: append([]string(nil), dirs...)}
	for _, dir := range dirs {
		full := filepath.Join(homePath, dir)
		if !isUnderHome(homePath, full) {
			return CompactReport{}, fmt.Errorf("refusing compact path outside home: %s", full)
		}
		files, bytes, err := measureTree(full)
		if err != nil {
			return CompactReport{}, err
		}
		report.RemovedFiles += files
		report.RemovedBytes += bytes
		if opts.DryRun {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return CompactReport{}, err
		}
		if err := os.MkdirAll(full, 0o755); err != nil {
			return CompactReport{}, err
		}
	}
	return report, nil
}

func writeHomeArchive(homePath, targetPath, kind string, excluded []string, now time.Time, include func(string, fs.FileInfo) bool) (ArchiveReport, error) {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ArchiveReport{}, err
	}
	f, err := os.Create(targetPath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	entries := []ArchiveEntry{}
	var bytesWritten int64
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
			return writeDirEntry(tw, rel)
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
		HomeSchemaVersion: SchemaVersion,
		ExcludedRoots:     append([]string(nil), excluded...),
		Entries:           sortedArchiveEntries(entries),
	}
	if err := writeArchiveManifest(tw, archiveManifest); err != nil {
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

func includeBackupPath(rel string, _ fs.FileInfo) bool {
	top := strings.Split(rel, "/")[0]
	for _, excluded := range backupExcludedRoots {
		if top == excluded {
			return false
		}
	}
	return true
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

func writeDirEntry(tw *tar.Writer, rel string) error {
	if err := validateArchivePath(rel); err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return nil
	}
	return tw.WriteHeader(&tar.Header{Name: strings.TrimSuffix(rel, "/") + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: time.Unix(0, 0).UTC()})
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

func readArchive(archivePath string, restore bool, targetHome string) (ArchiveManifest, map[string]ArchiveEntry, error) {
	archivePath, err := filepath.Abs(expandUser(archivePath))
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return ArchiveManifest{}, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return ArchiveManifest{}, nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	actual := map[string]ArchiveEntry{}
	var manifest ArchiveManifest
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ArchiveManifest{}, nil, err
		}
		name := filepath.ToSlash(hdr.Name)
		if err := validateArchivePath(name); err != nil {
			return ArchiveManifest{}, nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if restore {
				if err := os.MkdirAll(filepath.Join(targetHome, filepath.FromSlash(name)), fs.FileMode(hdr.Mode)); err != nil {
					return ArchiveManifest{}, nil, err
				}
			}
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return ArchiveManifest{}, nil, fmt.Errorf("archive contains unsupported entry %s", name)
		}
		if name == ArchiveManifestName {
			data, err := io.ReadAll(tr)
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return ArchiveManifest{}, nil, fmt.Errorf("parse archive manifest: %w", err)
			}
			continue
		}
		sum := sha256.New()
		var n int64
		if restore {
			target := filepath.Join(targetHome, filepath.FromSlash(name))
			if !isUnderHome(targetHome, target) {
				return ArchiveManifest{}, nil, fmt.Errorf("archive entry escapes target: %s", name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return ArchiveManifest{}, nil, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(hdr.Mode))
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			n, err = io.Copy(out, io.TeeReader(tr, sum))
			closeErr := out.Close()
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
			if closeErr != nil {
				return ArchiveManifest{}, nil, closeErr
			}
		} else {
			n, err = io.Copy(sum, tr)
			if err != nil {
				return ArchiveManifest{}, nil, err
			}
		}
		actual[name] = ArchiveEntry{Path: name, Size: n, SHA256: hex.EncodeToString(sum.Sum(nil))}
	}
	if manifest.SchemaVersion != ArchiveSchemaVersion {
		return ArchiveManifest{}, nil, fmt.Errorf("archive manifest schema_version must be %s, got %q", ArchiveSchemaVersion, manifest.SchemaVersion)
	}
	return manifest, actual, nil
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
	return filepath.Join(homePath, "exports", fmt.Sprintf("%s-%s.tar.gz", prefix, now.UTC().Format("20060102T150405Z")))
}

func requireHealthyHome(homePath string) error {
	r := Doctor(homePath)
	if !r.OK {
		return fmt.Errorf("home doctor failed for %s", homePath)
	}
	return nil
}

func validateArchivePath(name string) error {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid archive path %q", name)
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean != name && !strings.HasSuffix(name, "/") {
		return fmt.Errorf("invalid archive path %q", name)
	}
	for _, part := range strings.Split(strings.TrimSuffix(clean, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid archive path %q", name)
		}
	}
	return nil
}

func sortedArchiveEntries(entries []ArchiveEntry) []ArchiveEntry {
	out := append([]ArchiveEntry(nil), entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func dirEmptyOrMissing(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
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
