package product

import (
	"fmt"
	"sort"
	"strings"
)

// growthMaxMoves caps the next moves per product.
const growthMaxMoves = 5

var growthMoveKinds = map[string]bool{
	"refresh_data": true, "set_profile": true, "connect_data": true, "record_answer": true,
	"assess": true, "review_hypothesis": true, "run_experiment": true,
}

// growthBrief is the command that prints one area's brief for one product.
func growthBrief(productID, areaID string) string {
	return fmt.Sprintf("compound product growth --product %s --area %s", productID, areaID)
}

// growthApplies reports whether an area status belongs to an area that
// applies to the product and is judged: measured, partial, stale or blind.
// Unknown, not-applicable and set-aside areas do not.
func growthApplies(status string) bool {
	switch status {
	case GrowthMeasured, GrowthPartial, GrowthStale, GrowthBlind:
		return true
	}
	return false
}

// unblocks counts, per area, the areas that need it directly or through
// other areas, counting only the areas that apply to the product.
func (c *growthAreaCatalog) unblocks(status map[string]string) map[string]int {
	dependents := map[string][]string{}
	for _, a := range c.Areas {
		for _, n := range a.Needs {
			dependents[n] = append(dependents[n], a.ID)
		}
	}
	out := map[string]int{}
	for _, a := range c.Areas {
		seen := map[string]bool{}
		queue := append([]string{}, dependents[a.ID]...)
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			queue = append(queue, dependents[id]...)
		}
		for id := range seen {
			if growthApplies(status[id]) {
				out[a.ID]++
			}
		}
	}
	return out
}

// nextMoves ranks what to do next for one product. Order is the whole claim:
// stale data first, then the missing profile, then open gaps that unblock the
// most areas, then measured areas to test, then gaps that wait on a need.
func (c growthCtx) nextMoves(g *growthGroup, app GrowthApp) []GrowthNextMove {
	status := map[string]string{}
	index := map[string]int{}
	titles := map[string]string{}
	for i, a := range app.Audit {
		status[a.AreaID] = a.Status
		index[a.AreaID] = i
		titles[a.AreaID] = a.Title
	}
	unblocks := c.cat.unblocks(status)
	ordered := append([]GrowthAudit{}, app.Audit...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if unblocks[a.AreaID] != unblocks[b.AreaID] {
			return unblocks[a.AreaID] > unblocks[b.AreaID]
		}
		return index[a.AreaID] < index[b.AreaID] // catalog order is group order
	})

	var out []GrowthNextMove
	add := func(m GrowthNextMove) {
		if len(out) < growthMaxMoves {
			out = append(out, m)
		}
	}
	move := func(a GrowthAudit, kind, title, why string) GrowthNextMove {
		return GrowthNextMove{
			AreaID: a.AreaID, Kind: kind, Title: title, Why: why, Unblocks: unblocks[a.AreaID],
			BlockedBy: append([]string{}, a.BlockedBy...), Branch: a.Branch, Skills: append([]string{}, a.Skills...),
			Brief: growthBrief(app.ProductID, a.AreaID),
		}
	}
	gapWhy := func(a GrowthAudit) string {
		why := a.Summary
		if n := unblocks[a.AreaID]; n > 0 {
			why += " " + growthCount(float64(n), "other area needs", "other areas need") + " it first."
		}
		return why
	}

	// 1. Data that stopped.
	for _, a := range ordered {
		if a.Status != GrowthStale {
			continue
		}
		title := "Refresh data for " + a.Title
		if a.assessment {
			title = "Assess " + a.Title + " again"
		}
		add(move(a, "refresh_data", title, gapWhy(a)))
	}
	// 2. The profile, when a missing dimension hides whether areas apply.
	if m, ok := c.setProfileMove(app); ok {
		add(m)
	}
	// 3. Open gaps nothing blocks.
	gap := func(a GrowthAudit) bool { return a.Status == GrowthBlind || a.Status == GrowthPartial }
	for _, a := range ordered {
		if !gap(a) || len(a.BlockedBy) > 0 {
			continue
		}
		switch a.gap {
		case "connect_data":
			add(move(a, "connect_data", "Connect data for "+a.Title, gapWhy(a)))
		case "record_answer":
			add(move(a, "record_answer", "Record the answer for "+a.Title, gapWhy(a)))
		default:
			add(move(a, "assess", "Assess "+a.Title, gapWhy(a)))
		}
	}
	// 4. Measured areas: test what is known.
	for _, a := range ordered {
		if a.Status != GrowthMeasured {
			continue
		}
		if c.openHypothesisFor(g, app, a.AreaID) {
			add(move(a, "review_hypothesis", "Review the open hypothesis for "+a.Title, a.Summary))
		} else {
			add(move(a, "run_experiment", "Run an experiment on "+a.Title, a.Summary))
		}
	}
	// 5. Gaps that wait on a need, naming the need.
	for _, a := range ordered {
		if !gap(a) || len(a.BlockedBy) == 0 {
			continue
		}
		var needs []string
		for _, n := range a.BlockedBy {
			needs = append(needs, titles[n])
		}
		kind := a.gap
		if kind == "" {
			kind = "assess"
		}
		add(move(a, kind, "Assess "+a.Title, "Needs "+growthAnd(needs)+" first. "+a.Summary))
	}
	if out == nil {
		out = []GrowthNextMove{}
	}
	return out
}

// setProfileMove asks for the profile dimensions that leave areas unknown.
func (c growthCtx) setProfileMove(app GrowthApp) (GrowthNextMove, bool) {
	dims := map[string]bool{}
	areas := 0
	for i, a := range app.Audit {
		if a.Status != GrowthUnknown {
			continue
		}
		areas++
		for dim := range c.cat.Areas[i].Applies {
			if app.Profile.dim(dim).Source == GrowthProfileUnknown {
				dims[dim] = true
			}
		}
	}
	if areas == 0 || len(dims) == 0 {
		return GrowthNextMove{}, false
	}
	var labels []string
	for _, dim := range growthProfileDimensions {
		if dims[dim] {
			labels = append(labels, growthDimLabel[dim])
		}
	}
	return GrowthNextMove{
		Kind:  "set_profile",
		Title: "Write the product profile",
		Why: fmt.Sprintf("%s cannot be judged until the product's %s are on file.",
			growthCount(float64(areas), "area", "areas"), growthAnd(labels)),
		BlockedBy: []string{}, Branch: "strategy", Skills: []string{},
		Brief: "compound product import <product.yaml> (add a profile block: " + strings.Join(labels, ", ") + ")",
	}, true
}

// openHypothesisFor reports an open hypothesis whose metrics match any form of
// the area this product is judged on.
func (c growthCtx) openHypothesisFor(g *growthGroup, app GrowthApp, areaID string) bool {
	area, ok := c.cat.area(areaID)
	if !ok {
		return false
	}
	for _, spec := range c.forms(app.Profile, area) {
		if g.hasOpenHypothesisFor(spec) {
			return true
		}
	}
	return false
}
