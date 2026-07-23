package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/authority"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/mem"
)

func TestMaterializeOrientContextPackUsesOneSharedRuntimeSnapshot(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	in := contract.Input{
		Harness: "codex", Cwd: t.TempDir(), Task: "shared orient materialization",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}

	prior := materializeRuntimeForOrient
	calls := 0
	materializeRuntimeForOrient = func(in contract.Input, pol contract.OrientationPolicy, homePath string, now time.Time) (contract.RuntimeMaterialization, error) {
		calls++
		return contract.RuntimeMaterialization{
			EffectiveContract: contract.Compile(in, pol, now),
			ContextPack:       contract.Pack(in, pol, now),
		}, nil
	}
	t.Cleanup(func() { materializeRuntimeForOrient = prior })

	pack, err := materializeOrientContextPack(homePath, in, 0, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("runtime materializations = %d, want one shared snapshot", calls)
	}
	if pack.ID == "" || pack.IDs.ContractID == "" {
		t.Fatalf("recorded pack lost attributable identity: %+v", pack.IDs)
	}
	if got := orientSourceRevision(pack.SourceRevisions, authority.InstalledHarnessEntrypointRevisionPrefix+"codex"); got != "" {
		t.Fatalf("public clone without installed-harness marker must omit entrypoint binding, got %q", got)
	}
}

func TestMaterializeOrientContextPackDoesNotPersistPartialPairOnPrecommitErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bodyBudget int
		mutate     func(*contract.RuntimeMaterialization)
		bodyError  bool
	}{
		{name: "id mismatch", mutate: func(runtime *contract.RuntimeMaterialization) {
			runtime.ContextPack.IDs.ContractID = "contract_mismatch"
		}},
		{name: "body error", bodyBudget: 1, bodyError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", cliTestNow); err != nil {
				t.Fatal(err)
			}
			in := contract.Input{Harness: "codex", Cwd: t.TempDir(), Task: tc.name, RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
			priorRuntime, priorBodies := materializeRuntimeForOrient, materializeBodiesForOrient
			var produced contract.RuntimeMaterialization
			materializeRuntimeForOrient = func(in contract.Input, pol contract.OrientationPolicy, homePath string, now time.Time) (contract.RuntimeMaterialization, error) {
				produced = contract.RuntimeMaterialization{EffectiveContract: contract.Compile(in, pol, now), ContextPack: contract.Pack(in, pol, now)}
				if tc.mutate != nil {
					tc.mutate(&produced)
				}
				return produced, nil
			}
			if tc.bodyError {
				materializeBodiesForOrient = func(string, envelope.ContextPack, string, string, int) (envelope.ContextPack, error) {
					return envelope.ContextPack{}, fmt.Errorf("injected body error")
				}
			}
			t.Cleanup(func() { materializeRuntimeForOrient, materializeBodiesForOrient = priorRuntime, priorBodies })
			if _, err := materializeOrientContextPack(homePath, in, tc.bodyBudget, cliTestNow); err == nil {
				t.Fatal("expected precommit error")
			}
			assertOrientPairAbsent(t, homePath, produced)
		})
	}
}

func assertOrientPairAbsent(t *testing.T, homePath string, runtime contract.RuntimeMaterialization) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(homePath, "contracts", "effective", cliTestNow.Format("2006-01-02"), runtime.EffectiveContract.ID+".json"),
		filepath.Join(homePath, "contracts", "effective", "latest.json"),
		filepath.Join(homePath, "context", "packs", runtime.ContextPack.ID+".json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("precommit error left artifact %s: %v", path, err)
		}
	}
}

func TestMaterializeOrientContextPackBindsInstalledEntrypointIntoBothArtifacts(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	installedHome := t.TempDir()
	entrypointPath := filepath.Join(installedHome, ".codex", "AGENTS.md")
	entrypointBody := []byte("# exact installed codex entrypoint\n")
	if err := os.MkdirAll(filepath.Dir(entrypointPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypointPath, entrypointBody, 0o644); err != nil {
		t.Fatal(err)
	}
	seedOrientInstalledHarnessHome(t, homePath, installedHome)

	in := contract.Input{
		Harness: "codex", Cwd: t.TempDir(), Task: "bind installed entrypoint",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}
	pack, err := materializeOrientContextPack(homePath, in, 0, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	wantRevision := fmt.Sprintf("sha256:%x", sha256.Sum256(entrypointBody))
	source := authority.InstalledHarnessEntrypointRevisionPrefix + "codex"
	if got := orientSourceRevision(pack.SourceRevisions, source); got != wantRevision {
		t.Fatalf("ContextPack entrypoint revision = %q, want %q", got, wantRevision)
	}

	contractPath := filepath.Join(
		homePath,
		"contracts",
		"effective",
		cliTestNow.UTC().Format("2006-01-02"),
		pack.IDs.ContractID+".json",
	)
	body, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var effective envelope.EffectiveContract
	if err := json.Unmarshal(body, &effective); err != nil {
		t.Fatal(err)
	}
	if got := orientSourceRevision(effective.SourceRevisions, source); got != wantRevision {
		t.Fatalf("EffectiveContract entrypoint revision = %q, want %q", got, wantRevision)
	}
}

func TestMaterializeOrientContextPackBindsClaudeFamilyAliasesCanonically(t *testing.T) {
	for _, harness := range []string{"claude-code", "claude_lane_opus"} {
		t.Run(harness, func(t *testing.T) {
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", cliTestNow); err != nil {
				t.Fatal(err)
			}
			installedHome := t.TempDir()
			entrypoint := filepath.Join(installedHome, ".claude", "CLAUDE.md")
			if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(entrypoint, []byte("claude"), 0o644); err != nil {
				t.Fatal(err)
			}
			seedOrientInstalledHarnessHome(t, homePath, installedHome)
			pack, err := materializeOrientContextPack(homePath, contract.Input{Harness: harness, Cwd: t.TempDir(), Task: "alias", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}, 0, cliTestNow)
			if err != nil {
				t.Fatal(err)
			}
			if got := orientSourceRevision(pack.SourceRevisions, authority.InstalledHarnessEntrypointRevisionPrefix+"claude"); got == "" {
				t.Fatalf("canonical claude source missing: %+v", pack.SourceRevisions)
			}
			body, err := os.ReadFile(filepath.Join(homePath, "contracts", "effective", cliTestNow.Format("2006-01-02"), pack.IDs.ContractID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var effective envelope.EffectiveContract
			if err := json.Unmarshal(body, &effective); err != nil {
				t.Fatal(err)
			}
			if effective.Harness != harness {
				t.Fatalf("artifact harness = %q, want preserved alias %q", effective.Harness, harness)
			}
		})
	}
}

func TestMaterializeOrientContextPackFailsWhenInstalledEntrypointMissing(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	installedHome := t.TempDir()
	seedOrientInstalledHarnessHome(t, homePath, installedHome)

	_, err := materializeOrientContextPack(homePath, contract.Input{
		Harness: "codex", Cwd: t.TempDir(), Task: "missing installed entrypoint",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}, 0, cliTestNow)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("promoted installed-harness marker must fail closed on missing exact entrypoint, got %v", err)
	}
}

func seedOrientInstalledHarnessHome(t *testing.T, homePath, installedHome string) {
	t.Helper()
	registryPath := filepath.Join(t.TempDir(), "os-path-authority.yaml")
	body := []byte("version: 1\ncanonical_roots:\n  installed_harness_home:\n    path: " + installedHome + "\n    status: active\n")
	if err := os.WriteFile(registryPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := store.Entities()
	if err != nil {
		t.Fatal(err)
	}
	_, entities, facts, _, err := authority.ImportOSPathRegistry(registryPath, existing)
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range entities {
		if _, err := store.RegisterEntity(entity, cliTestNow); err != nil {
			t.Fatal(err)
		}
	}
	captured, err := store.Capture(facts, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range captured {
		if _, err := store.Adjudicate(candidate.ID, "promote", "orient installed entrypoint fixture", cliTestNow); err != nil {
			t.Fatal(err)
		}
	}
}

func orientSourceRevision(revisions []envelope.SourceRev, source string) string {
	for _, revision := range revisions {
		if revision.Source == source {
			return revision.Rev
		}
	}
	return ""
}
