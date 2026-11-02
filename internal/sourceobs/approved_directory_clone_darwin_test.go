//go:build darwin

package sourceobs

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"compound/internal/home"
)

// physicalOffset asks the filesystem where a file's first block really lives. Two
// files answering the same offset share storage, which no size or hash check can show.
func physicalOffset(t *testing.T, path string) uint64 {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	const fLog2Phys = 49
	var reply [20]byte // packed struct log2phys: flags, contigbytes, devoffset
	if _, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), fLog2Phys, uintptr(unsafe.Pointer(&reply))); errno != 0 {
		t.Skipf("volume does not report physical offsets: %v", errno)
	}
	return binary.LittleEndian.Uint64(reply[12:])
}

// Owned versions of an append-only transcript must share storage. If capture ever
// goes back to copying, every test that reads bytes still passes while the disk
// fills; only this check sees it.
func TestApprovedDirectoryVersionsShareStorageOnAPFS(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	probe, err := os.Create(filepath.Join(root, "probe"))
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := home.CloneFileInto(probe, root, "probe-clone"); err != nil {
		t.Skipf("volume cannot clone: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "probe-clone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "probe")); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "session.md")
	body := strings.Repeat("Synthetic transcript turn.\n", 160_000) // about 4 MiB
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "markdown", time.Now()); err != nil {
		t.Fatal(err)
	}
	appended, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appended.WriteString(body[:len(body)/2]); err != nil {
		t.Fatal(err)
	}
	if err := appended.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "markdown", time.Now()); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadSourceManifest(hp, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 2 {
		t.Fatalf("versions = %d, want 2", len(manifest.Items))
	}
	store, err := home.NewArtifactStore(hp)
	if err != nil {
		t.Fatal(err)
	}
	var offsets []uint64
	for _, item := range manifest.Items {
		if _, err := store.Verify(item.BodyRef); err != nil {
			t.Fatalf("version %s does not verify: %v", item.ID, err)
		}
		digest, err := home.ArtifactHex(item.BodyRef)
		if err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, physicalOffset(t, filepath.Join(hp, "artifacts", "sha256", digest)))
	}
	if offsets[0] != offsets[1] {
		t.Fatalf("versions occupy separate storage: %v", offsets)
	}
	if source := physicalOffset(t, path); source != offsets[0] {
		t.Fatalf("version does not share storage with its source: %d vs %d", source, offsets[0])
	}
}
