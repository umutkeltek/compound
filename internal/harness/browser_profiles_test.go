package harness

// browser_profiles_test.go — falsifier F4.
//
// F4: the browser-profile registry joins social-accounts.json by path.
// Seed through the door → 3 profiles, all pending_enrollment; create one
// dir → that one enrolled; an account path with no profile → doctor names
// it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureSocialAccounts writes a synthetic social-accounts.json with the
// given rows. Returns the home so callers can pass it on.
func fixtureSocialAccounts(t *testing.T, home string, accounts []map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "registries"), 0o755); err != nil {
		t.Fatalf("mkdir registries: %v", err)
	}
	body := map[string]any{
		"schema_version": SocialAccountsSchemaVersion,
		"accounts":       accounts,
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "registries", "social-accounts.json"), b, 0o644); err != nil {
		t.Fatalf("write social-accounts: %v", err)
	}
}

func fixedProfileNow() time.Time {
	return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
}

// F4: seed joins social-accounts; doctor counts; creating one dir flips
// that one to enrolled; an account whose path has no profile is named.
func TestBrowserProfileRegistryJoinsSocialAccounts(t *testing.T) {
	home := t.TempDir()
	// expandHome reads $HOME; redirect it to the test temp dir so
	// "~/social-profiles/profile-a" expands under the test fixture.
	t.Setenv("HOME", home)
	now := fixedProfileNow()

	// Three accounts, all pointing at profile dirs that do NOT exist yet.
	// The seeder will create 3 profiles, all pending_enrollment.
	profilesDir := filepath.Join(home, "social-profiles")
	accounts := []map[string]string{
		{"id": "acct-0001", "browser_profile": "~/social-profiles/profile-a"},
		{"id": "acct-0002", "browser_profile": "~/social-profiles/profile-b"},
		{"id": "acct-0003", "browser_profile": "~/social-profiles/profile-c"},
	}
	fixtureSocialAccounts(t, home, accounts)

	// Seed — should create 3 profiles, all pending.
	reg, err := SeedBrowserProfileRegistry(home, now)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := len(reg.Profiles); got != 3 {
		t.Fatalf("F4: seed wants 3 profiles, got %d", got)
	}
	for _, p := range reg.Profiles {
		if p.Status != StatusPendingEnrollment {
			t.Errorf("F4: profile %s status=%s want pending_enrollment (no dir on disk)", p.ID, p.Status)
		}
		if len(p.Logins) != 1 {
			t.Errorf("F4: profile %s logins=%d want 1", p.ID, len(p.Logins))
		}
	}

	// Persist through the door — write then re-read.
	if err := WriteBrowserProfileRegistry(home, reg); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	got, err := LoadBrowserProfileRegistry(home)
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	if len(got.Profiles) != 3 {
		t.Fatalf("F4: reloaded registry wants 3 profiles, got %d", len(got.Profiles))
	}

	// Create the dir for profile-a only — doctor should flip that one.
	if err := os.MkdirAll(filepath.Join(profilesDir, "profile-a"), 0o755); err != nil {
		t.Fatalf("mkdir profile-a: %v", err)
	}
	rep, err := DoctorBrowserProfileRegistry(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if rep.ProfileCount != 3 {
		t.Errorf("F4 doctor: profile_count=%d want 3", rep.ProfileCount)
	}
	if rep.EnrolledCount != 1 {
		t.Errorf("F4 doctor: enrolled_count=%d want 1", rep.EnrolledCount)
	}
	if rep.PendingCount != 2 {
		t.Errorf("F4 doctor: pending_count=%d want 2", rep.PendingCount)
	}
	// Find profile-a's row; assert enrolled.
	var sawA, sawB, sawC bool
	for _, p := range rep.Profiles {
		switch p.ID {
		case "acct-0001":
			sawA = true
			if p.Status != StatusEnrolled {
				t.Errorf("F4: profile-a status=%s want enrolled", p.Status)
			}
		case "acct-0002":
			sawB = true
			if p.Status != StatusPendingEnrollment {
				t.Errorf("F4: profile-b status=%s want pending", p.Status)
			}
		case "acct-0003":
			sawC = true
			if p.Status != StatusPendingEnrollment {
				t.Errorf("F4: profile-c status=%s want pending", p.Status)
			}
		}
	}
	if !sawA || !sawB || !sawC {
		t.Errorf("F4 doctor: missing expected profile rows (a=%v b=%v c=%v)", sawA, sawB, sawC)
	}
}

// F4 extension: an account path that does NOT resolve to a profile row is
// reported in unresolved_paths. We exercise this by deleting one profile
// row AFTER the seed — the next doctor run should name that path.
func TestBrowserProfileRegistryUnresolvedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := fixedProfileNow()

	accounts := []map[string]string{
		{"id": "acct-0001", "browser_profile": "~/social-profiles/profile-a"},
		{"id": "acct-0002", "browser_profile": "~/social-profiles/profile-b"},
	}
	fixtureSocialAccounts(t, home, accounts)

	reg, err := SeedBrowserProfileRegistry(home, now)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteBrowserProfileRegistry(home, reg); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Hand-edit: drop profile-a so its path becomes unresolved.
	loaded, _ := LoadBrowserProfileRegistry(home)
	for i, p := range loaded.Profiles {
		if p.ID == "acct-0001" {
			loaded.Profiles = append(loaded.Profiles[:i], loaded.Profiles[i+1:]...)
		}
	}
	if err := WriteBrowserProfileRegistry(home, loaded); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	rep, err := DoctorBrowserProfileRegistry(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if len(rep.UnresolvedPaths) != 1 {
		t.Fatalf("F4 unresolved: got %d paths, want 1", len(rep.UnresolvedPaths))
	}
	if !strings.HasSuffix(rep.UnresolvedPaths[0], "profile-a") {
		t.Errorf("F4 unresolved: path %q does not name profile-a", rep.UnresolvedPaths[0])
	}
}

// F4 edge: missing social-accounts.json → empty join, registry created
// with zero profiles. This is the fresh-home state.
func TestBrowserProfileRegistryFreshHome(t *testing.T) {
	home := t.TempDir()
	now := fixedProfileNow()

	reg, err := SeedBrowserProfileRegistry(home, now)
	if err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	if len(reg.Profiles) != 0 {
		t.Errorf("F4 fresh home: profiles=%d want 0", len(reg.Profiles))
	}
	if reg.SchemaVersion != BrowserProfileRegistrySchemaVersion {
		t.Errorf("F4 fresh home: schema_version=%q want %q", reg.SchemaVersion, BrowserProfileRegistrySchemaVersion)
	}
	// Write should be a no-op for an empty registry — no rows to add.
	if err := WriteBrowserProfileRegistry(home, reg); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	rep, err := DoctorBrowserProfileRegistry(home)
	if err != nil {
		t.Fatalf("doctor fresh: %v", err)
	}
	if rep.ProfileCount != 0 {
		t.Errorf("F4 fresh home: profile_count=%d want 0", rep.ProfileCount)
	}
	if len(rep.UnresolvedPaths) != 0 {
		t.Errorf("F4 fresh home: unresolved=%d want 0", len(rep.UnresolvedPaths))
	}
}
