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
type SourceRev struct {
	Source string `json:"source"`
	Rev    string `json:"rev"`
}
type EffectiveContract struct {
	Header
	Harness            string           `json:"harness"`
	TaskMode           string           `json:"task_mode,omitempty"`
	ActiveInstructions []string         `json:"active_instructions"`
	ActiveHooks        []string         `json:"active_hooks"`
	ActiveDecisions    []string         `json:"active_decisions"`
	MemoryPolicy       MemoryPolicy     `json:"memory_policy"`
	CapabilityPolicy   CapabilityPolicy `json:"capability_policy"`
	MutationPolicy     MutationPolicy   `json:"mutation_policy"`
	SourceRevisions    []SourceRev      `json:"source_revisions"`
	Unknowns           []string         `json:"unknowns"`
	Conflicts          []string         `json:"conflicts"`
}

func (e *EffectiveContract) Kind() string { return "effective_contract" }
func (e *EffectiveContract) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVEffectiveContract, "contract_")
	v.req("harness", e.Harness)
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
type ContextPack struct {
	Header
	SummaryMD          string            `json:"summary_md"`
	Included           []IncludedRef     `json:"included"`
	Excluded           []ExcludedRef     `json:"excluded"`
	CapabilityOffers   []CapabilityOffer `json:"capability_offers,omitempty"`
	ActiveInstructions []string          `json:"active_instructions"`
	ActiveDecisions    []string          `json:"active_decisions"`
	RequiredChecks     []string          `json:"required_checks"`
	Avoid              []string          `json:"avoid"`
}

func (e *ContextPack) Kind() string { return "context_pack" }
func (e *ContextPack) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVContextPack, "ctx_")
	for i, inc := range e.Included {
		v.enum(fmt.Sprintf("included[%d].source_class", i), inc.SourceClass, SourceClasses, true)
		v.enum(fmt.Sprintf("included[%d].status", i), inc.Status, AuthorityStatuses, false)
		v.req(fmt.Sprintf("included[%d].ref", i), inc.Ref)
	}
	for i, offer := range e.CapabilityOffers {
		prefix := fmt.Sprintf("capability_offers[%d]", i)
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
	// the excluded list (what was NOT loaded, and why) is the whole point of replaceability;
	// require its presence (may be empty) so it's never silently dropped.
	if e.Excluded == nil {
		v.addf("excluded must be present (record what was not loaded and why)")
	}
	return v.result(e.Kind())
}

func validateCapabilityOfferConstraints(v *verr, field string, values []CapabilityOfferConstraint) {
	for i, c := range values {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		v.req(prefix+".kind", c.Kind)
		v.req(prefix+".ref", c.Ref)
	}
}

type WorkEnvelope struct {
	Header
	Owner              string   `json:"owner"`
	CanonicalSurface   string   `json:"canonical_surface,omitempty"`
	State              string   `json:"state"`
	DesiredOutcome     string   `json:"desired_outcome,omitempty"`
	SuccessPredicate   string   `json:"success_predicate"`
	NonGoals           []string `json:"non_goals"`
	RouteDecisionID    string   `json:"route_decision_id,omitempty"`
	PolicyDecisionID   string   `json:"policy_decision_id,omitempty"`
	RunIDs             []string `json:"run_ids"`
	ArtifactIDs        []string `json:"artifact_ids"`
	VerificationIDs    []string `json:"verification_ids"`
	MemoryCandidateIDs []string `json:"memory_candidate_ids"`
}

func (e *WorkEnvelope) Kind() string { return "work" }
func (e *WorkEnvelope) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVWork, "work_")
	v.enum("state", e.State, WorkStates, true)
	v.req("owner", e.Owner)
	v.req("success_predicate", e.SuccessPredicate)
	return v.result(e.Kind())
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
	Used           []string `json:"used"`
	Missed         []string `json:"missed"`
	FalseTriggered []string `json:"false_triggered,omitempty"`
	Candidate      []string `json:"candidate"`
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
type Receipt struct {
	Header
	Outcome    string        `json:"outcome"`
	MoveType   string        `json:"move_type"`
	StateDelta StateDelta    `json:"state_delta"`
	Cost       ReceiptCost   `json:"cost"`
	Skills     ReceiptSkills `json:"skills"`
	Memory     ReceiptMemory `json:"memory"`
	Next       ReceiptNext   `json:"next"`
	ProofRefs  []string      `json:"proof_refs,omitempty"`
}

func (e *Receipt) Kind() string { return "receipt" }
func (e *Receipt) Validate() error {
	v := &verr{}
	e.Header.validate(v, SVReceipt, "rec_")
	v.enum("outcome", e.Outcome, ReceiptOutcomes, true)
	v.enum("move_type", e.MoveType, MoveTypes, true)
	proofRefs := append(append([]string{}, e.ProofRefs...), e.StateDelta.Artifacts...)
	proofRefs = append(proofRefs, e.StateDelta.Verification...)
	switch e.MoveType {
	case "outward_delta":
		if e.Outcome == "completed" && !e.StateDelta.Changed && len(nonEmpty(proofRefs)) == 0 {
			v.addf("outward_delta completed requires state_delta.changed=true or proof_refs/artifacts/verification")
		}
	case "memory_delta":
		if len(nonEmpty(append(append(append([]string{}, e.Memory.Candidates...), e.Memory.Promoted...), append(e.Memory.Parked, e.Memory.Rejected...)...))) == 0 {
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
