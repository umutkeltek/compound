package product

import (
	"fmt"
	"strings"
	"time"
)

// Statuses an area can take besides the evidence statuses: the profile does
// not say whether it applies, or the owner set it aside.
const (
	GrowthUnknown  = "unknown"
	GrowthDeferred = "deferred"
)

// Audit bases: what the status rests on.
const (
	GrowthBasisMetric     = "metric"
	GrowthBasisRecord     = "record"
	GrowthBasisAssessment = "assessment"
	GrowthBasisMixed      = "mixed"
	GrowthBasisNone       = "none"
)

var growthDimLabel = map[string]string{
	"kinds": "kinds", "audiences": "audiences", "revenue_models": "revenue models", "objectives": "objectives",
}

// audit judges every catalog area for one product, in catalog order.
func (c growthCtx) audit(g *growthGroup, app GrowthApp) []GrowthAudit {
	funnelFresh, funnelAny := 0, 0
	for _, f := range app.Funnel {
		switch f.Status {
		case GrowthMeasured:
			funnelFresh++
			funnelAny++
		case GrowthStale, GrowthPartial:
			funnelAny++
		}
	}
	out := make([]GrowthAudit, 0, len(c.cat.Areas))
	status := map[string]string{}
	for _, area := range c.cat.Areas {
		a := c.auditArea(g, app, area, funnelFresh, funnelAny)
		status[a.AreaID] = a.Status
		out = append(out, a)
	}
	for i, area := range c.cat.Areas {
		for _, n := range area.Needs {
			if s := status[n]; s == GrowthBlind || s == GrowthUnknown {
				out[i].BlockedBy = append(out[i].BlockedBy, n)
			}
		}
	}
	return out
}

func (c growthCtx) auditArea(g *growthGroup, app GrowthApp, area growthAreaDef, funnelFresh, funnelAny int) GrowthAudit {
	base := area.spec("")
	a := GrowthAudit{
		AreaID: area.ID, Basis: GrowthBasisNone, Title: base.Title, Question: base.Question, Forms: []string{},
		Evidence: []GrowthEvidence{}, BlockedBy: []string{}, Branch: c.cat.branch(area), Skills: append([]string{}, base.Skills...),
	}
	if d, ok := c.deferral(app.Profile, area); ok {
		a.Status = GrowthDeferred
		a.Reason = "Set aside by the owner: " + strings.TrimSuffix(d.Reason, ".") + "."
		if d.Until != "" {
			a.Reason += " Until " + d.Until + "."
		}
		a.Summary, a.NextMove = a.Reason, "None while it is set aside."
		return a
	}
	if status, reason, move := c.applies(app.Profile, area); status != "" {
		a.Status, a.Reason, a.Summary, a.NextMove = status, reason, reason, move
		return a
	}

	specs := c.forms(app.Profile, area)
	results := make([]growthFormResult, 0, len(specs))
	for _, spec := range specs {
		results = append(results, c.evalForm(g, app, area, spec, funnelFresh, funnelAny))
	}
	if len(specs) == 1 {
		a.Title, a.Question = specs[0].Title, specs[0].Question
	}
	a.Skills = []string{}
	for _, s := range specs {
		if s.Kind != "" || len(specs) > 1 {
			a.Forms = append(a.Forms, s.Title)
		}
		a.Skills = appendUnique(a.Skills, s.Skills...)
	}
	c.combineForms(&a, results)
	return a
}

// deferral finds an owner's deferral that covers the area and has not lapsed.
func (c growthCtx) deferral(p GrowthProfile, area growthAreaDef) (GrowthDeferral, bool) {
	asOf := growthDayStr(c.opts.AsOf)
	for _, d := range p.Deferred {
		if d.Area != area.ID && d.Area != area.Group {
			continue
		}
		if d.Until != "" && d.Until < asOf {
			continue // lapsed
		}
		return d, true
	}
	return GrowthDeferral{}, false
}

// applies checks the area's profile rule. It returns an empty status when the
// area applies.
func (c growthCtx) applies(p GrowthProfile, area growthAreaDef) (status, reason, move string) {
	var unknown []string
	for _, dim := range growthProfileDimensions {
		allowed, ok := area.Applies[dim]
		if !ok {
			continue
		}
		v := p.dim(dim)
		if v.Source == GrowthProfileUnknown {
			unknown = append(unknown, growthDimLabel[dim])
			continue
		}
		hit := false
		for _, x := range v.Values {
			if containsString(allowed, x) {
				hit = true
			}
		}
		if !hit {
			reason = fmt.Sprintf("Does not apply: the product's %s on file are %s, and this area is only for %s.",
				growthDimLabel[dim], strings.Join(growthWords(v.Values), ", "), growthOr(growthWords(allowed)))
			return GrowthNotApplicable, reason, "None needed."
		}
	}
	if len(unknown) > 0 {
		what := growthAnd(unknown)
		return GrowthUnknown, fmt.Sprintf("Not on file: we cannot tell if this applies until the product's %s are on file.", what),
			fmt.Sprintf("Write the product's %s in the product contract.", what)
	}
	return "", "", ""
}

// forms resolves the area for the product's kinds: one form per kind, the base
// row standing in for a kind without a form, and the base row alone when the
// kind is unknown.
func (c growthCtx) forms(p GrowthProfile, area growthAreaDef) []growthAreaSpec {
	if len(p.Kinds.Values) == 0 {
		return []growthAreaSpec{area.spec("")}
	}
	var out []growthAreaSpec
	seen := map[string]bool{}
	for _, kind := range p.Kinds.Values {
		s := area.spec(kind)
		key := s.Kind + "|" + s.Title
		if s.Kind != "" {
			key = "form|" + s.Title
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// evalForm picks the best verdict one form's evidence supports: metrics and
// custom evaluators, facts on file, or a recorded assessment.
func (c growthCtx) evalForm(g *growthGroup, app GrowthApp, area growthAreaDef, spec growthAreaSpec, funnelFresh, funnelAny int) growthFormResult {
	var candidates []growthFormResult
	if r, ok := c.evalMetrics(g, app, spec, funnelFresh, funnelAny); ok {
		candidates = append(candidates, r)
	}
	if r, ok := c.evalRecord(g, spec); ok {
		candidates = append(candidates, r)
	}
	if r, ok := c.evalAssessment(g, area, spec); ok {
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		move := fmt.Sprintf("Decide how to assess %s.", spec.measure)
		if len(spec.Skills) > 0 {
			move = fmt.Sprintf("Run %s and record what it finds.", spec.Skills[0])
		}
		return growthFormResult{
			spec: spec, status: GrowthBlind, basis: GrowthBasisNone, evidence: []GrowthEvidence{},
			summary: fmt.Sprintf("No data source. Nothing measures %s today.", spec.measure), nextMove: move,
		}
	}
	best := candidates[0]
	for _, r := range candidates[1:] {
		if growthStatusRank[r.status] > growthStatusRank[best.status] {
			best = r
		}
	}
	return best
}

var growthStatusRank = map[string]int{GrowthBlind: 0, GrowthStale: 1, GrowthPartial: 2, GrowthMeasured: 3}

// evalRecord reads the product-contract facts the form names.
func (c growthCtx) evalRecord(g *growthGroup, spec growthAreaSpec) (growthFormResult, bool) {
	if len(spec.record) == 0 {
		return growthFormResult{}, false
	}
	var have []string
	var missing []string
	for _, pred := range spec.record {
		v := strings.TrimSpace(g.claims[pred])
		if v == "" || v == EssenceUnknown {
			missing = append(missing, pred)
			continue
		}
		have = append(have, strings.Join(parseList(v), "; "))
	}
	r := growthFormResult{spec: spec, basis: GrowthBasisRecord, evidence: []GrowthEvidence{}}
	switch {
	case len(missing) == 0:
		r.status = GrowthMeasured
		r.summary = "On file: " + growthClip(strings.Join(have, " / "), 120)
		r.nextMove = "Check that it still holds, then act on it."
	case len(have) > 0:
		r.status = GrowthPartial
		r.summary = "On file: " + growthClip(strings.Join(have, " / "), 100) + " Missing: " + strings.Join(missing, ", ") + "."
		r.nextMove = "Write the rest in the product contract: " + strings.Join(missing, ", ") + "."
	default:
		r.status, r.basis = GrowthBlind, GrowthBasisNone
		r.summary = fmt.Sprintf("Nothing on file about %s.", spec.measure)
		r.nextMove = "Write it in the product contract."
	}
	return r, true
}

// evalAssessment reads the newest learned fact that names this area.
func (c growthCtx) evalAssessment(g *growthGroup, area growthAreaDef, spec growthAreaSpec) (growthFormResult, bool) {
	var newest *LearnedFact
	for i := range g.learned {
		l := g.learned[i]
		if l.Area != area.ID {
			continue
		}
		if newest == nil || l.At.After(newest.At) {
			newest = &g.learned[i]
		}
	}
	if newest == nil {
		return growthFormResult{}, false
	}
	date := growthDayStr(newest.At)
	age := int(c.opts.AsOf.Sub(newest.At.UTC().Truncate(24*time.Hour)).Hours() / 24)
	r := growthFormResult{spec: spec, basis: GrowthBasisAssessment, evidence: []GrowthEvidence{{Metric: "learned_fact", Rows: 1, LatestDate: date}}}
	if age > c.opts.AssessDays {
		r.status = GrowthStale
		r.summary = fmt.Sprintf("Last assessed on %s (%s ago): %s", date, growthDays(age), growthClip(newest.Fact, 100))
		r.nextMove = fmt.Sprintf("Assess %s again and record the result.", spec.measure)
		return r, true
	}
	r.status = GrowthMeasured
	r.summary = fmt.Sprintf("Assessed on %s: %s", date, growthClip(newest.Fact, 120))
	r.nextMove = "Act on the assessment, then assess again."
	return r, true
}

// combineForms folds the per-form verdicts into the area's one status.
func (c growthCtx) combineForms(a *GrowthAudit, results []growthFormResult) {
	counts := map[string]int{}
	bases := map[string]bool{}
	for _, r := range results {
		counts[r.status]++
		if r.basis != GrowthBasisNone {
			bases[r.basis] = true
		}
		for _, e := range r.evidence {
			a.Evidence = append(a.Evidence, e)
		}
		if r.basis == GrowthBasisAssessment && r.status != GrowthBlind {
			a.assessment = true
		}
	}
	switch len(bases) {
	case 0:
		a.Basis = GrowthBasisNone
	case 1:
		for b := range bases {
			a.Basis = b
		}
	default:
		a.Basis = GrowthBasisMixed
	}
	n := len(results)
	if n == 1 {
		r := results[0]
		a.Status, a.Summary, a.NextMove = r.status, r.summary, r.nextMove
		a.gap = growthGapKind(r)
		return
	}
	switch {
	case counts[GrowthMeasured] == n:
		a.Status = GrowthMeasured
	case counts[GrowthBlind] == n:
		a.Status = GrowthBlind
	case counts[GrowthStale] == n:
		a.Status = GrowthStale
	default:
		a.Status = GrowthPartial
	}
	// The weakest form decides the next move: it is what is missing.
	weakest := results[0]
	for _, r := range results[1:] {
		if growthStatusRank[r.status] < growthStatusRank[weakest.status] {
			weakest = r
		}
	}
	a.NextMove = weakest.nextMove
	a.gap = growthGapKind(weakest)
	var parts []string
	for _, r := range results {
		if a.Status == GrowthPartial && r.status == GrowthBlind {
			parts = append(parts, fmt.Sprintf("Nothing measures %s yet.", r.spec.Title))
			continue
		}
		parts = append(parts, r.spec.Title+": "+r.summary)
	}
	a.Summary = strings.Join(parts, " ")
}

// growthGapKind is the next-move kind that closes a form's gap.
func growthGapKind(r growthFormResult) string {
	s := r.spec
	switch {
	case len(s.patterns)+len(s.capped)+len(s.companion) > 0 || s.custom != "":
		return "connect_data"
	case len(s.record) > 0:
		return "record_answer"
	}
	return "assess"
}

// growthCoverage counts statuses. The score is the measured share of the areas
// that apply and that the profile can judge.
func growthCoverage(audit []GrowthAudit) GrowthCoverage {
	var cv GrowthCoverage
	for _, a := range audit {
		switch a.Status {
		case GrowthMeasured:
			cv.Measured++
		case GrowthPartial:
			cv.Partial++
		case GrowthStale:
			cv.Stale++
		case GrowthBlind:
			cv.Blind++
		case GrowthNotApplicable:
			cv.NotApplicable++
		case GrowthUnknown:
			cv.Unknown++
		case GrowthDeferred:
			cv.Deferred++
		}
	}
	if judged := len(audit) - cv.NotApplicable - cv.Deferred - cv.Unknown; judged > 0 {
		cv.Score = growthRound(float64(cv.Measured) / float64(judged))
	}
	return cv
}

func growthClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return strings.TrimRight(s[:cut], " ,;.") + "..."
}

// growthWords turns profile values into plain words.
func growthWords(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ReplaceAll(v, "_", " "))
	}
	return out
}

func growthOr(words []string) string  { return growthJoin(words, "or") }
func growthAnd(words []string) string { return growthJoin(words, "and") }

func growthJoin(words []string, conj string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " " + conj + " " + words[len(words)-1]
}
