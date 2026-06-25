package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLedgerKeepsCanonicalObjectsUnderOneRoot(t *testing.T) {
	root := t.TempDir()
	l, err := Open(root)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}

	if err := l.Append("claims", map[string]string{"id": "cl_1", "object": "one root"}); err != nil {
		t.Fatalf("append claim: %v", err)
	}
	if err := l.Append("evidence", map[string]string{"id": "ev_1", "source": "fixture"}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}

	for _, name := range []string{"claims.jsonl", "evidence.jsonl"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s under ledger root: %v", name, err)
		}
	}

	var seen []string
	if err := l.Each("claims", func(line []byte) error {
		var row struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return err
		}
		seen = append(seen, row.ID)
		return nil
	}); err != nil {
		t.Fatalf("read claims: %v", err)
	}
	if len(seen) != 1 || seen[0] != "cl_1" {
		t.Fatalf("unexpected claims read: %#v", seen)
	}
}

func TestLedgerMissingKindIsEmptyNotASecondPlacement(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}

	called := false
	if err := l.Each("positions", func([]byte) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("missing kind should be empty: %v", err)
	}
	if called {
		t.Fatal("missing kind unexpectedly produced records")
	}
}
