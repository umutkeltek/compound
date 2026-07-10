// Package hookplan owns desired hook bindings and their reconciliation against
// read-only harness installation evidence. Runtime hook handling stays in
// internal/hook; observed registrations never become desired authority here.
package hookplan

import (
	"fmt"
	"sort"

	"compound/internal/harness"
)

const (
	HookPlanSchemaVersion             = "compound.hook_plan.v0.1"
	InstallationSnapshotSchemaVersion = "compound.hook_installation_snapshot.v0.1"
	ReconciliationSchemaVersion       = "compound.hook_reconciliation.v0.1"
)

const (
	ActivationManaged  = "managed"
	ActivationOperator = "operator"
)

const (
	StateReady              = "ready"
	StateMissing            = "missing"
	StateExtraUnowned       = "extra_unowned"
	StateDrifted            = "drifted"
	StateActivationRequired = "activation_required"
	StateUnsupported        = "unsupported"
)

// HookPlan is desired authority. It is intentionally a different type from
// InstallationSnapshot so observations cannot be promoted by assignment.
type HookPlan struct {
	SchemaVersion string    `json:"schema_version" yaml:"schema_version"`
	ID            string    `json:"id" yaml:"id"`
	AuthorityRef  string    `json:"authority_ref,omitempty" yaml:"authority_ref,omitempty"`
	Revision      string    `json:"revision" yaml:"revision"`
	Bindings      []Binding `json:"bindings" yaml:"bindings"`
}

// Binding is one desired harness event binding.
type Binding struct {
	ID              string `json:"id" yaml:"id"`
	Harness         string `json:"harness" yaml:"harness"`
	Event           string `json:"event" yaml:"event"`
	Matcher         string `json:"matcher,omitempty" yaml:"matcher,omitempty"`
	HandlerRef      string `json:"handler_ref" yaml:"handler_ref"`
	HandlerRevision string `json:"handler_revision,omitempty" yaml:"handler_revision,omitempty"`
	Activation      string `json:"activation,omitempty" yaml:"activation,omitempty"`
	Blocking        bool   `json:"blocking,omitempty" yaml:"blocking,omitempty"`
}

// InstallationSnapshot is observed evidence from harness-owned config files.
type InstallationSnapshot struct {
	SchemaVersion string            `json:"schema_version"`
	Ref           string            `json:"ref"`
	Revision      string            `json:"revision"`
	Bindings      []ObservedBinding `json:"bindings"`
}

// ObservedBinding describes what a harness config currently registers. It is
// evidence only and carries no desired-state fields.
type ObservedBinding struct {
	BindingID       string `json:"binding_id,omitempty"`
	Harness         string `json:"harness"`
	Event           string `json:"event"`
	Matcher         string `json:"matcher,omitempty"`
	HandlerRef      string `json:"handler_ref"`
	HandlerRevision string `json:"handler_revision,omitempty"`
	Active          bool   `json:"active"`
	SourceRef       string `json:"source_ref,omitempty"`
}

type Delta struct {
	BindingID          string `json:"binding_id,omitempty"`
	Harness            string `json:"harness"`
	Event              string `json:"event"`
	Matcher            string `json:"matcher,omitempty"`
	State              string `json:"state"`
	DesiredHandlerRef  string `json:"desired_handler_ref,omitempty"`
	ObservedHandlerRef string `json:"observed_handler_ref,omitempty"`
	ObservedSourceRef  string `json:"observed_source_ref,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type Reconciliation struct {
	SchemaVersion       string  `json:"schema_version"`
	PlanID              string  `json:"plan_id"`
	PlanRevision        string  `json:"plan_revision"`
	ObservationRef      string  `json:"observation_ref"`
	ObservationRevision string  `json:"observation_revision"`
	Deltas              []Delta `json:"deltas"`
}

// Reconcile compares desired authority to observed evidence without mutating
// either input.
func Reconcile(plan HookPlan, observed InstallationSnapshot, registry harness.Registry) (Reconciliation, error) {
	out := Reconciliation{
		SchemaVersion:       ReconciliationSchemaVersion,
		PlanID:              plan.ID,
		PlanRevision:        plan.Revision,
		ObservationRef:      observed.Ref,
		ObservationRevision: observed.Revision,
	}
	desiredBindings := append([]Binding(nil), plan.Bindings...)
	observedBindings := append([]ObservedBinding(nil), observed.Bindings...)
	sort.SliceStable(desiredBindings, func(i, j int) bool {
		return bindingSortKey(desiredBindings[i]) < bindingSortKey(desiredBindings[j])
	})
	sort.SliceStable(observedBindings, func(i, j int) bool {
		return observedBindingSortKey(observedBindings[i]) < observedBindingSortKey(observedBindings[j])
	})
	if err := validateDesiredBindings(desiredBindings); err != nil {
		return Reconciliation{}, err
	}

	consumed := make([]bool, len(observedBindings))
	for _, desired := range desiredBindings {
		matched := -1
		var slotCandidates []int
		for i, actual := range observedBindings {
			if consumed[i] || !sameSlot(desired, actual) {
				continue
			}
			if actual.BindingID != "" && actual.BindingID != desired.ID {
				continue
			}
			slotCandidates = append(slotCandidates, i)
			if actual.BindingID == desired.ID || desired.HandlerRef == actual.HandlerRef {
				matched = i
				break
			}
		}
		if matched < 0 && len(slotCandidates) == 1 {
			matched = slotCandidates[0]
		}
		if matched < 0 && len(slotCandidates) > 1 {
			return Reconciliation{}, fmt.Errorf("hook plan binding %q has %d ambiguous observed registrations for %s/%s matcher %q", desired.ID, len(slotCandidates), desired.Harness, desired.Event, desired.Matcher)
		}
		delta := Delta{
			BindingID:         desired.ID,
			Harness:           desired.Harness,
			Event:             desired.Event,
			Matcher:           desired.Matcher,
			DesiredHandlerRef: desired.HandlerRef,
			State:             StateMissing,
		}
		if !bindingSupported(desired, registry) {
			delta.State = StateUnsupported
			delta.Reason = "harness registry does not support the desired event capability"
		}
		if matched >= 0 {
			actual := observedBindings[matched]
			consumed[matched] = true
			delta.ObservedHandlerRef = actual.HandlerRef
			delta.ObservedSourceRef = actual.SourceRef
			switch {
			case delta.State == StateUnsupported:
			case !actual.Active:
				delta.State = StateActivationRequired
				delta.Reason = "desired binding is installed but inactive"
			case desired.HandlerRef != actual.HandlerRef || handlerRevisionDrifted(desired, actual):
				delta.State = StateDrifted
				delta.Reason = "observed handler does not match desired binding"
			default:
				delta.State = StateReady
			}
		} else if delta.State != StateUnsupported && desired.Activation == ActivationOperator {
			delta.State = StateActivationRequired
			delta.Reason = "desired binding requires operator activation"
		}
		out.Deltas = append(out.Deltas, delta)
	}
	for i, actual := range observedBindings {
		if consumed[i] {
			continue
		}
		out.Deltas = append(out.Deltas, Delta{
			BindingID:          actual.BindingID,
			Harness:            actual.Harness,
			Event:              actual.Event,
			Matcher:            actual.Matcher,
			State:              StateExtraUnowned,
			ObservedHandlerRef: actual.HandlerRef,
			ObservedSourceRef:  actual.SourceRef,
		})
	}
	return out, nil
}

func validateDesiredBindings(bindings []Binding) error {
	seenSlots := map[string]string{}
	for _, binding := range bindings {
		key := bindingSlotKey(binding.Harness, binding.Event, binding.Matcher)
		if priorID, exists := seenSlots[key]; exists {
			return fmt.Errorf("duplicate desired hook slot %s/%s matcher %q in bindings %q and %q", binding.Harness, binding.Event, binding.Matcher, priorID, binding.ID)
		}
		seenSlots[key] = binding.ID
	}
	return nil
}

func bindingSortKey(binding Binding) string {
	return bindingSlotKey(binding.Harness, binding.Event, binding.Matcher) + "\x00" + binding.ID + "\x00" + binding.HandlerRef
}

func observedBindingSortKey(binding ObservedBinding) string {
	return bindingSlotKey(binding.Harness, binding.Event, binding.Matcher) + "\x00" + binding.BindingID + "\x00" + binding.HandlerRef + "\x00" + binding.SourceRef
}

func bindingSlotKey(harnessName, event, matcher string) string {
	return harnessName + "\x00" + event + "\x00" + matcher
}

func sameSlot(desired Binding, actual ObservedBinding) bool {
	return desired.Harness == actual.Harness && desired.Event == actual.Event && desired.Matcher == actual.Matcher
}

func bindingSupported(binding Binding, registry harness.Registry) bool {
	if _, found := registry.Get(binding.Harness); !found {
		return false
	}
	if binding.Blocking && binding.Event != "PreToolUse" {
		return false
	}
	return registry.Supports(binding.Harness, binding.Event)
}

func handlerRevisionDrifted(desired Binding, actual ObservedBinding) bool {
	return desired.HandlerRevision != "" && desired.HandlerRevision != actual.HandlerRevision
}
