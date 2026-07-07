package breadth

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	vectorSchemaVersion = "compound.breadth.index.v1"
	wrapTemplate        = "{indexText}"
)

var (
	vectorMagic               = [8]byte{'C', 'B', 'R', 'V', 'E', 'C', '1', '\n'}
	errVectorCacheMiss        = errors.New("vector cache missing")
	errVectorDimMismatch      = errors.New("vector dim mismatch")
	errVectorEmbedderMismatch = errors.New("vector embedder mismatch")
	errVectorTemplateMismatch = errors.New("vector wrap template mismatch")
)

type embedderStamp struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Dim      int    `json:"dim"`
	Bits     string `json:"bits"`
}

func (s embedderStamp) Identity() string {
	return fmt.Sprintf("%s:%s:%d:%s", s.Provider, s.Model, s.Dim, s.Bits)
}

func parseEmbedderIdentity(identity string) (embedderStamp, error) {
	parts := strings.Split(strings.TrimSpace(identity), ":")
	if len(parts) < 4 {
		return embedderStamp{}, fmt.Errorf("invalid embedder identity %q", identity)
	}
	dim, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil || dim < 0 {
		return embedderStamp{}, fmt.Errorf("invalid embedder dim in %q", identity)
	}
	return embedderStamp{
		Provider: strings.TrimSpace(parts[0]),
		Model:    strings.TrimSpace(strings.Join(parts[1:len(parts)-2], ":")),
		Dim:      dim,
		Bits:     strings.TrimSpace(parts[len(parts)-1]),
	}, nil
}

type vectorManifest struct {
	SchemaVersion    string        `json:"schema_version"`
	Embedder         embedderStamp `json:"embedder"`
	CorpusGen        int64         `json:"corpus_gen"`
	ClaimCount       int           `json:"claim_count"`
	EmbeddedCount    int           `json:"embedded_count"`
	WrapTemplateHash string        `json:"wrap_template_hash"`
}

type vectorCache struct {
	dir      string
	stamp    embedderStamp
	identity string
}

type vectorKey struct {
	claimID  string
	textHash [32]byte
}

type vectorRow struct {
	ClaimID  string
	TextHash [32]byte
	Vector   []float32
}

func newVectorCache(compoundHome, identity string) (*vectorCache, error) {
	if strings.TrimSpace(compoundHome) == "" {
		// No home to persist the cache — degrade to a temp dir rather than fail the whole
		// engine (which used to drop even lexical recall to zero, measured 2026-07-07).
		// Vectors are a cache: losing them across restarts costs re-embed time, never
		// correctness. The operator path always gets a real home (compound forwards it).
		compoundHome = filepath.Join(os.TempDir(), "compound-breadth-cache")
	}
	stamp, err := parseEmbedderIdentity(identity)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(compoundHome, "indexes", "breadth", stampSlug(identity))
	return &vectorCache{dir: dir, stamp: stamp, identity: identity}, nil
}

func (c *vectorCache) load() (map[vectorKey]vectorRow, error) {
	manifest, err := c.readManifest()
	if err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != vectorSchemaVersion {
		return nil, fmt.Errorf("%w: schema %q", errVectorEmbedderMismatch, manifest.SchemaVersion)
	}
	if manifest.Embedder.Identity() != c.identity {
		return nil, fmt.Errorf("%w: cache %q configured %q", errVectorEmbedderMismatch, manifest.Embedder.Identity(), c.identity)
	}
	if manifest.WrapTemplateHash != wrapTemplateHash() {
		return nil, fmt.Errorf("%w: cache %q configured %q", errVectorTemplateMismatch, manifest.WrapTemplateHash, wrapTemplateHash())
	}
	return c.readVectors(manifest.Embedder.Dim)
}

func (c *vectorCache) readManifest() (vectorManifest, error) {
	raw, err := os.ReadFile(filepath.Join(c.dir, "manifest.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return vectorManifest{}, errVectorCacheMiss
		}
		return vectorManifest{}, err
	}
	var manifest vectorManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return vectorManifest{}, fmt.Errorf("parse vector manifest: %w", err)
	}
	return manifest, nil
}

func (c *vectorCache) readVectors(expectedDim int) (map[vectorKey]vectorRow, error) {
	f, err := os.Open(filepath.Join(c.dir, "vectors.bin"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errVectorCacheMiss
		}
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var magic [8]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return nil, fmt.Errorf("read vector header: %w", err)
	}
	if magic != vectorMagic {
		return nil, fmt.Errorf("%w: bad magic", errVectorDimMismatch)
	}
	headerDim, err := readUint32(r)
	if err != nil {
		return nil, fmt.Errorf("read vector dim: %w", err)
	}
	if int(headerDim) != expectedDim {
		return nil, fmt.Errorf("%w: header dim %d != manifest dim %d", errVectorDimMismatch, headerDim, expectedDim)
	}
	count, err := readUint64(r)
	if err != nil {
		return nil, fmt.Errorf("read vector count: %w", err)
	}
	rows := make(map[vectorKey]vectorRow, int(count))
	for i := uint64(0); i < count; i++ {
		idLen, err := readUint32(r)
		if err != nil {
			return nil, fmt.Errorf("read vector row %d id length: %w", i, err)
		}
		if idLen == 0 || idLen > 1<<20 {
			return nil, fmt.Errorf("vector row %d invalid claim id length %d", i, idLen)
		}
		idBytes := make([]byte, idLen)
		if _, err := io.ReadFull(r, idBytes); err != nil {
			return nil, fmt.Errorf("read vector row %d claim id: %w", i, err)
		}
		var textHash [32]byte
		if _, err := io.ReadFull(r, textHash[:]); err != nil {
			return nil, fmt.Errorf("read vector row %d text hash: %w", i, err)
		}
		rowDim, err := readUint32(r)
		if err != nil {
			return nil, fmt.Errorf("read vector row %d dim: %w", i, err)
		}
		if rowDim != headerDim {
			return nil, fmt.Errorf("%w: row %d dim %d != header dim %d", errVectorDimMismatch, i, rowDim, headerDim)
		}
		vec := make([]float32, headerDim)
		for j := range vec {
			bits, err := readUint32(r)
			if err != nil {
				return nil, fmt.Errorf("read vector row %d value %d: %w", i, j, err)
			}
			v := math.Float32frombits(bits)
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, fmt.Errorf("%w: non-finite row %d value %d", errVectorDimMismatch, i, j)
			}
			vec[j] = v
		}
		claimID := string(idBytes)
		key := vectorKey{claimID: claimID, textHash: textHash}
		rows[key] = vectorRow{ClaimID: claimID, TextHash: textHash, Vector: vec}
	}
	return rows, nil
}

func (c *vectorCache) write(rows []vectorRow, corpusGen int64) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	if err := c.writeVectors(rows); err != nil {
		return err
	}
	manifest := vectorManifest{
		SchemaVersion:    vectorSchemaVersion,
		Embedder:         c.stamp,
		CorpusGen:        corpusGen,
		ClaimCount:       len(rows),
		EmbeddedCount:    len(rows),
		WrapTemplateHash: wrapTemplateHash(),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicWrite(filepath.Join(c.dir, "manifest.json"), raw)
}

func (c *vectorCache) writeVectors(rows []vectorRow) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if _, err := w.Write(vectorMagic[:]); err != nil {
		return err
	}
	if err := writeUint32(w, uint32(c.stamp.Dim)); err != nil {
		return err
	}
	if err := writeUint64(w, uint64(len(rows))); err != nil {
		return err
	}
	for _, row := range rows {
		if len(row.Vector) != c.stamp.Dim {
			return fmt.Errorf("%w: row %s dim %d != cache dim %d", errVectorDimMismatch, row.ClaimID, len(row.Vector), c.stamp.Dim)
		}
		if err := writeUint32(w, uint32(len(row.ClaimID))); err != nil {
			return err
		}
		if _, err := w.WriteString(row.ClaimID); err != nil {
			return err
		}
		if _, err := w.Write(row.TextHash[:]); err != nil {
			return err
		}
		if err := writeUint32(w, uint32(len(row.Vector))); err != nil {
			return err
		}
		for _, v := range row.Vector {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("%w: non-finite vector for %s", errVectorDimMismatch, row.ClaimID)
			}
			if err := writeUint32(w, math.Float32bits(v)); err != nil {
				return err
			}
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(c.dir, "vectors.bin"), buf.Bytes())
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func readUint32(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

func readUint64(r io.Reader) (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

func writeUint32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func writeUint64(w io.Writer, v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func textHash(text string) [32]byte { return sha256.Sum256([]byte(text)) }

func wrapTemplateHash() string {
	h := sha256.Sum256([]byte(wrapTemplate))
	return hex.EncodeToString(h[:])
}

func stampSlug(identity string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(identity) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "unknown"
	}
	return slug
}
