package home

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactRefPrefix is the stable reference grammar for immutable bodies held in
// COMPOUND_HOME/artifacts/sha256.
const ArtifactRefPrefix = "artifact:sha256:"

// ArtifactStore owns immutable, content-addressed bytes. It is intentionally in
// home: physical custody is a property of COMPOUND_HOME, not of any one object
// family that happens to consume the bytes.
type ArtifactStore struct {
	dir  string
	home string
}

func NewArtifactStore(homePath string) (*ArtifactStore, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("artifact store requires COMPOUND_HOME")
	}
	dir := filepath.Join(homePath, "artifacts", "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &ArtifactStore{dir: dir, home: homePath}, nil
}

func (s *ArtifactStore) Home() string { return s.home }

// Put stores body without ever replacing a previously-addressed object. A
// temporary file plus hard-link publication makes concurrent first writes safe;
// an existing object must already contain the exact same bytes.
func (s *ArtifactStore) Put(body []byte) (string, error) {
	sum := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(sum[:])
	ref := ArtifactRefPrefix + hexDigest
	path := filepath.Join(s.dir, hexDigest)
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, body) {
			return "", fmt.Errorf("artifact %s content-address collision", ref)
		}
		return ref, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp, err := os.CreateTemp(s.dir, ".artifact-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmpPath, path); err != nil {
		if !os.IsExist(err) {
			return "", err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", readErr
		}
		if !bytes.Equal(existing, body) {
			return "", fmt.Errorf("artifact %s content-address collision", ref)
		}
	}
	return ref, nil
}

// Get validates both the reference grammar and the bytes. A corrupt object is
// never served merely because a file exists at the expected path.
func (s *ArtifactStore) Get(ref string) ([]byte, error) {
	hexDigest, err := ArtifactHex(ref)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(s.dir, hexDigest))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != hexDigest {
		return nil, fmt.Errorf("artifact %s content hash mismatch", ref)
	}
	return body, nil
}

func ArtifactHex(ref string) (string, error) {
	if !strings.HasPrefix(ref, ArtifactRefPrefix) {
		return "", fmt.Errorf("artifact ref must be %q-prefixed, got %q", ArtifactRefPrefix, ref)
	}
	hexDigest := strings.TrimPrefix(ref, ArtifactRefPrefix)
	if len(hexDigest) != 64 {
		return "", fmt.Errorf("artifact ref must be %s<64 hex>, got %q", ArtifactRefPrefix, ref)
	}
	for _, r := range hexDigest {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", fmt.Errorf("artifact ref has a non-hex character: %q", ref)
		}
	}
	return hexDigest, nil
}
