package candidates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

var (
	linkCandidateFile = func(directory *candidateDirectory, source, target string) error {
		return directory.link(source, target)
	}
	renameCandidateFile = func(directory *candidateDirectory, source, target string) error {
		return directory.rename(source, target)
	}
	removeCandidateFile = func(directory *candidateDirectory, name string) error {
		return directory.remove(name)
	}
	syncCandidateDirectoryFn = func(directory *candidateDirectory) error {
		return directory.sync()
	}
)

// candidateID rejects an id that is empty or path-shaped (a directory component,
// "." / "..", or a path separator). A candidate id keys an inbox file path, so a
// crafted value must never be able to escape inbox/<kind>-candidates/.
func candidateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("candidate id required")
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("candidate id must be valid UTF-8")
	}
	if strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return fmt.Errorf("candidate id must not contain control characters, got %q", id)
	}
	if id != strings.TrimSpace(id) {
		return fmt.Errorf("candidate id must not contain surrounding whitespace, got %q", id)
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
// A read error — including a missing file — preserves os.IsNotExist semantics
// so a caller that probes several inboxes can continue. Custody, canonical
// filename, and nonblank matching JSON identity are enforced before target is
// decoded.
func ReadCandidate(homePath string, kind Kind, id string, target any) error {
	record, err := ReadRecord(homePath, kind, id)
	if err != nil {
		return err
	}
	return json.Unmarshal(record.Body, target)
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
	return withCandidatePublicationLock(homePath, dir, func(inbox *candidateInbox) error {
		return replaceCandidate(inbox, kind, id, data)
	})
}

// ReplaceCandidateResult distinguishes a committed replacement from an exact
// newer generation observed under the same candidate publication lock.
type ReplaceCandidateResult struct {
	Replaced bool
	Current  Record
}

// DigestPrecondition is the durable part of a descriptor-pinned candidate
// record that a persisted plan may carry across processes. The mutation door
// reacquires rooted custody and compares this digest while holding the
// candidate publication lock; callers never reconstruct Record identities.
type DigestPrecondition struct {
	ID     string
	Digest string
}

// ConditionalMutationResult reports the exact lock winner when any target or
// companion precondition drifted. Target is always the current target record
// when it was readable; Current is the record named by MismatchID.
type ConditionalMutationResult struct {
	Applied    bool
	Target     Record
	Current    Record
	MismatchID string
}

// ReplaceCandidateIfCurrent atomically replaces a candidate only when the
// descriptor-pinned record read by an earlier page walk is still the exact
// live generation. The comparison and replacement share the candidate
// publication lock, so another producer cannot win between the precondition
// check and rename. A valid newer generation returns replaced=false without
// being overwritten; corruption or root-custody drift remains an error.
func ReplaceCandidateIfCurrent(homePath string, expected Record, payload any) (result ReplaceCandidateResult, resultErr error) {
	if err := expected.Validate(); err != nil {
		return result, fmt.Errorf("validate expected candidate record: %w", err)
	}
	dir, data, err := candidatePayload(expected.Kind, expected.ID, payload)
	if err != nil {
		return result, err
	}
	err = withCandidatePublicationLock(homePath, dir, func(inbox *candidateInbox) error {
		if !expected.homeIdentity.sameObject(inbox.home.identity) || !expected.directoryIdentity.sameObject(inbox.directory.identity) {
			return &Corruption{
				EntryCursor: expected.EntryCursor,
				Code:        CorruptionRaced,
				Detail:      "candidate root custody changed since page read",
			}
		}
		current, err := readRootedRecord(inbox, expected.Kind, string(expected.EntryCursor), expected.ID)
		if err != nil {
			return err
		}
		if !expected.fileIdentity.same(current.fileIdentity) || expected.Digest != current.Digest || !bytes.Equal(expected.Body, current.Body) {
			result.Current = current
			return nil
		}
		if err := replaceCandidateFromRecord(inbox, expected.Kind, expected.ID, data, current); err != nil {
			return err
		}
		result.Replaced = true
		return nil
	})
	return result, err
}

// MutateCandidateIfDigests validates one target and any companion records
// under the candidate publication lock, then lets the owner prepare the
// target's replacement while that exact generation remains pinned. This is
// the cross-process CAS seam for durable plans: a producer replacement either
// wins before validation (the mutation callback never runs) or after the
// committed replacement, never between validation and an authority write.
func MutateCandidateIfDigests(
	homePath string,
	kind Kind,
	targetID string,
	preconditions []DigestPrecondition,
	mutate func(Record) (any, error),
) (result ConditionalMutationResult, resultErr error) {
	if !Known(kind) {
		return result, fmt.Errorf("unknown candidate kind %q", kind)
	}
	if err := candidateID(targetID); err != nil {
		return result, err
	}
	if mutate == nil {
		return result, fmt.Errorf("candidate mutation callback required")
	}
	if len(preconditions) == 0 {
		return result, fmt.Errorf("candidate digest preconditions required")
	}
	seen := make(map[string]bool, len(preconditions))
	targetDeclared := false
	for _, precondition := range preconditions {
		if err := candidateID(precondition.ID); err != nil {
			return result, err
		}
		if strings.TrimSpace(precondition.Digest) == "" {
			return result, fmt.Errorf("candidate %s digest required", precondition.ID)
		}
		if seen[precondition.ID] {
			return result, fmt.Errorf("duplicate candidate digest precondition %s", precondition.ID)
		}
		seen[precondition.ID] = true
		targetDeclared = targetDeclared || precondition.ID == targetID
	}
	if !targetDeclared {
		return result, fmt.Errorf("target candidate %s has no digest precondition", targetID)
	}

	dir := InboxDir(kind)
	return result, withCandidatePublicationLock(homePath, dir, func(inbox *candidateInbox) error {
		var target Record
		for _, precondition := range preconditions {
			current, err := readRootedRecord(inbox, kind, precondition.ID+".json", precondition.ID)
			if err != nil {
				if os.IsNotExist(err) {
					result.MismatchID = precondition.ID
					return nil
				}
				return err
			}
			if precondition.ID == targetID {
				target = current
				result.Target = current
			}
			if current.Digest != precondition.Digest {
				result.MismatchID = precondition.ID
				result.Current = current
				return nil
			}
		}

		payload, err := mutate(target)
		if err != nil {
			return err
		}
		payloadDir, data, err := candidatePayload(kind, targetID, payload)
		if err != nil {
			return err
		}
		if payloadDir != dir {
			return fmt.Errorf("candidate payload directory %q does not match %q", payloadDir, dir)
		}
		if err := replaceCandidateFromRecord(inbox, kind, targetID, data, target); err != nil {
			return err
		}
		current, err := readRootedRecord(inbox, kind, targetID+".json", targetID)
		if err != nil {
			return fmt.Errorf("read committed candidate %s: %w", targetID, err)
		}
		result.Applied = true
		result.Target = current
		result.Current = current
		return nil
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
	err = withCandidatePublicationLock(homePath, dir, func(inbox *candidateInbox) error {
		directory := inbox.directory
		target := id + ".json"
		tempName, err := stageCandidate(directory, data)
		if err != nil {
			return err
		}
		keepTemp := true
		defer func() {
			if keepTemp {
				_ = directory.remove(tempName)
			}
		}()
		if err := linkCandidateFile(directory, tempName, target); err != nil {
			if removeErr := removeCandidateFile(directory, tempName); removeErr != nil {
				return errors.Join(fmt.Errorf("publish candidate: %w", err), fmt.Errorf("remove candidate temp: %w", removeErr))
			}
			keepTemp = false
			if os.IsExist(err) {
				return classifyExistingCandidate(inbox, kind, id)
			}
			return fmt.Errorf("publish candidate: %w", err)
		}
		created = true
		var cleanupErr error
		if err := removeCandidateFile(directory, tempName); err != nil {
			cleanupErr = fmt.Errorf("remove candidate temp: %w", err)
		} else {
			keepTemp = false
		}
		var syncErr error
		if err := syncCandidateDirectoryFn(directory); err != nil {
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
func withCandidatePublicationLock(homePath, inboxPath string, fn func(*candidateInbox) error) (resultErr error) {
	home, err := openCandidateHome(homePath)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, home.close())
	}()
	lockDirectory, err := home.openDirectory("lock", true)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lockDirectory.close())
	}()
	lockFile, err := lockDirectory.openPublicationLock()
	if err != nil {
		return err
	}
	defer func() {
		if err := lockFile.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close candidate publication lock: %w", err))
		}
	}()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock candidate publication: %w", err)
	}
	defer func() {
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("unlock candidate publication: %w", err))
		}
	}()
	directory, err := home.openDirectory(inboxPath, true)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.close())
	}()
	return fn(&candidateInbox{home: home, directory: directory})
}

func replaceCandidate(inbox *candidateInbox, kind Kind, id string, data []byte) error {
	if err := candidateReplacementPreflight(inbox, kind, id); err != nil {
		return err
	}
	return publishCandidateReplacement(inbox.directory, id+".json", data)
}

func replaceCandidateFromRecord(inbox *candidateInbox, kind Kind, id string, data []byte, current Record) error {
	if err := authorizeCandidateReplacement(inbox, kind, id, current); err != nil {
		return err
	}
	return publishCandidateReplacement(inbox.directory, id+".json", data)
}

func publishCandidateReplacement(directory *candidateDirectory, target string, data []byte) error {
	tempName, err := stageCandidate(directory, data)
	if err != nil {
		return err
	}
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = directory.remove(tempName)
		}
	}()
	if err := renameCandidateFile(directory, tempName, target); err != nil {
		return fmt.Errorf("replace candidate: %w", err)
	}
	keepTemp = false
	if err := syncCandidateDirectoryFn(directory); err != nil {
		return fmt.Errorf("sync candidate directory: %w", err)
	}
	return nil
}

// candidateReplacementPreflight preserves WriteCandidate's existing
// authorization boundary: atomic rename must not make a read-only regular
// target replaceable when the former in-place writer could not open it.
func candidateReplacementPreflight(inbox *candidateInbox, kind Kind, id string) error {
	record, err := readCandidateRecordFromDirectory(inbox, kind, id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect candidate replacement target: %w", err)
	}
	return authorizeCandidateReplacement(inbox, kind, id, record)
}

func authorizeCandidateReplacement(inbox *candidateInbox, kind Kind, id string, record Record) error {
	if err := classifyCandidateRecord(record, kind, id); err != nil {
		return err
	}
	file, err := inbox.directory.openExistingWritable(id + ".json")
	if err != nil {
		return fmt.Errorf("authorize candidate replacement: %w", err)
	}
	identity, identityErr := candidateIdentityForFile(file)
	if identityErr == nil && !identity.same(record.fileIdentity) {
		identityErr = fmt.Errorf("candidate replacement target changed after custody read")
	}
	if closeErr := file.Close(); identityErr != nil || closeErr != nil {
		return errors.Join(identityErr, closeErr)
	}
	return nil
}

func stageCandidate(directory *candidateDirectory, data []byte) (_ string, resultErr error) {
	tempName, file, err := directory.createTempFile()
	if err != nil {
		return "", fmt.Errorf("create candidate temp: %w", err)
	}
	closed := false
	keep := false
	defer func() {
		if !closed {
			if err := file.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close candidate temp: %w", err))
			}
		}
		if !keep {
			_ = directory.remove(tempName)
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
	return tempName, nil
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
	meta, err := decodeCandidateJSONIdentity(data)
	if err != nil {
		return "", nil, err
	}
	if meta.ID == "" {
		return "", nil, fmt.Errorf("candidate payload id required")
	}
	if err := candidateID(meta.ID); err != nil {
		return "", nil, fmt.Errorf("candidate payload id: %w", err)
	}
	if meta.ID != id {
		return "", nil, fmt.Errorf("candidate payload id %q does not match requested %q", meta.ID, id)
	}
	if meta.Kind != "" && meta.Kind != string(kind) {
		return "", nil, fmt.Errorf("candidate payload kind %q does not match requested kind %q", meta.Kind, kind)
	}
	return dir, append(data, '\n'), nil
}

func classifyExistingCandidate(inbox *candidateInbox, kind Kind, id string) error {
	record, err := readCandidateRecordFromDirectory(inbox, kind, id)
	if err != nil {
		return fmt.Errorf("inspect existing candidate: %w", err)
	}
	return classifyCandidateRecord(record, kind, id)
}

func classifyCandidateRecord(record Record, kind Kind, id string) error {
	meta, err := decodeCandidateJSONIdentity(record.Body)
	if err != nil {
		return fmt.Errorf("decode existing candidate identity: %w", err)
	}
	if record.ID != id {
		return fmt.Errorf("existing candidate id %q does not match requested %q", record.ID, id)
	}
	if meta.Kind != "" && meta.Kind != string(kind) {
		return fmt.Errorf("existing candidate kind %q does not match requested kind %q", meta.Kind, kind)
	}
	return nil
}
