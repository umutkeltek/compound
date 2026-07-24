package work

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/runattempt"
)

// CollaborationRecipientQuery is the native adapter evidence available before
// a hook executes. It intentionally contains only harness-owned coordinates;
// actor/work/run/scope values are resolved from existing Compound state.
type CollaborationRecipientQuery struct {
	Harness   string
	SessionID string
	Cwd       string
}

// CollaborationRecipientResolution is the work owner's read model for one
// active native recipient. It is reference-only and creates no registry.
type CollaborationRecipientResolution struct {
	Actor          string
	WorkID         string
	RunAttemptID   string
	ScopeRefs      []string
	DependencyRefs []string
	PolicyRefs     []string
	BaseRevisions  []string
	ResourceRefs   []string
}

// ResolveCollaborationRecipient binds a native session/cwd/harness payload to
// one exact active, attached harness-execution RunAttempt. An exact existing
// delegate-session binding wins; otherwise the workspace/harness match must be
// unique. Ambiguity is never guessed.
func (s *Store) ResolveCollaborationRecipient(query CollaborationRecipientQuery) (CollaborationRecipientResolution, bool, error) {
	query.Harness = normalizeCollaborationHarness(query.Harness)
	query.SessionID = strings.TrimSpace(query.SessionID)
	query.Cwd = strings.TrimSpace(query.Cwd)
	if query.Harness == "" || query.SessionID == "" || query.Cwd == "" {
		return CollaborationRecipientResolution{}, false, nil
	}
	cwd, err := canonicalCollaborationPath(query.Cwd)
	if err != nil {
		return CollaborationRecipientResolution{}, false, err
	}
	works, err := s.List()
	if err != nil {
		return CollaborationRecipientResolution{}, false, err
	}
	workByID := make(map[string]envelope.WorkEnvelope, len(works))
	for _, item := range works {
		if terminalStates[item.State] {
			continue
		}
		workByID[item.ID] = item
	}
	attempts, err := s.runAttempts().ListNonTerminal()
	if err != nil {
		return CollaborationRecipientResolution{}, false, err
	}
	type candidate struct {
		attempt      runattempt.RunAttempt
		work         envelope.WorkEnvelope
		exactSession bool
	}
	candidates := make([]candidate, 0)
	for _, attempt := range attempts {
		if attempt.Origin != runattempt.OriginHarnessExecution ||
			normalizeCollaborationHarness(attempt.Harness) != query.Harness {
			continue
		}
		item, ok := workByID[attempt.WorkID]
		if !ok || !collaborationContains(item.RunAttemptIDs, attempt.ID) {
			continue
		}
		workspace, err := canonicalCollaborationPath(attempt.Workspace)
		if err != nil || !collaborationPathContains(workspace, cwd) {
			continue
		}
		binding, alive, err := s.latestDelegateBinding(item.ID)
		if err != nil {
			return CollaborationRecipientResolution{}, false, err
		}
		exactSession := alive && binding != nil &&
			binding.Session == query.SessionID &&
			binding.RunAttemptID == attempt.ID &&
			normalizeCollaborationHarness(binding.Harness) == query.Harness
		candidates = append(candidates, candidate{attempt: attempt, work: item, exactSession: exactSession})
	}
	exact := candidates[:0]
	for _, item := range candidates {
		if item.exactSession {
			exact = append(exact, item)
		}
	}
	if len(exact) == 1 {
		return s.collaborationRecipientResolution(exact[0].work, exact[0].attempt)
	}
	if len(exact) > 1 || len(candidates) != 1 {
		return CollaborationRecipientResolution{}, false, nil
	}
	return s.collaborationRecipientResolution(candidates[0].work, candidates[0].attempt)
}

func (s *Store) collaborationRecipientResolution(item envelope.WorkEnvelope, attempt runattempt.RunAttempt) (CollaborationRecipientResolution, bool, error) {
	resolution := CollaborationRecipientResolution{
		Actor: attempt.Actor, WorkID: item.ID, RunAttemptID: attempt.ID,
		ScopeRefs: []string{}, DependencyRefs: []string{}, PolicyRefs: []string{},
		BaseRevisions: []string{}, ResourceRefs: []string{},
	}
	resolution.ScopeRefs = appendCollaborationRef(resolution.ScopeRefs, item.CanonicalSurface)
	resolution.ScopeRefs = appendCollaborationRef(resolution.ScopeRefs, item.TargetSurface)
	resolution.PolicyRefs = appendCollaborationRef(resolution.PolicyRefs, item.PolicyDecisionID)
	resolution.BaseRevisions = appendCollaborationRef(resolution.BaseRevisions, item.ExpectedBaseSHA)
	if item.GitDelivery != nil {
		resolution.BaseRevisions = appendCollaborationRef(resolution.BaseRevisions, item.GitDelivery.ExpectedBaseSHA)
	}
	for _, ref := range item.EvidenceRefs {
		switch {
		case strings.HasPrefix(ref, "scope:"):
			resolution.ScopeRefs = appendCollaborationRef(resolution.ScopeRefs, ref)
		case strings.HasPrefix(ref, "dependency:"):
			resolution.DependencyRefs = appendCollaborationRef(resolution.DependencyRefs, ref)
		case strings.HasPrefix(ref, "pol_"), strings.HasPrefix(ref, "policy:"), strings.HasPrefix(ref, "compound://registries/"):
			resolution.PolicyRefs = appendCollaborationRef(resolution.PolicyRefs, ref)
		case strings.HasPrefix(ref, "git:"), strings.HasPrefix(ref, "base:"):
			resolution.BaseRevisions = appendCollaborationRef(resolution.BaseRevisions, ref)
		case strings.HasPrefix(ref, "resource:"):
			resolution.ResourceRefs = appendCollaborationRef(resolution.ResourceRefs, ref)
		}
	}
	if attempt.ActionIntentID != "" {
		events, err := eventstore.Open(s.home)
		if err != nil {
			return CollaborationRecipientResolution{}, false, err
		}
		entries, err := events.Scan(eventstore.ScanOptions{Prefixes: []string{"pol_"}})
		if err != nil {
			return CollaborationRecipientResolution{}, false, err
		}
		for _, entry := range entries {
			record, err := entry.Read()
			if err != nil {
				return CollaborationRecipientResolution{}, false, err
			}
			var decision envelope.PolicyDecision
			if err := json.Unmarshal(record.Bytes, &decision); err != nil {
				return CollaborationRecipientResolution{}, false, fmt.Errorf("resolve recipient policy %s: %w", entry.ID, err)
			}
			if err := decision.Validate(); err != nil {
				return CollaborationRecipientResolution{}, false, fmt.Errorf("resolve recipient policy %s: %w", entry.ID, err)
			}
			if decision.IDs.WorkID == item.ID && decision.ActionIntentID == attempt.ActionIntentID {
				resolution.PolicyRefs = appendCollaborationRef(resolution.PolicyRefs, decision.ID)
			}
		}
	}
	sort.Strings(resolution.ScopeRefs)
	sort.Strings(resolution.DependencyRefs)
	sort.Strings(resolution.PolicyRefs)
	sort.Strings(resolution.BaseRevisions)
	sort.Strings(resolution.ResourceRefs)
	return resolution, true, nil
}

func normalizeCollaborationHarness(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
}

func canonicalCollaborationPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("collaboration recipient workspace is empty")
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path, nil
}

func collaborationPathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func collaborationContains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func appendCollaborationRef(values []string, ref string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" || collaborationContains(values, ref) {
		return values
	}
	return append(values, ref)
}
