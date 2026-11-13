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

// inventoryPlural is "1 thing" or "N things".
func inventoryPlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
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
	notKnown := fmt.Sprintf("Not known: %s, %s a link names but no source lists", inventoryPlural(products, "unknown product", "unknown products"),
		inventoryPlural(outside, "thing", "things"))
	if p.UnknownRelations > 0 {
		notKnown += ", " + inventoryPlural(p.UnknownRelations, "link", "links") + " with a relation the kinds do not know"
	}
	add("%s.", notKnown)
	for i, u := range p.Unresolved {
		if i == inventoryGraphTextUnresolvedMax {
			add("  ... and %d more; --json lists them", len(p.Unresolved)-i)
			break
		}
		add("  %s, named by %s", inventoryNodeName(u.Label, u.Ref, "", u.Kind), inventoryPlural(u.Links, "link", "links"))
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

// inventoryTextFold is how many rows a folded group of a depends or impact
// answer shows before "... and N more"; --all unfolds every group.
const inventoryTextFold = 6

// inventoryTextRestsOnFold is how many lines the "rests on" tree shows folded.
const inventoryTextRestsOnFold = 10

// inventoryMachineKinds are the kinds whose own needs a depends answer folds:
// a host or a guest also serves many things besides the one asked about.
var inventoryMachineKinds = map[string]bool{"host": true, "guest": true}

// FormatInventoryWalkText renders `compound system depends` and `compound
// system impact` in plain words, long groups folded. Ids are masked to their
// last four characters.
func FormatInventoryWalkText(p InventoryWalkProjection) string {
	return FormatInventoryWalkTextWith(p, InventoryTextOptions{})
}

// FormatInventoryWalkTextWith is FormatInventoryWalkText; All unfolds every
// group and lists what lies beneath each machine.
func FormatInventoryWalkTextWith(p InventoryWalkProjection, opts InventoryTextOptions) string {
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
		tails := map[string]bool{}
		for _, c := range p.Candidates {
			add("  %s", inventoryNodeName(c.Label, c.Resource, c.Kind, c.Unknown))
			tails[MaskInventoryID(c.Resource)] = true
		}
		if len(tails) == len(p.Candidates) {
			add("Ask again with the masked id (for example `%s`) or the full id from --json.", MaskInventoryID(p.Candidates[0].Resource))
		} else {
			add("Their masked ids are alike: ask again with the full id from --json.")
		}
		return strings.Join(lines, "\n") + "\n"
	case InventoryWalkNotFound:
		add("%s.", inventoryClean(p.Notice, 160))
		return strings.Join(lines, "\n") + "\n"
	}
	w := &inventoryWalkText{p: p, all: opts.All}
	if what == "Impact" {
		w.impact()
	} else {
		w.depends()
	}
	w.notKnown()
	w.seen()
	return strings.Join(append(lines, w.lines...), "\n") + "\n"
}

// inventoryWalkText builds the body of a depends or impact answer.
type inventoryWalkText struct {
	p     InventoryWalkProjection
	all   bool
	lines []string
}

func (w *inventoryWalkText) add(format string, args ...any) {
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

// group adds rows under a heading with their count, at most inventoryTextFold
// unless all, and says how many more there are and how to list them.
func (w *inventoryWalkText) group(heading string, rows []string) {
	if len(rows) == 0 {
		return
	}
	w.add("%s (%d):", heading, len(rows))
	for i, r := range rows {
		if !w.all && i == inventoryTextFold {
			w.add("  ... and %d more (--all lists them)", len(rows)-i)
			return
		}
		w.add("  %s", r)
	}
}

func inventoryStepName(s InventoryGraphStep) string {
	name := inventoryNodeName(s.Label, s.Resource, s.Kind, s.Unknown)
	if s.StoreKind != "" {
		name = strings.TrimSuffix(name, ")") + ": " + s.StoreKind + ")"
	}
	return name
}

// how reads a step from the thing that reached it: "<rel> <name>" when the
// start reached it, "<name> · <via> <rel> it" deeper. Tags mark a link or path
// that is not observed, warnings, and a carried thing.
func (w *inventoryWalkText) how(s InventoryGraphStep) string {
	rel := inventoryClean(s.RelTitle, inventoryTextLabelMax)
	if s.viaIsTarget {
		return inventoryStepName(s) + " · " + rel + " " + inventoryResourceName(s.ViaLabel, s.Via) + inventoryStepTags(s)
	}
	if s.Via == "" || (w.p.Start != nil && s.Via == w.p.Start.Resource) {
		return rel + " " + inventoryStepName(s) + inventoryStepTags(s)
	}
	return inventoryStepName(s) + " · " + inventoryResourceName(s.ViaLabel, s.Via) + " " + rel + " it" + inventoryStepTags(s)
}

// dependsGroup is the heading a depends step is listed under.
func dependsGroup(s InventoryGraphStep) string {
	switch s.Kind {
	case "datastore", "backup_target":
		return "data"
	case "external_service":
		return "outside"
	case "product", "hostname":
		return "own"
	case "host", "guest", "container", "tunnel", "deployment", "store_app", "site":
		return "parts"
	case "service":
		if s.Rel == "deployed_on" || s.Rel == "runs_on" {
			return "parts"
		}
		return "own"
	}
	return "other"
}

func (w *inventoryWalkText) depends() {
	p := w.p
	how := "depends links and parent"
	if p.RoutesFollowed {
		how += ", and route links from a hostname"
	}
	byID := map[string]InventoryGraphStep{}
	for _, s := range p.Steps {
		byID[s.Resource] = s
	}
	// What lies beneath a machine is folded into the machine's line: the
	// machine it runs on is named, the rest counted (--all lists them).
	beneath, onChain := map[string][]InventoryGraphStep{}, map[string]string{}
	if !w.all {
		for _, s := range p.Steps {
			top := ""
			for via := s.Via; via != "" && via != p.Start.Resource; {
				v, ok := byID[via]
				if !ok {
					break
				}
				if inventoryMachineKinds[v.Kind] {
					top = via
				}
				via = v.Via
			}
			if top == "" {
				continue
			}
			beneath[top] = append(beneath[top], s)
			if inventoryMachineKinds[s.Kind] && s.Rel == "runs_on" && (s.Via == top || onChain[s.Via] == top) {
				onChain[s.Resource] = top
			}
		}
	}
	w.add("What %s cannot work without: %s, up to %s deep (%s).", inventoryStepName(*p.Start),
		inventoryPlural(len(p.Steps), "thing", "things"), inventoryPlural(p.DepthLimit, "link", "links"), how)

	var code []string
	for _, s := range p.Code {
		row := inventoryStepName(s) + inventoryStepTags(s)
		if s.Via != p.Start.Resource {
			row += " · code of " + inventoryResourceName(s.ViaLabel, s.Via)
		}
		code = append(code, row)
	}
	w.group("Code", code)

	groups := map[string][]string{}
	var outside []string
	for _, s := range p.Steps {
		if _, under := onChain[s.Resource]; under || w.isBeneath(beneath, s) {
			continue
		}
		row := w.how(s)
		if list := beneath[s.Resource]; len(list) > 0 {
			row += w.machineTail(s.Resource, list, onChain)
		}
		group := dependsGroup(s)
		if inventoryAccessKinds[s.Kind] {
			continue // listed under what it rests on
		}
		if group == "outside" && !w.all {
			name := inventoryClean(s.Label, 40)
			if s.LinkConfidence != InventoryConfidenceObserved && s.LinkConfidence != "" {
				name += " (" + s.LinkConfidence + ")"
			}
			outside = append(outside, name)
			continue
		}
		groups[group] = append(groups[group], row)
	}
	w.group("Parts and where they run", groups["parts"])

	var addrs []string
	leadsTo := map[string][]InventoryGraphStep{}
	isAddr := map[string]bool{}
	for _, s := range p.Addresses {
		isAddr[s.Resource] = s.Kind == "hostname" || s.Kind == "domain"
	}
	for _, s := range p.Addresses {
		if !isAddr[s.Resource] && isAddr[s.Via] {
			leadsTo[s.Via] = append(leadsTo[s.Via], s)
		}
	}
	for _, s := range p.Addresses {
		if !isAddr[s.Resource] && isAddr[s.Via] {
			continue
		}
		row := w.how(s)
		for _, t := range leadsTo[s.Resource] {
			row += " → " + inventoryClean(t.RelTitle, inventoryTextLabelMax) + " " + inventoryStepName(t) + inventoryStepTags(t)
		}
		addrs = append(addrs, row)
	}
	w.group("Addresses that serve it", addrs)

	w.group("Needs: data stores", groups["data"])
	w.group("Needs: own services and other products", groups["own"])
	if len(outside) > 0 {
		w.outsideLine(outside)
	}
	w.group("Needs: outside services", groups["outside"])
	w.group("Needs: other", groups["other"])
	w.restsOn()
}

// isBeneath says whether s is folded under some machine.
func (w *inventoryWalkText) isBeneath(beneath map[string][]InventoryGraphStep, s InventoryGraphStep) bool {
	for _, list := range beneath {
		for _, b := range list {
			if b.Resource == s.Resource {
				return true
			}
		}
	}
	return false
}

// machineTail names the machine a machine runs on and counts the rest of what
// lies beneath it, by kind.
func (w *inventoryWalkText) machineTail(top string, list []InventoryGraphStep, onChain map[string]string) string {
	tail := ""
	counts := map[string]int{}
	for _, s := range list {
		if onChain[s.Resource] == top {
			tail += " → " + inventoryClean(s.RelTitle, inventoryTextLabelMax) + " " + inventoryStepName(s) + inventoryStepTags(s)
			continue
		}
		counts[s.Kind]++
	}
	if n := len(list) - strings.Count(tail, " → "); n > 0 {
		tail += fmt.Sprintf("; beneath it %s (%s; --all lists them)", inventoryPlural(n, "more thing", "more things"), inventoryOrderedCounts(counts))
	}
	return tail
}

// outsideLine lists outside services by name on at most two lines.
func (w *inventoryWalkText) outsideLine(names []string) {
	w.add("Needs: outside services (%d):", len(names))
	const width = 110
	line, rows := "", 0
	for i, name := range names {
		if len(line)+len(name)+2 > width {
			rows++
			if rows == 2 {
				w.add("  %s ... and %d more (--all lists them)", strings.TrimSuffix(line, ","), len(names)-i)
				return
			}
			w.add("  %s", line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += name + ","
	}
	w.add("  %s", strings.TrimSuffix(line, ","))
}

// restsOn renders accounts, sign-in identities and where their secrets are
// kept as a tree: each root says what it came from, each child how it hangs
// on its parent.
func (w *inventoryWalkText) restsOn() {
	p := w.p
	if len(p.RestsOn) == 0 {
		w.add("Rests on: no account, sign-in identity or secret store is linked.")
		return
	}
	in := map[string]bool{}
	children := map[string][]InventoryGraphStep{}
	for _, s := range p.RestsOn {
		in[s.Resource] = true
	}
	var roots []InventoryGraphStep
	for _, s := range p.RestsOn {
		if in[s.Via] {
			children[s.Via] = append(children[s.Via], s)
		} else {
			roots = append(roots, s)
		}
	}
	var rows []string
	var tree func(id string, indent int)
	tree = func(id string, indent int) {
		for _, c := range children[id] {
			rows = append(rows, strings.Repeat("  ", indent)+inventoryClean(c.RelTitle, inventoryTextLabelMax)+" "+inventoryStepName(c)+inventoryStepTags(c))
			tree(c.Resource, indent+1)
		}
	}
	for _, r := range roots {
		rows = append(rows, w.how(r))
		tree(r.Resource, 1)
	}
	w.add("Rests on (%d): accounts, sign-in identities, and where their secrets are kept (store kind and entry name only):", len(p.RestsOn))
	for i, r := range rows {
		if !w.all && i == inventoryTextRestsOnFold {
			w.add("  ... and %d more lines (--all lists them)", len(rows)-i)
			return
		}
		w.add("  %s", r)
	}
}

func (w *inventoryWalkText) impact() {
	p := w.p
	w.add("What depends on %s: %s, %d of them %s.", inventoryStepName(*p.Start), inventoryPlural(len(p.Steps), "thing", "things"), p.Products,
		map[bool]string{true: "a product", false: "products"}[p.Products == 1])
	var order []string
	rows := map[string][]string{}
	for _, s := range p.Steps {
		kind := s.Kind
		if kind == "" {
			kind = "thing"
		}
		if _, seen := rows[kind]; !seen {
			order = append(order, kind)
		}
		rows[kind] = append(rows[kind], inventoryStepName(s)+" · "+inventoryClean(s.RelTitle, inventoryTextLabelMax)+" "+
			inventoryResourceName(s.ViaLabel, s.Via)+inventoryStepTags(s))
	}
	for _, kind := range order {
		w.group(strings.ReplaceAll(kind, "_", " "), rows[kind])
	}
}

func (w *inventoryWalkText) notKnown() {
	var rows []string
	for _, n := range w.p.NotKnown {
		rows = append(rows, inventoryResourceName(n.Label, n.Resource)+": "+inventoryClean(n.Why, 120))
	}
	if len(rows) == 0 {
		w.add("Not known: nothing on this walk.")
		return
	}
	w.group("Not known", rows)
}

// seen says when the information was last seen: the snapshot, second-hand
// evidence older than it, and things carried from a source's last good read.
func (w *inventoryWalkText) seen() {
	p := w.p
	all := append(append(append(append([]InventoryGraphStep{}, p.Steps...), p.Code...), p.Addresses...), p.RestsOn...)
	if p.Start != nil {
		all = append(all, *p.Start)
	}
	older, oldest, carried, oldestCarried := 0, "", 0, ""
	for _, s := range all {
		if s.ObservedAt != "" && s.ObservedAt < p.Snapshot.ObservedAt {
			older++
			if oldest == "" || s.ObservedAt < oldest {
				oldest = s.ObservedAt
			}
		}
		if s.Carried {
			carried++
			if oldestCarried == "" || (s.LastObservedAt != "" && s.LastObservedAt < oldestCarried) {
				oldestCarried = s.LastObservedAt
			}
		}
	}
	line := "Seen: the snapshot was observed " + inventoryShortTime(p.Snapshot.ObservedAt)
	if older > 0 {
		line += fmt.Sprintf("; %s from second-hand evidence, the oldest seen %s", inventoryPlural(older, "thing comes", "things come"), inventoryShortTime(oldest))
	}
	if carried > 0 {
		line += fmt.Sprintf("; %s from a source's last good read (oldest %s)", inventoryPlural(carried, "thing is", "things are"), inventoryShortTime(oldestCarried))
	}
	w.add("%s.", line)
	w.add("Marks: \"link inferred\", \"link declared\" and \"path …\" are not observed; --json has every step with its evidence.")
}
