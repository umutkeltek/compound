package home

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOperationalArchiveIndexRejectsSymlinkedAuthority(t *testing.T) {
	indexRaw := operationalArchiveIndexFixture(t)

	t.Run("leaf", func(t *testing.T) {
		home := t.TempDir()
		indexPath := OperationalArchiveIndexPath(home)
		if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
			t.Fatal(err)
		}
		externalPath := filepath.Join(t.TempDir(), "index.jsonl")
		if err := os.WriteFile(externalPath, indexRaw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(externalPath, indexPath); err != nil {
			t.Fatal(err)
		}

		if _, err := LoadOperationalArchiveIndex(home); err == nil || !strings.Contains(err.Error(), "operational archive index file must be a regular file") {
			t.Fatalf("symlinked operational archive index leaf did not fail closed: %v", err)
		}
		assertArchiveExternalFileUnchanged(t, externalPath, indexRaw)
	})

	t.Run("ancestor", func(t *testing.T) {
		home := t.TempDir()
		externalArchives := t.TempDir()
		externalPath := filepath.Join(externalArchives, "operational", "index.jsonl")
		if err := os.MkdirAll(filepath.Dir(externalPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(externalPath, indexRaw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(externalArchives, filepath.Join(home, "archives")); err != nil {
			t.Fatal(err)
		}

		if _, err := LoadOperationalArchiveIndex(home); err == nil || !strings.Contains(err.Error(), "operational archive index path must not contain symlink beneath compound home") {
			t.Fatalf("symlinked operational archive index ancestor did not fail closed: %v", err)
		}
		assertArchiveExternalFileUnchanged(t, externalPath, indexRaw)
	})
}

func TestLoadOperationalArchiveIndexRejectsUnterminatedFinalRow(t *testing.T) {
	home := t.TempDir()
	indexPath := OperationalArchiveIndexPath(home)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	indexRaw := bytes.TrimSuffix(operationalArchiveIndexFixture(t), []byte{'\n'})
	if err := os.WriteFile(indexPath, indexRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOperationalArchiveIndex(home); err == nil || !strings.Contains(err.Error(), "operational archive index must end with newline") {
		t.Fatalf("unterminated operational archive index did not fail closed: %v", err)
	}
}

func operationalArchiveIndexFixture(t *testing.T) []byte {
	t.Helper()
	entry := OperationalArchiveEntry{
		SchemaVersion: OperationalArchiveSchemaVersion,
		ID:            "op_rooted_fixture",
		Operation:     "archived",
		CreatedAt:     archiveTestNow.Format("2006-01-02T15:04:05Z"),
		Kind:          "work",
		OriginalPath:  "work/work/work_rooted_fixture.json",
		ArchivePath:   "archives/operational/files/work/work/work_rooted_fixture.json",
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func assertArchiveExternalFileUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read external archive fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("external archive fixture changed:\n got %q\nwant %q", got, want)
	}
}
