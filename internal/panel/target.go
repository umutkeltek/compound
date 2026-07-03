package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/mem"
)

type TargetInput struct {
	Kind       string          `json:"kind"`
	Ref        string          `json:"ref"`
	Visibility string          `json:"visibility"`
	Summary    string          `json:"summary"`
	Payload    json.RawMessage `json:"payload"`
}

func LoadMemoryCandidateTargets(homePath string, states []string, limit int) ([]TargetInput, error) {
	store, err := mem.Open(homePath)
	if err != nil {
		return nil, err
	}
	stateSet := targetStateSet(states)
	cands, err := store.AllCandidates()
	if err != nil {
		return nil, err
	}
	out := []TargetInput{}
	for _, cand := range cands {
		if !stateSet[cand.State] {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(homePath, candidates.InboxDir(candidates.Memory), cand.ID+".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, TargetInput{
			Kind:       "memory_candidate",
			Ref:        cand.ID,
			Visibility: cand.Visibility,
			Summary:    memoryCandidateSummary(cand),
			Payload:    json.RawMessage(append([]byte(nil), raw...)),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func LoadSkillCandidateTargets(homePath string, limit int) ([]TargetInput, error) {
	dir := filepath.Join(homePath, "reviews", "skill-candidates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := []TargetInput{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".decision.json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var review capgraph.SkillCandidateReview
		if err := json.Unmarshal(raw, &review); err != nil {
			return nil, fmt.Errorf("parse skill candidate review %s: %w", path, err)
		}
		ref := strings.TrimSuffix(entry.Name(), ".json")
		if strings.TrimSpace(review.ID) != "" {
			ref = review.ID
		}
		if hasSkillCandidateDecisionSidecar(dir, ref) {
			continue
		}
		out = append(out, TargetInput{
			Kind:       "skill_candidate",
			Ref:        ref,
			Visibility: "internal",
			Summary:    skillCandidateSummary(review),
			Payload:    json.RawMessage(append([]byte(nil), raw...)),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func LoadCapabilityObservationTargets(homePath string, limit int) ([]TargetInput, error) {
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := []TargetInput{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		var obs capabilityObservationTarget
		if err := candidates.ReadCandidate(homePath, candidates.CapabilityObs, id, &obs); err != nil {
			return nil, err
		}
		if !capabilityObservationAwaitingVerdict(obs) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, TargetInput{
			Kind:       "capability_observation",
			Ref:        id,
			Visibility: "internal",
			Summary:    capabilityObservationSummary(obs),
			Payload:    json.RawMessage(append([]byte(nil), raw...)),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func LoadTargetInput(homePath, targetKind, ref string) (TargetInput, error) {
	switch targetKind {
	case "memory_candidate":
		raw, err := os.ReadFile(filepath.Join(homePath, candidates.InboxDir(candidates.Memory), ref+".json"))
		if err != nil {
			return TargetInput{}, err
		}
		var cand mem.Candidate
		if err := json.Unmarshal(raw, &cand); err != nil {
			return TargetInput{}, err
		}
		return TargetInput{Kind: "memory_candidate", Ref: cand.ID, Visibility: cand.Visibility, Summary: memoryCandidateSummary(cand), Payload: json.RawMessage(append([]byte(nil), raw...))}, nil
	case "skill_candidate":
		path := filepath.Join(homePath, "reviews", "skill-candidates", ref+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return TargetInput{}, err
		}
		var review capgraph.SkillCandidateReview
		if err := json.Unmarshal(raw, &review); err != nil {
			return TargetInput{}, fmt.Errorf("parse skill candidate review %s: %w", path, err)
		}
		targetRef := ref
		if strings.TrimSpace(review.ID) != "" {
			targetRef = review.ID
		}
		return TargetInput{Kind: "skill_candidate", Ref: targetRef, Visibility: "internal", Summary: skillCandidateSummary(review), Payload: json.RawMessage(append([]byte(nil), raw...))}, nil
	case "capability_observation":
		var obs capabilityObservationTarget
		if err := candidates.ReadCandidate(homePath, candidates.CapabilityObs, ref, &obs); err != nil {
			return TargetInput{}, err
		}
		raw, err := os.ReadFile(filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs), ref+".json"))
		if err != nil {
			return TargetInput{}, err
		}
		return TargetInput{Kind: "capability_observation", Ref: ref, Visibility: "internal", Summary: capabilityObservationSummary(obs), Payload: json.RawMessage(append([]byte(nil), raw...))}, nil
	case "comparison":
		return loadComparisonTarget(homePath, ref)
	default:
		return TargetInput{}, fmt.Errorf("unsupported target kind %q", targetKind)
	}
}

func targetStateSet(states []string) map[string]bool {
	out := map[string]bool{}
	for _, state := range states {
		state = strings.TrimSpace(state)
		if state != "" {
			out[state] = true
		}
	}
	if len(out) == 0 {
		out["proposed"] = true
	}
	return out
}

func memoryCandidateSummary(c mem.Candidate) string {
	basis := c.PromotionBasis
	if basis == "" {
		basis = "none"
	}
	return fmt.Sprintf("%s %s %s (basis=%s confidence=%.2f)", c.Entity, c.Predicate, c.Object, basis, c.Confidence)
}

type capabilityObservationTarget struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Subject      string   `json:"subject"`
	Statement    string   `json:"statement"`
	Detail       string   `json:"detail"`
	Confidence   float64  `json:"confidence"`
	State        string   `json:"state,omitempty"`
	Verdict      string   `json:"verdict,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

func hasSkillCandidateDecisionSidecar(dir, reviewID string) bool {
	matches, err := filepath.Glob(filepath.Join(dir, reviewID+".*.decision.json"))
	return err == nil && len(matches) > 0
}

func skillCandidateSummary(review capgraph.SkillCandidateReview) string {
	name := firstNonEmptyString(review.External.Name, review.External.Slug, review.External.ID, review.CandidateID, review.ID)
	overlaps := make([]string, 0, len(review.OverlapRefs))
	for _, overlap := range review.OverlapRefs {
		ref := firstNonEmptyString(overlap.NodeID, overlap.Name)
		if ref != "" {
			overlaps = append(overlaps, ref)
		}
	}
	overlapText := "none"
	if len(overlaps) > 0 {
		overlapText = strings.Join(overlaps, ",")
	}
	return fmt.Sprintf("%s classification=%s disposition=%s overlaps=%s", name, emptyDashString(review.ProposedClassification), emptyDashString(review.RecommendedDisposition), overlapText)
}

func capabilityObservationSummary(obs capabilityObservationTarget) string {
	return fmt.Sprintf("%s %s %s (confidence=%.2f)", obs.Subject, obs.Detail, obs.Statement, obs.Confidence)
}

func capabilityObservationAwaitingVerdict(obs capabilityObservationTarget) bool {
	if strings.TrimSpace(obs.Verdict) != "" {
		return false
	}
	switch strings.TrimSpace(obs.State) {
	case "", "pending", "proposed", "awaiting_verdict", "needs_review":
		return true
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func emptyDashString(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
