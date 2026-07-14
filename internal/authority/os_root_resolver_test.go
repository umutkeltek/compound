package authority

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/mem"

	"gopkg.in/yaml.v3"
)

// seedAbsorbedOSRegistry writes a minimal os-path-authority.yaml declaring
// osRoot as the active absorbed_os canonical root and imports it into
// compoundHome's mem ledger exactly the way `compound authority
// path-registry` does in production (RegisterEntity + Capture + Adjudicate
// promote — see cmd/compound/main.go's "path-registry" handler). Every
// absorbed-OS proof fixture seeds through this one door instead of writing
// to the ledger by hand, so the persistence trace this package's resolver
// depends on is proven, not assumed.
func seedAbsorbedOSRegistry(t *testing.T, compoundHome, osRoot string) {
	t.Helper()
	registryPath := filepath.Join(t.TempDir(), "os-path-authority.yaml")
	body, err := yaml.Marshal(osPathRegistryFile{
		Version: 1,
		CanonicalRoots: map[string]osPathRoot{
			AbsorbedOSRegistryKey: {Path: osRoot, Status: "active"},
		},
	})
	if err != nil {
		t.Fatalf("marshal absorbed-os registry fixture: %v", err)
	}
	if err := os.WriteFile(registryPath, body, 0o644); err != nil {
		t.Fatalf("write absorbed-os registry fixture: %v", err)
	}

	store, err := mem.Open(compoundHome)
	if err != nil {
		t.Fatalf("open store to seed absorbed-os registry: %v", err)
	}
	existing, err := store.Entities()
	if err != nil {
		t.Fatalf("load entities to seed absorbed-os registry: %v", err)
	}
	_, entities, facts, report, err := ImportOSPathRegistry(registryPath, existing)
	if err != nil {
		t.Fatalf("import absorbed-os registry fixture: %v", err)
	}
	if report.Status != "ok" {
		t.Fatalf("import absorbed-os registry fixture: status=%s", report.Status)
	}
	now := time.Now()
	for _, entity := range entities {
		if _, err := store.RegisterEntity(entity, now); err != nil {
			t.Fatalf("register absorbed-os entity: %v", err)
		}
	}
	captured, err := store.Capture(facts, now)
	if err != nil {
		t.Fatalf("capture absorbed-os facts: %v", err)
	}
	for _, c := range captured {
		if _, err := store.Adjudicate(c.ID, "promote", "test-seed-absorbed-os-registry", now); err != nil {
			t.Fatalf("promote absorbed-os fact %s: %v", c.ID, err)
		}
	}
}

// TestResolveAbsorbedOSRootNoLedgerSkips is the public-clone falsifier: a
// COMPOUND_HOME with no ledger at all (fresh clone, no
// os-path-authority.yaml ever imported) must resolve found=false with a nil
// error, never an error and never a fabricated root.
func TestResolveAbsorbedOSRootNoLedgerSkips(t *testing.T) {
	compoundHome := filepath.Join(t.TempDir(), "compound-home") // deliberately never created
	root, found, err := ResolveAbsorbedOSRoot(compoundHome)
	if err != nil {
		t.Fatalf("no ledger should skip, not error: %v", err)
	}
	if found {
		t.Fatalf("no ledger should resolve found=false, got root=%q", root)
	}
	if root != "" {
		t.Fatalf("no ledger should resolve an empty root, got %q", root)
	}
}

// TestResolveAbsorbedOSRootSeededRegistryResolves proves the read side of
// the single-owner data flow: once an operator has imported
// os-path-authority.yaml (the seed helper mirrors that import), the resolver
// reads the promoted current_path claim back and reports the exact
// operator-declared root.
func TestResolveAbsorbedOSRootSeededRegistryResolves(t *testing.T) {
	root := t.TempDir()
	osRoot := filepath.Join(root, "absorbed-os")
	if err := os.MkdirAll(osRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	compoundHome := filepath.Join(root, "compound-home")
	if err := home.Init(compoundHome, "test", time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	seedAbsorbedOSRegistry(t, compoundHome, osRoot)

	got, found, err := ResolveAbsorbedOSRoot(compoundHome)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !found || got != osRoot {
		t.Fatalf("ResolveAbsorbedOSRoot = %q found=%v, want %q found=true", got, found, osRoot)
	}
}

// TestResolveAbsorbedOSEntrypointResolvesUnderDeclaredRoot proves the
// sub-path join for a root that is declared AND present on disk.
func TestResolveAbsorbedOSEntrypointResolvesUnderDeclaredRoot(t *testing.T) {
	root := t.TempDir()
	osRoot := filepath.Join(root, "absorbed-os")
	if err := os.MkdirAll(osRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	compoundHome := filepath.Join(root, "compound-home")
	if err := home.Init(compoundHome, "test", time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	seedAbsorbedOSRegistry(t, compoundHome, osRoot)

	got, found, err := ResolveAbsorbedOSEntrypoint(compoundHome, AbsorbedOSExecLaneEntrypoint)
	if err != nil {
		t.Fatalf("resolve entrypoint: %v", err)
	}
	want := filepath.Join(osRoot, "exec-lane", "os.ts")
	if !found || got != want {
		t.Fatalf("ResolveAbsorbedOSEntrypoint = %q found=%v, want %q found=true", got, found, want)
	}
}

// TestResolveAbsorbedOSEntrypointDeclaredButAbsentSkips is the second
// public-clone falsifier: a declared root that is absent on disk (stale or
// moved registry entry) must skip exactly like an absent registry — never a
// hard failure.
func TestResolveAbsorbedOSEntrypointDeclaredButAbsentSkips(t *testing.T) {
	root := t.TempDir()
	osRoot := filepath.Join(root, "absorbed-os-moved-away") // deliberately never created
	compoundHome := filepath.Join(root, "compound-home")
	if err := home.Init(compoundHome, "test", time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	seedAbsorbedOSRegistry(t, compoundHome, osRoot)

	path, found, err := ResolveAbsorbedOSEntrypoint(compoundHome, AbsorbedOSExecLaneEntrypoint)
	if err != nil {
		t.Fatalf("declared-but-absent should skip, not error: %v", err)
	}
	if found {
		t.Fatalf("declared-but-absent root should resolve found=false, got path=%q", path)
	}
}
