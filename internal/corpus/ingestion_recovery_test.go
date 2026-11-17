package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

func TestConfiguredIngestionSurvivesColdRequestAndHonorsCallerDeadline(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests++
		call := requests
		mu.Unlock()
		if call == 1 {
			if len(payload.Input) != configuredEmbedBatch || len(payload.Input[0]) != 4000 {
				t.Errorf("realistic ingestion batch changed: %d", len(payload.Input))
			}
			select {
			case <-time.After(11 * time.Second):
			case <-r.Context().Done():
				return
			}
		} else {
			<-r.Context().Done()
			return
		}
		data := make([]map[string]any, len(payload.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	lane := LaneConfig{Embedder: EmbedderConfig{Provider: "local", Model: "synthetic", Dims: 3, BaseURL: server.URL}}
	e, err := NewConfiguredEmbedder(lane)
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, configuredEmbedBatch)
	for i := range texts {
		texts[i] = strings.Repeat("x", 4000)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	vectors, err := e.Embed(ctx, texts)
	cancel()
	if err != nil || len(vectors) != len(texts) {
		t.Fatalf("cold local request still uses interactive timeout: %d %v", len(vectors), err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
	_, err = e.Embed(ctx, texts[:1])
	cancel()
	if err == nil || ctx.Err() == nil {
		t.Fatal("bounded caller cancellation was hidden")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("failed request silently retried: %d", requests)
	}
}

func TestRefreshRetainsSuccessfulHTTPBatchAndFailureCosts(t *testing.T) {
	hp, root, lane := incrementalFixture(t, 65)
	lane.Chunk = ChunkConfig{Size: 4000}
	lane.Source = &SourceConfig{Root: root, Format: "markdown"}
	var mu sync.Mutex
	calls := 0
	succeeded := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		calls++
		call := calls
		if call != 2 {
			for _, text := range payload.Input {
				succeeded[text]++
			}
		}
		mu.Unlock()
		if call == 2 {
			http.Error(w, "synthetic provider failure", http.StatusServiceUnavailable)
			return
		}
		data := make([]map[string]any, len(payload.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0, 0}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	lane.Embedder.BaseURL = server.URL
	factory := func(l LaneConfig) (Embedder, error) { return NewConfiguredEmbedder(l) }
	first := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	c := first.Sources[0]
	if !first.Failed() || c.Status != "index_failed" || c.Error == "" || c.BodyBytesRead == 0 || c.PreparedItems != 65 || c.IndexBytesWritten == 0 || c.NewEmbeddedChunks != 32 || c.IndexPendingChunks != 33 || c.IndexedAt != "" {
		t.Fatalf("failed refresh hid completed work or certified success: %+v", c)
	}
	var progress ProgressFile
	err := readJSONFile(filepath.Join(IndexDir(hp, lane), "progress.json"), &progress)
	if err != nil {
		t.Fatal(err)
	}
	if progress.EmbeddedChunks != 32 || progress.PendingChunks != 33 || progress.Error == "" {
		t.Fatalf("successful HTTP batch was discarded: %+v", progress)
	}
	second := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	done := second.Sources[0]
	if second.Failed() || done.NewEmbeddedChunks != 33 || done.IndexPendingChunks != 0 || done.BodyBytesRead != 0 {
		t.Fatalf("retry did not resume exact checkpoint: %+v", done)
	}
	mu.Lock()
	defer mu.Unlock()
	for text, count := range succeeded {
		if count != 1 {
			t.Fatalf("already-successful embedding repeated %d times: %q", count, text)
		}
	}
	if len(succeeded) != 65 || calls != 4 {
		t.Fatalf("retry cost not bounded to failed/missing batches: texts=%d calls=%d", len(succeeded), calls)
	}
}

func TestRefreshPreparationFailurePreservesReadCosts(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	body := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"Cedar visible message\"}}\n{broken native record\n"
	if err := os.WriteFile(filepath.Join(root, "broken.jsonl"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	lane := LaneConfig{Lane: "partial-cost", SourceFamily: "partial_cost_fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "synthetic", Dims: 3}, Source: &SourceConfig{Root: root, Format: "claude"}}
	factory := func(l LaneConfig) (Embedder, error) {
		return &fakeEmbedder{identity: l.Embedder.Identity(), dim: l.Embedder.Dims}, nil
	}
	r := Refresh(context.Background(), hp, []LaneConfig{lane}, factory, time.Now())
	c := r.Sources[0]
	if !r.Failed() || c.Status != "index_failed" || c.BodyBytesRead != int64(len(body)) || c.PreparedItems != 1 || !strings.Contains(c.Error, "invalid transcript record 2") {
		t.Fatalf("preparation failure erased observed cost: %+v", c)
	}
	m, err := sourceobs.LoadSourceManifest(hp, lane.SourceFamily)
	if err != nil || len(m.Items) != 1 {
		t.Fatalf("raw custody was lost on parse failure: %v %+v", err, m)
	}
}

func TestTranscriptForeignMetadataIsDecodedOnlyByItsRecordFamily(t *testing.T) {
	// Metadata shapes are intentionally unlike the native SQL projection header.
	lines := []string{
		`{"type":"system","subtype":"api_error","level":"error","error":{"type":"synthetic"},"retryInMs":1000,"retryAttempt":1,"maxRetries":3,"source":"request_retry","timestamp":"2026-01-01T00:00:00Z"}`,
		`{"type":"user","timestamp":1234,"context":{"counter":1},"source":"cli","message":{"role":"user","content":"Cedar family-specific message"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","context":{"phase":["final"]},"message":{"role":"assistant","content":[{"type":"text","text":"Maple assistant message"}]}}`,
	}
	text, err := conversationText("claude", strings.Join(lines, "\n")+"\n")
	if err != nil || !strings.Contains(text, "Cedar family-specific message") || !strings.Contains(text, "Maple assistant message") || !strings.Contains(text, "1234 record 2") {
		t.Fatalf("foreign metadata poisoned visible messages: %q %v", text, err)
	}
	_, err = conversationText("claude", `{"type":"user","message":{"role":"user","content":{"not":"valid user content"}}}`)
	if err == nil {
		t.Fatal(fmt.Sprint("malformed semantic message was silently accepted"))
	}
}
