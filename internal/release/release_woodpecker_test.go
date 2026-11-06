package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type woodpeckerReleaseStep struct {
	Image     string   `yaml:"image"`
	DependsOn []string `yaml:"depends_on"`
	When      []struct {
		Event    string `yaml:"event"`
		Evaluate string `yaml:"evaluate"`
	} `yaml:"when"`
	BackendOptions struct {
		Docker struct {
			User string `yaml:"user"`
		} `yaml:"docker"`
	} `yaml:"backend_options"`
	Volumes     []string       `yaml:"volumes"`
	Commands    []string       `yaml:"commands"`
	Settings    map[string]any `yaml:"settings"`
	Environment map[string]any `yaml:"environment"`
	Entrypoint  []string       `yaml:"entrypoint"`
}

func woodpeckerReleaseSteps(t *testing.T) map[string]woodpeckerReleaseStep {
	t.Helper()
	body, err := os.ReadFile("../../.woodpecker.yml")
	if err != nil {
		t.Fatal(err)
	}
	var pipeline struct {
		Steps map[string]woodpeckerReleaseStep `yaml:"steps"`
	}
	if err := yaml.Unmarshal(body, &pipeline); err != nil {
		t.Fatal(err)
	}
	return pipeline.Steps
}

func TestWoodpeckerReleaseGatesAndDependencyChain(t *testing.T) {
	steps := woodpeckerReleaseSteps(t)
	if len(steps) != 8 {
		t.Fatalf("got %d steps, want four proof steps plus four release steps", len(steps))
	}
	for _, name := range []string{"prepare-bun", "proof-gates", "console-gates", "secret-scan"} {
		if len(steps[name].When) != 0 {
			t.Errorf("routine gate %s became conditional", name)
		}
	}
	previous := "secret-scan"
	for _, name := range []string{"release-history", "release-build", "release-linux-drill", "release-stage"} {
		step := steps[name]
		if !reflect.DeepEqual(step.DependsOn, []string{previous}) {
			t.Errorf("%s dependencies = %v, want %s", name, step.DependsOn, previous)
		}
		if len(step.When) != 1 || step.When[0].Event != "manual" || step.When[0].Evaluate != `RELEASE_VERSION != ""` {
			t.Errorf("%s must AND manual event and nonempty release variable in one condition: %+v", name, step.When)
		}
		script := strings.ReplaceAll(strings.Join(step.Commands, "\n"), "$$", "$")
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s shell syntax: %v\n%s", name, err, output)
		}
		previous = name
	}
	for _, name := range []string{"release-linux-drill", "release-stage"} {
		if steps[name].BackendOptions.Docker.User != "1000:1000" || steps[name].Image != "docker.io/library/node:22-bookworm" {
			t.Errorf("%s must use Node 22 and uid 1000", name)
		}
	}
	if !reflect.DeepEqual(steps["release-stage"].Volumes, []string{"/srv/appdata/woodpecker/releases:/releases"}) {
		t.Fatal("release stage must use the owned bind mount")
	}
}

func TestWoodpeckerReleaseBuildAndDrillContract(t *testing.T) {
	steps := woodpeckerReleaseSteps(t)
	build := strings.Join(steps["release-build"].Commands, "\n")
	for _, want := range []string{
		`setpriv --reuid=1000 --regid=1000 --clear-groups`,
		`test "$CI_COMMIT_SHA" = "$main_sha"`,
		`test "$(git rev-parse HEAD)" = "$CI_COMMIT_SHA"`,
		`test "$(git rev-parse --is-shallow-repository)" = false`,
		`go mod verify`, `release export-public --repo "$source"`,
		`tar -tzf`, `cyclonedx-gomod/cmd/cyclonedx-gomod@v1.10.0`,
		`for build in a b; do`, `VERSION="$RELEASE_VERSION" SOURCE_ROOT="$source" DIST="$root/$build"`,
		`cmp "$root/a/SHA256SUMS" "$root/b/SHA256SUMS"`,
		`cmp "$root/a/$name" "$root/b/$name"`, `sha256sum --check SHA256SUMS`,
		`-mindepth 1 -maxdepth 1 -print | wc -l)" -eq 9`, `test ! -L`,
		`git rev-parse HEAD~1`, `git archive "$prior_commit"`,
		`git -C "$root/prior-source" reset --mixed "$prior_commit"`,
		`prior_version=$(cd "$root/prior-source" && go run ./cmd/compound version`,
		`VERSION="$prior_version" SOURCE_ROOT="$root/prior-source"`,
	} {
		if !strings.Contains(build, want) {
			t.Errorf("build lacks %q", want)
		}
	}
	if strings.Contains(build, "--is-ancestor") || strings.Contains(build, "0.0.0-prior") {
		t.Fatal("build permits stale main or invents an unsupported prior version")
	}
	// Each step runs in a fresh container and export-public refuses without a
	// home, so the step must create its own disposable home first.
	homeInit := strings.Index(build, `COMPOUND_HOME="$release_home" "$CI_WORKSPACE/.ci/bin/compound" home init`)
	export := strings.Index(build, `COMPOUND_HOME="$release_home" "$CI_WORKSPACE/.ci/bin/compound" release export-public --repo "$source"`)
	if !strings.Contains(build, `release_home="$(mktemp -d /tmp/`) || homeInit < 0 || export < homeInit {
		t.Fatal("release-build must initialize a disposable home outside the checkout before export-public")
	}
	drill := strings.Join(steps["release-linux-drill"].Commands, "\n")
	// home doctor refuses a home inside a Git checkout, so acceptance must run
	// outside the workspace; only its report comes back as workspace evidence.
	if !strings.Contains(drill, `acceptance="$(mktemp -d /tmp/compound-release-acceptance.XXXXXX)/run"`) ||
		strings.Contains(drill, `"$root/linux-acceptance" "$root/prior"`) ||
		!strings.Contains(drill, `cp "$acceptance/installed-acceptance.json" "$root/linux-acceptance.json"`) {
		t.Fatal("Linux drill must run acceptance outside the checkout and keep its report")
	}
	for _, want := range []string{`COMPOUND_RELEASE_TARBALL=`, `COMPOUND_HOME=`, `GOBIN=`, `compound $RELEASE_VERSION (build $CI_COMMIT_SHA)`, `compound-breadth-engine`, `node scripts/installed-release-acceptance.mjs`, `"$root/prior" "$(cat "$root/PRIOR_COMMIT")"`} {
		if !strings.Contains(drill, want) {
			t.Errorf("Linux drill lacks %q", want)
		}
	}
}

func TestLocalPublisherPinsTheCompilerThatBuiltTheStagedBytes(t *testing.T) {
	body, err := os.ReadFile("../../scripts/release-publish-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	pin := strings.Index(script, "export GOTOOLCHAIN=go1.27.2\n")
	firstGo := strings.Index(script, "go install ")
	if pin < 0 || firstGo < 0 || pin > firstGo {
		t.Fatal("publisher must pin the Go toolchain before its first build: another compiler changes the bytes and the rebuild refuses")
	}
	image := woodpeckerReleaseSteps(t)["release-build"].Image
	if image != "docker.io/library/golang:1.27.2-bookworm" {
		t.Fatalf("release-build image %s no longer matches the publisher's pinned toolchain", image)
	}
}

func TestWoodpeckerReleaseStageRefusesExistingCandidate(t *testing.T) {
	script := strings.Join(woodpeckerReleaseSteps(t)["release-stage"].Commands, "\n")
	for _, existing := range []string{"absent", "directory", "symlink"} {
		t.Run(existing, func(t *testing.T) {
			root := t.TempDir()
			stage := filepath.Join(root, "releases")
			candidate := filepath.Join(root, ".ci", "release", "a")
			tools := filepath.Join(root, "tools")
			for _, dir := range []string{stage, candidate, tools} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range releaseWorkflowArtifactNames("0.1.0") {
				if name != "SHA256SUMS" {
					if err := os.WriteFile(filepath.Join(candidate, name), []byte(name+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Use real checksums on both supported test hosts; only uid is synthetic.
			for name, body := range map[string]string{"id": "#!/bin/sh\nprintf '1000\\n'\n", "sha256sum": "#!/bin/sh\nexec shasum -a 256 \"$@\"\n"} {
				if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			checksum := exec.Command("sh", "-c", "shasum -a 256 * > SHA256SUMS")
			checksum.Dir = candidate
			if output, err := checksum.CombinedOutput(); err != nil {
				t.Fatalf("checksum: %v %s", err, output)
			}
			destination := filepath.Join(stage, "0.1.0")
			if existing == "directory" {
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
			} else if existing == "symlink" {
				if err := os.Symlink(candidate, destination); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", "-c", strings.ReplaceAll(script, `destination="/releases/`, `destination="`+stage+`/`))
			cmd.Env = append(os.Environ(), "PATH="+tools+":"+os.Getenv("PATH"), "CI_WORKSPACE="+root, "CI_PIPELINE_EVENT=manual", "RELEASE_VERSION=0.1.0", "CI_COMMIT_SHA="+releaseFixtureCommit, "CI_PIPELINE_NUMBER=42")
			output, err := cmd.CombinedOutput()
			if existing != "absent" {
				if err == nil || !strings.Contains(string(output), "release candidate already exists") {
					t.Fatalf("overwrote existing %s: %v %s", existing, err, output)
				}
				if _, err := os.Stat(filepath.Join(destination, "CANDIDATE")); !os.IsNotExist(err) {
					t.Fatal("refused candidate was modified")
				}
				return
			}
			if err != nil {
				t.Fatalf("stage: %v %s", err, output)
			}
			body, err := os.ReadFile(filepath.Join(destination, "CANDIDATE"))
			if err != nil || !strings.HasPrefix(string(body), fmt.Sprintf("version 0.1.0\ncommit %s\npipeline 42\nsha256sums_sha256 ", releaseFixtureCommit)) {
				t.Fatalf("candidate identity: %s, %v", body, err)
			}
		})
	}
}

func TestWoodpeckerReleaseHistoryKeepsCredentialsInTrustedClone(t *testing.T) {
	step := woodpeckerReleaseSteps(t)["release-history"]
	if step.Image != "docker.io/woodpeckerci/plugin-git:2.9.0@sha256:1e536c0da16f8cbb35e870fb3d75fde45c85c7239225fea2209cc6990ba330e4" {
		t.Fatal("release history must use the pinned existing trusted clone image")
	}
	// Woodpecker 3.14 grants clone credentials only when all three are absent.
	if len(step.Commands) != 0 || len(step.Entrypoint) != 0 || len(step.Environment) != 0 {
		t.Fatal("custom commands, entrypoint or environment lose trusted-plugin credential injection")
	}
	want := map[string]any{"path": "$" + "{CI_WORKSPACE}/.ci/release-source", "ref": "refs/heads/main", "partial": false, "depth": 0, "tags": false, "recursive": false, "umask": 18, "home": "/root"}
	if !reflect.DeepEqual(step.Settings, want) {
		t.Fatalf("release history settings = %#v, want %#v", step.Settings, want)
	}
	build := strings.Join(woodpeckerReleaseSteps(t)["release-build"].Commands, "\n")
	if strings.Contains(build, "git fetch") || strings.Contains(build, "CI_NETRC_") || strings.Contains(build, ".netrc") {
		t.Fatal("release build must consume full source without fetching or retaining clone credentials")
	}
}
func releaseHistoryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, body)
	}
	return strings.TrimSpace(string(body))
}
func TestWoodpeckerReleaseHistorySourceIdentityRefusals(t *testing.T) {
	origin := t.TempDir()
	releaseHistoryGit(t, origin, "init", "-b", "main")
	releaseHistoryGit(t, origin, "config", "user.name", "Release Fixture")
	releaseHistoryGit(t, origin, "config", "user.email", "release@example.invalid")
	for _, body := range []string{"prior", "candidate"} {
		if err := os.WriteFile(filepath.Join(origin, "source"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		releaseHistoryGit(t, origin, "add", "source")
		releaseHistoryGit(t, origin, "commit", "-m", body)
	}
	candidate := releaseHistoryGit(t, origin, "rev-parse", "HEAD")
	prior := releaseHistoryGit(t, origin, "rev-parse", "HEAD~1")
	remote := "file://" + origin
	build := strings.Join(woodpeckerReleaseSteps(t)["release-build"].Commands, "\n")
	start, end := strings.Index(build, `source="$CI_WORKSPACE/.ci/release-source"`), strings.Index(build, `root="$CI_WORKSPACE/.ci/release"`)
	if start < 0 || end <= start {
		t.Fatal("missing source admission before build effects")
	}
	for _, test := range []string{"full", "shallow", "stale-candidate", "wrong-head", "wrong-origin", "missing", "symlink"} {
		t.Run(test, func(t *testing.T) {
			workspace := t.TempDir()
			source := filepath.Join(workspace, ".ci", "release-source")
			if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
				t.Fatal(err)
			}
			if test != "missing" && test != "symlink" {
				if test == "shallow" {
					releaseHistoryGit(t, workspace, "clone", "--depth=1", remote, source)
				} else {
					if err := os.Mkdir(source, 0700); err != nil {
						t.Fatal(err)
					}
					releaseHistoryGit(t, source, "init")
					releaseHistoryGit(t, source, "remote", "add", "origin", remote)
					// Exact fetch/checkout grammar of the pinned plugin, without credentials.
					releaseHistoryGit(t, source, "fetch", "--no-tags", "origin", "+refs/heads/main:")
					releaseHistoryGit(t, source, "checkout", "-f", "FETCH_HEAD")
				}
			}
			commit, expectedRemote := candidate, remote
			switch test {
			case "stale-candidate":
				commit = prior
			case "wrong-head":
				releaseHistoryGit(t, source, "checkout", "--detach", prior)
			case "wrong-origin":
				expectedRemote = "https://git.example.invalid/other/repo.git"
			case "symlink":
				if err := os.Symlink(origin, source); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-euc", build[start:end])
			cmd.Env = append(os.Environ(), "CI_WORKSPACE="+workspace, "CI_COMMIT_SHA="+commit, "CI_REPO_CLONE_URL="+expectedRemote, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
			output, err := cmd.CombinedOutput()
			if test == "full" {
				if err != nil {
					t.Fatalf("full source refused: %v %s", err, output)
				}
				if got := releaseHistoryGit(t, source, "rev-parse", "HEAD~1"); got != prior {
					t.Fatal("real prior unavailable")
				}
			} else if err == nil {
				t.Fatalf("accepted %s source: %s", test, output)
			}
		})
	}
}
func TestWoodpeckerReleaseHistoryRequiresFreshDestination(t *testing.T) {
	commands := woodpeckerReleaseSteps(t)["secret-scan"].Commands
	script := commands[len(commands)-1]
	if !strings.Contains(script, "release source already exists") {
		t.Fatal("missing pre-clone fresh destination guard")
	}
	for _, test := range []string{"absent", "directory", "file", "symlink", "dangling-symlink", "routine"} {
		t.Run(test, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, ".ci", "release-source")
			if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
				t.Fatal(err)
			}
			switch test {
			case "directory", "routine":
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(source, []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(root, source); err != nil {
					t.Fatal(err)
				}
			case "dangling-symlink":
				if err := os.Symlink(filepath.Join(root, "missing"), source); err != nil {
					t.Fatal(err)
				}
			}
			event := "manual"
			if test == "routine" {
				event = "push"
			}
			cmd := exec.Command("sh", "-eu", "-c", script)
			cmd.Env = append(os.Environ(), "CI_WORKSPACE="+root, "CI_PIPELINE_EVENT="+event, "RELEASE_VERSION=0.1.0")
			output, err := cmd.CombinedOutput()
			if test == "absent" || test == "routine" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v %s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), "release source already exists") {
				t.Fatalf("accepted existing %s: %v %s", test, err, output)
			}
		})
	}
}
