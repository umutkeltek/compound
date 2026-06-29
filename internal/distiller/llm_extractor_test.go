package distiller

import (
	"strings"
	"testing"
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
			{"kind":"capability_observation","subject":"skill:paywall-cro","statement":"paywall","detail":"missed_when","confidence":0.74,"evidence_refs":["msg_9"]}
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
	if a.TraceRef != "llm_trace_1" || len(a.Candidates) != 2 {
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
