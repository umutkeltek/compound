package contract

import (
	"testing"

	"compound/internal/mem"
)

type unavailableTraceEngine struct{}

func (unavailableTraceEngine) Capabilities() mem.EngineCapabilities {
	return mem.EngineCapabilities{Name: "protocol", Breadth: true}
}

type invalidNameTraceEngine struct{}

func (invalidNameTraceEngine) Capabilities() mem.EngineCapabilities {
	return mem.EngineCapabilities{Name: "invalid/engine", Breadth: true, Embedder: "test:embedder:3:f32"}
}

func (invalidNameTraceEngine) Index([]mem.Claim, int64) error { return nil }

func (invalidNameTraceEngine) Query(string, string, []string, int) ([]mem.Hit, error) {
	return nil, nil
}

func (unavailableTraceEngine) Index([]mem.Claim, int64) error {
	return mem.ErrEngineUnavailable
}

func (unavailableTraceEngine) Query(string, string, []string, int) ([]mem.Hit, error) {
	return nil, mem.ErrEngineUnavailable
}

func TestPackWithCapabilitiesTracedRecordsBrokenConfiguredEngine(t *testing.T) {
	homePath := t.TempDir()
	original := breadthEngineFactory
	breadthEngineFactory = func(string) (mem.Engine, func()) {
		return unavailableTraceEngine{}, func() {}
	}
	t.Cleanup(func() { breadthEngineFactory = original })
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "opaque-test-command")
	t.Setenv("COMPOUND_MEMORY_CLI_PROFILE", "gbrain")

	_, trace, err := PackWithCapabilitiesTraced(Input{
		Harness:  "codex",
		Cwd:      "/workspace/synthetic",
		Task:     "trace the configured breadth dependency",
		RepoKind: "product",
		Mode:     "execute",
	}, DefaultPolicy(), homePath, fixedNow)
	if err != nil {
		t.Fatalf("orient must preserve its fail-open behavior: %v", err)
	}
	if !trace.Attempted || trace.RequestedEngine != "gbrain" {
		t.Fatalf("trace attempt/request = %+v, want attempted gbrain", trace)
	}
	if trace.ObservedEngine != "" {
		t.Fatalf("failure before real capabilities must not claim observed engine: %+v", trace)
	}
	if trace.Outcome != EngineOutcomeDegraded || trace.ErrorClass != EngineErrorUnavailable {
		t.Fatalf("trace outcome/error = %+v, want degraded/unavailable", trace)
	}
	if trace.Embedder != "" || trace.CorpusGeneration != 0 {
		t.Fatalf("failed engine must not invent capability evidence: %+v", trace)
	}
}

func TestPackWithCapabilitiesTracedRefusesUnreportableEngineIdentity(t *testing.T) {
	homePath := t.TempDir()
	original := breadthEngineFactory
	breadthEngineFactory = func(string) (mem.Engine, func()) {
		return invalidNameTraceEngine{}, func() {}
	}
	t.Cleanup(func() { breadthEngineFactory = original })
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "opaque-test-command")

	_, trace, err := PackWithCapabilitiesTraced(Input{
		Harness:  "codex",
		Cwd:      "/workspace/synthetic",
		Task:     "reject an unreportable engine identity",
		RepoKind: "product",
		Mode:     "execute",
	}, DefaultPolicy(), homePath, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !trace.Attempted || trace.ObservedEngine != "" || trace.Outcome != EngineOutcomeRefused || trace.ErrorClass != EngineErrorProtocolVersion {
		t.Fatalf("invalid-name trace = %+v, want bounded protocol refusal", trace)
	}
}
