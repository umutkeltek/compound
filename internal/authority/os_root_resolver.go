package authority

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/mem"
)

// AbsorbedOSRegistryKey is the os-path-authority.yaml canonical_roots key an
// operator uses to declare where their absorbed operating-system control
// plane lives on disk. This is the single-owner seam: the operator declares
// WHERE the absorbed OS lives (root path = data, via
// `compound authority path-registry`); compound owns the internal layout
// below that root (the AbsorbedOS* sub-path constants below). No absorbed-OS
// path is ever hardcoded into product source.
const (
	AbsorbedOSRegistryKey   = "umut_os"
	InstalledHarnessHomeKey = "installed_harness_home"

	InstalledHarnessEntrypointRevisionPrefix = "installed-entrypoint://"
)

// absorbedOSEntityRef mirrors pathAuthorityFromOSRoot's entity_ref minting
// ("source:" + key with underscores replaced by hyphens) for the
// AbsorbedOSRegistryKey key.
var (
	absorbedOSEntityRef     = "source:" + strings.ReplaceAll(AbsorbedOSRegistryKey, "_", "-")
	installedHarnessHomeRef = "source:" + strings.ReplaceAll(InstalledHarnessHomeKey, "_", "-")
)

// Compound-owned sub-paths beneath an absorbed OS root. These are CODE (the
// internal layout compound expects), not operator data — only the root
// itself is operator-declared.
const (
	AbsorbedOSExecLaneEntrypoint = "exec-lane/os.ts"
	AbsorbedOSSteeringConfig     = "spine/steering.config.ts"
	AbsorbedOSSkillProposals     = "state/skill-surfacing-proposals.json"
	AbsorbedOSEventsDir          = "events"
)

// ResolveAbsorbedOSRoot resolves the operator-declared absorbed-OS root from
// the promoted current_path claim on the umut_os path-authority entity.
// It returns found=false (never an error) whenever there is nothing to
// resolve: no COMPOUND_HOME ledger yet, or no current_path claim for the
// umut_os key. That is the public-clone skip case, not a fault — a fresh
// clone with no operator os-path-authority.yaml imported must resolve
// found=false, not fail.
func ResolveAbsorbedOSRoot(compoundHome string) (string, bool, error) {
	return resolveDeclaredOSPath(compoundHome, absorbedOSEntityRef)
}

// ResolveInstalledHarnessHome resolves the live harness installation home
// from Compound's promoted path-authority claim. Unlike os.UserHomeDir, this
// binding cannot be redirected by a process-local HOME override.
func ResolveInstalledHarnessHome(compoundHome string) (string, bool, error) {
	return resolveDeclaredOSPath(compoundHome, installedHarnessHomeRef)
}

// InstalledHarnessEntrypointBinding returns the artifact revision that a
// cmd-layer orient materialization must bind into both its ContextPack and
// EffectiveContract. A public clone without an installed-harness marker has
// no binding. Once the marker exists, supported harness entrypoints fail
// closed when their exact installed file is missing, unreadable, or not a
// regular file.
func InstalledHarnessEntrypointBinding(compoundHome, harness string) (string, string, bool, error) {
	var relativePath string
	switch strings.TrimSpace(harness) {
	case "codex":
		relativePath = osAbsorptionServedCodexEntrypoint
	case "claude":
		relativePath = osAbsorptionServedClaudeEntrypoint
	default:
		return "", "", false, nil
	}
	installedHome, found, err := ResolveInstalledHarnessHome(compoundHome)
	if err != nil {
		return "", "", false, err
	}
	if !found {
		return "", "", false, nil
	}
	entrypointPath := filepath.Join(installedHome, filepath.FromSlash(relativePath))
	info, err := os.Stat(entrypointPath)
	if err != nil {
		return "", "", false, fmt.Errorf("installed %s entrypoint %s: %w", harness, entrypointPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", false, fmt.Errorf("installed %s entrypoint is not a regular file: %s", harness, entrypointPath)
	}
	body, err := os.ReadFile(entrypointPath)
	if err != nil {
		return "", "", false, fmt.Errorf("read installed %s entrypoint %s: %w", harness, entrypointPath, err)
	}
	digest := sha256.Sum256(body)
	return InstalledHarnessEntrypointRevisionPrefix + harness, "sha256:" + hex.EncodeToString(digest[:]), true, nil
}

func resolveDeclaredOSPath(compoundHome, entityRef string) (string, bool, error) {
	store, err := mem.OpenReadOnly(compoundHome)
	if err != nil {
		// No COMPOUND_HOME, or no ledger dir yet: nothing declared, skip.
		return "", false, nil
	}
	claim, ok, err := store.CurrentClaim(entityRef, "current_path")
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	root := strings.TrimSpace(claim.Object)
	if root == "" {
		return "", false, nil
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", false, fmt.Errorf("declared path authority %s current_path must be absolute, got %q", entityRef, root)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			if entityRef == installedHarnessHomeRef {
				return "", false, fmt.Errorf("declared installed harness home must be an existing directory: %s", root)
			}
			return root, true, nil
		}
		return "", false, fmt.Errorf("canonicalize declared path authority %s current_path %s: %w", entityRef, root, err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	if canonicalRoot != root {
		return "", false, fmt.Errorf(
			"declared path authority %s current_path is no longer canonical: claim=%s resolved=%s",
			entityRef,
			root,
			canonicalRoot,
		)
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", false, fmt.Errorf("inspect declared path authority %s current_path %s: %w", entityRef, canonicalRoot, err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("declared path authority %s current_path is not a directory: %s", entityRef, canonicalRoot)
	}
	return canonicalRoot, true, nil
}

// ResolveAbsorbedOSEntrypoint resolves subPath (one of the compound-owned
// AbsorbedOS* sub-path constants, forward-slash form) beneath the
// operator-declared absorbed-OS root. It returns found=false whenever the
// root is not declared OR the declared root is absent on disk (a
// stale/moved registry entry skips exactly like an absent registry — a
// declared-but-missing path is never treated as a hard failure).
func ResolveAbsorbedOSEntrypoint(compoundHome, subPath string) (string, bool, error) {
	root, ok, err := ResolveAbsorbedOSRoot(compoundHome)
	if err != nil {
		return "", false, err
	}
	if !ok || !absorbedOSRootPresentOnDisk(root) {
		return "", false, nil
	}
	return filepath.Join(root, filepath.FromSlash(subPath)), true, nil
}

// absorbedOSRootPresentOnDisk reports whether a declared absorbed-OS root
// actually exists as a directory. A declared-but-absent root (stale or moved
// registry entry) is treated identically to an absent declaration.
func absorbedOSRootPresentOnDisk(root string) bool {
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// osAbsorptionAbsorbedOSSkipReason is the standard proof_detail every
// OS-absorption wrapper proof reports when it declines to run because no
// absorbed-OS root is configured on this machine (the public-clone case).
const osAbsorptionAbsorbedOSSkipReason = "no absorbed-OS root declared (os-path-authority key 'umut_os' absent or path missing) — OS-absorption proofs skipped"

// resolveAbsorbedOSRootForProof is the shared entry check every OS-absorption
// wrapper proof (os_capability, os_skills, os_status) calls first: it
// resolves the declared root and confirms it is present on disk, folding
// "not declared" and "declared but absent" into the same skipped outcome
// (returned via skip, never via err) so a public clone with no absorbed-OS
// root imported skips these proofs instead of failing them. err is reserved
// for a genuine resolver fault (e.g. an unreadable ledger).
func resolveAbsorbedOSRootForProof(compoundHome string) (root string, skip *osAbsorptionProofResult, err error) {
	root, found, err := ResolveAbsorbedOSRoot(compoundHome)
	if err != nil {
		return "", nil, err
	}
	if !found || !absorbedOSRootPresentOnDisk(root) {
		return "", &osAbsorptionProofResult{skipped: true, reason: osAbsorptionAbsorbedOSSkipReason}, nil
	}
	return root, nil, nil
}
