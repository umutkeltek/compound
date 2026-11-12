package product

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// GrowthAreaSchemaVersion is the contract version of the one-area brief.
const GrowthAreaSchemaVersion = "compound.product_growth_area.v0.1"

// GrowthUsageError is a caller mistake (a missing flag or an unknown area id),
// not a failure of the projection.
type GrowthUsageError struct{ Msg string }

func (e *GrowthUsageError) Error() string { return e.Msg }

// GrowthAreaRef names a related area and its status for this product.
type GrowthAreaRef struct {
	AreaID string `json:"area_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// GrowthBriefArea is the catalog row resolved for one product.
type GrowthBriefArea struct {
	ID       string              `json:"id"`
	Number   int                 `json:"number"`
	Group    string              `json:"group"`
	Title    string              `json:"title"`
	Question string              `json:"question"`
	Forms    []string            `json:"forms"`
	Inputs   []string            `json:"inputs"`
	Applies  map[string][]string `json:"applies"`
	Needs    []string            `json:"needs"`
	Branch   string              `json:"branch"`
	Skills   []string            `json:"skills"`
}

// GrowthAreaBrief is what a skill or agent reads before working one area of
// one product.
type GrowthAreaBrief struct {
	SchemaVersion string             `json:"schema_version"`
	GeneratedAt   string             `json:"generated_at"`
	AsOf          string             `json:"as_of"`
	Boundary      string             `json:"boundary"`
	ProductID     string             `json:"product_id"`
	Name          string             `json:"name"`
	Profile       GrowthProfile      `json:"profile"`
	Area          GrowthBriefArea    `json:"area"`
	Audit         GrowthAudit        `json:"audit"`
	Needs         []GrowthAreaRef    `json:"needs"`
	Unblocks      []GrowthAreaRef    `json:"unblocks"`
	Hypotheses    GrowthAreaBriefHyp `json:"hypotheses"`
	Record        string             `json:"record"`
}

// GrowthAreaBriefHyp counts the product's open hypotheses.
type GrowthAreaBriefHyp struct {
	Open int `json:"open"`
}

// BuildGrowthArea builds the brief for opts.Area of opts.ProductID. A missing
// product or an unknown area id is a *GrowthUsageError.
func BuildGrowthArea(homePath string, opts GrowthOptions) (GrowthAreaBrief, error) {
	areaID := strings.TrimSpace(opts.Area)
	if strings.TrimSpace(opts.ProductID) == "" {
		return GrowthAreaBrief{}, &GrowthUsageError{"--area requires --product"}
	}
	cat := opts.catalog
	if cat == nil {
		loaded, err := loadGrowthCatalog(homePath)
		if err != nil {
			return GrowthAreaBrief{}, fmt.Errorf("growth catalog: %w", err)
		}
		cat = loaded
	}
	area, ok := cat.area(areaID)
	if !ok {
		msg := fmt.Sprintf("unknown area %q", areaID)
		if near := cat.nearAreas(areaID); len(near) > 0 {
			msg += "; did you mean " + strings.Join(near, ", ") + "?"
		}
		return GrowthAreaBrief{}, &GrowthUsageError{msg}
	}
	opts.catalog = cat
	model, err := BuildGrowth(homePath, opts)
	if err != nil {
		return GrowthAreaBrief{}, err
	}
	app := model.Apps[0]
	ctx := growthCtx{opts: normalizeGrowthOptions(opts), cat: cat}
	audit := map[string]GrowthAudit{}
	for _, a := range app.Audit {
		audit[a.AreaID] = a
	}
	row := audit[areaID]
	b := GrowthAreaBrief{
		SchemaVersion: GrowthAreaSchemaVersion, GeneratedAt: model.GeneratedAt, AsOf: model.AsOf, Boundary: growthBoundary,
		ProductID: app.ProductID, Name: app.Name, Profile: app.Profile, Audit: row,
		Needs: []GrowthAreaRef{}, Unblocks: []GrowthAreaRef{}, Hypotheses: GrowthAreaBriefHyp{Open: app.Hypotheses.Open},
	}
	b.Area = GrowthBriefArea{
		ID: area.ID, Number: cat.byID[area.ID] + 1, Group: area.Group, Title: row.Title, Question: row.Question,
		Forms: row.Forms, Inputs: []string{}, Applies: map[string][]string{}, Needs: append([]string{}, area.Needs...),
		Branch: row.Branch, Skills: row.Skills,
	}
	for dim, v := range area.Applies {
		b.Area.Applies[dim] = append([]string{}, v...)
	}
	var record []string
	for _, s := range ctx.forms(app.Profile, area) {
		b.Area.Inputs = appendUnique(b.Area.Inputs, s.Inputs...)
		record = appendUnique(record, s.record...)
	}
	ref := func(id string) GrowthAreaRef {
		return GrowthAreaRef{AreaID: id, Title: audit[id].Title, Status: audit[id].Status}
	}
	for _, n := range area.Needs {
		b.Needs = append(b.Needs, ref(n))
	}
	for _, id := range cat.dependents(area.ID) {
		if s := audit[id].Status; s != GrowthNotApplicable && s != GrowthDeferred {
			b.Unblocks = append(b.Unblocks, ref(id))
		}
	}
	b.Record = fmt.Sprintf("Record what you find as a learned fact with area: %s (at, fact, evidence_ref) in the product contract, then run compound product import <product.yaml>.", area.ID)
	if len(record) > 0 {
		b.Record += " This area also reads " + strings.Join(record, ", ") + " from the product contract."
	}
	return b, nil
}

// dependents lists, in catalog order, the areas that need id directly or
// through other areas.
func (c *growthAreaCatalog) dependents(id string) []string {
	seen := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, a := range c.Areas {
			if seen[a.ID] {
				continue
			}
			for _, n := range a.Needs {
				if n == id || seen[n] {
					seen[a.ID] = true
					changed = true
					break
				}
			}
		}
	}
	var out []string
	for _, a := range c.Areas {
		if seen[a.ID] {
			out = append(out, a.ID)
		}
	}
	return out
}

// nearAreas suggests up to three area ids close to a mistyped one.
func (c *growthAreaCatalog) nearAreas(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	type cand struct {
		id   string
		dist int
	}
	var cands []cand
	for _, a := range c.Areas {
		d := min(growthEditDistance(q, a.ID), growthEditDistance(q, strings.TrimPrefix(a.ID, a.Group+"_")))
		if q != "" && (strings.Contains(a.ID, q) || strings.HasSuffix(a.ID, "_"+q)) {
			d = 0
		}
		if d <= 3 {
			cands = append(cands, cand{a.ID, d})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	var out []string
	for i := 0; i < len(cands) && i < 3; i++ {
		out = append(out, cands[i].id)
	}
	return out
}

func growthEditDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// RenderGrowthAreaText prints the brief in about ten lines.
func RenderGrowthAreaText(w io.Writer, b GrowthAreaBrief) {
	a := b.Audit
	fmt.Fprintf(w, "# %s: %s [%s] (as of %s)\n", b.Area.Title, b.Name, b.ProductID, b.AsOf)
	fmt.Fprintf(w, "question: %s\n", b.Area.Question)
	fmt.Fprintf(w, "status: %s (%s). %s\n", a.Status, a.Basis, a.Summary)
	if a.Reason != "" && a.Reason != a.Summary {
		fmt.Fprintf(w, "reason: %s\n", a.Reason)
	}
	if len(b.Area.Forms) > 0 {
		fmt.Fprintf(w, "forms: %s\n", strings.Join(b.Area.Forms, "; "))
	}
	if len(b.Needs) > 0 {
		fmt.Fprintf(w, "needs first: %s\n", growthRefs(b.Needs))
	}
	if len(b.Unblocks) > 0 {
		fmt.Fprintf(w, "unblocks: %s\n", growthRefs(b.Unblocks))
	}
	skills := "none"
	if len(b.Area.Skills) > 0 {
		skills = strings.Join(b.Area.Skills, ", ") + " (optional complements)"
	}
	fmt.Fprintf(w, "branch: %s; skills: %s\n", b.Area.Branch, skills)
	fmt.Fprintf(w, "next move: %s\n", a.NextMove)
	fmt.Fprintf(w, "record: %s\n", b.Record)
}

func growthRefs(refs []GrowthAreaRef) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.Title, r.Status))
	}
	return strings.Join(parts, ", ")
}

// RenderGrowthText prints the whole projection: one line per app with its
// status counts and focus, one line with measured/judged per group, and the
// first next move.
func RenderGrowthText(w io.Writer, model Growth) {
	fmt.Fprintf(w, "# product growth (as of %s, %d-day window)\n", model.AsOf, model.WindowDays)
	fmt.Fprintf(w, "catalog: %d groups, %d areas", model.Catalog.Groups, model.Catalog.Areas)
	if model.Catalog.Overlay != "" {
		fmt.Fprintf(w, ", overlay %s", model.Catalog.Overlay)
	}
	if model.Catalog.OverlayError != "" {
		fmt.Fprintf(w, ", overlay not applied: %s", model.Catalog.OverlayError)
	}
	fmt.Fprintf(w, "\napps: %d, without data: %d\n", model.Portfolio.AppCount, len(model.Portfolio.AppsWithoutData))
	focus := map[string]string{}
	for _, al := range model.Portfolio.Allocation {
		focus[al.ProductID] = al.Focus
	}
	groupOf := map[string]string{}
	for _, a := range model.Areas {
		groupOf[a.ID] = a.Group
	}
	for _, app := range model.Apps {
		proceeds := "n/a"
		if k := app.Kpis["proceeds_usd"]; k.HasData && k.Value != nil {
			proceeds = fmt.Sprintf("$%.2f", *k.Value)
		}
		c := app.Coverage
		fmt.Fprintf(w, "%s [%s]: measured %d, partial %d, stale %d, blind %d, n/a %d, not on file %d, set aside %d; %dd proceeds %s; focus %s\n",
			app.Name, app.ProductID, c.Measured, c.Partial, c.Stale, c.Blind, c.NotApplicable, c.Unknown, c.Deferred, model.WindowDays, proceeds, focus[app.ProductID])
		measured, judged := map[string]int{}, map[string]int{}
		for _, a := range app.Audit {
			switch a.Status {
			case GrowthNotApplicable, GrowthDeferred, GrowthUnknown:
				continue
			case GrowthMeasured:
				measured[groupOf[a.AreaID]]++
			}
			judged[groupOf[a.AreaID]]++
		}
		parts := make([]string, 0, len(model.Groups))
		for _, g := range model.Groups {
			if judged[g.ID] == 0 {
				parts = append(parts, g.ID+" -")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %d/%d", g.ID, measured[g.ID], judged[g.ID]))
		}
		fmt.Fprintf(w, "  groups: %s\n", strings.Join(parts, ", "))
		if len(app.NextMoves) > 0 {
			m := app.NextMoves[0]
			fmt.Fprintf(w, "  next: %s (%s): %s\n", m.Title, m.Kind, m.Brief)
		}
	}
}
