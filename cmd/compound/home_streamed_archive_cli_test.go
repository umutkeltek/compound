package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
)

// Exercise the public recovery doors, including format selection. A v1 archive
// that merely passes its own roundtrip must not stand in for the streamed path.
func TestHomeStreamedArchivePublicRoundTrip(t *testing.T) {
	t.Parallel()
	bin := compoundTestBinary(t)
	source := t.TempDir()
	if err := home.Init(source, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	want := []byte("synthetic recovery consumer\n")
	rel := filepath.Join("config", "streamed-fixture.txt")
	if err := os.WriteFile(filepath.Join(source, rel), want, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "home.tar.gz")
	runStreamedHomeCLI(t, bin, source, true, "home", "backup", "--format", "streamed-v2", "--target", archive)
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "compound-home-archive-header.json" || header.Size > 4096 {
		t.Fatalf("streamed profile was not selected: first header = %+v", header)
	}
	data, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
	}
	if err := json.Unmarshal(data, &marker); err != nil || marker.SchemaVersion != "compound.home_archive.v0.2" || marker.Kind != "backup" {
		t.Fatalf("unexpected streamed descriptor: %s, error: %v", data, err)
	}
	runStreamedHomeCLI(t, bin, source, true, "home", "verify", archive)
	// The original body is unavailable; restore must consume archived bytes.
	if err := os.Rename(filepath.Join(source, rel), filepath.Join(t.TempDir(), "retained-source.txt")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	runStreamedHomeCLI(t, bin, source, true, "home", "restore", archive, "--target", target)
	got, err := os.ReadFile(filepath.Join(target, rel))
	if err != nil || string(got) != string(want) {
		t.Fatalf("restored body = %q, error: %v", got, err)
	}
	runStreamedHomeCLI(t, bin, target, true, "home", "doctor", "--json")
	runStreamedHomeCLI(t, bin, target, true, "workbench", "--json")
	runStreamedHomeCLI(t, bin, source, false, "home", "restore", archive, "--target", target)
	got, err = os.ReadFile(filepath.Join(target, rel))
	if err != nil || string(got) != string(want) {
		t.Fatalf("refused overwrite changed restored body = %q, error: %v", got, err)
	}
}

func TestHomeBackupUnknownFormatRefusesPublication(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := home.Init(source, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "must-not-exist.tar.gz")
	out := runStreamedHomeCLI(t, compoundTestBinary(t), source, false, "home", "backup", "--format", "future-unreviewed", "--target", archive)
	if !strings.Contains(strings.ToLower(out), "format") {
		t.Fatalf("refusal does not explain format: %s", out)
	}
	if _, err := os.Lstat(archive); !os.IsNotExist(err) {
		t.Fatalf("unknown format published an archive: %v", err)
	}
}

func TestHomeBackupMissingFormatRefusesPublication(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := home.Init(source, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "must-not-exist.tar.gz")
	runStreamedHomeCLI(t, compoundTestBinary(t), source, false, "home", "backup", "--target", archive, "--format")
	if _, err := os.Lstat(archive); !os.IsNotExist(err) {
		t.Fatalf("missing format published an archive: %v", err)
	}
}

func TestHomeBackupExplicitV1AndLeadingFormatRemainCompatible(t *testing.T) {
	t.Parallel()
	bin := compoundTestBinary(t)
	source := t.TempDir()
	if err := home.Init(source, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"v1", "streamed-v2"} {
		t.Run(format, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "home.tar.gz")
			runStreamedHomeCLI(t, bin, source, true, "home", "--format", format, "backup", "--target", archive)
			runStreamedHomeCLI(t, bin, source, true, "home", "verify", archive)
			target := filepath.Join(t.TempDir(), "restored")
			runStreamedHomeCLI(t, bin, source, true, "home", "restore", archive, "--target", target)
			runStreamedHomeCLI(t, bin, target, true, "home", "doctor", "--json")
		})
	}
}

func runStreamedHomeCLI(t *testing.T, bin, homePath string, success bool, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if success && err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
	if !success {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("%v did not refuse with process failure: %v\n%s", args, err, out)
		}
	}
	return string(out)
}
