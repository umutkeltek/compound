package distiller

import (
	"strings"
	"testing"

	"compound/internal/contract"
	"compound/internal/envelope"
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
		MoveType: "memory_delta",
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
	if len(src.Extra) != 1 || src.Extra[0].Kind() != "transition" {
		t.Fatalf("loaded extra = %+v, want transition only", src.Extra)
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
		"transition:" + tr.ID,
	} {
		if !hasString(req.SourceRefs, want) {
			t.Fatalf("request source refs missing %q: %v", want, req.SourceRefs)
		}
	}
	if req.RawTrace != "" {
		t.Fatalf("runtime loader should not invent raw trace: %q", req.RawTrace)
	}
	if req.Work == nil || req.Work.State != "completed" || !hasString(req.Work.RunIDs, "run_pricing_distill") {
		t.Fatalf("request work summary incomplete: %+v", req.Work)
	}
	if req.Context == nil || !strings.Contains(req.Context.SummaryMD, "product") {
		t.Fatalf("request context summary incomplete: %+v", req.Context)
	}
	if req.Receipt == nil || req.Receipt.StateDelta.Summary == "" {
		t.Fatalf("request receipt summary incomplete: %+v", req.Receipt)
	}
	if len(req.Envelopes) != 1 || req.Envelopes[0].ID != tr.ID {
		t.Fatalf("request extra envelopes = %+v, want transition %s", req.Envelopes, tr.ID)
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
		State:            "completed",
	}, t0)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	_, err = LoadRuntimeSources(homePath, w.ID)
	if err == nil || !strings.Contains(err.Error(), "missing transition_id") {
		t.Fatalf("err = %v, want missing transition_id", err)
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
