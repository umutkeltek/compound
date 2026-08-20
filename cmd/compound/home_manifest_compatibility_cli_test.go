package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

const homeManifestCompatibilityChildEnv = "COMPOUND_TEST_HOME_MANIFEST_COMPATIBILITY_CHILD"

const (
	homeManifestCompatibilityHomeEnv    = "COMPOUND_TEST_HOME_MANIFEST_COMPATIBILITY_HOME"
	homeManifestCompatibilityArchiveEnv = "COMPOUND_TEST_HOME_MANIFEST_COMPATIBILITY_ARCHIVE"
	homeManifestCompatibilityTargetEnv  = "COMPOUND_TEST_HOME_MANIFEST_COMPATIBILITY_TARGET"
)

func TestHomeDoctorSurfacesManifestFailureAndBackupFirstRecovery(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "1" {
		cmdHome([]string{"doctor"})
		return
	}

	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := os.Mkdir(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, home.ManifestName), []byte("schema_version: compound.home.v9.9\nhome_kind: global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := buildHomeDoctorReport(home.Doctor(homePath))
	if report.ManifestOK || report.Verdict != "fail" || !strings.Contains(report.ManifestError, "unsupported home schema") {
		t.Fatalf("doctor JSON report = %+v", report)
	}
	if !strings.Contains(report.ManifestRecovery, "backup") || !strings.Contains(report.ManifestRecovery, "restore") {
		t.Fatalf("doctor JSON recovery = %q", report.ManifestRecovery)
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHomeDoctorSurfacesManifestFailureAndBackupFirstRecovery$")
	cmd.Env = append(os.Environ(), homeManifestCompatibilityChildEnv+"=1", "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("home doctor exit = %v, want 1; output:\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"manifest_error: unsupported home schema", "manifest_recovery:", "backup", "restore", "will not rewrite"} {
		if !strings.Contains(text, want) {
			t.Fatalf("home doctor output missing %q:\n%s", want, text)
		}
	}
}

func TestDispatchCompatibilityGateBlocksFindingRecordBeforeAnyWrite(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "finding-record" {
		os.Args = []string{"compound", "finding", "record", "--topic", "compatibility-gate", "--body", "must not be written"}
		main()
		return
	}
	homePath := incompatibleCanonicalHome(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchCompatibilityGateBlocksFindingRecordBeforeAnyWrite$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=finding-record",
		"COMPOUND_HOME="+homePath,
	)
	out, err := cmd.CombinedOutput()
	assertHomeManifestCompatibilityCLIError(t, err, out, "manifest kind")
	if _, statErr := os.Lstat(filepath.Join(homePath, "observed")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("finding record bypass mutated incompatible home: %v", statErr)
	}
}

func TestDispatchCompatibilityGateBlocksPrivateCustodyRepairBeforeChmod(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "private-custody-repair" {
		os.Args = []string{"compound", "home", "private-custody", "repair"}
		main()
		return
	}
	homePath := incompatibleCanonicalHome(t)
	if err := os.Chmod(homePath, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchCompatibilityGateBlocksPrivateCustodyRepairBeforeChmod$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=private-custody-repair",
		"COMPOUND_HOME="+homePath,
	)
	out, err := cmd.CombinedOutput()
	assertHomeManifestCompatibilityCLIError(t, err, out, "manifest kind")
	info, statErr := os.Lstat(homePath)
	if statErr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("private-custody repair bypass changed incompatible home mode: info=%v err=%v", info, statErr)
	}
}

func TestDispatchCompatibilityGateBlocksMutationWhenManifestIsMissing(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "missing-manifest" {
		os.Args = []string{"compound", "finding", "record", "--topic", "compatibility-gate", "--body", "must not be written"}
		main()
		return
	}
	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := os.Mkdir(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(homePath, "operator-data")
	if err := os.WriteFile(markerPath, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchCompatibilityGateBlocksMutationWhenManifestIsMissing$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=missing-manifest",
		"COMPOUND_HOME="+homePath,
	)
	out, err := cmd.CombinedOutput()
	assertHomeManifestCompatibilityCLIError(t, err, out, "manifest is missing")
	if _, statErr := os.Lstat(filepath.Join(homePath, "observed")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing-manifest gate allowed mutation: %v", statErr)
	}
	if got, readErr := os.ReadFile(markerPath); readErr != nil || string(got) != "preserve" {
		t.Fatalf("missing-manifest gate changed existing data: body=%q err=%v", got, readErr)
	}
}

func TestDispatchCompatibilityGatePreservesReadOnlyHomeWhere(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "home-where" {
		os.Args = []string{"compound", "home", "where"}
		main()
		return
	}
	homePath := incompatibleCanonicalHome(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchCompatibilityGatePreservesReadOnlyHomeWhere$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=home-where",
		"COMPOUND_HOME="+homePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), homePath+"\n") {
		t.Fatalf("home where lost read-only recovery access: err=%v output=%q", err, out)
	}
}

func TestDispatchCompatibilityGateUsesLocalHomeForLocalMutation(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "local-private-custody" {
		os.Args = []string{"compound", "home", "--local", "private-custody", "repair"}
		main()
		return
	}
	ambientHome := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(ambientHome, "ambient-profile", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	localHome := filepath.Join(workspace, ".compound")
	if err := os.Mkdir(localHome, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("schema_version: " + home.SchemaVersion + "\nkind: compound_server\nhome_kind: local-profile\n")
	if err := os.WriteFile(filepath.Join(localHome, home.ManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchCompatibilityGateUsesLocalHomeForLocalMutation$")
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=local-private-custody",
		"COMPOUND_HOME="+ambientHome,
	)
	out, err := cmd.CombinedOutput()
	assertHomeManifestCompatibilityCLIError(t, err, out, "manifest kind")
	info, statErr := os.Lstat(localHome)
	if statErr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("local compatibility failure changed home mode: info=%v err=%v", info, statErr)
	}
}

func incompatibleCanonicalHome(t *testing.T) string {
	t.Helper()
	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := os.Mkdir(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("schema_version: " + home.SchemaVersion + "\nkind: compound_server\nhome_kind: global\n")
	if err := os.WriteFile(filepath.Join(homePath, home.ManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return homePath
}

func TestHomeInitCLIManifestIncompatibility(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "init" {
		cmdHome([]string{"init", "--home", os.Getenv(homeManifestCompatibilityHomeEnv), "--local"})
		return
	}

	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := os.Mkdir(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(homePath, home.ManifestName)
	before := []byte("schema_version: compound.home.v9.9\nhome_kind: local\n")
	if err := os.WriteFile(manifestPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHomeInitCLIManifestIncompatibility$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=init",
		homeManifestCompatibilityHomeEnv+"="+homePath,
	)
	out, err := cmd.CombinedOutput()
	assertHomeManifestCompatibilityCLIError(t, err, out, "unsupported home schema")

	after, readErr := os.ReadFile(manifestPath)
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("home init CLI mutated manifest: body=%q err=%v", after, readErr)
	}
	for _, rel := range home.HabitatDirs() {
		if _, statErr := os.Lstat(filepath.Join(homePath, filepath.FromSlash(rel))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("home init CLI created habitat %s: %v", rel, statErr)
		}
	}
}

func TestHomeRestoreCLIManifestIncompatibility(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "restore" {
		cmdHome([]string{
			"restore",
			os.Getenv(homeManifestCompatibilityArchiveEnv),
			"--target",
			os.Getenv(homeManifestCompatibilityTargetEnv),
		})
		return
	}

	manifestBody := []byte("schema_version: " + home.SchemaVersion + "\nhome_kind: local\nunknown_identity: true\n")
	archivePath := writeManifestCompatibilityCLIArchive(t, manifestBody)
	target := filepath.Join(t.TempDir(), ".compound-restored")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHomeRestoreCLIManifestIncompatibility$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=restore",
		homeManifestCompatibilityArchiveEnv+"="+archivePath,
		homeManifestCompatibilityTargetEnv+"="+target,
	)
	out, err := cmd.CombinedOutput()
	assertHomeManifestCompatibilityCLIError(t, err, out, "unknown_identity")
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("home restore CLI published incompatible target: %v", statErr)
	}
}

func TestHomeRestoreCLIArchiveSchemaIncompatibility(t *testing.T) {
	if os.Getenv(homeManifestCompatibilityChildEnv) == "restore-archive-schema" {
		cmdHome([]string{
			"restore",
			os.Getenv(homeManifestCompatibilityArchiveEnv),
			"--target",
			os.Getenv(homeManifestCompatibilityTargetEnv),
		})
		return
	}

	manifestBody := []byte("schema_version: " + home.SchemaVersion + "\nhome_kind: global\ncreated_at: 2026-01-01T00:00:00Z\nactive_modules: []\n")
	archivePath := writeManifestCompatibilityCLIArchiveWithHomeSchema(t, manifestBody, "compound.home.v9.9")
	target := filepath.Join(t.TempDir(), ".compound-restored")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHomeRestoreCLIArchiveSchemaIncompatibility$")
	cmd.Env = append(os.Environ(),
		homeManifestCompatibilityChildEnv+"=restore-archive-schema",
		homeManifestCompatibilityArchiveEnv+"="+archivePath,
		homeManifestCompatibilityTargetEnv+"="+target,
	)
	out, err := cmd.CombinedOutput()
	assertArchiveCompatibilityCLIError(t, err, out, "archive home_schema_version must be")
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("home restore CLI published archive-schema-incompatible target: %v", statErr)
	}
}

func assertHomeManifestCompatibilityCLIError(t *testing.T, err error, output []byte, reason string) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("home command exit = %v, want 1; output:\n%s", err, output)
	}
	text := strings.ToLower(string(output))
	for _, want := range []string{
		reason,
		"compound home backup --target <archive>",
		"compound home verify <archive>",
		"preserve the original home",
		"roll back",
		"compound home restore <archive> --target <new-absent-home-path>",
		"will not rewrite manifest.yaml",
	} {
		if !strings.Contains(text, strings.ToLower(want)) {
			t.Fatalf("home command output missing %q:\n%s", want, output)
		}
	}
}

func assertArchiveCompatibilityCLIError(t *testing.T, err error, output []byte, reason string) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("home command exit = %v, want 1; output:\n%s", err, output)
	}
	text := strings.ToLower(string(output))
	for _, want := range []string{
		reason,
		"preserve the archive unchanged",
		"compound home verify <archive>",
		"restore only to a new absent target",
		"compound home restore <archive> --target <new-absent-home-path>",
		"will not rewrite the archive",
	} {
		if !strings.Contains(text, strings.ToLower(want)) {
			t.Fatalf("home command output missing %q:\n%s", want, output)
		}
	}
}

func writeManifestCompatibilityCLIArchive(t *testing.T, manifestBody []byte) string {
	return writeManifestCompatibilityCLIArchiveWithHomeSchema(t, manifestBody, home.SchemaVersion)
}

func writeManifestCompatibilityCLIArchiveWithHomeSchema(t *testing.T, manifestBody []byte, homeSchemaVersion string) string {
	t.Helper()
	digest := sha256.Sum256(manifestBody)
	manifest := home.ArchiveManifest{
		SchemaVersion:     home.ArchiveSchemaVersion,
		Kind:              home.ArchiveKindBackup,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
		HomeSchemaVersion: homeSchemaVersion,
		Entries: []home.ArchiveEntry{{
			Path:   home.ManifestName,
			Size:   int64(len(manifestBody)),
			SHA256: fmt.Sprintf("%x", digest[:]),
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "incompatible-home.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	writeEntry := func(name string, body []byte) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	writeEntry(home.ManifestName, manifestBody)
	writeEntry(home.ArchiveManifestName, manifestJSON)
	if err := errors.Join(tw.Close(), gz.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return archivePath
}
