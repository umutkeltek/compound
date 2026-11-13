package projection

import (
	"fmt"
	"sort"
	"strings"
)

// inventoryGraphTextUnresolvedMax bounds the unresolved references listed in
// the text view; --json lists them all.
const inventoryGraphTextUnresolvedMax = 10

// inventorySnapshotLine is the first line of every graph answer.
func inventorySnapshotLine(s InventorySnapshotStatus, what string) []string {
	if s.State == "missing" {
		return []string{what + ": no snapshot.", "Notice: " + s.Notice}
	}
	age := 0.0
	if s.AgeHours != nil {
		age = *s.AgeHours
	}
	lines := []string{fmt.Sprintf("%s from the inventory observed %s (%s ago). %s.", what, inventoryShortTime(s.ObservedAt), humanInventoryAge(age), inventoryTitleCase(s.State))}
	if s.Notice != "" {
		lines = append(lines, "Notice: "+s.Notice)
	}
	return lines
}

// inventoryOrderedCounts renders counts with the largest first, then by name.
func inventoryOrderedCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// inventoryNodeName is a thing as a person reads it: label, masked id, kind.
func inventoryNodeName(label, id, kind, unknown string) string {
	switch unknown {
	case inventoryUnknownProduct:
		return inventoryResourceName(label, id) + " (unknown product)"
	case inventoryNotInInventory:
		return MaskInventoryID(id) + " (not in the inventory)"
	}
	if kind == "" {
		return inventoryResourceName(label, id)
	}
	return inventoryResourceName(label, id) + " (" + kind + ")"
}

// FormatInventoryGraphText renders `compound system graph` in plain words.
// Ids are masked to their last four characters.
func FormatInventoryGraphText(p InventoryGraphProjection) string {
	lines := inventorySnapshotLine(p.Snapshot, "System graph")
	if p.OverlayError != "" {
		lines = append(lines, "Kinds overlay ignored, built-in kinds served: "+inventoryClean(p.OverlayError, 200))
	}
	if p.Snapshot.State == "missing" {
		return strings.Join(lines, "\n") + "\n"
	}
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	add("Things: %d (%s).", p.Nodes, inventoryOrderedCounts(p.NodesByKind))
	add("Links: %d by relation (%s); by confidence (%s).", p.Edges, inventoryOrderedCounts(p.EdgesByRelation), inventoryOrderedCounts(p.EdgesByConfidence))
	if p.CarriedEdges > 0 {
		add("From the last good read of a source that did not answer: %d links.", p.CarriedEdges)
	}
	products, outside := 0, 0
	for _, u := range p.Unresolved {
		if u.Kind == inventoryUnknownProduct {
			products++
		} else {
			outside++
		}
	}
	notKnown := fmt.Sprintf("Not known: %d unknown products, %d things a link names but no source lists", products, outside)
	if p.UnknownRelations > 0 {
		notKnown += fmt.Sprintf(", %d links with a relation the kinds do not know", p.UnknownRelations)
	}
	add("%s.", notKnown)
	for i, u := range p.Unresolved {
		if i == inventoryGraphTextUnresolvedMax {
			add("  ... and %d more; --json lists them", len(p.Unresolved)-i)
			break
		}
		add("  %s, named by %d links", inventoryNodeName(u.Label, u.Ref, "", u.Kind), u.Links)
	}
	if p.Filter.Kind == "" && p.Filter.Rel == "" {
		add("`compound system graph --kind K` or `--rel R` lists the links; `compound system depends X` and `compound system impact X` walk them.")
		return strings.Join(lines, "\n") + "\n"
	}
	var filter []string
	if p.Filter.Kind != "" {
		filter = append(filter, "kind "+p.Filter.Kind)
	}
	if p.Filter.Rel != "" {
		filter = append(filter, "relation "+p.Filter.Rel)
	}
	add("Links with %s: %d.", strings.Join(filter, " and "), p.EdgeListTotal)
	for _, e := range p.EdgeList {
		line := fmt.Sprintf("  %s %s %s", inventoryResourceName(e.FromLabel, e.From)+" ("+e.FromKind+")",
			inventoryClean(e.RelTitle, inventoryTextLabelMax), inventoryResourceName(e.ToLabel, e.To))
		if e.ToKind != "" {
			line += " (" + e.ToKind + ")"
		}
		if e.Confidence != InventoryConfidenceObserved {
			line += " · " + e.Confidence
		}
		if e.Carried {
			line += " · from the last good read"
		}
		lines = append(lines, line)
	}
	if p.EdgeListTruncated {
		add("  ... %d more not listed", p.EdgeListTotal-len(p.EdgeList))
	}
	return strings.Join(lines, "\n") + "\n"
}

// inventoryStepTags are the parts of a step a person must not miss: a link or
// path that is less than observed, warnings, a carried or unknown thing.
func inventoryStepTags(s InventoryGraphStep) string {
	var tags []string
	if s.LinkConfidence != "" && s.LinkConfidence != InventoryConfidenceObserved {
		tags = append(tags, "link "+s.LinkConfidence)
	}
	if s.PathConfidence != InventoryConfidenceObserved && s.PathConfidence != s.LinkConfidence {
		tags = append(tags, "path "+s.PathConfidence)
	}
	for _, w := range s.Warnings {
		tags = append(tags, inventoryClean(w, 80))
	}
	if s.Carried {
		tags = append(tags, "from the last good read")
	}
	if len(tags) == 0 {
		return ""
	}
	return " · " + strings.Join(tags, " · ")
}

// FormatInventoryWalkText renders `compound system depends` and `compound
// system impact` in plain words. Ids are masked to their last four characters.
func FormatInventoryWalkText(p InventoryWalkProjection) string {
	what := "Depends"
	if p.impact || p.SchemaVersion == InventoryImpactSchemaVersion {
		what = "Impact"
	}
	lines := inventorySnapshotLine(p.Snapshot, what)
	if p.Snapshot.State == "missing" {
		return strings.Join(lines, "\n") + "\n"
	}
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	switch p.Status {
	case InventoryWalkAmbiguous:
		add("%s:", inventoryClean(p.Notice, 160))
		for _, c := range p.Candidates {
			add("  %s", inventoryNodeName(c.Label, c.Resource, c.Kind, c.Unknown))
		}
		add("Ask again with the masked id (for example `…%s`) or the full id from --json.", strings.TrimPrefix(MaskInventoryID(p.Candidates[0].Resource), "…"))
		return strings.Join(lines, "\n") + "\n"
	case InventoryWalkNotFound:
		add("%s.", inventoryClean(p.Notice, 160))
		return strings.Join(lines, "\n") + "\n"
	}
	start := inventoryNodeName(p.Start.Label, p.Start.Resource, p.Start.Kind, p.Start.Unknown)
	if what == "Impact" {
		add("What depends on %s: %d things, %d of them products.", start, len(p.Steps), p.Products)
		for _, s := range p.Steps {
			add("  %s · %s %s%s", inventoryNodeName(s.Label, s.Resource, s.Kind, s.Unknown),
				inventoryClean(s.RelTitle, inventoryTextLabelMax), inventoryResourceName(s.ViaLabel, s.Via), inventoryStepTags(s))
		}
	} else {
		how := "depends links and parent"
		if p.RoutesFollowed {
			how += ", and route links from a hostname"
		}
		add("What %s cannot work without: %d things, up to %d links deep (%s).", start, len(p.Steps), p.DepthLimit, how)
		children := map[string][]InventoryGraphStep{}
		for _, s := range p.Steps {
			children[s.Via] = append(children[s.Via], s)
		}
		var tree func(id string, indent int)
		tree = func(id string, indent int) {
			for _, s := range children[id] {
				add("%s%s %s%s", strings.Repeat("  ", indent), inventoryClean(s.RelTitle, inventoryTextLabelMax),
					inventoryNodeName(s.Label, s.Resource, s.Kind, s.Unknown), inventoryStepTags(s))
				tree(s.Resource, indent+1)
			}
		}
		tree(p.Start.Resource, 1)
		if len(p.RestsOn) == 0 {
			add("Rests on: no account, identity or secret store is linked.")
		} else {
			add("Rests on (accounts, identities, secret stores and their routes):")
			for _, s := range p.RestsOn {
				add("  %s · %s, from %s%s", inventoryNodeName(s.Label, s.Resource, s.Kind, s.Unknown),
					inventoryClean(s.RelTitle, inventoryTextLabelMax), inventoryResourceName(s.ViaLabel, s.Via), inventoryStepTags(s))
			}
		}
	}
	if len(p.NotKnown) == 0 {
		add("Not known: nothing on this walk.")
	} else {
		add("Not known (%d):", len(p.NotKnown))
		for _, n := range p.NotKnown {
			add("  %s: %s", inventoryResourceName(n.Label, n.Resource), inventoryClean(n.Why, 120))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
