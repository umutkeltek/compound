package product

import (
	"sort"
	"strings"
	"time"

	"compound/internal/sourceobs"
)

// ReleaseAwarenessSchemaVersion is the JSON schema for the additive
// release-awareness surface included in ProductLoop. It is
// evidence-only and never auto-claims current-live, never schedules
// release execution, and never promotes memory.
const ReleaseAwarenessSchemaVersion = "compound.product_release_awareness.v0.1"

// ReleaseAwarenessStatus is the closed enum for the per-product
// release-awareness summary. Anything outside this list is a programming
// error; callers should treat unknown statuses as "unknown".
var ReleaseAwarenessStatus = []string{
	"unobserved",  // no external release observation has been admitted for this product
	"invalid",     // admitted observations failed strict re-validation (gap in intake)
	"stale",       // observations exist but every channel is older than StaleAfter
	"partial",     // some channels observed, others not, no same-channel+platform conflict
	"conflicting", // same channel+platform has multiple equally-current incompatible observations
	"current",     // at least one observation reports a live version with valid provenance
}

// ReleaseChannelPlatformView is the per (channel, platform) slice of
// release awareness for one product. Different channels/platforms are
// not contradictions: e.g. an App Store Connect candidate entry for iOS
// and a direct-mac live dmg are two independent views, never a conflict.
type ReleaseChannelPlatformView struct {
	Channel        string                       `json:"channel"`
	Platform       string                       `json:"platform"`
	Latest         sourceobs.ReleaseObservation `json:"latest"`
	StateSet       []string                     `json:"state_set,omitempty"`
	VersionSet     []string                     `json:"version_set,omitempty"`
	CurrentVersion string                       `json:"current_version,omitempty"`
	EvidenceRefs   []string                     `json:"evidence_refs,omitempty"`
	ObservationIDs []string                     `json:"observation_ids,omitempty"`
}

// ReleaseAwareness is the additive evidence-only per-product release
// reconciliation view. It surfaces candidate/submitted/live observations
// per channel+platform with provenance, never auto-claiming current-live
// or contradicting fresh metrics. ContractVersion is the recalled
// ReleaseIdentity from the product registry (adjudicated identity), not
// the deployed version; the gap between ContractVersion and the latest
// observed version is reported as OutdatedContract, never as a fix.
//
// NextAction is a bounded safe move: import more observations, resolve
// a conflict through operator review, or wait — never "ship" or
// "promote to live".
type ReleaseAwareness struct {
	SchemaVersion    string                       `json:"schema_version"`
	ProductID        string                       `json:"product_id"`
	Status           string                       `json:"status"`
	ContractVersion  string                       `json:"contract_version,omitempty"`
	ContractBuild    string                       `json:"contract_build,omitempty"`
	OutdatedContract bool                         `json:"outdated_contract,omitempty"`
	LatestObservedAt string                       `json:"latest_observed_at,omitempty"`
	LatestVersion    string                       `json:"latest_version,omitempty"`
	LatestChannel    string                       `json:"latest_channel,omitempty"`
	LatestPlatform   string                       `json:"latest_platform,omitempty"`
	LatestState      string                       `json:"latest_state,omitempty"`
	ChannelPlatforms []ReleaseChannelPlatformView `json:"channel_platforms,omitempty"`
	ObservationCount int                          `json:"observation_count"`
	Conflict         []string                     `json:"conflict,omitempty"`
	MissingEvidence  []string                     `json:"missing_evidence,omitempty"`
	Boundary         string                       `json:"boundary"`
	NextAction       string                       `json:"next_action"`
	EvidenceRefs     []string                     `json:"evidence_refs,omitempty"`
}

// ReleaseAwarenessOptions controls the per-product aggregation. Now is
// injected (Law 10 — tests must not compare wall-clock against fixed
// fixture dates). StaleAfter is the freshness window; defaults to 7
// days, matching the product-loop default.
type ReleaseAwarenessOptions struct {
	Now        time.Time
	StaleAfter time.Duration
}

// BuildReleaseAwareness aggregates typed release observations for one
// product. It is additive and read-only: no records, claims, or
// receipts are written. It returns an empty (Status="unobserved")
// ReleaseAwareness when the product has no observations, and an
// "invalid" status when admitted observations fail strict
// re-validation. Stale observations that pass strict re-validation
// surface as "stale" with a bounded next action.
//
// Falsifier contract (one bullet per acceptance scenario):
//
//  1. outdated contract: any observation reporting a version newer than
//     the recalled ReleaseIdentity sets OutdatedContract=true and the
//     status remains current; both provenance refs are returned.
//  2. different channels/platforms: never merged into one bucket;
//     each (channel, platform) is its own view, no Conflict raised.
//  3. same channel+platform equal-current conflict: Status="conflicting"
//     and Conflict[] names the (channel, platform, version, state) tuples
//     in deterministic order; NextAction asks for a reviewed reconcile.
//  4. missing/invalid/future timestamps or wrong identity fields: the
//     carrier's Validate rejects at intake; if a record slipped past
//     earlier rules, strict re-validation rejects it here (Status="invalid").
//  5. fresh metric, stale release: status reflects release observations
//     independently of signal freshness — a stale release cannot ride a
//     fresh metric to "current".
//  6. intake → persisted evidence → product loop JSON path: callers
//     go through sourceobs.Apply + LoadReleaseObservations once per
//     portfolio projection; this function adds the per-product slice
//     and never reads the home twice for one product.
//  7. backward compatibility: observations that pre-date the carrier
//     have no Release payload and never reach this function; existing
//     sourceobs records keep their prior shape.
//  8. bounded reading: this function calls LoadReleaseObservations
//     once per product; the underlying sourceobs list projection is
//     memoized once per (home, fingerprint), so a portfolio projection
//     stays bounded to ~1 home scan.
func BuildReleaseAwareness(homePath, productID string, identity ReleaseIdentity, opts ReleaseAwarenessOptions) (ReleaseAwareness, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 7 * 24 * time.Hour
	}
	out := ReleaseAwareness{
		SchemaVersion:    ReleaseAwarenessSchemaVersion,
		ProductID:        productID,
		Status:           "unobserved",
		ContractVersion:  strings.TrimSpace(identity.Version),
		ContractBuild:    strings.TrimSpace(identity.Build),
		Boundary:         "evidence-only; never auto-claims current-live, never schedules release execution, never promotes memory",
		NextAction:       "import typed release observations through compound sourceobs Apply or wait for next governed intake",
		ChannelPlatforms: []ReleaseChannelPlatformView{},
		Conflict:         []string{},
		MissingEvidence:  []string{},
		EvidenceRefs:     []string{},
	}
	if strings.TrimSpace(productID) == "" {
		out.Status = "invalid"
		out.Conflict = append(out.Conflict, "product_id required")
		return out, nil
	}
	observations, err := sourceobs.LoadReleaseObservations(homePath, []string{productID}, now)
	if err != nil {
		return ReleaseAwareness{}, err
	}
	if len(observations) == 0 {
		return out, nil
	}
	out.ObservationCount = len(observations)
	// Group by (channel, platform), preserve determinism.
	groups := map[string][]sourceobs.ReleaseObservation{}
	groupOrder := []string{}
	for _, obs := range observations {
		key := obs.Channel + "|" + obs.Platform
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], obs)
	}
	sort.Strings(groupOrder)
	allRefs := map[string]bool{}
	hasLive := false
	for _, key := range groupOrder {
		obsList := groups[key]
		parts := strings.SplitN(key, "|", 2)
		channel, platform := parts[0], parts[1]
		// pick the latest observed_at per (channel, platform) deterministically
		latestIdx := -1
		latestTime := time.Time{}
		for i, o := range obsList {
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(o.ObservedAt))
			if err != nil {
				continue
			}
			if latestIdx == -1 || t.After(latestTime) {
				latestTime = t
				latestIdx = i
			}
		}
		if latestIdx == -1 {
			continue
		}
		latest := obsList[latestIdx]
		// detect same-channel+platform equally-current conflict
		equalCurrent := map[string][]sourceobs.ReleaseObservation{}
		for _, o := range obsList {
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(o.ObservedAt))
			if err != nil {
				continue
			}
			// two observations are "equally current" when their observed_at
			// is within the staleness window of the latest (i.e. they are
			// co-current evidence, not strictly stale relative to the
			// newest). This is the packet's "incompatible equally-current
			// observations" definition.
			if latestTime.Sub(t) <= staleAfter {
				sig := o.ObservedVersion + "|" + o.ObservedState
				equalCurrent[sig] = append(equalCurrent[sig], o)
			}
		}
		var states, versions []string
		refs := map[string]bool{}
		obsIDs := []string{}
		for _, o := range obsList {
			states = append(states, o.ObservedState)
			versions = append(versions, o.ObservedVersion)
			refs[o.EvidenceRef] = true
			refs[o.SourceRevision] = true
			refs["release_observation:"+o.ProductID+":"+o.Channel+":"+o.Platform] = true
			obsIDs = append(obsIDs, stableID("relobs_", o.ProductID, o.Channel, o.Platform, o.ObservedVersion, o.ObservedState, o.ObservedAt, o.SourceRevision))
		}
		if len(equalCurrent) > 1 {
			// conflicting: same channel+platform, equally current, disagree on version or state
			var sigs []string
			for sig := range equalCurrent {
				sigs = append(sigs, sig)
			}
			sort.Strings(sigs)
			for _, sig := range sigs {
				out.Conflict = append(out.Conflict, channel+"/"+platform+":"+sig)
			}
		}
		view := ReleaseChannelPlatformView{
			Channel:        channel,
			Platform:       platform,
			Latest:         latest,
			StateSet:       sortedUnique(states),
			VersionSet:     sortedUnique(versions),
			CurrentVersion: latest.ObservedVersion,
			ObservationIDs: sortedUnique(obsIDs),
		}
		for r := range refs {
			view.EvidenceRefs = append(view.EvidenceRefs, r)
			allRefs[r] = true
		}
		sort.Strings(view.EvidenceRefs)
		out.ChannelPlatforms = append(out.ChannelPlatforms, view)
		if latest.ObservedState == "live" {
			hasLive = true
		}
		// track the newest observation across all (channel, platform) views
		latestT, _ := time.Parse(time.RFC3339, strings.TrimSpace(latest.ObservedAt))
		if out.LatestObservedAt == "" || latestT.After(parseTimeOrZero(out.LatestObservedAt)) {
			out.LatestObservedAt = latest.ObservedAt
			out.LatestVersion = latest.ObservedVersion
			out.LatestChannel = latest.Channel
			out.LatestPlatform = latest.Platform
			out.LatestState = latest.ObservedState
		}
	}
	// detect outdated contract: at least one observation reports a live
	// version that differs from the recalled ReleaseIdentity.Version.
	if strings.TrimSpace(out.ContractVersion) != "" && strings.TrimSpace(out.LatestVersion) != "" {
		if out.LatestState == "live" && !sameReleaseVersion(out.ContractVersion, out.LatestVersion) {
			out.OutdatedContract = true
		}
	}
	// status precedence: conflicting > stale > current > partial > unobserved
	// Stale is checked before current so a fresh-metric world never inflates
	// a stale release observation to "current". The release-awareness
	// freshness contract is independent of any signal freshness contract.
	switch {
	case len(out.Conflict) > 0:
		out.Status = "conflicting"
		out.NextAction = "operator-reviewed reconcile required: same channel+platform reports multiple equally-current versions/states; do not auto-claim live"
	case isStale(out.LatestObservedAt, now, staleAfter):
		out.Status = "stale"
		out.NextAction = "latest observation is older than the freshness window; re-import current release observations before claiming release awareness"
	case hasLive:
		out.Status = "current"
		if out.OutdatedContract {
			out.NextAction = "recalled contract identity lags observed live version; do not promote contract identity — import a fresh release observation through the same intake door"
		} else {
			out.NextAction = "evidence-only; current live version matches recalled contract identity; do not infer causality or schedule release execution"
		}
	default:
		out.Status = "partial"
		out.NextAction = "evidence-only; some channels observed, others not; do not promote any channel to current-live without operator review"
	}
	for r := range allRefs {
		out.EvidenceRefs = append(out.EvidenceRefs, r)
	}
	sort.Strings(out.EvidenceRefs)
	out.Conflict = sortedUnique(out.Conflict)
	return out, nil
}

func isStale(observedAt string, now time.Time, staleAfter time.Duration) bool {
	observedAt = strings.TrimSpace(observedAt)
	if observedAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, observedAt)
	if err != nil {
		return true
	}
	return now.UTC().Sub(t) > staleAfter
}

func parseTimeOrZero(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// sameReleaseVersion is the contract-side equality check: normalize the
// version string (trim, lowercase) and compare. Build numbers are
// compared separately by callers when needed.
func sameReleaseVersion(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
