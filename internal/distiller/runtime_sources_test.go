package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/harness"
	"compound/internal/hook"
	"compound/internal/policy"
	"compound/internal/work"
)

func TestLoadRuntimeSourcesFromClosedHookWork(t *testing.T) {
	homePath, memStore := setup(t)
	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	deps := hook.Deps{
		Registry: harness.DefaultRegistry(),
		Orient:   contract.DefaultPolicy(),
		Policy:   policy.DefaultConfig(),
		HomePath: homePath,
		Store:    workStore,
	}

	start, err := hook.Run(hook.Fixture{
		Hook:     "user-prompt-submit",
		Harness:  "codex",
		Cwd:      "/repo/productX",
		Task:     "extract reusable pricing workflow",
		RepoKind: "product",
		Prompt:   "prepare a pricing experiment and remember only durable lessons",
	}, deps, t0)
	if err != nil {
		t.Fatalf("user prompt hook: %v", err)
	}
	ctxReq := findProduced[*envelope.ContextRequest](t, start.Produced)
	ec := findProduced[*envelope.EffectiveContract](t, start.Produced)
	pack := findProduced[*envelope.ContextPack](t, start.Produced)
	w := findProduced[*envelope.WorkEnvelope](t, start.Produced)
	if w.IDs.ContextPackID != pack.ID {
		t.Fatalf("work context_pack_id = %q, want %q", w.IDs.ContextPackID, pack.ID)
	}

	if _, err := workStore.Advance(w.ID, work.AdvanceInput{To: "ready", Note: "admit prompt work"}, t0); err != nil {
		t.Fatalf("advance ready: %v", err)
	}
	if _, err := workStore.Claim(w.ID, "codex", t0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{To: "running", RunID: "run_pricing_distill"}, t0); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{To: "verifying", ArtifactID: "artifact_pricing_summary", VerificationID: "ver_pricing_summary"}, t0); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	closed, err := hook.Run(hook.Fixture{
		Hook:     "session-end",
		Harness:  "codex",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "pricing experiment prepared with reusable workflow notes",
	}, deps, t0)
	if err != nil {
		t.Fatalf("session end hook: %v", err)
	}
	rec := findProduced[*envelope.Receipt](t, closed.Produced)
	tr := findProduced[*envelope.Transition](t, closed.Produced)

	src, err := LoadRuntimeSources(homePath, w.ID)
	if err != nil {
		t.Fatalf("load runtime sources: %v", err)
	}
	if src.Work == nil || src.Work.ID != w.ID || src.Work.State != "completed" {
		t.Fatalf("loaded work = %+v, want completed %s", src.Work, w.ID)
	}
	if src.ContextPack == nil || src.ContextPack.ID != pack.ID {
		t.Fatalf("loaded context pack = %+v, want %s", src.ContextPack, pack.ID)
	}
	if src.Receipt == nil || src.Receipt.ID != rec.ID {
		t.Fatalf("loaded receipt = %+v, want %s", src.Receipt, rec.ID)
	}
	if countKind(src.Extra, "hook_event") < 2 ||
		countKind(src.Extra, "context_request") != 1 ||
		countKind(src.Extra, "effective_contract") != 1 ||
		countKind(src.Extra, "transition") != 1 {
		t.Fatalf("loaded extra = %+v, want hook events, context request, effective contract, plus one transition", src.Extra)
	}

	prior, err := LoadPriorStateFromHome(homePath, memStore, DefaultPolicy())
	if err != nil {
		t.Fatalf("prior: %v", err)
	}
	req, err := BuildLLMExtractionRequestFromSources("runtime:"+w.ID, src, DefaultPolicy(), prior)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for _, want := range []string{
		"work:" + w.ID,
		"context_pack:" + pack.ID,
		"receipt:" + rec.ID,
		"context_request:" + ctxReq.ID,
		"effective_contract:" + ec.ID,
		"transition:" + tr.ID,
	} {
		if !hasString(req.SourceRefs, want) {
			t.Fatalf("request source refs missing %q: %v", want, req.SourceRefs)
		}
	}
	if req.RawTrace != "" {
		t.Fatalf("runtime loader should not invent raw trace: %q", req.RawTrace)
	}
	if req.Work == nil || req.Work.State != "completed" || !hasString(req.Work.ActionIntentIDs, "run_pricing_distill") {
		t.Fatalf("request work summary incomplete: %+v", req.Work)
	}
	if req.Context == nil || !strings.Contains(req.Context.SummaryMD, "product") {
		t.Fatalf("request context summary incomplete: %+v", req.Context)
	}
	if req.Receipt == nil || req.Receipt.StateDelta.Summary == "" {
		t.Fatalf("request receipt summary incomplete: %+v", req.Receipt)
	}
	if countLLMKind(req.Envelopes, "hook_event") < 2 ||
		countLLMKind(req.Envelopes, "context_request") != 1 ||
		countLLMKind(req.Envelopes, "effective_contract") != 1 ||
		countLLMKind(req.Envelopes, "transition") != 1 {
		t.Fatalf("request extra envelopes = %+v, want hook events, context request, effective contract, plus transition %s", req.Envelopes, tr.ID)
	}
	if !hasString(req.SourceRefs, "hook_event:"+start.Event.ID) || !hasString(req.SourceRefs, "hook_event:"+closed.Event.ID) {
		t.Fatalf("request source refs missing hook events %q/%q: %v", start.Event.ID, closed.Event.ID, req.SourceRefs)
	}
}

func TestLoadRuntimeSourcesRejectsBrokenClosedWork(t *testing.T) {
	homePath, _ := setup(t)
	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w, err := workStore.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "broken close chain is rejected",
	}, t0)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := workStore.Claim(w.ID, "codex", t0); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{Actor: "codex", To: "running"}, t0); err != nil {
		t.Fatalf("advance work: %v", err)
	}
	closed, err := workStore.Close(w.ID, work.CloseInput{Actor: "codex", Outcome: "completed", MoveType: "audit_only", Summary: "closed before corruption"}, t0)
	if err != nil {
		t.Fatalf("close work: %v", err)
	}
	w = closed.Work
	w.IDs.TransitionID = ""
	data, err := json.MarshalIndent(&w, "", "  ")
	if err != nil {
		t.Fatalf("marshal broken work: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "work", "work", w.ID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write broken work: %v", err)
	}
	_, err = LoadRuntimeSources(homePath, w.ID)
	if err == nil || !strings.Contains(err.Error(), "missing transition_id") {
		t.Fatalf("err = %v, want missing transition_id", err)
	}
}

func TestHookSideEffectJSONPathRejectsUnsafeRefs(t *testing.T) {
	homePath := t.TempDir()
	if _, ok, err := hookSideEffectJSONPath(homePath, "context/injections/hook_123.md"); err != nil || ok {
		t.Fatalf("markdown ref ok=%v err=%v, want ignored", ok, err)
	}
	if _, ok, err := hookSideEffectJSONPath(homePath, "ledger/claims.jsonl"); err != nil || ok {
		t.Fatalf("jsonl ref ok=%v err=%v, want ignored", ok, err)
	}
	if _, _, err := hookSideEffectJSONPath(homePath, filepath.Join("..", "escape.json")); err == nil {
		t.Fatal("expected escaping ref to fail")
	}
	if _, _, err := hookSideEffectJSONPath(homePath, filepath.Join(homePath, "events", "hook.json")); err == nil {
		t.Fatal("expected absolute ref to fail")
	}
	path, ok, err := hookSideEffectJSONPath(homePath, filepath.Join("context", "requests", "ctxreq_123.json"))
	if err != nil || !ok {
		t.Fatalf("safe ref ok=%v err=%v, want accepted", ok, err)
	}
	if want := filepath.Join(homePath, "context", "requests", "ctxreq_123.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestLoadHookSideEffectEnvelopesKeepsGenericJSONPathsSeparate(t *testing.T) {
	homePath := t.TempDir()
	generic := testTraceEnvelope("trace_generic_side_effect", "evt_generic", "Loaded from a generic side-effect path.")
	writeRuntimeEnvelope(t, filepath.Join(homePath, "rec_report.json"), generic)
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatalf("open event store: %v", err)
	}

	loaded, err := loadHookSideEffectEnvelopes(homePath, store, []envelope.HookEvent{{
		Header:      envelope.Header{ID: "hook_generic_side_effect"},
		SideEffects: []string{"rec_report.json"},
	}}, map[string]bool{})
	if err != nil {
		t.Fatalf("load generic side effect: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Kind() != "trace_envelope" {
		t.Fatalf("loaded = %+v, want generic trace envelope", loaded)
	}
}

func TestLoadTraceEnvelopesFromRuntimeEvents(t *testing.T) {
	homePath := t.TempDir()
	first := testTraceEnvelope("trace_runtime_a", "evt_a", "Generated first snapshot.")
	second := testTraceEnvelope("trace_runtime_b", "evt_b", "Generated second snapshot.")
	writeRuntimeEnvelope(t, filepath.Join(homePath, "events", first.ID+".json"), first)
	writeRuntimeEnvelope(t, filepath.Join(homePath, "events", second.ID+".json"), second)

	loaded, err := LoadTraceEnvelopes(homePath, []string{
		first.ID,
		"compound://events/" + first.ID,
		" events/" + second.ID + ".json ",
	})
	if err != nil {
		t.Fatalf("load trace envelopes: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded traces = %d, want 2", len(loaded))
	}
	if loaded[0].Kind() != "trace_envelope" || loaded[1].Kind() != "trace_envelope" {
		t.Fatalf("loaded kinds = %s/%s", loaded[0].Kind(), loaded[1].Kind())
	}

	_, err = LoadTraceEnvelopes(homePath, []string{"trace_missing"})
	if err == nil || !strings.Contains(err.Error(), "load trace trace_missing") {
		t.Fatalf("err = %v, want missing trace error", err)
	}
}

func testTraceEnvelope(id, sourceID, summary string) *envelope.TraceEnvelope {
	return &envelope.TraceEnvelope{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTraceEnvelope,
			ID:            id,
			CreatedAt:     "2026-06-29T12:00:00Z",
			Producer:      envelope.Producer{Kind: "test", Name: "compound"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{"path:/workspace/events.json"},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Source: envelope.TraceSource{
			System:     "os-event",
			Stream:     "control-events",
			SourceID:   sourceID,
			SourceKind: "snapshot.generated",
		},
		MutationClass:   "read",
		Summary:         summary,
		ToolsModelsUsed: []string{"caller:scheduler"},
		FilesTouched:    []string{"/workspace/events.json"},
		Outputs:         []string{"path:/workspace/events.json"},
		Cost:            envelope.ReceiptCost{ModelCalls: []string{}},
	}
}

func writeRuntimeEnvelope(t *testing.T, path string, env envelope.Envelope) {
	t.Helper()
	if err := env.Validate(); err != nil {
		t.Fatalf("invalid test envelope: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(env, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findProduced[T envelope.Envelope](t *testing.T, produced []envelope.Envelope) T {
	t.Helper()
	var zero T
	for _, item := range produced {
		if typed, ok := item.(T); ok {
			return typed
		}
	}
	t.Fatalf("produced does not include %T: %v", zero, produced)
	return zero
}

func countKind(items []envelope.Envelope, kind string) int {
	var count int
	for _, item := range items {
		if item.Kind() == kind {
			count++
		}
	}
	return count
}

func countLLMKind(items []LLMEnvelope, kind string) int {
	var count int
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}
