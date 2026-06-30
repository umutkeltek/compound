package home

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var archiveTestNow = time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

func TestInitAndDoctor(t *testing.T) {
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "local", time.Unix(0, 0)); err != nil {
		t.Fatalf("init: %v", err)
	}
	// idempotent: a second init must not error or destroy state.
	if err := Init(h, "local", time.Unix(0, 0)); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	r := Doctor(h)
	if !r.ManifestOK {
		t.Fatal("manifest not ok")
	}
	if r.DirsOK != len(Dirs) {
		for _, d := range r.Dirs {
			if !d.OK {
				t.Errorf("missing dir: %s", d.Name)
			}
		}
		t.Fatalf("dirs %d/%d ok", r.DirsOK, len(Dirs))
	}
	if !r.OK {
		t.Fatalf("doctor not ok: %+v", r)
	}
	if r.Manifest.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %q, want %q", r.Manifest.SchemaVersion, SchemaVersion)
	}
	// the durable ledger dir must exist under the home, not the repo.
	if _, err := os.Stat(filepath.Join(h, "ledger")); err != nil {
		t.Fatalf("ledger dir missing: %v", err)
	}
}

func TestDoctorMissingIsNotOK(t *testing.T) {
	h := t.TempDir() // empty: no manifest, no dirs
	r := Doctor(h)
	if r.OK {
		t.Fatal("empty dir reported OK")
	}
	if r.ManifestOK {
		t.Fatal("missing manifest reported OK")
	}
}

func TestResolvePrecedence(t *testing.T) {
	env := t.TempDir()
	t.Setenv("COMPOUND_HOME", env)
	got, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got != env {
		t.Fatalf("env home = %q, want %q", got, env)
	}
	flag := t.TempDir()
	if got, _ = Resolve(flag); got != flag {
		t.Fatalf("--home = %q, want %q (flag must beat env)", got, flag)
	}
}

func TestBackupVerifyRestoreRoundTrip(t *testing.T) {
	now := archiveTestNow
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "local", now); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, h, "ledger/claims.json", `{"claim":"durable"}`)
	writeHomeFile(t, h, "events/receipt.json", `{"receipt":"durable"}`)
	writeHomeFile(t, h, "tmp/cache.txt", "drop")
	writeHomeFile(t, h, "indexes/cache.db", "drop")
	writeHomeFile(t, h, "projections/view.json", "drop")

	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	report, err := Backup(h, archivePath, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != ArchiveKindBackup || report.Files == 0 {
		t.Fatalf("backup report = %+v", report)
	}
	if !slices.Contains(report.Skipped, "tmp") || !slices.Contains(report.Skipped, "indexes") {
		t.Fatalf("backup skipped roots = %v", report.Skipped)
	}
	verify, err := VerifyArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !verify.OK || verify.Kind != ArchiveKindBackup {
		t.Fatalf("verify = %+v", verify)
	}

	restored := filepath.Join(t.TempDir(), ".compound-restored")
	restore, err := RestoreArchive(archivePath, restored, now)
	if err != nil {
		t.Fatal(err)
	}
	if restore.Kind != ArchiveKindBackup || !Doctor(restored).OK {
		t.Fatalf("restore = %+v doctor=%+v", restore, Doctor(restored))
	}
	if got := mustReadHomeFile(t, restored, "ledger/claims.json"); got != `{"claim":"durable"}` {
		t.Fatalf("restored ledger = %q", got)
	}
	assertMissing(t, filepath.Join(restored, "tmp/cache.txt"))
	assertMissing(t, filepath.Join(restored, "indexes/cache.db"))
	assertMissing(t, filepath.Join(restored, "projections/view.json"))
}

func TestExportPublicFixtureOmitsRuntimeData(t *testing.T) {
	now := archiveTestNow
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "global", now); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, h, "events/private.json", `{"private":true}`)
	writeHomeFile(t, h, "ledger/private.json", `{"private":true}`)
	writeHomeFile(t, h, "context/packs/private.json", `{"private":true}`)

	archivePath := filepath.Join(t.TempDir(), "fixture.tar.gz")
	report, err := ExportPublicFixture(h, archivePath, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != ArchiveKindPublicFixture || report.Files != 2 {
		t.Fatalf("fixture report = %+v", report)
	}
	verify, err := VerifyArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !verify.OK || verify.Kind != ArchiveKindPublicFixture {
		t.Fatalf("verify = %+v", verify)
	}
	names := archiveNames(t, archivePath)
	for _, want := range []string{ManifestName, PublicFixtureSummaryName, ArchiveManifestName} {
		if !slices.Contains(names, want) {
			t.Fatalf("fixture archive missing %s in %v", want, names)
		}
	}
	for _, name := range names {
		if strings.Contains(name, "private.json") {
			t.Fatalf("public fixture leaked private runtime entry %s", name)
		}
	}
	restored := filepath.Join(t.TempDir(), ".compound-fixture")
	if _, err := RestoreArchive(archivePath, restored, now); err != nil {
		t.Fatal(err)
	}
	if !Doctor(restored).OK {
		t.Fatalf("restored fixture doctor = %+v", Doctor(restored))
	}
	assertMissing(t, filepath.Join(restored, "events/private.json"))
	assertMissing(t, filepath.Join(restored, "ledger/private.json"))
}

func TestCompactClearsTransientAndOptionalRebuildableDirs(t *testing.T) {
	now := archiveTestNow
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "global", now); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, h, "ledger/keep.json", "keep")
	writeHomeFile(t, h, "tmp/cache.txt", "drop")
	writeHomeFile(t, h, "lock/lease", "drop")
	writeHomeFile(t, h, "indexes/cache.db", "drop")
	writeHomeFile(t, h, "projections/view.json", "drop")

	report, err := Compact(h, CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedFiles != 2 || report.RemovedBytes == 0 {
		t.Fatalf("compact report = %+v", report)
	}
	assertMissing(t, filepath.Join(h, "tmp/cache.txt"))
	assertMissing(t, filepath.Join(h, "lock/lease"))
	if got := mustReadHomeFile(t, h, "ledger/keep.json"); got != "keep" {
		t.Fatalf("ledger changed: %q", got)
	}
	if got := mustReadHomeFile(t, h, "indexes/cache.db"); got != "drop" {
		t.Fatalf("indexes should remain without rebuildable compact: %q", got)
	}

	report, err = Compact(h, CompactOptions{Rebuildable: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedFiles != 2 {
		t.Fatalf("rebuildable compact report = %+v", report)
	}
	assertMissing(t, filepath.Join(h, "indexes/cache.db"))
	assertMissing(t, filepath.Join(h, "projections/view.json"))
	for _, dir := range []string{"tmp", "lock", "indexes", "projections"} {
		if fi, err := os.Stat(filepath.Join(h, dir)); err != nil || !fi.IsDir() {
			t.Fatalf("compacted dir %s not recreated: %v", dir, err)
		}
	}
}

func writeHomeFile(t *testing.T, homePath, rel, content string) {
	t.Helper()
	path := filepath.Join(homePath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadHomeFile(t *testing.T, homePath, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(homePath, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or returned unexpected error: %v", path, err)
	}
}

func archiveNames(t *testing.T, archivePath string) []string {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := []string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if _, err := io.Copy(io.Discard, tr); err != nil {
			t.Fatal(err)
		}
	}
	return names
}
