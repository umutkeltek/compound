package main

import (
	"crypto/sha256"
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
const (
	laneSettingsArtifactKind        = "harness_settings"
	laneLaunchProfilePlanSchema     = "switchback/launch-profile-plan@1"
	laneLaunchProfileOwner          = "switchback"
	laneLaunchProfileCompoundRole   = "consumer"
	laneLaunchProfileProjection     = "non_secret_profile_conformance"
	laneLaunchProfileTransportID    = "switchback"
	laneLaunchProfileCaptureMode    = "segmented_full_wire"
	laneLaunchProfilePlanMaxBytes   = 1 << 20
	delegateLaunchProfilePlanSchema = "compound.delegate_launch_profile_plan.v0.1"
)

type lanePlanArtifact struct {
	Kind          string  `json:"kind"`
	Path          string  `json:"path"`
	Exists        bool    `json:"exists"`
	Changed       bool    `json:"changed"`
	CurrentSHA256 *string `json:"current_sha256"`
	DesiredSHA256 string  `json:"desired_sha256"`
	ModeMatches   bool    `json:"mode_matches"`
}

type lanePlanAuthority struct {
	Owner        string `json:"owner"`
	CompoundRole string `json:"compound_role"`
	Projection   string `json:"projection"`
}

type lanePlanCapture struct {
	Mode string `json:"mode"`
}

type lanePlanProfile struct {
	ID              string          `json:"id"`
	ProviderLane    string          `json:"provider_lane"`
	ClientProfile   string          `json:"client_profile"`
	Harness         string          `json:"harness"`
	Route           string          `json:"route"`
	RequestedModel  string          `json:"requested_model"`
	RequestedEffort string          `json:"requested_effort"`
	Transport       string          `json:"transport"`
	Capture         lanePlanCapture `json:"capture"`
}

type lanePlan struct {
	Schema            string             `json:"schema"`
	Authority         lanePlanAuthority  `json:"authority"`
	AuthorityRevision string             `json:"authority_revision"`
	Revision          string             `json:"revision"`
	Profile           lanePlanProfile    `json:"profile"`
	Changed           bool               `json:"changed"`
	Artifacts         []lanePlanArtifact `json:"artifacts"`
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

// resolveLaneArtifacts asks the lane owner what it publishes for a harness and
// returns artifact kind -> path. One query serves every consumer that needs a
// lane's real files, so no caller has to learn the profile-id conventions twice.
func resolveLaneArtifacts(harness, lane string) (map[string]string, error) {
	candidates := laneProfileCandidates(harness, lane)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: names neither a lane nor a harness", work.ErrSessionStoreRootUnknown)
	}
	for _, profile := range candidates {
		out, runErr := laneProfilePlanOutput(profile)
		if runErr != nil {
			continue
		}
		var plan lanePlan
		if json.Unmarshal(out, &plan) != nil {
			continue
		}
		artifacts := make(map[string]string, len(plan.Artifacts))
		for _, a := range plan.Artifacts {
			if p := strings.TrimSpace(a.Path); p != "" {
				artifacts[a.Kind] = p
			}
		}
		if _, ok := artifacts[laneSettingsArtifactKind]; ok {
			return artifacts, nil
		}
	}
	return nil, fmt.Errorf(
		"%w: no launch profile among [%s] published a %s artifact",
		work.ErrSessionStoreRootUnknown, strings.Join(candidates, ", "), laneSettingsArtifactKind,
	)
}

func resolveLaneSessionStore(harness, lane string) (string, error) {
	artifacts, err := resolveLaneArtifacts(harness, lane)
	if err != nil {
		return "", err
	}
	// The artifact is the settings FILE; the profile home is its parent, which is
	// what holds `projects/`.
	return filepath.Dir(artifacts[laneSettingsArtifactKind]), nil
}

func laneProfilePlanOutput(profile string) ([]byte, error) {
	bin, err := exec.LookPath("sb")
	if err != nil {
		return nil, fmt.Errorf("lane profile owner (sb) not on PATH")
	}
	out, err := exec.Command(bin, "profile", "plan", profile, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("lane profile owner could not plan profile %q", profile)
	}
	return out, nil
}

func resolveDelegateLaunchProfile(req work.DelegateLaunchProfileResolveRequest) (*work.DelegateLaunchProfileResolution, error) {
	profileID := strings.TrimSpace(req.ProfileID)
	if profileID == "" {
		return nil, fmt.Errorf("launch profile id is required")
	}
	raw, err := laneProfilePlanOutput(profileID)
	if err != nil {
		return nil, err
	}
	return normalizeDelegateLaunchProfilePlan(raw, req)
}

func normalizeDelegateLaunchProfilePlan(
	raw []byte,
	req work.DelegateLaunchProfileResolveRequest,
) (*work.DelegateLaunchProfileResolution, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("launch profile owner returned an empty plan")
	}
	if len(raw) > laneLaunchProfilePlanMaxBytes {
		return nil, fmt.Errorf("launch profile owner plan exceeds %d bytes", laneLaunchProfilePlanMaxBytes)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("launch profile owner returned invalid JSON")
	}

	var source lanePlan
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("decode launch profile owner plan: %w", err)
	}
	if source.Schema != laneLaunchProfilePlanSchema {
		return nil, fmt.Errorf("launch profile owner schema = %q, want %q", source.Schema, laneLaunchProfilePlanSchema)
	}
	if source.Authority.Owner != laneLaunchProfileOwner ||
		source.Authority.CompoundRole != laneLaunchProfileCompoundRole ||
		source.Authority.Projection != laneLaunchProfileProjection {
		return nil, fmt.Errorf("launch profile authority projection is not the Switchback consumer contract")
	}
	if !validLaneSHA256Revision(source.AuthorityRevision) {
		return nil, fmt.Errorf("launch profile authority_revision is not a sha256 revision")
	}
	if !validLaneSHA256Revision(source.Revision) {
		return nil, fmt.Errorf("launch profile revision is not a sha256 revision")
	}
	if source.Changed {
		return nil, fmt.Errorf("launch profile generated artifacts have drift")
	}
	if len(source.Artifacts) == 0 {
		return nil, fmt.Errorf("launch profile owner plan has no generated artifacts")
	}
	for _, artifact := range source.Artifacts {
		current := ""
		if artifact.CurrentSHA256 != nil {
			current = strings.TrimSpace(*artifact.CurrentSHA256)
		}
		if strings.TrimSpace(artifact.Kind) == "" ||
			!artifact.Exists ||
			artifact.Changed ||
			!artifact.ModeMatches ||
			!validLaneSHA256Revision(current) ||
			!validLaneSHA256Revision(artifact.DesiredSHA256) ||
			current != strings.TrimSpace(artifact.DesiredSHA256) {
			return nil, fmt.Errorf("launch profile artifact %q is missing or drifted", artifact.Kind)
		}
	}

	profile := source.Profile
	for name, value := range map[string]string{
		"id":               profile.ID,
		"provider_lane":    profile.ProviderLane,
		"harness":          profile.Harness,
		"route":            profile.Route,
		"requested_model":  profile.RequestedModel,
		"requested_effort": profile.RequestedEffort,
		"transport":        profile.Transport,
		"client_profile":   profile.ClientProfile,
		"capture_mode":     profile.Capture.Mode,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("launch profile %s is required", name)
		}
		if strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("launch profile %s is not canonical", name)
		}
	}
	if profile.ID != strings.TrimSpace(req.ProfileID) {
		return nil, fmt.Errorf("launch profile owner returned id %q for requested id %q", profile.ID, req.ProfileID)
	}
	if !validLaneOpaqueID(profile.ID) ||
		!validLaneOpaqueID(profile.ProviderLane) ||
		!validLaneOpaqueID(profile.ClientProfile) {
		return nil, fmt.Errorf("launch profile or client_profile identity is not opaque")
	}
	if !oneOfLaneValue(profile.Transport, "gateway", "tap", "headroom") {
		return nil, fmt.Errorf("launch profile transport %q is not a supported Switchback transport", profile.Transport)
	}
	if profile.Capture.Mode != laneLaunchProfileCaptureMode {
		return nil, fmt.Errorf(
			"launch profile capture_mode = %q, want %q",
			profile.Capture.Mode,
			laneLaunchProfileCaptureMode,
		)
	}

	sourceSum := sha256.Sum256(raw)
	canonical := work.DelegateLaunchProfilePlan{
		SchemaVersion:     delegateLaunchProfilePlanSchema,
		SourceSchema:      source.Schema,
		SourceDigest:      fmt.Sprintf("sha256:%x", sourceSum),
		AuthorityRevision: source.AuthorityRevision,
		Revision:          source.Revision,
		ID:                profile.ID,
		Harness:           profile.Harness,
		ProviderLane:      profile.ProviderLane,
		Route:             profile.Route,
		RequestedModel:    profile.RequestedModel,
		RequestedEffort:   profile.RequestedEffort,
		TransportID:       laneLaunchProfileTransportID,
		OwnerTransport:    profile.Transport,
		ClientProfileID:   profile.ClientProfile,
		CaptureMode:       profile.Capture.Mode,
	}
	snapshot, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode normalized launch profile plan: %w", err)
	}

	disposition := work.DelegateLaunchProfileIncompatible
	if normalizeLaneHarness(req.SelectedHarness) == normalizeLaneHarness(profile.Harness) &&
		strings.TrimSpace(req.SelectedModelID) == profile.RequestedModel &&
		strings.TrimSpace(req.SelectedProviderRoute) == profile.Route &&
		strings.TrimSpace(req.SelectedTransportID) == laneLaunchProfileTransportID &&
		strings.TrimSpace(req.RequestedNativeEffort) == profile.RequestedEffort {
		disposition = work.DelegateLaunchProfileCompatible
	}
	return &work.DelegateLaunchProfileResolution{
		Snapshot: snapshot,
		Compatibility: work.DelegateLaunchProfileCompatibility{
			Disposition:           disposition,
			SelectedHarness:       req.SelectedHarness,
			SelectedLaneID:        req.SelectedLaneID,
			SelectedModelID:       req.SelectedModelID,
			SelectedProviderRoute: req.SelectedProviderRoute,
			SelectedTransportID:   req.SelectedTransportID,
			RequestedNativeEffort: req.RequestedNativeEffort,
			PlanID:                canonical.ID,
			ClientProfileID:       canonical.ClientProfileID,
			Revision:              canonical.Revision,
		},
	}, nil
}

func normalizeLaneHarness(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
}

func validLaneSHA256Revision(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	return validLaneSHA256Digest(strings.TrimPrefix(value, "sha256:"))
}

func validLaneSHA256Digest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'))
	}) == -1
}

func validLaneOpaqueID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' ||
			r == '_' ||
			r == '.')
	}) == -1
}

func oneOfLaneValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func init() {
	work.DelegateSessionStoreResolver = resolveLaneSessionStore
	work.DelegateLaunchProfileResolver = resolveDelegateLaunchProfile
}
