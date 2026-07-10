// Package hookplan owns desired hook bindings and their reconciliation against
// read-only harness installation evidence. Runtime hook handling stays in
// internal/hook; observed registrations never become desired authority here.
package hookplan

import "compound/internal/harness"

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
// either input. Capability validation and non-ready classifications deepen in
// subsequent vertical tests.
func Reconcile(plan HookPlan, observed InstallationSnapshot, registry harness.Registry) (Reconciliation, error) {
	_ = registry
	out := Reconciliation{
		SchemaVersion:       ReconciliationSchemaVersion,
		PlanID:              plan.ID,
		PlanRevision:        plan.Revision,
		ObservationRef:      observed.Ref,
		ObservationRevision: observed.Revision,
	}
	consumed := make([]bool, len(observed.Bindings))
	for _, desired := range plan.Bindings {
		matched := -1
		for i, actual := range observed.Bindings {
			if consumed[i] || desired.Harness != actual.Harness || desired.Event != actual.Event || desired.Matcher != actual.Matcher {
				continue
			}
			if desired.HandlerRef == actual.HandlerRef {
				matched = i
				break
			}
		}
		delta := Delta{
			BindingID:         desired.ID,
			Harness:           desired.Harness,
			Event:             desired.Event,
			Matcher:           desired.Matcher,
			DesiredHandlerRef: desired.HandlerRef,
			State:             StateMissing,
		}
		if matched >= 0 {
			actual := observed.Bindings[matched]
			consumed[matched] = true
			delta.State = StateReady
			delta.ObservedHandlerRef = actual.HandlerRef
			delta.ObservedSourceRef = actual.SourceRef
		}
		out.Deltas = append(out.Deltas, delta)
	}
	for i, actual := range observed.Bindings {
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
