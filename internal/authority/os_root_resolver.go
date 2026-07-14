package authority

import (
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
const AbsorbedOSRegistryKey = "absorbed_os"

// absorbedOSEntityRef mirrors pathAuthorityFromOSRoot's entity_ref minting
// ("source:" + key with underscores replaced by hyphens) for the
// AbsorbedOSRegistryKey key.
var absorbedOSEntityRef = "source:" + strings.ReplaceAll(AbsorbedOSRegistryKey, "_", "-")

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
// the promoted current_path claim on the absorbed_os path-authority entity.
// It returns found=false (never an error) whenever there is nothing to
// resolve: no COMPOUND_HOME ledger yet, or no current_path claim for the
// absorbed_os key. That is the public-clone skip case, not a fault — a fresh
// clone with no operator os-path-authority.yaml imported must resolve
// found=false, not fail.
func ResolveAbsorbedOSRoot(compoundHome string) (string, bool, error) {
	store, err := mem.OpenReadOnly(compoundHome)
	if err != nil {
		// No COMPOUND_HOME, or no ledger dir yet: nothing declared, skip.
		return "", false, nil
	}
	claim, ok, err := store.CurrentClaim(absorbedOSEntityRef, "current_path")
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
	return root, true, nil
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
	if !ok {
		return "", false, nil
	}
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return "", false, nil
	}
	return filepath.Join(root, filepath.FromSlash(subPath)), true, nil
}
