package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkAdmissionLockUsesLegacyCapabilityLifecycleInode(t *testing.T) {
	homePath := t.TempDir()
	workID := "work_admission_compatibility"
	wantPath := filepath.Join(homePath, "lock", "capability-lifecycle-work-admission", workID+".lock")

	if err := WithWorkAdmissionLock(homePath, workID, func() error {
		if _, err := os.Stat(wantPath); err != nil {
			return err
		}
		acquired, err := TryWithWorkAdmissionLock(homePath, workID, func() error {
			t.Fatal("nonblocking admission callback ran while the work lock was held")
			return nil
		})
		if err != nil {
			return err
		}
		if acquired {
			t.Fatal("nonblocking admission acquired the legacy inode while it was held")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	acquired, err := TryWithWorkAdmissionLock(homePath, workID, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("nonblocking admission did not acquire the work lock after release")
	}
}

func TestWorkAdmissionLockRejectsInvalidIdentityWithoutFootprint(t *testing.T) {
	homePath := t.TempDir()
	for _, workID := range []string{"", ".", "../work_escape", "nested/work"} {
		t.Run(workID, func(t *testing.T) {
			if err := WithWorkAdmissionLock(homePath, workID, func() error {
				t.Fatal("invalid work identity callback ran")
				return nil
			}); err == nil {
				t.Fatalf("WithWorkAdmissionLock(%q) succeeded", workID)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(homePath, "lock")); !os.IsNotExist(err) {
		t.Fatalf("invalid identities materialized lock state: %v", err)
	}
}
