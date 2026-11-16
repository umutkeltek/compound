package corpus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBackendReadinessClassifiesFailuresWithoutDownloading(t *testing.T) {
	for _, tc := range []struct {
		name, status            string
		inventory, embedding    any
		tagsStatus, embedStatus int
	}{
		{"ready", "ready", map[string]any{"models": []map[string]string{{"name": "fixture:latest"}}}, map[string]any{"data": []map[string]any{{"embedding": []float64{1, 2}}}}, 200, 200},
		{"missing", "model_missing", map[string]any{"models": []any{}}, nil, 200, 200},
		{"unhealthy", "process_unhealthy", nil, nil, 503, 200},
		{"bad_dimensions", "incompatible", map[string]any{"models": []map[string]string{{"name": "fixture"}}}, map[string]any{"data": []map[string]any{{"embedding": []float64{1}}}}, 200, 200},
		{"bad_api", "incompatible", map[string]any{"models": []map[string]string{{"name": "fixture"}}}, nil, 200, 404},
		{"load_failure", "process_unhealthy", map[string]any{"models": []map[string]string{{"name": "fixture"}}}, nil, 200, 500},
		{"zero_vector", "incompatible", map[string]any{"models": []map[string]string{{"name": "fixture"}}}, map[string]any{"data": []map[string]any{{"embedding": []float64{0, 0}}}}, 200, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Path {
				case "/api/tags":
					if r.Method != "GET" {
						t.Error("inventory mutated")
					}
					w.WriteHeader(tc.tagsStatus)
					_ = json.NewEncoder(w).Encode(tc.inventory)
				case "/v1/embeddings":
					if r.Method != "POST" {
						t.Error("wrong embedding method")
					}
					w.WriteHeader(tc.embedStatus)
					_ = json.NewEncoder(w).Encode(tc.embedding)
				default:
					t.Errorf("unexpected/download request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			cfg := EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 2}
			got := checkBackend(context.Background(), srv.URL+"/v1", cfg)
			if got.Status != tc.status || got.Identity != cfg.Identity() {
				t.Fatalf("%+v", got)
			}
			if calls > 2 {
				t.Fatalf("unbounded readiness calls %d", calls)
			}
		})
	}
}

func TestBackendReadinessUnavailableAndUnownedEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close()
	cfg := EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 2}
	if got := checkBackend(context.Background(), endpoint, cfg); got.Status != "process_unavailable" {
		t.Fatal(got)
	}
	if got := checkBackend(context.Background(), "https://backend.invalid/v1", cfg); got.Status != "incompatible" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := checkBackend(ctx, endpoint, cfg); got.Status != "process_unavailable" {
		t.Fatal(got)
	}
}

func TestMemoryServiceRecoveryFixtures(t *testing.T) {
	script := filepath.Join("..", "..", "scripts", "test_memory_service_health.py")
	cmd := exec.Command("python3", script)
	cmd.Env = append(cmd.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recovery fixtures: %v\n%s", err, out)
	}
}
