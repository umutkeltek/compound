package distiller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// S4 -- Bind improvement acceptance to the change and its negative control.
//
// The accept path used to check only that each cited eval proof was RECORDED
// and PASSING. That made the acceptance evidence unbound:
//
//   - an UNRELATED passing proof satisfied any review;
//   - a wrong-target or wrong-revision proof satisfied it;
//   - evidence covering only SOME of the affected consumers satisfied it;
//   - and an improvement that fixed nothing looked identical to one that fixed
//     a real regression, because nothing had to FAIL before the change.
//
// The binding here answers all four. An improvement review pins the replay
// cases it claims to fix (ReplayCases). Each binding names the case, the
// semantic assertion it exercises, the consumers it covers, and the target
// revision it was captured against. An eval proof is admissible for a binding
// only when it was captured on the SAME case, at the SAME target revision,
// covering the SAME consumer set, and exercising the SAME assertion.
//
// A binding marked RegressionFix additionally requires a BEFORE proof: the
// same case, captured before the change, failing the intended semantic
// assertion. An infrastructure failure is not a semantic failure, so it cannot
// establish a fix; neither can a both-green pair (nothing failed) nor a lone
// pass (nothing was proven to have failed).
//
// The validation is EVIDENCE ONLY. It reads recorded proofs and decides
// whether the acceptance evidence supports the claim. It never mutates
// distiller policy, prompts, code, or the eval corpus, and never promotes
// memory truth.

// replayProof is the recorded shape this validation reads: the case it was
// captured on, the assertions it exercised, the consumers it covered, and
// whether it passed.
type replayProof struct {
	Ref          string
	CaseID       string
	Assertion    string
	Consumers    []string
	Passed       bool
	LiveCalls    int
	RunError     string
	TargetDigest string
}

// loadReplayProofs resolves the cited proof refs into replayProof values.
//
// A ref that names no recorded proof, or whose recorded proof carries live
// model calls, is an error: the replay must stay zero-live-call.
func loadReplayProofs(homePath string, refs []string) ([]replayProof, error) {
	out := make([]replayProof, 0, len(refs))
	for _, ref := range compactEvalProofRefs(refs) {
		proof, err := loadReplayProof(homePath, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, proof)
	}
	return out, nil
}

// loadReplayProof reads one recorded eval proof into a replayProof.
//
// Unlike ValidateRecordedEvalProofRefs, this ACCEPTS a failing proof: the
// before proof of a regression fix is supposed to fail. It still refuses a
// proof that used live model calls, because replay stays zero-live-call.
func loadReplayProof(homePath, ref string) (replayProof, error) {
	path := evalProofPath(homePath, ref)
	data, err := readEvalProofFile(path)
	if err != nil {
		return replayProof{}, fmt.Errorf("eval proof ref %q not recorded: %w", ref, err)
	}
	proof := replayProof{Ref: ref}
	switch {
	case strings.HasPrefix(ref, "distiller_eval:"):
		var report EvalReport
		if err := unmarshalEvalProof(data, &report); err != nil {
			return replayProof{}, fmt.Errorf("parse eval proof %q: %w", ref, err)
		}
		if report.ID != strings.TrimPrefix(ref, "distiller_eval:") {
			return replayProof{}, fmt.Errorf("eval proof ref %q id mismatch: %q", ref, report.ID)
		}
		proof.CaseID = report.CaseID
		if proof.CaseID == "" {
			proof.CaseID = report.ID
		}
		proof.Passed = report.Status == "pass"
		proof.LiveCalls = report.Run.LiveModelCalls
		proof.RunError = report.Run.Error
		proof.TargetDigest = report.TargetDigest
		for _, check := range report.Checks {
			if check.Status != "pass" {
				proof.Assertion = check.Name
				break
			}
		}
		for _, decision := range report.Run.Decisions {
			proof.Consumers = append(proof.Consumers, decision.Kind)
		}
	case strings.HasPrefix(ref, "distiller_eval_corpus:"):
		var report EvalCorpusReport
		if err := unmarshalEvalProof(data, &report); err != nil {
			return replayProof{}, fmt.Errorf("parse eval corpus proof %q: %w", ref, err)
		}
		if report.ID != strings.TrimPrefix(ref, "distiller_eval_corpus:") {
			return replayProof{}, fmt.Errorf("eval corpus proof ref %q id mismatch: %q", ref, report.ID)
		}
		proof.CaseID = report.ID
		proof.Passed = report.Status == "pass"
		proof.LiveCalls = report.LiveModelCalls
		proof.TargetDigest = report.TargetDigest
		for _, r := range report.Reports {
			for _, d := range r.Run.Decisions {
				proof.Consumers = append(proof.Consumers, d.Kind)
			}
		}
	default:
		return replayProof{}, fmt.Errorf("eval proof ref %q must start with distiller_eval: or distiller_eval_corpus:", ref)
	}
	if proof.LiveCalls != 0 {
		return replayProof{}, fmt.Errorf("eval proof ref %q used live_model_calls=%d; replay stays zero-live-call", ref, proof.LiveCalls)
	}
	proof.Consumers = uniqueSortedStrings(proof.Consumers)
	return proof, nil
}

// validateImprovementAcceptance checks that the cited acceptance evidence is
// actually about the change the review claims.
//
// It returns the first blocking reason, or nil when the evidence supports the
// claim.
func validateImprovementAcceptance(homePath string, review ImprovementReview) error {
	bindings := review.ReplayCases
	if len(bindings) == 0 {
		// No pinned case: the review may not claim a regression fix. A review
		// that says nothing about which case it fixes is not acceptance
		// evidence, so an accept decision cannot stand on it.
		return fmt.Errorf("accept decision requires pinned replay_cases binding the evidence to the change")
	}
	after, err := loadReplayProofs(homePath, review.EvalProofRefs)
	if err != nil {
		return err
	}
	before, err := loadReplayProofs(homePath, review.BeforeEvalProofRefs)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.CaseID) == "" {
			return fmt.Errorf("replay case binding requires a case_id")
		}
		if strings.TrimSpace(binding.Assertion) == "" {
			return fmt.Errorf("replay case %q requires the semantic assertion it exercises", binding.CaseID)
		}
		afterProof, ok := findProofForCase(after, binding.CaseID)
		if !ok {
			return fmt.Errorf("replay case %q has no after proof; the cited evidence is unrelated to the change", binding.CaseID)
		}
		if err := checkProofMatchesBinding(afterProof, binding); err != nil {
			return err
		}
		if !afterProof.Passed {
			return fmt.Errorf("replay case %q after proof %q did not pass; the change is not proven", binding.CaseID, afterProof.Ref)
		}
		if !binding.RegressionFix {
			continue
		}
		beforeProof, ok := findProofForCase(before, binding.CaseID)
		if !ok {
			return fmt.Errorf("replay case %q claims a regression fix but has no before proof; a lone pass proves nothing was fixed", binding.CaseID)
		}
		if err := checkProofMatchesBinding(beforeProof, binding); err != nil {
			return err
		}
		if beforeProof.Passed {
			return fmt.Errorf("replay case %q before proof %q also passed; a both-green pair establishes no fix, so nothing failed before the change", binding.CaseID, beforeProof.Ref)
		}
		if strings.TrimSpace(beforeProof.RunError) != "" {
			return fmt.Errorf("replay case %q before proof %q failed with infrastructure error %q, not the intended semantic assertion %q", binding.CaseID, beforeProof.Ref, beforeProof.RunError, binding.Assertion)
		}
		if beforeProof.Assertion != "" && beforeProof.Assertion != binding.Assertion {
			return fmt.Errorf("replay case %q before proof %q failed assertion %q, not the intended semantic assertion %q", binding.CaseID, beforeProof.Ref, beforeProof.Assertion, binding.Assertion)
		}
	}
	return nil
}

// checkProofMatchesBinding checks that a proof is about the same case, target
// revision, consumer set, and assertion the binding names.
func checkProofMatchesBinding(proof replayProof, binding ReplayCaseBinding) error {
	if strings.TrimSpace(proof.CaseID) != binding.CaseID {
		return fmt.Errorf("eval proof %q is for case %q, not the pinned case %q; the cited evidence is unrelated to the change", proof.Ref, proof.CaseID, binding.CaseID)
	}
	if binding.TargetDigest != "" && proof.TargetDigest != "" && proof.TargetDigest != binding.TargetDigest {
		return fmt.Errorf("eval proof %q was captured against target revision %q, not %q", proof.Ref, proof.TargetDigest, binding.TargetDigest)
	}
	if len(binding.Consumers) > 0 {
		covered := make(map[string]bool, len(proof.Consumers))
		for _, consumer := range proof.Consumers {
			covered[consumer] = true
		}
		missing := make([]string, 0, len(binding.Consumers))
		for _, consumer := range binding.Consumers {
			if !covered[consumer] {
				missing = append(missing, consumer)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("eval proof %q does not cover the affected consumer(s) %s; the evidence is incomplete", proof.Ref, strings.Join(missing, ","))
		}
	}
	return nil
}

// uniqueSortedStrings removes duplicates and sorts, so a consumer set is stable
// across runs.
func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// findProofForCase returns the proof captured on the given case.
func findProofForCase(proofs []replayProof, caseID string) (replayProof, bool) {
	for _, proof := range proofs {
		if proof.CaseID == caseID {
			return proof, true
		}
	}
	return replayProof{}, false
}

// evalProofPath is where a recorded eval proof lives.
func evalProofPath(homePath, ref string) string {
	return filepath.Join(homePath, "reviews", "distiller-eval", "proofs", safeEvalProofRef(ref)+".json")
}

// readEvalProofFile reads a recorded eval proof body.
func readEvalProofFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// unmarshalEvalProof decodes a recorded eval proof body.
func unmarshalEvalProof(data []byte, value any) error {
	return json.Unmarshal(data, value)
}
