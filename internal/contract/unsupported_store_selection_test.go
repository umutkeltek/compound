package contract

import (
	"fmt"
	"strings"
	"testing"

	"compound/internal/breadth"
	"compound/internal/mem"
)

// File is the only store backend. The Postgres adapter was removed, and these
// tests pin what stays: selecting any other store, or leaving one of the
// removed adapter's variables set, is visible Degraded evidence on the recall
// trace and never falls through to file or breadth recall.

func syntheticRemovedStoreDSN() string {
	return strings.Join([]string{"postgres", "://", "synthetic", "@", "db.example.invalid", "/", "compound"}, "")
}

const (
	removedExperimentEnv = "COMPOUND_EXPERIMENTAL_POSTGRES"
	removedProfileEnv    = "COMPOUND_POSTGRES_PROFILE"
	removedDSNEnv        = "COMPOUND_DSN"
	removedVectorDimsEnv = "COMPOUND_VECTOR_DIMS"
)

func setStoreSelectionEnv(t *testing.T, store string, signalMask int) {
	t.Helper()
	t.Setenv("COMPOUND_STORE", store)
	t.Setenv(removedExperimentEnv, "")
	t.Setenv(removedProfileEnv, "")
	t.Setenv(removedDSNEnv, "")
	t.Setenv(removedVectorDimsEnv, "")
	if signalMask&1 != 0 {
		t.Setenv(removedExperimentEnv, "1")
	}
	if signalMask&2 != 0 {
		t.Setenv(removedProfileEnv, "local-lab")
	}
	if signalMask&4 != 0 {
		t.Setenv(removedDSNEnv, syntheticRemovedStoreDSN())
	}
	if signalMask&8 != 0 {
		t.Setenv(removedVectorDimsEnv, "1536")
	}
}

// countBreadthEngineCalls swaps the breadth engine factory for a counter.
func countBreadthEngineCalls(t *testing.T) *int {
	t.Helper()
	prior := breadthEngineFactory
	calls := 0
	breadthEngineFactory = func(string) (mem.Engine, func()) {
		calls++
		return unavailableTraceEngine{}, func() {}
	}
	t.Cleanup(func() { breadthEngineFactory = prior })
	return &calls
}

func orientForStoreSelection(t *testing.T, readOnly bool) EngineDependencyTrace {
	t.Helper()
	homePath := t.TempDir()
	seedTraceLedgerMemory(t, homePath)
	_, trace, err := PackWithCapabilitiesTraced(Input{
		Harness:           "codex",
		Cwd:               "/workspace/synthetic",
		Task:              "task retrieval memory provider posture",
		RepoKind:          "control-plane",
		Mode:              "execute",
		ReadOnlyRetrieval: readOnly,
	}, DefaultPolicy(), homePath, fixedNow)
	if err != nil {
		t.Fatalf("orient: %v", err)
	}
	return trace
}

func TestSelectedUnsupportedStoreUsesStableRequestedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		store      string
		signalMask int
	}{
		{name: "complete removed postgres gate", store: "postgres", signalMask: 1 | 2 | 4},
		{name: "unknown backend", store: "postgre"},
		{name: "stray opt in", signalMask: 1},
		{name: "stray profile on file", store: "file", signalMask: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setStoreSelectionEnv(t, tc.store, tc.signalMask)
			if got := requestedEngineIdentity(); got != unsupportedStoreIdentity {
				t.Fatalf("requestedEngineIdentity() = %q, want %q for a selected unsupported store", got, unsupportedStoreIdentity)
			}
		})
	}
}

func TestExternalStoreSelectionNeverFallsBackToFileRecall(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "file", value: "file", want: false},
		{name: "postgres", value: "postgres", want: true},
		{name: "unknown", value: "postgre", want: true},
		{name: "padded_file", value: " file ", want: true},
		{name: "padded_postgres", value: " postgres ", want: true},
		{name: "case_varied_file", value: "FILE", want: true},
		{name: "case_varied_postgres", value: "POSTGRES", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setStoreSelectionEnv(t, tc.value, 0)
			if got := externalStoreSelected(); got != tc.want {
				t.Fatalf("externalStoreSelected() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBreadthEmbedderConfigurationDoesNotSelectExternalStore(t *testing.T) {
	setStoreSelectionEnv(t, "", 0)
	t.Setenv("COMPOUND_EMBED_URL", "https://embed.example.invalid")
	t.Setenv("COMPOUND_EMBED_MODEL", "synthetic-embedder")

	if externalStoreSelected() {
		t.Fatal("breadth embedder configuration was misclassified as a store selection")
	}
}

func TestStrayRemovedStoreVariableCountsAsExternalSelection(t *testing.T) {
	for _, store := range []string{"", "file"} {
		storeName := store
		if storeName == "" {
			storeName = "unset"
		}
		for signalMask := 1; signalMask < 16; signalMask++ {
			t.Run(fmt.Sprintf("%s/signals_%04b", storeName, signalMask), func(t *testing.T) {
				setStoreSelectionEnv(t, store, signalMask)
				if !externalStoreSelected() {
					t.Fatal("a stray removed-adapter variable was classified as the file backend")
				}
			})
		}
	}
}

func TestUnknownStoreSelectionCannotFallBackToBreadthEngine(t *testing.T) {
	setStoreSelectionEnv(t, "postgre", 0)
	breadthCalls := countBreadthEngineCalls(t)

	trace := orientForStoreSelection(t, false)
	if *breadthCalls != 0 {
		t.Fatalf("a selected unsupported store fell through to the breadth engine %d time(s)", *breadthCalls)
	}
	if trace.RequestedEngine != unsupportedStoreIdentity {
		t.Fatalf("requested engine = %q, want %q", trace.RequestedEngine, unsupportedStoreIdentity)
	}
	if !trace.Attempted || trace.Outcome != EngineOutcomeDegraded || trace.ErrorClass != EngineErrorUnavailable {
		t.Fatalf("trace = %+v, want attempted degraded/unavailable", trace)
	}
}

func TestCompleteRemovedPostgresGateIsDegradedWithoutFallback(t *testing.T) {
	// The exact environment that used to open the Postgres adapter.
	setStoreSelectionEnv(t, "postgres", 1|2|4|8)
	breadthCalls := countBreadthEngineCalls(t)

	trace := orientForStoreSelection(t, false)
	if *breadthCalls != 0 {
		t.Fatalf("the removed Postgres selection fell through to the breadth engine %d time(s)", *breadthCalls)
	}
	if !trace.Attempted || trace.Outcome != EngineOutcomeDegraded || trace.ErrorClass != EngineErrorUnavailable {
		t.Fatalf("trace = %+v, want attempted degraded/unavailable", trace)
	}
}

func TestReadOnlyUnsupportedStoreSelectionIsNotReportedAsBreadth(t *testing.T) {
	setStoreSelectionEnv(t, "postgre", 0)

	priorResolver := breadthEmbedderResolver
	breadthEmbedderResolver = func(string) (breadth.EmbedderAdapter, bool) {
		return &capturedDependencyEmbedder{}, true
	}
	t.Cleanup(func() { breadthEmbedderResolver = priorResolver })

	trace := orientForStoreSelection(t, true)
	if trace.RequestedEngine != unsupportedStoreIdentity {
		t.Fatalf("requested engine = %q, want %q rather than the bypassed breadth lane", trace.RequestedEngine, unsupportedStoreIdentity)
	}
	if !trace.Attempted || trace.Outcome != EngineOutcomeDegraded || trace.ErrorClass != EngineErrorUnavailable {
		t.Fatalf("trace = %+v, want attempted degraded/unavailable", trace)
	}
}

func TestNotAttemptedRetrievalStillNamesSelectedUnsupportedStore(t *testing.T) {
	posture := retrievalPostureFromEngineTrace(EngineDependencyTrace{
		Outcome:         EngineOutcomeNotAttempted,
		RequestedEngine: unsupportedStoreIdentity,
	})
	if posture.RequestedEngine != unsupportedStoreIdentity {
		t.Fatalf("not-attempted posture requested engine = %q, want %q", posture.RequestedEngine, unsupportedStoreIdentity)
	}
	if posture.Fallback != "ledger_ranker" {
		t.Fatalf("not-attempted posture fallback = %q, want ledger_ranker", posture.Fallback)
	}
}

func TestStrayRemovedStoreVariableCannotFallBackToBreadthEngine(t *testing.T) {
	for _, store := range []string{"", "file"} {
		storeName := store
		if storeName == "" {
			storeName = "unset"
		}
		for signalMask := 1; signalMask < 16; signalMask++ {
			t.Run(fmt.Sprintf("%s/signals_%04b", storeName, signalMask), func(t *testing.T) {
				setStoreSelectionEnv(t, store, signalMask)
				breadthCalls := countBreadthEngineCalls(t)

				trace := orientForStoreSelection(t, false)
				if *breadthCalls != 0 {
					t.Fatalf("a stray removed-adapter variable fell through to the breadth engine %d time(s)", *breadthCalls)
				}
				if !trace.Attempted || trace.Outcome != EngineOutcomeDegraded || trace.ErrorClass != EngineErrorUnavailable {
					t.Fatalf("trace = %+v, want attempted degraded/unavailable", trace)
				}
			})
		}
	}
}

func TestCleanFileConfigurationRetainsBreadthEngine(t *testing.T) {
	for _, store := range []string{"", "file"} {
		storeName := store
		if storeName == "" {
			storeName = "unset"
		}
		t.Run(storeName, func(t *testing.T) {
			setStoreSelectionEnv(t, store, 0)
			t.Setenv("COMPOUND_MEM_ENGINE_BIN", "/synthetic/breadth-engine")
			breadthCalls := countBreadthEngineCalls(t)

			trace := orientForStoreSelection(t, false)
			if *breadthCalls == 0 {
				t.Fatal("clean file configuration did not reach the breadth engine")
			}
			if trace.RequestedEngine == unsupportedStoreIdentity {
				t.Fatalf("clean file configuration was reported as an unsupported store: %+v", trace)
			}
		})
	}
}
