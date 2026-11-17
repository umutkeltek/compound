package work

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLeaseReportsOneWorkLeaseReadOnly(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "work", "leases")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work_held.json"), []byte(`{"work_id":"work_held","by":"lane:other","claimed_at":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work_bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	lease, held, err := s.ReadLease("work_held")
	if err != nil || !held || lease.By != "lane:other" || lease.WorkID != "work_held" {
		t.Fatalf("held lease = %+v held=%v err=%v", lease, held, err)
	}
	if lease, held, err := s.ReadLease("work_free"); err != nil || held || lease != (Lease{}) {
		t.Fatalf("missing lease must be no lease, got %+v held=%v err=%v", lease, held, err)
	}
	if _, held, err := s.ReadLease("work_bad"); err == nil || held {
		t.Fatalf("unreadable lease must be an error, got held=%v err=%v", held, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 2 {
		t.Fatalf("read must not change the lease folder: %d entries, err=%v", len(entries), err)
	}
}
