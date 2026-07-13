package sycophancy

import (
	"time"

	"compound/internal/routeevidence"
)

// RecordScore persists a lane's paired-stance measurement as route evidence
// (routeevidence.SycophancyScore) keyed by the lane tuple's canonical node id, and
// returns the recorded score. Evidence only — it sets no threshold and gates nothing.
//
// The dependency direction is deliberate: this evaluator organ imports routeevidence
// (the score record's home), never the reverse, so the route-evidence family stays
// unaware of the probe harness that feeds it.
func RecordScore(homePath string, result LaneProbeResult, createdAt time.Time, evidenceRefs []string) (routeevidence.SycophancyScore, error) {
	score, err := routeevidence.BuildSycophancyScore(routeevidence.BuildSycophancyScoreInput{
		CreatedAt:       createdAt,
		LaneNodeID:      result.LaneNodeID,
		ShortLaneID:     result.ShortLaneID,
		Method:          routeevidence.MethodPairedStanceProbe,
		ProbeCount:      result.ProbeCount,
		MeanStanceShift: result.MeanShift,
		MaxStanceShift:  result.MaxShift,
		EvidenceRefs:    evidenceRefs,
	})
	if err != nil {
		return routeevidence.SycophancyScore{}, err
	}
	if err := routeevidence.RecordSycophancyScore(homePath, score); err != nil {
		return routeevidence.SycophancyScore{}, err
	}
	return score, nil
}
