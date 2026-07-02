package mem

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const SynthesisSweepSchemaVersion = "compound.synthesis_sweep.v0.1"

var synthesisSweepStages = []string{"cluster", "contradict"}

type SynthesisSweepReport struct {
	SchemaVersion              string                           `json:"schema_version"`
	GeneratedAt                string                           `json:"generated_at"`
	Stages                     []string                         `json:"stages"`
	CandidateCount             int                              `json:"candidate_count"`
	ClaimCount                 int                              `json:"claim_count"`
	ClusterCount               int                              `json:"cluster_count"`
	EchoFoldProposalCount      int                              `json:"echo_fold_proposal_count"`
	ContradictionProposalCount int                              `json:"contradiction_proposal_count"`
	Clusters                   []SynthesisCluster               `json:"clusters,omitempty"`
	EchoFoldProposals          []SynthesisEchoFoldProposal      `json:"echo_fold_proposals,omitempty"`
	ContradictionProposals     []SynthesisContradictionProposal `json:"contradiction_proposals,omitempty"`
}

type SynthesisCluster struct {
	ClusterKey     string   `json:"cluster_key"`
	CandidateCount int      `json:"candidate_count"`
	ObjectCount    int      `json:"object_count"`
	CandidateIDs   []string `json:"candidate_ids"`
}

type SynthesisEchoFoldProposal struct {
	ID               string   `json:"id"`
	ClusterKey       string   `json:"cluster_key"`
	NormalizedObject string   `json:"normalized_object"`
	CandidateIDs     []string `json:"candidate_ids"`
}

type SynthesisContradictionProposal struct {
	ID         string   `json:"id"`
	ClusterKey string   `json:"cluster_key"`
	ClaimIDs   []string `json:"claim_ids"`
	Objects    []string `json:"objects"`
}

func (s *Store) SynthesisSweep(stages []string, now time.Time) (SynthesisSweepReport, error) {
	stageList, stageSet, err := normalizeSynthesisStages(stages)
	if err != nil {
		return SynthesisSweepReport{}, err
	}
	report := SynthesisSweepReport{
		SchemaVersion: SynthesisSweepSchemaVersion,
		GeneratedAt:   rfc3339(now),
		Stages:        stageList,
	}
	if stageSet["cluster"] {
		candidates, err := s.AllCandidates()
		if err != nil {
			return report, err
		}
		report.CandidateCount = len(candidates)
		clusters, proposals, err := s.synthesisCandidateClusters(candidates)
		if err != nil {
			return report, err
		}
		report.Clusters = clusters
		report.EchoFoldProposals = proposals
		report.ClusterCount = len(clusters)
		report.EchoFoldProposalCount = len(proposals)
	}
	if stageSet["contradict"] {
		claims, err := s.loadClaims()
		if err != nil {
			return report, err
		}
		report.ClaimCount = len(claims)
		proposals, err := s.synthesisContradictions(claims)
		if err != nil {
			return report, err
		}
		report.ContradictionProposals = proposals
		report.ContradictionProposalCount = len(proposals)
	}
	return report, nil
}

func normalizeSynthesisStages(stages []string) ([]string, map[string]bool, error) {
	if len(stages) == 0 {
		stages = synthesisSweepStages
	}
	allowed := map[string]bool{}
	for _, stage := range synthesisSweepStages {
		allowed[stage] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range stages {
		for _, part := range strings.Split(raw, ",") {
			stage := strings.ToLower(strings.TrimSpace(part))
			if stage == "" {
				continue
			}
			if !allowed[stage] {
				return nil, nil, fmt.Errorf("unknown synthesis stage %q (valid: %s)", stage, strings.Join(synthesisSweepStages, ","))
			}
			if !seen[stage] {
				seen[stage] = true
				out = append(out, stage)
			}
		}
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("at least one synthesis stage is required")
	}
	return out, seen, nil
}

func (s *Store) synthesisCandidateClusters(candidates []Candidate) ([]SynthesisCluster, []SynthesisEchoFoldProposal, error) {
	groups := map[string][]Candidate{}
	for _, c := range candidates {
		key, err := s.candidateClusterKey(c)
		if err != nil {
			return nil, nil, err
		}
		groups[key] = append(groups[key], c)
	}
	keys := sortedKeys(groups)
	clusters := make([]SynthesisCluster, 0, len(keys))
	var proposals []SynthesisEchoFoldProposal
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		objectGroups := map[string][]string{}
		for _, c := range group {
			objectGroups[NormalizeClaimValue(c.Object)] = append(objectGroups[NormalizeClaimValue(c.Object)], c.ID)
		}
		objects := sortedKeys(objectGroups)
		ids := make([]string, 0, len(group))
		for _, c := range group {
			ids = append(ids, c.ID)
		}
		clusters = append(clusters, SynthesisCluster{ClusterKey: key, CandidateCount: len(group), ObjectCount: len(objects), CandidateIDs: ids})
		for _, object := range objects {
			candidateIDs := objectGroups[object]
			sort.Strings(candidateIDs)
			if len(candidateIDs) < 2 {
				continue
			}
			proposals = append(proposals, SynthesisEchoFoldProposal{
				ID:               idFor("synth_echo_", key, object, strings.Join(candidateIDs, ",")),
				ClusterKey:       key,
				NormalizedObject: object,
				CandidateIDs:     candidateIDs,
			})
		}
	}
	return clusters, proposals, nil
}

func (s *Store) synthesisContradictions(claims []Claim) ([]SynthesisContradictionProposal, error) {
	superseded, err := s.supersededAsOf(s.maxSeq(claims))
	if err != nil {
		return nil, err
	}
	groups := map[string][]Claim{}
	for _, claim := range claims {
		if superseded[claim.ID] || claim.Visibility == "retired" {
			continue
		}
		key, err := s.claimClusterKey(claim)
		if err != nil {
			return nil, err
		}
		groups[key] = append(groups[key], claim)
	}
	var proposals []SynthesisContradictionProposal
	for _, key := range sortedKeys(groups) {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Seq == group[j].Seq {
				return group[i].ID < group[j].ID
			}
			return group[i].Seq < group[j].Seq
		})
		objectSet := map[string]bool{}
		var claimIDs []string
		for _, claim := range group {
			objectSet[NormalizeClaimValue(claim.Object)] = true
			claimIDs = append(claimIDs, claim.ID)
		}
		objects := sortedKeysBool(objectSet)
		if len(objects) < 2 {
			continue
		}
		proposals = append(proposals, SynthesisContradictionProposal{
			ID:         idFor("synth_contradiction_", key, strings.Join(claimIDs, ","), strings.Join(objects, ",")),
			ClusterKey: key,
			ClaimIDs:   claimIDs,
			Objects:    objects,
		})
	}
	return proposals, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysBool(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
