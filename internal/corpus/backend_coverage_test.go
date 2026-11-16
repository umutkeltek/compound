package corpus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIndexingHealthReportsCoverageCostAgeAndUnknownsWithoutIndexReads(t *testing.T) {
	hp := t.TempDir()
	now := time.Now().UTC()
	lane := LaneConfig{Lane: "fixture", SourceFamily: "fixture", Source: &SourceConfig{Root: t.TempDir(), Format: "markdown"}}
	dir := filepath.Dir(refreshPath(hp, lane))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, status, checked, indexed, want string
		pending                              int
		cost                                 bool
	}{
		{"healthy", "ok", now.Add(-time.Minute).Format(time.RFC3339Nano), now.Add(-2 * time.Minute).Format(time.RFC3339Nano), "ready", 0, true},
		{"stale", "ok", now.Add(-time.Hour).Format(time.RFC3339Nano), now.Add(-time.Hour).Format(time.RFC3339Nano), "stale", 0, true},
		{"stale_index", "ok", now.Format(time.RFC3339Nano), now.Add(-time.Hour).Format(time.RFC3339Nano), "stale", 0, true},
		{"no_index_stamp", "ok", now.Format(time.RFC3339Nano), "", "stale", 0, false},
		{"failed", "index_failed", now.Format(time.RFC3339Nano), "", "degraded", 0, false},
		{"pending", "backfilling", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), "degraded", 7, true},
		{"unknown_clock", "ok", "invalid", "", "stale", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"lane": "untrusted-identity", "status": tc.status, "checked_at": tc.checked, "indexed_at": tc.indexed, "pending_files": tc.pending}
			if tc.cost {
				raw["refresh_milliseconds"] = 42
				raw["body_bytes_read"] = 100
			}
			body, _ := json.Marshal(raw)
			if err := os.WriteFile(refreshPath(hp, lane), body, 0600); err != nil {
				t.Fatal(err)
			}
			// Invalid vector/manifest bytes cannot affect this metadata-only projection.
			_ = os.WriteFile(filepath.Join(dir, "vectors.json"), []byte("not vectors"), 0600)
			_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("not a manifest"), 0600)
			status, checks := indexingHealth(hp, []LaneConfig{lane}, now)
			if status != tc.want || len(checks) != 1 || checks[0].Lane != lane.Lane || checks[0].Status != tc.status {
				t.Fatalf("%s %+v", status, checks)
			}
			got := checks[0]
			if (got.RefreshMilliseconds != nil) != tc.cost || (got.CostStatus == "reported") != tc.cost {
				t.Fatalf("cost %+v", got)
			}
			if tc.indexed == "" && (got.IngestionLagSeconds != nil || got.IngestionLagStatus != "unknown") {
				t.Fatalf("invented age %+v", got)
			}
			if tc.name == "healthy" && (got.IngestionLagSeconds == nil || *got.IngestionLagSeconds != 120) {
				t.Fatalf("age %+v", got)
			}
		})
	}
	_ = os.Remove(refreshPath(hp, lane))
	if status, checks := indexingHealth(hp, []LaneConfig{lane}, now); status != "stale" || checks[0].Status != "never_refreshed" {
		t.Fatalf("%s %+v", status, checks)
	}
	_ = os.WriteFile(refreshPath(hp, lane), []byte("{broken"), 0600)
	if status, checks := indexingHealth(hp, []LaneConfig{lane}, now); status != "degraded" || checks[0].Status != "coverage_unavailable" {
		t.Fatalf("%s %+v", status, checks)
	}
}

func TestIndexingFailureNeverMakesHealthyBackendRecoverable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte("{\"models\":[{\"name\":\"fixture\"}]}"))
			return
		}
		_, _ = w.Write([]byte("{\"data\":[{\"embedding\":[1,2]}]}"))
	}))
	defer srv.Close()
	hp := t.TempDir()
	lane := LaneConfig{Lane: "fixture", SourceFamily: "fixture", Source: &SourceConfig{Root: t.TempDir(), Format: "markdown"},
		Embedder: EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 2, BaseURL: srv.URL}}
	_ = os.MkdirAll(filepath.Dir(refreshPath(hp, lane)), 0700)
	_ = os.WriteFile(refreshPath(hp, lane), []byte("{\"status\":\"index_failed\"}"), 0600)
	report := CheckBackends(context.Background(), hp, []LaneConfig{lane})
	if report.Status != "ready" || report.IndexingStatus != "degraded" || report.Backends[0].Status != "ready" {
		t.Fatalf("%+v", report)
	}
}
