package mem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadStrangerDealtLogMissingFileIsEmptyNotError(t *testing.T) {
	homePath := t.TempDir()
	rows, err := ReadStrangerDealtLog(homePath)
	if err != nil {
		t.Fatalf("missing dealt-log must not error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %v, want empty", rows)
	}
}

func TestAppendStrangerDealtRowThenReadRoundTrips(t *testing.T) {
	homePath := t.TempDir()
	want := []StrangerDealtRow{
		{Ts: "2026-07-17T10:00:00Z", CardID: "stranger:cand_a", CandidateRef: "cand_a", Topic: "topic-a"},
		{Ts: "2026-07-17T11:00:00Z", CardID: "stranger:cand_b", CandidateRef: "cand_b", Topic: "topic-b"},
	}
	for _, row := range want {
		if err := AppendStrangerDealtRow(homePath, row); err != nil {
			t.Fatalf("append %+v: %v", row, err)
		}
	}
	got, err := ReadStrangerDealtLog(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestReadStrangerDealtLogCorruptLineErrors(t *testing.T) {
	homePath := t.TempDir()
	logPath := filepath.Join(homePath, filepath.FromSlash(StrangerDealtLogRelPath))
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("{\"ts\":\"2026-07-17T10:00:00Z\"}\nnot json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStrangerDealtLog(homePath); err == nil {
		t.Fatal("corrupt dealt-log line must surface an error so callers fail closed")
	}
}

func TestAppendStrangerDealtRowWritesOnlyTheOneFile(t *testing.T) {
	homePath := t.TempDir()
	if _, err := Open(homePath); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, homePath)
	if err := AppendStrangerDealtRow(homePath, StrangerDealtRow{Ts: "2026-07-17T10:00:00Z", CardID: "stranger:x", CandidateRef: "cand_x"}); err != nil {
		t.Fatal(err)
	}
	after := snapshotFiles(t, homePath)
	var added []string
	for f := range after {
		if !before[f] {
			added = append(added, f)
		}
	}
	wantPath := filepath.Join(homePath, filepath.FromSlash(StrangerDealtLogRelPath))
	if len(added) != 1 || added[0] != wantPath {
		t.Fatalf("unexpected new files: %v (want exactly [%s])", added, wantPath)
	}
}
