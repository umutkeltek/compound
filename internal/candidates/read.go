package candidates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	linkCandidateFile        = os.Link
	syncCandidateDirectoryFn = syncCandidateDirectory
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
	return withCandidatePublicationLock(homePath, func() error {
		full := filepath.Join(homePath, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			return err
		}
		return replaceCandidate(full, filepath.Join(full, id+".json"), data)
	})
}

// CreateCandidateIfAbsent persists a candidate only when its canonical inbox
// path does not already exist. Any existing candidate remains byte-identical;
// unsafe existing objects are reported rather than followed or replaced.
func CreateCandidateIfAbsent(homePath string, kind Kind, id string, payload any) (bool, error) {
	dir, data, err := candidatePayload(kind, id, payload)
	if err != nil {
		return false, err
	}
	created := false
	err = withCandidatePublicationLock(homePath, func() error {
		full := filepath.Join(homePath, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			return err
		}
		target := filepath.Join(full, id+".json")
		tempPath, err := stageCandidate(full, data)
		if err != nil {
			return err
		}
		keepTemp := true
		defer func() {
			if keepTemp {
				_ = os.Remove(tempPath)
			}
		}()
		if err := linkCandidateFile(tempPath, target); err != nil {
			if removeErr := os.Remove(tempPath); removeErr != nil {
				return errors.Join(fmt.Errorf("publish candidate: %w", err), fmt.Errorf("remove candidate temp: %w", removeErr))
			}
			keepTemp = false
			if os.IsExist(err) {
				return classifyExistingCandidate(target, kind, id)
			}
			return fmt.Errorf("publish candidate: %w", err)
		}
		created = true
		var cleanupErr error
		if err := os.Remove(tempPath); err != nil {
			cleanupErr = fmt.Errorf("remove candidate temp: %w", err)
		} else {
			keepTemp = false
		}
		var syncErr error
		if err := syncCandidateDirectoryFn(full); err != nil {
			syncErr = fmt.Errorf("sync candidate directory: %w", err)
		}
		return errors.Join(cleanupErr, syncErr)
	})
	return created, err
}

// withCandidatePublicationLock serializes candidate publication across
// processes. Memory may call a candidate writer while holding mem.lock, so the
// only permitted nesting is mem.lock -> candidates.lock; this leaf never
// reaches back into memory or acquires another Compound lock.
func withCandidatePublicationLock(homePath string, fn func() error) (resultErr error) {
	lockPath := filepath.Join(homePath, "lock", "candidates.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create candidate lock directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open candidate publication lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return fmt.Errorf("lock candidate publication: %w", err)
	}
	defer func() {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("unlock candidate publication: %w", err))
		}
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close candidate publication lock: %w", err))
		}
	}()
	return fn()
}

func replaceCandidate(dir, target string, data []byte) error {
	if err := candidateReplacementPreflight(target); err != nil {
		return err
	}
	tempPath, err := stageCandidate(dir, data)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("replace candidate: %w", err)
	}
	if err := syncCandidateDirectoryFn(dir); err != nil {
		return fmt.Errorf("sync candidate directory: %w", err)
	}
	return nil
}

// candidateReplacementPreflight preserves WriteCandidate's existing
// authorization boundary: atomic rename must not make a read-only regular
// target replaceable when the former in-place writer could not open it.
func candidateReplacementPreflight(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect candidate replacement target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	file, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("authorize candidate replacement: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close candidate replacement preflight: %w", err)
	}
	return nil
}

func stageCandidate(dir string, data []byte) (_ string, resultErr error) {
	file, err := os.CreateTemp(dir, ".candidate-publish-*")
	if err != nil {
		return "", fmt.Errorf("create candidate temp: %w", err)
	}
	tempPath := file.Name()
	closed := false
	keep := false
	defer func() {
		if !closed {
			if err := file.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close candidate temp: %w", err))
			}
		}
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o644); err != nil {
		return "", fmt.Errorf("set candidate temp mode: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return "", fmt.Errorf("write candidate temp: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync candidate temp: %w", err)
	}
	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		return "", fmt.Errorf("close candidate temp: %w", closeErr)
	}
	keep = true
	return tempPath, nil
}

func syncCandidateDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
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
