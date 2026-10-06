package sourceobs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/home"
)

func TestSourceManifestDoctorPreservesArchivedEvidenceBinding(t *testing.T) {
	homePath, family, _, before := archivedLifecycleEvidenceFixture(t)
	doctor, err := DoctorSourceManifest(homePath, family)
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ok" || doctor.ItemCount != 1 || doctor.LifecycleEvidenceMismatchCount != 0 || doctor.LifecycleEvidenceMissingCount != 0 {
		t.Fatalf("archival invalidated unchanged bound proof: %+v", doctor)
	}
	assertFileBytes(t, SourceManifestPath(homePath, family), before)
}

func archivedLifecycleEvidenceFixture(t *testing.T) (string, string, string, []byte) {
	t.Helper()
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", lifecycleTestNow); err != nil {
		t.Fatal(err)
	}
	const family = "archived_lifecycle_test_family"
	sourcePath := filepath.Join(t.TempDir(), "source.md")
	if err := os.WriteFile(sourcePath, []byte("# Source family\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildOwnedSourceManifest(homePath, family, []string{sourcePath}, lifecycleTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordSourceManifest(homePath, manifest); err != nil {
		t.Fatal(err)
	}
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleCapturing, lifecycleProofs(SourceLifecycleGateSourceInventory), nil, lifecycleTestNow)
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleOwnedSnapshot, lifecycleProofs(SourceLifecycleGateExactCustody), nil, lifecycleTestNow.Add(time.Minute))
	const workID = "work_archived_source_evidence"
	gates := verifiedLifecycleGates()
	ref := writeLifecycleEvidenceFixture(t, homePath, lifecycleEvidenceFixture{
		Family: family, Gates: gates, State: "completed", WorkID: workID,
	})
	proofs := lifecycleProofs(gates...)
	for i := range proofs {
		proofs[i].EvidenceRef = ref
	}
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleVerified, proofs, nil, lifecycleTestNow.Add(2*time.Minute))
	manifestPath := SourceManifestPath(homePath, family)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(filepath.Join("work", "work", workID+".json"))
	archived, err := home.ArchiveOperationalHistory(homePath, home.OperationalArchiveOptions{
		Kinds: []string{"work"}, Paths: []string{relative},
	}, lifecycleTestNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedCount != 1 {
		t.Fatalf("proof work not archived: %+v", archived)
	}
	if _, err := os.Stat(filepath.Join(homePath, filepath.FromSlash(relative))); !os.IsNotExist(err) {
		t.Fatalf("current work still present: %v", err)
	}
	return homePath, family, relative, before
}

// These controls deliberately bypass write doors: legitimate writers cannot
// produce forged archive metadata or a corrupt present canonical envelope.
func TestSourceManifestDoctorRejectsInvalidArchivedEvidence(t *testing.T) {
	for _, name := range []string{
		"changed_body", "missing_body", "unindexed_body", "forged_size", "forged_digest",
		"pending", "wrong_kind", "wrong_status", "escape_path", "malformed_index",
		"unterminated_index", "duplicate_index", "ambiguous_index", "archive_symlink",
		"archive_ancestor_symlink", "index_symlink", "current_corrupt", "current_symlink",
		"forged_body_and_index",
	} {
		t.Run(name, func(t *testing.T) {
			homePath, family, relative, before := archivedLifecycleEvidenceFixture(t)
			indexPath := home.OperationalArchiveIndexPath(homePath)
			indexBody, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := home.LoadOperationalArchiveIndex(homePath)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("fixture archive entries=%d", len(entries))
			}
			entry := entries[0]
			archivePath := filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath))
			currentPath := filepath.Join(homePath, filepath.FromSlash(relative))
			archiveBody, err := os.ReadFile(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			write := func(path string, body []byte) {
				t.Helper()
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			remove := func(path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			replaceSymlink := func(path string, body []byte) {
				t.Helper()
				external := filepath.Join(t.TempDir(), "external.json")
				write(external, body)
				remove(path)
				if err := os.Symlink(external, path); err != nil {
					t.Fatal(err)
				}
			}
			metadataChanged := true
			switch name {
			case "changed_body":
				write(archivePath, append(archiveBody, '\n'))
				metadataChanged = false
			case "missing_body":
				remove(archivePath)
				metadataChanged = false
			case "unindexed_body":
				write(indexPath, nil)
				metadataChanged = false
			case "forged_size":
				entry.Size++
			case "forged_digest":
				entry.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			case "pending":
				entry.Operation = "archived_pending"
			case "wrong_kind":
				entry.Kind = "receipt"
			case "wrong_status":
				entry.Status = "failed"
			case "escape_path":
				entry.ArchivePath = "../external.json"
			case "malformed_index":
				write(indexPath, []byte("{\n"))
				metadataChanged = false
			case "unterminated_index":
				write(indexPath, bytes.TrimSuffix(indexBody, []byte("\n")))
				metadataChanged = false
			case "duplicate_index":
				write(indexPath, append(indexBody, indexBody...))
				metadataChanged = false
			case "ambiguous_index":
				entry.ID += "_second"
				row, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				write(indexPath, append(indexBody, append(row, '\n')...))
				metadataChanged = false
			case "archive_symlink":
				replaceSymlink(archivePath, archiveBody)
				metadataChanged = false
			case "archive_ancestor_symlink":
				parent := filepath.Dir(archivePath)
				external := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(parent, external); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, parent); err != nil {
					t.Fatal(err)
				}
				metadataChanged = false
			case "index_symlink":
				replaceSymlink(indexPath, indexBody)
				metadataChanged = false
			case "current_corrupt":
				write(currentPath, []byte("{"))
				metadataChanged = false
			case "current_symlink":
				if err := os.Symlink(archivePath, currentPath); err != nil {
					t.Fatal(err)
				}
				metadataChanged = false
			case "forged_body_and_index":
				archiveBody = append(archiveBody, '\n')
				write(archivePath, archiveBody)
				entry.Size = int64(len(archiveBody))
				entry.SHA256 = lifecycleEvidenceSHA256(archiveBody)[len("sha256:"):]
			}
			if metadataChanged {
				row, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				write(indexPath, append(row, '\n'))
			}
			doctor, err := DoctorSourceManifest(homePath, family)
			if err != nil {
				t.Fatal(err)
			}
			if doctor.LifecycleEvidenceMismatchCount != len(verifiedLifecycleGates()) {
				t.Fatalf("invalid archive evidence accepted: mismatches=%d", doctor.LifecycleEvidenceMismatchCount)
			}
			assertFileBytes(t, SourceManifestPath(homePath, family), before)
		})
	}
}
