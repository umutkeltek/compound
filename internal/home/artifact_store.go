package home

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	// The file bytes were synced before publication. Sync the containing
	// directory as well so a successful Put means the name-to-inode link is
	// durable across a crash, not merely visible to this process.
	if err := syncDirectory(s.dir); err != nil {
		return "", fmt.Errorf("sync artifact directory: %w", err)
	}
	return ref, nil
}

// PutFileExpected streams sourcePath into immutable custody and publishes it
// only when its bytes match expectedRef. The source and any existing artifact
// are hashed without loading the file into memory.
func (s *ArtifactStore) PutFileExpected(sourcePath, expectedRef string) (string, int64, error) {
	hexDigest, err := ArtifactHex(expectedRef)
	if err != nil {
		return "", 0, err
	}
	source, err := os.Open(strings.TrimSpace(sourcePath))
	if err != nil {
		return "", 0, err
	}
	defer source.Close()

	tmp, err := os.CreateTemp(s.dir, ".artifact-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", 0, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), source)
	if copyErr != nil {
		tmp.Close()
		return "", 0, copyErr
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	gotDigest := hex.EncodeToString(hash.Sum(nil))
	if gotDigest != hexDigest {
		return "", 0, fmt.Errorf("source %s content hash mismatch for %s: got sha256:%s", sourcePath, expectedRef, gotDigest)
	}

	path := filepath.Join(s.dir, hexDigest)
	if err := os.Link(tmpPath, path); err != nil {
		if !os.IsExist(err) {
			return "", 0, err
		}
		existingBytes, verifyErr := s.Verify(expectedRef)
		if verifyErr != nil {
			return "", 0, verifyErr
		}
		if existingBytes != written {
			return "", 0, fmt.Errorf("artifact %s byte count mismatch: existing=%d source=%d", expectedRef, existingBytes, written)
		}
	}
	if err := syncDirectory(s.dir); err != nil {
		return "", 0, fmt.Errorf("sync artifact directory: %w", err)
	}
	return expectedRef, written, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
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

// Verify checks an artifact's content hash using bounded memory and returns its
// byte count.
func (s *ArtifactStore) Verify(ref string) (int64, error) {
	hexDigest, err := ArtifactHex(ref)
	if err != nil {
		return 0, err
	}
	return verifyFileDigest(filepath.Join(s.dir, hexDigest), hexDigest, ref)
}

// Materialize streams an immutable artifact to a new path without overwriting
// an existing non-identical target. Both the stored artifact and the published
// target are verified against ref.
func (s *ArtifactStore) Materialize(ref, targetPath string, mode os.FileMode) (int64, bool, error) {
	hexDigest, err := ArtifactHex(ref)
	if err != nil {
		return 0, false, err
	}
	expectedBytes, err := s.Verify(ref)
	if err != nil {
		return 0, false, err
	}
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return 0, false, fmt.Errorf("artifact materialize target required")
	}
	mode &= os.ModePerm
	if mode == 0 {
		return 0, false, fmt.Errorf("artifact materialize mode required")
	}
	if n, exists, err := verifyExistingMaterializedTarget(targetPath, hexDigest, ref, mode, expectedBytes); exists || err != nil {
		return n, exists && err == nil, err
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, false, err
	}
	source, err := os.Open(filepath.Join(s.dir, hexDigest))
	if err != nil {
		return 0, false, err
	}
	defer source.Close()
	tmp, err := os.CreateTemp(dir, ".compound-materialize-*")
	if err != nil {
		return 0, false, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return 0, false, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), source)
	if copyErr != nil {
		tmp.Close()
		return 0, false, copyErr
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return 0, false, err
	}
	if err := tmp.Close(); err != nil {
		return 0, false, err
	}
	if written != expectedBytes || hex.EncodeToString(hash.Sum(nil)) != hexDigest {
		return 0, false, fmt.Errorf("artifact %s changed during materialization", ref)
	}
	if err := os.Link(tmpPath, targetPath); err != nil {
		if !os.IsExist(err) {
			return 0, false, err
		}
		return verifyExistingMaterializedTarget(targetPath, hexDigest, ref, mode, expectedBytes)
	}
	if err := syncDirectory(dir); err != nil {
		return 0, false, fmt.Errorf("sync materialized artifact directory: %w", err)
	}
	return written, false, nil
}

func verifyFileDigest(path, hexDigest, ref string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, f)
	if err != nil {
		return 0, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != hexDigest {
		return 0, fmt.Errorf("artifact %s content hash mismatch", ref)
	}
	return n, nil
}

func verifyExistingMaterializedTarget(targetPath, hexDigest, ref string, mode os.FileMode, expectedBytes int64) (int64, bool, error) {
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !info.Mode().IsRegular() {
		return 0, true, fmt.Errorf("refusing to overwrite non-regular target %s", targetPath)
	}
	n, err := verifyFileDigest(targetPath, hexDigest, ref)
	if err != nil || n != expectedBytes {
		return 0, true, fmt.Errorf("refusing to overwrite non-identical target %s", targetPath)
	}
	if info.Mode().Perm() != mode.Perm() {
		return 0, true, fmt.Errorf("refusing byte-identical target with non-identical mode %s: got %04o want %04o", targetPath, info.Mode().Perm(), mode.Perm())
	}
	return n, true, nil
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
