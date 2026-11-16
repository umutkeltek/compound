package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BackendHealth is a read-only readiness probe. Recovery belongs to the admitted
// memory-service child, never to search or embedder construction.
type BackendHealth struct {
	Status   string         `json:"status"`
	Backends []BackendCheck `json:"backends"`
}
type BackendCheck struct {
	Status   string `json:"status"`
	Identity string `json:"identity"`
	BaseURL  string `json:"base_url"`
	Detail   string `json:"detail,omitempty"`
}

func CheckBackends(ctx context.Context, lanes []LaneConfig) BackendHealth {
	report := BackendHealth{Status: "ready", Backends: []BackendCheck{}}
	seen := map[string]bool{}
	for _, lane := range lanes {
		cfg := lane.Embedder
		if cfg.Provider != "" && !strings.EqualFold(cfg.Provider, "ollama") && !strings.EqualFold(cfg.Provider, "local") {
			continue
		}
		base := strings.TrimRight(cfg.BaseURL, "/")
		if base == "" {
			base = "http://127.0.0.1:11434/v1"
		}
		key := base + "|" + cfg.Identity()
		if seen[key] {
			continue
		}
		seen[key] = true
		check := checkBackend(ctx, base, cfg)
		report.Backends = append(report.Backends, check)
		if check.Status != "ready" {
			report.Status = "backend_unavailable"
		}
	}
	return report
}

func checkBackend(ctx context.Context, base string, cfg EmbedderConfig) BackendCheck {
	check := BackendCheck{Status: "incompatible", Identity: cfg.Identity(), BaseURL: base}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback()) {
		check.Detail = "automatic recovery requires a loopback HTTP Ollama endpoint"
		return check
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, endpoint string, body []byte, out any) (int, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return resp.StatusCode, err
		}
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	origin := u.Scheme + "://" + u.Host
	status, err := request(http.MethodGet, origin+"/api/tags", nil, &tags)
	if err != nil {
		check.Status = "process_unavailable"
		if status != 0 {
			check.Status = "process_unhealthy"
		}
		check.Detail = "Ollama model inventory unavailable"
		return check
	}
	found := false
	for _, model := range tags.Models {
		for _, name := range []string{model.Name, model.Model} {
			if name == cfg.Model || (!strings.Contains(cfg.Model, ":") && name == cfg.Model+":latest") {
				found = true
			}
		}
	}
	if !found {
		check.Status, check.Detail = "model_missing", "configured model is absent; no automatic download"
		return check
	}
	raw, _ := json.Marshal(map[string]any{"model": cfg.Model, "input": []string{"memory readiness probe"}})
	var embedded struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	endpoint := base
	if !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}
	status, err = request(http.MethodPost, endpoint+"/embeddings", raw, &embedded)
	if err != nil {
		check.Status, check.Detail = "process_unhealthy", "configured model could not produce embeddings"
		if status == http.StatusNotFound {
			check.Status = "incompatible"
		}
		return check
	}
	if len(embedded.Data) != 1 || len(embedded.Data[0].Embedding) != cfg.Dims {
		check.Detail = "embedding dimensions do not match configured identity"
		return check
	}
	nonzero := false
	for _, n := range embedded.Data[0].Embedding {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			check.Detail = "embedding contains nonfinite values"
			return check
		}
		nonzero = nonzero || n != 0
	}
	if !nonzero {
		check.Detail = "embedding is empty or zero"
		return check
	}
	check.Status = "ready"
	return check
}
