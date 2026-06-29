package distiller

import (
	"encoding/json"
	"fmt"
	"strings"
)

const LLMExtractionSchemaVersion = "compound.distiller.llm_extraction.v0.1"

// LLMExtractionRequest is the provider-neutral packet sent to a model lane.
// It is data, not a live client: callers can route it through Switchback, a local
// model, or a fixture without changing distiller authority rules.
type LLMExtractionRequest struct {
	SchemaVersion string        `json:"schema_version"`
	TraceRef      string        `json:"trace_ref"`
	Policy        Policy        `json:"policy"`
	PriorCoverage PriorCoverage `json:"prior_coverage"`
	RawTrace      string        `json:"raw_trace,omitempty"`
	Instructions  []string      `json:"instructions"`
}

func BuildLLMExtractionRequest(traceRef string, rawTrace []byte, policy Policy, prior PriorState) LLMExtractionRequest {
	return LLMExtractionRequest{
		SchemaVersion: LLMExtractionSchemaVersion,
		TraceRef:      traceRef,
		Policy:        policy,
		PriorCoverage: prior.Coverage(),
		RawTrace:      string(rawTrace),
		Instructions: []string{
			"Return JSON matching compound.distiller.llm_extraction.v0.1.",
			"Extract candidates only; do not decide truth, promotion, writeback, or policy changes.",
			"Omit promotion_basis. LLM output cannot promote memory directly.",
			"Every candidate must include kind, confidence in [0,1], and evidence_refs when granular evidence exists.",
		},
	}
}

// LLMExtractionResponse is the model-shaped result consumed by LLMExtractor.
// It intentionally has no client/provider dependency and no authority-bearing fields.
type LLMExtractionResponse struct {
	SchemaVersion string         `json:"schema_version,omitempty"`
	TraceRef      string         `json:"trace_ref"`
	Extractor     string         `json:"extractor,omitempty"`
	Model         string         `json:"model,omitempty"`
	SourceRefs    []string       `json:"source_refs,omitempty"`
	Candidates    []LLMCandidate `json:"candidates"`
	Critique      []string       `json:"critique,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
}

// LLMCandidate mirrors Candidate but keeps authority-sensitive fields under parser control.
type LLMCandidate struct {
	Kind           string            `json:"kind"`
	Entity         string            `json:"entity,omitempty"`
	Predicate      string            `json:"predicate,omitempty"`
	Object         string            `json:"object,omitempty"`
	Subject        string            `json:"subject,omitempty"`
	Statement      string            `json:"statement,omitempty"`
	Detail         string            `json:"detail,omitempty"`
	Confidence     *float64          `json:"confidence"`
	Derivation     string            `json:"derivation,omitempty"`
	ClaimType      string            `json:"claim_type,omitempty"`
	Qualifiers     map[string]string `json:"qualifiers,omitempty"`
	PromotionBasis string            `json:"promotion_basis,omitempty"`
	EvidenceRefs   []string          `json:"evidence_refs,omitempty"`
}

// LLMExtractor consumes an already-produced model response. It does not call a model.
type LLMExtractor struct{}

func (LLMExtractor) Extract(raw []byte, policy Policy) (Trace, error) {
	var response LLMExtractionResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return Trace{}, fmt.Errorf("parse llm extraction response: %w", err)
	}
	if strings.TrimSpace(response.TraceRef) == "" {
		return Trace{}, fmt.Errorf("llm extraction response missing trace_ref")
	}

	trace := Trace{
		TraceRef:   response.TraceRef,
		Candidates: make([]Candidate, 0, len(response.Candidates)),
	}
	for i, lc := range response.Candidates {
		c, err := lc.toCandidate(i, response)
		if err != nil {
			return Trace{}, err
		}
		trace.Candidates = append(trace.Candidates, c)
	}
	return normalizeTrace(trace, policy)
}

func (lc LLMCandidate) toCandidate(index int, response LLMExtractionResponse) (Candidate, error) {
	if strings.TrimSpace(lc.PromotionBasis) != "" {
		return Candidate{}, fmt.Errorf("llm candidate %d attempted promotion_basis %q; model output cannot promote memory", index, lc.PromotionBasis)
	}
	if lc.Confidence == nil {
		return Candidate{}, fmt.Errorf("llm candidate %d missing confidence", index)
	}
	if *lc.Confidence < 0 || *lc.Confidence > 1 {
		return Candidate{}, fmt.Errorf("llm candidate %d confidence %.2f outside [0,1]", index, *lc.Confidence)
	}

	derivation := strings.TrimSpace(lc.Derivation)
	if derivation == "" {
		derivation = "llm_extracted"
	}
	evidenceRefs := append([]string(nil), lc.EvidenceRefs...)
	if len(evidenceRefs) == 0 && len(response.SourceRefs) > 0 {
		evidenceRefs = append(evidenceRefs, response.SourceRefs...)
	}

	return Candidate{
		Kind:         lc.Kind,
		Entity:       lc.Entity,
		Predicate:    lc.Predicate,
		Object:       lc.Object,
		Subject:      lc.Subject,
		Statement:    lc.Statement,
		Detail:       lc.Detail,
		Confidence:   *lc.Confidence,
		Derivation:   derivation,
		ClaimType:    lc.ClaimType,
		Qualifiers:   lc.Qualifiers,
		EvidenceRefs: evidenceRefs,
	}, nil
}
