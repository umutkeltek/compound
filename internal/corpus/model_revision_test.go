package corpus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"compound/internal/mem"
)

type revisionFixture struct {
	mu            sync.Mutex
	digest        string
	modelName     string
	failEmbedding bool
	missing       bool
	swapAt        int
	embeds        int
	tags          int
	server        *httptest.Server
}

func revisionServer(t *testing.T) *revisionFixture {
	t.Helper()
	f := &revisionFixture{digest: strings.Repeat("a", 64), modelName: "fixture"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/tags":
			f.tags++
			models := []map[string]string{}
			if !f.missing {
				models = append(models, map[string]string{"name": f.modelName + ":latest", "digest": f.digest})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
		case "/v1/embeddings":
			f.embeds++
			if f.failEmbedding {
				http.Error(w, "PRIVATE_INPUT_MUST_NOT_APPEAR", 500)
				return
			}
			if f.swapAt == f.embeds {
				f.digest = strings.Repeat("b", 64)
			}
			var req struct{ Input []string }
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			rows := []map[string]any{}
			weights := []float64{1, 2, 3}
			if f.digest != strings.Repeat("a", 64) {
				weights = []float64{3, 2, 1}
			}
			for i := range req.Input {
				rows = append(rows, map[string]any{"index": i, "embedding": weights})
			}
			if f.swapAt == f.embeds {
				f.digest = strings.Repeat("b", 64)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *revisionFixture) config() EmbedderConfig {
	return EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 3, BaseURL: f.server.URL + "/v1", ModelDigest: "sha256:" + strings.Repeat("a", 64)}
}
func (f *revisionFixture) change(digest string) { f.mu.Lock(); f.digest = digest; f.mu.Unlock() }
func (f *revisionFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.embeds, f.tags
}

func TestModelRevisionConfigPinsAreOptionalLocalAndIdentityStable(t *testing.T) {
	lane := LaneConfig{Lane: "fixture", SourceFamily: "fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 3}}
	original := lane.Embedder.Identity()
	lane.Embedder.ModelDigest = strings.Repeat("a", 64)
	normalized, err := ValidateLaneConfig(lane)
	if err != nil || normalized.Embedder.ModelDigest != "sha256:"+strings.Repeat("a", 64) || normalized.Embedder.Identity() != original {
		t.Fatalf("%+v %v", normalized, err)
	}
	lane.Embedder.ModelDigest = "not-a-digest"
	if _, err := ValidateLaneConfig(lane); err == nil {
		t.Fatal("invalid digest accepted")
	}
	lane.Embedder.ModelDigest = strings.Repeat("a", 64)
	lane.Embedder.Provider = "openai"
	if _, err := ValidateLaneConfig(lane); err == nil {
		t.Fatal("non-Ollama pin accepted")
	}
	lane.Embedder.Provider = "ollama"
	lane.Embedder.BaseURL = "https://backend.invalid"
	if _, err := ValidateLaneConfig(lane); err == nil {
		t.Fatal("remote pin accepted")
	}
}
func TestModelRevisionGuardRejectsChangedAndMissingBeforeEmbedding(t *testing.T) {
	for _, missing := range []bool{false, true} {
		f := revisionServer(t)
		cfg := f.config()
		f.change(strings.Repeat("b", 64))
		f.mu.Lock()
		f.missing = missing
		f.mu.Unlock()
		e, err := NewConfiguredEmbedder(LaneConfig{Embedder: cfg})
		if err != nil {
			t.Fatal(err)
		}
		vectors, err := e.Embed(context.Background(), []string{"PRIVATE_INPUT_MUST_NOT_APPEAR"})
		want := ErrModelChanged
		if missing {
			want = ErrModelMissing
		}
		if !errors.Is(err, want) || len(vectors) != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") {
			t.Fatalf("vectors=%v err=%v", vectors, err)
		}
		if count, _ := f.counts(); count != 0 {
			t.Fatal("embedding happened before revision refusal")
		}
	}
}
func TestModelRevisionGuardBracketsExistingRequestsWithoutExtraEmbeddings(t *testing.T) {
	f := revisionServer(t)
	cfg := f.config()
	e, err := NewConfiguredEmbedder(LaneConfig{Embedder: cfg})
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 65)
	got, err := e.Embed(context.Background(), texts)
	if err != nil || len(got) != 65 {
		t.Fatalf("%d %v", len(got), err)
	}
	embeds, tags := f.counts()
	if embeds != 3 || tags != 6 {
		t.Fatalf("embeddings=%d tags=%d", embeds, tags)
	}
	f.mu.Lock()
	f.swapAt = f.embeds + 1
	f.mu.Unlock()
	got, err = e.Embed(context.Background(), texts)
	if !errors.Is(err, ErrModelChanged) || got != nil {
		t.Fatalf("mixed batch returned: %v %v", got, err)
	}
	if count, _ := f.counts(); count != 4 {
		t.Fatal("requests continued after changed weights")
	}
}
func TestModelRevisionReadinessReportsActualDigestAndNoRestartClass(t *testing.T) {
	f := revisionServer(t)
	cfg := f.config()
	f.change(strings.Repeat("b", 64))
	got := checkBackend(context.Background(), f.server.URL, cfg)
	if got.Status != "model_changed" || got.ModelDigest != "sha256:"+strings.Repeat("b", 64) || got.ExpectedModelDigest != cfg.ModelDigest {
		t.Fatalf("%+v", got)
	}
	if n, _ := f.counts(); n != 0 {
		t.Fatal("wrong digest reached embedding endpoint")
	}
	f.mu.Lock()
	f.missing = true
	f.mu.Unlock()
	if got := checkBackend(context.Background(), f.server.URL, cfg); got.Status != "model_missing" {
		t.Fatalf("%+v", got)
	}
}
func TestModelRevisionProspectiveAdoptionAndExplicitRebind(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 3)
	first := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	if _, err := Index(context.Background(), hp, lane, first, IndexOptions{Incremental: true}); err != nil {
		t.Fatal(err)
	}
	f := revisionServer(t)
	lane.Embedder.BaseURL = f.server.URL
	f.mu.Lock()
	f.modelName = "synthetic"
	f.mu.Unlock()
	lane.Embedder.ModelDigest = "sha256:" + strings.Repeat("a", 64)
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	if _, err := QueryWithOptions(context.Background(), hp, lane, e, "fact", 5, QueryOptions{}); !errors.Is(err, ErrModelChanged) {
		t.Fatalf("unbound query %v", err)
	}
	adopted, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true})
	if err != nil || e.embedded != 0 || adopted.ReusedChunks != adopted.TotalChunks || adopted.BodyBytesRead != 0 || adopted.IndexBytesRead != 0 {
		t.Fatalf("%+v embeddings=%d err=%v", adopted, e.embedded, err)
	}
	manifest, _ := readManifest(IndexDir(hp, lane))
	identity, _ := readIdentity(IndexDir(hp, lane))
	if manifest.ModelDigest != lane.Embedder.ModelDigest || identity.ModelDigest != manifest.ModelDigest || manifest.ModelDigestBinding != "prospective" || identity.ModelDigestBinding != "prospective" {
		t.Fatalf("%+v %+v", manifest, identity)
	}
	again, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true})
	if err != nil || !again.Unchanged || e.embedded != 0 {
		t.Fatalf("%+v %v", again, err)
	}
	if _, err := QueryWithOptions(context.Background(), hp, lane, e, "fact", 5, QueryOptions{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(IndexDir(hp, lane), "manifest.json"))
	lane.Embedder.ModelDigest = "sha256:" + strings.Repeat("b", 64)
	f.change(strings.Repeat("b", 64))
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true}); !errors.Is(err, ErrModelChanged) {
		t.Fatalf("pin change reused vectors: %v", err)
	}
	if _, err := QueryWithOptions(context.Background(), hp, lane, e, "fact", 5, QueryOptions{}); !errors.Is(err, ErrModelChanged) {
		t.Fatalf("wrong binding query %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(IndexDir(hp, lane), "manifest.json"))
	if string(before) != string(after) {
		t.Fatal("refusal mutated binding")
	}
	e = &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true, Rebuild: true}); err != nil {
		t.Fatal(err)
	}
	manifest, _ = readManifest(IndexDir(hp, lane))
	if manifest.ModelDigest != lane.Embedder.ModelDigest || manifest.ModelDigestBinding != "verified_requests" || e.embedded == 0 {
		t.Fatalf("%+v count=%d", manifest, e.embedded)
	}
	lane.Embedder.ModelDigest = ""
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true}); !errors.Is(err, ErrModelChanged) {
		t.Fatalf("pin removal widened binding %v", err)
	}
}
func TestModelRevisionSwapNeverPersistsMixedBatches(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 70)
	f := revisionServer(t)
	lane.Embedder = f.config()
	e, err := NewConfiguredEmbedder(lane)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.swapAt = 3
	f.mu.Unlock()
	report, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true})
	if !errors.Is(err, ErrModelChanged) || report.EmbeddedChunks != 64 {
		t.Fatalf("%+v %v", report, err)
	}
	manifest, _ := readManifest(IndexDir(hp, lane))
	vectors, err := readVectors(IndexDir(hp, lane))
	if err != nil || len(vectors.Vectors) != 64 || manifest.VectorPending != 6 || manifest.ModelDigest != lane.Embedder.ModelDigest {
		t.Fatalf("vectors=%d manifest=%+v err=%v", len(vectors.Vectors), manifest, err)
	}
	for _, vector := range vectors.Vectors {
		if vector[0] >= vector[2] {
			t.Fatal("changed-weight vector persisted")
		}
	}
}

func TestModelRevisionPrivacyPreflightStillPrecedesInventoryAndEmbedding(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 1)
	f := revisionServer(t)
	lane.Embedder = f.config()
	lane.Privacy = "public"
	e, err := NewConfiguredEmbedder(lane)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true}); err == nil || !strings.Contains(err.Error(), "privacy") {
		t.Fatalf("privacy gate %v", err)
	}
	if embeds, tags := f.counts(); embeds != 0 || tags != 0 {
		t.Fatalf("privacy failure contacted model: %d %d", embeds, tags)
	}
}
func TestModelRevisionPinnedEmbeddingFailureDoesNotLeakInput(t *testing.T) {
	f := revisionServer(t)
	f.mu.Lock()
	f.failEmbedding = true
	f.mu.Unlock()
	e, err := NewConfiguredEmbedder(LaneConfig{Embedder: f.config()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"PRIVATE_INPUT_MUST_NOT_APPEAR"}); err == nil || strings.Contains(err.Error(), "PRIVATE_INPUT") {
		t.Fatalf("unsafe failure %v", err)
	}
}

func TestModelRevisionBoundQueryFailsReadOnlyWhenWeightsChange(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 2)
	f := revisionServer(t)
	lane.Embedder = f.config()
	e, err := NewConfiguredEmbedder(lane)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(IndexDir(hp, lane), "manifest.json")
	before, _ := os.ReadFile(path)
	count, _ := f.counts()
	f.change(strings.Repeat("b", 64))
	result, err := QueryWithOptions(context.Background(), hp, lane, e, "fact", 5, QueryOptions{})
	if err == nil || !strings.Contains(err.Error(), "model_changed") || len(result.Hits) != 0 {
		t.Fatalf("query hid model change: %+v %v", result, err)
	}
	if after, _ := f.counts(); after != count {
		t.Fatal("changed model received query text")
	}
	bytes, _ := os.ReadFile(path)
	if string(bytes) != string(before) {
		t.Fatal("read-only refusal changed binding")
	}
}

func TestModelRevisionQueryRechecksActualLoadedBinding(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 2)
	f := revisionServer(t)
	lane.Embedder = f.config()
	e, err := NewConfiguredEmbedder(lane)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true}); err != nil {
		t.Fatal(err)
	}
	count, _ := f.counts()
	original := runCorpusEngineQuery
	t.Cleanup(func() { runCorpusEngineQuery = original })
	runCorpusEngineQuery = func(engine *mem.ProtocolEngine, text string, k int) ([]mem.Hit, error) {
		dir := IndexDir(hp, lane)
		identity, err := readIdentity(dir)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		identity.ModelDigest = "sha256:" + strings.Repeat("b", 64)
		manifest.ModelDigest = identity.ModelDigest
		if err := writeJSONFile(filepath.Join(dir, "identity.json"), identity); err != nil {
			t.Fatal(err)
		}
		if err := writeJSONFile(filepath.Join(dir, "manifest.json"), manifest); err != nil {
			t.Fatal(err)
		}
		return original(engine, text, k)
	}
	result, err := QueryWithOptions(context.Background(), hp, lane, e, "fact", 5, QueryOptions{})
	if err == nil || !strings.Contains(err.Error(), "model_changed") || len(result.Hits) != 0 {
		t.Fatalf("query consumed replaced binding: %+v %v", result, err)
	}
	if after, _ := f.counts(); after != count {
		t.Fatal("replaced index received query embedding")
	}
}
func TestModelRevisionDefaultIndexMigratesLegacyVectorsToImmutableShards(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 2)
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{}); err != nil {
		t.Fatal(err)
	}
	f := revisionServer(t)
	f.mu.Lock()
	f.modelName = "synthetic"
	f.mu.Unlock()
	lane.Embedder.BaseURL = f.server.URL
	lane.Embedder.ModelDigest = "sha256:" + strings.Repeat("a", 64)
	e = &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	report, err := Index(context.Background(), hp, lane, e, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(IndexDir(hp, lane))
	if err != nil || m.VectorShards == nil || m.ModelDigestBinding != "prospective" || e.embedded != 0 || report.ReusedChunks != report.TotalChunks {
		t.Fatalf("manifest=%+v report=%+v embeds=%d err=%v", m, report, e.embedded, err)
	}
	if _, err := QueryWithOptions(context.Background(), hp, lane, e, "fact", 5, QueryOptions{}); err != nil {
		t.Fatal(err)
	}
}
