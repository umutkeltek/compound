package sourceobs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
)

// ReleaseObservationSchemaVersion is the schema for the typed external
// release-state payload carried by a SourceObservation. The payload is
// additive and evidence-only; it never promotes to release planning, never
// mutates product memory, and never authorizes release execution.
const ReleaseObservationSchemaVersion = "compound.source_release_observation.v0.1"

// ReleaseObservationStates is the closed enum for the observed release
// state. Anything outside this list fails Validate and is rejected by
// intake, so projection cannot promote ambiguity as a live claim.
var ReleaseObservationStates = []string{
	"candidate",
	"submitted",
	"preparing",
	"live",
	"unknown",
}

// ReleaseObservation is the minimal typed payload for one external
// release-state observation. It is additive: a SourceObservation without
// this field preserves its prior behavior. Recalled contract identity
// (from Product.ReleaseIdentity) and these observations are distinct
// sources; the carrier keeps both with their own provenance.
//
// Different channels/platforms are not contradictions: e.g. an App Store
// Connect candidate entry for iOS and a direct-mac live dmg are two
// independent views. Same channel+platform with equally-current
// incompatible observations remains unresolved (Status="conflicting"),
// never silently overwritten.
type ReleaseObservation struct {
	ProductID       string `json:"product_id" yaml:"product_id"`
	Channel         string `json:"channel" yaml:"channel"`
	Platform        string `json:"platform" yaml:"platform"`
	ObservedVersion string `json:"observed_version" yaml:"observed_version"`
	ObservedBuild   string `json:"observed_build,omitempty" yaml:"observed_build,omitempty"`
	ObservedState   string `json:"observed_state" yaml:"observed_state"`
	ObservedAt      string `json:"observed_at" yaml:"observed_at"`
	SourceRevision  string `json:"source_revision" yaml:"source_revision"`
	EvidenceRef     string `json:"evidence_ref" yaml:"evidence_ref"`
}

// releaseObservationObjectFamily is the synthetic ObjectFamily marker the
// aggregation uses to filter a single source-observation list scan. It is
// not a candidate kind; it is the carrier's namespace inside MapsTo.
const releaseObservationObjectFamily = "release_observation"

// releaseObservationCandidateKind is the candidate kind the carrier
// writes through the existing intake door. route_observation fits
// because release-state observation is evidence about an external
// route to a live surface (vs. promoted memory).
const releaseObservationCandidateKind = candidates.RouteObservation

// ValidateReleaseObservation is the strict-contract check for the typed
// payload. It rejects missing required fields, malformed timestamps,
// future timestamps (clock injection is the falsifier), and states
// outside the closed enum. It does NOT decide whether the observation is
// current-live; that is the aggregator's job.
func ValidateReleaseObservation(obs ReleaseObservation, now time.Time) error {
	if strings.TrimSpace(obs.ProductID) == "" {
		return fmt.Errorf("release observation product_id required")
	}
	if strings.TrimSpace(obs.Channel) == "" {
		return fmt.Errorf("release observation %s channel required", obs.ProductID)
	}
	if strings.TrimSpace(obs.Platform) == "" {
		return fmt.Errorf("release observation %s/%s platform required", obs.ProductID, obs.Channel)
	}
	if strings.TrimSpace(obs.ObservedVersion) == "" {
		return fmt.Errorf("release observation %s/%s/%s observed_version required", obs.ProductID, obs.Channel, obs.Platform)
	}
	if !oneOfReleaseState(obs.ObservedState) {
		return fmt.Errorf("release observation %s/%s/%s observed_state %q not in %v", obs.ProductID, obs.Channel, obs.Platform, obs.ObservedState, ReleaseObservationStates)
	}
	if strings.TrimSpace(obs.ObservedAt) == "" {
		return fmt.Errorf("release observation %s/%s/%s observed_at required", obs.ProductID, obs.Channel, obs.Platform)
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(obs.ObservedAt))
	if err != nil {
		return fmt.Errorf("release observation %s/%s/%s observed_at %q invalid RFC3339: %w", obs.ProductID, obs.Channel, obs.Platform, obs.ObservedAt, err)
	}
	if now.IsZero() {
		now = time.Now()
	}
	if parsed.After(now.UTC().Add(time.Minute)) {
		// tolerate a 1-minute clock-skew margin; anything beyond is rejected.
		return fmt.Errorf("release observation %s/%s/%s observed_at %q is in the future (now=%s)", obs.ProductID, obs.Channel, obs.Platform, obs.ObservedAt, now.UTC().Format(time.RFC3339))
	}
	if strings.TrimSpace(obs.SourceRevision) == "" {
		return fmt.Errorf("release observation %s/%s/%s source_revision required", obs.ProductID, obs.Channel, obs.Platform)
	}
	if strings.TrimSpace(obs.EvidenceRef) == "" {
		return fmt.Errorf("release observation %s/%s/%s evidence_ref required", obs.ProductID, obs.Channel, obs.Platform)
	}
	return nil
}

// FromReleaseObservation wraps a typed release observation into the
// generic SourceObservation contract so it can enter through the
// existing sourceobs evidence door. The SourceRef path is a deterministic
// synthetic key. PromoteAllowed is false: this is no-promote evidence,
// never memory authority. All structured fields are re-encoded into
// EvidenceRefs so ListSourceObservations (which is bounded to a single
// scan) can reconstruct the typed payload without re-reading JSON files.
func FromReleaseObservation(obs ReleaseObservation, now time.Time) (SourceObservation, error) {
	if err := ValidateReleaseObservation(obs, now); err != nil {
		return SourceObservation{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	path := releaseObservationPath(obs)
	evidenceRefs := []string{
		"release_observation:" + obs.ProductID + ":" + obs.Channel + ":" + obs.Platform,
		"release_observation:evidence:" + obs.EvidenceRef,
		"release_observation:revision:" + obs.SourceRevision,
		"release_observation:state:" + obs.ObservedState,
		"release_observation:observed_at:" + obs.ObservedAt,
		"release_observation:version:" + strings.TrimSpace(obs.ObservedVersion),
	}
	if build := strings.TrimSpace(obs.ObservedBuild); build != "" {
		evidenceRefs = append(evidenceRefs, "release_observation:build:"+build)
	}
	return SourceObservation{
		SchemaVersion: SchemaVersion,
		ID:            idFor("srcobs_", "release_observation", obs.ProductID, obs.Channel, obs.Platform, obs.ObservedVersion, obs.ObservedState, obs.ObservedAt, obs.SourceRevision),
		AdapterID:     "compound.release-observation",
		SourceSystem:  "external",
		ObservedAt:    obs.ObservedAt,
		SourceRef: SourceRef{
			Path:        path,
			Kind:        "release_observation",
			Owner:       firstNonEmpty(obs.ProductID, "release_observation"),
			SourceClass: "release_observation",
			Revision:    strings.TrimSpace(obs.SourceRevision),
		},
		GeneratedView: GeneratedView{
			ViewKind:    "release_observation",
			Consumer:    "compound product release-awareness",
			Generator:   "compound sourceobs release-observation adapter",
			Marker:      ReleaseObservationSchemaVersion,
			Freshness:   "observed",
			DriftReason: fmt.Sprintf("release_observation:version=%s state=%s observed_at=%s", obs.ObservedVersion, obs.ObservedState, obs.ObservedAt),
		},
		MapsTo: []MapTarget{
			{
				ObjectFamily:   releaseObservationObjectFamily,
				CandidateKind:  string(releaseObservationCandidateKind),
				EntityHint:     obs.ProductID,
				PromoteAllowed: false,
			},
		},
		EvidenceRefs: evidenceRefs,
		Release:      &obs,
	}, nil
}

// LoadReleaseObservations reads the source-observation list projection
// ONCE and returns the typed release observations, bounded to the given
// product IDs (empty list = all). It is the bounded-reading path the
// per-product projection reuses for N products so a portfolio
// projection is ~1 scan, not N. The home is read-only: no records,
// claims, or receipts are written by this projection.
//
// Records without a typed Release payload are filtered out (existing
// non-release sourceobs records keep their prior shape and never reach
// release aggregation).
func LoadReleaseObservations(homePath string, productIDs []string, now time.Time) ([]ReleaseObservation, error) {
	if now.IsZero() {
		now = time.Now()
	}
	list, err := ListSourceObservations(homePath, now)
	if err != nil {
		return nil, err
	}
	filter := stringSet(productIDs)
	want := filter == nil
	var out []ReleaseObservation
	seen := map[string]bool{}
	for _, item := range list.Items {
		if !item.IsReleaseObservation() {
			continue
		}
		obs, ok := decodeReleaseObservationFromItem(item)
		if !ok {
			continue
		}
		if !want && !filter[obs.ProductID] {
			continue
		}
		if err := ValidateReleaseObservation(obs, now); err != nil {
			// strict re-validation: a record admitted before stricter rules
			// cannot leak into release aggregation. Skip rather than claim.
			continue
		}
		key := obs.ProductID + "|" + obs.Channel + "|" + obs.Platform + "|" + obs.ObservedVersion + "|" + obs.ObservedState + "|" + obs.ObservedAt + "|" + obs.SourceRevision
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, obs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProductID != out[j].ProductID {
			return out[i].ProductID < out[j].ProductID
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return out[i].ObservedAt < out[j].ObservedAt
	})
	return out, nil
}

// releaseObservationPath is the deterministic synthetic key the carrier
// uses as the SourceRef path. It survives re-import without false
// revision drift; two observations that disagree on any identity field
// produce different paths.
func releaseObservationPath(obs ReleaseObservation) string {
	return "compound://release_observation/" + strings.TrimSpace(obs.ProductID) +
		"/" + strings.TrimSpace(obs.Channel) +
		"/" + strings.TrimSpace(obs.Platform) +
		"/" + strings.TrimSpace(obs.ObservedVersion) +
		"/" + strings.TrimSpace(obs.ObservedState) +
		"/" + strings.TrimSpace(obs.ObservedAt) +
		"#" + strings.TrimSpace(obs.SourceRevision)
}

// decodeReleaseObservationFromItem reconstructs the typed payload from a
// SourceObservationListItem. All structured fields are in EvidenceRefs
// (added in FromReleaseObservation) and the carrier's SourceRef path;
// malformed entries (missing evidence_ref / source_revision / state /
// observed_at / version) are skipped, never silently promoted.
func decodeReleaseObservationFromItem(item SourceObservationListItem) (ReleaseObservation, bool) {
	obs := ReleaseObservation{}
	for _, ref := range item.EvidenceRefs {
		ref = strings.TrimSpace(ref)
		switch {
		case strings.HasPrefix(ref, "release_observation:evidence:"):
			obs.EvidenceRef = strings.TrimPrefix(ref, "release_observation:evidence:")
		case strings.HasPrefix(ref, "release_observation:revision:"):
			obs.SourceRevision = strings.TrimPrefix(ref, "release_observation:revision:")
		case strings.HasPrefix(ref, "release_observation:state:"):
			obs.ObservedState = strings.TrimPrefix(ref, "release_observation:state:")
		case strings.HasPrefix(ref, "release_observation:observed_at:"):
			obs.ObservedAt = strings.TrimPrefix(ref, "release_observation:observed_at:")
		case strings.HasPrefix(ref, "release_observation:version:"):
			obs.ObservedVersion = strings.TrimPrefix(ref, "release_observation:version:")
		case strings.HasPrefix(ref, "release_observation:build:"):
			obs.ObservedBuild = strings.TrimPrefix(ref, "release_observation:build:")
		case strings.HasPrefix(ref, "release_observation:"):
			// anchor: "release_observation:<productID>:<channel>:<platform>"
			parts := strings.SplitN(strings.TrimPrefix(ref, "release_observation:"), ":", 3)
			if len(parts) == 3 {
				obs.ProductID = strings.TrimSpace(parts[0])
				obs.Channel = strings.TrimSpace(parts[1])
				obs.Platform = strings.TrimSpace(parts[2])
			}
		}
	}
	if obs.ProductID == "" || obs.Channel == "" || obs.Platform == "" {
		return obs, false
	}
	if obs.ObservedVersion == "" || obs.ObservedState == "" || obs.ObservedAt == "" {
		return obs, false
	}
	if obs.EvidenceRef == "" || obs.SourceRevision == "" {
		return obs, false
	}
	return obs, true
}

// IsReleaseObservation reports whether a SourceObservationListItem came
// from the release-observation carrier. The carrier always writes its
// SourceRef path under "compound://release_observation/", so a single
// prefix check is sufficient.
func (i SourceObservationListItem) IsReleaseObservation() bool {
	return strings.HasPrefix(strings.TrimSpace(i.SourceRef), "compound://release_observation/")
}

func stringSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func oneOfReleaseState(value string) bool {
	for _, a := range ReleaseObservationStates {
		if a == value {
			return true
		}
	}
	return false
}
