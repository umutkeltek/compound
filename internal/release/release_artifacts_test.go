package release

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const releaseFixtureCommit = "0123456789abcdef0123456789abcdef01234567"
const releaseGNUtarImage = "ubuntu:24.04@sha256:561618e2c15bf2397621dd04f96926663a3b5616c189cf7e38db7e82f5c538ea"

func TestReleaseArtifactsAreExactAndReproducible(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")

	runFixtureReleaseBuild(t, repo, first, "0.1.0")
	runFixtureReleaseBuild(t, repo, second, "0.1.0")

	wantNames := []string{
		"RELEASE",
		"SHA256SUMS",
		"compound_0.1.0_darwin_arm64.cdx.json",
		"compound_0.1.0_darwin_arm64.tar.gz",
		"compound_0.1.0_darwin_arm64_compound-breadth-engine.cdx.json",
		"compound_0.1.0_linux_amd64.cdx.json",
		"compound_0.1.0_linux_amd64.tar.gz",
		"compound_0.1.0_linux_amd64_compound-breadth-engine.cdx.json",
		"install-release.sh",
	}
	if got := releaseDirNames(t, first); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("release inventory = %v, want exactly %v", got, wantNames)
	}
	if got := releaseDirNames(t, second); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("second release inventory = %v, want exactly %v", got, wantNames)
	}
	for _, name := range wantNames {
		firstBody, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		secondBody, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(firstBody) != string(secondBody) {
			t.Fatalf("release artifact %s differs across clean builds", name)
		}
	}

	wantManifest := strings.Join([]string{
		"schema compound.release.v0.2",
		"version 0.1.0",
		"commit " + releaseTestGitSHA(t, repo),
		"installer install-release.sh",
		"artifact darwin arm64 compound_0.1.0_darwin_arm64.tar.gz compound_0.1.0_darwin_arm64.cdx.json compound_0.1.0_darwin_arm64_compound-breadth-engine.cdx.json",
		"artifact linux amd64 compound_0.1.0_linux_amd64.tar.gz compound_0.1.0_linux_amd64.cdx.json compound_0.1.0_linux_amd64_compound-breadth-engine.cdx.json",
		"",
	}, "\n")
	manifest, err := os.ReadFile(filepath.Join(first, "RELEASE"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != wantManifest {
		t.Fatalf("RELEASE = %q, want exact manifest %q", manifest, wantManifest)
	}

	checksumNames := checksumInventory(t, filepath.Join(first, "SHA256SUMS"))
	wantChecksums := append([]string(nil), wantNames...)
	wantChecksums = removeString(wantChecksums, "SHA256SUMS")
	if !reflect.DeepEqual(checksumNames, wantChecksums) {
		t.Fatalf("checksum inventory = %v, want %v", checksumNames, wantChecksums)
	}

	for _, platform := range []string{"darwin_arm64", "linux_amd64"} {
		archive := filepath.Join(first, "compound_0.1.0_"+platform+".tar.gz")
		bundle := "compound_0.1.0_" + platform
		wantMembers := []releaseArchiveMember{
			{name: bundle + "/", typeflag: tar.TypeDir},
			{name: bundle + "/compound", typeflag: tar.TypeReg},
			{name: bundle + "/compound-breadth-engine", typeflag: tar.TypeReg},
			{name: bundle + "/RELEASE-IDENTITY", typeflag: tar.TypeReg},
		}
		if got := releaseArchiveMembers(t, archive); !reflect.DeepEqual(got, wantMembers) {
			t.Fatalf("archive %s members = %#v, want %#v", platform, got, wantMembers)
		}
		assertReleaseSBOMApplication(t, filepath.Join(first, bundle+".cdx.json"), "cmd/compound", "0.1.0")
		assertReleaseSBOMApplication(t, filepath.Join(first, bundle+"_compound-breadth-engine.cdx.json"), "cmd/compound-breadth-engine", "0.1.0")
	}
}

func TestReleaseArtifactsRequireStableSemVerAndEmptyDestination(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	for _, version := range []string{"", "1.2.3-rc.1", "1.2.3+build.1", "01.2.3", "1.02.3", "1.2.03", "v1.2.3", "1.2"} {
		t.Run(version, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "dist")
			combined, err := fixtureReleaseBuild(repo, output, version)
			if err == nil {
				t.Fatalf("release build accepted non-stable version %q", version)
			}
			if !strings.Contains(string(combined), "strict stable SemVer") {
				t.Fatalf("version %q refusal did not name stable SemVer:\n%s", version, combined)
			}
		})
	}

	nonEmpty := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(nonEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(nonEmpty, "unrelated-stale-release.bin")
	if err := os.WriteFile(stale, []byte("must survive refusal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	combined, err := fixtureReleaseBuild(repo, nonEmpty, "0.1.0")
	if err == nil {
		t.Fatalf("release build accepted non-empty destination")
	}
	if !strings.Contains(string(combined), "must be empty") {
		t.Fatalf("non-empty destination refusal was unclear:\n%s", combined)
	}
	if body, readErr := os.ReadFile(stale); readErr != nil || string(body) != "must survive refusal\n" {
		t.Fatalf("release refusal modified stale file: body=%q err=%v", body, readErr)
	}
}

func TestReleaseArtifactsRefuseDirtySource(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	makefile := filepath.Join(repo, "Makefile")
	f, err := os.OpenFile(makefile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n# uncommitted release mutation\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(t.TempDir(), "dist")
	combined, err := fixtureReleaseBuild(repo, dist, "0.1.0")
	if err == nil {
		t.Fatalf("release build accepted dirty source")
	}
	if !strings.Contains(string(combined), "refuses dirty source") {
		t.Fatalf("dirty-source refusal was unclear:\n%s", combined)
	}
	if _, statErr := os.Lstat(dist); !os.IsNotExist(statErr) {
		t.Fatalf("dirty-source refusal published DIST: %v", statErr)
	}
}

func TestReleaseArtifactsRefuseSymlinkedInstallerSource(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	installer := filepath.Join(repo, "scripts", "install-release.sh")
	body, err := os.ReadFile(installer)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "install-release.sh")
	if err := os.WriteFile(outside, body, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(installer); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, installer); err != nil {
		t.Fatal(err)
	}
	commitReleaseFixtureChange(t, repo, "scripts/install-release.sh")

	combined, err := fixtureReleaseBuild(repo, filepath.Join(t.TempDir(), "dist"), "0.1.0")
	if err == nil {
		t.Fatalf("release build accepted symlinked installer source")
	}
	if !strings.Contains(string(combined), "installer source must be a regular non-symlink file") {
		t.Fatalf("symlinked installer refusal was unclear:\n%s", combined)
	}
}

func TestReleaseArtifactsRefuseSymlinkedBuildOutput(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	target := filepath.Join(t.TempDir(), "binary")
	writeExecutable(t, target, "#!/bin/sh\nexit 0\n")

	combined, err := fixtureReleaseBuildWithEnv(repo, filepath.Join(t.TempDir(), "dist"), "0.1.0", []string{
		"RELEASE_TEST_BINARY_SYMLINK_TARGET=" + target,
	})
	if err == nil {
		t.Fatalf("release build accepted symlinked compiler output")
	}
	if !strings.Contains(string(combined), "build output must be a regular non-symlink file") {
		t.Fatalf("symlinked build-output refusal was unclear:\n%s", combined)
	}
}

func TestReleaseArtifactsRefuseSymlinkedSBOMOutput(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	target := filepath.Join(t.TempDir(), "sbom.json")
	if err := os.WriteFile(target, []byte(`{"bomFormat":"CycloneDX"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	combined, err := fixtureReleaseBuildWithEnv(repo, filepath.Join(t.TempDir(), "dist"), "0.1.0", []string{
		"RELEASE_TEST_SBOM_OUTPUT_KIND=symlink",
		"RELEASE_TEST_SBOM_SYMLINK_TARGET=" + target,
	})
	if err == nil {
		t.Fatalf("release build accepted symlinked SBOM output")
	}
	if !strings.Contains(string(combined), "SBOM output must be a regular non-symlink file") {
		t.Fatalf("symlinked SBOM refusal was unclear:\n%s", combined)
	}
}

func TestReleaseArtifactsRefuseNonRegularSBOMOutput(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	combined, err := fixtureReleaseBuildWithEnv(repo, filepath.Join(t.TempDir(), "dist"), "0.1.0", []string{
		"RELEASE_TEST_SBOM_OUTPUT_KIND=directory",
	})
	if err == nil {
		t.Fatalf("release build accepted directory as SBOM output")
	}
	if !strings.Contains(string(combined), "SBOM output must be a regular non-symlink file") {
		t.Fatalf("non-regular SBOM refusal was unclear:\n%s", combined)
	}
}

func TestReleaseArtifactsAreConsumableByUbuntuGNUTar(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker portability probe is integration coverage")
	}
	requireDockerDaemon(t)
	repo := newReleaseBuildFixture(t)
	builder := filepath.Join(repo, "release-artifacts-linux-amd64")
	build := exec.Command("go", "build", "-o", builder, "./scripts/release-artifacts.go")
	build.Dir = repo
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Linux release helper: %v\n%s", err, output)
	}
	fakeGit := filepath.Join(repo, "tools", "fake-git")
	writeExecutable(t, fakeGit, `#!/bin/sh
set -eu
case "$1 $2" in
  "status --porcelain") exit 0 ;;
  "rev-parse --verify") printf '%s\n' "${RELEASE_TEST_COMMIT}" ;;
  *) printf 'unexpected fake git args: %s\n' "$*" >&2; exit 64 ;;
esac
`)

	run := exec.Command("docker", "run", "--rm", "--platform", "linux/amd64",
		"-v", repo+":/fixture",
		"-w", "/fixture",
		// Write as the host user: the release helper creates linux-dist
		// inside the mounted TempDir, and root-owned output made t.TempDir
		// cleanup fail with permission denied on CI runners. The probe
		// script needs no privileges.
		"-u", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-e", "VERSION=0.1.0",
		"-e", "DIST=/fixture/linux-dist",
		"-e", "BUILD_GO=/fixture/tools/fake-go",
		"-e", "BUILD_GIT=/fixture/tools/fake-git",
		"-e", "SBOM_TOOL=/fixture/tools/fake-sbom",
		"-e", "RELEASE_TEST_COMMIT="+releaseFixtureCommit,
		releaseGNUtarImage, "sh", "-c",
		`set -eu
tar --version | grep 'GNU tar'
/fixture/release-artifacts-linux-amd64
test "$(find /fixture/linux-dist -mindepth 1 -maxdepth 1 -type f | wc -l)" -eq 9
test "$(tar -tzf /fixture/linux-dist/compound_0.1.0_linux_amd64.tar.gz | wc -l)" -eq 4
tar -tzf /fixture/linux-dist/compound_0.1.0_linux_amd64.tar.gz >/dev/null`,
	)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("Ubuntu GNU-tar release probe failed: %v\n%s", err, output)
	}
}

type releaseArchiveMember struct {
	name     string
	typeflag byte
}

func releaseArchiveMembers(t *testing.T, path string) []releaseArchiveMember {
	t.Helper()
	f, err := os.Open(path)
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
	var members []releaseArchiveMember
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatal(err)
		}
		members = append(members, releaseArchiveMember{name: hdr.Name, typeflag: hdr.Typeflag})
	}
	return members
}

func newReleaseBuildFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	copyReleaseFixtureFile(t, filepath.Join("..", "..", "Makefile"), filepath.Join(repo, "Makefile"), 0o600)
	copyReleaseFixtureFile(t, filepath.Join("..", "..", "scripts", "release-artifacts.go"), filepath.Join(repo, "scripts", "release-artifacts.go"), 0o600)
	copyReleaseFixtureFile(t, filepath.Join("..", "..", "scripts", "install-release.sh"), filepath.Join(repo, "scripts", "install-release.sh"), 0o700)
	tools := filepath.Join(repo, "tools")
	if err := os.MkdirAll(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(tools, "fake-go"), `#!/bin/sh
set -eu
case "$1" in
  run)
    if [ -n "${RELEASE_TEST_VERSION_STDERR:-}" ]; then printf '%s\n' "$RELEASE_TEST_VERSION_STDERR" >&2; fi
    printf 'compound %s (build dev)\n' "${RELEASE_TEST_SOURCE_VERSION:-$COMPOUND_RELEASE_VERSION}"
    ;;
  build)
    output=
    target=
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "-o" ]; then output=$2; shift 2; continue; fi
      target=$1
      shift
    done
	if [ -n "${RELEASE_TEST_BINARY_SYMLINK_TARGET:-}" ]; then
	  ln -s "$RELEASE_TEST_BINARY_SYMLINK_TARGET" "$output"
	  exit 0
	fi
    case "$target" in
      ./cmd/compound)
        printf '#!/bin/sh\nprintf '\''compound %%s (build %%s)\\n'\'' "%s" "%s"\n' \
          "${COMPOUND_RELEASE_VERSION}" "${COMPOUND_RELEASE_COMMIT}" > "$output"
        ;;
      ./cmd/compound-breadth-engine)
        printf '#!/bin/sh\nprintf '\''breadth %%s %%s\\n'\'' "%s" "%s"\n' \
          "${COMPOUND_RELEASE_VERSION}" "${COMPOUND_RELEASE_COMMIT}" > "$output"
        ;;
      *) printf 'unexpected build target: %s\n' "$target" >&2; exit 64 ;;
    esac
    chmod 0755 "$output"
    ;;
  *) printf 'unexpected fake go args: %s\n' "$*" >&2; exit 64 ;;
esac
`)
	writeExecutable(t, filepath.Join(tools, "fake-sbom"), `#!/bin/sh
set -eu
[ "$1" = bin ] || { printf 'expected binary inspection, got: %s\n' "$*" >&2; exit 64; }
shift
output=
version=
binary=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -json|-noserial|-notimestamp) shift ;;
    -output) output=$2; shift 2 ;;
    -version) version=$2; shift 2 ;;
    -*) printf 'unexpected fake sbom flag: %s\n' "$1" >&2; exit 64 ;;
    *) binary=$1; shift ;;
  esac
done
[ -n "$output" ]
[ "$version" = "$COMPOUND_RELEASE_VERSION" ]
[ -f "$binary" ] && [ -x "$binary" ]
# Inspect the just-built application, without executing a cross-platform binary.
grep -F -- "$version" "$binary" >/dev/null
grep -F -- "$COMPOUND_RELEASE_COMMIT" "$binary" >/dev/null
case "$(basename "$binary")" in
  compound) main=cmd/compound ;;
  compound-breadth-engine) main=cmd/compound-breadth-engine ;;
  *) printf 'unexpected binary: %s\n' "$binary" >&2; exit 64 ;;
esac
case "${RELEASE_TEST_SBOM_OUTPUT_KIND:-regular}" in
  regular)
    if [ -n "${RELEASE_TEST_GENERATOR_HASH:-}" ]; then
      printf '{"bomFormat":"CycloneDX","specVersion":"1.6","application":"%s","version":"%s","metadata":{"tools":[{"vendor":"CycloneDX","name":"cyclonedx-gomod","version":"v1.10.0","hashes":[{"alg":"SHA-256","content":"%s"}]},{"vendor":"Other","name":"other-tool","hashes":[{"alg":"SHA-256","content":"other-tool-hash"}]}]},"components":[{"name":"dependency","hashes":[{"alg":"SHA-256","content":"%s"}]}]}\n' "$main" "$version" "$RELEASE_TEST_GENERATOR_HASH" "$RELEASE_TEST_COMPONENT_HASH" > "$output"
    else
      printf '{"bomFormat":"CycloneDX","specVersion":"1.6","application":"%s","version":"%s"}\n' "$main" "$version" > "$output"
    fi
    ;;
  malformed) printf '{"metadata":' > "$output" ;;
  symlink) ln -s "$RELEASE_TEST_SBOM_SYMLINK_TARGET" "$output" ;;
  directory) mkdir "$output" ;;
  *) printf 'unexpected SBOM output kind: %s\n' "$RELEASE_TEST_SBOM_OUTPUT_KIND" >&2; exit 64 ;;
esac
`)
	runReleaseGit(t, repo, "init", "-q")
	runReleaseGit(t, repo, "config", "user.name", "Synthetic Release Test")
	runReleaseGit(t, repo, "config", "user.email", "release-test@example.invalid")
	runReleaseGit(t, repo, "add", "Makefile", "scripts", "tools")
	runReleaseGit(t, repo, "commit", "-qm", "synthetic release fixture")
	return repo
}

func releaseTestGitSHA(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	body, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(body))
}

func runFixtureReleaseBuild(t *testing.T, repo, dist, version string) {
	t.Helper()
	if output, err := fixtureReleaseBuild(repo, dist, version); err != nil {
		t.Fatalf("release build failed: %v\n%s", err, output)
	}
}

func fixtureReleaseBuild(repo, dist, version string) ([]byte, error) {
	return fixtureReleaseBuildWithEnv(repo, dist, version, nil)
}

func fixtureReleaseBuildWithEnv(repo, dist, version string, extraEnv []string) ([]byte, error) {
	cmd := exec.Command("make", "release-artifacts", "VERSION="+version, "DIST="+dist,
		"GO="+filepath.Join(repo, "tools", "fake-go"),
		"SBOM_TOOL="+filepath.Join(repo, "tools", "fake-sbom"))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), extraEnv...)
	return cmd.CombinedOutput()
}

func commitReleaseFixtureChange(t *testing.T, repo string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	runReleaseGit(t, repo, args...)
	runReleaseGit(t, repo, "commit", "-qm", "change synthetic release fixture")
}

func releaseDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("release output contains directory %s", entry.Name())
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func checksumInventory(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			t.Fatalf("malformed checksum line %q", line)
		}
		names = append(names, strings.TrimPrefix(fields[1], "*"))
	}
	sort.Strings(names)
	return names
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func assertReleaseSBOMApplication(t *testing.T, path, application, version string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `"application":"` + application + `"`
	if !strings.Contains(string(body), want) {
		t.Fatalf("SBOM %s = %q, want %q", filepath.Base(path), body, want)
	}
	if want := `"version":"` + version + `"`; !strings.Contains(string(body), want) {
		t.Fatalf("SBOM %s = %q, want release version %q", filepath.Base(path), body, version)
	}
}

func copyReleaseFixtureFile(t *testing.T, source, target string, mode os.FileMode) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, mode); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func runReleaseGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestReleaseArtifactsIgnoreGeneratorSelfHashes(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	build := func(hash, component string) string {
		t.Helper()
		dist := filepath.Join(t.TempDir(), "dist")
		output, err := fixtureReleaseBuildWithEnv(repo, dist, "0.1.0", []string{
			"RELEASE_TEST_GENERATOR_HASH=" + hash, "RELEASE_TEST_COMPONENT_HASH=" + component,
		})
		if err != nil {
			t.Fatalf("fixture build: %v\n%s", err, output)
		}
		return dist
	}
	component := strings.Repeat("c", 64)
	first := build(strings.Repeat("a", 64), component)
	second := build(strings.Repeat("b", 64), component)
	for _, name := range releaseDirNames(t, first) {
		a, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatalf("native generator selfhash changed release artifact %s", name)
		}
	}
	for _, name := range releaseDirNames(t, first) {
		if !strings.HasSuffix(name, ".cdx.json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		tools := doc["metadata"].(map[string]any)["tools"].([]any)
		generator := tools[0].(map[string]any)
		if _, exists := generator["hashes"]; exists {
			t.Fatalf("generator selfhash still shipped: %s", body)
		}
		if generator["vendor"] != "CycloneDX" || generator["name"] != "cyclonedx-gomod" || generator["version"] != "v1.10.0" {
			t.Fatalf("generator identity lost: %+v", generator)
		}
		if tools[1].(map[string]any)["hashes"].([]any)[0].(map[string]any)["content"] != "other-tool-hash" {
			t.Fatal("another tool hash was removed or changed")
		}
		if doc["components"].([]any)[0].(map[string]any)["hashes"].([]any)[0].(map[string]any)["content"] != component {
			t.Fatal("dependency hash was removed or changed")
		}
	}
	third := build(strings.Repeat("b", 64), strings.Repeat("d", 64))
	a, err := os.ReadFile(filepath.Join(first, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(third, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatal("a dependency hash change was hidden from the full bundle comparison")
	}
}

func TestReleaseArtifactsSourceVersionUsesStdout(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	output, err := fixtureReleaseBuildWithEnv(repo, filepath.Join(t.TempDir(), "dist"), "0.1.0",
		[]string{"RELEASE_TEST_VERSION_STDERR=go: downloading example.invalid/module v0.1.0"})
	if err != nil {
		t.Fatalf("cold-cache diagnostics became version data: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "go: downloading example.invalid/module") {
		t.Fatal("diagnostic output was discarded")
	}
	output, err = fixtureReleaseBuildWithEnv(repo, filepath.Join(t.TempDir(), "dist"), "0.1.0",
		[]string{"RELEASE_TEST_SOURCE_VERSION=0.2.0", "RELEASE_TEST_VERSION_STDERR=compound 0.1.0 (build dev)"})
	if err == nil || !strings.Contains(string(output), "does not match source version output") {
		t.Fatalf("stderr supplied a false valid source version: %v\n%s", err, output)
	}
}

func TestReleaseArtifactsRefuseMalformedSBOM(t *testing.T) {
	repo := newReleaseBuildFixture(t)
	dist := filepath.Join(t.TempDir(), "dist")
	output, err := fixtureReleaseBuildWithEnv(repo, dist, "0.1.0", []string{"RELEASE_TEST_SBOM_OUTPUT_KIND=malformed"})
	if err == nil {
		t.Fatal("malformed SBOM was published")
	}
	if !strings.Contains(string(output), "decode SBOM") {
		t.Fatalf("unclear malformed SBOM refusal: %s", output)
	}
	if _, err := os.Lstat(dist); !os.IsNotExist(err) {
		t.Fatalf("malformed evidence published DIST: %v", err)
	}
}
