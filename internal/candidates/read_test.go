package candidates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Depth probe for the one candidate-inbox reader: it must enforce the id-match
// contract, tolerate an empty stored id, surface a missing file as
// os.IsNotExist (multi-inbox callers depend on it), and reject an unknown kind
// before touching disk. Fails if any reader's guard semantics regress.
func TestReadCandidate(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, InboxDir(Memory))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type rec struct {
		ID     string `json:"id"`
		Entity string `json:"entity"`
	}

	write("c1.json", `{"id":"c1","entity":"x"}`)
	var c rec
	if err := ReadCandidate(home, Memory, "c1", &c); err != nil || c.Entity != "x" {
		t.Fatalf("matching id: %+v err=%v", c, err)
	}

	// An empty stored id is allowed — the caller defaults it.
	write("c2.json", `{"entity":"y"}`)
	c = rec{}
	if err := ReadCandidate(home, Memory, "c2", &c); err != nil || c.Entity != "y" {
		t.Fatalf("empty stored id: %+v err=%v", c, err)
	}

	// A non-empty mismatched id is the contract violation.
	write("c3.json", `{"id":"other","entity":"z"}`)
	if err := ReadCandidate(home, Memory, "c3", &rec{}); err == nil {
		t.Fatal("mismatched id must error")
	}

	// A missing file is an os.IsNotExist error (multi-inbox callers continue on it).
	if err := ReadCandidate(home, Memory, "nope", &rec{}); !os.IsNotExist(err) {
		t.Fatalf("missing file: want IsNotExist, got %v", err)
	}

	// An unknown kind errors before touching disk.
	if err := ReadCandidate(home, Kind("bogus-kind"), "x", &rec{}); err == nil {
		t.Fatal("unknown kind must error")
	}
}

// Depth probe for the candidate-inbox writer: it shares one path + serialization
// with ReadCandidate (round-trips), writes the canonical two-space JSON + newline,
// and rejects an unknown kind before touching disk.
func TestWriteCandidate(t *testing.T) {
	home := t.TempDir()
	type rec struct {
		ID     string `json:"id"`
		Entity string `json:"entity"`
	}
	if err := WriteCandidate(home, Memory, "c1", rec{ID: "c1", Entity: "x"}); err != nil {
		t.Fatal(err)
	}
	var got rec
	if err := ReadCandidate(home, Memory, "c1", &got); err != nil || got.Entity != "x" {
		t.Fatalf("write→read round-trip: %+v err=%v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, InboxDir(Memory), "c1.json"))
	if !strings.HasSuffix(string(b), "}\n") || !strings.Contains(string(b), "\n  \"id\":") {
		t.Fatalf("serialization not canonical (two-space + trailing newline): %q", string(b))
	}
	if err := WriteCandidate(home, Kind("bogus-kind"), "x", rec{}); err == nil {
		t.Fatal("unknown kind must error before write")
	}
}
