package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/policy"
)

func TestTypeSafeCredentialEnrollmentIsGovernedAndContentFree(t *testing.T) {
	home := t.TempDir()
	key := []byte("synthetic-typesafe-test-key")
	path := filepath.Join(home, "secrets", "typesafe", "jev-api-key")
	refused, err := StoreTypeSafeCredential(home, "work_browser_test", key, policy.DefaultConfig(), testTime())
	if err != nil || !refused.Refused || refused.ReceiptID == "" || refused.TransitionID == "" {
		t.Fatalf("default security hold did not refuse: %+v err=%v", refused, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("held credential was written")
	}
	cfg := policy.DefaultConfig()
	cfg.StandingGrants = append(cfg.StandingGrants, policy.StandingGrant{
		ID: "synthetic-jev-credential", Capability: "browser.credential.store",
		MutationClass: "security", ReversibilityCeiling: "hard", BlastRadius: "local",
	})
	stored, err := StoreTypeSafeCredential(home, "work_browser_test", key, cfg, testTime())
	if err != nil || !stored.Stored || stored.ReceiptID == "" || stored.DecisionID == "" {
		t.Fatalf("governed credential store failed: %+v err=%v", stored, err)
	}
	second, err := StoreTypeSafeCredential(home, "work_browser_test", key, cfg, testTime().Add(time.Second))
	if err != nil || !second.Stored || second.ReceiptID == "" || second.TransitionID == "" {
		t.Fatalf("same-key retry lacks terminal audit: %+v err=%v", second, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode: %v %v", info, err)
	}
	resolved, err := capgraph.ResolveCredentialForSurface(home, "tool:browser-jev-typesafe", "work_browser_test", jevCredentialHandle())
	if err != nil || resolved != "TYPESAFE_API_KEY="+string(key) {
		t.Fatalf("credential handle did not resolve: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := capgraph.ResolveCredentialForSurface(home, "tool:browser-jev-typesafe", "work_browser_test", jevCredentialHandle()); err == nil {
		t.Fatal("world-readable credential was accepted")
	}
	for _, dir := range []string{"events", "registries"} {
		_ = filepath.WalkDir(filepath.Join(home, dir), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if strings.Contains(string(body), string(key)) {
				t.Errorf("credential value leaked into %s", dir)
			}
			return nil
		})
	}
}

func TestTypeSafeCredentialEnrollmentRejectsSymlinkCustodyAndDifferentValue(t *testing.T) {
	cfg := policy.DefaultConfig()
	cfg.StandingGrants = append(cfg.StandingGrants, policy.StandingGrant{
		ID: "synthetic-jev-credential", Capability: "browser.credential.store",
		MutationClass: "security", ReversibilityCeiling: "hard", BlastRadius: "local",
	})
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "secrets")); err != nil {
		t.Fatal(err)
	}
	if _, err := StoreTypeSafeCredential(home, "work_browser_test", []byte("synthetic-first-key"), cfg, testTime()); err == nil {
		t.Fatal("symlinked custody accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "typesafe", "jev-api-key")); !os.IsNotExist(err) {
		t.Fatal("key escaped custody")
	}
	if err := os.Remove(filepath.Join(home, "secrets")); err != nil {
		t.Fatal(err)
	}
	first, err := StoreTypeSafeCredential(home, "work_browser_test", []byte("synthetic-first-key"), cfg, testTime().Add(time.Second))
	if err != nil || !first.Stored {
		t.Fatalf("first enrollment failed: %+v %v", first, err)
	}
	if _, err := StoreTypeSafeCredential(home, "work_browser_test", []byte("synthetic-different-key"), cfg, testTime().Add(2*time.Second)); err == nil {
		t.Fatal("credential-set replaced an existing secret")
	}
	got, err := os.ReadFile(filepath.Join(home, "secrets", "typesafe", "jev-api-key"))
	if err != nil || string(got) != "synthetic-first-key" {
		t.Fatal("first credential changed")
	}
}

func TestTypeSafeCredentialRotationRequiresGrantAndExactSlot(t *testing.T) {
	home := t.TempDir()
	first := []byte("synthetic-initial-key")
	next := []byte("synthetic-replacement-key")
	cfg := policy.DefaultConfig()
	cfg.StandingGrants = append(cfg.StandingGrants, policy.StandingGrant{
		ID: "synthetic-jev-credential", Capability: "browser.credential.store",
		MutationClass: "security", ReversibilityCeiling: "hard", BlastRadius: "local",
	})
	if _, err := RotateTypeSafeCredential(home, "work_browser_test", next, cfg, testTime()); err == nil {
		t.Fatal("rotation without registered slot accepted")
	}
	if change, err := StoreTypeSafeCredential(home, "work_browser_test", first, cfg, testTime().Add(time.Second)); err != nil || !change.Stored {
		t.Fatalf("initial enrollment failed: %+v %v", change, err)
	}
	if change, err := RotateTypeSafeCredential(home, "work_browser_test", next, policy.DefaultConfig(), testTime().Add(2*time.Second)); err != nil || !change.Refused {
		t.Fatalf("rotation escaped security policy: %+v %v", change, err)
	}
	change, err := RotateTypeSafeCredential(home, "work_browser_test", next, cfg, testTime().Add(3*time.Second))
	if err != nil || !change.Stored || change.ReceiptID == "" || change.TransitionID == "" {
		t.Fatalf("rotation failed: %+v %v", change, err)
	}
	resolved, err := capgraph.ResolveCredentialForSurface(home, jevCredentialSurface, "work_browser_test", jevCredentialHandle())
	if err != nil || resolved != "TYPESAFE_API_KEY="+string(next) {
		t.Fatalf("rotation not live: %v", err)
	}
}
