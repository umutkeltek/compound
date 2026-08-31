package landlease

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStatusReadOnlyDoesNotMaterializeMissingLock(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	status, err := StatusReadOnly(homePath, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if status.Held || status.IsExpired || status.Holder != "" {
		t.Fatalf("missing read-only status = %+v, want unheld", status)
	}
	entries, err := os.ReadDir(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("StatusReadOnly materialized fresh home: %v", entries)
	}
}

func TestStatusReadOnlyUsesExistingLockWithoutMutatingLease(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	lease, err := Acquire(homePath, "test-holder", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(homePath, stateRelPath)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	status, err := StatusReadOnly(homePath, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Held || status.Holder != lease.Holder || status.IsExpired {
		t.Fatalf("read-only status = %+v, want live holder %q", status, lease.Holder)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("StatusReadOnly mutated lease state:\nbefore=%s\nafter=%s", before, after)
	}
}
