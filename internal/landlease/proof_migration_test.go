package landlease

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func unmigratedProofDomainFixture(t *testing.T) ([]string, string) {
	t.Helper()
	backing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	homes := []string{t.TempDir(), t.TempDir()}
	for i, h := range homes {
		homes[i], err = filepath.EvalSymlinks(h)
		if err != nil {
			t.Fatal(err)
		}
		if err := MigrateLegacyLayout(homes[i], backing, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(homes)
	return homes, backing
}
func TestProofMigrationInterruptedFenceRefusesOldAndNewClients(t *testing.T) {
	for _, stage := range []string{"proof-fenced", "proof-prepared"} {
		t.Run(stage, func(t *testing.T) {
			homes, backing := unmigratedProofDomainFixture(t)
			omitted, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := MigrateLegacyLayout(omitted, backing, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			err = MigrateProofDomain(backing, homes, func() error { return nil }, func(got string) error {
				if got == stage {
					return errors.New("interrupted")
				}
				return nil
			})
			if err == nil {
				t.Fatal("injected interruption missing")
			}
			// This is the exact scoped path an old binary would use, bypassing new routing.
			if _, err := Acquire(scopedRoot(backing, ProofResourceKey()), "old", time.Minute, time.Now()); err == nil {
				t.Fatal("old client bypassed permanent fence")
			}
			if _, err := ProofResource(homes[0]).Acquire("upgraded", time.Minute, time.Now()); err == nil {
				t.Fatal("upgraded historical client admitted during cutover")
			}
			if _, err := ProofResource(omitted).Acquire("omitted-old", time.Minute, time.Now()); err == nil {
				t.Fatal("omitted scoped home bypassed shared fence")
			}
			if err := MigrateProofDomain(backing, homes, func() error { return nil }, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := ProofCoordinationFor(omitted, ProofCapacityNativeConsumer, strings.Repeat("a", 64)); err == nil {
				t.Fatal("unenrolled home admitted")
			}
			if _, err := ProofCoordinationFor(homes[0], ProofCapacityNativeConsumer, strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestProofMigrationRefusesLeaseDescendantsAndOwnerCensus(t *testing.T) {
	for _, mode := range []string{"expired-lease", "descendant", "census"} {
		t.Run(mode, func(t *testing.T) {
			homes, backing := unmigratedProofDomainFixture(t)
			root := scopedRoot(backing, ProofResourceKey())
			if mode == "expired-lease" {
				if _, err := Acquire(root, "old", time.Second, time.Now().Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "descendant" {
				barrier, err := lockExecutionBarrier(root, "descendant")
				if err != nil {
					t.Fatal(err)
				}
				defer barrier.Close()
			}
			err := MigrateProofDomain(backing, homes, func() error {
				if mode == "census" {
					return errors.New("pending recovery")
				}
				return nil
			}, nil)
			if err == nil {
				t.Fatal("unsafe migration admitted")
			}
			if _, err := os.Stat(proofDomainManifestPath(backing)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refusal published manifest: %v", err)
			}
		})
	}
}
func TestProofMigrationActiveManifestRequiresExactFence(t *testing.T) {
	homes, backing, _ := proofDomainFixture(t)
	path, _ := statePath(scopedRoot(backing, ProofResourceKey()))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ProofCoordinationFor(homes[0], ProofCapacityNativeConsumer, strings.Repeat("a", 64)); err == nil {
		t.Fatal("active manifest without fence admitted")
	}
}
