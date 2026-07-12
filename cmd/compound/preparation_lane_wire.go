package main

import (
	"time"

	"compound/internal/laneselect"
	"compound/internal/routeevidence"
	"compound/internal/work"
)

// Wire the preparation-lane selector seam: the work dispatch surface depends only on the pure
// laneselect types and calls the routeevidence adapter through this function variable, which
// keeps work independent of routeevidence (no import cycle) while the real binary routes
// preparation-class dispatch by capability fit, token-generation speed, and cost.
func init() {
	work.PreparationLaneSelector = func(homePath, class string, now time.Time) (*laneselect.PreparationLaneRanking, error) {
		ranking, err := routeevidence.PlanPreparationLane(homePath, class, routeevidence.PreparationLaneOptions{}, now)
		if err != nil {
			return nil, err
		}
		return &ranking, nil
	}
}
