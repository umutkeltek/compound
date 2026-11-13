package projection

// What the graph adds to `compound system inventory`: the pieces many things
// depend on, the questions a person can settle with one declared line, and
// one attention row for product references Compound does not know.

import (
	"fmt"
	"sort"
	"strings"
)

const (
	inventorySharedMax           = 15
	inventoryTextSharedMax       = 6
	inventoryUnknownProductNames = 5
	// inventoryTextQuestionsMax is how many questions the default text lists;
	// --all (or --json) lists every one.
	inventoryTextQuestionsMax = 12
)

// InventoryTextOptions shapes a text view: All lists every row of a group
// that is otherwise folded to its first few and a count.
type InventoryTextOptions struct {
	All bool
}

// InventoryShared is a piece several things depend on (impact walk): how many
// things in all, and how many of them are products.
type InventoryShared struct {
	Resource   string `json:"resource"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Dependents int    `json:"dependents"`
	Products   int    `json:"products"`
}

// InventoryQuestion is one thing not known that a declared line settles. Kind
// is "<resource kind>.<fact>"; About is the resource id. Products and
// Dependents are how many products and things depend on it (the impact
// walk): questions are ranked by them, so the ones that matter come first.
type InventoryQuestion struct {
	About      string `json:"about"`
	Kind       string `json:"kind"`
	Question   string `json:"question"`
	Why        string `json:"why"`
	AnswerWith string `json:"answer_with"`
	Products   int    `json:"products"`
	Dependents int    `json:"dependents"`
	label      string
}

// InventoryCoverageGap is a fact that no reader reports for any thing of a
// kind. That is a gap in what is collected, not a question for a person
// about each thing, so it is shown once. Kind is "<resource kind>.<fact>";
// Resources counts the things that would each have been asked; About lists
// their ids.
type InventoryCoverageGap struct {
	Kind       string   `json:"kind"`
	Resources  int      `json:"resources"`
	Gap        string   `json:"gap"`
	AnswerWith string   `json:"answer_with"`
	About      []string `json:"about"`
}

// inventoryGapWords says, for each question kind, what no reader reports and
// how the things are counted (one, many).
var inventoryGapWords = map[string][3]string{
	"hostname.traced":                {"where hostnames lead, end to end", "hostname", "hostnames"},
	"deployment.live":                {"whether deployments are live", "deployment of a product with more than one", "deployments of products with more than one"},
	"identity.second_factor_on_file": {"whether a second factor is on file", "sign-in identity an account uses", "sign-in identities accounts use"},
	"identity.recovery_on_file":      {"whether a recovery route is on file", "sign-in identity an account uses", "sign-in identities accounts use"},
	"datastore.backed_up":            {"whether datastores are backed up", "datastore", "datastores"},
	"backup_target.off_host":         {"whether backup targets are kept off the host they back up", "backup target", "backup targets"},
}

func (gap *InventoryCoverageGap) fill() {
	words, fact := inventoryGapWords[gap.Kind], gap.Kind[strings.Index(gap.Kind, ".")+1:]
	noun := words[2]
	if gap.Resources == 1 {
		noun = words[1]
	}
	gap.Gap = fmt.Sprintf("No reader reports %s: %d %s", words[0], gap.Resources, noun)
	gap.AnswerWith = "a collector reader that reports " + fact + ", or declared " + fact + " facts on the ones that matter" + inventoryDeclaredTail
}

// impactCounts is how many things, and how many products, depend on id
// (each counted once), computed once per graph.
func (g *inventoryGraph) impactCounts(id string) (dependents, products int) {
	if c, ok := g.impactCache[id]; ok {
		return c[0], c[1]
	}
	for dep := range g.impact(id) {
		dependents++
		if g.nodes[dep].Kind == "product" {
			products++
		}
	}
	if g.impactCache == nil {
		g.impactCache = map[string][2]int{}
	}
	g.impactCache[id] = [2]int{dependents, products}
	return dependents, products
}

// inventoryQuestionOrder is the order question kinds are listed in.
var inventoryQuestionOrder = []string{
	"hostname.traced", "deployment.live", "identity.second_factor_on_file", "identity.recovery_on_file",
	"datastore.backed_up", "backup_target.off_host", "repo.has_remote",
}

const inventoryDeclaredTail = "; give by, on and evidence"

// shared lists the pieces with at least two dependents, most products first,
// then most dependents; at most inventorySharedMax.
func (g *inventoryGraph) shared() []InventoryShared {
	out := []InventoryShared{}
	for _, id := range g.ids {
		n := g.nodes[id]
		if n.Unknown != "" || n.Kind == "product" || len(g.in[id]) == 0 {
			continue
		}
		s := InventoryShared{Resource: id, Label: n.Label, Kind: n.Kind}
		s.Dependents, s.Products = g.impactCounts(id)
		if s.Dependents >= 2 {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Products != b.Products {
			return a.Products > b.Products
		}
		if a.Dependents != b.Dependents {
			return a.Dependents > b.Dependents
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Resource < b.Resource
	})
	if len(out) > inventorySharedMax {
		out = out[:inventorySharedMax]
	}
	return out
}

// factIs reports a fact's ok value: known says whether it is reported, ok
// and not the value not_known.
func (n *inventoryNode) factIs(name string) (value any, known bool) {
	f, ok := n.Facts[name]
	if !ok || f.State != "ok" || f.Value == inventoryNotKnownValue {
		return nil, false
	}
	return f.Value, true
}

func inventoryQuestionWhy(fact string, value any, known bool) string {
	switch {
	case !known:
		return fact + " is not known"
	case value == false:
		return fact + " is no"
	}
	return fact + " is " + fmt.Sprint(value)
}

// questions are generated from facts that are not known (or no) where one
// declared line would settle them, ranked by how many products and things
// depend on what they are about. A fact that no thing of its kind reports is
// a gap in what is collected: it becomes one coverage gap, not a question per
// thing. A sign-in identity's second-factor and recovery questions are asked
// only when an account signs in with it.
func (g *inventoryGraph) questions() ([]InventoryQuestion, []InventoryCoverageGap) {
	reported := map[string]bool{} // "<kind>.<fact>" some thing of the kind reports, in any state
	for _, id := range g.ids {
		if n := g.nodes[id]; n.Unknown == "" {
			for name := range n.Facts {
				reported[n.Kind+"."+name] = true
			}
		}
	}
	signedInWith := map[string]bool{} // identities an account signs in with
	for _, e := range g.edges {
		if e.Rel == "signs_in_with" && g.nodes[e.From].Kind == "account" {
			signedInWith[e.To] = true
		}
	}
	deployments := map[string]int{} // deployment -> most deployments any of its products has
	for _, id := range g.ids {
		if g.nodes[id].Kind != "product" {
			continue
		}
		var mine []string
		for _, e := range g.out[id] {
			if e.Rel == "deployed_on" && g.nodes[e.To].Kind == "deployment" {
				mine = append(mine, e.To)
			}
		}
		for _, d := range mine {
			deployments[d] = max(deployments[d], len(mine))
		}
	}
	var out []InventoryQuestion
	gaps := map[string]*InventoryCoverageGap{}
	ask := func(id, kind, question, why, answer string) {
		if !reported[kind] {
			gap := gaps[kind]
			if gap == nil {
				gap = &InventoryCoverageGap{Kind: kind, About: []string{}}
				gaps[kind] = gap
			}
			gap.About = append(gap.About, id)
			return
		}
		dependents, products := g.impactCounts(id)
		out = append(out, InventoryQuestion{About: id, Kind: kind, Question: question, Why: why, AnswerWith: answer + inventoryDeclaredTail,
			Products: products, Dependents: dependents, label: g.nodes[id].Label})
	}
	for _, id := range g.ids {
		n := g.nodes[id]
		if n.Unknown != "" {
			continue
		}
		label := inventoryClean(n.Label, inventoryTextLabelMax)
		if label == "" {
			label = "this " + strings.ReplaceAll(n.Kind, "_", " ")
		}
		switch n.Kind {
		case "hostname":
			if _, known := n.factIs("traced"); !known {
				ask(id, "hostname.traced", "Where does "+label+" lead, end to end?", "how the name reaches what serves it is not known",
					"a declared fact traced: end_to_end (or inferred) on this hostname and a routes_to or points_to link to what it reaches")
			}
		case "deployment":
			if _, known := n.factIs("live"); !known && deployments[id] > 1 {
				ask(id, "deployment.live", "Is "+label+" live?", fmt.Sprintf("its product has %d deployments and whether this one is live is not known", deployments[id]),
					"a declared fact live: yes or no on this deployment")
			}
		case "identity":
			if !signedInWith[id] {
				break // no account signs in with it: its factors are not a question
			}
			if v, known := n.factIs("second_factor_on_file"); v != true {
				ask(id, "identity.second_factor_on_file", "Is a second factor on file for "+label+"?", inventoryQuestionWhy("second factor on file", v, known),
					"a declared fact second_factor_on_file: yes once one is set up, naming where it is kept, never the code")
			}
			if v, known := n.factIs("recovery_on_file"); v != true {
				ask(id, "identity.recovery_on_file", "Is a recovery route on file for "+label+"?", inventoryQuestionWhy("recovery route on file", v, known),
					"a declared fact recovery_on_file: yes and a recovers_through link to the mailbox or identity that recovers it")
			}
		case "datastore":
			if _, known := n.factIs("backed_up"); !known {
				ask(id, "datastore.backed_up", "Is "+label+" backed up?", "whether it is backed up is not known",
					"a declared fact backed_up: yes or no and a backs_up_to link to the backup target")
			}
		case "backup_target":
			if v, known := n.factIs("off_host"); v != true {
				ask(id, "backup_target.off_host", "Is "+label+" kept off the host it backs up?", inventoryQuestionWhy("off the host", v, known),
					"a declared fact off_host: yes or no on this backup target")
			}
		case "repo":
			if v, _ := n.factIs("has_remote"); v == false {
				ask(id, "repo.has_remote", "Where else is "+label+" kept besides this machine?", "it has no remote: the only copy is local",
					"push it to a remote so the repo scan reports has_remote: yes, or a declared fact has_remote: yes")
			}
		}
	}
	rank := map[string]int{}
	for i, k := range inventoryQuestionOrder {
		rank[k] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Products != b.Products {
			return a.Products > b.Products
		}
		if a.Dependents != b.Dependents {
			return a.Dependents > b.Dependents
		}
		if a.label != b.label {
			return a.label < b.label
		}
		if a.About != b.About {
			return a.About < b.About
		}
		return rank[a.Kind] < rank[b.Kind]
	})
	if out == nil {
		out = []InventoryQuestion{}
	}
	gapList := []InventoryCoverageGap{}
	for _, kind := range inventoryQuestionOrder {
		if gap := gaps[kind]; gap != nil {
			gap.Resources = len(gap.About)
			gap.fill()
			gapList = append(gapList, *gap)
		}
	}
	return out, gapList
}

// unknownProductsAttention is one attention row naming the product references
// Compound does not know, or nil when there are none.
func (g *inventoryGraph) unknownProductsAttention() *InventoryAttention {
	var names []string
	for _, id := range g.ids {
		if n := g.nodes[id]; n.Unknown == inventoryUnknownProduct {
			names = append(names, inventoryClean(strings.TrimPrefix(id, InventoryProductPrefix), inventoryTextLabelMax))
		}
	}
	if len(names) == 0 {
		return nil
	}
	shown := names
	if len(shown) > inventoryUnknownProductNames {
		shown = shown[:inventoryUnknownProductNames]
	}
	why := fmt.Sprintf("%d product references are not Compound products and are shown as unknown products: %s", len(names), strings.Join(shown, ", "))
	if len(names) == 1 {
		why = "1 product reference is not a Compound product and is shown as an unknown product: " + names[0]
	}
	if len(names) > len(shown) {
		why += fmt.Sprintf(" and %d more (`compound system graph` lists them)", len(names)-len(shown))
	}
	return &InventoryAttention{Kind: "product", Label: "unknown products", Title: "Product references", Why: why, rank: 5}
}

// addGraphFindings fills shared pieces, questions and the unknown-product row
// of the inventory projection, narrowed by its kind and provider filter.
func (p *InventoryProjection) addGraphFindings(g *inventoryGraph, q InventoryQuery) {
	keep := func(id string) bool {
		n := g.nodes[id]
		return (q.Kind == "" || n.Kind == q.Kind) && (q.Provider == "" || n.Provider == q.Provider)
	}
	for _, s := range g.shared() {
		if keep(s.Resource) {
			p.Shared = append(p.Shared, s)
		}
	}
	questions, gaps := g.questions()
	for _, question := range questions {
		if keep(question.About) {
			p.Questions = append(p.Questions, question)
		}
	}
	for _, gap := range gaps {
		about := []string{}
		for _, id := range gap.About {
			if keep(id) {
				about = append(about, id)
			}
		}
		if len(about) > 0 {
			gap.About, gap.Resources = about, len(about)
			gap.fill()
			p.CoverageGaps = append(p.CoverageGaps, gap)
		}
	}
	if row := g.unknownProductsAttention(); row != nil && (q.Kind == "" || q.Kind == "product") && q.Provider == "" {
		p.Attention = append(p.Attention, *row)
	}
}

// formatInventoryGraphLines are the inventory text view's lines for shared
// pieces and the questions count: at most eight.
func formatInventoryGraphLines(p InventoryProjection) []string {
	var lines []string
	if len(p.Shared) > 0 {
		lines = append(lines, "Shared pieces (how many things depend on each; products first):")
		for i, s := range p.Shared {
			if i == inventoryTextSharedMax {
				break
			}
			lines = append(lines, fmt.Sprintf("  %s (%s): %d dependents, %d products", inventoryResourceName(s.Label, s.Resource), s.Kind, s.Dependents, s.Products))
		}
	}
	gaps := map[string]int{}
	for _, gap := range p.CoverageGaps {
		gaps[gap.Kind] = gap.Resources
	}
	notCollected := ""
	if len(gaps) > 0 {
		notCollected = fmt.Sprintf("; %s no reader reports (%s)", inventoryPlural(len(gaps), "fact", "facts"), inventoryOrderedCounts(gaps))
	}
	if len(p.Questions) == 0 && len(gaps) == 0 {
		return append(lines, "Questions: none.")
	}
	counts := map[string]int{}
	for _, q := range p.Questions {
		counts[q.Kind]++
	}
	return append(lines, fmt.Sprintf("Questions: %d (%s)%s; `compound system inventory --questions` lists them.", len(p.Questions), inventoryOrderedCounts(counts), notCollected))
}

// FormatInventoryQuestionsText lists the top questions, two lines each, and
// the coverage gaps, with ids masked to their last four characters.
func FormatInventoryQuestionsText(p InventoryProjection) string {
	return FormatInventoryQuestionsTextWith(p, InventoryTextOptions{})
}

// FormatInventoryQuestionsTextWith is FormatInventoryQuestionsText; All lists
// every question instead of the top inventoryTextQuestionsMax.
func FormatInventoryQuestionsTextWith(p InventoryProjection, opts InventoryTextOptions) string {
	lines := inventorySnapshotLine(p.Snapshot, "Questions")
	if p.Snapshot.State == "missing" {
		return strings.Join(lines, "\n") + "\n"
	}
	switch {
	case len(p.Questions) == 0 && len(p.CoverageGaps) == 0:
		lines = append(lines, "No questions: every fact the questions look at is on file.")
		return strings.Join(lines, "\n") + "\n"
	case len(p.Questions) == 0:
		lines = append(lines, "No questions about single things.")
	case opts.All || len(p.Questions) <= inventoryTextQuestionsMax:
		lines = append(lines, fmt.Sprintf("Questions (%d), the ones most products and things depend on first: things not known that one declared line settles.", len(p.Questions)))
	default:
		lines = append(lines, fmt.Sprintf("Questions: the top %d of %d, the ones most products and things depend on first (`--questions --all` lists every one; --json too).",
			inventoryTextQuestionsMax, len(p.Questions)))
	}
	for i, q := range p.Questions {
		if !opts.All && i == inventoryTextQuestionsMax {
			break
		}
		weight := ""
		if q.Products > 0 || q.Dependents > 0 {
			weight = fmt.Sprintf("; dependents: %s, %s in all", inventoryPlural(q.Products, "product", "products"), inventoryPlural(q.Dependents, "thing", "things"))
		}
		lines = append(lines, fmt.Sprintf("  - %s (%s, %s%s): %s.", inventoryClean(q.Question, 120), MaskInventoryID(q.About), q.Kind, weight, inventoryClean(q.Why, 120)),
			"    answer with: "+inventoryClean(q.AnswerWith, 240))
	}
	if len(p.CoverageGaps) > 0 {
		lines = append(lines, fmt.Sprintf("Not collected (%d): facts no reader reports for any thing of their kind; not asked thing by thing.", len(p.CoverageGaps)))
		for _, gap := range p.CoverageGaps {
			lines = append(lines, fmt.Sprintf("  - %s (%s).", inventoryClean(gap.Gap, 160), gap.Kind), "    answer with: "+inventoryClean(gap.AnswerWith, 240))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
