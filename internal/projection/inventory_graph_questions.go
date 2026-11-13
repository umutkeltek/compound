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
)

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
// is "<resource kind>.<fact>"; About is the resource id.
type InventoryQuestion struct {
	About      string `json:"about"`
	Kind       string `json:"kind"`
	Question   string `json:"question"`
	Why        string `json:"why"`
	AnswerWith string `json:"answer_with"`
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
		for dep := range g.impact(id) {
			s.Dependents++
			if g.nodes[dep].Kind == "product" {
				s.Products++
			}
		}
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
// declared line would settle them.
func (g *inventoryGraph) questions() []InventoryQuestion {
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
	ask := func(id, kind, question, why, answer string) {
		out = append(out, InventoryQuestion{About: id, Kind: kind, Question: question, Why: why, AnswerWith: answer + inventoryDeclaredTail})
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
		if out[i].Kind != out[j].Kind {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return out[i].About < out[j].About
	})
	if out == nil {
		out = []InventoryQuestion{}
	}
	return out
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
	why := fmt.Sprintf("%d product references are not Compound products and are shown as unknown product: %s", len(names), strings.Join(shown, ", "))
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
	for _, question := range g.questions() {
		if keep(question.About) {
			p.Questions = append(p.Questions, question)
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
	if len(p.Questions) == 0 {
		return append(lines, "Questions: none.")
	}
	counts := map[string]int{}
	for _, q := range p.Questions {
		counts[q.Kind]++
	}
	return append(lines, fmt.Sprintf("Questions: %d (%s); `compound system inventory --questions` lists them.", len(p.Questions), inventoryOrderedCounts(counts)))
}

// FormatInventoryQuestionsText lists the questions, two lines each, with ids
// masked to their last four characters.
func FormatInventoryQuestionsText(p InventoryProjection) string {
	lines := inventorySnapshotLine(p.Snapshot, "Questions")
	if p.Snapshot.State == "missing" {
		return strings.Join(lines, "\n") + "\n"
	}
	if len(p.Questions) == 0 {
		lines = append(lines, "No questions: every fact the questions look at is on file.")
		return strings.Join(lines, "\n") + "\n"
	}
	lines = append(lines, fmt.Sprintf("Questions (%d): things not known that one declared line settles.", len(p.Questions)))
	for _, q := range p.Questions {
		lines = append(lines, fmt.Sprintf("  - %s (%s, %s): %s.", inventoryClean(q.Question, 120), MaskInventoryID(q.About), q.Kind, inventoryClean(q.Why, 120)),
			"    answer with: "+inventoryClean(q.AnswerWith, 240))
	}
	return strings.Join(lines, "\n") + "\n"
}
