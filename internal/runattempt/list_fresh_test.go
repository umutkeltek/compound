package runattempt

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestListFreshlyAggregatesNewCorruptionAfterSuccessfulRead(t *testing.T) {
	home := t.TempDir()
	writer := Open(home)
	first, err := writer.Mint("work_first", "test", "os-harness-entrypoints", time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.Mint("work_second", "test", "os-harness-entrypoints", time.Date(2026, 10, 14, 0, 0, 1, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	reader := Open(home)
	if got, err := reader.List(); err != nil || len(got) != 2 {
		t.Fatalf("initial List = %d attempts, %v; want 2 valid attempts", len(got), err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if err := os.WriteFile(filepath.Join(home, "run_attempts", id+".json"), []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := reader.List()
	if len(got) != 0 {
		t.Fatalf("corrupt attempts surfaced as readable: %d", len(got))
	}
	var unreadable *UnreadableRunAttemptsError
	if !errors.As(err, &unreadable) {
		t.Fatalf("List error = %v; want aggregate unreadable error", err)
	}
	want := []string{first.ID, second.ID}
	sort.Strings(want)
	if ids := UnreadableRunAttemptIDs(err); !reflect.DeepEqual(ids, want) {
		t.Fatalf("unreadable ids = %v; want %v", ids, want)
	}
}
