package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	IndexingStatus string           `json:"indexing_status"`
	Indexing       []IndexingHealth `json:"indexing"`
	Status         string           `json:"status"`
	Backends       []BackendCheck   `json:"backends"`
}
type BackendCheck struct {
	RecoveryEligible    bool   `json:"recovery_eligible"`
	ModelDigest         string `json:"model_digest,omitempty"`
	ExpectedModelDigest string `json:"expected_model_digest,omitempty"`
	Status              string `json:"status"`
	Identity            string `json:"identity"`
	BaseURL             string `json:"base_url"`
	Detail              string `json:"detail,omitempty"`
}

func CheckBackends(ctx context.Context, homePath string, lanes []LaneConfig) BackendHealth {
	report := BackendHealth{Status: "ready", Backends: []BackendCheck{}}
	report.IndexingStatus, report.Indexing = indexingHealth(homePath, lanes, time.Now())
	seen := map[string]bool{}
	for _, lane := range lanes {
		cfg := lane.Embedder
		if cfg.Provider != "" && !strings.EqualFold(cfg.Provider, "ollama") && !strings.EqualFold(cfg.Provider, "local") {
			continue
		}
		base := strings.TrimRight(cfg.BaseURL, "/")
		if base == "" {
			base = (&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", "11434"), Path: "/v1"}).String()
		}
		key := base + "|" + cfg.Identity() + "|" + cfg.ModelDigest
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
	check := BackendCheck{Status: "incompatible", Identity: cfg.Identity(), BaseURL: "(invalid endpoint)", ExpectedModelDigest: cfg.ModelDigest}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		check.Detail = "invalid backend endpoint"
		return check
	}
	reported := *u
	reported.User, reported.RawQuery, reported.Fragment = nil, "", ""
	check.BaseURL = reported.String()
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		check.Detail = "backend endpoint credentials or parameters are unsupported"
		return check
	}
	loopbackHTTP := u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
	requireInventory := strings.EqualFold(cfg.Provider, "ollama") || cfg.ModelDigest != ""
	if requireInventory && !loopbackHTTP {
		check.Detail = "Ollama inventory requires a loopback HTTP endpoint"
		return check
	}
	// Readiness and service ownership are independent. Generic local APIs,
	// including HTTPS with ordinary certificate verification, are probeable.
	check.RecoveryEligible = loopbackHTTP && u.Port() == "11434" &&
		(strings.EqualFold(cfg.Provider, "ollama") || cfg.BaseURL == "")
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
			Name   string `json:"name"`
			Model  string `json:"model"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	origin := u.Scheme + "://" + u.Host
	status, err := request(http.MethodGet, origin+"/api/tags", nil, &tags)
	if err != nil && !requireInventory && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented || status == http.StatusOK) {
		// A generic OpenAI-compatible local server need not implement Ollama's
		// inventory. Unsupported inventory never attests a restart owner.
		check.RecoveryEligible = false
	} else {
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
			if modelNameMatches(model.Name, cfg.Model) || modelNameMatches(model.Model, cfg.Model) {
				found = true
				check.ModelDigest, _ = normalizeModelDigest(model.Digest)
			}
		}
		if !found {
			if requireInventory {
				check.Status, check.Detail = "model_missing", "configured model is absent; no automatic download"
				return check
			}
			check.RecoveryEligible = false
		}
		if cfg.ModelDigest != "" && check.ModelDigest != cfg.ModelDigest {
			check.Status = "model_changed"
			check.Detail = "configured weights differ; no automatic restart"
			return check
		}
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
	if cfg.ModelDigest != "" {
		if err := verifyModelDigest(ctx, cfg); err != nil {
			check.Status = "process_unhealthy"
			if errors.Is(err, ErrModelChanged) {
				check.Status = "model_changed"
			}
			if errors.Is(err, ErrModelMissing) {
				check.Status = "model_missing"
			}
			check.Detail = "model inventory changed during readiness probe"
			return check
		}
	}
	check.Status = "ready"
	return check
}
