package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/candidates"
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

func targetStateSet(states []string) map[string]bool {
	out := map[string]bool{}
	for _, state := range states {
		state = strings.TrimSpace(state)
		if state != "" {
			out[state] = true
		}
	}
	if len(out) == 0 {
		out["parked"] = true
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
