package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resumeArchiveFixture(t *testing.T) (string, string, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "home")
	if err := Init(source, "test", archiveTestNow); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ledger", "session.json"), []byte("synthetic session"), 0600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := BackupWithFormat(source, archivePath, "streamed-v2", archiveTestNow); err != nil {
		t.Fatal(err)
	}
	parentPath := t.TempDir()
	parent, fd, _, err := openStableRestoreParent(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	defer fd.Close()
	name, root, info, err := createRestoreStaging(parent, fd, parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	archive, err := openVerifiedBackupArchive(archivePath, archiveStreamTarLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	target := &archiveRestoreTarget{root: root, displayPath: filepath.Join(parentPath, name), deferFileParentSync: true, stagingInfo: info}
	if _, _, err := archive.extract(target); err != nil {
		t.Fatal(err)
	}
	if err := target.completeHabitat(); err != nil {
		t.Fatal(err)
	}
	if err := target.finalizeDirectoryModes(); err != nil {
		t.Fatal(err)
	}
	return archivePath, filepath.Join(parentPath, name), filepath.Join(parentPath, "restored")
}
func TestResumeRestoreArchiveVerifiesAndPublishes(t *testing.T) {
	archive, stage, target := resumeArchiveFixture(t)
	report, err := ResumeRestoreArchive(archive, stage, target, archiveTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if report.TargetHome != target || report.Files == 0 || report.RevalidatedStage != stage || len(report.ArchiveSHA256) != 64 || report.StageIdentity == "" {
		t.Fatalf("report: %+v", report)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage remains: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(target, "ledger", "session.json"))
	if err != nil || string(body) != "synthetic session" {
		t.Fatalf("restored state mismatch: %v", err)
	}
}
func TestResumeRestoreArchiveRejectsIncompleteOrTamperedStage(t *testing.T) {
	for _, scenario := range []string{"body", "extra-file", "extra-directory", "missing", "mode", "symlink", "parent", "occupied", "late-body"} {
		t.Run(scenario, func(t *testing.T) {
			archive, stage, target := resumeArchiveFixture(t)
			file := filepath.Join(stage, "ledger", "session.json")
			switch scenario {
			case "body":
				if err := os.WriteFile(file, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra-file":
				if err := os.WriteFile(filepath.Join(stage, "ledger", "extra"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra-directory":
				if err := os.Mkdir(filepath.Join(stage, "ledger", "extra"), 0700); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(stage, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				moved := stage + "-original"
				if err := os.Rename(stage, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, stage); err != nil {
					t.Fatal(err)
				}
			case "parent":
				target = filepath.Join(t.TempDir(), "restored")
			case "occupied":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "late-body":
				old := restoreBeforePublish
				t.Cleanup(func() { restoreBeforePublish = old })
				restoreBeforePublish = func(string, string) error { return os.WriteFile(file, []byte("late"), 0600) }
			}
			_, err := ResumeRestoreArchive(archive, stage, target, archiveTestNow)
			if err == nil {
				t.Fatal("unsafe resume accepted")
			}
			if _, statErr := os.Lstat(stage); statErr != nil {
				t.Fatalf("failed resume removed stage: %v", statErr)
			}
			if scenario != "occupied" {
				if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
					t.Fatalf("failed resume published target: %v", statErr)
				}
			}
		})
	}
}
func TestResumeRestoreArchiveRejectsLegacy(t *testing.T) {
	_, stage, target := resumeArchiveFixture(t)
	_, err := ResumeRestoreArchive(backupArchiveFixture(t), stage, target, archiveTestNow)
	if err == nil || !strings.Contains(err.Error(), "streamed") {
		t.Fatalf("legacy resume: %v", err)
	}
	if _, err := os.Lstat(stage); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRestoreArchiveRefusesLiveHomeAndHardlinkAliases(t *testing.T) {
	for _, scenario := range []string{"live-home", "external-hardlink", "missing-habitat", "stage-replaced"} {
		t.Run(scenario, func(t *testing.T) {
			archive, stage, target := resumeArchiveFixture(t)
			file := filepath.Join(stage, "ledger", "session.json")
			var external string
			switch scenario {
			case "live-home":
				t.Setenv("COMPOUND_HOME", stage)
			case "external-hardlink":
				external = filepath.Join(t.TempDir(), "original")
				if err := os.WriteFile(external, []byte("synthetic session"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, file); err != nil {
					t.Fatal(err)
				}
			case "missing-habitat":
				if err := os.Remove(filepath.Join(stage, "private", "artifacts", "sha256")); err != nil {
					t.Fatal(err)
				}
			case "stage-replaced":
				old := restoreBeforePublish
				t.Cleanup(func() { restoreBeforePublish = old })
				restoreBeforePublish = func(string, string) error {
					if err := os.Rename(stage, stage+"-pinned"); err != nil {
						return err
					}
					return os.Mkdir(stage, 0700)
				}
			}
			if _, err := ResumeRestoreArchive(archive, stage, target, archiveTestNow); err == nil {
				t.Fatal("unsafe resume accepted")
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("unsafe publication: %v", err)
			}
			if external != "" {
				body, err := os.ReadFile(external)
				if err != nil || string(body) != "synthetic session" {
					t.Fatalf("external original changed: %v", err)
				}
			}
			if scenario == "missing-habitat" {
				if _, err := os.Lstat(filepath.Join(stage, "private", "artifacts", "sha256")); !os.IsNotExist(err) {
					t.Fatalf("unverified stage normalized: %v", err)
				}
			}
		})
	}
}

func TestResumeRestoreArchiveRejectsArchiveChangeAfterPreflight(t *testing.T) {
	archive, stage, target := resumeArchiveFixture(t)
	old := restoreArchivePreflighted
	t.Cleanup(func() { restoreArchivePreflighted = old })
	restoreArchivePreflighted = func(path string) error { return os.WriteFile(path, []byte("changed archive"), 0600) }
	if _, err := ResumeRestoreArchive(archive, stage, target, archiveTestNow); err == nil {
		t.Fatal("changed archive accepted")
	}
	if _, err := os.Lstat(stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target published: %v", err)
	}
}
func TestResumeStageFlagIsNeverAPositional(t *testing.T) {
	for _, args := range [][]string{
		{"--resume-stage", "stage", "restore", "backup", "--target", "target"},
		{"restore", "--resume-stage", "stage", "backup", "--target", "target"},
	} {
		parsed := ParseCLIArgs(args)
		if parsed.Subcommand != "restore" || len(parsed.RemainingPositionals) != 1 || parsed.RemainingPositionals[0] != "backup" {
			t.Fatalf("parsed: %+v", parsed)
		}
	}
}
