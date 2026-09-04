package hook

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/work"
)

// TestLiveUserPromptScaleStaysInsideCPUTimeBudget is the always-on sibling of the load-aware
// wall-clock test. While TestLiveUserPromptScaleStaysInsideNativeBudget SKIPS its hard
// assertion under heavy machine load (a contested CI runner can blow the wall-clock budget
// even with a perfect hook), this sibling asserts the SAME property in CPU time and never
// skips. The two together pin the hook's prompt latency: wall-clock below 1.2s p95 under
// quiet conditions, and CPU-time always below a per-NumCPU budget — the property is never
// entirely unguarded.
//
// Like its wall-clock sibling it uses fixedNow so seeded timestamps stay deterministic.
func TestLiveUserPromptScaleStaysInsideCPUTimeBudget(t *testing.T) {
	homePath := t.TempDir()
	seedPromptScaleMemory(t, homePath)
	seedPromptScaleEntityHistory(t, homePath)
	seedPromptScaleCapabilities(t, homePath)
	assertPromptScaleShape(t, homePath)

	embedder := newPromptScaleEmbedServer(t)
	t.Setenv("COMPOUND_BREADTH_EMBED_PROVIDER", "openai")
	t.Setenv("COMPOUND_BREADTH_EMBED_MODEL", "prompt-scale-test")
	t.Setenv("COMPOUND_BREADTH_EMBED_BASE_URL", embedder.server.URL)
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")

	if _, err := contract.MaterializeRuntime(contract.Input{
		Harness: "codex", Cwd: "/repo/compound", Task: "warm prompt runtime",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute", ReadOnlyRetrieval: true,
	}, contract.DefaultPolicy(), homePath, fixedNow); err != nil {
		t.Fatal(err)
	}
	embedder.resetTaskCounts()

	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	deps.Store = workStore
	if got := deps.Registry.InjectionBudgetMS("codex"); got != 1500 {
		t.Fatalf("codex injection budget = %dms, want pinned 1500ms", got)
	}

	cpuTimes := make([]time.Duration, 0, promptScaleRuns)
	for i := 0; i < promptScaleRuns; i++ {
		harness := "codex"
		if i%2 == 1 {
			harness = "claude_code"
		}
		raw := fmt.Sprintf("PROMPT_SCALE_CPU_%02d accelerate worldwide shipping", i)
		// promptScalePerPromptCPUTime samples Rusage around the closure so the measurement
		// covers exactly the hook invocation, not the surrounding assertions or seed I/O.
		var res Result
		var runErr error
		cpu := promptScalePerPromptCPUTime(func() {
			res, runErr = Run(Fixture{
				Hook: "user-prompt-submit", Harness: harness, Live: true,
				Cwd: "/repo/compound", RepoKind: "control-plane", Task: raw, Prompt: raw,
			}, deps, fixedNow.Add(time.Duration(i)*time.Second))
		})
		cpuTimes = append(cpuTimes, cpu)
		if runErr != nil {
			t.Fatalf("prompt %d (%s): %v", i, harness, runErr)
		}
		if res.BreakerTripped || res.Event.Status == "fail_open" {
			t.Fatalf("prompt %d (%s) tripped breaker: %+v", i, harness, res)
		}
	}

	sort.Slice(cpuTimes, func(i, j int) bool { return cpuTimes[i] < cpuTimes[j] })
	p95CPU := cpuTimes[int(float64(len(cpuTimes))*0.95)-1]
	maxCPU := cpuTimes[len(cpuTimes)-1]
	budget := promptScaleCPUTimeBudget()
	if p95CPU > budget {
		t.Fatalf("prompt p95 CPU time = %s, want <= %s; cpu_times=%v", p95CPU, budget, cpuTimes)
	}
	// Max budget is 2x p95 to leave headroom for one outlier under jitter.
	if maxCPU > 2*budget {
		t.Fatalf("prompt max CPU time = %s, want <= 2*%s; cpu_times=%v", maxCPU, budget, cpuTimes)
	}
	t.Logf("prompt scale CPU-time budget=%s p95=%s max=%s distribution=%v", budget, p95CPU, maxCPU, cpuTimes)
}
