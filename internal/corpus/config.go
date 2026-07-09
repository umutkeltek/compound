package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	IdentitySchemaVersion = "compound.corpus.identity.v0.1"
	ManifestSchemaVersion = "compound.corpus.manifest.v0.1"
	ProgressSchemaVersion = "compound.corpus.progress.v0.1"
	PrefixSchemaVersion   = "v1"
	defaultChunkUnit      = "runes"
	defaultChunkSize      = 4000
	defaultChunkOverlap   = 400
	defaultChunkBoundary  = "structure"
	defaultEmbedBits      = "f32"
)

var ErrEmbedderMismatch = errors.New("corpus embedder identity mismatch")

type LaneConfig struct {
	Lane         string         `json:"lane" yaml:"lane"`
	SourceFamily string         `json:"source_family" yaml:"source_family"`
	Generation   string         `json:"generation,omitempty" yaml:"generation,omitempty"`
	Privacy      string         `json:"privacy" yaml:"privacy"`
	Embedder     EmbedderConfig `json:"embedder" yaml:"embedder"`
	Chunk        ChunkConfig    `json:"chunk" yaml:"chunk"`
}

type EmbedderConfig struct {
	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
	Dims     int    `json:"dims" yaml:"dims"`
	Bits     string `json:"bits" yaml:"bits"`
	BaseURL  string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
}

func (e EmbedderConfig) Identity() string {
	bits := strings.TrimSpace(e.Bits)
	if bits == "" {
		bits = defaultEmbedBits
	}
	return fmt.Sprintf("%s:%s:%d:%s", strings.TrimSpace(e.Provider), strings.TrimSpace(e.Model), e.Dims, bits)
}

type ChunkConfig struct {
	Unit     string `json:"unit" yaml:"unit"`
	Size     int    `json:"size" yaml:"size"`
	Overlap  int    `json:"overlap" yaml:"overlap"`
	Boundary string `json:"boundary" yaml:"boundary"`
}

func (c ChunkConfig) withDefaults() (ChunkConfig, error) {
	if strings.TrimSpace(c.Unit) == "" {
		c.Unit = defaultChunkUnit
	}
	sizeDefaulted := false
	if c.Size == 0 {
		c.Size = defaultChunkSize
		sizeDefaulted = true
	}
	if c.Overlap == 0 && sizeDefaulted {
		c.Overlap = defaultChunkOverlap
	}
	if strings.TrimSpace(c.Boundary) == "" {
		c.Boundary = defaultChunkBoundary
	}
	c.Unit = strings.ToLower(strings.TrimSpace(c.Unit))
	c.Boundary = strings.ToLower(strings.TrimSpace(c.Boundary))
	if c.Unit != "runes" {
		return ChunkConfig{}, fmt.Errorf("chunk unit must be runes, got %q", c.Unit)
	}
	if c.Boundary != "structure" {
		return ChunkConfig{}, fmt.Errorf("chunk boundary must be structure, got %q", c.Boundary)
	}
	if c.Size <= 0 {
		return ChunkConfig{}, fmt.Errorf("chunk size must be positive")
	}
	if c.Overlap < 0 {
		return ChunkConfig{}, fmt.Errorf("chunk overlap must be non-negative")
	}
	if c.Overlap >= c.Size {
		return ChunkConfig{}, fmt.Errorf("chunk overlap must be smaller than size")
	}
	return c, nil
}

func ValidateLaneConfig(lane LaneConfig) (LaneConfig, error) {
	lane.Lane = strings.TrimSpace(lane.Lane)
	lane.SourceFamily = strings.TrimSpace(lane.SourceFamily)
	lane.Generation = strings.TrimSpace(lane.Generation)
	lane.Privacy = strings.ToLower(strings.TrimSpace(lane.Privacy))
	lane.Embedder.Provider = strings.TrimSpace(lane.Embedder.Provider)
	lane.Embedder.Model = strings.TrimSpace(lane.Embedder.Model)
	lane.Embedder.Bits = strings.TrimSpace(lane.Embedder.Bits)
	lane.Embedder.BaseURL = strings.TrimSpace(lane.Embedder.BaseURL)
	if lane.Embedder.Bits == "" {
		lane.Embedder.Bits = defaultEmbedBits
	}
	var missing []string
	if lane.Lane == "" {
		missing = append(missing, "lane")
	}
	if lane.SourceFamily == "" {
		missing = append(missing, "source_family")
	}
	if lane.Privacy == "" {
		missing = append(missing, "privacy")
	}
	if lane.Embedder.Provider == "" {
		missing = append(missing, "embedder.provider")
	}
	if lane.Embedder.Model == "" {
		missing = append(missing, "embedder.model")
	}
	if lane.Embedder.Dims <= 0 {
		missing = append(missing, "embedder.dims")
	}
	if lane.Embedder.Bits == "" {
		missing = append(missing, "embedder.bits")
	}
	if len(missing) > 0 {
		return LaneConfig{}, fmt.Errorf("corpus lane %q missing %s", lane.Lane, strings.Join(missing, ", "))
	}
	chunk, err := lane.Chunk.withDefaults()
	if err != nil {
		return LaneConfig{}, fmt.Errorf("corpus lane %q: %w", lane.Lane, err)
	}
	lane.Chunk = chunk
	if lane.Privacy == "private" && !ProviderIsLocal(lane.Embedder.Provider, lane.Embedder.BaseURL) {
		return LaneConfig{}, fmt.Errorf("corpus lane %q privacy=private refuses non-local embedder %s", lane.Lane, lane.Embedder.Identity())
	}
	return lane, nil
}

func ProviderIsLocal(provider, baseURL string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return provider == "ollama" || provider == "local"
	}
	parsed := baseURL
	if !strings.Contains(parsed, "://") {
		parsed = "http://" + parsed
	}
	u, err := url.Parse(parsed)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return false
	}
	if host == "localhost" || host == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func IndexDir(homePath string, lane LaneConfig) string {
	return filepath.Join(homePath, "indexes", "corpus-"+safePathComponent(lane.Lane), SanitizedEmbedderIdentity(lane.Embedder.Identity()))
}

func SanitizedEmbedderIdentity(identity string) string {
	var b strings.Builder
	for _, r := range identity {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "unknown"
	}
	sum := sha256.Sum256([]byte(identity))
	return name + "-" + hex.EncodeToString(sum[:])[:12]
}

func safePathComponent(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func parseIdentityDim(identity string) int {
	parts := strings.Split(strings.TrimSpace(identity), ":")
	if len(parts) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(parts[len(parts)-2])
	return n
}
