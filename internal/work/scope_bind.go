package work

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// A piece of work records the app it is for in Header.Scope.ProductID, or the
// system it is for in Header.Scope.ProjectID. This file binds that scope on
// existing open work; new work gets it at creation.

const ScopeBindPlanSchemaVersion = "compound.work_scope_bind.v0.1"

// Scope binding item statuses.
const (
	ScopeBindWouldBind       = "would_bind"       // dry run: one home matches; apply would record it
	ScopeBindBound           = "bound"            // apply: recorded
	ScopeBindAlreadyRecorded = "already_recorded" // the record already names its scope; left as is
	ScopeBindInFlight        = "in_flight"        // claimed, running or verifying: its claim seals the record's bytes
	ScopeBindAmbiguous       = "ambiguous"        // more than one home matches; left empty
	ScopeBindNone            = "none"             // no recorded key names a home; left empty
	ScopeBindRefused         = "refused"          // policy did not allow it, or the record changed since the plan
)

// AppScopeKeys are the recorded values of one piece of work that can name
// the app or system it is for.
type AppScopeKeys struct {
	Surface       string
	Worktree      string
	ContextFolder string
}

// AppScopeAnswer is the identity owner's answer for one piece of work. Scope
// is non-empty only when exactly one home matched; Key names the recorded key
// that decided it, and Reason says why in plain words either way.
type AppScopeAnswer struct {
	Scope      envelope.Scope
	Key        string
	Ambiguous  bool
	Candidates []string
	Reason     string
}

// AppScopeResolver answers which app or system a piece of work is for. The
// identity owner implements it; work cannot import that owner, so a caller
// passes it in.
type AppScopeResolver func(AppScopeKeys) AppScopeAnswer

// ScopeBindItem is one open work item in a binding plan.
type ScopeBindItem struct {
	WorkID           string   `json:"work_id"`
	State            string   `json:"state"`
	Status           string   `json:"status"`
	ProductID        string   `json:"product_id,omitempty"`
	ProjectID        string   `json:"project_id,omitempty"`
	Key              string   `json:"key,omitempty"`
	Candidates       []string `json:"candidates,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	PolicyOutcome    string   `json:"policy_outcome,omitempty"`
	ActionIntentID   string   `json:"action_intent_id,omitempty"`
	PolicyDecisionID string   `json:"policy_decision_id,omitempty"`
	workSHA256       string
	scope            envelope.Scope
}

// ScopeBindPlan is the result of `work bind-app`: what binding open work to
// its app would do (dry run) or did (apply). Closed work is never read into it.
type ScopeBindPlan struct {
	SchemaVersion string          `json:"schema_version"`
	GeneratedAt   string          `json:"generated_at"`
	Mode          string          `json:"mode"`
	Open          int             `json:"open"`
	Counts        map[string]int  `json:"counts"`
	Homes         map[string]int  `json:"homes"`
	Items         []ScopeBindItem `json:"items"`
}

// AppScopeKeysFor gathers the recorded keys of w: its canonical surface, the
// git worktree it runs in, and the folder its context pack was built for.
func (s *Store) AppScopeKeysFor(w envelope.WorkEnvelope) AppScopeKeys {
	keys := AppScopeKeys{Surface: strings.TrimSpace(w.CanonicalSurface)}
	if w.GitDelivery != nil {
		keys.Worktree = strings.TrimSpace(w.GitDelivery.TargetWorktreeRoot)
	}
	if id := strings.TrimSpace(w.IDs.ContextPackID); id != "" {
		var pack envelope.ContextPack
		if s.readJSON(filepath.Join(s.home, "context", "packs", id+".json"), &pack) == nil {
			keys.ContextFolder = strings.TrimSpace(pack.Scope.Surface)
		}
	}
	return keys
}

// scopeInFlight reports why w's bytes may not change now: a claimed, running
// or verifying record, or one that holds a claim lease, is sealed by hash in
// its claim, so rewriting it would break that chain.
func (s *Store) scopeInFlight(w envelope.WorkEnvelope) string {
	switch {
	case strings.HasPrefix(w.State, "claimed") || w.State == "running" || w.State == "verifying":
		return "work is " + w.State + "; its claim is sealed to the record as it is, so it keeps no app until new work carries one"
	}
	if _, err := os.Stat(s.leasePath(w.ID)); err == nil {
		return "work holds a claim lease sealed to the record as it is"
	}
	return ""
}

// PlanScopeBinding reads every open work item and says, per item, what
// binding it to its app would do, including the policy outcome for actor. It
// writes nothing.
func (s *Store) PlanScopeBinding(resolve AppScopeResolver, actor string, now time.Time) (ScopeBindPlan, error) {
	actor = firstNonEmpty(strings.TrimSpace(actor), "compound")
	if resolve == nil {
		return ScopeBindPlan{}, errors.New("plan scope binding: no app resolver")
	}
	works, err := s.List()
	if err != nil {
		return ScopeBindPlan{}, err
	}
	cfg, _, err := policy.LoadConfig(s.home)
	if err != nil {
		return ScopeBindPlan{}, err
	}
	plan := ScopeBindPlan{
		SchemaVersion: ScopeBindPlanSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Mode:          "dry_run",
		Counts:        map[string]int{},
		Homes:         map[string]int{},
		Items:         []ScopeBindItem{},
	}
	sort.Slice(works, func(i, j int) bool { return works[i].ID < works[j].ID })
	for _, w := range works {
		if terminalStates[w.State] {
			continue
		}
		plan.Open++
		item := ScopeBindItem{WorkID: w.ID, State: w.State}
		switch {
		case w.Scope != (envelope.Scope{}):
			item.Status, item.ProductID, item.ProjectID = ScopeBindAlreadyRecorded, w.Scope.ProductID, w.Scope.ProjectID
		case s.scopeInFlight(w) != "":
			item.Status, item.Reason = ScopeBindInFlight, s.scopeInFlight(w)
		default:
			answer := resolve(s.AppScopeKeysFor(w))
			item.Key, item.Reason, item.Candidates = answer.Key, answer.Reason, answer.Candidates
			switch {
			case answer.Scope != (envelope.Scope{}):
				item.Status, item.scope = ScopeBindWouldBind, answer.Scope
				item.ProductID, item.ProjectID = answer.Scope.ProductID, answer.Scope.ProjectID
				sha, err := workEnvelopeSHA256(w)
				if err != nil {
					return ScopeBindPlan{}, err
				}
				item.workSHA256 = sha
				ai := s.scopeBindActionIntent(w, answer.Scope, item.Key, actor, now)
				item.PolicyOutcome = policy.Evaluate(ai, cfg, now).Outcome
			case answer.Ambiguous:
				item.Status = ScopeBindAmbiguous
			default:
				item.Status = ScopeBindNone
			}
		}
		plan.Counts[item.Status]++
		if item.Status == ScopeBindWouldBind {
			plan.Homes[firstNonEmpty(item.ProductID, item.ProjectID)]++
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

// ApplyScopeBinding records each would_bind item of a fresh plan through
// BindScope. An item that policy does not allow or whose record changed since
// the plan is reported as refused and left as it was.
func (s *Store) ApplyScopeBinding(plan ScopeBindPlan, actor string, now time.Time) (ScopeBindPlan, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return plan, errors.New("bind scope: --by is required to record who bound the work")
	}
	plan.Mode = "apply"
	plan.Counts = map[string]int{}
	plan.Homes = map[string]int{}
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Status == ScopeBindWouldBind {
			ai, pd, err := s.BindScope(item.WorkID, item.scope, item.Key, item.workSHA256, actor, now)
			item.ActionIntentID, item.PolicyOutcome = ai.ID, pd.Outcome
			if err != nil {
				item.Status, item.Reason = ScopeBindRefused, err.Error()
			} else {
				item.Status, item.PolicyDecisionID = ScopeBindBound, pd.ID
				plan.Homes[firstNonEmpty(item.ProductID, item.ProjectID)]++
			}
		}
		plan.Counts[item.Status]++
	}
	return plan, nil
}

// BindScope records scope on one open, unclaimed work item whose record still
// hashes to expectedSHA256. It is a same-state update under the lifecycle
// lock: it mints an ActionIntent, evaluates current policy, and only when
// policy allows persists both, appends a lifecycle event (from == to) that
// attaches them and names the deciding key, then rewrites the record. It does
// not touch the record's action intent ids or policy decision, so no launch
// generation changes, and it never changes a scope already recorded.
func (s *Store) BindScope(workID string, scope envelope.Scope, key, expectedSHA256, actor string, now time.Time) (envelope.ActionIntent, envelope.PolicyDecision, error) {
	var ai envelope.ActionIntent
	var pd envelope.PolicyDecision
	err := s.withLifecycleMutation(func() error {
		cur, err := s.Get(workID)
		if err != nil {
			return err
		}
		if terminalStates[cur.State] {
			return fmt.Errorf("bind scope %s: work is %s; closed history stays as it is", workID, cur.State)
		}
		if cur.Scope != (envelope.Scope{}) {
			return fmt.Errorf("bind scope %s: work already records its scope", workID)
		}
		if reason := s.scopeInFlight(cur); reason != "" {
			return fmt.Errorf("bind scope %s: %s", workID, reason)
		}
		if scope == (envelope.Scope{}) {
			return fmt.Errorf("bind scope %s: no scope to record", workID)
		}
		sha, err := workEnvelopeSHA256(cur)
		if err != nil {
			return err
		}
		if sha != expectedSHA256 {
			return fmt.Errorf("bind scope %s: work changed since it was planned; plan again", workID)
		}
		ai = s.scopeBindActionIntent(cur, scope, key, actor, now)
		if err := ai.Validate(); err != nil {
			return fmt.Errorf("bind scope %s: action intent invalid: %w", workID, err)
		}
		cfg, _, err := policy.LoadConfig(s.home)
		if err != nil {
			return err
		}
		pd = policy.Evaluate(ai, cfg, now)
		if err := pd.Validate(); err != nil {
			return fmt.Errorf("bind scope %s: policy decision invalid: %w", workID, err)
		}
		if !policy.Allowed(pd) {
			return fmt.Errorf("bind scope %s: policy %s (decision %s)", workID, pd.Outcome, pd.ID)
		}
		cur.Scope = scope
		if err := cur.Validate(); err != nil {
			return fmt.Errorf("bind scope %s: record invalid: %w", workID, err)
		}
		if err := s.writeEventJSON(ai.ID, &ai); err != nil {
			return err
		}
		if err := s.writeEventJSON(pd.ID, &pd); err != nil {
			return err
		}
		if err := s.appendEventUnlocked(Event{
			TS: ts(now), WorkID: workID, From: cur.State, To: cur.State, By: actor,
			Note:     scopeBindNote(scope, key),
			Attached: []string{ai.ID, pd.ID},
		}); err != nil {
			return err
		}
		return s.writeJSON(s.workPath(workID), &cur)
	})
	return ai, pd, err
}

func scopeBindNote(scope envelope.Scope, key string) string {
	parts := []string{"scope bound"}
	if scope.ProductID != "" {
		parts = append(parts, "product_id="+scope.ProductID)
	}
	if scope.ProjectID != "" {
		parts = append(parts, "project_id="+scope.ProjectID)
	}
	return strings.Join(append(parts, "key="+firstNonEmpty(key, "unknown")), " ")
}

func (s *Store) scopeBindActionIntent(w envelope.WorkEnvelope, scope envelope.Scope, key, actor string, now time.Time) envelope.ActionIntent {
	home := firstNonEmpty(scope.ProductID, scope.ProjectID)
	return envelope.ActionIntent{
		Header:        newHeader(envelope.SVActionIntent, idFor("act_", "bind_work_scope", w.ID, home, key, actor, ts(now)), now, w.ID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		Actor:         actor,
		Capability:    "work.bind_scope",
		Action:        "bind_work_scope",
		Target:        envelope.ActionTarget{Kind: "work", ID: w.ID, Path: s.workPath(w.ID)},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("record that work %s is for %s (decided by %s)", w.ID, home, firstNonEmpty(key, "unknown")),
		RollbackPlan:  "clear the scope on the work record with a same-state update",
	}
}
