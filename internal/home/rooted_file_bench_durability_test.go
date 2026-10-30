package home

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteRootedImmutableFileDurablePreservesFinalSyncFailure(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "reviews", "bench")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "report.json")
	data := []byte("synthetic report\n")
	previous := syncRootedDirectoryFile
	defer func() { syncRootedDirectoryFile = previous }()
	calls := 0
	syncRootedDirectoryFile = func(relative string, file *os.File) error {
		if relative == filepath.Join("reviews", "bench") {
			calls++
			return syscall.EIO
		}
		return file.Sync()
	}
	// This is the actual late-failure shape: complete bytes remain after parent sync fails.
	if err := WriteRootedImmutableFileDurable(root, path, "bench report", data, 0o640); !errors.Is(err, syscall.EIO) {
		t.Fatalf("initial error = %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("leftover = %q, %v", got, err)
	}
	if err := WriteRootedImmutableFileDurable(root, path, "bench report", data, 0o600); !errors.Is(err, syscall.EIO) {
		t.Fatalf("retry hid sync failure: %v", err)
	}
	if calls != 2 {
		t.Fatalf("final directory sync calls = %d, want 2", calls)
	}
	syncRootedDirectoryFile = previous
	if err := WriteRootedImmutableFileDurable(root, path, "bench report", data, 0o600); err != nil {
		t.Fatalf("recovered retry: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatal("retry replaced or chmodded original evidence")
	}
	if err := WriteRootedNewFileDurable(root, path, "bench report", data, 0o640); err == nil {
		t.Fatal("new-file API now accepts existing files")
	}
}

func TestWriteRootedImmutableFileDurableRefusesConflicts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.json")
	data := []byte("original\n")
	if err := WriteRootedImmutableFileDurable(root, path, "bench report", data, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range [][]byte{[]byte("different\n"), []byte("orig"), append(append([]byte{}, data...), 'x')} {
		if err := WriteRootedImmutableFileDurable(root, path, "bench report", other, 0o600); err == nil {
			t.Fatalf("conflicting bytes accepted: %q", other)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatalf("conflict changed original evidence: %v", err)
	}
}

func TestWriteRootedImmutableFileDurableRejectsSwapDuringSync(t *testing.T) {
	for _, swapParent := range []bool{false, true} {
		name := "leaf"
		if swapParent {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "reviews", "bench")
			path := filepath.Join(parent, "report.json")
			data := []byte("preserved\n")
			if err := WriteRootedImmutableFileDurable(root, path, "bench report", data, 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			previous := syncRootedDirectoryFile
			defer func() { syncRootedDirectoryFile = previous }()
			saved := path + ".original"
			changed := false
			syncRootedDirectoryFile = func(relative string, file *os.File) error {
				if relative == filepath.Join("reviews", "bench") && !changed {
					changed = true
					source := path
					if swapParent {
						source = parent
						saved = parent + ".original"
					}
					if err := os.Rename(source, saved); err != nil {
						return err
					}
					if swapParent {
						if err := os.Mkdir(parent, 0o755); err != nil {
							return err
						}
					}
					if err := os.WriteFile(path, data, 0o640); err != nil {
						return err
					}
				}
				return file.Sync()
			}
			if err := WriteRootedImmutableFileDurable(root, path, "bench report", data, 0o640); err == nil {
				t.Fatal("path replacement during sync accepted")
			}
			if !changed {
				t.Fatal("sync fault seam not reached")
			}
			if swapParent {
				saved = filepath.Join(saved, "report.json")
			}
			after, err := os.Stat(saved)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(saved)
			if err != nil || !bytes.Equal(got, data) || !os.SameFile(before, after) {
				t.Fatalf("original lost during refused retry: %v", err)
			}
		})
	}
}
