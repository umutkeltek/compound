package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func TestRefreshPreflightThroughMainPreservesHomeAdmission(t *testing.T) {
	const childEnv = "COMPOUND_TEST_REFRESH_PREFLIGHT_MAIN"
	if raw := os.Getenv(childEnv); raw != "" {
		os.Args = append([]string{"compound"}, strings.Split(raw, "\x1f")...)
		main()
		os.Exit(0)
	}
	for _, tc := range []struct {
		name, manifest, external, want string
		ordinary                       bool
		wantExit                       int
	}{
		{name: "absent", want: "[]"},
		{name: "ordinary-absent", ordinary: true, wantExit: 1, want: "home manifest is missing"},
		{name: "existing-missing", manifest: "missing", wantExit: 1, want: "home manifest is missing"},
		{name: "existing-corrupt", manifest: "schema_version: [", wantExit: 1, want: "parse home manifest"},
		{name: "existing-incompatible", manifest: "schema_version: compound.home.v99\n", wantExit: 1, want: "unsupported home schema"},
		{name: "existing-valid", manifest: "valid", want: "[]"},
		{name: "external-omp", manifest: "valid", external: "oh_my_pi", wantExit: 1, want: "compound hook install --harness oh-my-pi"},
		{name: "external-opencode", manifest: "valid", external: "opencode", wantExit: 1, want: "adapters/opencode/install.ts"},
		{name: "external-unknown", manifest: "valid", external: "unrecognized", wantExit: 1, want: "no native hook refresh owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			homePath, baseDir := filepath.Join(root, "home"), filepath.Join(root, "profile")
			if tc.manifest == "valid" {
				if err := home.Init(homePath, home.HomeKindGlobal, time.Now()); err != nil {
					t.Fatal(err)
				}
			} else if tc.manifest != "" {
				if err := os.Mkdir(homePath, 0700); err != nil {
					t.Fatal(err)
				}
				if tc.manifest != "missing" {
					if err := os.WriteFile(filepath.Join(homePath, home.ManifestName), []byte(tc.manifest), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.external != "" {
				manifest := "schema_version: compound.installed_hooks.v0.1\nhooks:\n"
				for _, name := range []string{"codex", tc.external} {
					manifest += "  - harness: " + name + "\n    name: PostToolUse\n    event: PostToolUse\n    ref: compound\n    revision: synthetic\n    status: current\n"
				}
				if err := os.WriteFile(filepath.Join(homePath, "config", "installed-hooks.yaml"), []byte(manifest), 0640); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(baseDir, ".codex"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(baseDir, ".codex", "hooks.json"), []byte("{\"synthetic_setting\":\"preserve\"}\n"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			before := refreshPreflightFixtureSnapshot(t, root)
			args := []string{"harness", "refresh-lane", "--home", homePath, "--base", baseDir, "--binary", filepath.Join(root, "compound-synthetic"), "--json"}
			if !tc.ordinary {
				args = append(args, "--preflight")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRefreshPreflightThroughMainPreservesHomeAdmission$")
			cmd.Env = append(os.Environ(), childEnv+"="+strings.Join(args, "\x1f"), "COMPOUND_HOME="+homePath)
			out, err := cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != tc.wantExit || !strings.Contains(string(out), tc.want) {
				t.Fatalf("exit=%d, want=%d; error=%v; output missing %q:\n%s", got, tc.wantExit, err, tc.want, out)
			}
			if after := refreshPreflightFixtureSnapshot(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("read-only preflight changed home/profile preimages: before=%v after=%v", before, after)
			}
		})
	}
}

func refreshPreflightFixtureSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		body := []byte(nil)
		if entry.Type().IsRegular() {
			body, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		out[path] = fmt.Sprintf("%s:%x", info.Mode(), sha256.Sum256(body))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
