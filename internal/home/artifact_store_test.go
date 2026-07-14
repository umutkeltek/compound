package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
