package work

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The lane owner already publishes where a harness keeps its session store:
// `sb profile plan <profile> --json` returns a `harness_settings` artifact, and
// its parent directory is the profile home containing `projects/`. Compound
// consumes that instead of keeping its own copy of the path.
//
// Copying the path here would recreate the exact defect this guards against.
// The lane doctor already reported one path while the launcher used another,
// and the harvester trusted the reported one and found nothing. A second
// hand-maintained copy in Compound would drift the same way. Ask the owner, or
// admit you do not know.
//
// The candidate enumeration and the owner consultation live HERE, beside the
// transcript lookup that consumes them, so the enrolled-lane contract (every
// lane resolves a root or carries an actionable repair) is testable without a
// live `sb` on PATH. The CLI installs only the process seam.

// SessionStoreSettingsArtifactKind is the exact artifact kind the lane owner
// publishes whose parent directory is the profile home holding `projects/`.
const SessionStoreSettingsArtifactKind = "harness_settings"

// DelegateSessionStorePlanLookupFunc answers "which artifacts does launch
// profile <id> publish" as artifact kind -> path. The CLI installs the
// `sb profile plan <id> --json` implementation; tests install a fixture-backed
// stand-in for the owner.
type DelegateSessionStorePlanLookupFunc func(profile string) (artifacts map[string]string, err error)

// DelegateSessionStorePlanLookup is installed by the composition root. When
// nil, lane artifact resolution fails closed rather than guessing homes.
var DelegateSessionStorePlanLookup DelegateSessionStorePlanLookupFunc

// laneProfileCandidates maps a binding's harness/lane onto the launch-profile
// ids the lane owner might know it by. Historical bindings recorded the lane
// inconsistently -- bare (`minimax`) and prefixed (`claude-minimax`) forms both
// exist -- so try the specific form first and fall back, rather than assuming
// one convention and reporting "unknown" for every older binding.
func laneProfileCandidates(harness, lane string) []string {
	lane = strings.TrimSpace(lane)
	harness = strings.TrimSpace(harness)
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}
	add(lane)
	if lane != "" && !strings.HasPrefix(lane, "claude-") {
		add("claude-" + lane)
	}
	// Compound spells harnesses with underscores (`claude_minimax`); the lane
	// owner spells profiles with dashes.
	add(strings.ReplaceAll(harness, "_", "-"))
	return out
}

// ResolveLaneArtifacts asks the lane owner what it publishes for a harness and
// returns artifact kind -> path. One query serves every consumer that needs a
// lane's real files, so no caller has to learn the profile-id conventions twice.
func ResolveLaneArtifacts(harness, lane string) (map[string]string, error) {
	candidates := laneProfileCandidates(harness, lane)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: names neither a lane nor a harness", ErrSessionStoreRootUnknown)
	}
	if DelegateSessionStorePlanLookup == nil {
		return nil, fmt.Errorf("%w: no launch-profile plan lookup is installed", ErrSessionStoreRootUnknown)
	}
	for _, profile := range candidates {
		artifacts, lookupErr := DelegateSessionStorePlanLookup(profile)
		if lookupErr != nil || artifacts == nil {
			continue
		}
		if _, ok := artifacts[SessionStoreSettingsArtifactKind]; ok {
			return artifacts, nil
		}
	}
	return nil, fmt.Errorf(
		"%w: no launch profile among [%s] published a %s artifact",
		ErrSessionStoreRootUnknown, strings.Join(candidates, ", "), SessionStoreSettingsArtifactKind,
	)
}

// ResolveDelegateSessionStore resolves the profile home root for a lane's
// harness: the directory CONTAINING `projects/`, derived from the owner's
// harness_settings artifact. The CLI installs this as
// DelegateSessionStoreResolver.
func ResolveDelegateSessionStore(harness, lane string) (string, error) {
	artifacts, err := ResolveLaneArtifacts(harness, lane)
	if err != nil {
		return "", err
	}
	// The artifact is the settings FILE; the profile home is its parent, which is
	// what holds `projects/`.
	return filepath.Dir(artifacts[SessionStoreSettingsArtifactKind]), nil
}
