package projection

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

const (
	// inventoryTextGroupMax is the largest kind-and-provider group listed one
	// line per resource when no --kind filter is given.
	inventoryTextGroupMax = 6
	inventoryTextValueMax = 40
	inventoryTextLabelMax = 60
)

// FormatInventoryText renders the projection for a person, in plain words:
// the snapshot age line, what needs attention (every row, never collapsed),
// the sources, one line per kind, the changes since the previous import, then
// resources grouped by kind and provider with the facts that are known. Ids
// are masked to their last four characters. A fact that is not known never
// shows a value. Without --kind, a group of more than six resources is one
// summary line that says how to list it, which keeps a real snapshot near 40
// lines.
func FormatInventoryText(p InventoryProjection) string {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	generated, _ := time.Parse(time.RFC3339, p.GeneratedAt)

	if p.Snapshot.State == "missing" {
		add("Inventory: no snapshot.")
	} else {
		age := 0.0
		if p.Snapshot.AgeHours != nil {
			age = *p.Snapshot.AgeHours
		}
		add("Inventory observed %s (%s ago), imported %s. %s.", inventoryShortTime(p.Snapshot.ObservedAt),
			humanInventoryAge(age), inventoryShortTime(p.Snapshot.ImportedAt), inventoryTitleCase(p.Snapshot.State))
	}
	if p.Snapshot.Notice != "" {
		add("Notice: %s", p.Snapshot.Notice)
	}
	if p.OverlayError != "" {
		add("Kinds overlay ignored, built-in kinds served: %s", inventoryClean(p.OverlayError, 200))
	}
	if p.Snapshot.State == "missing" {
		ids := make([]string, 0, len(p.Kinds))
		for _, k := range p.Kinds {
			ids = append(ids, k.ID)
		}
		add("Kinds known: %s.", strings.Join(ids, ", "))
		return strings.Join(lines, "\n") + "\n"
	}

	var attention []InventoryAttention
	for _, a := range p.Attention {
		if a.Kind != "snapshot" { // the notice line already says it
			attention = append(attention, a)
		}
	}
	if len(attention) == 0 {
		add("Needs attention: nothing.")
	} else {
		add("Needs attention (%d):", len(attention))
		for _, a := range attention { // never collapsed
			if a.Kind == "source" || a.ResourceID == "" {
				add("  - %s: %s", inventoryClean(a.Label, inventoryTextLabelMax), inventoryClean(a.Why, 160))
				continue
			}
			add("  - %s (%s): %s", inventoryResourceName(a.Label, a.ResourceID), a.Kind, inventoryClean(a.Why, 160))
		}
	}

	states := map[string]int{}
	for _, s := range p.Sources {
		states[strings.ReplaceAll(s.State, "_", " ")]++
	}
	add("Sources: %d (%s).", len(p.Sources), formatInventoryCounts(states))

	add("Kinds:")
	kindProviders := map[string]map[string]int{}
	moneyUnknown := map[string]int{}
	for _, r := range p.Resources {
		if kindProviders[r.Kind] == nil {
			kindProviders[r.Kind] = map[string]int{}
		}
		kindProviders[r.Kind][r.Provider]++
		for _, f := range r.Facts {
			if (f.Group == InventoryGroupCost || f.Group == InventoryGroupCredit) && f.State != "ok" {
				moneyUnknown[r.Kind]++
				break
			}
		}
	}
	for _, k := range p.Kinds {
		n := p.Summary.ByKind[k.ID]
		if n == 0 {
			continue
		}
		line := fmt.Sprintf("  %s: %d (%s)", k.ID, n, formatInventoryCounts(kindProviders[k.ID]))
		if moneyUnknown[k.ID] > 0 {
			line += fmt.Sprintf("; cost or credit not known for %d", moneyUnknown[k.ID])
		}
		lines = append(lines, line)
	}
	if p.Summary.Resources == 0 {
		add("  none")
	}
	if p.Summary.Carried > 0 {
		add("Shown from the last good read of a source that did not answer: %d resources.", p.Summary.Carried)
	}

	if p.Changes.PreviousObservedAt != "" {
		add("Changes since the import observed %s: %d added, %d removed, %d facts changed, %d links added, %d links removed.",
			inventoryShortTime(p.Changes.PreviousObservedAt), len(p.Changes.Added), len(p.Changes.Removed), len(p.Changes.FactChanges),
			len(p.Changes.LinksAdded), len(p.Changes.LinksRemoved))
	} else if p.Changes.Note != "" {
		add("Changes: %s.", p.Changes.Note)
	}
	lines = append(lines, formatInventoryGraphLines(p)...)

	if len(p.Resources) == 0 {
		add("Resources: none match.")
		return strings.Join(lines, "\n") + "\n"
	}
	add("Resources:")
	type group struct {
		kind, provider string
		items          []InventoryResourceView
	}
	var groups []group
	for _, r := range p.Resources {
		if len(groups) == 0 || groups[len(groups)-1].kind != r.Kind || groups[len(groups)-1].provider != r.Provider {
			groups = append(groups, group{kind: r.Kind, provider: r.Provider})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, r)
	}
	for _, g := range groups {
		// Without --kind, a group of more than six resources is one line:
		// how many, how many warn, how many are carried, and how to list them.
		if p.Filter.Kind == "" && len(g.items) > inventoryTextGroupMax {
			warned, carried := 0, 0
			for _, r := range g.items {
				if r.Carried {
					carried++
				}
				for _, f := range r.Facts {
					if f.Warn {
						warned++
						break
					}
				}
			}
			add("  %s · %s: %d resources, %d with a warning, %d from the last good read; `compound system inventory --kind %s` lists them",
				g.kind, g.provider, len(g.items), warned, carried, g.kind)
			continue
		}
		add("  %s · %s (%d)", g.kind, g.provider, len(g.items))
		for _, r := range g.items {
			add("    %s", inventoryResourceLine(r, generated))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// inventoryResourceLine is one resource: its name, masked id, the facts that
// are known, and which money questions are not known.
func inventoryResourceLine(r InventoryResourceView, now time.Time) string {
	var parts []string
	costUnknown, creditUnknown := false, false
	for _, f := range r.Facts {
		if f.State != "ok" {
			switch f.Group {
			case InventoryGroupCost:
				costUnknown = true
			case InventoryGroupCredit:
				creditUnknown = true
			}
			continue
		}
		parts = append(parts, inventoryClean(f.Title, inventoryTextLabelMax)+" "+formatInventoryValue(f.Value, f.Unit))
	}
	if costUnknown {
		parts = append(parts, "cost not known")
	}
	if creditUnknown {
		parts = append(parts, "credit not known")
	}
	if r.Carried {
		note := "from the last good read"
		if at, err := time.Parse(time.RFC3339, r.LastObservedAt); err == nil {
			note += ", " + humanInventoryAge(math.Max(0, now.Sub(at).Hours())) + " ago"
		}
		parts = append(parts, note)
	}
	line := inventoryResourceName(r.Label, r.ID)
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, " · ")
	}
	return line
}

// inventoryResourceName is the label (when there is one) and the masked id.
func inventoryResourceName(label, id string) string {
	label = inventoryClean(label, inventoryTextLabelMax)
	if label == "" {
		return MaskInventoryID(id)
	}
	return label + " " + MaskInventoryID(id)
}

// formatInventoryValue renders a known value in plain words. It is only ever
// called with the value of an ok fact.
func formatInventoryValue(v any, unit string) string {
	switch x := v.(type) {
	case nil:
		return "not known"
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case string:
		return inventoryClean(x, inventoryTextValueMax)
	}
	f, ok := inventoryNumber(v)
	if !ok {
		if n, isNum := v.(json.Number); isNum {
			return inventoryClean(n.String(), inventoryTextValueMax)
		}
		return inventoryClean(fmt.Sprint(v), inventoryTextValueMax)
	}
	n := formatInventoryNumber(f)
	switch unit {
	case "pct":
		return n + "%"
	case "", "count", "bool":
		return n
	default:
		return n + " " + inventoryClean(unit, 12)
	}
}

func inventoryShortTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return inventoryClean(s, 40)
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func inventoryTitleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// inventoryClean makes collector text safe for one line: control characters
// become spaces and the text is cut at max runes.
func inventoryClean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return string(r[:int(math.Max(1, float64(max-1)))]) + "…"
	}
	return s
}
