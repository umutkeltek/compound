package home

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitAndDoctor(t *testing.T) {
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "local", time.Unix(0, 0)); err != nil {
		t.Fatalf("init: %v", err)
	}
	// idempotent: a second init must not error or destroy state.
	if err := Init(h, "local", time.Unix(0, 0)); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	r := Doctor(h)
	if !r.ManifestOK {
		t.Fatal("manifest not ok")
	}
	if r.DirsOK != len(Dirs) {
		for _, d := range r.Dirs {
			if !d.OK {
				t.Errorf("missing dir: %s", d.Name)
			}
		}
		t.Fatalf("dirs %d/%d ok", r.DirsOK, len(Dirs))
	}
	if !r.OK {
		t.Fatalf("doctor not ok: %+v", r)
	}
	if r.Manifest.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %q, want %q", r.Manifest.SchemaVersion, SchemaVersion)
	}
	// the durable ledger dir must exist under the home, not the repo.
	if _, err := os.Stat(filepath.Join(h, "ledger")); err != nil {
		t.Fatalf("ledger dir missing: %v", err)
	}
}

func TestDoctorMissingIsNotOK(t *testing.T) {
	h := t.TempDir() // empty: no manifest, no dirs
	r := Doctor(h)
	if r.OK {
		t.Fatal("empty dir reported OK")
	}
	if r.ManifestOK {
		t.Fatal("missing manifest reported OK")
	}
}

func TestResolvePrecedence(t *testing.T) {
	env := t.TempDir()
	t.Setenv("COMPOUND_HOME", env)
	got, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got != env {
		t.Fatalf("env home = %q, want %q", got, env)
	}
	flag := t.TempDir()
	if got, _ = Resolve(flag); got != flag {
		t.Fatalf("--home = %q, want %q (flag must beat env)", got, flag)
	}
}
