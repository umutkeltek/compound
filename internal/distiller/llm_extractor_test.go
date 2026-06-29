package distiller

import (
	"strings"
	"testing"

	"compound/internal/envelope"
)

func TestLLMExtractorNormalizesProviderResponse(t *testing.T) {
	raw := []byte(`{
		"schema_version":"compound.distiller.llm_extraction.v0.1",
		"trace_ref":"llm_trace_1",
		"extractor":"fixture-critic",
		"model":"fixture-small",
		"source_refs":["raw:turn_7"],
		"candidates":[
			{"kind":"memory","entity":"project:api","predicate":"backend_language","object":"go","confidence":0.91},
			{"kind":"capability_observation","subject":"skill:paywall-cro","statement":"paywall","detail":"missed_when","confidence":0.74,"evidence_refs":["msg_9"]},
			{"kind":"capability_observation","subject":"skill:noisy-copy","statement":"paywall","detail":"false_triggered_by","confidence":0.8,"evidence_refs":["msg_10"]}
		]
	}`)

	a, err := LLMExtractor{}.Extract(raw, DefaultPolicy())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	b, err := LLMExtractor{}.Extract(raw, DefaultPolicy())
	if err != nil {
		t.Fatalf("extract replay: %v", err)
	}
	if a.TraceRef != "llm_trace_1" || len(a.Candidates) != 3 {
		t.Fatalf("unexpected trace shape: %+v", a)
	}
	if a.Candidates[0].ID == "" || a.Candidates[0].ID != b.Candidates[0].ID {
		t.Fatalf("candidate id must be deterministic across replay: %q / %q", a.Candidates[0].ID, b.Candidates[0].ID)
	}
	if a.Candidates[0].PolicyVersion != "default.v1" {
		t.Fatalf("policy version = %q", a.Candidates[0].PolicyVersion)
	}
	if a.Candidates[0].Derivation != "llm_extracted" {
		t.Fatalf("derivation = %q", a.Candidates[0].Derivation)
	}
	if got := strings.Join(a.Candidates[0].EvidenceRefs, ","); got != "raw:turn_7" {
		t.Fatalf("missing candidate evidence should inherit response source_refs; got %q", got)
	}
	if got := strings.Join(a.Candidates[1].EvidenceRefs, ","); got != "msg_9" {
		t.Fatalf("candidate evidence refs should win over response source_refs; got %q", got)
	}
	if a.Candidates[2].Detail != "false_triggered_by" {
		t.Fatalf("false-trigger capability observation detail = %q", a.Candidates[2].Detail)
	}
	if a.Candidates[0].PromotionBasis != "" {
		t.Fatalf("LLM extractor must not accept promotion basis, got %q", a.Candidates[0].PromotionBasis)
	}
}

func TestLLMExtractorRejectsMalformedAuthorityAndKind(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "malformed json",
			raw:  `{"trace_ref":`,
			want: "parse llm extraction response",
		},
		{
			name: "missing confidence",
			raw:  `{"trace_ref":"t","candidates":[{"kind":"memory","entity":"e","predicate":"p","object":"o"}]}`,
			want: "missing confidence",
		},
		{
			name: "unknown kind",
			raw:  `{"trace_ref":"t","candidates":[{"kind":"../../evil","subject":"x","confidence":0.9}]}`,
			want: "unknown kind",
		},
		{
			name: "bad confidence",
			raw:  `{"trace_ref":"t","candidates":[{"kind":"skill","subject":"x","confidence":1.2}]}`,
			want: "outside [0,1]",
		},
		{
			name: "promotion attempt",
			raw:  `{"trace_ref":"t","candidates":[{"kind":"memory","entity":"e","predicate":"p","object":"o","confidence":0.9,"promotion_basis":"trusted_deterministic_import"}]}`,
			want: "cannot promote memory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LLMExtractor{}.Extract([]byte(tt.raw), DefaultPolicy())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLLMExtractorRunParksLiveMemory(t *testing.T) {
	home, store := setup(t)
	raw := []byte(`{
		"trace_ref":"llm_live_trace",
		"candidates":[
			{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.95},
			{"kind":"eval_case","subject":"distill.memory","statement":"missed evidence citation","confidence":0.8}
		]
	}`)

	res, err := Run(home, store, raw, DefaultPolicy(), LLMExtractor{}, t0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(res.Decisions))
	}
	if res.Decisions[0].Kind != KindMemory || res.Decisions[0].Verdict != Park {
		t.Fatalf("LLM memory must park in live mode; got %+v", res.Decisions[0])
	}
	if res.Decisions[1].Kind != KindEvalCase || res.Decisions[1].Verdict != Operationalize {
		t.Fatalf("eval case should operationalize; got %+v", res.Decisions[1])
	}
	if cur, _ := store.AllCurrent(); len(cur) != 0 {
		t.Fatalf("LLM output must not promote canon directly; got %d current claims", len(cur))
	}
}

func TestBuildLLMExtractionRequestCarriesPriorCoverage(t *testing.T) {
	home, store := setup(t)
	run(t, home, store, `{"trace_ref":"seed","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.9,"claim_type":"decision"}]}`)

	prior, err := LoadPriorStateFromHome(home, store, DefaultPolicy())
	if err != nil {
		t.Fatalf("prior: %v", err)
	}
	req := BuildLLMExtractionRequest("trace_new", []byte("raw transcript"), DefaultPolicy(), prior)
	if req.SchemaVersion != LLMExtractionSchemaVersion {
		t.Fatalf("schema version = %q", req.SchemaVersion)
	}
	if req.PriorCoverage.CurrentClaims != 1 || req.PriorCoverage.Decisions != 1 {
		t.Fatalf("prior coverage = %+v", req.PriorCoverage)
	}
	if !strings.Contains(strings.Join(req.Instructions, "\n"), "cannot promote memory directly") {
		t.Fatalf("request instructions do not preserve authority boundary: %v", req.Instructions)
	}
}

func TestBuildLLMExtractionRequestFromSpineSources(t *testing.T) {
	home, store := setup(t)
	run(t, home, store, `{"trace_ref":"seed","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.9,"claim_type":"decision"}]}`)
	prior, err := LoadPriorStateFromHome(home, store, DefaultPolicy())
	if err != nil {
		t.Fatalf("prior: %v", err)
	}

	ctx := &envelope.ContextPack{
		Header:    llmTestHeader(envelope.SVContextPack, "ctx_distill", "work_distill", ""),
		SummaryMD: "Use product context; governance excluded.",
		Included: []envelope.IncludedRef{{
			Ref:         "repo://docs",
			SourceClass: "source",
			Reason:      "product task",
			Confidence:  0.9,
			Status:      "current",
		}},
		Excluded: []envelope.ExcludedRef{{
			Ref:    "control-plane://workflow-contract",
			Reason: "not ambient for product repo",
		}},
		ActiveInstructions: []string{"keep context scoped"},
		RequiredChecks:     []string{"memory doctor"},
		Avoid:              []string{"full OS governance dump"},
	}
	work := &envelope.WorkEnvelope{
		Header:           llmTestHeader(envelope.SVWork, "work_distill", "work_distill", "ctx_distill"),
		Owner:            "distiller:llm",
		State:            "running",
		DesiredOutcome:   "extract useful candidates from a bounded run",
		SuccessPredicate: "LLM request packet contains context, work, receipt, and prior coverage",
		NonGoals:         []string{"call a live model", "promote memory"},
		RunIDs:           []string{"act_distill"},
	}
	receipt := &envelope.Receipt{
		Header:   llmTestHeader(envelope.SVReceipt, "rec_distill", "work_distill", "ctx_distill"),
		Outcome:  "completed",
		MoveType: "memory_delta",
		StateDelta: envelope.StateDelta{
			Changed:      true,
			Summary:      "distiller found a reusable extraction gap",
			FilesChanged: []string{"internal/distiller/llm_extractor.go"},
			Artifacts:    []string{"artifact_request_packet"},
			Verification: []string{"go test ./internal/distiller"},
		},
		Cost: envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{
			Used:           []string{"capability://skill:golang-testing"},
			Missed:         []string{"capability://skill:distiller-request-packet"},
			FalseTriggered: []string{"capability://skill:generic-copy"},
			Candidate:      []string{"capability://skill:offered-only"},
		},
		Memory: envelope.ReceiptMemory{Candidates: []string{"cand_method"}, Promoted: []string{}, Parked: []string{"cand_method"}},
		Next:   envelope.ReceiptNext{SuggestedWork: []string{"add offline eval runner"}, ReviewTrigger: "method debt > 0"},
	}
	hook := &envelope.HookEvent{
		Header:      llmTestHeader(envelope.SVHookEvent, "hook_distill", "work_distill", "ctx_distill"),
		Harness:     "codex",
		HookName:    "PostToolUse",
		InputHash:   "sha256:abc",
		SideEffects: []string{"wrote_candidate"},
		OutputRef:   "artifact_request_packet",
		DurationMS:  42,
		Status:      "ok",
	}

	req, err := BuildLLMExtractionRequestFromSources("trace_spine", LLMExtractionSources{
		RawTrace:    []byte("operator said this should become reusable"),
		ContextPack: ctx,
		Work:        work,
		Receipt:     receipt,
		Extra:       []envelope.Envelope{hook},
	}, DefaultPolicy(), prior)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Context == nil || req.Context.ID != "ctx_distill" || len(req.Context.Excluded) != 1 {
		t.Fatalf("context summary missing excluded refs: %+v", req.Context)
	}
	if req.Work == nil || req.Work.SuccessPredicate == "" || len(req.Work.NonGoals) != 2 {
		t.Fatalf("work summary incomplete: %+v", req.Work)
	}
	if req.Receipt == nil || req.Receipt.StateDelta.Summary == "" || req.Receipt.Next.ReviewTrigger == "" {
		t.Fatalf("receipt summary incomplete: %+v", req.Receipt)
	}
	if req.Receipt.Cost.ModelCalls == nil {
		t.Fatalf("receipt cost metadata missing: %+v", req.Receipt.Cost)
	}
	if len(req.CapabilityFeedback) != 2 {
		t.Fatalf("capability feedback = %+v, want 2 explicit items", req.CapabilityFeedback)
	}
	if req.CapabilityFeedback[0].Subject != "skill:distiller-request-packet" || req.CapabilityFeedback[0].Detail != "missed_when" {
		t.Fatalf("missed feedback not normalized: %+v", req.CapabilityFeedback[0])
	}
	if req.CapabilityFeedback[1].Subject != "skill:generic-copy" || req.CapabilityFeedback[1].Detail != "false_triggered_by" {
		t.Fatalf("false-trigger feedback not normalized: %+v", req.CapabilityFeedback[1])
	}
	for _, feedback := range req.CapabilityFeedback {
		if feedback.Statement != receipt.StateDelta.Summary {
			t.Fatalf("feedback context = %q, want receipt summary %q", feedback.Statement, receipt.StateDelta.Summary)
		}
		if hasString(feedback.EvidenceRefs, "capability://skill:offered-only") {
			t.Fatalf("candidate offers must stay inert, got evidence refs %+v", feedback.EvidenceRefs)
		}
	}
	if len(req.Envelopes) != 1 || req.Envelopes[0].Kind != "hook_event" || !strings.Contains(req.Envelopes[0].Summary, "PostToolUse") {
		t.Fatalf("extra envelope summary = %+v", req.Envelopes)
	}
	for _, want := range []string{"raw_trace:trace_spine", "context_pack:ctx_distill", "work:work_distill", "receipt:rec_distill", "hook_event:hook_distill"} {
		if !hasString(req.SourceRefs, want) {
			t.Fatalf("source refs missing %q: %v", want, req.SourceRefs)
		}
	}
	if req.PriorCoverage.CurrentClaims != 1 || req.PriorCoverage.Decisions != 1 {
		t.Fatalf("prior coverage = %+v", req.PriorCoverage)
	}
	if !strings.Contains(strings.Join(req.Instructions, "\n"), "Use context/work/receipt as evidence-bearing source material") {
		t.Fatalf("instructions missing source-boundary rule: %v", req.Instructions)
	}
	if !strings.Contains(strings.Join(req.Instructions, "\n"), "Do not infer missed_when or false_triggered_by from skills.candidate offers") {
		t.Fatalf("instructions missing candidate-offer no-inference rule: %v", req.Instructions)
	}
}

func TestBuildLLMExtractionRequestRejectsInvalidSourceEnvelope(t *testing.T) {
	badContext := &envelope.ContextPack{
		Header:   llmTestHeader(envelope.SVContextPack, "ctx_bad", "", ""),
		Included: []envelope.IncludedRef{},
		// Excluded must be present by envelope contract.
	}
	_, err := BuildLLMExtractionRequestFromSources("trace_bad", LLMExtractionSources{ContextPack: badContext}, DefaultPolicy(), PriorState{})
	if err == nil || !strings.Contains(err.Error(), "context pack invalid") {
		t.Fatalf("err = %v, want context pack invalid", err)
	}
}

func llmTestHeader(schemaVersion, id, workID, contextPackID string) envelope.Header {
	return envelope.Header{
		SchemaVersion: schemaVersion,
		ID:            id,
		CreatedAt:     "2026-06-29T12:00:00Z",
		Producer:      envelope.Producer{Kind: "test", Name: "compound"},
		IDs:           envelope.IDs{WorkID: workID, ContextPackID: contextPackID},
		Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       "private",
		Risk:          "tier1",
		EvidenceRefs:  []string{},
		Uncertainty:   envelope.Uncertainty{Level: "known"},
	}
}
