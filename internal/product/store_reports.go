package product

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// StoreReportsSchemaVersion is the contract version of `compound product
// reports`.
const StoreReportsSchemaVersion = "compound.store_reports.v0.1"

const storeReportsBoundary = "read-only view of store report captures under the Compound home: collectors write them; this reads, checks and shows them; evidence, not truth; writes nothing"

// StoreReportsOptions tunes the view. Now and AsOf are injected so tests never
// depend on the wall clock.
type StoreReportsOptions struct {
	ProductID string
	Now       time.Time
	AsOf      time.Time
	Days      int
	// Check reads raw report files and runs every exactness check.
	Check bool
}

// GrowthAppReports is the store report summary growth carries per app: how
// many catalog reports are in each coverage state, and the fast checks.
type GrowthAppReports struct {
	Fresh         int                    `json:"fresh"`
	Gaps          int                    `json:"gaps"`
	Stale         int                    `json:"stale"`
	NotFinal      int                    `json:"not_final"`
	NotCaptured   int                    `json:"not_captured"`
	NotApplicable int                    `json:"not_applicable"`
	Checks        StoreReportCheckCounts `json:"checks"`
	Command       string                 `json:"command"`
}

// StoreReportsApp is one product's store report coverage.
type StoreReportsApp struct {
	ProductID string                `json:"product_id"`
	Name      string                `json:"name"`
	AppIDs    []string              `json:"app_ids"`
	Summary   GrowthAppReports      `json:"summary"`
	Reports   []StoreReportCoverage `json:"reports"`
}

// StoreReportReach is how many apps one captured report covers.
type StoreReportReach struct {
	ReportID     string `json:"report_id"`
	AppsWithRows int    `json:"apps_with_rows"`
	LinkedApps   int    `json:"linked_apps"`
}

// StoreReportsPortfolio is the account-wide view.
type StoreReportsPortfolio struct {
	ReportsCaptured    int                               `json:"reports_captured"`
	ReportsNotCaptured int                               `json:"reports_not_captured"`
	Account            []StoreReportCoverage             `json:"account"`
	Reach              []StoreReportReach                `json:"reach"`
	UnusedCaptures     []string                          `json:"unused_captures"`
	Checks             map[string]StoreReportCheckCounts `json:"checks"`
}

// StoreReports is the `compound product reports` model.
type StoreReports struct {
	SchemaVersion string                 `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	AsOf          string                 `json:"as_of"`
	Days          int                    `json:"days"`
	Boundary      string                 `json:"boundary"`
	CheckRun      bool                   `json:"check_run"`
	Catalog       StoreReportCatalogInfo `json:"catalog"`
	Reports       []StoreReport          `json:"reports"`
	Portfolio     StoreReportsPortfolio  `json:"portfolio"`
	Identity      StoreReportIdentity    `json:"identity"`
	Apps          []StoreReportsApp      `json:"apps"`
	Checks        []StoreReportCheck     `json:"checks"`
}

func storeReportsCommand(productID string) string {
	return "compound product reports --product " + productID + " --check"
}

// BuildStoreReports reads the store report captures and, with Check, the raw
// report files, and says per product and report what is present, missing or
// not final yet, and whether the sources agree. It reads only.
func BuildStoreReports(homePath string, opts StoreReportsOptions) (StoreReports, error) {
	gopts := normalizeGrowthOptions(GrowthOptions{ProductID: opts.ProductID, Now: opts.Now, AsOf: opts.AsOf})
	if opts.Days == 0 {
		opts.Days = storeReportsDefaultDays
	}
	if opts.Days < 0 {
		return StoreReports{}, fmt.Errorf("--days must be positive")
	}
	w, err := loadGrowthWorld(homePath, gopts)
	if err != nil {
		return StoreReports{}, err
	}
	s := w.stores
	out := StoreReports{
		SchemaVersion: StoreReportsSchemaVersion,
		GeneratedAt:   gopts.Now.Format(time.RFC3339),
		AsOf:          growthDayStr(gopts.AsOf),
		Days:          opts.Days,
		Boundary:      storeReportsBoundary,
		CheckRun:      opts.Check,
		Catalog:       s.cat.info(),
		Apps:          []StoreReportsApp{},
	}
	for _, r := range s.cat.Reports {
		out.Reports = append(out.Reports, r.clone())
	}
	groupsOf := storeAppGroups(w.identity)
	link := func(appID string) string {
		if set := groupsOf[appID]; len(set) == 1 {
			for g := range set {
				return g.canonical
			}
		}
		return ""
	}
	window := newStoreWindow(gopts.AsOf, opts.Days)
	checks, err := runStoreReportChecks(homePath, s, window, opts.Check, link)
	if err != nil {
		return StoreReports{}, err
	}
	if opts.ProductID != "" {
		keep := map[string]bool{}
		for _, g := range w.groups {
			keep[g.canonical] = true
		}
		var mine []StoreReportCheck
		for _, c := range checks {
			if c.AppID == "" || keep[c.ProductID] {
				mine = append(mine, c)
			}
		}
		checks = mine
	}
	out.Checks = checks
	if out.Checks == nil {
		out.Checks = []StoreReportCheck{}
	}
	out.Identity = w.identitySection(s)
	ctx := growthCtx{opts: gopts, cat: w.cat}
	for _, g := range w.groups {
		ids := linkedAppIDs(w.identity, g)
		if len(ids) == 0 && opts.ProductID == "" && !containsString(ctx.profile(g).Kinds.Values, "ios_app") {
			continue
		}
		app := StoreReportsApp{ProductID: g.canonical, Name: growthHumanize(g.canonical), AppIDs: append([]string{}, ids...)}
		for _, r := range s.cat.Reports {
			app.Reports = append(app.Reports, s.coverage(r.ID, ids, false, opts.Days))
		}
		app.Summary = storeReportSummary(app.Reports, countStoreChecks(checks, g.canonical), g.canonical)
		if len(ids) == 0 {
			app.Summary.Checks = StoreReportCheckCounts{NotRun: len(storeReportCheckNames)}
		}
		out.Apps = append(out.Apps, app)
	}
	sort.SliceStable(out.Apps, func(i, j int) bool {
		a, b := out.Apps[i].Summary, out.Apps[j].Summary
		if a.Checks.Failed != b.Checks.Failed {
			return a.Checks.Failed > b.Checks.Failed
		}
		return out.Apps[i].ProductID < out.Apps[j].ProductID
	})
	out.Portfolio = w.portfolio(s, checks, opts.Days)
	return out, nil
}

func storeReportSummary(covs []StoreReportCoverage, checks StoreReportCheckCounts, productID string) GrowthAppReports {
	sum := GrowthAppReports{Checks: checks, Command: storeReportsCommand(productID)}
	for _, c := range covs {
		switch c.State {
		case StoreReportFresh:
			sum.Fresh++
		case StoreReportGaps:
			sum.Gaps++
		case StoreReportStale:
			sum.Stale++
		case StoreReportNotFinal:
			sum.NotFinal++
		case StoreReportNotCaptured:
			sum.NotCaptured++
		case StoreReportNotApplicable:
			sum.NotApplicable++
		}
	}
	return sum
}

// appReportCounts is growth's per-app store report summary, from the fast
// path only: coverage from file names and the two series, and the days_final
// check; the raw checks are not run inside growth.
func (w *growthWorld) appReportCounts(g *growthGroup, productID string) GrowthAppReports {
	ids := linkedAppIDs(w.identity, g)
	var covs []StoreReportCoverage
	for _, r := range w.stores.cat.Reports {
		covs = append(covs, w.stores.coverage(r.ID, ids, false, storeReportsDefaultDays))
	}
	counts := StoreReportCheckCounts{NotRun: len(storeReportCheckNames)}
	if len(ids) > 0 {
		counts = countStoreChecks(w.fastChecks(), productID)
	}
	return storeReportSummary(covs, counts, productID)
}

// fastChecks runs the fast check once per world.
func (w *growthWorld) fastChecks() []StoreReportCheck {
	if w.fast == nil {
		w.fast, _ = runStoreReportChecks("", w.stores, newStoreWindow(w.stores.asOf, storeReportsDefaultDays), false, func(string) string { return "" })
	}
	return w.fast
}

// identitySection lists every app id the captures hold with the product it
// belongs to, and products that look like store apps but have no app id.
func (w *growthWorld) identitySection(s *storeReportScan) StoreReportIdentity {
	out := StoreReportIdentity{Apps: []StoreAppLink{}, AppsWithoutProduct: []string{}, Conflicts: []string{}, ProductsWithoutApp: []string{}}
	ids := map[string]bool{}
	for _, id := range s.sortedAppsSeen() {
		ids[id] = true
	}
	for id := range w.identity {
		ids[id] = true
	}
	for _, id := range growthSortedKeys(ids) {
		link := StoreAppLink{AppID: id, Sources: []string{}, Claims: []string{}, SeenIn: []string{}}
		for rid := range s.appsSeen[id] {
			link.SeenIn = append(link.SeenIn, rid)
		}
		sort.Strings(link.SeenIn)
		known := map[string]bool{}
		for _, c := range w.identity[id] {
			link.Sources = appendUnique(link.Sources, c.source)
			name := c.product
			if c.group != nil {
				name = c.group.canonical
				known[name] = true
			}
			link.Claims = appendUnique(link.Claims, name)
		}
		sort.Strings(link.Sources)
		sort.Strings(link.Claims)
		switch len(known) {
		case 0:
			link.Status = "no_product"
			out.AppsWithoutProduct = append(out.AppsWithoutProduct, id)
		case 1:
			link.Status = "linked"
			for p := range known {
				link.ProductID = p
			}
			out.Linked++
		default:
			link.Status = "conflict"
			out.Conflicts = append(out.Conflicts, id)
		}
		out.Apps = append(out.Apps, link)
	}
	ctx := growthCtx{opts: normalizeGrowthOptions(GrowthOptions{AsOf: s.asOf, Now: s.asOf}), cat: w.cat}
	for _, g := range w.all {
		if len(linkedAppIDs(w.identity, g)) == 0 && containsString(ctx.profile(g).Kinds.Values, "ios_app") {
			out.ProductsWithoutApp = append(out.ProductsWithoutApp, g.canonical)
		}
	}
	sort.Strings(out.ProductsWithoutApp)
	return out
}

func (w *growthWorld) portfolio(s *storeReportScan, checks []StoreReportCheck, days int) StoreReportsPortfolio {
	p := StoreReportsPortfolio{Account: []StoreReportCoverage{}, Reach: []StoreReportReach{}, UnusedCaptures: []string{}, Checks: map[string]StoreReportCheckCounts{}}
	groupsOf := storeAppGroups(w.identity)
	for _, r := range s.cat.Reports {
		if r.Capture.None() {
			p.ReportsNotCaptured++
			continue
		}
		p.ReportsCaptured++
		p.Account = append(p.Account, s.coverage(r.ID, nil, true, days))
		f := s.found[r.ID]
		reach := StoreReportReach{ReportID: r.ID, AppsWithRows: len(f.appDays)}
		for id := range f.appDays {
			if len(groupsOf[id]) == 1 {
				reach.LinkedApps++
			}
		}
		p.Reach = append(p.Reach, reach)
		if f.captured && len(r.Feeds.Kpis) == 0 && len(r.Feeds.Areas) == 0 {
			p.UnusedCaptures = append(p.UnusedCaptures, r.ID)
		}
	}
	for _, name := range storeReportCheckNames {
		var c StoreReportCheckCounts
		for _, r := range checks {
			if r.Check != name {
				continue
			}
			switch r.Status {
			case StoreCheckOK:
				c.OK++
			case StoreCheckFailed:
				c.Failed++
			default:
				c.NotRun++
			}
		}
		p.Checks[name] = c
	}
	return p
}

// Validate checks the model's shape before it is printed.
func (m StoreReports) Validate() error {
	if m.SchemaVersion != StoreReportsSchemaVersion {
		return fmt.Errorf("schema_version = %q", m.SchemaVersion)
	}
	states := map[string]bool{StoreReportFresh: true, StoreReportGaps: true, StoreReportStale: true, StoreReportNotFinal: true, StoreReportNotCaptured: true, StoreReportNotApplicable: true}
	for _, a := range m.Apps {
		if len(a.Reports) != len(m.Reports) {
			return fmt.Errorf("app %s has %d report rows, want %d", a.ProductID, len(a.Reports), len(m.Reports))
		}
		for _, r := range a.Reports {
			if !states[r.State] {
				return fmt.Errorf("app %s report %s state %q", a.ProductID, r.ReportID, r.State)
			}
		}
	}
	results := map[string]bool{StoreCheckOK: true, StoreCheckFailed: true, StoreCheckNotRun: true}
	for _, c := range m.Checks {
		if !results[c.Status] || !containsString(storeReportCheckNames, c.Check) {
			return fmt.Errorf("check %q result %q", c.Check, c.Status)
		}
	}
	return nil
}

// RenderStoreReportsText prints the view in plain lines: failures first, then
// a few lines per app.
func RenderStoreReportsText(w io.Writer, m StoreReports) {
	fmt.Fprintf(w, "# store reports (as of %s, %d-day window)\n", m.AsOf, m.Days)
	fmt.Fprintf(w, "catalog: %d reports, %d captured", m.Catalog.Reports, m.Catalog.Captured)
	if m.Catalog.Overlay != "" {
		fmt.Fprintf(w, ", overlay %s", m.Catalog.Overlay)
	}
	if m.Catalog.OverlayError != "" {
		fmt.Fprintf(w, ", overlay not applied: %s", m.Catalog.OverlayError)
	}
	fmt.Fprintln(w)
	parts := make([]string, 0, len(storeReportCheckNames))
	for _, name := range storeReportCheckNames {
		c := m.Portfolio.Checks[name]
		parts = append(parts, fmt.Sprintf("%s ok %d failed %d not run %d", name, c.OK, c.Failed, c.NotRun))
	}
	fmt.Fprintf(w, "checks: %s\n", strings.Join(parts, "; "))
	if !m.CheckRun {
		fmt.Fprintln(w, "  raw checks not run; add --check")
	}
	for _, c := range m.Checks {
		if c.Status != StoreCheckFailed {
			continue
		}
		who := c.ProductID
		if who == "" {
			who = "account"
		}
		if c.AppID != "" && c.ProductID == "" {
			who = "app " + c.AppID
		}
		line := fmt.Sprintf("FAILED %s %s %s", c.Check, who, c.Period)
		if c.Field != "" {
			line += " " + c.Field
		}
		if c.Expected != nil && c.Actual != nil {
			line += fmt.Sprintf(": expected %s, actual %s, difference %s", growthNum(*c.Expected), growthNum(*c.Actual), growthNum(*c.Difference))
		}
		if c.Note != "" {
			line += " (" + c.Note + ")"
		}
		fmt.Fprintln(w, line)
	}
	id := m.Identity
	fmt.Fprintf(w, "identity: %d app ids linked, %d without product, %d in conflict, %d store products without app id\n",
		id.Linked, len(id.AppsWithoutProduct), len(id.Conflicts), len(id.ProductsWithoutApp))
	if len(m.Portfolio.UnusedCaptures) > 0 {
		fmt.Fprintf(w, "captured but no reader uses: %s\n", strings.Join(m.Portfolio.UnusedCaptures, ", "))
	}
	for _, a := range m.Apps {
		s := a.Summary
		fmt.Fprintf(w, "%s [%s] app %s: fresh %d, gaps %d, stale %d, not final %d, not captured %d, n/a %d; checks ok %d failed %d not run %d\n",
			a.Name, a.ProductID, strings.Join(a.AppIDs, ","), s.Fresh, s.Gaps, s.Stale, s.NotFinal, s.NotCaptured, s.NotApplicable, s.Checks.OK, s.Checks.Failed, s.Checks.NotRun)
		shown := 0
		for _, r := range a.Reports {
			if r.State == StoreReportNotCaptured || r.State == StoreReportNotApplicable || shown >= 6 {
				continue
			}
			shown++
			line := fmt.Sprintf("  %s: %s, %s..%s", r.ReportID, r.State, r.FirstDay, r.NewestDay)
			if n := len(r.MissingDays); n > 0 {
				line += fmt.Sprintf(", %d missing", n)
			}
			if r.Note != "" && r.State != StoreReportFresh {
				line += " (" + r.Note + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
}
