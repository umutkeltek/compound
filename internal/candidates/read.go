package candidates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// candidateID rejects an id that is empty or path-shaped (a directory component,
// "." / "..", or a path separator). A candidate id keys an inbox file path, so a
// crafted value must never be able to escape inbox/<kind>-candidates/.
func candidateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("candidate id required")
	}
	if id == "." || id == ".." || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("candidate id must be a bare filename, got %q", id)
	}
	return nil
}

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
	if err := candidateID(id); err != nil {
		return err
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
// two-space-indented JSON plus a trailing newline).
//
// It refuses to file a record under a path the record itself contradicts: an
// unknown kind, a path-shaped id, or a payload whose own non-empty json "id" /
// "kind" disagrees with the requested id / kind are all rejected before any disk
// write.
func WriteCandidate(homePath string, kind Kind, id string, payload any) error {
	dir, data, err := candidatePayload(kind, id, payload)
	if err != nil {
		return err
	}
	full := filepath.Join(homePath, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(full, id+".json"), data, 0o644)
}

// CreateCandidateIfAbsent persists a candidate only when its canonical inbox
// path does not already exist. Any existing candidate remains byte-identical;
// unsafe existing objects are reported rather than followed or replaced.
func CreateCandidateIfAbsent(homePath string, kind Kind, id string, payload any) (bool, error) {
	dir, data, err := candidatePayload(kind, id, payload)
	if err != nil {
		return false, err
	}
	full := filepath.Join(homePath, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		return false, err
	}
	target := filepath.Join(full, id+".json")
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, classifyExistingCandidate(target, kind, id)
		}
		return false, err
	}
	created := true
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return created, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return created, err
	}
	return created, nil
}

func candidatePayload(kind Kind, id string, payload any) (string, []byte, error) {
	dir := InboxDir(kind)
	if dir == "" {
		return "", nil, fmt.Errorf("unknown candidate kind %q", kind)
	}
	if err := candidateID(id); err != nil {
		return "", nil, err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", nil, err
	}
	var meta struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", nil, err
	}
	if meta.ID != "" && meta.ID != id {
		return "", nil, fmt.Errorf("candidate payload id %q does not match requested %q", meta.ID, id)
	}
	if meta.Kind != "" && meta.Kind != string(kind) {
		return "", nil, fmt.Errorf("candidate payload kind %q does not match requested kind %q", meta.Kind, kind)
	}
	return dir, append(data, '\n'), nil
}

func classifyExistingCandidate(path string, kind Kind, id string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect existing candidate: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("existing candidate %s is not a regular file (%s)", path, info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read existing candidate: %w", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("decode existing candidate: %w", err)
	}
	if object == nil {
		return fmt.Errorf("decode existing candidate: expected JSON object")
	}
	var meta struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("decode existing candidate identity: %w", err)
	}
	if meta.ID != "" && meta.ID != id {
		return fmt.Errorf("existing candidate id %q does not match requested %q", meta.ID, id)
	}
	if meta.Kind != "" && meta.Kind != string(kind) {
		return fmt.Errorf("existing candidate kind %q does not match requested kind %q", meta.Kind, kind)
	}
	return nil
}
