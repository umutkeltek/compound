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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/authority"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/mem"
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
	Hook           string                        `json:"hook"`
	Harness        string                        `json:"harness"`
	Cwd            string                        `json:"cwd,omitempty"`
	Task           string                        `json:"task,omitempty"`
	Mode           string                        `json:"mode,omitempty"`
	RepoKind       string                        `json:"repo_kind,omitempty"`
	Prompt         string                        `json:"prompt,omitempty"` // user-prompt-submit; only hashed, never stored raw
	Action         *envelope.ActionIntent        `json:"action,omitempty"` // pre-tool-use
	SessionID      string                        `json:"session_id,omitempty"`
	WorkID         string                        `json:"work_id,omitempty"` // session-end
	MoveType       string                        `json:"move_type,omitempty"`
	Outcome        string                        `json:"outcome,omitempty"`
	Summary        string                        `json:"summary,omitempty"`
	Missed         []string                      `json:"missed,omitempty"`
	FalseTriggered []string                      `json:"false_triggered,omitempty"`
	Tool           string                        `json:"tool,omitempty"` // post-tool-use
	Placement      *policy.PlacementGuardContext `json:"placement,omitempty"`
}

// Deps are the services a hook orchestrates.
type Deps struct {
	Registry harness.Registry
	Orient   contract.OrientationPolicy
	Policy   policy.Config
	Store    *work.Store // required only for session-end; may be nil otherwise
	HomePath string      // optional; when present session-start persists context artifacts
}

// InjectionArtifact records the exact harness-facing context text written under
// COMPOUND_HOME. The markdown is the consumed artifact; the pack JSON is the
// typed source it was rendered from.
type InjectionArtifact struct {
	Ref                string `json:"ref"`
	Path               string `json:"path"`
	ContextRequestID   string `json:"context_request_id"`
	ContextRequestRef  string `json:"context_request_ref"`
	ContextRequestPath string `json:"context_request_path"`
	ContractID         string `json:"contract_id"`
	ContractRef        string `json:"contract_ref"`
	ContractPath       string `json:"contract_path"`
	ContextPackID      string `json:"context_pack_id"`
	ContextPackRef     string `json:"context_pack_ref"`
	ContextPackPath    string `json:"context_pack_path"`
}

// Result is what a hook produces: the normalized event, the typed objects, and the small
// harness-facing output line.
type Result struct {
	Event     envelope.HookEvent  `json:"event"`
	Produced  []envelope.Envelope `json:"produced"`
	Summary   string              `json:"summary"`
	Injection *InjectionArtifact  `json:"injection,omitempty"`
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
	if strings.TrimSpace(deps.HomePath) != "" {
		if err := writeHookEvent(deps.HomePath, res.Event); err != nil {
			return res, fmt.Errorf("persist hook event: %w", err)
		}
	}
	return res, nil
}

func sessionStart(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	mode := hookRequestedMode(name, fx)
	in, err := orientationInput(fx, deps, mode)
	if err != nil {
		return Result{}, err
	}
	ec := contract.Compile(in, deps.Orient, now)
	pack := contract.Pack(in, deps.Orient, now)
	if strings.TrimSpace(deps.HomePath) != "" {
		var err error
		ec, err = contract.CompileWithRuntime(in, deps.Orient, deps.HomePath, now)
		if err != nil {
			return Result{}, err
		}
		pack, err = contract.PackWithCapabilities(in, deps.Orient, deps.HomePath, now)
		if err != nil {
			return Result{}, err
		}
	}
	req := buildContextRequest(name, fx, ec, pack, mode, now)
	ev := newEvent(name, fx, status(deps, fx.Harness, name), contextHookInputHash(name, fx, mode), nil, now)
	ev.IDs.ContextPackID = pack.Header.ID
	var injection *InjectionArtifact
	if strings.TrimSpace(deps.HomePath) != "" {
		var err error
		injection, err = writeContextArtifacts(deps.HomePath, ev.Header.ID, req, ec, pack)
		if err != nil {
			return Result{}, err
		}
		ev.SideEffects = append(ev.SideEffects, injection.ContextRequestRef, injection.ContractRef, injection.ContextPackRef, injection.Ref)
		ev.OutputRef = injection.Ref
	}
	return Result{Event: ev, Produced: []envelope.Envelope{&req, &ec, &pack}, Summary: pack.SummaryMD, Injection: injection}, nil
}

func userPrompt(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	pred := "address user intent"
	if fx.Task != "" {
		pred = "address: " + fx.Task
	}
	mode := hookRequestedMode(name, fx)
	in, err := orientationInput(fx, deps, mode)
	if err != nil {
		return Result{}, err
	}
	ec := contract.Compile(in, deps.Orient, now)
	pack := contract.Pack(in, deps.Orient, now)
	if strings.TrimSpace(deps.HomePath) != "" {
		var err error
		ec, err = contract.CompileWithRuntime(in, deps.Orient, deps.HomePath, now)
		if err != nil {
			return Result{}, err
		}
		pack, err = contract.PackWithCapabilities(in, deps.Orient, deps.HomePath, now)
		if err != nil {
			return Result{}, err
		}
	}
	req := buildContextRequest(name, fx, ec, pack, mode, now)
	ev := newEvent(name, fx, status(deps, fx.Harness, name), contextHookInputHash(name, fx, mode), nil, now)
	ev.IDs.ContextPackID = pack.Header.ID
	var injection *InjectionArtifact
	if strings.TrimSpace(deps.HomePath) != "" {
		var err error
		injection, err = writeContextArtifacts(deps.HomePath, ev.Header.ID, req, ec, pack)
		if err != nil {
			return Result{}, err
		}
		ev.SideEffects = append(ev.SideEffects, injection.ContextRequestRef, injection.ContractRef, injection.ContextPackRef, injection.Ref)
		ev.OutputRef = injection.Ref
	}
	create := work.CreateInput{Owner: fx.Harness, SuccessPredicate: pred, DesiredOutcome: fx.Task, State: "proposed"}
	if injection != nil {
		create.ContextPackID = pack.Header.ID
	}
	cand := work.BuildWork(create, now)
	ev.IDs.WorkID = cand.ID
	produced := []envelope.Envelope{&cand}
	if deps.Store != nil && injection != nil {
		var err error
		cand, err = deps.Store.Create(create, now)
		if err != nil {
			return Result{}, err
		}
		ev.IDs.WorkID = cand.ID
		ev.SideEffects = append(ev.SideEffects, filepath.Join("work", "work", cand.ID+".json"))
		produced = []envelope.Envelope{&req, &ec, &pack, &cand}
	}
	summary := fmt.Sprintf("candidate work %s (proposed); scope=%q repo=%s; %d ctx in / %d excluded; mutations need ActionIntent+PolicyDecision",
		cand.ID, orDash(fx.Task), in.RepoKind, len(pack.Included), len(pack.Excluded))
	return Result{Event: ev, Produced: produced, Summary: summary, Injection: injection}, nil
}

func orientationInput(fx Fixture, deps Deps, mode string) (contract.Input, error) {
	kind, via, err := contract.ResolveRepoKindWithHome(deps.HomePath, fx.RepoKind, fx.Cwd)
	if err != nil {
		return contract.Input{}, err
	}
	return contract.Input{Harness: fx.Harness, Cwd: fx.Cwd, Task: fx.Task, RepoKind: kind, RepoKindVia: via, Mode: mode}, nil
}

func preToolUse(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	if fx.Action == nil {
		return Result{}, fmt.Errorf("pre-tool-use fixture needs an action")
	}
	ai := *fx.Action
	if fx.WorkID != "" {
		ai.IDs.WorkID = fx.WorkID
	}
	ai = normalizeActionTarget(fx, ai)
	evalCtx := policy.EvaluationContext{}
	var sourceControl *policy.SourceControlContext
	sideEffects := []string(nil)
	if fx.Placement != nil {
		evalCtx.Placement = fx.Placement
	} else if deps.HomePath != "" && strings.TrimSpace(ai.Target.Path) != "" {
		placement, effects, err := policy.PlacementContextFromHome(deps.HomePath, ai.Target.Path)
		if err == nil && placement != nil {
			evalCtx.Placement = placement
		}
		if err == nil {
			sideEffects = append(sideEffects, effects...)
		}
	}
	if deps.HomePath != "" && strings.TrimSpace(ai.Target.Path) != "" {
		var effects []string
		var err error
		sourceControl, effects, err = sourceControlContextForAction(deps.HomePath, ai.Target.Path, now)
		if err != nil {
			return Result{}, err
		}
		if sourceControl != nil {
			evalCtx.SourceControl = sourceControl
			sideEffects = append(sideEffects, effects...)
		}
	}
	pd := policy.EvaluateWithContext(ai, deps.Policy, evalCtx, now)
	if deps.HomePath != "" {
		effects, err := writeGateArtifacts(deps.HomePath, ai, pd)
		if err != nil {
			return Result{}, err
		}
		sideEffects = append(sideEffects, effects...)
	}
	reviewWorkID := ""
	if deps.Store != nil && sourceControl != nil && !policy.Allowed(pd) && strings.TrimSpace(sourceControl.Status) == "dirty" {
		review, err := createHeldGateReview(deps.Store, fx, ai, pd, *sourceControl, now)
		if err != nil {
			return Result{}, err
		}
		reviewWorkID = review.ID
		sideEffects = append(sideEffects, filepath.Join("work", "work", review.ID+".json"))
	}

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
	ev := newEvent(name, fx, st, hashStr(ai.ID), sideEffects, now)
	ev.IDs.WorkID = ai.IDs.WorkID
	summary := fmt.Sprintf("policy %s for %s%s", pd.Outcome, ai.Action, warn)
	if reviewWorkID != "" {
		summary += "; held review work " + reviewWorkID
	}
	return Result{Event: ev, Produced: []envelope.Envelope{&ai, &pd}, Summary: summary}, nil
}

func normalizeActionTarget(fx Fixture, ai envelope.ActionIntent) envelope.ActionIntent {
	if normalized := actionTargetPath(fx, ai); normalized != "" {
		ai.Target.Path = normalized
	}
	return ai
}

func actionTargetPath(fx Fixture, ai envelope.ActionIntent) string {
	targetPath := strings.TrimSpace(ai.Target.Path)
	if targetPath == "" || filepath.IsAbs(targetPath) {
		return targetPath
	}
	cwd := strings.TrimSpace(fx.Cwd)
	if cwd == "" {
		return targetPath
	}
	return filepath.Join(cwd, targetPath)
}

func createHeldGateReview(store *work.Store, fx Fixture, ai envelope.ActionIntent, pd envelope.PolicyDecision, sc policy.SourceControlContext, now time.Time) (envelope.WorkEnvelope, error) {
	target := strings.TrimSpace(ai.Target.Path)
	if target == "" {
		target = ai.Target.ID
	}
	predicate := fmt.Sprintf("review held policy gate for %s on %s", ai.Action, target)
	return store.Create(work.CreateInput{
		Owner:            fx.Harness,
		State:            "held",
		Risk:             orDef(ai.Risk, "tier1"),
		Privacy:          orDef(ai.Privacy, "private"),
		SuccessPredicate: predicate,
		DesiredOutcome:   "decide whether to clean the repo, revise the action, or explicitly review the dirty source-control state before mutation",
		NonGoals: []string{
			"do not mutate the target before policy allows it",
			"do not treat git status as source truth for meaning",
		},
		EvidenceRefs: mergeRefs([]string{ai.ID, pd.ID}, pd.EvidenceRefs, sc.EvidenceRefs),
	}, now)
}

func mergeRefs(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range groups {
		for _, ref := range group {
			ref = strings.TrimSpace(ref)
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

func writeGateArtifacts(homePath string, ai envelope.ActionIntent, pd envelope.PolicyDecision) ([]string, error) {
	actionRef := filepath.Join("events", ai.ID+".json")
	policyRef := filepath.Join("events", pd.ID+".json")
	if err := writeJSON(filepath.Join(homePath, actionRef), ai); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(homePath, policyRef), pd); err != nil {
		return nil, err
	}
	return []string{actionRef, policyRef}, nil
}

func sourceControlContextForAction(homePath, targetPath string, now time.Time) (*policy.SourceControlContext, []string, error) {
	memStore, err := mem.Open(homePath)
	if err != nil {
		return nil, nil, err
	}
	existing, err := memStore.Entities()
	if err != nil {
		return nil, nil, err
	}
	_, entity, facts, report, err := authority.ImportGitSnapshot(targetPath, existing)
	if err != nil {
		return nil, nil, nil
	}
	if _, err := memStore.RegisterEntity(entity, now); err != nil {
		return nil, nil, err
	}
	captured, err := memStore.Capture(facts, now)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range captured {
		if _, err := memStore.Adjudicate(c.ID, "promote", "source-control-pre-tool-use", now); err != nil {
			return nil, nil, err
		}
	}
	return &policy.SourceControlContext{
			EntityRef:    report.EntityRef,
			Status:       report.Status,
			ChangedPaths: report.ChangedPaths,
			EvidenceRefs: []string{report.EvidenceRef},
		}, []string{
			report.EvidenceRef,
			filepath.Join("ledger", "entities.jsonl"),
			filepath.Join("ledger", "claims.jsonl"),
		}, nil
}

func postToolUse(name string, fx Fixture, now time.Time) (Result, error) {
	ev := newEvent(name, fx, "ok", hashStr(fx.Tool), []string{fx.Tool}, now)
	return Result{Event: ev, Produced: []envelope.Envelope{}, Summary: "recorded tool event: " + orDash(fx.Tool) + " (ToolEvent/ArtifactRef land at P6+)"}, nil
}

func sessionEnd(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	if fx.WorkID == "" {
		if strings.TrimSpace(fx.SessionID) == "" {
			return Result{}, fmt.Errorf("session-end fixture needs work_id or session_id")
		}
		inputHash := hashStr(strings.Join([]string{name, fx.SessionID, fx.Cwd, fx.Summary}, "\x00"))
		ev := newEvent(name, fx, status(deps, fx.Harness, name), inputHash, nil, now)
		summary := fmt.Sprintf("recorded %s receipt event for session %s", name, fx.SessionID)
		return Result{Event: ev, Produced: []envelope.Envelope{}, Summary: summary}, nil
	}
	outcome := strings.TrimSpace(fx.Outcome)
	moveType := strings.TrimSpace(fx.MoveType)
	if outcome == "" || moveType == "" {
		inputHash := hashStr(strings.Join([]string{name, fx.SessionID, fx.WorkID, fx.Cwd, fx.Summary, outcome, moveType}, "\x00"))
		ev := newEvent(name, fx, status(deps, fx.Harness, name), inputHash, nil, now)
		summary := fmt.Sprintf("recorded %s receipt event for work %s; explicit outcome and move_type required to close work", name, fx.WorkID)
		return Result{Event: ev, Produced: []envelope.Envelope{}, Summary: summary}, nil
	}
	if deps.Store == nil {
		return Result{}, fmt.Errorf("session-end needs a work store (run `compound home init`)")
	}
	res, err := deps.Store.Close(fx.WorkID, work.CloseInput{
		Outcome:                    outcome,
		MoveType:                   moveType,
		Summary:                    fx.Summary,
		Changed:                    strings.TrimSpace(fx.Summary) != "",
		MissedCapabilities:         fx.Missed,
		FalseTriggeredCapabilities: fx.FalseTriggered,
	}, now)
	if err != nil {
		return Result{}, err
	}
	ev := newEvent(name, fx, status(deps, fx.Harness, name), hashStr(fx.WorkID), []string{res.Receipt.ID, res.Transition.ID}, now)
	ev.IDs.ContextPackID = res.Work.IDs.ContextPackID
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
			ID:            idFor("hook_", fx.Harness, name, inputHash, ts(now)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{WorkID: fx.WorkID, SessionID: fx.SessionID},
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

func buildContextRequest(name string, fx Fixture, ec envelope.EffectiveContract, pack envelope.ContextPack, mode string, now time.Time) envelope.ContextRequest {
	intentRef := ""
	if strings.TrimSpace(fx.Prompt) != "" {
		intentRef = "sha256:" + hashStr(fx.Prompt)
	} else if strings.TrimSpace(fx.Task) != "" {
		intentRef = "sha256:" + hashStr(fx.Task)
	}
	return envelope.ContextRequest{
		Header: envelope.Header{
			SchemaVersion: envelope.SVContextRequest,
			ID:            idFor("ctxreq_", fx.Harness, name, fx.Cwd, fx.Task, "mode:"+strings.TrimSpace(mode), intentRef, ts(now)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{ContextPackID: pack.Header.ID, ContractID: ec.Header.ID},
			Scope:         envelope.Scope{Surface: fx.Cwd},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		IntentTextRef: intentRef,
		Harness:       fx.Harness,
		Cwd:           fx.Cwd,
		TaskGuess:     fx.Task,
		RequestedMode: mode,
		Constraints: envelope.CtxConstraints{
			MaxContextChars:         6000,
			AllowPrivateMemory:      false,
			AllowRawCorpus:          false,
			AllowProvisionalReviews: false,
		},
	}
}

func contextHookInputHash(name string, fx Fixture, mode string) string {
	promptHash := ""
	if strings.TrimSpace(fx.Prompt) != "" {
		promptHash = hashStr(fx.Prompt)
	}
	return hashStr(strings.Join([]string{
		"hook", strings.TrimSpace(name),
		"harness", strings.TrimSpace(fx.Harness),
		"cwd", strings.TrimSpace(fx.Cwd),
		"task", strings.TrimSpace(fx.Task),
		"repo_kind", strings.TrimSpace(fx.RepoKind),
		"mode", strings.TrimSpace(mode),
		"prompt_sha256", promptHash,
	}, "\x00"))
}

func hookRequestedMode(name string, fx Fixture) string {
	if mode := strings.TrimSpace(fx.Mode); mode != "" {
		return mode
	}
	switch name {
	case "SessionStart":
		return "observe"
	case "UserPromptSubmit":
		return "execute"
	default:
		if strings.TrimSpace(fx.Task) == "" {
			return "observe"
		}
		return "execute"
	}
}

func writeContextArtifacts(homePath, hookID string, req envelope.ContextRequest, ec envelope.EffectiveContract, pack envelope.ContextPack) (*InjectionArtifact, error) {
	if pack.IDs.ContractID != ec.ID {
		return nil, fmt.Errorf("context pack contract_id %q does not match effective contract %q", pack.IDs.ContractID, ec.ID)
	}
	contractPaths, err := contract.RecordEffectiveContract(homePath, ec)
	if err != nil {
		return nil, err
	}
	contractRef, err := filepath.Rel(homePath, contractPaths.ContractPath)
	if err != nil {
		return nil, err
	}
	requestRef := filepath.Join("context", "requests", req.Header.ID+".json")
	requestPath := filepath.Join(homePath, requestRef)
	if err := writeJSON(requestPath, req); err != nil {
		return nil, err
	}

	packRef := filepath.Join("context", "packs", pack.Header.ID+".json")
	packPaths, err := contract.RecordContextPack(homePath, pack)
	if err != nil {
		return nil, err
	}

	injectionRef := filepath.Join("context", "injections", hookID+".md")
	injectionPath := filepath.Join(homePath, injectionRef)
	if err := os.MkdirAll(filepath.Dir(injectionPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(injectionPath, []byte(renderInjection(hookID, pack)), 0o644); err != nil {
		return nil, err
	}
	return &InjectionArtifact{
		Ref:                injectionRef,
		Path:               injectionPath,
		ContextRequestID:   req.Header.ID,
		ContextRequestRef:  requestRef,
		ContextRequestPath: requestPath,
		ContractID:         ec.ID,
		ContractRef:        contractRef,
		ContractPath:       contractPaths.ContractPath,
		ContextPackID:      pack.Header.ID,
		ContextPackRef:     packRef,
		ContextPackPath:    packPaths.PackPath,
	}, nil
}

func writeHookEvent(homePath string, ev envelope.HookEvent) error {
	eventRef := filepath.Join("events", ev.Header.ID+".json")
	if err := writeJSON(filepath.Join(homePath, eventRef), ev); err != nil {
		return err
	}
	streamPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(streamPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// maxInjectionActiveMemoryRefs caps how many ActiveMemory refs a hook digest lists;
// the pack keeps the full set — the digest is a budgeted surface, not the record.
const maxInjectionActiveMemoryRefs = 8

// maxInjectionActiveDecisionRefs caps the harness-facing decision list; the
// ContextPack JSON keeps the full authoritative active_decisions set.
const maxInjectionActiveDecisionRefs = 12

// maxInjectionIncludedRefs caps generic provenance in the harness digest.
// Dedicated sections render the high-signal subsets separately.
const maxInjectionIncludedRefs = 24

func renderInjection(hookID string, pack envelope.ContextPack) string {
	var b strings.Builder
	b.WriteString("# Compound Context Injection\n\n")
	fmt.Fprintf(&b, "- Hook event: `%s`\n", safeMD(hookID))
	fmt.Fprintf(&b, "- Context pack: `%s`\n", safeMD(pack.Header.ID))
	if strings.TrimSpace(pack.Header.Scope.Surface) != "" {
		fmt.Fprintf(&b, "- Scope: `%s`\n", safeMD(pack.Header.Scope.Surface))
	}
	b.WriteString("- Capability refs are offers only; execution still requires WorkEnvelope, ActionIntent, and PolicyDecision.\n\n")

	b.WriteString("## Summary\n")
	b.WriteString(safeMD(pack.SummaryMD))
	b.WriteString("\n\n")

	writeCapabilityOfferList(&b, pack.CapabilityOffers)

	writeIncludedList(&b, pack.Included)
	b.WriteString("\n## Excluded\n")
	if len(pack.Excluded) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, ref := range pack.Excluded {
			fmt.Fprintf(&b, "- `%s` reason=%s\n", safeMD(ref.Ref), safeMD(ref.Reason))
		}
	}
	writeStringList(&b, "Active Instructions", pack.ActiveInstructions)
	writeStringList(&b, "Active Hooks", pack.ActiveHooks)
	writeHookReadinessList(&b, pack.HookReadiness)
	writeHookRosterList(&b, pack.HookRoster)
	writeCappedStringList(&b, "Active Decisions", pack.ActiveDecisions, maxInjectionActiveDecisionRefs, "active decision ref(s)")
	if len(pack.ActiveMemory) > 0 {
		shown := prioritizedActiveMemoryRefs(pack.ActiveMemory, pack.ActivePositions)
		omitted := 0
		if len(shown) > maxInjectionActiveMemoryRefs {
			omitted = len(shown) - maxInjectionActiveMemoryRefs
			shown = shown[:maxInjectionActiveMemoryRefs]
		}
		writeActiveMemoryList(&b, pack, shown)
		if omitted > 0 {
			fmt.Fprintf(&b, "- %d more active memory ref(s) omitted from digest budget\n", omitted)
		}
	}
	writeStringList(&b, "Required Checks", pack.RequiredChecks)
	writeStringList(&b, "Avoid", pack.Avoid)
	return b.String()
}

func writeCapabilityOfferList(b *strings.Builder, offers []envelope.CapabilityOffer) {
	if len(offers) == 0 {
		return
	}
	b.WriteString("## Capability Offers\n")
	b.WriteString("- offers only; execution still requires WorkEnvelope, ActionIntent, PolicyDecision\n")
	for _, offer := range offers {
		fmt.Fprintf(b, "- `%s` kind=%s name=%s score=%.2f", safeMD(offer.Ref), safeMD(offer.Kind), safeMD(offer.Name), offer.Score)
		if strings.TrimSpace(offer.Why) != "" {
			fmt.Fprintf(b, " why=%s", safeMD(offer.Why))
		}
		writeCapabilityConstraintSummary(b, "required", offer.Required)
		writeCapabilityConstraintSummary(b, "harnesses", offer.Harnesses)
		writeCapabilityConstraintSummary(b, "conflicts", offer.Conflicts)
		writeCapabilityConstraintSummary(b, "missed_when", offer.KnownMissedWhen)
		writeCapabilityConstraintSummary(b, "false_triggered_by", offer.KnownFalseTriggeredBy)
		writeCapabilityConstraintSummary(b, "evidence", offer.Evidence)
		if strings.TrimSpace(offer.CostRiskNote) != "" {
			fmt.Fprintf(b, " note=%s", safeMD(offer.CostRiskNote))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeIncludedList(b *strings.Builder, refs []envelope.IncludedRef) {
	b.WriteString("## Included\n")
	if len(refs) == 0 {
		b.WriteString("- none\n")
		return
	}
	shown := refs
	if len(shown) > maxInjectionIncludedRefs {
		shown = shown[:maxInjectionIncludedRefs]
	}
	for _, ref := range shown {
		fmt.Fprintf(b, "- `%s` source=%s status=%s confidence=%.2f reason=%s",
			safeMD(ref.Ref), safeMD(ref.SourceClass), safeMD(ref.Status), ref.Confidence, safeMD(ref.Reason))
		if strings.HasPrefix(ref.Ref, "capability://") {
			b.WriteString(" [offer, not execution]")
		}
		b.WriteString("\n")
	}
	if omitted := len(refs) - len(shown); omitted > 0 {
		fmt.Fprintf(b, "- %d more included ref(s) omitted digest budget; see ContextPack JSON for full provenance\n", omitted)
	}
}

func writeCapabilityConstraintSummary(b *strings.Builder, label string, values []envelope.CapabilityOfferConstraint) {
	if len(values) == 0 {
		return
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		part := strings.TrimSpace(value.Ref)
		if strings.TrimSpace(value.Kind) != "" {
			part = strings.TrimSpace(value.Kind) + ":" + part
		}
		if strings.TrimSpace(value.Detail) != "" {
			part += "(" + strings.TrimSpace(value.Detail) + ")"
		}
		if part == "" {
			continue
		}
		parts = append(parts, safeMD(part))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(b, " %s=%s", label, strings.Join(parts, ","))
}

func writeActiveMemoryList(b *strings.Builder, pack envelope.ContextPack, refs []string) {
	if len(refs) == 0 {
		return
	}
	b.WriteString("\n## Active Memory\n")
	for _, ref := range refs {
		line := activeMemoryLine(pack, ref)
		if line == "" {
			fmt.Fprintf(b, "- `%s`\n", safeMD(ref))
			continue
		}
		fmt.Fprintf(b, "- `%s` - %s\n", safeMD(ref), safeMD(line))
	}
}

func prioritizedActiveMemoryRefs(refs []string, positions []envelope.ActivePosition) []string {
	if len(refs) == 0 || len(positions) == 0 {
		return refs
	}
	positionRefs := map[string]bool{}
	for _, position := range positions {
		if strings.TrimSpace(position.Ref) != "" {
			positionRefs[position.Ref] = true
		}
	}
	if len(positionRefs) == 0 {
		return refs
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if positionRefs[ref] {
			out = append(out, ref)
		}
	}
	for _, ref := range refs {
		if !positionRefs[ref] {
			out = append(out, ref)
		}
	}
	return out
}

func activeMemoryLine(pack envelope.ContextPack, ref string) string {
	for _, included := range pack.Included {
		if included.Ref != ref {
			continue
		}
		reason := strings.TrimSpace(included.Reason)
		if strings.HasPrefix(reason, "Compound memory ledger claim;") || strings.HasPrefix(reason, "Compound memory folded position;") {
			if _, line, ok := strings.Cut(reason, ":"); ok {
				return strings.TrimSpace(line)
			}
		}
		return reason
	}
	return ""
}

func writeStringList(b *strings.Builder, title string, xs []string) {
	fmt.Fprintf(b, "\n## %s\n", title)
	if len(xs) == 0 {
		b.WriteString("- none\n")
		return
	}
	for _, x := range xs {
		fmt.Fprintf(b, "- `%s`\n", safeMD(x))
	}
}

func writeCappedStringList(b *strings.Builder, title string, xs []string, limit int, omittedLabel string) {
	fmt.Fprintf(b, "\n## %s\n", title)
	if len(xs) == 0 {
		b.WriteString("- none\n")
		return
	}
	shown := xs
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, x := range shown {
		fmt.Fprintf(b, "- `%s`\n", safeMD(x))
	}
	if omitted := len(xs) - len(shown); omitted > 0 {
		fmt.Fprintf(b, "- %d more %s omitted digest budget\n", omitted, safeMD(omittedLabel))
	}
}

func writeHookReadinessList(b *strings.Builder, readiness []envelope.HookReadiness) {
	if len(readiness) == 0 {
		return
	}
	b.WriteString("\n## Hook Gate Readiness\n")
	for _, r := range readiness {
		fmt.Fprintf(b, "- `%s/%s` status=%s installed=%t native_block=%t",
			safeMD(r.Harness), safeMD(r.Hook), safeMD(r.Status), r.Installed, r.NativeBlock)
		if r.InstalledRef != "" {
			fmt.Fprintf(b, " ref=`%s`", safeMD(r.InstalledRef))
		}
		if r.AdapterRequired {
			b.WriteString(" adapter_required=true")
		}
		if r.AdapterCommand != "" {
			fmt.Fprintf(b, " command=`%s`", safeMD(r.AdapterCommand))
		}
		if r.Reason != "" {
			fmt.Fprintf(b, " reason=%s", safeMD(r.Reason))
		}
		b.WriteString("\n")
	}
}

func writeHookRosterList(b *strings.Builder, roster []envelope.HookRosterEntry) {
	if len(roster) == 0 {
		return
	}
	b.WriteString("\n## Hook Roster\n")
	for _, r := range roster {
		fmt.Fprintf(b, "- `%s` harness=%s hook=%s event=%s status=%s",
			safeMD(r.Ref), safeMD(r.Harness), safeMD(r.Hook), safeMD(r.Event), safeMD(r.Status))
		if r.Matcher != "" {
			fmt.Fprintf(b, " matcher=%s", safeMD(r.Matcher))
		}
		if r.Reason != "" {
			fmt.Fprintf(b, " reason=%s", safeMD(r.Reason))
		}
		b.WriteString("\n")
	}
}

func safeMD(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "`", "'")
	return strings.TrimSpace(s)
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
