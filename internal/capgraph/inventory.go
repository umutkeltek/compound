package capgraph

import (
	"fmt"
	"sort"
	"strings"
)

// CapabilityInventory is the per-node companion to Reachability. Reachability
// answers "how bad is it, and which door do I build next"; the inventory answers
// "and which specific ones", which is what an operator actually browses.
//
// They are separate types because they fail differently: a rollup can be honest
// while individual rows are wrong, and a row can be right while the rollup is
// mis-ordered. Deriving both from one join keeps them from disagreeing.
type CapabilityInventory struct {
	SchemaVersion string                     `json:"schema_version"`
	States        map[string]CapabilityState `json:"states"`
	Order         []string                   `json:"order"`
}

// CapabilityState is what is known about one capability beyond its existence.
type CapabilityState struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Reachable means a connection surface answers "how do I get to it".
	Reachable bool `json:"reachable"`
	// Unlockable means a credential handle answers "and what authenticates me".
	Unlockable bool `json:"unlockable"`
	// Harnesses lists the executors this capability was explicitly scoped to.
	// Empty means generic: offered to EVERY harness, runnable or not.
	Harnesses []string `json:"harnesses,omitempty"`
	// FalseTriggered / Missed count recorded routing corrections against it.
	FalseTriggered int  `json:"false_triggered,omitempty"`
	Missed         int  `json:"missed,omitempty"`
	Retired        bool `json:"retired,omitempty"`
}

// CapabilityInventory builds the per-node view over the same joins Reachability
// uses.
func (s *Store) CapabilityInventory() (CapabilityInventory, error) {
	nodes, err := s.loadNodes()
	if err != nil {
		return CapabilityInventory{}, err
	}
	surfaces, err := s.loadConnectionSurfaces()
	if err != nil {
		return CapabilityInventory{}, err
	}
	edges, err := s.loadEdges()
	if err != nil {
		return CapabilityInventory{}, err
	}
	bySurface := make(map[string]ConnectionSurface, len(surfaces))
	for _, cs := range surfaces {
		bySurface[cs.NodeID] = cs
	}

	inv := CapabilityInventory{
		SchemaVersion: "compound.capability_inventory.v0.1",
		States:        make(map[string]CapabilityState, len(nodes)),
	}
	for _, n := range nodes {
		st := CapabilityState{ID: n.ID, Kind: n.Kind, Name: n.Name, Retired: n.Retired}
		if cs, ok := bySurface[n.ID]; ok {
			// Same rule as the rollup: a surface with neither endpoint nor
			// config path answers nothing, so it is not reachability.
			if strings.TrimSpace(cs.Endpoint) != "" || len(cs.ConfigPaths) > 0 {
				st.Reachable = true
			}
			st.Unlockable = strings.TrimSpace(cs.CredentialHandle.Ref) != ""
		}
		inv.States[n.ID] = st
		inv.Order = append(inv.Order, n.ID)
	}
	for _, e := range edges {
		switch e.Kind {
		case EdSurfacesWith:
			if !strings.HasPrefix(e.To, "harness:") {
				continue
			}
			if st, ok := inv.States[e.From]; ok && !containsString(st.Harnesses, e.To) {
				st.Harnesses = append(st.Harnesses, e.To)
				sort.Strings(st.Harnesses)
				inv.States[e.From] = st
			}
		case EdFalseTriggeredBy, EdMissedWhen:
			// Situation -> capability, so the capability is the TO side.
			if st, ok := inv.States[e.To]; ok {
				if e.Kind == EdFalseTriggeredBy {
					st.FalseTriggered++
				} else {
					st.Missed++
				}
				inv.States[e.To] = st
			}
		}
	}
	return inv, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// State returns the known state for a node id (zero value when absent).
func (inv CapabilityInventory) State(id string) CapabilityState {
	return inv.States[id]
}

// Visible applies the operator's filters. Harness matching deliberately keeps
// GENERIC capabilities: an unscoped node really is offered to that harness, and
// hiding it would make the list disagree with what the executor is handed.
func (st CapabilityState) Visible(harness, kind string, onlyUnreachable, onlyCorrected, showRetired bool) bool {
	if st.Retired && !showRetired {
		return false
	}
	if kind != "" && st.Kind != kind {
		return false
	}
	if harness != "" && len(st.Harnesses) > 0 {
		want := harness
		if !strings.HasPrefix(want, "harness:") {
			want = "harness:" + want
		}
		if !containsString(st.Harnesses, want) {
			return false
		}
	}
	if onlyUnreachable && st.Reachable {
		return false
	}
	if onlyCorrected && st.FalseTriggered == 0 && st.Missed == 0 {
		return false
	}
	return true
}

// Filter returns the subset matching the operator's filters, preserving order.
func (inv CapabilityInventory) Filter(harness, kind string, onlyUnreachable, onlyCorrected, showRetired bool) []CapabilityState {
	out := []CapabilityState{}
	for _, id := range inv.Order {
		st := inv.States[id]
		if st.Visible(harness, kind, onlyUnreachable, onlyCorrected, showRetired) {
			out = append(out, st)
		}
	}
	return out
}

// Flags renders the state as a fixed-width glyph column. Fixed width matters:
// an operator scans this vertically, and a ragged column defeats that.
func (st CapabilityState) Flags() string {
	glyph := func(on bool, c string) string {
		if on {
			return c
		}
		return "-"
	}
	return glyph(st.Reachable, "R") + glyph(st.Unlockable, "K") +
		glyph(len(st.Harnesses) > 0, "S") + glyph(st.FalseTriggered+st.Missed > 0, "!")
}

// Note appends the detail worth reading inline: which harnesses, and how many
// corrections. Silent when there is nothing to say, so the common row stays thin.
func (st CapabilityState) Note() string {
	var parts []string
	if len(st.Harnesses) > 0 {
		trimmed := make([]string, 0, len(st.Harnesses))
		for _, h := range st.Harnesses {
			trimmed = append(trimmed, strings.TrimPrefix(h, "harness:"))
		}
		parts = append(parts, strings.Join(trimmed, "+"))
	} else {
		parts = append(parts, "generic")
	}
	if n := st.FalseTriggered + st.Missed; n > 0 {
		parts = append(parts, fmt.Sprintf("%d correction(s)", n))
	}
	if st.Retired {
		parts = append(parts, "retired")
	}
	return "  [" + strings.Join(parts, ", ") + "]"
}
