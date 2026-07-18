package candidates

import (
	"bytes"
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

func TestCreateCandidateIfAbsentPreservesExistingBytes(t *testing.T) {
	type rec struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Marker string `json:"marker"`
	}
	payload := rec{ID: "c1", Kind: string(Memory), Marker: "contender"}

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "same candidate", body: []byte("{\n  \"id\": \"c1\",\n  \"kind\": \"memory\",\n  \"marker\": \"contender\"\n}\n")},
		{name: "different candidate", body: []byte("{\"id\":\"c1\",\"kind\":\"memory\",\"marker\":\"existing\"}\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			target := filepath.Join(home, InboxDir(Memory), "c1.json")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, tc.body, 0o644); err != nil {
				t.Fatal(err)
			}

			created, err := CreateCandidateIfAbsent(home, Memory, "c1", payload)
			if err != nil {
				t.Fatalf("existing classifiable candidate: %v", err)
			}
			if created {
				t.Fatal("existing candidate reported created")
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.body) {
				t.Fatalf("existing candidate changed:\n got: %q\nwant: %q", got, tc.body)
			}
		})
	}

	t.Run("malformed candidate", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(home, InboxDir(Memory), "c1.json")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		want := []byte("{not-json\n")
		if err := os.WriteFile(target, want, 0o644); err != nil {
			t.Fatal(err)
		}

		created, err := CreateCandidateIfAbsent(home, Memory, "c1", payload)
		if err == nil {
			t.Fatal("malformed existing candidate must return an error")
		}
		if created {
			t.Fatal("malformed existing candidate reported created")
		}
		got, readErr := os.ReadFile(target)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("malformed candidate changed: got %q want %q", got, want)
		}
	})

	t.Run("unreadable candidate", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(home, InboxDir(Memory), "c1.json")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		want := []byte("{\"id\":\"c1\",\"kind\":\"memory\"}\n")
		if err := os.WriteFile(target, want, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(target, 0o644) })

		created, err := CreateCandidateIfAbsent(home, Memory, "c1", payload)
		if err == nil {
			t.Fatal("unreadable existing candidate must return an error")
		}
		if created {
			t.Fatal("unreadable existing candidate reported created")
		}
		if err := os.Chmod(target, 0o644); err != nil {
			t.Fatal(err)
		}
		got, readErr := os.ReadFile(target)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("unreadable candidate changed: got %q want %q", got, want)
		}
	})

	t.Run("non-regular candidate", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(home, InboxDir(Memory), "c1.json")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}

		created, err := CreateCandidateIfAbsent(home, Memory, "c1", payload)
		if err == nil {
			t.Fatal("non-regular existing candidate must return an error")
		}
		if created {
			t.Fatal("non-regular existing candidate reported created")
		}
		info, statErr := os.Lstat(target)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if !info.IsDir() {
			t.Fatalf("non-regular target changed type: %v", info.Mode())
		}
	})

	t.Run("symlink candidate", func(t *testing.T) {
		home := t.TempDir()
		dir := filepath.Join(home, InboxDir(Memory))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(home, "sentinel.json")
		want := []byte("sentinel bytes\n")
		if err := os.WriteFile(sentinel, want, 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "c1.json")
		if err := os.Symlink(sentinel, target); err != nil {
			t.Fatal(err)
		}

		created, err := CreateCandidateIfAbsent(home, Memory, "c1", payload)
		if err == nil {
			t.Fatal("symlink existing candidate must return an error")
		}
		if created {
			t.Fatal("symlink existing candidate reported created")
		}
		info, statErr := os.Lstat(target)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("candidate symlink was replaced: %v", info.Mode())
		}
		got, readErr := os.ReadFile(sentinel)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("symlink referent changed: got %q want %q", got, want)
		}
	})
}

// Guards (C3 hardening): the codec rejects path-shaped ids and payloads that
// contradict the path they would be filed under.
func TestCandidateCodecGuards(t *testing.T) {
	home := t.TempDir()
	type rec struct {
		ID   string `json:"id,omitempty"`
		Kind string `json:"kind,omitempty"`
	}
	for _, bad := range []string{"", "..", ".", "a/b", "../escape", "x\\y"} {
		if err := WriteCandidate(home, Memory, bad, rec{}); err == nil {
			t.Errorf("WriteCandidate id=%q: expected rejection", bad)
		}
		if err := ReadCandidate(home, Memory, bad, &rec{}); err == nil {
			t.Errorf("ReadCandidate id=%q: expected rejection", bad)
		}
	}
	if err := WriteCandidate(home, Memory, "c1", rec{ID: "other"}); err == nil {
		t.Fatal("payload id mismatch must be rejected")
	}
	if err := WriteCandidate(home, Memory, "c2", rec{Kind: "skill"}); err == nil {
		t.Fatal("payload kind mismatch must be rejected")
	}
	if err := WriteCandidate(home, Memory, "c3", rec{ID: "c3", Kind: "memory"}); err != nil {
		t.Fatalf("matching id+kind rejected: %v", err)
	}
}
