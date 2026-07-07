package mem

import (
	"fmt"

	"compound/internal/reviewartifact"
)

func (p MeaningWritePlan) ReviewOperatorCard() (reviewartifact.OperatorCard, error) {
	return OperatorCardForMeaningWritePlan(p)
}

func OperatorCardForMeaningWritePlan(p MeaningWritePlan) (reviewartifact.OperatorCard, error) {
	entity, err := reviewartifact.RequireText("entity", p.Entity)
	if err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	predicate, err := reviewartifact.RequireText("predicate", p.Predicate)
	if err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	return reviewartifact.NewOperatorCard(
		fmt.Sprintf("Memory has a curated write ready for %s %s. Review the source edit?", reviewartifact.ShortLabel(entity, 34), reviewartifact.ShortLabel(reviewartifact.PlainLabel(predicate), 30)),
		entity,
		"meaning-write|"+reviewartifact.FirstNonEmpty(p.ClaimType, p.Predicate),
		"curated memory changes only after source review",
	)
}

func OperatorCardForBakeoffReport(r BakeoffReport) (reviewartifact.OperatorCard, error) {
	engine, err := reviewartifact.RequireText("candidate_engine", r.CandidateEngine)
	if err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	return reviewartifact.NewOperatorCard(
		fmt.Sprintf("A recall bake-off ran %d golden queries against %s. Read the scores.", r.QueryCount, reviewartifact.ShortLabel(engine, 42)),
		engine,
		"memory-bakeoffs|"+engine,
		"",
	)
}
