package panel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"compound/internal/routeevidence"
)

const panelAttemptSchemaVersion = "compound.panel_attempt.v0.1"

type panelAttempt struct {
	SchemaVersion string   `json:"schema_version"`
	Kind          string   `json:"kind"`
	RunID         string   `json:"run_id"`
	WorkID        string   `json:"work_id,omitempty"`
	TargetRef     string   `json:"target_ref"`
	RequestID     string   `json:"request_id,omitempty"`
	ClientProfile string   `json:"client_profile,omitempty"`
	CallRole      string   `json:"call_role,omitempty"`
	Lane          *LaneRef `json:"lane,omitempty"`
	CreatedAt     string   `json:"created_at"`
}

// withPanelRunLock serializes the complete read-call-save lifecycle for one
// panel run. The lock lives outside panel-runs so run artifacts can be archived
// without replacing the inode against which an active executor is locked.
func withPanelRunLock(homePath, runID string, fn func() error) error {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	if !validPanelRunID(runID) {
		return fmt.Errorf("invalid panel run id %q", runID)
	}
	path := filepath.Join(homePath, "lock", "panel-runs", targetDigest(runID)+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}

func validPanelRunID(runID string) bool {
	runID = strings.TrimSpace(runID)
	return strings.HasPrefix(runID, "prun_") && filepath.Base(runID) == runID
}

func panelAttemptDir(homePath, runID string) string {
	return filepath.Join(homePath, "panel-runs", "attempts", targetDigest(runID))
}

func panelTargetAttemptPath(homePath, runID, targetRef string) string {
	return filepath.Join(panelAttemptDir(homePath, runID), "target-"+targetDigest(targetRef)+".json")
}

func panelCallAttemptPath(homePath, runID, requestID string) string {
	return filepath.Join(panelAttemptDir(homePath, runID), "call-"+targetDigest(requestID)+".json")
}

func reservePanelTargetAttempt(homePath string, run Run, targetRef string, createdAt time.Time) (bool, error) {
	attempt := panelAttempt{
		SchemaVersion: panelAttemptSchemaVersion,
		Kind:          "target",
		RunID:         run.ID,
		WorkID:        run.WorkID,
		TargetRef:     targetRef,
		CreatedAt:     createdAt.UTC().Format(time.RFC3339),
	}
	path := panelTargetAttemptPath(homePath, run.ID, targetRef)
	created, err := writePanelAttemptExclusive(path, attempt)
	if err != nil {
		return false, err
	}
	if created {
		return true, nil
	}
	existing, err := readPanelAttempt(path)
	if err != nil {
		return false, err
	}
	if err := validatePanelAttemptCore(existing, "target", run, targetRef, ""); err != nil {
		return false, err
	}
	if existing.WorkID != run.WorkID {
		return false, fmt.Errorf("panel target attempt conflicts with work identity for %s", targetRef)
	}
	return false, nil
}

func reservePanelCallAttempt(homePath string, run Run, targetRef, requestID, clientProfile, callRole string, lane LaneRef, createdAt time.Time) error {
	laneCopy := lane
	attempt := panelAttempt{
		SchemaVersion: panelAttemptSchemaVersion,
		Kind:          "call",
		RunID:         run.ID,
		WorkID:        run.WorkID,
		TargetRef:     targetRef,
		RequestID:     requestID,
		ClientProfile: clientProfile,
		CallRole:      callRole,
		Lane:          &laneCopy,
		CreatedAt:     createdAt.UTC().Format(time.RFC3339),
	}
	path := panelCallAttemptPath(homePath, run.ID, requestID)
	created, err := writePanelAttemptExclusive(path, attempt)
	if err != nil {
		return err
	}
	if created {
		return nil
	}
	if _, _, err := inspectPanelCallAttempt(homePath, run, targetRef, requestID, clientProfile, callRole, lane); err != nil {
		return err
	}
	return fmt.Errorf("panel route attempt %s already resolved; allocate a fresh request id", requestID)
}

func inspectPanelCallAttempt(homePath string, run Run, targetRef, requestID, clientProfile, callRole string, lane LaneRef) (routeevidence.Evidence, bool, error) {
	path := panelCallAttemptPath(homePath, run.ID, requestID)
	attempt, err := readPanelAttempt(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return routeevidence.Evidence{}, false, nil
		}
		return routeevidence.Evidence{}, false, err
	}
	if err := validatePanelAttemptCore(attempt, "call", run, targetRef, requestID); err != nil {
		return routeevidence.Evidence{}, false, err
	}

	existing, ok, err := routeevidence.LookupRequestID(homePath, requestID)
	if err != nil {
		return routeevidence.Evidence{}, false, err
	}
	if !ok {
		return routeevidence.Evidence{}, false, fmt.Errorf("unresolved route attempt %s has unknown_spend; refusing another provider call", requestID)
	}
	// Older reservation fixtures may omit the extended identity. New writers
	// always persist it; when present, every field must still match exactly.
	if attempt.WorkID != "" && attempt.WorkID != run.WorkID {
		return routeevidence.Evidence{}, false, fmt.Errorf("panel route attempt %s conflicts with work identity", requestID)
	}
	if attempt.ClientProfile != "" && attempt.ClientProfile != clientProfile {
		return routeevidence.Evidence{}, false, fmt.Errorf("panel route attempt %s conflicts with client profile", requestID)
	}
	if attempt.CallRole != "" && attempt.CallRole != callRole {
		return routeevidence.Evidence{}, false, fmt.Errorf("panel route attempt %s conflicts with call role", requestID)
	}
	if attempt.Lane != nil && !reflect.DeepEqual(*attempt.Lane, lane) {
		return routeevidence.Evidence{}, false, fmt.Errorf("panel route attempt %s conflicts with lane identity", requestID)
	}
	if existing.RecordedVia != routeevidence.RecorderGeneric {
		return routeevidence.Evidence{}, false, fmt.Errorf("untrusted route evidence shadows panel request %s: recorded_via=%q", requestID, existing.RecordedVia)
	}
	if !samePanelRouteIdentity(existing, requestID, run.WorkID, clientProfile, lane) {
		return routeevidence.Evidence{}, false, fmt.Errorf("panel request %s conflicts with existing route evidence %s", requestID, existing.ID)
	}
	return existing, true, nil
}

func panelTargetAttemptRefs(homePath string, run Run) (map[string]bool, error) {
	refs := map[string]bool{}
	dir := panelAttemptDir(homePath, run.ID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return refs, nil
		}
		return nil, err
	}
	allowed := map[string]bool{}
	for _, target := range run.Targets {
		allowed[target.Ref] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "target-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		attempt, err := readPanelAttempt(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if err := validatePanelAttemptCore(attempt, "target", run, attempt.TargetRef, ""); err != nil {
			return nil, err
		}
		if attempt.WorkID != run.WorkID {
			return nil, fmt.Errorf("panel target attempt %s conflicts with work identity", attempt.TargetRef)
		}
		if !allowed[attempt.TargetRef] {
			return nil, fmt.Errorf("panel target attempt %s is outside run %s", attempt.TargetRef, run.ID)
		}
		if filepath.Base(panelTargetAttemptPath(homePath, run.ID, attempt.TargetRef)) != entry.Name() {
			return nil, fmt.Errorf("panel target attempt path conflicts with target identity %s", attempt.TargetRef)
		}
		refs[attempt.TargetRef] = true
	}
	return refs, nil
}

func validatePanelAttemptCore(attempt panelAttempt, kind string, run Run, targetRef, requestID string) error {
	if attempt.SchemaVersion != panelAttemptSchemaVersion {
		return fmt.Errorf("panel attempt schema_version must be %s", panelAttemptSchemaVersion)
	}
	if attempt.Kind != kind || attempt.RunID != run.ID || attempt.TargetRef != targetRef {
		return fmt.Errorf("panel %s attempt conflicts with run or target identity", kind)
	}
	if kind == "call" && attempt.RequestID != requestID {
		return fmt.Errorf("panel call attempt conflicts with request identity %s", requestID)
	}
	if _, err := time.Parse(time.RFC3339, attempt.CreatedAt); err != nil {
		return fmt.Errorf("panel %s attempt created_at invalid: %w", kind, err)
	}
	return nil
}

func writePanelAttemptExclusive(path string, attempt panelAttempt) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	data, err := json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	cleanup := func(cause error) (bool, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return false, cause
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
}

func readPanelAttempt(path string) (panelAttempt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return panelAttempt{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var attempt panelAttempt
	if err := dec.Decode(&attempt); err != nil {
		return panelAttempt{}, fmt.Errorf("decode panel attempt %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return panelAttempt{}, fmt.Errorf("decode panel attempt %s: trailing JSON", path)
		}
		return panelAttempt{}, fmt.Errorf("decode panel attempt %s trailing JSON: %w", path, err)
	}
	return attempt, nil
}

func persistedPanelRouteTokens(homePath, runID string) (int, error) {
	path := filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	prefix := "panel:" + runID + ":"
	total := 0
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var row routeevidence.Evidence
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return 0, fmt.Errorf("parse execution route evidence line %d: %w", i+1, err)
		}
		if !strings.HasPrefix(row.RequestID, prefix) {
			continue
		}
		if row.Usage.TotalTokens < 0 {
			return 0, fmt.Errorf("panel route evidence %s has negative token usage", row.ID)
		}
		next := total + row.Usage.TotalTokens
		if next < total {
			return 0, fmt.Errorf("panel route token usage overflow for run %s", runID)
		}
		total = next
	}
	return total, nil
}
