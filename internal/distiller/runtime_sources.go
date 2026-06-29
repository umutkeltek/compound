package distiller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		src.Extra = append(src.Extra, tr)

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
