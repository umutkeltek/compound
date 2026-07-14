package home

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactStorePutFileExpectedRejectsMismatchWithoutPublication(t *testing.T) {
	homePath := t.TempDir()
	store, err := NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	dump := []byte(strings.Repeat("postgres-dump-block\n", 256*1024))
	dumpPath := filepath.Join(t.TempDir(), "gbrain.dump")
	if err := os.WriteFile(dumpPath, dump, 0o600); err != nil {
		t.Fatal(err)
	}

	wrong := sha256.Sum256([]byte("different dump"))
	wrongRef := ArtifactRefPrefix + hex.EncodeToString(wrong[:])
	if _, _, err := store.PutFileExpected(dumpPath, wrongRef); err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("mismatch error = %v", err)
	}
	wrongHex, err := ArtifactHex(wrongRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "artifacts", "sha256", wrongHex)); !os.IsNotExist(err) {
		t.Fatalf("mismatched artifact was published: %v", err)
	}

	matching := sha256.Sum256(dump)
	matchingRef := ArtifactRefPrefix + hex.EncodeToString(matching[:])
	ref, size, err := store.PutFileExpected(dumpPath, matchingRef)
	if err != nil {
		t.Fatal(err)
	}
	if ref != matchingRef || size != int64(len(dump)) {
		t.Fatalf("capture = ref %q bytes %d", ref, size)
	}
	verified, err := store.Verify(ref)
	if err != nil || verified != size {
		t.Fatalf("verify = bytes %d err %v", verified, err)
	}
}

func TestArtifactStoreRejectsTraversalAndCorruptObjects(t *testing.T) {
	homePath := t.TempDir()
	store, err := NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(homePath, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		"artifact:sha256:../../outside",
		"artifact:sha256:" + strings.Repeat("a", 63) + "/",
		"sha256:" + strings.Repeat("a", 64),
	} {
		if _, err := store.Get(ref); err == nil {
			t.Fatalf("hostile ref %q was accepted", ref)
		}
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "outside" {
		t.Fatalf("outside file changed: body=%q err=%v", got, err)
	}

	ref, err := store.Put([]byte("owned body"))
	if err != nil {
		t.Fatal(err)
	}
	hexDigest, err := ArtifactHex(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "artifacts", "sha256", hexDigest), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ref); err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("corrupt artifact err = %v", err)
	}
}
