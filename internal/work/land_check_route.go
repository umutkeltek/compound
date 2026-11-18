package work

import (
	"context"
	"fmt"

	"compound/internal/authority/gitrepository"
)

// Which proof can close a landed candidate depends on the repository, not on
// the landing. The post-land remote verifier runs Compound's own suite on
// Compound's target branch; it never builds another repository. A repository
// that commits a supported native profile (compound-proof.json consumers, or a
// Rust workspace) is proved by a canonical proof-check run at the exact
// candidate. A repository with neither has an explicit verification gap.
// Routing only names the next step: every gate that decides a close is the
// same for all three, and no route lowers or replaces one.
const (
	landingProofRouteRemoteVerifier = "remote_verifier"
	landingProofRouteNativeExact    = "native_exact_candidate"
	landingProofRouteNoProfile      = "no_profile"
)

// landingProofRoute is the proof route for one exact candidate commit.
type landingProofRoute struct {
	kind    string
	profile string // canonical plan ID when a profile was read
	reason  string // why no supported profile was read
}

// landingProofRouteAtCandidate reads the committed profile declaration at the
// exact candidate commit through the same resolver canonical admission uses,
// in its profile-only mode: it builds no inventory, runs nothing and certifies
// nothing. The snapshot names the candidate commit and its tree; the resolver
// reads only committed blobs at that commit, so worktree state is irrelevant.
func landingProofRouteAtCandidate(repoPath, fingerprint, expectedBase, candidate string) (landingProofRoute, error) {
	tree, err := runLandingGitOutput(repoPath, "rev-parse", "--verify", candidate+"^{tree}")
	if err != nil {
		return landingProofRoute{}, fmt.Errorf("resolve candidate %s tree for its proof profile: %w", candidate, err)
	}
	snapshot := gitrepository.Snapshot{
		RepositoryFingerprint: fingerprint,
		BaseSHA:               expectedBase,
		HeadSHA:               candidate,
		TreeSHA:               tree,
		Clean:                 true,
	}
	plan, err := resolveCanonicalProofPlanContext(context.Background(), repoPath, snapshot, nil, true)
	if err != nil {
		return landingProofRoute{kind: landingProofRouteNoProfile, reason: err.Error()}, nil
	}
	if plan.ID == CanonicalProofPlanCompoundV1 {
		return landingProofRoute{kind: landingProofRouteRemoteVerifier, profile: plan.ID}, nil
	}
	return landingProofRoute{kind: landingProofRouteNativeExact, profile: plan.ID}, nil
}

// nativeExactCandidateProofAction names the canonical proof-check run that can
// certify a landed candidate of a repository with a native profile.
func nativeExactCandidateProofAction(workID, candidate, expectedBase, profile string) string {
	return fmt.Sprintf(
		"run native exact-candidate proof for commit:%s: this repository commits the %s proof profile and the remote verifier covers only Compound, so a verifier verdict cannot certify it; from a clean checkout whose HEAD is exactly %s, run compound proof-check --json --repo <that checkout> --base %s --work %s --actor <lease holder> --out <absolute report path>, then rerun land-check",
		candidate, profile, candidate, expectedBase, workID,
	)
}

// noProofProfileAction names the explicit gap of a landed candidate whose
// repository commits no supported proof profile.
func noProofProfileAction(candidate, reason string) string {
	return fmt.Sprintf(
		"verification gap: commit:%s has no supported native proof profile (%s) and the remote verifier covers only Compound; enroll the repository's compound-proof.json in a reviewed change and prove an exact candidate that carries it, or leave this work open; a certificate from another repository cannot close it",
		candidate, reason,
	)
}
