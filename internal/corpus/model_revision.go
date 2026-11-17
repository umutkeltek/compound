package corpus

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var ErrModelChanged = fmt.Errorf("%w: model_changed", ErrEmbedderMismatch)
var ErrModelMissing = errors.New("model_missing")

// ModelDigest pins model weights without changing the historical embedding
// identity or index directory. Legacy vectors can only be bound prospectively.
// model_digest_binding=prospective does not attest the weights that produced old
// cached vectors. Only an explicit rebuild replaces them with guarded requests.
// Before/after inventory checks detect observed model swaps, not an atomic or
// cryptographic attestation of provider internals (including a swap back between
// the two checks). Metadata belongs to the existing operator config and index;
// no new service, registry or recovery authority is introduced.
func normalizeModelDigest(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	value = strings.TrimPrefix(value, "sha256:")
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("model_digest must be a SHA-256 digest")
	}
	return "sha256:" + value, nil
}

func modelNameMatches(name, requested string) bool {
	return name == requested || (!strings.Contains(requested, ":") && name == requested+":latest")
}

func modelInventoryURL(cfg EmbedderConfig) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider != "local" && provider != "ollama" {
		return "", fmt.Errorf("model_digest requires local Ollama")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = (&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", "11434")}).String()
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback()) {
		return "", fmt.Errorf("model_digest requires loopback HTTP Ollama")
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/api/tags"}).String(), nil
}

func verifyModelDigest(ctx context.Context, cfg EmbedderConfig) error {
	if cfg.ModelDigest == "" {
		return nil
	}
	expected, err := normalizeModelDigest(cfg.ModelDigest)
	if err != nil {
		return err
	}
	endpoint, err := modelInventoryURL(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("model inventory unavailable")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("model inventory unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("model inventory unavailable: HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	var inventory struct {
		Models []struct {
			Name   string
			Model  string
			Digest string
		}
	}
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &inventory) != nil {
		return fmt.Errorf("model inventory unavailable")
	}
	for _, model := range inventory.Models {
		if !modelNameMatches(model.Name, cfg.Model) && !modelNameMatches(model.Model, cfg.Model) {
			continue
		}
		actual, e := normalizeModelDigest(model.Digest)
		if e != nil || actual == "" || actual != expected {
			return fmt.Errorf("%w: configured weights differ from local inventory", ErrModelChanged)
		}
		return nil
	}
	return fmt.Errorf("%w: configured local model is absent", ErrModelMissing)
}

func checkIndexModelDigest(dir string, lane LaneConfig, manifest Manifest, rebuild, query bool) error {
	if rebuild {
		return nil
	}
	identity, err := readIdentity(dir)
	if err != nil && !os.IsNotExist(err) {
		// A genuinely absent unbound legacy identity is handled by the existing
		// index/probe path. A configured pin may not masquerade as a bound query.
		return err
	}
	expected := lane.Embedder.ModelDigest
	if identity.ModelDigest != manifest.ModelDigest || (manifest.ModelDigest != "" && identity.ModelDigestBinding != manifest.ModelDigestBinding) {
		return fmt.Errorf("%w: index metadata digest mismatch; explicit --rebuild required", ErrModelChanged)
	}
	bound := manifest.ModelDigest
	if bound != "" && manifest.ModelDigestBinding != "prospective" && manifest.ModelDigestBinding != "verified_requests" {
		return fmt.Errorf("%w: index digest provenance missing; explicit --rebuild required", ErrModelChanged)
	}
	if bound != "" && bound != expected {
		return fmt.Errorf("%w: index pin changed or removed; explicit --rebuild required", ErrModelChanged)
	}
	if query && expected != "" && bound == "" {
		return fmt.Errorf("%w: legacy index needs prospective refresh adoption or explicit --rebuild", ErrModelChanged)
	}
	return nil
}

func modelDigestBinding(old Manifest, pin string, rebuild bool) string {
	if pin == "" {
		return ""
	}
	if !rebuild && (old.ModelDigestBinding == "prospective" || (old.ModelDigest == "" && len(old.Items) > 0)) {
		return "prospective"
	}
	return "verified_requests"
}
