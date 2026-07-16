package hook

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/mem"
	"compound/internal/work"
)

const (
	promptScaleClaimCount = 632
	promptScaleNodeCount  = 670
	promptScaleRuns       = 10
	promptScaleTarget     = "SCALE_TARGET_MEANING"
)

func TestLiveUserPromptScaleStaysInsideNativeBudget(t *testing.T) {
	homePath := t.TempDir()
	seedPromptScaleMemory(t, homePath)
	seedPromptScaleCapabilities(t, homePath)

	embedder := newPromptScaleEmbedServer(t)
	t.Setenv("COMPOUND_BREADTH_EMBED_PROVIDER", "openai")
	t.Setenv("COMPOUND_BREADTH_EMBED_MODEL", "prompt-scale-test")
	t.Setenv("COMPOUND_BREADTH_EMBED_BASE_URL", embedder.server.URL)
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")

	// Warm the real production materialization path outside the native breaker.
	if _, err := contract.MaterializeRuntime(contract.Input{
		Harness: "codex", Cwd: "/repo/compound", Task: "warm prompt runtime",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute", ReadOnlyRetrieval: true,
	}, contract.DefaultPolicy(), homePath, fixedNow); err != nil {
		t.Fatal(err)
	}
	cacheTimes := promptScaleCacheTimes(t, homePath)
	embedder.resetTaskCounts()

	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := workStore.List()
	if err != nil {
		t.Fatal(err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	deps.Store = workStore
	if got := deps.Registry.InjectionBudgetMS("codex"); got != 1500 {
		t.Fatalf("codex injection budget = %dms, want pinned 1500ms", got)
	}
	if got := deps.Registry.InjectionBudgetMS("claude-code"); got != 1500 {
		t.Fatalf("claude injection budget = %dms, want pinned 1500ms", got)
	}

	latencies := make([]time.Duration, 0, promptScaleRuns)
	for i := 0; i < promptScaleRuns; i++ {
		harness := "codex"
		if i%2 == 1 {
			harness = "claude-code"
		}
		raw := fmt.Sprintf("PROMPT_SCALE_QUERY_%02d accelerate worldwide shipping", i)
		started := time.Now()
		res, err := Run(Fixture{
			Hook: "user-prompt-submit", Harness: harness, Live: true,
			Cwd: "/repo/compound", RepoKind: "control-plane", Task: raw, Prompt: raw,
		}, deps, fixedNow.Add(time.Duration(i)*time.Second))
		elapsed := time.Since(started)
		latencies = append(latencies, elapsed)
		if err != nil {
			t.Fatalf("prompt %d (%s): %v", i, harness, err)
		}
		if res.BreakerTripped || res.Event.Status == "fail_open" {
			t.Fatalf("prompt %d (%s) tripped breaker: %+v", i, harness, res)
		}
		if res.Event.IDs.WorkID != "" || len(res.Produced) != 3 {
			t.Fatalf("prompt %d created work or wrong spine: work=%q produced=%v", i, res.Event.IDs.WorkID, kinds(res.Produced))
		}
		if res.Injection == nil {
			t.Fatalf("prompt %d missing persisted injection", i)
		}
		persisted, err := os.ReadFile(res.Injection.Path)
		if err != nil {
			t.Fatal(err)
		}
		if res.ContextOutput != string(persisted) {
			t.Fatalf("prompt %d returned bytes differ from persisted injection", i)
		}
		for _, want := range []string{promptScaleTarget, "capability://skill:scale-target"} {
			if !strings.Contains(res.ContextOutput, want) {
				t.Fatalf("prompt %d missing semantic-only meaning %q", i, want)
			}
		}
		assertPromptSentinelAbsentFromTree(t, homePath, raw)
	}

	after, err := workStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("live prompts changed work count %d -> %d", len(before), len(after))
	}
	for i := 0; i < promptScaleRuns; i++ {
		raw := fmt.Sprintf("PROMPT_SCALE_QUERY_%02d accelerate worldwide shipping", i)
		if got := embedder.taskCount(raw); got != 1 {
			t.Fatalf("prompt %d exact semantic task embeds = %d, want 1", i, got)
		}
	}
	for path, beforeTime := range cacheTimes {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(beforeTime) {
			t.Fatalf("warm prompt cache churned %s: before=%s after=%s", path, beforeTime, info.ModTime())
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[int(math.Ceil(float64(len(latencies))*0.95))-1]
	maxLatency := latencies[len(latencies)-1]
	if p95 > 1200*time.Millisecond {
		t.Fatalf("prompt p95 = %s, want <= 1.2s; latencies=%v", p95, latencies)
	}
	if maxLatency >= 1500*time.Millisecond {
		t.Fatalf("prompt max = %s, want < 1.5s; latencies=%v", maxLatency, latencies)
	}
	t.Logf("prompt scale latency p95=%s max=%s distribution=%v", p95, maxLatency, latencies)
}

func seedPromptScaleMemory(t *testing.T, homePath string) {
	t.Helper()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	candidatesIn := make([]mem.Candidate, 0, promptScaleClaimCount)
	candidatesIn = append(candidatesIn, mem.Candidate{
		Entity: "release-transit", Predicate: "architecture",
		Object:     promptScaleTarget + " deployment automation releases verified artifacts",
		Confidence: 0.99, Derivation: "deterministic", ClaimType: "architecture",
		PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: "trace_scale_target",
		SnippetHash: "sha256:scale-target", Visibility: "operational",
	})
	for i := 1; i < promptScaleClaimCount; i++ {
		candidatesIn = append(candidatesIn, mem.Candidate{
			Entity: fmt.Sprintf("scale-filler-%03d", i), Predicate: "status",
			Object:     fmt.Sprintf("mechanical corpus evidence parcel %03d", i),
			Confidence: 0.9, Derivation: "deterministic", ClaimType: "status",
			PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: fmt.Sprintf("trace_scale_%03d", i),
			SnippetHash: fmt.Sprintf("sha256:scale-%03d", i), Visibility: "operational",
		})
	}
	captured, err := store.Capture(candidatesIn, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for i, candidate := range captured {
		if _, err := store.Adjudicate(candidate.ID, candidates.Promote, "test", fixedNow.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("promote claim %d: %v", i, err)
		}
	}
	claims, err := store.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != promptScaleClaimCount {
		t.Fatalf("seeded claims = %d, want %d", len(claims), promptScaleClaimCount)
	}
}

func seedPromptScaleCapabilities(t *testing.T, homePath string) {
	t.Helper()
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(capgraph.Node{
		ID: "skill:scale-target", Kind: "skill", Name: promptScaleTarget + " Launch Transit",
		Triggers: []string{"deployment orchestration"}, Weight: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < promptScaleNodeCount; i++ {
		if err := store.Add(capgraph.Node{
			ID: fmt.Sprintf("skill:scale-filler-%03d", i), Kind: "skill",
			Name: fmt.Sprintf("Mechanical Filler %03d", i), Triggers: []string{fmt.Sprintf("filler capability %03d", i)}, Weight: 1,
		}); err != nil {
			t.Fatalf("add capability %d: %v", i, err)
		}
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.NodeCount != promptScaleNodeCount {
		t.Fatalf("seeded nodes = %d, want %d", stats.NodeCount, promptScaleNodeCount)
	}
}

type promptScaleEmbedServer struct {
	server *httptest.Server

	mu         sync.Mutex
	taskCounts map[string]int
	target     []float32
	filler     []float32
}

func newPromptScaleEmbedServer(t *testing.T) *promptScaleEmbedServer {
	t.Helper()
	server := &promptScaleEmbedServer{
		taskCounts: map[string]int{},
		target:     promptScaleVector(false),
		filler:     promptScaleVector(true),
	}
	server.server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.server.Close)
	return server
}

func (s *promptScaleEmbedServer) handle(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data := make([]map[string]any, 0, len(request.Input))
	for i, text := range request.Input {
		isTask := strings.HasPrefix(text, "PROMPT_SCALE_QUERY_") || text == "warm prompt runtime"
		isTarget := isTask || strings.Contains(text, promptScaleTarget)
		if isTask {
			s.mu.Lock()
			s.taskCounts[text]++
			s.mu.Unlock()
		}
		vector := s.filler
		if isTarget {
			vector = s.target
		}
		data = append(data, map[string]any{"index": i, "embedding": vector})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (s *promptScaleEmbedServer) resetTaskCounts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskCounts = map[string]int{}
}

func (s *promptScaleEmbedServer) taskCount(task string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taskCounts[task]
}

func promptScaleVector(alternate bool) []float32 {
	vector := make([]float32, 768)
	for i := range vector {
		value := float32((i%29)+1) / 29
		if alternate && i%2 == 1 {
			value = -value
		}
		vector[i] = value
	}
	return vector
}

func promptScaleCacheTimes(t *testing.T, homePath string) map[string]time.Time {
	t.Helper()
	wantNames := map[string]bool{"manifest.json": true, "vectors.bin": true, "vectors.json": true}
	times := map[string]time.Time{}
	for _, family := range []string{"breadth", "capgraph"} {
		root := filepath.Join(homePath, "indexes", family)
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !wantNames[entry.Name()] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			times[path] = info.ModTime()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(times) != 4 {
		t.Fatalf("semantic cache files = %d, want 4: %v", len(times), times)
	}
	return times
}
