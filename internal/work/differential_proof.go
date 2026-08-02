package work

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"compound/internal/authority/gitrepository"
	"compound/internal/envelope"
	"compound/internal/home"
)

const DifferentialProofVerificationPrefix = "proof-diff:"

// ProofFailureFacts seals deterministic unique facts from complete failing output.
// Exact-line hashing is deliberately conservative: nondeterministic or path-specific
// output will not compare equal and therefore cannot silently become certifying proof.
func ProofFailureFacts(output []byte, runErr error) []string {
	if runErr == nil {
		return nil
	}
	unique := make(map[string]struct{})
	for _, line := range strings.Split(string(output), "\n") {
		if normalized := strings.Join(strings.Fields(line), " "); normalized != "" {
			unique[normalized] = struct{}{}
		}
	}
	if len(unique) == 0 {
		if normalized := strings.Join(strings.Fields(runErr.Error()), " "); normalized != "" {
			unique[normalized] = struct{}{}
		}
	}
	lines := make([]string, 0, len(unique))
	for line := range unique {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	facts := make([]string, 0, len(lines))
	for _, line := range lines {
		sum := sha256.Sum256([]byte(line))
		facts = append(facts, "sha256:"+hex.EncodeToString(sum[:]))
	}
	return facts
}

func (s *Store) loadCommittedFailedProofResult(id string) (envelope.WorkEnvelope, ProofResourceResult, ownedProofReport, error) {
	if !strings.HasPrefix(id, "pfrs_") {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, fmt.Errorf("invalid result identity %q", id)
	}
	var result ProofResourceResult
	if err := s.readEventJSON(id, &result); err != nil {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, err
	}
	if result.ID != id {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, fmt.Errorf("result identity mismatch")
	}
	w, err := s.Get(result.WorkID)
	if err != nil {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, err
	}
	artifacts, err := home.NewArtifactStore(s.home)
	if err != nil {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, err
	}
	body, err := artifacts.Get(result.ReportArtifactRef)
	if err != nil {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, err
	}
	report, err := decodeOwnedProofReport(body)
	if err != nil {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, err
	}
	if err := s.validateCommittedFailedProofResourceChain(w, result, report); err != nil {
		return envelope.WorkEnvelope{}, ProofResourceResult{}, ownedProofReport{}, err
	}
	return w, result, report, nil
}

func validateDifferentialProofComparison(candidate ProofResourceResult, candidateReport ownedProofReport, baseline ProofResourceResult, baselineReport ownedProofReport) error {
	if candidate.Status != ProofResourceFailed || baseline.Status != ProofResourceFailed || candidateReport.Status != "failed" || baselineReport.Status != "failed" {
		return fmt.Errorf("differential proof requires canonical failed candidate and baseline results")
	}
	if candidate.SourceSnapshot == nil || baseline.SourceSnapshot == nil || candidateReport.SourceSnapshot == nil || baselineReport.SourceSnapshot == nil {
		return fmt.Errorf("differential proof requires sealed source snapshots")
	}
	for label, snapshot := range map[string]gitrepository.Snapshot{"candidate": *candidate.SourceSnapshot, "baseline": *baseline.SourceSnapshot} {
		if err := gitrepository.ValidateSnapshotShape(snapshot); err != nil {
			return fmt.Errorf("%s source snapshot: %w", label, err)
		}
		if !snapshot.Clean {
			return fmt.Errorf("%s source snapshot is not clean", label)
		}
	}
	if baseline.SourceSnapshot.RepositoryFingerprint != candidate.SourceSnapshot.RepositoryFingerprint {
		return fmt.Errorf("candidate and baseline repository fingerprints differ")
	}
	if baseline.SourceSnapshot.HeadSHA != candidate.SourceSnapshot.BaseSHA {
		return fmt.Errorf("baseline HEAD %s is not candidate base %s", baseline.SourceSnapshot.HeadSHA, candidate.SourceSnapshot.BaseSHA)
	}
	if *candidate.SourceSnapshot != *candidateReport.SourceSnapshot || *baseline.SourceSnapshot != *baselineReport.SourceSnapshot {
		return fmt.Errorf("proof result source does not match content-addressed report")
	}
	if err := compareDifferentialGateInventory(candidateReport, baselineReport); err != nil {
		return err
	}
	baselineSteps := make(map[string]struct {
		status string
		facts  map[string]bool
	}, len(baselineReport.Steps))
	for _, step := range baselineReport.Steps {
		facts := make(map[string]bool, len(step.FailureFacts))
		for _, fact := range step.FailureFacts {
			if !validDifferentialFailureFact(fact) {
				return fmt.Errorf("baseline failed gate %s has malformed failure_facts", step.Name)
			}
			facts[fact] = true
		}
		baselineSteps[stepIdentity(step.Name, step.ProofRef)] = struct {
			status string
			facts  map[string]bool
		}{step.Status, facts}
	}
	for _, step := range candidateReport.Steps {
		if step.Status != "failed" {
			continue
		}
		if len(step.FailureFacts) == 0 {
			return fmt.Errorf("candidate failed gate %s has no failure_facts", step.Name)
		}
		base := baselineSteps[stepIdentity(step.Name, step.ProofRef)]
		if base.status != "failed" || len(base.facts) == 0 {
			return fmt.Errorf("candidate failed gate %s is not an attributable baseline failure", step.Name)
		}
		for _, fact := range step.FailureFacts {
			if !validDifferentialFailureFact(fact) {
				return fmt.Errorf("candidate failed gate %s has malformed failure_facts", step.Name)
			}
			if !base.facts[fact] {
				return fmt.Errorf("candidate failed gate %s fact %s is not present on baseline", step.Name, fact)
			}
		}
	}
	return nil
}

func validDifferentialFailureFact(fact string) bool {
	if !strings.HasPrefix(fact, "sha256:") || len(fact) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(fact, "sha256:"))
	return err == nil
}

func compareDifferentialGateInventory(candidate, baseline ownedProofReport) error {
	if len(candidate.Steps) != len(baseline.Steps) {
		return fmt.Errorf("candidate and baseline gate inventory length differs")
	}
	for i := range candidate.Steps {
		c, b := candidate.Steps[i], baseline.Steps[i]
		if c.Name != b.Name || c.Dir != b.Dir || c.SkipIfMissing != b.SkipIfMissing || c.ProofRef != b.ProofRef ||
			!reflect.DeepEqual(normalizeDifferentialCommand(c.Command, *candidate.SourceSnapshot), normalizeDifferentialCommand(b.Command, *baseline.SourceSnapshot)) {
			return fmt.Errorf("candidate and baseline gate inventory differs at step %d", i)
		}
	}
	return nil
}

func normalizeDifferentialCommand(command []string, source gitrepository.Snapshot) []string {
	out := make([]string, len(command))
	for i, token := range command {
		token = strings.ReplaceAll(token, source.BaseSHA, "$BASE")
		token = strings.ReplaceAll(token, source.HeadSHA, "$HEAD")
		out[i] = token
	}
	return out
}

func stepIdentity(name, proofRef string) string { return name + "\x00" + proofRef }

func differentialProofVerificationRef(candidateID, baselineID string) string {
	return DifferentialProofVerificationPrefix + candidateID + ":" + baselineID
}

func parseDifferentialProofVerificationRef(ref string) (candidateID, baselineID string, ok bool) {
	parts := strings.Split(ref, ":")
	if len(parts) != 3 || parts[0] != strings.TrimSuffix(DifferentialProofVerificationPrefix, ":") || !strings.HasPrefix(parts[1], "pfrs_") || !strings.HasPrefix(parts[2], "pfrs_") {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (s *Store) validateDifferentialProofRef(w envelope.WorkEnvelope, ref string) (ProofResourceResult, error) {
	candidateID, baselineID, ok := parseDifferentialProofVerificationRef(ref)
	if !ok {
		return ProofResourceResult{}, fmt.Errorf("differential proof verification %q is malformed", ref)
	}
	candidateWork, candidate, candidateReport, err := s.loadCommittedFailedProofResult(candidateID)
	if err != nil {
		return ProofResourceResult{}, fmt.Errorf("candidate proof chain: %w", err)
	}
	if candidateWork.ID != w.ID {
		return ProofResourceResult{}, fmt.Errorf("differential candidate belongs to work %s, not %s", candidateWork.ID, w.ID)
	}
	_, baseline, baselineReport, err := s.loadCommittedFailedProofResult(baselineID)
	if err != nil {
		return ProofResourceResult{}, fmt.Errorf("baseline proof chain: %w", err)
	}
	if err := validateDifferentialProofComparison(candidate, candidateReport, baseline, baselineReport); err != nil {
		return ProofResourceResult{}, err
	}
	want := differentialProofVerificationRef(candidate.ID, baseline.ID)
	if want != ref {
		return ProofResourceResult{}, fmt.Errorf("differential proof verification identity mismatch")
	}
	candidate.VerificationRef = ref
	return candidate, nil
}
