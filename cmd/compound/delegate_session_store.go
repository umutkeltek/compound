package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"compound/internal/work"
)

// The lane owner already publishes where a harness keeps its session store:
// `sb profile plan <profile> --json` returns a `harness_settings` artifact, and
// its parent directory is the profile home containing `projects/`. Compound
// consumes that instead of keeping its own copy of the path.
//
// Copying it here would recreate the exact defect this fixes. The lane doctor
// already reports one path while the launcher uses another, and the harvester
// trusted the reported one and found nothing. A second hand-maintained copy in
// Compound would drift the same way. Ask the owner, or admit you do not know.
const laneSettingsArtifactKind = "harness_settings"

type lanePlanArtifact struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type lanePlan struct {
	Artifacts []lanePlanArtifact `json:"artifacts"`
}

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

func resolveLaneSessionStore(harness, lane string) (string, error) {
	bin, err := exec.LookPath("sb")
	if err != nil {
		return "", fmt.Errorf("%w: lane profile owner (sb) not on PATH", work.ErrSessionStoreRootUnknown)
	}
	candidates := laneProfileCandidates(harness, lane)
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w: binding names neither a lane nor a harness", work.ErrSessionStoreRootUnknown)
	}
	for _, profile := range candidates {
		out, runErr := exec.Command(bin, "profile", "plan", profile, "--json").Output()
		if runErr != nil {
			continue
		}
		var plan lanePlan
		if json.Unmarshal(out, &plan) != nil {
			continue
		}
		for _, a := range plan.Artifacts {
			if a.Kind != laneSettingsArtifactKind || strings.TrimSpace(a.Path) == "" {
				continue
			}
			// The artifact is the settings FILE; the profile home is its parent,
			// which is what holds `projects/`.
			return filepath.Dir(a.Path), nil
		}
	}
	return "", fmt.Errorf(
		"%w: no launch profile among [%s] published a %s artifact",
		work.ErrSessionStoreRootUnknown, strings.Join(candidates, ", "), laneSettingsArtifactKind,
	)
}

func init() {
	work.DelegateSessionStoreResolver = resolveLaneSessionStore
}
