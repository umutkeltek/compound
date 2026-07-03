package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
)

const (
	maxDispatchPromptOffers          = 5
	dispatchWorkerPromptContractRef  = "contract://dispatch-worker-prompt"
	dispatchWorkerPromptContractNote = "dispatch worker prompt is derived from an already-admitted WorkEnvelope plus task-scoped CapabilityGraph offers; no new EffectiveContract is compiled for this capability-only prompt"
)

type dispatchPrompt struct {
	ContextPackID       string
	CapabilitiesOffered []DispatchCapabilityOffer
	Markdown            string
}

func (s *Store) attachDispatchPrompt(workID, actor string, now time.Time) (dispatchPrompt, error) {
	w, err := s.Get(workID)
	if err != nil {
		return dispatchPrompt{}, err
	}
	pack, err := s.dispatchContextPack(w, actor, now)
	if err != nil {
		return dispatchPrompt{}, err
	}
	if err := pack.Validate(); err != nil {
		return dispatchPrompt{}, fmt.Errorf("dispatch context pack invalid: %w", err)
	}
	packPath := filepath.Join(s.home, "context", "packs", pack.ID+".json")
	if err := os.MkdirAll(filepath.Dir(packPath), 0o755); err != nil {
		return dispatchPrompt{}, err
	}
	if err := s.writeJSON(packPath, &pack); err != nil {
		return dispatchPrompt{}, err
	}
	w.IDs.ContextPackID = pack.ID
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		return dispatchPrompt{}, err
	}
	offers := dispatchPromptOffers(pack)
	return dispatchPrompt{
		ContextPackID:       pack.ID,
		CapabilitiesOffered: offers,
		Markdown:            renderDispatchPrompt(w, offers),
	}, nil
}

func (s *Store) dispatchContextPack(w envelope.WorkEnvelope, actor string, now time.Time) (envelope.ContextPack, error) {
	task := strings.TrimSpace(strings.Join(compactStrings(
		w.CanonicalSurface,
		w.DesiredOutcome,
		w.SuccessPredicate,
		strings.Join(w.NonGoals, " "),
	), " "))
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		cwd = s.home
	}
	pack := envelope.ContextPack{
		Header:             newHeader(envelope.SVContextPack, idFor("ctx_", "dispatch", w.ID, actor, task), now, w.ID, "", w.Privacy, "tier0"),
		SummaryMD:          fmt.Sprintf("Dispatch context for %s. Capability offers are task-scoped hints, not execution.", w.ID),
		Included:           []envelope.IncludedRef{},
		Excluded:           []envelope.ExcludedRef{{Ref: dispatchWorkerPromptContractRef, Reason: dispatchWorkerPromptContractNote}},
		CapabilityOffers:   []envelope.CapabilityOffer{},
		ActiveInstructions: []string{},
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		ActiveMemory:       []string{},
		SourceRevisions:    []envelope.SourceRev{{Source: "work://" + w.ID, Rev: dispatchWorkRevision(w)}},
		RequiredChecks:     []string{},
		Avoid:              []string{},
	}
	pack.Scope.Surface = cwd
	if !capgraph.Exists(s.home) {
		return pack, nil
	}
	graph, err := capgraph.Open(s.home)
	if err != nil {
		return envelope.ContextPack{}, err
	}
	surfaced, err := graph.SurfaceForHarness(task, orDefault(actor, "codex"))
	if err != nil {
		return envelope.ContextPack{}, err
	}
	if len(surfaced) > 12 {
		surfaced = surfaced[:12]
	}
	for _, hit := range surfaced {
		ref := "capability://" + hit.Node.ID
		reason := dispatchSurfaceReason(hit)
		pack.Included = append(pack.Included, envelope.IncludedRef{
			Ref:         ref,
			SourceClass: "runtime",
			Reason:      reason,
			Confidence:  1,
			Status:      "current",
		})
		pack.CapabilityOffers = append(pack.CapabilityOffers, envelope.CapabilityOffer{
			Ref:   ref,
			Kind:  hit.Node.Kind,
			Name:  hit.Node.Name,
			Score: hit.Score,
			Why:   reason,
		})
	}
	if len(surfaced) > 0 {
		rev, err := capgraph.AuthorityRevision(s.home)
		if err != nil {
			return envelope.ContextPack{}, err
		}
		pack.SourceRevisions = append(pack.SourceRevisions, envelope.SourceRev{Source: capgraph.CapabilityGraphAuthorityRef, Rev: rev})
	}
	return pack, nil
}

func dispatchWorkRevision(w envelope.WorkEnvelope) string {
	parts := []string{"state:" + w.State}
	if w.PolicyDecisionID != "" {
		parts = append(parts, "policy:"+w.PolicyDecisionID)
	}
	if w.IDs.ContextPackID != "" {
		parts = append(parts, "context:"+w.IDs.ContextPackID)
	}
	return strings.Join(parts, ";")
}

func dispatchSurfaceReason(hit capgraph.Surfaced) string {
	reason := fmt.Sprintf("capability_graph surface score=%.1f kind=%s why=%s", hit.Score, hit.Node.Kind, hit.Why)
	if len(hit.MatchedTokens) > 0 {
		reason += " matched=" + strings.Join(hit.MatchedTokens, ",")
	}
	return reason
}

func dispatchPromptOffers(pack envelope.ContextPack) []DispatchCapabilityOffer {
	out := []DispatchCapabilityOffer{}
	for _, offer := range pack.CapabilityOffers {
		if offer.Kind != "skill" {
			continue
		}
		out = append(out, dispatchCapabilityOffer(offer))
		if len(out) >= maxDispatchPromptOffers {
			return out
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, offer := range pack.CapabilityOffers {
		out = append(out, dispatchCapabilityOffer(offer))
		if len(out) >= maxDispatchPromptOffers {
			break
		}
	}
	return out
}

func dispatchCapabilityOffer(offer envelope.CapabilityOffer) DispatchCapabilityOffer {
	return DispatchCapabilityOffer{
		Ref:   offer.Ref,
		Kind:  offer.Kind,
		Name:  offer.Name,
		Score: offer.Score,
		Why:   offer.Why,
	}
}

func renderDispatchPrompt(w envelope.WorkEnvelope, offers []DispatchCapabilityOffer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Dispatch Work %s\n\n", w.ID)
	if strings.TrimSpace(w.CanonicalSurface) != "" {
		fmt.Fprintf(&b, "- surface: %s\n", w.CanonicalSurface)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## LOAD THESE SKILLS")
	fmt.Fprintln(&b)
	if len(offers) == 0 {
		fmt.Fprintln(&b, "- none surfaced")
	} else {
		for _, offer := range offers {
			fmt.Fprintf(&b, "- %s (%s): %s\n", offer.Ref, offer.Name, firstNonEmpty(offer.Why, "surfaced by capability graph"))
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## TASK")
	fmt.Fprintln(&b)
	if strings.TrimSpace(w.DesiredOutcome) != "" {
		fmt.Fprintln(&b, w.DesiredOutcome)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## SUCCESS")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, w.SuccessPredicate)
	if len(w.NonGoals) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## NON-GOALS")
		fmt.Fprintln(&b)
		for _, item := range w.NonGoals {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	return strings.TrimSpace(b.String())
}
