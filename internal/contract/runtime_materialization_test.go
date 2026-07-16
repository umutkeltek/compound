package contract

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"compound/internal/mem"
)

type countingRuntimeEngine struct {
	queryCalls int
}

func (e *countingRuntimeEngine) Capabilities() mem.EngineCapabilities {
	return mem.EngineCapabilities{Name: "counting-runtime", Breadth: true}
}

func (e *countingRuntimeEngine) Index([]mem.Claim, int64) error { return nil }

func (e *countingRuntimeEngine) Query(string, string, []string, int) ([]mem.Hit, error) {
	e.queryCalls++
	return nil, nil
}

func TestMaterializeRuntimeProjectsOneSharedSnapshot(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC)
	writeLease(t, home, "work_runtime_materialization", "codex-test", now.Add(-time.Minute))

	engine := &countingRuntimeEngine{}
	priorFactory := breadthEngineFactory
	breadthEngineFactory = func(string) (mem.Engine, func()) { return engine, func() {} }
	t.Cleanup(func() { breadthEngineFactory = priorFactory })
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "counting-runtime")

	in := Input{
		Harness: "codex", Cwd: "/repo/product", Task: "shared runtime materialization",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}
	got, err := MaterializeRuntime(in, DefaultPolicy(), home, now)
	if err != nil {
		t.Fatal(err)
	}
	if engine.queryCalls != 1 {
		t.Fatalf("shared materialization breadth queries = %d, want 1", engine.queryCalls)
	}
	if got.ContextPack.Peers == nil || len(got.ContextPack.Peers.ActiveLeases) != 1 {
		t.Fatalf("combined pack lost pack-only peers projection: %+v", got.ContextPack.Peers)
	}

	engine.queryCalls = 0
	wantContract, err := CompileWithRuntime(in, DefaultPolicy(), home, now)
	if err != nil {
		t.Fatal(err)
	}
	if engine.queryCalls != 1 {
		t.Fatalf("CompileWithRuntime breadth queries = %d, want 1", engine.queryCalls)
	}
	engine.queryCalls = 0
	wantPack, wantTrace, err := PackWithCapabilitiesTraced(in, DefaultPolicy(), home, now)
	if err != nil {
		t.Fatal(err)
	}
	if engine.queryCalls != 1 {
		t.Fatalf("PackWithCapabilitiesTraced breadth queries = %d, want 1", engine.queryCalls)
	}

	assertRuntimeJSONEqual(t, "effective contract", got.EffectiveContract, wantContract)
	assertRuntimeJSONEqual(t, "context pack", got.ContextPack, wantPack)
	if !reflect.DeepEqual(got.EngineDependencyTrace, wantTrace) {
		t.Fatalf("engine trace drift:\ngot  %+v\nwant %+v", got.EngineDependencyTrace, wantTrace)
	}
}

func assertRuntimeJSONEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s bytes drifted:\ngot  %s\nwant %s", label, gotJSON, wantJSON)
	}
}
