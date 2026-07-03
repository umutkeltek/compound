package distiller

import (
	"encoding/json"
	"fmt"
	"strings"

	"compound/internal/reviewartifact"
)

const LLMRequestRecordRefPrefix = "distiller_request:"

type LLMRequestRecord struct {
	ID   string
	Ref  string
	Path string
}

// RecordLLMExtractionRequest persists an exact replay packet under COMPOUND_HOME.
// The artifact is still only adapter input: it does not call a model, write
// candidates, adjudicate truth, or mutate distiller policy.
func RecordLLMExtractionRequest(homePath string, req LLMExtractionRequest) (LLMRequestRecord, error) {
	if strings.TrimSpace(homePath) == "" {
		return LLMRequestRecord{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(req.TraceRef) == "" {
		return LLMRequestRecord{}, fmt.Errorf("trace_ref is required")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return LLMRequestRecord{}, err
	}
	id := reviewartifact.ID("llmreq_", req.TraceRef, string(data))
	path, err := reviewartifact.WriteJSON(homePath, "distiller-requests", id+".json", req, " ")
	if err != nil {
		return LLMRequestRecord{}, err
	}
	return LLMRequestRecord{
		ID:   id,
		Ref:  LLMRequestRecordRefPrefix + id,
		Path: path,
	}, nil
}
