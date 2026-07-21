package actuator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

// Plan is the actuator's full routing verdict for one move: the policy decision,
// the budget headroom, and the auto/hold/deny routing — computed but NOT yet
// executed. EvaluatePlan is pure orchestration over existing authority (policy,
// budget), so it is cheap, side-effect-free, and safe to call speculatively.
type Plan struct {
	Decision       Decision                `json:"decision"`
	PolicyDecision envelope.PolicyDecision `json:"policy_decision"`
	Headroom       Headroom                `json:"headroom"`
	Intent         envelope.ActionIntent   `json:"intent"`
	Channel        string                  `json:"channel"`
	ProductID      string                  `json:"product_id,omitempty"`
}

// EvaluatePlan composes the three authoritative inputs — policy.Evaluate, the
// budget Check, and Route — into a Plan. It never executes. channel + productID
// select the budget; intent carries the declared mutation_class + reversibility
// that Route reads.
func EvaluatePlan(homePath string, intent envelope.ActionIntent, channel, productID string, cfg policy.Config, now time.Time) (Plan, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		channel = WildcardChannel
	}
	pd := policy.Evaluate(intent, cfg, now)
	ledger := OpenLedger(homePath)
	budget, err := ledger.LoadBudget(productID, now)
	if err != nil {
		return Plan{}, err
	}
	headroom, err := ledger.Check(budget, productID, channel, now)
	if err != nil {
		return Plan{}, err
	}
	decision := Route(intent, pd.Outcome, headroom)
	decision.Channel = channel
	decision.PolicyOutcome = pd.Outcome
	return Plan{
		Decision:       decision,
		PolicyDecision: pd,
		Headroom:       headroom,
		Intent:         intent,
		Channel:        channel,
		ProductID:      productID,
	}, nil
}

// StepResult is the outcome of applying one switchback step. Applied is true
// when the actuator performed it; Held means it needs an operator (with the
// reason); the receipt/transition refs are set when a governed write landed.
type StepResult struct {
	Kind          string   `json:"kind"`
	Description   string   `json:"description"`
	Applied       bool     `json:"applied"`
	Held          bool     `json:"held"`
	Reasons       []string `json:"reasons,omitempty"`
	ReceiptRef    string   `json:"receipt_ref,omitempty"`
	TransitionRef string   `json:"transition_ref,omitempty"`
	ApplyHint     string   `json:"apply_hint,omitempty"`
}

// SwitchbackResult is the outcome of applying a ProjectProfile's switchback.
type SwitchbackResult struct {
	ProfileID string       `json:"profile_id"`
	WorkID    string       `json:"work_id"`
	Steps     []StepResult `json:"steps"`
}

// Switchback applies a project profile's switchback within known work. It writes
// instruction files and repoints the routing key directly (both local_reversible,
// policy-gated, budget-checked, receipt-emitting via the act seam), and surfaces
// the settings/budget steps as the exact governed commands for the operator. A
// switchback is the "adapt to this project smoothly" act: one call re-points
// routing + instructions for the project, and every mutation leaves a receipt so
// it is audited, never silent.
func Switchback(homePath string, p ProjectProfile, workID string, cfg policy.Config, now time.Time) (SwitchbackResult, error) {
	steps, err := SwitchbackPlan(p, workID, now)
	if err != nil {
		return SwitchbackResult{}, err
	}
	ledger := OpenLedger(homePath)
	budget, err := ledger.LoadBudget(p.ProductID, now)
	if err != nil {
		return SwitchbackResult{}, err
	}
	result := SwitchbackResult{ProfileID: p.ID, WorkID: workID}

	for _, step := range steps {
		switch step.Kind {
		case StepInstructions:
			result.Steps = append(result.Steps, applyInstructionStep(step))
		case StepRouting:
			result.Steps = append(result.Steps, applyRoutingStep(homePath, p, step, budget, cfg, ledger, now))
		default:
			// settings + budget: surface the governed command, do not reimplement.
			result.Steps = append(result.Steps, StepResult{
				Kind:        step.Kind,
				Description: step.Description,
				Held:        true,
				Reasons:     []string{"operator-governed step; run the existing command to apply"},
				ApplyHint:   step.ApplyHint,
			})
		}
	}
	return result, nil
}

// applyInstructionStep writes one instruction file into the workspace. It is a
// local_reversible projection write (overwrite/restore), so it is applied
// directly; a failure holds the step rather than aborting the whole switchback.
func applyInstructionStep(step SwitchbackStep) StepResult {
	res := StepResult{Kind: step.Kind, Description: step.Description}
	if err := os.WriteFile(step.FilePath, []byte(step.Body), 0o644); err != nil {
		res.Held = true
		res.Reasons = []string{fmt.Sprintf("could not write instruction file: %v", err)}
		return res
	}
	res.Applied = true
	return res
}

// applyRoutingStep runs the routing repoint through the actuator's own gate:
// EvaluatePlan (policy + budget + Route), then act.Execute when auto. Because the
// intent is local_reversible/easy/local, the default policy allows it and the
// switchback flows — but act.Execute still captures the pre-image and emits a
// receipt + transition, so the repoint is audited and reversible. A hold/deny
// leaves the config byte-identical and reports why.
func applyRoutingStep(homePath string, p ProjectProfile, step SwitchbackStep, budget BudgetEnvelope, cfg policy.Config, ledger *Ledger, now time.Time) StepResult {
	res := StepResult{Kind: step.Kind, Description: step.Description}
	if step.Intent == nil {
		res.Held = true
		res.Reasons = []string{"routing step has no action intent"}
		return res
	}
	plan, err := EvaluatePlan(homePath, *step.Intent, "switchback", p.ProductID, cfg, now)
	if err != nil {
		res.Held = true
		res.Reasons = []string{fmt.Sprintf("could not evaluate routing: %v", err)}
		return res
	}
	if plan.Decision.Mode != ModeAuto {
		res.Held = true
		res.Reasons = plan.Decision.Reasons
		return res
	}
	store, err := act.NewArtifactStore(homePath)
	if err != nil {
		res.Held = true
		res.Reasons = []string{fmt.Sprintf("artifact store: %v", err)}
		return res
	}
	out, err := act.Execute(store, act.NewConfigKeyAdapter(), *step.Intent, plan.PolicyDecision, []byte(step.NewValue), now)
	if err != nil {
		// No allowing decision OR write failure: act rolled back, target unchanged.
		res.Held = true
		res.Reasons = []string{fmt.Sprintf("governed routing repoint refused/rolled back: %v", err)}
		return res
	}
	// Record the routing repoint against the switchback channel budget.
	_ = ledger.Record(Consumption{
		SchemaVersion: ConsumptionSchemaVersion,
		At:            now.UTC().Format(time.RFC3339),
		ProductID:     p.ProductID,
		Channel:       "switchback",
		Actions:       1,
		WorkID:        step.Intent.IDs.WorkID,
		ReceiptRef:    out.Receipt.ID,
	})
	res.Applied = true
	res.ReceiptRef = out.Receipt.ID
	res.TransitionRef = out.Transition.ID
	return res
}

// RunInput is the actuator's request to execute one move of known work on a
// harness. It mirrors harness.RunInput's caller-facing fields plus the budget
// channel + product the run executes within.
type RunInput struct {
	WorkID         string
	ProductID      string
	Channel        string
	Harness        string
	Workspace      string
	Command        string
	Args           []string
	Prompt         string
	PermissionMode string
	Timeout        time.Duration
}

// RunResult is the outcome of an actuator Run. When held, HeldMoveID/HeldMovePath
// point at the persisted human-gated action card explaining how to release it.
type RunResult struct {
	Plan           Plan   `json:"plan"`
	Executed       bool   `json:"executed"`
	Held           bool   `json:"held"`
	RunAttemptID   string `json:"run_attempt_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	ExitCode       int    `json:"exit_code,omitempty"`
	TimedOut       bool   `json:"timed_out,omitempty"`
	HeldMoveID     string `json:"held_move_id,omitempty"`
	HeldMovePath   string `json:"held_move_path,omitempty"`
}

// Run executes one move of known work within limits. It builds a local_reversible
// ActionIntent for the run, evaluates the full Plan (policy + budget + Route), and
// — only if the verdict is auto — dispatches through the existing harness adapter
// (which mints a RunAttempt and captures a transcript) and records the budget
// consumption. A hold/deny returns without dispatching, carrying the reasons as a
// plain-language card. This is "execute within known work, talking to harnesses,
// within limits": the actuator never invents work and never bypasses the gate.
func Run(homePath string, in RunInput, cfg policy.Config, now time.Time) (RunResult, error) {
	if strings.TrimSpace(in.WorkID) == "" {
		return RunResult{}, fmt.Errorf("actuator run: work_id required (execution is scoped to known work)")
	}
	if strings.TrimSpace(in.Harness) == "" {
		return RunResult{}, fmt.Errorf("actuator run: harness required")
	}
	channel := firstNonEmpty(in.Channel, "execution")
	intent := runIntent(in, now)
	plan, err := EvaluatePlan(homePath, intent, channel, in.ProductID, cfg, now)
	if err != nil {
		return RunResult{}, err
	}
	if plan.Decision.Mode != ModeAuto {
		// Held: do not dispatch. Persist a human-gated action card so the hold is
		// actionable, not silent.
		card := ReleaseGateFor(plan, now)
		path, _ := WriteHeldMoveCard(homePath, card)
		return RunResult{Plan: plan, Held: true, HeldMoveID: card.ID, HeldMovePath: path}, nil
	}

	registry := harness.NewAdapterRegistry(runattempt.Open(homePath))
	adapter, ok := registry.AdapterByName(in.Harness)
	if !ok {
		return RunResult{}, fmt.Errorf("actuator run: unknown harness %q; available: %v", in.Harness, registry.Names())
	}
	out, runErr := adapter.Run(context.Background(), harness.RunInput{
		WorkID:         in.WorkID,
		Harness:        in.Harness,
		Workspace:      in.Workspace,
		Command:        in.Command,
		Args:           in.Args,
		Prompt:         in.Prompt,
		PermissionMode: in.PermissionMode,
		Now:            now,
		Timeout:        in.Timeout,
		ActionIntentID: intent.ID,
	})

	// Record consumption for the run regardless of subprocess exit — a run that
	// happened consumed budget even if the task failed. Infrastructure failure
	// (runErr != nil) still produced a terminalized RunAttempt we record.
	_ = OpenLedger(homePath).Record(Consumption{
		SchemaVersion: ConsumptionSchemaVersion,
		At:            now.UTC().Format(time.RFC3339),
		ProductID:     in.ProductID,
		Channel:       channel,
		Actions:       1,
		WorkID:        in.WorkID,
		ReceiptRef:    out.RunAttempt.ID,
	})

	return RunResult{
		Plan:           plan,
		Executed:       true,
		RunAttemptID:   out.RunAttempt.ID,
		TranscriptPath: out.TranscriptPath,
		ExitCode:       out.ExitCode,
		TimedOut:       out.TimedOut,
	}, runErr
}

// runIntent builds the local_reversible / easy / local ActionIntent for a harness
// run of known work. A harness run inside a scoped workspace with a captured
// transcript is locally reversible (the diff can be reverted), so it qualifies
// for auto-execution under the default policy — while the budget gate still caps
// how many runs happen per window.
func runIntent(in RunInput, now time.Time) envelope.ActionIntent {
	target := envelope.ActionTarget{Kind: "harness_run", ID: in.Harness, Path: in.Workspace}
	preview := in.Prompt
	if preview == "" {
		preview = strings.TrimSpace(in.Command + " " + strings.Join(in.Args, " "))
	}
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            intentIDFor(in.WorkID, "harness_run", in.Harness, in.Workspace, preview),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "actuator", Name: "compound"},
			IDs:           envelope.IDs{WorkID: in.WorkID},
			Scope:         envelope.Scope{ProductID: in.ProductID, Surface: in.Harness},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "actuator",
		Capability:    "harness_run",
		Action:        "run",
		Target:        target,
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       truncate(preview, 200),
		RollbackPlan:  "revert the workspace diff captured in the run transcript",
	}
}

// intentIDFor derives a deterministic, collision-resistant intent id from the
// move's components, prefixed "act_" to match the governed-act id family used by
// the act CLI and internal/act's own tests.
func intentIDFor(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return "act_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
