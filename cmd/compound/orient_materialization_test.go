package main

import (
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/home"
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
}
