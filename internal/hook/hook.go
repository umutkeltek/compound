// Package hook is the capability-aware hook fixture loop — the seam where a harness's
// lifecycle is normalized into the typed spine. Hooks are CLIENTS, not brains: each one
// REQUESTS context / GUARDS a mutation / CAPTURES a receipt by producing typed envelopes,
// and is gated by what the harness verifiably supports (a deny on a harness that cannot
// block becomes fail_open, surfaced — never a silent pretense of enforcement).
// See docs/architecture/runtime.md §3. No live hook install before this loop passes.
package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/policy"
	"compound/internal/work"
)

// hookEnum maps the dashed CLI/fixture name to the HookEvent enum name.
var hookEnum = map[string]string{
	"session-start":      "SessionStart",
	"user-prompt-submit": "UserPromptSubmit",
	"pre-tool-use":       "PreToolUse",
	"post-tool-use":      "PostToolUse",
	"session-end":        "SessionEnd",
	"stop":               "Stop",
}

// hookCapability is the HookEvent.hook_capability for each hook.
var hookCapability = map[string]string{
	"SessionStart":     "context_injection",
	"UserPromptSubmit": "context_injection",
	"PreToolUse":       "policy_gate",
	"PostToolUse":      "telemetry",
	"SessionEnd":       "receipt",
	"Stop":             "receipt",
}

// Fixture is one normalized hook invocation (a fixture file, or a live hook's stdin).
type Fixture struct {
	Hook     string                 `json:"hook"`
	Harness  string                 `json:"harness"`
	Cwd      string                 `json:"cwd,omitempty"`
	Task     string                 `json:"task,omitempty"`
	RepoKind string                 `json:"repo_kind,omitempty"`
	Prompt   string                 `json:"prompt,omitempty"`   // user-prompt-submit; only hashed, never stored raw
	Action   *envelope.ActionIntent `json:"action,omitempty"`   // pre-tool-use
	WorkID   string                 `json:"work_id,omitempty"`  // session-end
	MoveType string                 `json:"move_type,omitempty"`
	Outcome  string                 `json:"outcome,omitempty"`
	Summary  string                 `json:"summary,omitempty"`
	Tool     string                 `json:"tool,omitempty"` // post-tool-use
}

// Deps are the services a hook orchestrates.
type Deps struct {
	Registry harness.Registry
	Orient   contract.OrientationPolicy
	Policy   policy.Config
	Store    *work.Store // required only for session-end; may be nil otherwise
}

// Result is what a hook produces: the normalized event, the typed objects, and the small
// harness-facing output line.
type Result struct {
	Event    envelope.HookEvent  `json:"event"`
	Produced []envelope.Envelope `json:"produced"`
	Summary  string              `json:"summary"`
}

// Run dispatches a hook fixture, validating everything it produces through the spine.
func Run(fx Fixture, deps Deps, now time.Time) (Result, error) {
	name, ok := hookEnum[fx.Hook]
	if !ok {
		return Result{}, fmt.Errorf("unknown hook %q (want session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop)", fx.Hook)
	}
	if strings.TrimSpace(fx.Harness) == "" {
		return Result{}, fmt.Errorf("hook fixture needs a harness")
	}
	var (
		res Result
		err error
	)
	switch name {
	case "SessionStart":
		res, err = sessionStart(name, fx, deps, now)
	case "UserPromptSubmit":
		res, err = userPrompt(name, fx, deps, now)
	case "PreToolUse":
		res, err = preToolUse(name, fx, deps, now)
	case "PostToolUse":
		res, err = postToolUse(name, fx, now)
	case "SessionEnd", "Stop":
		res, err = sessionEnd(name, fx, deps, now)
	default:
		return Result{}, fmt.Errorf("unhandled hook %q", name)
	}
	if err != nil {
		return res, err
	}
	if err := res.Event.Validate(); err != nil {
		return res, fmt.Errorf("hook event invalid: %w", err)
	}
	for _, e := range res.Produced {
		if err := e.Validate(); err != nil {
			return res, fmt.Errorf("produced %s invalid: %w", e.Kind(), err)
		}
	}
	return res, nil
}

func sessionStart(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	kind, via := contract.ResolveRepoKind(fx.RepoKind, fx.Cwd)
	in := contract.Input{Harness: fx.Harness, Cwd: fx.Cwd, Task: fx.Task, RepoKind: kind, RepoKindVia: via}
	ec := contract.Compile(in, deps.Orient, now)
	pack := contract.Pack(in, deps.Orient, now)
	ev := newEvent(name, fx, status(deps, fx.Harness, name), hashStr(fx.Task), nil, now)
	return Result{Event: ev, Produced: []envelope.Envelope{&ec, &pack}, Summary: pack.SummaryMD}, nil
}

func userPrompt(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	pred := "address user intent"
	if fx.Task != "" {
		pred = "address: " + fx.Task
	}
	cand := work.BuildWork(work.CreateInput{Owner: fx.Harness, SuccessPredicate: pred, DesiredOutcome: fx.Task, State: "proposed"}, now)
	kind, via := contract.ResolveRepoKind(fx.RepoKind, fx.Cwd)
	pack := contract.Pack(contract.Input{Harness: fx.Harness, Cwd: fx.Cwd, Task: fx.Task, RepoKind: kind, RepoKindVia: via}, deps.Orient, now)
	ev := newEvent(name, fx, status(deps, fx.Harness, name), hashStr(fx.Prompt), nil, now)
	summary := fmt.Sprintf("candidate work %s (proposed); scope=%q repo=%s; %d ctx in / %d excluded; mutations need ActionIntent+PolicyDecision",
		cand.ID, orDash(fx.Task), kind, len(pack.Included), len(pack.Excluded))
	return Result{Event: ev, Produced: []envelope.Envelope{&cand}, Summary: summary}, nil
}

func preToolUse(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	if fx.Action == nil {
		return Result{}, fmt.Errorf("pre-tool-use fixture needs an action")
	}
	ai := *fx.Action
	if fx.WorkID != "" {
		ai.IDs.WorkID = fx.WorkID
	}
	pd := policy.Evaluate(ai, deps.Policy, now)

	// Capability-aware enforcement: a block only counts if the harness can actually block.
	st, warn := "ok", ""
	if !policy.Allowed(pd) {
		if deps.Registry.Supports(fx.Harness, name) {
			st = "blocked"
		} else {
			st = "fail_open"
			warn = fmt.Sprintf(" — WARNING: %s cannot block natively; enforce via wrapper/adapter", fx.Harness)
		}
	}
	ev := newEvent(name, fx, st, hashStr(ai.ID), nil, now)
	return Result{Event: ev, Produced: []envelope.Envelope{&ai, &pd}, Summary: fmt.Sprintf("policy %s for %s%s", pd.Outcome, ai.Action, warn)}, nil
}

func postToolUse(name string, fx Fixture, now time.Time) (Result, error) {
	ev := newEvent(name, fx, "ok", hashStr(fx.Tool), []string{fx.Tool}, now)
	return Result{Event: ev, Produced: []envelope.Envelope{}, Summary: "recorded tool event: " + orDash(fx.Tool) + " (ToolEvent/ArtifactRef land at P6+)"}, nil
}

func sessionEnd(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	if fx.WorkID == "" {
		return Result{}, fmt.Errorf("session-end fixture needs a work_id (the receipt belongs to a work)")
	}
	if deps.Store == nil {
		return Result{}, fmt.Errorf("session-end needs a work store (run `compound home init`)")
	}
	res, err := deps.Store.Close(fx.WorkID, work.CloseInput{
		Outcome:  orDef(fx.Outcome, "completed"),
		MoveType: orDef(fx.MoveType, "audit_only"),
		Summary:  fx.Summary,
		Changed:  strings.TrimSpace(fx.Summary) != "",
	}, now)
	if err != nil {
		return Result{}, err
	}
	ev := newEvent(name, fx, status(deps, fx.Harness, name), hashStr(fx.WorkID), []string{res.Receipt.ID, res.Transition.ID}, now)
	summary := fmt.Sprintf("closed %s: %s, receipt %s, transition %s", res.Work.ID, res.Work.State, res.Receipt.ID, res.Transition.ID)
	return Result{Event: ev, Produced: []envelope.Envelope{&res.Receipt, &res.Transition}, Summary: summary}, nil
}

// --- helpers ---

// status is "ok" when the harness supports the hook, else "fail_open" (the honest signal
// that the harness can't natively honor it and an adapter must).
func status(deps Deps, harnessName, hookName string) string {
	if deps.Registry.Supports(harnessName, hookName) {
		return "ok"
	}
	return "fail_open"
}

func newEvent(name string, fx Fixture, st, inputHash string, sideEffects []string, now time.Time) envelope.HookEvent {
	return envelope.HookEvent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVHookEvent,
			ID:            idFor("hook_", fx.Harness, name, ts(now)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Harness:        fx.Harness,
		HookName:       name,
		HookCapability: hookCapability[name],
		InputHash:      inputHash,
		SideEffects:    orEmptySlice(sideEffects),
		Status:         st,
	}
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

// hashStr returns a hex digest of input — hooks store only the hash of a prompt, never the
// raw text (privacy by default).
func hashStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func ts(now time.Time) string { return strconv.FormatInt(now.UTC().UnixNano(), 10) }

func orDef(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
