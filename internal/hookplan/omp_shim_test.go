package hookplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shim is the whole point of the install path — a registry row that claims
// hook coverage while the embedded source lost its contract fails here, not in
// a live session.
func TestOmpShimSourceCarriesOwnershipAndContract(t *testing.T) {
	if !strings.HasPrefix(ompHookShimSource, OmpShimMarker) {
		t.Fatalf("embedded shim must start with %q", OmpShimMarker)
	}
	for _, needle := range []string{
		"pi.on(\"session_start\"",
		"pi.on(\"before_agent_start\"",
		"pi.on(\"tool_call\"",
		"pi.on(\"tool_result\"",
		"pi.on(\"session_shutdown\"",
		"export default function compoundHook",
	} {
		if !strings.Contains(ompHookShimSource, needle) {
			t.Errorf("embedded shim lost %s — the installed hook surface would silently shrink", needle)
		}
	}
}

func TestInstallOhMyPiShimWritesOwnedFile(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallOhMyPiShim(base, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "installed" {
		t.Fatalf("report = %+v, want one installed entry", rep.Entries)
	}
	body, err := os.ReadFile(filepath.Join(base, OmpShimRelPath))
	if err != nil {
		t.Fatalf("shim not written: %v", err)
	}
	if !strings.Contains(string(body), OmpShimMarker) {
		t.Error("written shim lost the ownership marker")
	}
	// Idempotence: same bytes → ok, no churn.
	rep, err = InstallOhMyPiShim(base, false)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if rep.Entries[0].Action != "ok" {
		t.Fatalf("second install action = %s, want ok", rep.Entries[0].Action)
	}
}

func TestInstallOhMyPiShimRefusesForeignFile(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, OmpShimRelPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("export default function userHook() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := InstallOhMyPiShim(base, false)
	if err == nil || !strings.Contains(err.Error(), "not compound-owned") {
		t.Fatalf("install over a foreign shim must fail closed, got %v", err)
	}
}

func TestInstallOhMyPiShimCheckModeWritesNothing(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallOhMyPiShim(base, true)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if rep.Entries[0].Action != "absent" {
		t.Fatalf("check action = %s, want absent", rep.Entries[0].Action)
	}
	if _, err := os.Stat(filepath.Join(base, OmpShimRelPath)); !os.IsNotExist(err) {
		t.Fatal("check mode must not create the shim")
	}
}

func TestResolveInstallRequestAcceptsOhMyPi(t *testing.T) {
	req, err := ResolveInstallRequest("", "oh-my-pi", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(req.Harnesses) != 1 || req.Harnesses[0] != "oh-my-pi" {
		t.Fatalf("harnesses = %v, want [oh-my-pi]", req.Harnesses)
	}
}
