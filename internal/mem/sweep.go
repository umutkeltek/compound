package mem

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
)

const SynthesisSweepSchemaVersion = "compound.synthesis_sweep.v0.1"

// DecayReasonExpired is stamped on parked candidates that expire without
// corroboration (decisions-down landfill guard).
const DecayReasonExpired = "parked_stale_never_reinforced"

var synthesisSweepStages = []string{"cluster", "contradict", "corroborate", "decay", "reconcile"}

type SynthesisSweepReport struct {
	SchemaVersion              string                           `json:"schema_version"`
	GeneratedAt                string                           `json:"generated_at"`
	Stages                     []string                         `json:"stages"`
	CandidateCount             int                              `json:"candidate_count"`
	ClaimCount                 int                              `json:"claim_count"`
	ClusterCount               int                              `json:"cluster_count"`
	EchoFoldProposalCount      int                              `json:"echo_fold_proposal_count"`
	ContradictionProposalCount int                              `json:"contradiction_proposal_count"`
	CorroborationProposalCount int                              `json:"corroboration_proposal_count"`
	DecayedCount               int                              `json:"decayed_count"`
	ReconcileDismissedCount    int                              `json:"dismissed_count"`
	ReconcileReopenedCount     int                              `json:"reopened_count"`
	ReconcileRaisedCount       int                              `json:"raised_count"`
	ReconcileDecayRoutedCount  int                              `json:"decay_routed_count"`
	ReconcileParkedCount       int                              `json:"parked_count"`
	ReviewDebtCount            int                              `json:"review_debt_count"`
	Clusters                   []SynthesisCluster               `json:"clusters,omitempty"`
	EchoFoldProposals          []SynthesisEchoFoldProposal      `json:"echo_fold_proposals,omitempty"`
	ContradictionProposals     []SynthesisContradictionProposal `json:"contradiction_proposals,omitempty"`
	CorroborationProposals     []SynthesisCorroborationProposal `json:"corroboration_proposals,omitempty"`
	ReopenedClaims             []SynthesisClaimReview           `json:"reopened_claims,omitempty"`
	RaisedSeeds                []SynthesisSeedReview            `json:"raised_seeds,omitempty"`
	Decayed                    []SynthesisDecayAction           `json:"decayed,omitempty"`
	ReconcileParkedReasons     []SynthesisReconcileParkedReason `json:"parked_reasons,omitempty"`
}

// SynthesisDecayAction records one parked candidate filed to no_promote by the decay stage.
type SynthesisDecayAction struct {
	CandidateID   string `json:"candidate_id"`
	PreviousState string `json:"previous_state"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Reason        string `json:"reason"`
	CreatedAt     string `json:"created_at"`
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

type SynthesisCorroborationProposal struct {
	ID                string               `json:"id"`
	SeedCandidateID   string               `json:"seed_candidate_id"`
	SeedText          string               `json:"seed_text"`
	SeedCreatedAt     string               `json:"seed_created_at"`
	Expectation       CandidateExpectation `json:"expectation"`
	EvidenceClaimID   string               `json:"evidence_claim_id"`
	EvidenceSummary   string               `json:"evidence_summary"`
	EvidenceCreatedAt string               `json:"evidence_created_at"`
	EvidenceRefs      []string             `json:"evidence_refs"`
}

type SynthesisSeedReview struct {
	CandidateID     string   `json:"candidate_id"`
	State           string   `json:"state"`
	ReviewRef       string   `json:"review_ref"`
	EvidenceClaimID string   `json:"evidence_claim_id"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
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
		report.ReviewDebtCount += len(reopened)
	}
	if stageSet["corroborate"] {
		claims, err := s.loadClaims()
		if err != nil {
			return report, err
		}
		candidates, err := s.AllCandidates()
		if err != nil {
			return report, err
		}
		report.ClaimCount = len(claims)
		proposals, raised, err := s.synthesisCorroborations(claims, candidates, now)
		if err != nil {
			return report, err
		}
		report.CorroborationProposals = proposals
		report.CorroborationProposalCount = len(proposals)
		report.RaisedSeeds = raised
		report.ReviewDebtCount += len(raised)
	}
	if stageSet["decay"] {
		candidates, err := s.AllCandidates()
		if err != nil {
			return report, err
		}
		report.CandidateCount = len(candidates)
		decayed, err := s.decayExpiredCandidates(candidates, now)
		if err != nil {
			return report, err
		}
		report.Decayed = decayed
		report.DecayedCount = len(decayed)
	}
	if stageSet["reconcile"] {
		if err := s.synthesisReconcile(&report, now); err != nil {
			return report, err
		}
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
		if superseded[claim.ID] || claim.Visibility == "retired" {
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
	claimsInContradiction := map[string]bool{}
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
			claimsInContradiction[claim.ID] = true
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
	reopened, err := s.reopenClaimsForContradictions(proposals, claimByID, candidatesByID, claimsInContradiction, now)
	if err != nil {
		return nil, nil, err
	}
	return proposals, reopened, nil
}

func (s *Store) reopenClaimsForContradictions(proposals []SynthesisContradictionProposal, claims map[string]Claim, candidates map[string]Candidate, claimsInContradiction map[string]bool, now time.Time) ([]SynthesisClaimReview, error) {
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
		for claimID, mark := range marks {
			if claimsInContradiction[claimID] {
				continue
			}
			for _, ref := range mark.Refs {
				if !isSynthesisContradictionRef(ref) {
					continue
				}
				seq, err := s.nextSeq()
				if err != nil {
					return err
				}
				edge := Edge{
					Seq:          seq,
					Kind:         EdgeKindReviewCleared,
					From:         ref,
					To:           claimID,
					Confidence:   1,
					EvidenceRefs: []string{ref},
					CreatedAt:    rfc3339(now),
				}
				if err := appendJSONL(s.edgesLog(), &edge); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return reopened, err
}

func isSynthesisContradictionRef(ref string) bool {
	return strings.HasPrefix(ref, "synth_contradiction_")
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

func (s *Store) synthesisCorroborations(claims []Claim, candidates []Candidate, now time.Time) ([]SynthesisCorroborationProposal, []SynthesisSeedReview, error) {
	marks, err := s.candidateReviewMarksAsOf(0)
	if err != nil {
		return nil, nil, err
	}
	superseded, err := s.supersededAsOf(s.maxSeq(claims))
	if err != nil {
		return nil, nil, err
	}
	claims = laterEvidenceClaims(claims, superseded)
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].Seq == claims[j].Seq {
			return claims[i].ID < claims[j].ID
		}
		return claims[i].Seq < claims[j].Seq
	})
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })

	var proposals []SynthesisCorroborationProposal
	for _, seed := range candidates {
		if !openSeedCandidate(seed, now) {
			continue
		}
		if mark, ok := marks[seed.ID]; ok && len(mark.Refs) > 0 {
			continue
		}
		for _, claim := range claims {
			if !claimIsLaterThanCandidate(claim, seed) {
				continue
			}
			if !expectationSatisfiedByClaim(*seed.Expectation, seed, claim) {
				continue
			}
			proposals = append(proposals, synthesisCorroborationProposal(seed, claim))
			break
		}
	}
	raised, err := s.raiseSeedsForCorroborations(proposals, now)
	if err != nil {
		return nil, nil, err
	}
	return proposals, raised, nil
}

func laterEvidenceClaims(claims []Claim, superseded map[string]bool) []Claim {
	out := make([]Claim, 0, len(claims))
	for _, claim := range claims {
		if superseded[claim.ID] || claim.Visibility == "retired" || len(claim.EvidenceRefs) == 0 {
			continue
		}
		out = append(out, claim)
	}
	return out
}

func openSeedCandidate(c Candidate, now time.Time) bool {
	if c.State != "parked" {
		return false
	}
	if c.Expectation == nil || strings.TrimSpace(c.Expectation.Predicate) == "" || strings.TrimSpace(c.Expectation.Object) == "" {
		return false
	}
	if !candidateHasTag(c, "seed") && strings.TrimSpace(c.Salience) != "low" {
		return false
	}
	if candidateExpired(c, now) {
		return false
	}
	return true
}

func candidateHasTag(c Candidate, tag string) bool {
	for _, got := range c.Tags {
		if strings.EqualFold(strings.TrimSpace(got), tag) {
			return true
		}
	}
	return false
}

func candidateExpired(c Candidate, now time.Time) bool {
	expiresAt := strings.TrimSpace(c.ExpiresAt)
	if expiresAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return !now.Before(parsed)
}

func claimIsLaterThanCandidate(claim Claim, seed Candidate) bool {
	seedAt, err := time.Parse(time.RFC3339, seed.CreatedAt)
	if err != nil {
		return false
	}
	claimAt, err := time.Parse(time.RFC3339, claim.CreatedAt)
	if err != nil {
		return false
	}
	return claimAt.After(seedAt)
}

func expectationSatisfiedByClaim(exp CandidateExpectation, seed Candidate, claim Claim) bool {
	entity := firstNonEmpty(exp.Entity, seed.Entity)
	if strings.TrimSpace(entity) != "" && !strings.EqualFold(strings.TrimSpace(claim.Entity), strings.TrimSpace(entity)) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(claim.Predicate), strings.TrimSpace(exp.Predicate)) {
		return false
	}
	return SameClaimObject(claim.Object, exp.Object)
}

func synthesisCorroborationProposal(seed Candidate, claim Claim) SynthesisCorroborationProposal {
	exp := *seed.Expectation
	evidenceSummary := fmt.Sprintf("%s.%s = %s", claim.Entity, claim.Predicate, claim.Object)
	refs := []string{"claim:" + claim.ID, "candidate:" + seed.ID}
	for _, ref := range claim.EvidenceRefs {
		if strings.TrimSpace(ref) != "" {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs[2:])
	return SynthesisCorroborationProposal{
		ID:                idFor("synth_corroboration_", seed.ID, claim.ID, exp.Predicate, exp.Object),
		SeedCandidateID:   seed.ID,
		SeedText:          seed.Object,
		SeedCreatedAt:     seed.CreatedAt,
		Expectation:       exp,
		EvidenceClaimID:   claim.ID,
		EvidenceSummary:   evidenceSummary,
		EvidenceCreatedAt: claim.CreatedAt,
		EvidenceRefs:      uniqueStrings(refs),
	}
}

func (s *Store) raiseSeedsForCorroborations(proposals []SynthesisCorroborationProposal, now time.Time) ([]SynthesisSeedReview, error) {
	var raised []SynthesisSeedReview
	err := s.withLock(func() error {
		marks, err := s.candidateReviewMarksAsOf(0)
		if err != nil {
			return err
		}
		for _, proposal := range proposals {
			if mark, ok := marks[proposal.SeedCandidateID]; ok && len(mark.Refs) > 0 {
				continue
			}
			seq, err := s.nextSeq()
			if err != nil {
				return err
			}
			edge := Edge{
				Seq:          seq,
				Kind:         EdgeKindCorroboration,
				From:         proposal.ID,
				To:           proposal.SeedCandidateID,
				Confidence:   1,
				EvidenceRefs: append([]string(nil), proposal.EvidenceRefs...),
				CreatedAt:    rfc3339(now),
			}
			if err := appendJSONL(s.edgesLog(), &edge); err != nil {
				return err
			}
			// Corroboration earns immunity from decay: clear expiry so the landfill
			// sweep can never silently drop a raised seed (decisions-down falsifier).
			if c, err := s.getCandidate(proposal.SeedCandidateID); err == nil {
				if strings.TrimSpace(c.ExpiresAt) != "" {
					c.ExpiresAt = ""
					if strings.TrimSpace(c.Reason) == "" || strings.Contains(c.Reason, "expiry") || strings.Contains(c.Reason, "park with expiry") {
						c.Reason = "corroborated; expiry cleared"
					}
					_ = candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c)
				}
			}
			marks[proposal.SeedCandidateID] = candidateReviewMark{Refs: []string{proposal.ID}}
			raised = append(raised, SynthesisSeedReview{
				CandidateID:     proposal.SeedCandidateID,
				State:           ClaimStateNeedsReview,
				ReviewRef:       proposal.ID,
				EvidenceClaimID: proposal.EvidenceClaimID,
				EvidenceRefs:    append([]string(nil), proposal.EvidenceRefs...),
				CreatedAt:       rfc3339(now),
			})
		}
		return nil
	})
	return raised, err
}

// decayExpiredCandidates files parked candidates past ExpiresAt to no_promote.
// Corroborated candidates (EdgeKindCorroboration mark) NEVER expire.
// Idempotent: already no_promote / rejected / promoted are skipped.
func (s *Store) decayExpiredCandidates(all []Candidate, now time.Time) ([]SynthesisDecayAction, error) {
	marks, err := s.candidateReviewMarksAsOf(0)
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	var decayed []SynthesisDecayAction
	for _, c := range all {
		if c.State != "parked" {
			continue
		}
		if !candidateExpired(c, now) {
			continue
		}
		// Corroboration mark = raised/needs_review path — never silently drop.
		if mark, ok := marks[c.ID]; ok && len(mark.Refs) > 0 {
			continue
		}
		if err := s.fileExpiredCandidate(c, now); err != nil {
			return decayed, err
		}
		decayed = append(decayed, SynthesisDecayAction{
			CandidateID:   c.ID,
			PreviousState: c.State,
			ExpiresAt:     c.ExpiresAt,
			Reason:        DecayReasonExpired,
			CreatedAt:     rfc3339(now),
		})
	}
	return decayed, nil
}

func (s *Store) fileExpiredCandidate(c Candidate, now time.Time) error {
	c.State = "no_promote"
	c.Reason = DecayReasonExpired
	// Keep ExpiresAt for audit; do not re-park.
	return candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
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
