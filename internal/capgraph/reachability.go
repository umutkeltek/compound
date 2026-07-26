package capgraph

import (
	"sort"
	"strings"
)

// ReachabilitySchemaVersion tags the reachability projection.
const ReachabilitySchemaVersion = "compound.capability_reachability.v0.1"

// Reachability answers a question the capability graph could not previously be
// asked: of everything Compound can NAME, how much can an agent actually USE?
//
// The graph is good at existence and bad at access. A node says a capability
// exists; a ConnectionSurface says how to reach it; a CredentialHandle says what
// unlocks it. Only the first is populated at scale, which is why a harness that
// is told a tool exists still ends up reading a wrapper script to work out how
// to invoke it -- the served answer stopped one field short of useful, and
// improvisation is what fills that gap.
//
// This projection exists so that shortfall is a number in a doctor rather than a
// recurring surprise in a session. It reads; it never writes.
type Reachability struct {
	SchemaVersion string             `json:"schema_version"`
	Kinds         []KindReachability `json:"kinds"`
	Total         KindReachability   `json:"total"`
	Gaps          []ReachabilityGap  `json:"gaps,omitempty"`
}

// KindReachability is the per-kind tally. Named counts rather than percentages
// so a caller can re-derive any ratio without trusting ours.
type KindReachability struct {
	Kind string `json:"kind"`
	// Nodes is how many capabilities of this kind Compound can name.
	Nodes int `json:"nodes"`
	// Reachable is how many carry a ConnectionSurface -- an answer to "how do I
	// get to it".
	Reachable int `json:"reachable"`
	// Unlockable is how many carry a credential handle -- an answer to "and what
	// authenticates me". A capability can be reachable without being unlockable,
	// which is the state that sends an agent looking for a key by hand.
	Unlockable int `json:"unlockable"`
	// ValueTransiting counts handles whose mechanism causes the secret to pass
	// through a process Compound started. Operators auditing exposure want this
	// small, and want it visible rather than inferred.
	ValueTransiting int `json:"value_transiting"`
}

// ReachabilityGap names a kind whose access facts are materially missing, worst
// first. It is deliberately a list of kinds rather than of nodes: the fix is
// always an import door for a whole kind, never a hand-written record per node.
// Anything else would make the map the work.
type ReachabilityGap struct {
	Kind        string `json:"kind"`
	Nodes       int    `json:"nodes"`
	Missing     int    `json:"missing"`
	Consequence string `json:"consequence"`
}

// ComputeReachability joins nodes against connection surfaces. A kind with zero
// nodes never appears; a kind with full coverage produces no gap.
func (s *Store) ComputeReachability() (Reachability, error) {
	nodes, err := s.loadNodes()
	if err != nil {
		return Reachability{}, err
	}
	surfaces, err := s.loadConnectionSurfaces()
	if err != nil {
		return Reachability{}, err
	}
	byNode := make(map[string]ConnectionSurface, len(surfaces))
	for _, cs := range surfaces {
		byNode[cs.NodeID] = cs
	}

	tally := map[string]*KindReachability{}
	for _, n := range nodes {
		// A retired capability is not a gap. Counting one would inflate the
		// shortfall and make the number the operator is meant to act on the
		// first thing they learn to discount.
		if n.Retired {
			continue
		}
		kind := strings.TrimSpace(n.Kind)
		if kind == "" {
			kind = "unknown"
		}
		k, ok := tally[kind]
		if !ok {
			k = &KindReachability{Kind: kind}
			tally[kind] = k
		}
		k.Nodes++
		cs, has := byNode[n.ID]
		if !has {
			continue
		}
		// A surface with no endpoint AND no config path answers nothing, so it
		// does not count as reachable. Counting a placeholder would report the
		// gap as closed while the agent still cannot act -- the failure mode
		// this projection exists to prevent.
		if strings.TrimSpace(cs.Endpoint) == "" && len(cs.ConfigPaths) == 0 {
			continue
		}
		k.Reachable++
		if strings.TrimSpace(cs.CredentialHandle.Ref) != "" {
			k.Unlockable++
			if transits, known := CredentialValueTransits(cs.CredentialHandle); known && transits {
				k.ValueTransiting++
			}
		}
	}

	out := Reachability{SchemaVersion: ReachabilitySchemaVersion}
	for _, k := range tally {
		out.Kinds = append(out.Kinds, *k)
		out.Total.Nodes += k.Nodes
		out.Total.Reachable += k.Reachable
		out.Total.Unlockable += k.Unlockable
		out.Total.ValueTransiting += k.ValueTransiting
	}
	out.Total.Kind = "total"
	// Worst coverage first, then largest, then name: the operator reads the top
	// of this list and nothing else, so ordering IS the recommendation.
	sort.Slice(out.Kinds, func(i, j int) bool {
		a, b := out.Kinds[i], out.Kinds[j]
		ac := coverageRatio(a.Reachable, a.Nodes)
		bc := coverageRatio(b.Reachable, b.Nodes)
		if ac != bc {
			return ac < bc
		}
		if a.Nodes != b.Nodes {
			return a.Nodes > b.Nodes
		}
		return a.Kind < b.Kind
	})
	for _, k := range out.Kinds {
		missing := k.Nodes - k.Reachable
		if missing == 0 {
			continue
		}
		out.Gaps = append(out.Gaps, ReachabilityGap{
			Kind: k.Kind, Nodes: k.Nodes, Missing: missing,
			Consequence: gapConsequence(k.Kind),
		})
	}
	return out, nil
}

func coverageRatio(have, total int) float64 {
	if total == 0 {
		return 1
	}
	return float64(have) / float64(total)
}

// gapConsequence states what the missing facts cost at the point of use. Kinds
// fail differently, and a generic "no connection surface" line teaches nobody
// which door to build next.
func gapConsequence(kind string) string {
	switch kind {
	case "tool":
		return "a harness is told the tool exists but not how to invoke it, so it infers a command or reads a wrapper script"
	case "mcp":
		return "a server is listed but carries no transport or launch facts, so a harness cannot connect to it from the served answer alone"
	case "skill":
		return "a skill is discoverable by description but its host prerequisites are not served, so it fires in a harness that cannot run it"
	case "adapter", "service", "gateway":
		return "an integration is named without an address, so reaching it depends on someone remembering where it lives"
	case "model_profile", "agent-lane", "harness":
		return "a lane is nameable but not launchable from served state, so lane selection falls back to hardcoded knowledge"
	default:
		return "the capability can be named but not acted on from served state alone"
	}
}
