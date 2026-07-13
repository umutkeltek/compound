// Package envelope is the typed runtime grammar — the spine every harness, hook,
// and run flows through, hung off one root work_id. Every envelope begins with the
// universal Header (which prevents retrieved != relevant != authoritative != current).
// Typed before clever: schemas + validation + round-trip come before behavior.
// See docs/architecture/runtime.md and docs/architecture/contracts/README.md.
package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Schema versions (compound.<kind>.v0.1) and their id prefixes.
const (
	SVEffectiveContract = "compound.effective_contract.v0.1"
	SVContextRequest    = "compound.context_request.v0.1"
	SVContextPack       = "compound.context_pack.v0.1"
	SVWork              = "compound.work.v0.1"
	SVActionIntent      = "compound.action_intent.v0.1"
	SVPolicyDecision    = "compound.policy_decision.v0.1"
	SVHookEvent         = "compound.hook_event.v0.1"
	SVTraceEnvelope     = "compound.trace_envelope.v0.1"
	SVReceipt           = "compound.receipt.v0.1"
	SVTransition        = "compound.transition.v0.1"
)

// Enum sets — the canonical vocabularies referenced across the docs.
var (
	Privacies         = []string{"public", "private", "sensitive", "secret"}
	Risks             = []string{"tier0", "tier1", "tier2", "tier3"}
	SourceClasses     = []string{"source", "generated", "evidence", "review", "runtime", "private_overlay"}
	AuthorityStatuses = []string{"current", "stale", "disputed", "superseded", "unknown"}
	UncertaintyLevels = []string{"known", "inferred", "suspected", "unknown"}
	WorkStates        = []string{"proposed", "admitted", "ready", "claimed", "running", "verifying", "completed", "held", "blocked", "failed", "cancelled"}
	MutationClasses   = []string{"read", "local_reversible", "external_side_effect", "public", "destructive", "financial", "security"}
	Reversibilities   = []string{"none", "easy", "manual", "hard", "irreversible"}
	PolicyOutcomes    = []string{"allow", "allow_with_obligations", "hold", "deny"}
	MoveTypes         = []string{"outward_delta", "memory_delta", "rule_delta", "control_plane_repair", "audit_only"}
	ProofMedia        = []string{"web-ui", "ios", "cli", "api", "none"}
	ProjectionKinds   = []string{"memory", "skill", "product", "cost", "risk", "taste", "routing", "human_attention", "rule", "control_plane"}
	ProjectionRoles   = []string{"offered", "used", "missed", "false_triggered", "validated", "candidate", "promoted", "parked", "rejected", "proof"}
	HookNames         = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionEnd", "Stop"}
	HookStatuses      = []string{"ok", "fail_open", "blocked", "error"}
	ReceiptOutcomes   = []string{"completed", "blocked", "failed", "no_op"}
	RequestedModes    = []string{"observe", "plan", "execute", "review"}
)

func oneOf(val string, set []string) bool {
	for _, s := range set {
		if val == s {
			return true
		}
	}
	return false
}

// verr accumulates validation problems so a caller sees all of them at once.
type verr struct{ errs []string }

func (v *verr) addf(f string, a ...any) { v.errs = append(v.errs, fmt.Sprintf(f, a...)) }
func (v *verr) req(field, val string) {
	if strings.TrimSpace(val) == "" {
		v.addf("%s is required", field)
	}
}
func (v *verr) enum(field, val string, s []string, required bool) {
	if val == "" {
		if required {
			v.addf("%s is required", field)
		}
		return
	}
	if !oneOf(val, s) {
		v.addf("%s=%q not one of %v", field, val, s)
	}
}
func (v *verr) result(kind string) error {
	if len(v.errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s invalid: %s", kind, strings.Join(v.errs, "; "))
}

// --- Universal header ---

type Producer struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
	LaneID  string `json:"lane_id,omitempty"`
}
type IDs struct {
	WorkID        string `json:"work_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	TransitionID  string `json:"transition_id,omitempty"`
	ContextPackID string `json:"context_pack_id,omitempty"`
	ContractID    string `json:"contract_id,omitempty"`
}
type Scope struct {
	EntityID  string `json:"entity_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	Surface   string `json:"surface,omitempty"`
}
type Authority struct {
	SourceClass    string `json:"source_class"`
	Owner          string `json:"owner,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	Status         string `json:"status,omitempty"`
}
type Uncertainty struct {
	Level  string `json:"level"`
	Reason string `json:"reason,omitempty"`
}

// Header is on every envelope. Anonymous-embedded so its JSON fields flatten to top level.
type Header struct {
	SchemaVersion string      `json:"schema_version"`
	ID            string      `json:"id"`
	CreatedAt     string      `json:"created_at"`
	Producer      Producer    `json:"producer"`
	IDs           IDs         `json:"ids"`
	Scope         Scope       `json:"scope"`
	Authority     Authority   `json:"authority"`
	Privacy       string      `json:"privacy"`
	Risk          string      `json:"risk"`
	EvidenceRefs  []string    `json:"evidence_refs"`
	Uncertainty   Uncertainty `json:"uncertainty"`
}

func (h Header) validate(v *verr, expectSV, idPrefix string) {
	if h.SchemaVersion != expectSV {
		v.addf("schema_version=%q, want %q", h.SchemaVersion, expectSV)
	}
	v.req("id", h.ID)
	if h.ID != "" && idPrefix != "" && !strings.HasPrefix(h.ID, idPrefix) {
		v.addf("id %q must start with %q", h.ID, idPrefix)
	}
	v.req("created_at", h.CreatedAt)
	v.enum("privacy", h.Privacy, Privacies, true)
	v.enum("risk", h.Risk, Risks, true)
	v.enum("authority.source_class", h.Authority.SourceClass, SourceClasses, true)
	v.enum("authority.status", h.Authority.Status, AuthorityStatuses, false)
	v.enum("uncertainty.level", h.Uncertainty.Level, UncertaintyLevels, false)
}

// Envelope is any typed runtime object.
type Envelope interface {
	Kind() string
	Validate() error
}

// --- The nine envelopes ---

type MemoryPolicy struct {
	Ambient      []string `json:"ambient"`
	ExplicitOnly []string `json:"explicit_only"`
	Forbidden    []string `json:"forbidden"`
}
type CapabilityPolicy struct {
	ToolsAllowed     []string `json:"tools_allowed"`
	ToolsOnDemand    []string `json:"tools_available_on_demand"`
	MCPsNotByDefault []string `json:"mcps_not_loaded_by_default"`
}
type MutationPolicy struct {
	RequiresActionIntent      bool `json:"requires_action_intent"`
	IrreversibleRequiresHuman bool `json:"irreversible_requires_human"`
}

// TaskContract is the provider- and harness-neutral meaning of one task. It is
// a value carried by the existing envelope spine, not a new envelope or source
// of identity; concrete tools, models, and routes remain compiled outputs.
type TaskContract struct {
	Intent           string   `json:"intent,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	DesiredOutcome   string   `json:"desired_outcome,omitempty"`
	SuccessPredicate string   `json:"success_predicate,omitempty"`
	NonGoals         []string `json:"non_goals,omitempty"`
	ProofMedium      string   `json:"proof_medium,omitempty"`
}

// IsZero lets additive task contracts remain absent on legacy v0.1 artifacts.
func (t TaskContract) IsZero() bool {
	return strings.TrimSpace(t.Intent) == "" &&
		strings.TrimSpace(t.Mode) == "" &&
		strings.TrimSpace(t.DesiredOutcome) == "" &&
		strings.TrimSpace(t.SuccessPredicate) == "" &&
		len(t.NonGoals) == 0 &&
		strings.TrimSpace(t.ProofMedium) == ""
}

func (t TaskContract) Validate() error {
	v := &verr{}
	validateTaskContract(v, "task_contract", t)
	return v.result("task_contract")
}

func validateTaskContract(v *verr, field string, task TaskContract) {
	v.enum(field+".mode", task.Mode, RequestedModes, false)
	v.enum(field+".proof_medium", task.ProofMedium, ProofMedia, false)
}

type HookReadiness struct {
	Harness         string                `json:"harness"`
	Hook            string                `json:"hook"`
	NativeBlock     bool                  `json:"native_block"`
	Installed       bool                  `json:"installed"`
	InstalledRef    string                `json:"installed_ref,omitempty"`
	Status          string                `json:"status"`
	AdapterRequired bool                  `json:"adapter_required,omitempty"`
	AdapterCommand  string                `json:"adapter_command,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	Actions         []HookReadinessAction `json:"actions,omitempty"`
}

type HookReadinessAction struct {
	Kind             string `json:"kind"`
	Label            string `json:"label"`
	Command          string `json:"command,omitempty"`
	ExpectedExitCode int    `json:"expected_exit_code,omitempty"`
	ExpectedStatus   string `json:"expected_status,omitempty"`
	Boundary         string `json:"boundary,omitempty"`
	Proof            string `json:"proof,omitempty"`
	Rollback         string `json:"rollback,omitempty"`
	RequiresOperator bool   `json:"requires_operator,omitempty"`
}

type HookRosterEntry struct {
	Ref     string `json:"ref"`
	Harness string `json:"harness"`
	Hook    string `json:"hook"`
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type SourceRev struct {
	Source string `json:"source"`
	Rev    string `json:"rev"`
}
type EffectiveContract struct {
	Header
	Harness            string            `json:"harness"`
	TaskMode           string            `json:"task_mode,omitempty"`
	TaskContract       TaskContract      `json:"task_contract,omitzero"`
	ActiveInstructions []string          `json:"active_instructions"`
	ActiveHooks        []string          `json:"active_hooks"`
	HookRoster         []HookRosterEntry `json:"hook_roster,omitempty"`
	HookReadiness      []HookReadiness   `json:"hook_readiness,omitempty"`
	ActiveDecisions    []string          `json:"active_decisions"`
	ActiveMemory       []string          `json:"active_memory,omitempty"`
	Excluded           []ExcludedRef     `json:"excluded"`
	CapabilityOffers   []CapabilityOffer `json:"capability_offers,omitempty"`
	MemoryPolicy       MemoryPolicy      `json:"memory_policy"`
	CapabilityPolicy   CapabilityPolicy  `json:"capability_policy"`
	MutationPolicy     MutationPolicy    `json:"mutation_policy"`
	SourceRevisions    []SourceRev       `json:"source_revisions"`
	Unknowns           []string          `json:"unknowns"`
	Conflicts          []string          `json:"conflicts"`
}

func (e *EffectiveContract) Kind() string { return "effective_contract" }
func (e *EffectiveContract) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVEffectiveContract, "contract_")
	v.req("harness", e.Harness)
	validateTaskContract(v, "task_contract", e.TaskContract)
	if taskMode, contractMode := strings.TrimSpace(e.TaskMode), strings.TrimSpace(e.TaskContract.Mode); taskMode != "" && contractMode != "" && taskMode != contractMode {
		v.addf("task_mode %q conflicts with task_contract.mode %q", taskMode, contractMode)
	}
	validateCapabilityOffers(v, "capability_offers", e.CapabilityOffers)
	return v.result(e.Kind())
}

type CtxConstraints struct {
	MaxContextChars         int  `json:"max_context_chars"`
	AllowPrivateMemory      bool `json:"allow_private_memory"`
	AllowRawCorpus          bool `json:"allow_raw_corpus"`
	AllowProvisionalReviews bool `json:"allow_provisional_reviews"`
}
type ContextRequest struct {
	Header
	IntentTextRef string         `json:"intent_text_ref,omitempty"`
	Harness       string         `json:"harness"`
	Cwd           string         `json:"cwd,omitempty"`
	TaskGuess     string         `json:"task_guess,omitempty"`
	RequestedMode string         `json:"requested_mode,omitempty"`
	Constraints   CtxConstraints `json:"constraints"`
}

func (e *ContextRequest) Kind() string { return "context_request" }
func (e *ContextRequest) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVContextRequest, "ctxreq_")
	v.req("harness", e.Harness)
	v.enum("requested_mode", e.RequestedMode, RequestedModes, false)
	return v.result(e.Kind())
}

type IncludedRef struct {
	Ref         string  `json:"ref"`
	SourceClass string  `json:"source_class"`
	Reason      string  `json:"reason"`
	Confidence  float64 `json:"confidence"`
	Status      string  `json:"status"`
}
type ExcludedRef struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}
type CapabilityOfferConstraint struct {
	Kind       string  `json:"kind"`
	Ref        string  `json:"ref"`
	Detail     string  `json:"detail,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}
type CapabilityOffer struct {
	Ref                   string                      `json:"ref"`
	Kind                  string                      `json:"kind"`
	Name                  string                      `json:"name"`
	Score                 float64                     `json:"score"`
	Why                   string                      `json:"why"`
	Required              []CapabilityOfferConstraint `json:"required,omitempty"`
	Conflicts             []CapabilityOfferConstraint `json:"conflicts,omitempty"`
	Harnesses             []CapabilityOfferConstraint `json:"harnesses,omitempty"`
	KnownMissedWhen       []CapabilityOfferConstraint `json:"known_missed_when,omitempty"`
	KnownFalseTriggeredBy []CapabilityOfferConstraint `json:"known_false_triggered_by,omitempty"`
	Evidence              []CapabilityOfferConstraint `json:"evidence,omitempty"`
	CostRiskNote          string                      `json:"cost_risk_note,omitempty"`
}

// ActivePosition is the folded current stance digest rendered into ContextPack
// active memory. Line is the canonical compact shape hooks display:
// position-id [predicate=P, claim_type=T] → stance (since-seq N, chain-length L)
// [contested]. Ref carries narrow fact-slot identity while Topic remains the
// broad aggregation axis.
type ActivePosition struct {
	Ref         string `json:"ref"`
	Entity      string `json:"entity"`
	Topic       string `json:"topic"`
	Stance      string `json:"stance"`
	SinceSeq    int64  `json:"since_seq"`
	ChainLength int    `json:"chain_length"`
	Contested   bool   `json:"contested,omitempty"`
	Line        string `json:"line"`
}

// NegativeCard is one already-tried-and-failed approach surfaced at orient time,
// scoped to the incoming task (compound.negatives.v0.1 projection —
// internal/negatives, joined over inbox/failure-patterns + reviews/route-remorse).
// Display-only: read by ContextPack consumers, never by route/dispatch/claim
// decisions. WhatWasTried/WhyItFailed are reduced to "" at build time when the
// source free text looked operator-personal — ApproachKey and Ref remain so the
// card still cites its source.
type NegativeCard struct {
	Source       string  `json:"source"` // "failure_pattern" | "route_remorse"
	ApproachKey  string  `json:"approach_key"`
	WhatWasTried string  `json:"what_was_tried,omitempty"`
	WhyItFailed  string  `json:"why_it_failed,omitempty"`
	Ref          string  `json:"ref"`
	Confidence   float64 `json:"confidence,omitempty"`
}

type ContextPack struct {
	Header
	SummaryMD          string            `json:"summary_md"`
	TaskContract       TaskContract      `json:"task_contract,omitzero"`
	Included           []IncludedRef     `json:"included"`
	Excluded           []ExcludedRef     `json:"excluded"`
	CapabilityOffers   []CapabilityOffer `json:"capability_offers,omitempty"`
	ActiveInstructions []string          `json:"active_instructions"`
	ActiveHooks        []string          `json:"active_hooks"`
	HookRoster         []HookRosterEntry `json:"hook_roster,omitempty"`
	HookReadiness      []HookReadiness   `json:"hook_readiness,omitempty"`
	ActiveDecisions    []string          `json:"active_decisions"`
	ActiveMemory       []string          `json:"active_memory,omitempty"`
	ActivePositions    []ActivePosition  `json:"active_positions,omitempty"`
	Negatives          []NegativeCard    `json:"negatives,omitempty"`
	SourceRevisions    []SourceRev       `json:"source_revisions,omitempty"`
	RequiredChecks     []string          `json:"required_checks"`
	Avoid              []string          `json:"avoid"`
}

func (e *ContextPack) Kind() string { return "context_pack" }
func (e *ContextPack) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVContextPack, "ctx_")
	validateTaskContract(v, "task_contract", e.TaskContract)
	for i, inc := range e.Included {
		v.enum(fmt.Sprintf("included[%d].source_class", i), inc.SourceClass, SourceClasses, true)
		v.enum(fmt.Sprintf("included[%d].status", i), inc.Status, AuthorityStatuses, false)
		v.req(fmt.Sprintf("included[%d].ref", i), inc.Ref)
	}
	validateCapabilityOffers(v, "capability_offers", e.CapabilityOffers)
	// the excluded list (what was NOT loaded, and why) is the whole point of replaceability;
	// require its presence (may be empty) so it's never silently dropped.
	if e.Excluded == nil {
		v.addf("excluded must be present (record what was not loaded and why)")
	}
	return v.result(e.Kind())
}

func validateCapabilityOffers(v *verr, field string, values []CapabilityOffer) {
	for i, offer := range values {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		v.req(prefix+".ref", offer.Ref)
		v.req(prefix+".kind", offer.Kind)
		v.req(prefix+".name", offer.Name)
		validateCapabilityOfferConstraints(v, prefix+".required", offer.Required)
		validateCapabilityOfferConstraints(v, prefix+".conflicts", offer.Conflicts)
		validateCapabilityOfferConstraints(v, prefix+".harnesses", offer.Harnesses)
		validateCapabilityOfferConstraints(v, prefix+".known_missed_when", offer.KnownMissedWhen)
		validateCapabilityOfferConstraints(v, prefix+".known_false_triggered_by", offer.KnownFalseTriggeredBy)
		validateCapabilityOfferConstraints(v, prefix+".evidence", offer.Evidence)
	}
}

func validateCapabilityOfferConstraints(v *verr, field string, values []CapabilityOfferConstraint) {
	for i, c := range values {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		v.req(prefix+".kind", c.Kind)
		v.req(prefix+".ref", c.Ref)
	}
}

// GitDeliveryContract binds source-controlled completion to one exact local
// target and one explicit publication policy. The bound worktree is authority;
// land-check callers may assert it, but may never replace it.
type GitDeliveryContract struct {
	TargetWorktreeRoot string `json:"target_worktree_root"`
	TargetLocalRef     string `json:"target_local_ref"`
	ExpectedBaseSHA    string `json:"expected_base_sha"`
	PublicationMode    string `json:"publication_mode"`
	PublicationRef     string `json:"publication_ref,omitempty"`
}

type WorkEnvelope struct {
	Header
	Owner            string `json:"owner"`
	CanonicalSurface string `json:"canonical_surface,omitempty"`
	// TargetSurface, when set, marks this work as an explicit harness-edit proposal
	// aimed at a named editable surface (EVOLVE-BLOCK). It is OPTIONAL and additive: work
	// without it is ordinary work. A system-originated proposal that declares a
	// TargetSurface is admitted only while that surface is actively whitelisted in the
	// policy-owned editable-surfaces registry (see internal/policy/editable_surfaces.go).
	TargetSurface string `json:"target_surface,omitempty"`
	// ExpectedBaseSHA is the legacy, incomplete pre-contract field. It remains
	// readable so existing envelopes do not become corrupt, but new admissions
	// must use GitDelivery and completed commit-bearing work fails closed without it.
	ExpectedBaseSHA    string               `json:"expected_base_sha,omitempty"`
	GitDelivery        *GitDeliveryContract `json:"git_delivery,omitempty"`
	State              string               `json:"state"`
	DesiredOutcome     string               `json:"desired_outcome,omitempty"`
	SuccessPredicate   string               `json:"success_predicate"`
	NonGoals           []string             `json:"non_goals"`
	ProofMedium        string               `json:"proof_medium,omitempty"`
	RouteDecisionID    string               `json:"route_decision_id,omitempty"`
	PolicyDecisionID   string               `json:"policy_decision_id,omitempty"`
	RunIDs             []string             `json:"run_ids"`
	ArtifactIDs        []string             `json:"artifact_ids"`
	VerificationIDs    []string             `json:"verification_ids"`
	MemoryCandidateIDs []string             `json:"memory_candidate_ids"`
}

func (e *WorkEnvelope) Kind() string { return "work" }
func (e *WorkEnvelope) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVWork, "work_")
	v.enum("state", e.State, WorkStates, true)
	v.enum("proof_medium", e.ProofMedium, ProofMedia, false)
	v.req("owner", e.Owner)
	v.req("success_predicate", e.SuccessPredicate)
	if base := strings.TrimSpace(e.ExpectedBaseSHA); base != "" && !validFullGitObjectID(base) {
		v.addf("expected_base_sha must be a lowercase full Git object id (40 or 64 hex characters)")
	}
	if e.GitDelivery != nil {
		validateGitDeliveryContract(v, *e.GitDelivery)
	}
	return v.result(e.Kind())
}

func validateGitDeliveryContract(v *verr, contract GitDeliveryContract) {
	root := strings.TrimSpace(contract.TargetWorktreeRoot)
	if root == "" {
		v.addf("git_delivery.target_worktree_root is required")
	} else {
		if !filepath.IsAbs(root) {
			v.addf("git_delivery.target_worktree_root must be absolute")
		}
		if filepath.Clean(root) != root {
			v.addf("git_delivery.target_worktree_root must be a clean canonical path")
		}
	}
	localRef := strings.TrimSpace(contract.TargetLocalRef)
	if !validBoundGitRef(localRef, "refs/heads/") {
		v.addf("git_delivery.target_local_ref must be a canonical refs/heads/* ref")
	}
	if !validFullGitObjectID(strings.TrimSpace(contract.ExpectedBaseSHA)) {
		v.addf("git_delivery.expected_base_sha must be a lowercase full Git object id (40 or 64 hex characters)")
	}
	switch strings.TrimSpace(contract.PublicationMode) {
	case "required":
		publicationRef := strings.TrimSpace(contract.PublicationRef)
		if !validBoundGitRef(publicationRef, "refs/remotes/") {
			v.addf("git_delivery.publication_ref must be a canonical remote-tracking refs/remotes/* ref")
		} else if validBoundGitRef(localRef, "refs/heads/") {
			branch := strings.TrimPrefix(localRef, "refs/heads/")
			if !strings.HasSuffix(publicationRef, "/"+branch) {
				v.addf("git_delivery.publication_ref must track the target_local_ref branch")
			}
		}
	case "local_only":
		if strings.TrimSpace(contract.PublicationRef) != "" {
			v.addf("git_delivery.publication_ref must be empty when publication_mode is local_only")
		}
	default:
		v.addf("git_delivery.publication_mode must be required or local_only")
	}
}

func validBoundGitRef(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) {
		return false
	}
	if strings.ContainsAny(value, " ~^:?*[\\") || strings.Contains(value, "..") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, ".lock") || strings.Contains(value, "@{") {
		return false
	}
	return true
}

func validFullGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

type ActionTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}
type EstimatedCost struct {
	USD            *float64 `json:"usd"`
	Tokens         *int     `json:"tokens"`
	HumanReviewMin *int     `json:"human_review_minutes"`
}
type ActionIntent struct {
	Header
	Actor         string        `json:"actor"`
	Capability    string        `json:"capability"`
	Action        string        `json:"action"`
	Target        ActionTarget  `json:"target"`
	MutationClass string        `json:"mutation_class"`
	Reversibility string        `json:"reversibility"`
	BlastRadius   string        `json:"blast_radius,omitempty"`
	EstimatedCost EstimatedCost `json:"estimated_cost"`
	Preview       string        `json:"preview,omitempty"`
	RollbackPlan  string        `json:"rollback_plan,omitempty"`
}

func (e *ActionIntent) Kind() string { return "action_intent" }
func (e *ActionIntent) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVActionIntent, "act_")
	v.req("capability", e.Capability)
	v.enum("mutation_class", e.MutationClass, MutationClasses, true)
	v.enum("reversibility", e.Reversibility, Reversibilities, true)
	if e.MutationClass != "" && e.MutationClass != "read" {
		v.req("target.kind", e.Target.Kind)
		if strings.TrimSpace(e.Target.ID) == "" && strings.TrimSpace(e.Target.Path) == "" {
			v.addf("target.id or target.path is required for mutating action")
		}
	}
	if e.IDs.WorkID == "" {
		v.addf("ids.work_id is required (an action belongs to a work)")
	}
	return v.result(e.Kind())
}

type PolicyDecision struct {
	Header
	ActionIntentID  string   `json:"action_intent_id"`
	Outcome         string   `json:"outcome"`
	Reasons         []string `json:"reasons"`
	Obligations     []string `json:"obligations"`
	StandingGrantID string   `json:"standing_grant_id,omitempty"`
	ExpiresAt       string   `json:"expires_at,omitempty"`
}

func (e *PolicyDecision) Kind() string { return "policy_decision" }
func (e *PolicyDecision) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVPolicyDecision, "pol_")
	v.req("action_intent_id", e.ActionIntentID)
	v.enum("outcome", e.Outcome, PolicyOutcomes, true)
	if e.Outcome == "allow_with_obligations" && len(e.Obligations) == 0 {
		v.addf("outcome=allow_with_obligations requires at least one obligation")
	}
	return v.result(e.Kind())
}

type HookEvent struct {
	Header
	Harness        string   `json:"harness"`
	HookName       string   `json:"hook_name"`
	HookCapability string   `json:"hook_capability,omitempty"`
	InputHash      string   `json:"input_hash,omitempty"`
	SideEffects    []string `json:"side_effects"`
	OutputRef      string   `json:"output_ref,omitempty"`
	DurationMS     int      `json:"duration_ms"`
	Status         string   `json:"status"`
}

func (e *HookEvent) Kind() string { return "hook_event" }
func (e *HookEvent) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVHookEvent, "hook_")
	v.req("harness", e.Harness)
	v.enum("hook_name", e.HookName, HookNames, true)
	v.enum("status", e.Status, HookStatuses, true)
	return v.result(e.Kind())
}

type TraceSource struct {
	System      string `json:"system"`
	Stream      string `json:"stream,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
	SourceKind  string `json:"source_kind,omitempty"`
	EventClass  string `json:"event_class,omitempty"`
	RawRef      string `json:"raw_ref,omitempty"`
	Line        int    `json:"line,omitempty"`
	Quality     string `json:"quality,omitempty"`
	MutationRaw string `json:"mutation_raw,omitempty"`
}

type TraceEnvelope struct {
	Header
	Source          TraceSource `json:"source"`
	Actor           string      `json:"actor,omitempty"`
	StartedAt       string      `json:"started_at,omitempty"`
	EndedAt         string      `json:"ended_at,omitempty"`
	MutationClass   string      `json:"mutation_class"`
	Summary         string      `json:"summary,omitempty"`
	ToolsModelsUsed []string    `json:"tools_models_used"`
	FilesTouched    []string    `json:"files_touched"`
	Outputs         []string    `json:"outputs"`
	Cost            ReceiptCost `json:"cost"`
}

func (e *TraceEnvelope) Kind() string { return "trace_envelope" }
func (e *TraceEnvelope) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVTraceEnvelope, "trace_")
	v.req("source.system", e.Source.System)
	v.enum("mutation_class", e.MutationClass, MutationClasses, true)
	return v.result(e.Kind())
}

type StateDelta struct {
	Changed      bool     `json:"changed"`
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed"`
	Artifacts    []string `json:"artifacts"`
	Verification []string `json:"verification"`
}
type ReceiptCost struct {
	ModelCalls   []string `json:"model_calls"`
	Tokens       *int     `json:"tokens"`
	USDActual    *float64 `json:"usd_actual"`
	USDEstimated *float64 `json:"usd_estimated"`
	HumanMinutes *int     `json:"human_minutes"`
}
type ReceiptSkills struct {
	Used   []string `json:"used"`
	Missed []string `json:"missed"`
	// UsedDeclared is the honesty flag for the injected-ref reinforcement projection:
	// true means this receipt made an AFFIRMATIVE statement about what was used (a close
	// that passed --used, or a session-end whose consumption detector ran), so an injected
	// ref ABSENT from Used counts as IGNORED. false (the additive default for old receipts
	// and closes that never declared) means Used is silent — absence is UNKNOWN, never
	// ignored. Additive/omitempty: absence decodes to false and changes no prior behavior.
	UsedDeclared        bool     `json:"used_declared,omitempty"`
	FalseTriggered      []string `json:"false_triggered,omitempty"`
	Candidate           []string `json:"candidate"`
	CapabilitiesOffered []string `json:"capabilities_offered,omitempty"`
}
type ReceiptMemory struct {
	Candidates []string `json:"candidates"`
	Promoted   []string `json:"promoted"`
	Parked     []string `json:"parked"`
	Rejected   []string `json:"rejected,omitempty"`
}
type ReceiptNext struct {
	SuggestedWork []string `json:"suggested_work"`
	ReviewTrigger string   `json:"review_trigger,omitempty"`
}

// FailureCause is the OPTIONAL causal-failure annotation on a close receipt:
// what the operator/agent observed (surface_outcome) and the mechanism they
// attribute it to. Additive and omitempty — absence changes no existing behavior;
// present, it lets the usage projection aggregate why work failed.
type FailureCause struct {
	SurfaceOutcome string `json:"surface_outcome,omitempty"`
	Mechanism      string `json:"mechanism,omitempty"`
}
type Receipt struct {
	Header
	Outcome      string        `json:"outcome"`
	MoveType     string        `json:"move_type"`
	StateDelta   StateDelta    `json:"state_delta"`
	Cost         ReceiptCost   `json:"cost"`
	Skills       ReceiptSkills `json:"skills"`
	Memory       ReceiptMemory `json:"memory"`
	Next         ReceiptNext   `json:"next"`
	ProofRefs    []string      `json:"proof_refs,omitempty"`
	FailureCause *FailureCause `json:"failure_cause,omitempty"`
}

func (e *Receipt) Kind() string { return "receipt" }

func (e Receipt) EffectiveChanged() bool {
	if e.StateDelta.Changed {
		return true
	}
	if e.Outcome != "completed" {
		return false
	}
	proofRefs := e.proofRefs()
	switch e.MoveType {
	case "memory_delta":
		return len(nonEmpty(e.memoryRefs())) > 0
	case "rule_delta":
		return hasPrefixedProof(proofRefs, []string{"failure:", "failure_pattern:", "rule:", "check:", "guard:"})
	case "control_plane_repair", "outward_delta":
		return len(nonEmpty(proofRefs)) > 0
	default:
		return false
	}
}

func (e Receipt) proofRefs() []string {
	proofRefs := append([]string{}, e.ProofRefs...)
	proofRefs = append(proofRefs, e.StateDelta.Artifacts...)
	proofRefs = append(proofRefs, e.StateDelta.Verification...)
	return proofRefs
}

func (e Receipt) memoryRefs() []string {
	refs := append([]string{}, e.Memory.Candidates...)
	refs = append(refs, e.Memory.Promoted...)
	refs = append(refs, e.Memory.Parked...)
	refs = append(refs, e.Memory.Rejected...)
	return refs
}

func (e *Receipt) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVReceipt, "rec_")
	v.enum("outcome", e.Outcome, ReceiptOutcomes, true)
	v.enum("move_type", e.MoveType, MoveTypes, true)
	proofRefs := e.proofRefs()
	switch e.MoveType {
	case "outward_delta":
		if e.Outcome == "completed" && !e.StateDelta.Changed && len(nonEmpty(proofRefs)) == 0 {
			v.addf("outward_delta completed requires state_delta.changed=true or proof_refs/artifacts/verification")
		}
	case "memory_delta":
		if len(nonEmpty(e.memoryRefs())) == 0 {
			v.addf("memory_delta requires memory candidate/promoted/parked/rejected ref")
		}
	case "rule_delta":
		if !hasPrefixedProof(proofRefs, []string{"failure:", "failure_pattern:", "rule:", "check:", "guard:"}) {
			v.addf("rule_delta requires failure/rule/check/guard proof_ref")
		}
	case "control_plane_repair":
		if len(nonEmpty(proofRefs)) == 0 {
			v.addf("control_plane_repair requires repair/retirement/gate proof_ref, artifact, or verification")
		}
	}
	return v.result(e.Kind())
}

func nonEmpty(values []string) []string {
	out := []string{}
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func hasPrefixedProof(values []string, prefixes []string) bool {
	for _, v := range nonEmpty(values) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(v, prefix) {
				return true
			}
		}
	}
	return false
}

type ProjectionRef struct {
	Kind        string  `json:"kind"`
	Ref         string  `json:"ref"`
	Role        string  `json:"role"`
	EvidenceRef string  `json:"evidence_ref,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
	Status      string  `json:"status,omitempty"`
}

type Projections struct {
	Memory         []string        `json:"memory"`
	Skill          []string        `json:"skill"`
	Product        []string        `json:"product"`
	Cost           []string        `json:"cost"`
	Risk           []string        `json:"risk"`
	Taste          []string        `json:"taste"`
	Routing        []string        `json:"routing"`
	HumanAttention []string        `json:"human_attention"`
	Typed          []ProjectionRef `json:"typed,omitempty"`
}
type SecondOrder struct {
	FutureBehaviorChanged bool   `json:"future_behavior_changed"`
	ShouldHarden          bool   `json:"should_harden"`
	ShouldCreateRule      bool   `json:"should_create_rule"`
	ShouldDecay           bool   `json:"should_decay"`
	RevisitAt             string `json:"revisit_at,omitempty"`
}
type Transition struct {
	Header
	SourceTraceID string      `json:"source_trace_id,omitempty"`
	BeforeRefs    []string    `json:"before_refs"`
	ActionRefs    []string    `json:"action_refs"`
	AfterRefs     []string    `json:"after_refs"`
	StateDelta    string      `json:"state_delta,omitempty"`
	Projections   Projections `json:"projections"`
	SecondOrder   SecondOrder `json:"second_order"`
}

func (e *Transition) Kind() string { return "transition" }
func (e *Transition) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVTransition, "tr_")
	if e.IDs.WorkID == "" {
		v.addf("ids.work_id is required (a transition belongs to a work)")
	}
	for i, p := range e.Projections.Typed {
		v.enum(fmt.Sprintf("projections.typed[%d].kind", i), p.Kind, ProjectionKinds, true)
		v.enum(fmt.Sprintf("projections.typed[%d].role", i), p.Role, ProjectionRoles, true)
		v.req(fmt.Sprintf("projections.typed[%d].ref", i), p.Ref)
	}
	return v.result(e.Kind())
}

// --- Registry / dispatch ---

var registry = map[string]func() Envelope{
	SVEffectiveContract: func() Envelope { return &EffectiveContract{} },
	SVContextRequest:    func() Envelope { return &ContextRequest{} },
	SVContextPack:       func() Envelope { return &ContextPack{} },
	SVWork:              func() Envelope { return &WorkEnvelope{} },
	SVActionIntent:      func() Envelope { return &ActionIntent{} },
	SVPolicyDecision:    func() Envelope { return &PolicyDecision{} },
	SVHookEvent:         func() Envelope { return &HookEvent{} },
	SVTraceEnvelope:     func() Envelope { return &TraceEnvelope{} },
	SVReceipt:           func() Envelope { return &Receipt{} },
	SVTransition:        func() Envelope { return &Transition{} },
}

// New returns a zero envelope for a schema_version, or nil if unknown.
func New(schemaVersion string) Envelope {
	if ctor, ok := registry[schemaVersion]; ok {
		return ctor()
	}
	return nil
}

// Kinds lists "schema_version  kind" for every registered envelope, sorted.
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for sv, ctor := range registry {
		out = append(out, fmt.Sprintf("%-36s %s", sv, ctor().Kind()))
	}
	sort.Strings(out)
	return out
}

// ValidateBytes detects the kind from schema_version, decodes into the concrete
// type, and validates. Returns the kind (best-effort) and any error.
func ValidateBytes(data []byte) (string, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("not valid json: %w", err)
	}
	if probe.SchemaVersion == "" {
		return "", errors.New("missing schema_version")
	}
	e := New(probe.SchemaVersion)
	if e == nil {
		return probe.SchemaVersion, fmt.Errorf("unknown schema_version %q", probe.SchemaVersion)
	}
	if err := json.Unmarshal(data, e); err != nil {
		return e.Kind(), fmt.Errorf("decode: %w", err)
	}
	return e.Kind(), e.Validate()
}

// RoundTrip marshals an envelope, decodes it back into a fresh instance, and
// asserts byte-identical re-marshal — proving the schema is lossless.
func RoundTrip(e Envelope) error {
	b1, err := json.Marshal(e)
	if err != nil {
		return err
	}
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	_ = json.Unmarshal(b1, &probe)
	e2 := New(probe.SchemaVersion)
	if e2 == nil {
		return fmt.Errorf("round-trip: unregistered schema_version %q", probe.SchemaVersion)
	}
	if err := json.Unmarshal(b1, e2); err != nil {
		return err
	}
	b2, err := json.Marshal(e2)
	if err != nil {
		return err
	}
	if !bytes.Equal(b1, b2) {
		return fmt.Errorf("round-trip mismatch for %s", e.Kind())
	}
	return nil
}
