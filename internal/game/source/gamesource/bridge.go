package gamesource

import (
	"context"
	"fmt"
	"time"

	"compound/internal/candidates"
	"compound/internal/sourceobs"
)

type ApplyResult struct {
	AdapterID    string
	SourceSystem string
	Revision     string
	Written      int
	Failed       int
	CandidateIDs []string
	NoPromoteIDs []string
}

func Apply(ctx context.Context, homePath string, res FetchResult, now time.Time) (ApplyResult, error) {
	if res.AdapterID == "" {
		return ApplyResult{}, fmt.Errorf("gamesource.Apply: AdapterID required")
	}
	if res.SourceSys == "" {
		return ApplyResult{}, fmt.Errorf("gamesource.Apply: SourceSys required")
	}
	if homePath == "" {
		return ApplyResult{}, fmt.Errorf("gamesource.Apply: homePath required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	out := ApplyResult{
		AdapterID:    res.AdapterID,
		SourceSystem: res.SourceSys,
		Revision:     res.RawSHA256,
	}

	for i, it := range res.Items {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		obsRev := uniqueObservationRevision(res.RawSHA256, now, i)
		obs, err := BuildObservation(res.AdapterID, res.SourceSys, obsRev, it, now)
		if err != nil {
			out.Failed++
			return out, err
		}
		report, err := sourceobs.Apply(homePath, obs, now)
		if err != nil {
			out.Failed++
			return out, fmt.Errorf("gamesource.Apply: %s/%s: %w", it.Kind, it.SourceKey, err)
		}
		out.Written++
		out.CandidateIDs = append(out.CandidateIDs, report.CandidateIDs...)
		out.NoPromoteIDs = append(out.NoPromoteIDs, report.NoPromoteIDs...)
	}
	return out, nil
}

func BuildObservation(adapterID, sourceSys, revision string, it RawObservation, now time.Time) (sourceobs.SourceObservation, error) {
	if adapterID == "" {
		return sourceobs.SourceObservation{}, fmt.Errorf("BuildObservation: adapterID required")
	}
	if sourceSys == "" {
		return sourceobs.SourceObservation{}, fmt.Errorf("BuildObservation: sourceSys required")
	}
	if it.Kind == "" {
		return sourceobs.SourceObservation{}, fmt.Errorf("BuildObservation: Kind required")
	}
	if it.SourceKey == "" {
		return sourceobs.SourceObservation{}, fmt.Errorf("BuildObservation: SourceKey required")
	}
	if len(it.Evidence) == 0 {
		return sourceobs.SourceObservation{}, fmt.Errorf("BuildObservation: Evidence required (URL + revision only)")
	}

	path := it.Evidence[0]
	id := "srcobs_" + gameIDFor(adapterID, it.SourceKey, it.Kind)

	return sourceobs.SourceObservation{
		SchemaVersion: sourceobs.SchemaVersion,
		ID:            id,
		AdapterID:     adapterID,
		SourceSystem:  sourceSys,
		ObservedAt:    rfc3339UTC(now),
		SourceRef: sourceobs.SourceRef{
			Path:        path,
			Kind:        "api_view",
			Owner:       adapterID,
			SourceClass: sourceSys,
			Revision:    revision,
		},
		MapsTo: []sourceobs.MapTarget{{
			ObjectFamily:   "game",
			CandidateKind:  string(candidates.CapabilityObs),
			EntityHint:     entityHint(it.Kind, it.SourceKey, it.Subject),
			PromoteAllowed: false,
		}},
		EvidenceRefs: append([]string(nil), it.Evidence...),
	}, nil
}

func entityHint(kind, sourceKey, label string) string {
	return "game:" + kind + ":" + sourceKey + "::" + label
}

func rfc3339UTC(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func gameIDFor(adapterID, sourceKey, kind string) string {
	parts := []string{
		trimSpace(adapterID),
		trimSpace(sourceKey),
		trimSpace(kind),
	}
	return sha256Hex12(joinWithUS(parts))
}
