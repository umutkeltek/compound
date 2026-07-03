package mem

import (
	"fmt"
	"math"
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
	ReviewDebtCount            int                              `json:"review_debt_count"`
	Clusters                   []SynthesisCluster               `json:"clusters,omitempty"`
	EchoFoldProposals          []SynthesisEchoFoldProposal      `json:"echo_fold_proposals,omitempty"`
	ContradictionProposals     []SynthesisContradictionProposal `json:"contradiction_proposals,omitempty"`
	ReopenedClaims             []SynthesisClaimReview           `json:"reopened_claims,omitempty"`
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
	ID           string   `json:"id"`
	ClusterKey   string   `json:"cluster_key"`
	ClaimIDs     []string `json:"claim_ids"`
	CandidateIDs []string `json:"candidate_ids,omitempty"`
	Objects      []string `json:"objects"`
}

type SynthesisClaimReview struct {
	ClaimID         string   `json:"claim_id"`
	State           string   `json:"state"`
	ReviewRef       string   `json:"review_ref"`
	CandidateIDs    []string `json:"candidate_ids,omitempty"`
	ConfidenceDelta float64  `json:"confidence_delta"`
	CreatedAt       string   `json:"created_at"`
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
		candidates, err := s.AllCandidates()
		if err != nil {
			return report, err
		}
		report.ClaimCount = len(claims)
		proposals, reopened, err := s.synthesisContradictions(claims, candidates, now)
		if err != nil {
			return report, err
		}
		report.ContradictionProposals = proposals
		report.ContradictionProposalCount = len(proposals)
		report.ReopenedClaims = reopened
		report.ReviewDebtCount = len(reopened)
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

type synthesisContradictionGroup struct {
	claims     []Claim
	candidates []Candidate
	objects    map[string]bool
}

func (s *Store) synthesisContradictions(claims []Claim, candidates []Candidate, now time.Time) ([]SynthesisContradictionProposal, []SynthesisClaimReview, error) {
	superseded, err := s.supersededAsOf(s.maxSeq(claims))
	if err != nil {
		return nil, nil, err
	}
	groups := map[string]*synthesisContradictionGroup{}
	ensure := func(key string) *synthesisContradictionGroup {
		g := groups[key]
		if g == nil {
			g = &synthesisContradictionGroup{objects: map[string]bool{}}
			groups[key] = g
		}
		return g
	}
	for _, claim := range claims {
		if superseded[claim.ID] || claim.Visibility == "retired" || claim.State == ClaimStateNeedsReview {
			continue
		}
		key, err := s.claimClusterKey(claim)
		if err != nil {
			return nil, nil, err
		}
		group := ensure(key)
		group.claims = append(group.claims, claim)
		group.objects[NormalizeClaimValue(claim.Object)] = true
	}
	for _, candidate := range candidates {
		if candidate.State != "proposed" {
			continue
		}
		key, err := s.candidateClusterKey(candidate)
		if err != nil {
			return nil, nil, err
		}
		group := ensure(key)
		group.candidates = append(group.candidates, candidate)
		group.objects[NormalizeClaimValue(candidate.Object)] = true
	}

	var proposals []SynthesisContradictionProposal
	claimByID := map[string]Claim{}
	candidatesByID := map[string]Candidate{}
	for _, key := range sortedKeys(groups) {
		group := groups[key]
		if len(group.claims) == 0 {
			continue
		}
		sort.Slice(group.claims, func(i, j int) bool {
			if group.claims[i].Seq == group.claims[j].Seq {
				return group.claims[i].ID < group.claims[j].ID
			}
			return group.claims[i].Seq < group.claims[j].Seq
		})
		sort.Slice(group.candidates, func(i, j int) bool { return group.candidates[i].ID < group.candidates[j].ID })
		objects := sortedKeysBool(group.objects)
		if len(objects) < 2 {
			continue
		}
		var claimIDs []string
		for _, claim := range group.claims {
			claimIDs = append(claimIDs, claim.ID)
			claimByID[claim.ID] = claim
		}
		var candidateIDs []string
		for _, candidate := range group.candidates {
			candidateIDs = append(candidateIDs, candidate.ID)
			candidatesByID[candidate.ID] = candidate
		}
		proposals = append(proposals, SynthesisContradictionProposal{
			ID:           idFor("synth_contradiction_", key, strings.Join(claimIDs, ","), strings.Join(candidateIDs, ","), strings.Join(objects, ",")),
			ClusterKey:   key,
			ClaimIDs:     claimIDs,
			CandidateIDs: candidateIDs,
			Objects:      objects,
		})
	}
	reopened, err := s.reopenClaimsForContradictions(proposals, claimByID, candidatesByID, now)
	if err != nil {
		return nil, nil, err
	}
	return proposals, reopened, nil
}

func (s *Store) reopenClaimsForContradictions(proposals []SynthesisContradictionProposal, claims map[string]Claim, candidates map[string]Candidate, now time.Time) ([]SynthesisClaimReview, error) {
	if len(proposals) == 0 {
		return nil, nil
	}
	var reopened []SynthesisClaimReview
	err := s.withLock(func() error {
		marks, err := s.claimReviewMarksAsOf(0)
		if err != nil {
			return err
		}
		for _, proposal := range proposals {
			for _, claimID := range proposal.ClaimIDs {
				claim, ok := claims[claimID]
				if !ok {
					continue
				}
				if _, exists := marks[claimID]; exists {
					continue
				}
				delta := contradictionConfidenceDelta(claim, proposal, claims, candidates)
				seq, err := s.nextSeq()
				if err != nil {
					return err
				}
				review := SynthesisClaimReview{
					ClaimID:         claimID,
					State:           ClaimStateNeedsReview,
					ReviewRef:       proposal.ID,
					CandidateIDs:    append([]string(nil), proposal.CandidateIDs...),
					ConfidenceDelta: delta,
					CreatedAt:       rfc3339(now),
				}
				edge := Edge{
					Seq:             seq,
					Kind:            EdgeKindNeedsReview,
					From:            proposal.ID,
					To:              claimID,
					Confidence:      math.Abs(delta),
					ConfidenceDelta: delta,
					EvidenceRefs:    append([]string{proposal.ID}, proposal.CandidateIDs...),
					CreatedAt:       rfc3339(now),
				}
				if err := appendJSONL(s.edgesLog(), &edge); err != nil {
					return err
				}
				marks[claimID] = claimReviewMark{Refs: []string{proposal.ID}, ConfidenceDelta: delta}
				reopened = append(reopened, review)
			}
		}
		return nil
	})
	return reopened, err
}

func contradictionConfidenceDelta(claim Claim, proposal SynthesisContradictionProposal, claims map[string]Claim, candidates map[string]Candidate) float64 {
	best := 0.0
	claimObject := NormalizeClaimValue(claim.Object)
	consider := func(object string, confidence float64) {
		if NormalizeClaimValue(object) == claimObject {
			return
		}
		delta := confidence - claim.Confidence
		if math.Abs(delta) > math.Abs(best) {
			best = delta
		}
	}
	for _, otherID := range proposal.ClaimIDs {
		if otherID == claim.ID {
			continue
		}
		if other, ok := claims[otherID]; ok {
			consider(other.Object, other.Confidence)
		}
	}
	for _, candidateID := range proposal.CandidateIDs {
		if candidate, ok := candidates[candidateID]; ok {
			consider(candidate.Object, candidate.Confidence)
		}
	}
	return best
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
