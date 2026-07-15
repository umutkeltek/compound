package sourceobs

import (
	"fmt"
	"time"

	"compound/internal/candidates"
)

// CanonDriftReport is what ObserveCanonDrift returns: the set of canon files whose live bytes drifted
// from the stored baseline, the capability-graph node refs those files carry, the no-promote
// candidate/decision ids parked for the drift, and the manifest path re-baselined.
type CanonDriftReport struct {
	ChangedRefs  []string `json:"changed_refs"`
	NodeRefs     []string `json:"node_refs"`
	CandidateIDs []string `json:"candidate_ids"`
	NoPromoteIDs []string `json:"no_promote_ids,omitempty"`
	ManifestPath string   `json:"manifest_path"`
}

// ObserveCanonDrift is the harness-canon Importer: it loads the stored source-manifest baseline for
// a family, re-hashes each item's live file, and for every changed file parks exactly one
// CapabilityObs candidate (PromoteAllowed=false => auto no-promote) via the existing candidate
// writers. It NEVER mints a Transition — that is the ObjectFamily's job (capgraph.RecordCanonEvolution).
// After observing, it re-stores the baseline so a repeat observation of the same physical drift is
// idempotent (the drift has been consumed into candidates). An unchanged family mints nothing and
// re-stores nothing.
func ObserveCanonDrift(homePath, family string, now time.Time) (CanonDriftReport, error) {
	if now.IsZero() {
		now = time.Now()
	}
	manifest, err := LoadSourceManifest(homePath, family)
	if err != nil {
		return CanonDriftReport{}, fmt.Errorf("canon-observe: load manifest %q: %w", family, err)
	}
	report := CanonDriftReport{ManifestPath: SourceManifestPath(homePath, family)}
	seenNode := map[string]bool{}
	changed := false
	for i := range manifest.Items {
		item := &manifest.Items[i]
		liveHash, herr := hashFile(item.SourceRef)
		if herr != nil {
			// An unreadable canon file is not counted as drift in v1 (a transient read error must
			// not fabricate a candidate); the baseline stays and doctor surfaces staleness instead.
			continue
		}
		if liveHash == item.FileHash {
			continue
		}
		obs := canonDriftObservation(family, *item, item.FileHash, liveHash, now)
		applied, err := Apply(homePath, obs, now)
		if err != nil {
			return report, fmt.Errorf("canon-observe: park drift for %s: %w", item.SourceRef, err)
		}
		report.CandidateIDs = append(report.CandidateIDs, applied.CandidateIDs...)
		report.NoPromoteIDs = append(report.NoPromoteIDs, applied.NoPromoteIDs...)
		report.ChangedRefs = append(report.ChangedRefs, item.SourceRef)
		for _, ref := range item.CompoundEntityRefs {
			if ref == "" || seenNode[ref] {
				continue
			}
			seenNode[ref] = true
			report.NodeRefs = append(report.NodeRefs, ref)
		}
		item.FileHash = liveHash
		item.SnapshotTime = now.UTC().Format(time.RFC3339)
		item.EvidenceRefs = []string{"source:" + item.SourceRef, liveHash}
		changed = true
	}
	if changed {
		if _, err := RecordSourceManifest(homePath, manifest); err != nil {
			return report, fmt.Errorf("canon-observe: re-store baseline: %w", err)
		}
	}
	return report, nil
}

// canonDriftObservation shapes one drifted canon file as the generic SourceObservation contract so
// the standard candidate/no-promote writers apply. The mapped node is capability/no-promote evidence
// only; a canon file never promotes itself into authority.
func canonDriftObservation(family string, item SourceManifestItem, priorHash, liveHash string, now time.Time) SourceObservation {
	nodeRef := ""
	if len(item.CompoundEntityRefs) > 0 {
		nodeRef = item.CompoundEntityRefs[0]
	}
	entityHint := firstNonEmpty(nodeRef, item.SourceRef)
	return SourceObservation{
		SchemaVersion: SchemaVersion,
		ID:            idFor("srcobs_", "harness_canon", family, item.SourceRef, liveHash),
		AdapterID:     "compound.harness-canon",
		SourceSystem:  "compound",
		ObservedAt:    rfc3339(now),
		SourceRef: SourceRef{
			Path:        item.SourceRef,
			Kind:        firstNonEmpty(item.SourceKind, "harness-canon-file"),
			Owner:       firstNonEmpty(nodeRef, item.SourceRef),
			SourceClass: "harness-canon-file",
			Revision:    liveHash,
		},
		GeneratedView: GeneratedView{
			ViewKind:    "harness_canon_file",
			Consumer:    "compound source canon-observe",
			Generator:   "compound.harness-canon",
			Marker:      SourceManifestSchemaVersion,
			Freshness:   "drift",
			DriftReason: fmt.Sprintf("file_hash %s -> %s", priorHash, liveHash),
		},
		MapsTo: []MapTarget{
			{
				ObjectFamily:   "capability",
				CandidateKind:  string(candidates.CapabilityObs),
				EntityHint:     entityHint,
				PromoteAllowed: false,
			},
		},
		EvidenceRefs: []string{"source:" + item.SourceRef + "#" + liveHash, priorHash},
	}
}
