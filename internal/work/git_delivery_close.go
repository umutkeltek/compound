package work

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
)

// gitDeliveryProofForClose is the single delivery gate shared by ClosePlan and
// Close. It derives source-control intent from immutable work evidence, then
// requires the envelope-owned contract and a live-valid landed proof.
func (s *Store) gitDeliveryProofForClose(w envelope.WorkEnvelope, outcome, moveType string, proofRefs []string, now time.Time) (*LandCheckReport, error) {
	if outcome != "completed" {
		return nil, nil
	}
	signal, err := s.gitDeliveryRequirementSignal(w, moveType, proofRefs)
	if err != nil {
		return nil, err
	}
	if w.GitDelivery == nil {
		if signal == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("completed work %s requires git_delivery authority because %s", w.ID, signal)
	}
	proof, err := s.recordedLandedProofForClose(w, proofRefs)
	if err != nil {
		return nil, err
	}
	if err := s.revalidateLandedProof(w, proof, now); err != nil {
		return nil, err
	}
	return &proof, nil
}

func (s *Store) gitDeliveryRequirementSignal(w envelope.WorkEnvelope, moveType string, proofRefs []string) (string, error) {
	if ref, _, found, _ := latestCommitArtifact(proofRefs); found {
		return fmt.Sprintf("proof %s declares a Git commit", ref), nil
	}
	if moveType == "audit_only" {
		return "", nil
	}
	if signal, err := gitScopedPathSignal("work target_surface", w.TargetSurface); signal != "" || err != nil {
		return signal, err
	}
	for _, runID := range w.RunIDs {
		if !strings.HasPrefix(runID, "act_") {
			continue
		}
		var action envelope.ActionIntent
		if err := s.readJSON(s.envPath(runID), &action); err != nil {
			return "", fmt.Errorf("read ActionIntent %s for git_delivery scope: %w", runID, err)
		}
		if err := action.Validate(); err != nil {
			return "", fmt.Errorf("ActionIntent %s invalid while deriving git_delivery scope: %w", runID, err)
		}
		if action.ID != runID || action.IDs.WorkID != w.ID {
			return "", fmt.Errorf("ActionIntent %s identity conflicts with work %s while deriving git_delivery scope", runID, w.ID)
		}
		if action.MutationClass == "read" {
			continue
		}
		if signal, err := gitScopedPathSignal("recorded ActionIntent "+runID+" target.path", action.Target.Path); signal != "" || err != nil {
			return signal, err
		}
	}
	return "", nil
}

func gitScopedPathSignal(source, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return "", nil
	}
	root, found, err := canonicalGitRootForPath(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q for git_delivery scope: %w", source, value, err)
	}
	if !found {
		return "", nil
	}
	return fmt.Sprintf("%s %q resolves inside Git worktree %q", source, value, root), nil
}

// canonicalGitRootForPath accepts a repository, directory, file, or not-yet-created
// descendant. It walks to the nearest existing directory before asking Git for the
// canonical worktree root, so callers cannot evade the gate with a future file path.
func canonicalGitRootForPath(value string) (string, bool, error) {
	path := filepath.Clean(value)
	for {
		info, err := os.Stat(path)
		switch {
		case err == nil:
			if !info.IsDir() {
				path = filepath.Dir(path)
			}
			goto existing
		case errors.Is(err, os.ErrNotExist):
			parent := filepath.Dir(path)
			if parent == path {
				return "", false, nil
			}
			path = parent
		default:
			return "", false, err
		}
	}

existing:
	canonicalDir, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, err
	}
	cmd := exec.Command("git", "-C", canonicalDir, "rev-parse", "--show-toplevel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "not a git repository") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("git rev-parse --show-toplevel: %w: %s", err, strings.TrimSpace(string(out)))
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(string(out))))
	if err != nil {
		return "", false, err
	}
	return root, true, nil
}
