package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/localmodel"
)

func TestExecutorsProjectionProbeLocalLMStudio(t *testing.T) {
	loaded := 4096
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			t.Fatalf("path = %q, want /api/v0/models", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id":                    "fixture-model",
				"state":                 "loaded",
				"max_context_length":    8192,
				"loaded_context_length": loaded,
			}},
		})
	}))
	defer server.Close()

	homePath := t.TempDir()
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := Open(homePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	projection, err := store.ExecutorsProjection(now, ExecutorsProjectionOptions{
		ProbeLocal: true,
		LMStudioClient: localmodel.LMStudioClient{
			BaseURL:    server.URL + "/v1",
			HTTPClient: server.Client(),
		},
	})
	if err != nil {
		t.Fatalf("executors projection: %v", err)
	}
	row := requireExecutorProjectionRow(t, projection, "local_lm_studio")
	if row.Status != "reachable" || row.StatusSource != "live_probe" || row.Tone != "green" {
		t.Fatalf("local LM Studio row = %+v, want reachable live probe", row)
	}
	if !strings.Contains(row.ProjectionNote, "1 model") {
		t.Fatalf("projection note = %q, want model count", row.ProjectionNote)
	}
}

func TestExecutorsProjectionDefaultDoesNotProbeLocalLMStudio(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := Open(homePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	projection, err := store.ExecutorsProjection(now, ExecutorsProjectionOptions{
		LMStudioClient: localmodel.LMStudioClient{BaseURL: "http://127.0.0.1:1/v1"},
	})
	if err != nil {
		t.Fatalf("executors projection: %v", err)
	}
	row := requireExecutorProjectionRow(t, projection, "local_lm_studio")
	if row.Status != "not_checked" || row.StatusSource != "profile_guide" {
		t.Fatalf("local LM Studio row = %+v, want static profile without probing", row)
	}
}

func requireExecutorProjectionRow(t *testing.T, projection ExecutorsProjection, id string) ExecutorRow {
	t.Helper()
	for _, row := range projection.Executors {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("missing executor row %q in %+v", id, projection.Executors)
	return ExecutorRow{}
}
