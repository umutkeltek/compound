package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRemovalFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "state", "victim.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("owned bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

func TestRemoveRootedRegularFileDurableValidatedRemovesExactFile(t *testing.T) {
	home, path := writeRemovalFixture(t)
	validated := false
	err := RemoveRootedRegularFileDurableValidated(home, path, "test file", func(data []byte) error {
		validated = string(data) == "owned bytes\n"
		return nil
	})
	if err != nil || !validated {
		t.Fatalf("validated removal failed: err=%v validated=%v", err, validated)
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Fatalf("file survived validated removal: %v", statErr)
	}
}

// FALSIFIER (final quarantine swap): a foreign file atomically swapped onto
// the predictable quarantine pathname after content/inode validation must
// never be unlinked. A path-based final removal deletes bytes the validator
// never reviewed; the unlink must stay bound to the validated inode.
func TestRemoveRootedRegularFileDurableValidatedFinalQuarantineSwapPreservesReplacement(t *testing.T) {
	home, path := writeRemovalFixture(t)
	quarantinePath := path + ".remove-pending"
	var replacementInfo os.FileInfo
	removeRootedBeforeUnlink = func(reported string) error {
		if reported != quarantinePath {
			t.Fatalf("seam reported %q, want %q", reported, quarantinePath)
		}
		swap := path + ".swap"
		if err := os.WriteFile(swap, []byte("owned bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(swap, quarantinePath); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(quarantinePath)
		if err != nil {
			t.Fatal(err)
		}
		replacementInfo = info
		return nil
	}
	defer func() { removeRootedBeforeUnlink = nil }()

	err := RemoveRootedRegularFileDurableValidated(home, path, "test file", nil)
	if err == nil || !strings.Contains(err.Error(), "file changed after validation") {
		t.Fatalf("byte-identical foreign swap was not refused: %v", err)
	}
	survivor := path
	info, statErr := os.Lstat(survivor)
	if statErr != nil {
		survivor = quarantinePath
		info, statErr = os.Lstat(survivor)
	}
	if statErr != nil {
		t.Fatalf("replacement did not survive refusal anywhere: %v", statErr)
	}
	if !os.SameFile(replacementInfo, info) {
		t.Fatalf("survivor at %s is not the replacement inode", survivor)
	}
	body, readErr := os.ReadFile(survivor)
	if readErr != nil || string(body) != "owned bytes\n" {
		t.Fatalf("replacement bytes were not preserved: err=%v body=%q", readErr, body)
	}
}

func TestRemoveRootedRegularFileDurableValidatedValidatorRefusalRestoresFile(t *testing.T) {
	home, path := writeRemovalFixture(t)
	err := RemoveRootedRegularFileDurableValidated(home, path, "test file", func([]byte) error {
		return os.ErrPermission
	})
	if err == nil {
		t.Fatal("validator refusal did not error")
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil || string(body) != "owned bytes\n" {
		t.Fatalf("refused file was not left in place: err=%v body=%q", readErr, body)
	}
}
