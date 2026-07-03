package distiller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/envelope"
)

// LoadRuntimeSources builds distiller source material from a durable work chain
// already present in COMPOUND_HOME. It is read-only: work remains owned by the
// work store, and distiller only turns the chain into extraction input.
func LoadRuntimeSources(homePath, workID string) (LLMExtractionSources, error) {
	var src LLMExtractionSources
	homePath = strings.TrimSpace(homePath)
	workID = strings.TrimSpace(workID)
	if homePath == "" {
		return src, fmt.Errorf("home path is required")
	}
	if workID == "" {
		return src, fmt.Errorf("work_id is required")
	}

	workPath := filepath.Join(homePath, "work", "work", workID+".json")
	workEnv, err := readRuntimeEnvelope(workPath)
	if err != nil {
		return src, fmt.Errorf("load work %s: %w", workID, err)
	}
	w, ok := workEnv.(*envelope.WorkEnvelope)
	if !ok {
		return src, fmt.Errorf("%s is %s, want work", workPath, workEnv.Kind())
	}
	if w.ID != workID {
		return src, fmt.Errorf("work file id %q does not match requested %q", w.ID, workID)
	}
	src.Work = w

	if ctxID := strings.TrimSpace(w.IDs.ContextPackID); ctxID != "" {
		ctxPath := filepath.Join(homePath, "context", "packs", ctxID+".json")
		ctxEnv, err := readRuntimeEnvelope(ctxPath)
		if err != nil {
			return src, fmt.Errorf("load context pack %s: %w", ctxID, err)
		}
		ctx, ok := ctxEnv.(*envelope.ContextPack)
		if !ok {
			return src, fmt.Errorf("%s is %s, want context_pack", ctxPath, ctxEnv.Kind())
		}
		src.ContextPack = ctx
	}

	seenExtra := map[string]bool{w.ID: true}
	if src.ContextPack != nil {
		seenExtra[src.ContextPack.ID] = true
	}

	hookEvents, err := loadHookEvents(homePath, w.ID, w.IDs.ContextPackID)
	if err != nil {
		return src, err
	}
	for i := range hookEvents {
		if markEnvelopeSeen(&hookEvents[i], seenExtra) {
			src.Extra = append(src.Extra, &hookEvents[i])
		}
	}
	hookSideEffects, err := loadHookSideEffectEnvelopes(homePath, hookEvents, seenExtra)
	if err != nil {
		return src, err
	}
	src.Extra = append(src.Extra, hookSideEffects...)

	if terminalWork(w.State) && strings.TrimSpace(w.IDs.TransitionID) == "" {
		return src, fmt.Errorf("closed work %s missing transition_id", workID)
	}
	if trID := strings.TrimSpace(w.IDs.TransitionID); trID != "" {
		trPath := filepath.Join(homePath, "events", trID+".json")
		trEnv, err := readRuntimeEnvelope(trPath)
		if err != nil {
			return src, fmt.Errorf("load transition %s: %w", trID, err)
		}
		tr, ok := trEnv.(*envelope.Transition)
		if !ok {
			return src, fmt.Errorf("%s is %s, want transition", trPath, trEnv.Kind())
		}
		if markEnvelopeSeen(tr, seenExtra) {
			src.Extra = append(src.Extra, tr)
		}

		recID := transitionReceiptID(*tr)
		if recID == "" {
			return src, fmt.Errorf("transition %s missing receipt ref in after_refs", trID)
		}
		recPath := filepath.Join(homePath, "events", recID+".json")
		recEnv, err := readRuntimeEnvelope(recPath)
		if err != nil {
			return src, fmt.Errorf("load receipt %s: %w", recID, err)
		}
		rec, ok := recEnv.(*envelope.Receipt)
		if !ok {
			return src, fmt.Errorf("%s is %s, want receipt", recPath, recEnv.Kind())
		}
		src.Receipt = rec
	}

	return src, nil
}

// LoadTraceEnvelopes loads already-imported TraceEnvelope runtime events from
// COMPOUND_HOME/events. It is read-only: event streams remain evidence sources,
// and distiller callers may only turn them into extraction input.
func LoadTraceEnvelopes(homePath string, traceIDs []string) ([]envelope.Envelope, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return nil, fmt.Errorf("home path is required")
	}
	var out []envelope.Envelope
	seen := map[string]bool{}
	for _, rawID := range traceIDs {
		traceID := strings.TrimSpace(rawID)
		if traceID == "" || seen[traceID] {
			continue
		}
		seen[traceID] = true
		tracePath := filepath.Join(homePath, "events", traceID+".json")
		env, err := readRuntimeEnvelope(tracePath)
		if err != nil {
			return nil, fmt.Errorf("load trace %s: %w", traceID, err)
		}
		if env.Kind() != "trace_envelope" {
			return nil, fmt.Errorf("%s is %s, want trace_envelope", tracePath, env.Kind())
		}
		out = append(out, env)
	}
	return out, nil
}

func loadHookEvents(homePath, workID, contextPackID string) ([]envelope.HookEvent, error) {
	streamPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	data, err := os.ReadFile(streamPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read hook events: %w", err)
	}
	var out []envelope.HookEvent
	seen := map[string]bool{}
	for lineNo, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev envelope.HookEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("parse hook event line %d: %w", lineNo+1, err)
		}
		if err := ev.Validate(); err != nil {
			return nil, fmt.Errorf("hook event line %d invalid: %w", lineNo+1, err)
		}
		if seen[ev.ID] {
			continue
		}
		if ev.IDs.WorkID == workID || (contextPackID != "" && ev.IDs.ContextPackID == contextPackID) {
			seen[ev.ID] = true
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func loadHookSideEffectEnvelopes(homePath string, events []envelope.HookEvent, seen map[string]bool) ([]envelope.Envelope, error) {
	var out []envelope.Envelope
	for _, ev := range events {
		for _, ref := range ev.SideEffects {
			path, ok, err := hookSideEffectJSONPath(homePath, ref)
			if err != nil {
				return nil, fmt.Errorf("hook event %s side effect %q: %w", ev.ID, ref, err)
			}
			if !ok {
				continue
			}
			env, err := readRuntimeEnvelope(path)
			if err != nil {
				return nil, fmt.Errorf("load hook event %s side effect %s: %w", ev.ID, ref, err)
			}
			if markEnvelopeSeen(env, seen) {
				out = append(out, env)
			}
		}
	}
	return out, nil
}

func hookSideEffectJSONPath(homePath, ref string) (string, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || !strings.HasSuffix(ref, ".json") {
		return "", false, nil
	}
	if filepath.IsAbs(ref) {
		return "", false, fmt.Errorf("absolute path is not allowed")
	}
	clean := filepath.Clean(ref)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("path escapes COMPOUND_HOME")
	}
	return filepath.Join(homePath, clean), true, nil
}

func markEnvelopeSeen(e envelope.Envelope, seen map[string]bool) bool {
	id := runtimeEnvelopeID(e)
	if id == "" {
		return true
	}
	if seen[id] {
		return false
	}
	seen[id] = true
	return true
}

func runtimeEnvelopeID(e envelope.Envelope) string {
	switch v := e.(type) {
	case *envelope.EffectiveContract:
		return v.ID
	case *envelope.ContextRequest:
		return v.ID
	case *envelope.ContextPack:
		return v.ID
	case *envelope.WorkEnvelope:
		return v.ID
	case *envelope.ActionIntent:
		return v.ID
	case *envelope.PolicyDecision:
		return v.ID
	case *envelope.HookEvent:
		return v.ID
	case *envelope.TraceEnvelope:
		return v.ID
	case *envelope.Receipt:
		return v.ID
	case *envelope.Transition:
		return v.ID
	default:
		return ""
	}
}

func readRuntimeEnvelope(path string) (envelope.Envelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse probe: %w", err)
	}
	e := envelope.New(probe.SchemaVersion)
	if e == nil {
		return nil, fmt.Errorf("unknown schema_version %q", probe.SchemaVersion)
	}
	if err := json.Unmarshal(data, e); err != nil {
		return nil, fmt.Errorf("parse envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}

func transitionReceiptID(tr envelope.Transition) string {
	for _, ref := range tr.AfterRefs {
		if strings.HasPrefix(ref, "rec_") {
			return ref
		}
	}
	return ""
}

func terminalWork(state string) bool {
	switch state {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}
