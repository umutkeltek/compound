package corpus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestBackendReadinessNeverReportsURLSecrets(t *testing.T) {
	cfg := EmbedderConfig{Provider: "local", Model: "fixture", Dims: 2}
	unsafe := (&url.URL{Scheme: "http", Host: "backend.invalid", Path: "/v1", User: url.UserPassword("name", "secret"), RawQuery: "token=secret", Fragment: "secret"}).String()
	for _, base := range []string{unsafe, "http://%secret"} {
		got := checkBackend(context.Background(), base, cfg)
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "name:") {
			t.Fatalf("endpoint leaked: %s", raw)
		}
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if fields["recovery_eligible"] != false {
			t.Fatalf("unsafe endpoint eligible: %s", raw)
		}
	}
}
func TestBackendReadinessGenericLocalWithoutOllamaInventory(t *testing.T) {
	for _, code := range []int{404, 405, 501} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/tags" {
				w.WriteHeader(code)
				return
			}
			if r.URL.Path != "/v1/embeddings" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float64{1, 2}}}})
		}))
		cfg := EmbedderConfig{Provider: "local", Model: "fixture", Dims: 2}
		got := checkBackend(context.Background(), srv.URL+"/v1", cfg)
		srv.Close()
		raw, _ := json.Marshal(got)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if got.Status != "ready" || fields["recovery_eligible"] != false {
			t.Fatalf("generic backend rejected or restartable: %s", raw)
		}
	}
}

func TestBackendReadinessGenericHTTPSUsesNormalCertificateVerification(t *testing.T) {
	embeds := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(404)
			return
		}
		embeds++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float64{1, 2}}}})
	}))
	defer srv.Close()
	cfg := EmbedderConfig{Provider: "local", Model: "fixture", Dims: 2, BaseURL: srv.URL + "/v1"}
	if got := checkBackend(context.Background(), cfg.BaseURL, cfg); got.Status == "ready" || embeds != 0 || got.RecoveryEligible {
		t.Fatalf("untrusted TLS accepted: %+v", got)
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	defer transport.CloseIdleConnections()
	http.DefaultTransport = transport
	got := checkBackend(context.Background(), cfg.BaseURL, cfg)
	if got.Status != "ready" || got.RecoveryEligible || embeds != 1 {
		t.Fatalf("trusted TLS not usable: %+v embeds=%d", got, embeds)
	}
}

func TestBackendReadinessDistinguishesResponseTimeoutFromConnectionRefusal(t *testing.T) {
	for _, listening := range []bool{false, true} {
		name := "connection_refused"
		want := "process_unavailable"
		if listening {
			name = "response_timeout"
			want = "process_unhealthy"
		}
		t.Run(name, func(t *testing.T) {
			arrived := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/tags" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				close(arrived)
				// A real accepted request whose server never sends a response header.
				<-r.Context().Done()
			}))
			t.Cleanup(srv.Close)
			if !listening {
				srv.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			got := checkBackend(ctx, srv.URL, EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 2})
			if listening {
				select {
				case <-arrived:
				default:
					t.Fatal("response timeout was not observed after accepting the request")
				}
			}
			if got.Status != want {
				t.Fatalf("listening=%v check=%+v want=%s", listening, got, want)
			}
		})
	}
}
