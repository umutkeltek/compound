package lanes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/home"
)

func TestRefreshAllRefusesExternalAdapterBeforeNativeMutation(t *testing.T) {
	for _, external := range []string{"oh_my_pi", "opencode"} {
		t.Run(external, func(t *testing.T) {
			homePath, baseDir := t.TempDir(), t.TempDir()
			binaryPath := filepath.Join(t.TempDir(), "compound-synthetic")
			if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
			writeInstalledHooksManifest(t, homePath, map[string]string{
				"codex": "binary:" + binaryPath, external: "binary:" + binaryPath,
			})
			configPath := filepath.Join(baseDir, ".codex", "hooks.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
				t.Fatal(err)
			}
			originalConfig := []byte("{\"synthetic_setting\":\"preserve\"}\n")
			if err := os.WriteFile(configPath, originalConfig, 0640); err != nil {
				t.Fatal(err)
			}
			manifestPath := contract.InstalledHooksPath(homePath)
			originalManifest, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			verdicts, err := RefreshAllInstalledHarnesses(RefreshLaneOptions{
				HomePath: homePath, BaseDir: baseDir, BinaryPath: binaryPath, Now: time.Now().UTC(),
			})
			if err != nil || len(verdicts) != 2 {
				t.Fatalf("refresh verdicts: %+v; error: %v", verdicts, err)
			}
			owner := "adapters/opencode/install.ts"
			if external == "oh_my_pi" {
				owner = "compound hook install --harness oh-my-pi"
			}
			if verdicts[0].Harness != "codex" || verdicts[0].Status != statusRefreshReady ||
				verdicts[1].Harness != external || verdicts[1].Status != StatusError ||
				!strings.Contains(verdicts[1].Detail, owner) {
				t.Fatalf("refusal did not preserve the external owner boundary: %+v", verdicts)
			}
			for path, before := range map[string][]byte{configPath: originalConfig, manifestPath: originalManifest} {
				after, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(before, after) {
					t.Errorf("unsupported adapter %s mutated preimage %s before refusing: %v", external, path, readErr)
				}
			}
			if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0640 {
				t.Fatalf("native config mode changed: %v %v", info, err)
			}
			if _, err := os.Stat(contract.InstalledHooksSourceAuthorityPath(homePath)); !os.IsNotExist(err) {
				t.Fatalf("preflight created installed hook authority: %v", err)
			}
			if _, err := os.Stat(filepath.Join(homePath, "context")); !os.IsNotExist(err) {
				t.Fatalf("preflight materialized instruction context: %v", err)
			}
		})
	}
}

func TestRefreshPreflightCLIIsReadOnlyAndDoesNotClaimActivation(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), "absent-home")
	baseDir := filepath.Join(t.TempDir(), "absent-profile")
	binaryPath := filepath.Join(t.TempDir(), "compound-synthetic")
	args := []string{"--preflight", "--home", homePath, "--base", baseDir, "--binary", binaryPath}
	if err := RunCLIFromArgs(args, time.Now().UTC()); err != nil {
		t.Fatalf("cold-start preflight: %v", err)
	}
	if err := RunCLIFromArgs(append(args, "--harness", "codex"), time.Now().UTC()); err != nil {
		t.Fatalf("missing native config is eligible for later creation: %v", err)
	}
	for _, path := range []string{homePath, baseDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("read-only preflight created %s: %v", path, err)
		}
	}
	if err := home.Init(homePath, home.HomeKindGlobal, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeInstalledHooksManifest(t, homePath, map[string]string{"codex": "binary:synthetic", "opencode": "binary:synthetic"})
	if err := RunCLIFromArgs(args, time.Now().UTC()); err == nil {
		t.Fatal("all-lane preflight silently accepted unsupported adapter")
	}
	if err := RunCLIFromArgs(args[1:], time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "opencode") {
		t.Fatalf("full refresh did not identify the external blocker: %v", err)
	}
	if err := RunCLIFromArgs(append(args, "--harness", "codex"), time.Now().UTC()); err != nil {
		t.Fatalf("explicit native lane was blocked by unrelated external entry: %v", err)
	}
	if v := preflightLaneRefresh(RefreshLaneOptions{HomePath: homePath, BaseDir: baseDir, BinaryPath: binaryPath}, "unrecognized"); v.Status != StatusError {
		t.Fatalf("unknown adapter accepted: %+v", v)
	}
}
