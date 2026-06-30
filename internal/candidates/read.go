package candidates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadCandidate reads inbox/<kind>-candidates/<id>.json from COMPOUND_HOME,
// verifies the stored id matches the requested id, and decodes the record into
// target. The candidate inbox JSON is a cross-package contract: every reader
// (distiller's read*Candidate, mem's write-plan reader, decisions' decision
// reader) re-implemented the identical path-build + read + id guard. This is the
// one owner of that read; target is any struct whose json tags project the
// fields its package needs from the one record.
//
// A read error — including a missing file — is returned unwrapped so a caller
// that probes several inboxes can test it with os.IsNotExist. A stored id that
// is non-empty and differs from the requested id is the contract violation. An
// empty stored id is left to the caller to default.
func ReadCandidate(homePath string, kind Kind, id string, target any) error {
	dir := InboxDir(kind)
	if dir == "" {
		return fmt.Errorf("unknown candidate kind %q", kind)
	}
	data, err := os.ReadFile(filepath.Join(homePath, dir, id+".json"))
	if err != nil {
		return err
	}
	var idCheck struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &idCheck); err != nil {
		return err
	}
	if idCheck.ID != "" && idCheck.ID != id {
		return fmt.Errorf("candidate file id %q does not match requested %q", idCheck.ID, id)
	}
	return json.Unmarshal(data, target)
}

// WriteCandidate persists a candidate record to inbox/<kind>-candidates/<id>.json
// under COMPOUND_HOME. It is the one owner of the candidate-inbox WRITE mechanism
// — the twin of ReadCandidate — so every producer (distiller fan-out, source
// observations, product hypotheses, memory capture/adjudication) hands its own
// projected record as payload and shares this path + serialization (canonical
// two-space-indented JSON plus a trailing newline). An unknown kind is rejected
// before any disk write.
func WriteCandidate(homePath string, kind Kind, id string, payload any) error {
	dir := InboxDir(kind)
	if dir == "" {
		return fmt.Errorf("unknown candidate kind %q", kind)
	}
	full := filepath.Join(homePath, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(full, id+".json"), append(data, '\n'), 0o644)
}
