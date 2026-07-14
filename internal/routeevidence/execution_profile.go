package routeevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ExecutionProfilePlanSchema = "compound.execution_profile_plan.v0.1"
	UnknownUntilReceipt        = "unknown_until_receipt"
)

const (
	dispositionExact      = "exact"
	dispositionTranslated = "translated"
)

type ExecutionProfileIntent struct {
	ProfileID           string `json:"profile_id"`
	Role                string `json:"role,omitempty"`
	CapabilityClass     string `json:"capability_class,omitempty"`
	ClientHarnessID     string `json:"client_harness_id,omitempty"`
	TransportID         string `json:"transport_id,omitempty"`
	RouteAlias          string `json:"route_alias,omitempty"`
	ModelConstraint     string `json:"model_constraint,omitempty"`
	SemanticEffort      string `json:"semantic_effort,omitempty"`
	NativeEffort        string `json:"native_effort,omitempty"`
	OrchestrationPolicy string `json:"orchestration_policy,omitempty"`
}

type ExecutionBudget struct {
	MaxRequests        int     `json:"max_requests"`
	MaxInputTokens     int     `json:"max_input_tokens"`
	MaxOutputTokens    int     `json:"max_output_tokens"`
	MaxDurationSeconds int     `json:"max_duration_seconds"`
	MaxUSD             float64 `json:"max_usd,omitempty"`
	MaxParallelism     int     `json:"max_parallelism"`
}

type ExecutionCheckpointPolicy struct {
	Required       bool `json:"required"`
	EveryRequests  int  `json:"every_requests,omitempty"`
	PersistPartial bool `json:"persist_partial"`
}

type NativeEffortRequest struct {
	Requested   string `json:"requested"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

type ExecutionProfileCandidate struct {
	ClientHarnessID string `json:"client_harness_id"`
	TransportID     string `json:"transport_id"`
	SemanticEffort  string `json:"semantic_effort"`
	NativeEffort    string `json:"native_effort"`
	Terminal        bool   `json:"terminal"`
	Explanation     string `json:"explanation"`
}

type ExecutionIndependenceRequirement struct {
	CrossModelRequired bool   `json:"cross_model_required"`
	Basis              string `json:"basis,omitempty"`
}

type ExecutionProfileDefinition struct {
	ID                    string                           `json:"id"`
	Role                  string                           `json:"role"`
	CapabilityClass       string                           `json:"capability_class"`
	DefaultClientHarness  string                           `json:"default_client_harness_id"`
	DefaultTransport      string                           `json:"default_transport_id"`
	SemanticEffort        string                           `json:"semantic_effort"`
	OrchestrationPolicy   string                           `json:"orchestration_policy"`
	NativeEffortByHarness map[string]string                `json:"native_effort_by_harness"`
	Budget                ExecutionBudget                  `json:"budget"`
	Checkpoint            ExecutionCheckpointPolicy        `json:"checkpoint"`
	Candidates            []ExecutionProfileCandidate      `json:"candidates"`
	Independence          ExecutionIndependenceRequirement `json:"independence_requirement"`
	Explanation           string                           `json:"explanation"`
}

type ExecutionProfilePlan struct {
	SchemaVersion           string                           `json:"schema_version"`
	ID                      string                           `json:"id"`
	RequestedIntent         ExecutionProfileIntent           `json:"requested_intent"`
	ProfileID               string                           `json:"profile_id"`
	Role                    string                           `json:"role"`
	CapabilityClass         string                           `json:"capability_class"`
	ClientHarnessID         string                           `json:"client_harness_id"`
	TransportID             string                           `json:"transport_id"`
	RouteAlias              string                           `json:"route_alias,omitempty"`
	ModelConstraint         string                           `json:"model_constraint,omitempty"`
	SemanticEffort          string                           `json:"semantic_effort"`
	NativeEffort            NativeEffortRequest              `json:"native_effort"`
	OrchestrationPolicy     string                           `json:"orchestration_policy"`
	Disposition             string                           `json:"disposition"`
	Budget                  ExecutionBudget                  `json:"budget"`
	Checkpoint              ExecutionCheckpointPolicy        `json:"checkpoint"`
	Candidates              []ExecutionProfileCandidate      `json:"candidates"`
	CandidateExplanation    string                           `json:"candidate_explanation"`
	ParentPlanID            string                           `json:"parent_plan_id,omitempty"`
	InheritedCeilings       []string                         `json:"inherited_ceilings,omitempty"`
	IndependenceRequirement ExecutionIndependenceRequirement `json:"independence_requirement"`
	EffectiveProvider       string                           `json:"effective_provider"`
	EffectiveModel          string                           `json:"effective_model"`
	EffectiveNativeEffort   string                           `json:"effective_native_effort"`
	Warnings                []string                         `json:"warnings,omitempty"`
}

type ChildExecutionProfileIntent struct {
	Role                string          `json:"role,omitempty"`
	CapabilityClass     string          `json:"capability_class,omitempty"`
	ClientHarnessID     string          `json:"client_harness_id"`
	TransportID         string          `json:"transport_id"`
	RouteAlias          string          `json:"route_alias,omitempty"`
	ModelConstraint     string          `json:"model_constraint,omitempty"`
	SemanticEffort      string          `json:"semantic_effort"`
	NativeEffort        string          `json:"native_effort,omitempty"`
	OrchestrationPolicy string          `json:"orchestration_policy,omitempty"`
	Budget              ExecutionBudget `json:"budget"`
}

type ObservedExecutionIdentity struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ExecutionProfileCompatibilityError struct {
	Path   string
	Reason string
}

func (e *ExecutionProfileCompatibilityError) Error() string {
	return e.Path + ": " + e.Reason
}

func ListExecutionProfiles() []ExecutionProfileDefinition {
	profiles := []ExecutionProfileDefinition{
		{
			ID: "interactive-default", Role: "interactive", CapabilityClass: "interactive",
			DefaultClientHarness: "claude-code", DefaultTransport: "native",
			SemanticEffort: "balanced", OrchestrationPolicy: "standard",
			NativeEffortByHarness: map[string]string{"claude-code": "medium", "codex": "medium", "worker": "medium"},
			Budget:                ExecutionBudget{MaxRequests: 8, MaxInputTokens: 120_000, MaxOutputTokens: 24_000, MaxDurationSeconds: 1_200, MaxUSD: 5, MaxParallelism: 1},
			Checkpoint:            ExecutionCheckpointPolicy{},
			Explanation:           "interactive work favors a bounded balanced request and standard orchestration",
		},
		{
			ID: "coding-implementation", Role: "executor", CapabilityClass: "build-implement",
			DefaultClientHarness: "codex", DefaultTransport: "native",
			SemanticEffort: "deep", OrchestrationPolicy: "standard",
			NativeEffortByHarness: map[string]string{"claude-code": "xhigh", "codex": "xhigh", "worker": "high"},
			Budget:                ExecutionBudget{MaxRequests: 40, MaxInputTokens: 1_000_000, MaxOutputTokens: 200_000, MaxDurationSeconds: 7_200, MaxUSD: 25, MaxParallelism: 2},
			Checkpoint:            ExecutionCheckpointPolicy{},
			Explanation:           "implementation maps semantic depth through the selected harness without opting into ultracode",
		},
		{
			ID: "mining-bulk", Role: "miner", CapabilityClass: "classify-extract",
			DefaultClientHarness: "codex", DefaultTransport: "switchback",
			SemanticEffort: "economy", OrchestrationPolicy: "standard",
			NativeEffortByHarness: map[string]string{"claude-code": "low", "codex": "low", "worker": "low"},
			Budget:                ExecutionBudget{MaxRequests: 500, MaxInputTokens: 10_000_000, MaxOutputTokens: 2_000_000, MaxDurationSeconds: 14_400, MaxUSD: 30, MaxParallelism: 16},
			Checkpoint:            ExecutionCheckpointPolicy{Required: true, EveryRequests: 20, PersistPartial: true},
			Candidates:            []ExecutionProfileCandidate{{ClientHarnessID: "codex", TransportID: "switchback", SemanticEffort: "economy", NativeEffort: "low", Terminal: true, Explanation: "bounded Codex client over Switchback transport; no provider call is made by resolution"}},
			Explanation:           "bulk mining prefers cheap transport, bounded parallelism, and persistent checkpoints",
		},
		{
			ID: "judge-critique", Role: "judge", CapabilityClass: "judge-critique",
			DefaultClientHarness: "claude-code", DefaultTransport: "native",
			SemanticEffort: "deep", OrchestrationPolicy: "standard",
			NativeEffortByHarness: map[string]string{"claude-code": "xhigh", "codex": "xhigh", "worker": "high"},
			Budget:                ExecutionBudget{MaxRequests: 20, MaxInputTokens: 400_000, MaxOutputTokens: 80_000, MaxDurationSeconds: 3_600, MaxUSD: 20, MaxParallelism: 3},
			Checkpoint:            ExecutionCheckpointPolicy{},
			Independence:          ExecutionIndependenceRequirement{CrossModelRequired: true, Basis: "terminal critique requires distinct observed provider/model identities; free lanes are tripwire-only"},
			Explanation:           "critique uses deep semantic effort; free candidates may object but cannot terminate judgment",
		},
		{
			ID: "orchestration-ultracode", Role: "orchestrator", CapabilityClass: "plan-decompose",
			DefaultClientHarness: "codex", DefaultTransport: "native",
			SemanticEffort: "deep", OrchestrationPolicy: "ultracode",
			NativeEffortByHarness: map[string]string{"claude-code": "xhigh", "codex": "xhigh", "worker": "high"},
			Budget:                ExecutionBudget{MaxRequests: 80, MaxInputTokens: 2_000_000, MaxOutputTokens: 400_000, MaxDurationSeconds: 10_800, MaxUSD: 50, MaxParallelism: 4},
			Checkpoint:            ExecutionCheckpointPolicy{Required: true, EveryRequests: 20, PersistPartial: true},
			Explanation:           "ultracode is an explicit orchestration policy, never a model or native effort value",
		},
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles
}

func ResolveExecutionProfile(intent ExecutionProfileIntent) (ExecutionProfilePlan, error) {
	requested := intent
	definition, ok := executionProfileDefinition(strings.TrimSpace(intent.ProfileID))
	if !ok {
		return ExecutionProfilePlan{}, compatibilityError("requested_intent.profile_id", "unknown execution profile "+quoted(intent.ProfileID))
	}

	intent.ProfileID = definition.ID
	intent.Role = defaultString(intent.Role, definition.Role)
	intent.CapabilityClass = defaultString(intent.CapabilityClass, definition.CapabilityClass)
	intent.ClientHarnessID = defaultString(intent.ClientHarnessID, definition.DefaultClientHarness)
	intent.TransportID = defaultString(intent.TransportID, definition.DefaultTransport)
	intent.SemanticEffort = defaultString(intent.SemanticEffort, definition.SemanticEffort)
	intent.OrchestrationPolicy = defaultString(intent.OrchestrationPolicy, definition.OrchestrationPolicy)

	if err := validateExecutionSelectors(intent, definition); err != nil {
		return ExecutionProfilePlan{}, err
	}
	nativeEffort := strings.TrimSpace(intent.NativeEffort)
	nativeDisposition := dispositionExact
	nativeReason := "explicit native effort is supported by the selected client adapter"
	if nativeEffort == "" {
		nativeEffort = translatedNativeEffort(definition, intent.ClientHarnessID, intent.SemanticEffort)
		nativeDisposition = dispositionTranslated
		nativeReason = fmt.Sprintf("semantic effort %s maps to %s for %s", intent.SemanticEffort, nativeEffort, intent.ClientHarnessID)
	}
	if !supportsNativeEffort(intent.ClientHarnessID, nativeEffort) {
		return ExecutionProfilePlan{}, compatibilityError(
			"requested_intent.native_effort",
			fmt.Sprintf("%s does not support native effort %q", intent.ClientHarnessID, nativeEffort),
		)
	}

	plan := ExecutionProfilePlan{
		SchemaVersion:           ExecutionProfilePlanSchema,
		RequestedIntent:         requested,
		ProfileID:               definition.ID,
		Role:                    intent.Role,
		CapabilityClass:         intent.CapabilityClass,
		ClientHarnessID:         intent.ClientHarnessID,
		TransportID:             intent.TransportID,
		RouteAlias:              strings.TrimSpace(intent.RouteAlias),
		ModelConstraint:         strings.TrimSpace(intent.ModelConstraint),
		SemanticEffort:          intent.SemanticEffort,
		NativeEffort:            NativeEffortRequest{Requested: nativeEffort, Disposition: nativeDisposition, Reason: nativeReason},
		OrchestrationPolicy:     intent.OrchestrationPolicy,
		Disposition:             nativeDisposition,
		Budget:                  definition.Budget,
		Checkpoint:              definition.Checkpoint,
		Candidates:              append([]ExecutionProfileCandidate(nil), definition.Candidates...),
		CandidateExplanation:    definition.Explanation,
		IndependenceRequirement: definition.Independence,
		EffectiveProvider:       UnknownUntilReceipt,
		EffectiveModel:          UnknownUntilReceipt,
		EffectiveNativeEffort:   UnknownUntilReceipt,
	}
	if plan.TransportID == "switchback" {
		plan.Warnings = append(plan.Warnings, "Switchback is transport only; effective provider, model, and native effort remain unknown until receipt")
	}
	if plan.ProfileID == "judge-critique" {
		plan.Warnings = append(plan.Warnings, "free judge candidates are tripwire-only and cannot provide a terminal verdict")
	}
	plan.ID = executionProfilePlanID(plan)
	return plan, nil
}

func DeriveChildExecutionProfile(parent ExecutionProfilePlan, child ChildExecutionProfileIntent) (ExecutionProfilePlan, error) {
	if strings.TrimSpace(parent.ID) == "" {
		return ExecutionProfilePlan{}, compatibilityError("parent_plan.id", "parent execution plan id is required")
	}
	if strings.TrimSpace(child.ClientHarnessID) == "" {
		return ExecutionProfilePlan{}, compatibilityError("child_intent.client_harness_id", "child selector must be explicit; harness is not inherited")
	}
	if strings.TrimSpace(child.TransportID) == "" {
		return ExecutionProfilePlan{}, compatibilityError("child_intent.transport_id", "child selector must be explicit; transport is not inherited")
	}
	if strings.TrimSpace(child.SemanticEffort) == "" {
		return ExecutionProfilePlan{}, compatibilityError("child_intent.semantic_effort", "child effort must be explicit")
	}
	if effortRank(child.SemanticEffort) > effortRank(parent.SemanticEffort) {
		return ExecutionProfilePlan{}, compatibilityError("child_intent.semantic_effort", "child semantic effort cannot exceed the parent")
	}
	if parent.OrchestrationPolicy == "ultracode" && strings.TrimSpace(child.OrchestrationPolicy) == "" {
		return ExecutionProfilePlan{}, compatibilityError("child_intent.orchestration_policy", "ultracode is not inherited; child opt-in must be explicit")
	}

	budget, err := childBudgetWithinCeiling(parent.Budget, child.Budget)
	if err != nil {
		return ExecutionProfilePlan{}, err
	}
	role := defaultString(child.Role, parent.Role)
	capabilityClass := defaultString(child.CapabilityClass, parent.CapabilityClass)
	plan, err := ResolveExecutionProfile(ExecutionProfileIntent{
		ProfileID:           parent.ProfileID,
		Role:                role,
		CapabilityClass:     capabilityClass,
		ClientHarnessID:     child.ClientHarnessID,
		TransportID:         child.TransportID,
		RouteAlias:          child.RouteAlias,
		ModelConstraint:     child.ModelConstraint,
		SemanticEffort:      child.SemanticEffort,
		NativeEffort:        child.NativeEffort,
		OrchestrationPolicy: child.OrchestrationPolicy,
	})
	if err != nil {
		return ExecutionProfilePlan{}, err
	}
	plan.RequestedIntent = ExecutionProfileIntent{
		ProfileID:           parent.ProfileID,
		Role:                child.Role,
		CapabilityClass:     child.CapabilityClass,
		ClientHarnessID:     child.ClientHarnessID,
		TransportID:         child.TransportID,
		RouteAlias:          child.RouteAlias,
		ModelConstraint:     child.ModelConstraint,
		SemanticEffort:      child.SemanticEffort,
		NativeEffort:        child.NativeEffort,
		OrchestrationPolicy: child.OrchestrationPolicy,
	}
	plan.ParentPlanID = parent.ID
	plan.InheritedCeilings = []string{"budget.max_requests", "budget.max_input_tokens", "budget.max_output_tokens", "budget.max_duration_seconds", "budget.max_usd", "budget.max_parallelism"}
	plan.Budget = budget
	plan.ID = executionProfilePlanID(plan)
	return plan, nil
}

func ObservedExecutionsIndependent(left, right ObservedExecutionIdentity) bool {
	leftProvider := strings.TrimSpace(left.Provider)
	leftModel := strings.TrimSpace(left.Model)
	rightProvider := strings.TrimSpace(right.Provider)
	rightModel := strings.TrimSpace(right.Model)
	if leftProvider == "" || leftModel == "" || rightProvider == "" || rightModel == "" {
		return false
	}
	return leftProvider != rightProvider || leftModel != rightModel
}

func executionProfileDefinition(id string) (ExecutionProfileDefinition, bool) {
	for _, profile := range ListExecutionProfiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return ExecutionProfileDefinition{}, false
}

func validateExecutionSelectors(intent ExecutionProfileIntent, definition ExecutionProfileDefinition) error {
	if !oneOf(intent.ClientHarnessID, "claude-code", "codex", "worker") {
		reason := "unsupported client harness " + quoted(intent.ClientHarnessID)
		if intent.ClientHarnessID == "switchback" {
			reason = "switchback is a transport, not a client harness"
		}
		return compatibilityError("requested_intent.client_harness_id", reason)
	}
	if !oneOf(intent.TransportID, "native", "switchback", "local") {
		return compatibilityError("requested_intent.transport_id", "unsupported transport "+quoted(intent.TransportID))
	}
	if intent.ClientHarnessID == "claude-code" && intent.TransportID != "native" {
		return compatibilityError("requested_intent.transport_id", "claude-code supports native transport in this binding")
	}
	if intent.ClientHarnessID == "codex" && intent.TransportID == "local" {
		return compatibilityError("requested_intent.transport_id", "codex supports native or switchback transport in this binding")
	}
	if !oneOf(intent.SemanticEffort, "economy", "balanced", "deep", "max") {
		return compatibilityError("requested_intent.semantic_effort", "unsupported semantic effort "+quoted(intent.SemanticEffort))
	}
	if !oneOf(intent.OrchestrationPolicy, "direct", "standard", "ultracode") {
		return compatibilityError("requested_intent.orchestration_policy", "unsupported orchestration policy "+quoted(intent.OrchestrationPolicy))
	}
	if intent.OrchestrationPolicy == "ultracode" && definition.ID != "orchestration-ultracode" {
		return compatibilityError("requested_intent.orchestration_policy", "ultracode requires explicit profile orchestration-ultracode")
	}
	if strings.TrimSpace(intent.RouteAlias) != "" {
		if intent.TransportID != "switchback" {
			return compatibilityError("requested_intent.route_alias", "route aliases require switchback transport")
		}
		if !fullyQualifiedSelector(intent.RouteAlias) {
			return compatibilityError("requested_intent.route_alias", "route alias must be owner-qualified (for example auto/extract)")
		}
	}
	if model := strings.TrimSpace(intent.ModelConstraint); model != "" && !fullyQualifiedSelector(model) {
		return compatibilityError("requested_intent.model_constraint", "model constraint must be a fully qualified provider/model selector; friendly aliases are unowned")
	}
	return nil
}

func supportsNativeEffort(harness, effort string) bool {
	switch harness {
	case "claude-code":
		return oneOf(effort, "low", "medium", "high", "xhigh", "max")
	case "codex":
		return oneOf(effort, "low", "medium", "high", "xhigh", "ultra")
	case "worker":
		return oneOf(effort, "low", "medium", "high")
	default:
		return false
	}
}

func translatedNativeEffort(definition ExecutionProfileDefinition, harness, semanticEffort string) string {
	if semanticEffort == definition.SemanticEffort {
		return definition.NativeEffortByHarness[harness]
	}
	switch semanticEffort {
	case "economy":
		return "low"
	case "balanced":
		return "medium"
	case "deep":
		if harness == "worker" {
			return "high"
		}
		return "xhigh"
	case "max":
		switch harness {
		case "claude-code":
			return "max"
		case "worker":
			return "high"
		default:
			return "xhigh"
		}
	default:
		return ""
	}
}

func childBudgetWithinCeiling(parent, requested ExecutionBudget) (ExecutionBudget, error) {
	child := requested
	inheritInt := func(value, ceiling int) int {
		if value == 0 {
			return ceiling
		}
		return value
	}
	child.MaxRequests = inheritInt(child.MaxRequests, parent.MaxRequests)
	child.MaxInputTokens = inheritInt(child.MaxInputTokens, parent.MaxInputTokens)
	child.MaxOutputTokens = inheritInt(child.MaxOutputTokens, parent.MaxOutputTokens)
	child.MaxDurationSeconds = inheritInt(child.MaxDurationSeconds, parent.MaxDurationSeconds)
	child.MaxParallelism = inheritInt(child.MaxParallelism, parent.MaxParallelism)
	if child.MaxUSD == 0 {
		child.MaxUSD = parent.MaxUSD
	}

	checks := []struct {
		path           string
		value, ceiling int
	}{
		{"max_requests", child.MaxRequests, parent.MaxRequests},
		{"max_input_tokens", child.MaxInputTokens, parent.MaxInputTokens},
		{"max_output_tokens", child.MaxOutputTokens, parent.MaxOutputTokens},
		{"max_duration_seconds", child.MaxDurationSeconds, parent.MaxDurationSeconds},
		{"max_parallelism", child.MaxParallelism, parent.MaxParallelism},
	}
	for _, check := range checks {
		if check.value <= 0 || check.value > check.ceiling {
			return ExecutionBudget{}, compatibilityError("child_intent.budget."+check.path, fmt.Sprintf("child value %d exceeds parent ceiling %d", check.value, check.ceiling))
		}
	}
	if child.MaxUSD < 0 || child.MaxUSD > parent.MaxUSD {
		return ExecutionBudget{}, compatibilityError("child_intent.budget.max_usd", fmt.Sprintf("child value %.2f exceeds parent ceiling %.2f", child.MaxUSD, parent.MaxUSD))
	}
	return child, nil
}

func executionProfilePlanID(plan ExecutionProfilePlan) string {
	canonical := struct {
		ProfileID               string
		Role                    string
		CapabilityClass         string
		ClientHarnessID         string
		TransportID             string
		RouteAlias              string
		ModelConstraint         string
		SemanticEffort          string
		NativeEffort            string
		OrchestrationPolicy     string
		Budget                  ExecutionBudget
		Checkpoint              ExecutionCheckpointPolicy
		ParentPlanID            string
		IndependenceRequirement ExecutionIndependenceRequirement
	}{
		ProfileID: plan.ProfileID, Role: plan.Role, CapabilityClass: plan.CapabilityClass,
		ClientHarnessID: plan.ClientHarnessID, TransportID: plan.TransportID,
		RouteAlias: plan.RouteAlias, ModelConstraint: plan.ModelConstraint,
		SemanticEffort: plan.SemanticEffort, NativeEffort: plan.NativeEffort.Requested,
		OrchestrationPolicy: plan.OrchestrationPolicy, Budget: plan.Budget,
		Checkpoint: plan.Checkpoint, ParentPlanID: plan.ParentPlanID,
		IndependenceRequirement: plan.IndependenceRequirement,
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return "epp_" + hex.EncodeToString(sum[:6])
}

func compatibilityError(path, reason string) error {
	return &ExecutionProfileCompatibilityError{Path: path, Reason: reason}
}

func defaultString(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func fullyQualifiedSelector(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}

func effortRank(effort string) int {
	switch strings.TrimSpace(effort) {
	case "economy":
		return 1
	case "balanced":
		return 2
	case "deep":
		return 3
	case "max":
		return 4
	default:
		return 100
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func quoted(value string) string {
	return fmt.Sprintf("%q", strings.TrimSpace(value))
}
