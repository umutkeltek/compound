package mem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// EngineDoctorReport proves the rented breadth engine can serve recall without
// becoming a ledger owner. Engines may build private indexes; they may not mutate
// COMPOUND_HOME/ledger.
type EngineDoctorReport struct {
	Query               string             `json:"query"`
	Engine              EngineCapabilities `json:"engine"`
	LedgerGen           int64              `json:"ledger_gen"`
	LedgerGenAfter      int64              `json:"ledger_gen_after"`
	LedgerHashBefore    string             `json:"ledger_hash_before"`
	LedgerHashAfter     string             `json:"ledger_hash_after"`
	LedgerUnchanged     bool               `json:"ledger_unchanged"`
	Hits                []EngineDoctorHit  `json:"hits,omitempty"`
	Degraded            bool               `json:"degraded,omitempty"`
	Error               string             `json:"error,omitempty"`
	RetryCount          int                `json:"retry_count,omitempty"`
	OwnerLedgerAdvanced bool               `json:"owner_ledger_advanced,omitempty"`
	AdvancedRows        int                `json:"advanced_rows,omitempty"`     // governed rows OTHER compound processes appended during the probe
	Note                string             `json:"note,omitempty"`              // INFO surfaced when a benign concurrent-writer retry proved the ledger
	EngineViolations    []string           `json:"engine_violations,omitempty"` // ledger paths/rows attributable to the rented engine (hard invariant failure)
}

type EngineDoctorHit struct {
	ClaimID   string `json:"claim_id"`
	Entity    string `json:"entity"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Seq       int64  `json:"seq"`
}

// ErrLedgerBusy means ownership attribution succeeded for every observed delta,
// but the ledger advanced across both bounded attempts so current synchronization
// could not be certified. It is not an engine-mutation verdict.
var ErrLedgerBusy = errors.New("owned ledger remained active during engine doctor")

var errLedgerChangedDuringDoctor = errors.New("owned ledger changed during engine doctor")

// EngineDoctor runs one breadth query through the supplied rented engine and
// verifies either a byte-stable ledger or that every observed delta is a governed
// owner append. Missing or unavailable breadth degrades explicitly;
// identity/protocol errors and engine-attributed writes remain hard failures.
//
// The owned ledger is live: OTHER compound processes (a governed close, a writeback
// gate, a product signal) legitimately append rows while the doctor's query runs, so a
// whole-directory byte digest moving is NOT proof the RENTED engine mutated the ledger.
// When the digest moves, EngineDoctor attributes the delta: a legitimate concurrent write
// is append-only governed rows carrying producer provenance (plus a counter bump); an
// engine mutation is not (a private index file dropped in the ledger, a non-append
// rewrite, or a row without provenance). A purely-legit delta is retried ONCE against the
// fresh generation. A second legitimate delta returns ErrLedgerBusy: ownership was
// preserved, but current synchronization was not certified. An engine-attributed delta
// stays red and names the offending path.
func (s *Store) EngineDoctor(query string, k int, eng Engine) (EngineDoctorReport, error) {
	if strings.TrimSpace(query) == "" {
		return EngineDoctorReport{}, fmt.Errorf("query required")
	}
	if k <= 0 {
		k = 10
	}
	report, before, err := s.engineDoctorAttempt(query, k, eng)
	if err == nil || report.LedgerUnchanged {
		return report, err
	}
	if !errors.Is(err, errLedgerChangedDuringDoctor) {
		// Snapshot and query errors are not ownership evidence. Preserve the real
		// failure instead of classifying a zero/partial snapshot as engine mutation.
		return report, err
	}
	if report.Error != "" && !report.Degraded {
		// A hard engine query error, not a ledger-ownership question — surface it as-is.
		return report, err
	}
	// The owned ledger digest moved while the rented engine ran. Attribute the delta:
	// only an engine-attributed mutation keeps the invariant red.
	delta := s.classifyLedgerDeltaSince(before)
	if !delta.legit() {
		report.EngineViolations = delta.violations
		return report, fmt.Errorf("rented engine mutated owned ledger: %s", strings.Join(delta.violations, "; "))
	}
	// Benign concurrent governed writers advanced the ledger under the doctor. Retry the
	// hop ONCE against the fresh generation to observe a quiet window.
	retry, retryBefore, retryErr := s.engineDoctorAttempt(query, k, eng)
	retry.RetryCount = 1
	retry.OwnerLedgerAdvanced = true
	retry.AdvancedRows = delta.rows
	if retryErr == nil && retry.LedgerUnchanged {
		retry.Note = fmt.Sprintf("ledger advanced %d row(s) during doctor (concurrent legit writers); verified against fresh gen", delta.rows)
		return retry, nil
	}
	if retryErr != nil && !errors.Is(retryErr, errLedgerChangedDuringDoctor) {
		// Never turn a failed retry snapshot into a benign hot-ledger result. The
		// caller needs the original error to know the proof did not run.
		return retry, retryErr
	}
	if retry.LedgerUnchanged {
		// A genuine (non-ledger) error surfaced on the retry.
		return retry, retryErr
	}
	if retry.Error != "" && !retry.Degraded {
		return retry, retryErr
	}
	// The ledger moved again during the stable retry. An engine write is still red;
	// otherwise the ledger is simply too hot to prove a quiet window this run.
	retryDelta := s.classifyLedgerDeltaSince(retryBefore)
	if !retryDelta.legit() {
		retry.EngineViolations = retryDelta.violations
		return retry, fmt.Errorf("rented engine mutated owned ledger: %s", strings.Join(retryDelta.violations, "; "))
	}
	retry.AdvancedRows = delta.rows + retryDelta.rows
	retry.Note = fmt.Sprintf("ledger remained active across both doctor attempts (%d concurrent legit row(s)); ownership preserved by governed append attribution; current synchronization not certified", retry.AdvancedRows)
	return retry, fmt.Errorf("%w: %d concurrent legit row(s); current synchronization not certified", ErrLedgerBusy, retry.AdvancedRows)
}

func (s *Store) engineDoctorAttempt(query string, k int, eng Engine) (EngineDoctorReport, ledgerDoctorSnapshot, error) {
	before, err := s.ledgerDoctorSnapshot()
	if err != nil {
		return EngineDoctorReport{}, ledgerDoctorSnapshot{}, err
	}
	report := EngineDoctorReport{
		Query:            query,
		LedgerGen:        before.gen,
		LedgerHashBefore: before.digest,
	}
	claims, qerr := s.QueryBreadth(query, k, eng)
	report.Engine = eng.Capabilities()
	for _, c := range claims {
		report.Hits = append(report.Hits, EngineDoctorHit{
			ClaimID:   c.ID,
			Entity:    c.Entity,
			Predicate: c.Predicate,
			Object:    c.Object,
			Seq:       c.Seq,
		})
	}
	after, derr := s.ledgerDoctorSnapshot()
	if derr != nil {
		return report, before, derr
	}
	report.LedgerGenAfter = after.gen
	report.LedgerHashAfter = after.digest
	report.LedgerUnchanged = before.digest == after.digest
	if qerr != nil {
		report.Error = qerr.Error()
		if errors.Is(qerr, ErrUnsupported) || errors.Is(qerr, ErrEngineUnavailable) {
			report.Degraded = true
		} else {
			return report, before, qerr
		}
	}
	if !report.LedgerUnchanged {
		// EngineDoctor attributes this delta (concurrent legit writers vs. an engine
		// mutation). It is a hard failure only when the delta is engine-attributed.
		return report, before, errLedgerChangedDuringDoctor
	}
	return report, before, nil
}

// ledgerFileMeta fingerprints one ledger file so a later read can tell an append-only
// governed growth from a non-append rewrite without holding the whole ledger in memory.
type ledgerFileMeta struct {
	size int64
	hash string // sha256 hex of the file's bytes
}

type ledgerDoctorSnapshot struct {
	gen    int64
	digest string
	files  map[string]ledgerFileMeta // relpath -> fingerprint, captured under the same shared lock as digest
}

// ledgerDelta is the attribution verdict for a ledger that moved during a probe.
type ledgerDelta struct {
	rows       int      // governed rows appended by legitimate producers
	violations []string // engine-attributed changes (empty == purely concurrent-legit)
}

func (d ledgerDelta) legit() bool { return len(d.violations) == 0 }

// classifyLedgerDeltaSince attributes every change to the owned ledger since `before`.
// A legitimate concurrent write is append-only governed rows (each carrying producer
// provenance) plus the monotonic seq counter; anything else — a new non-log file, a
// non-append rewrite, a removed governed file, or a row without provenance — is
// engine-attributed and named. It re-reads the live ledger, which is race-tolerant:
// legit appends stay legit (the before-prefix is unchanged) and an engine artifact
// persists.
func (s *Store) classifyLedgerDeltaSince(before ledgerDoctorSnapshot) ledgerDelta {
	root := filepath.Join(s.home, "ledger")
	var delta ledgerDelta
	seen := make(map[string]bool, len(before.files))
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		seen[rel] = true
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		prev, existed := before.files[rel]
		cur := sha256.Sum256(b)
		if existed && hex.EncodeToString(cur[:]) == prev.hash {
			return nil // unchanged since the pre-query snapshot
		}
		classifyChangedLedgerFile(rel, b, prev, existed, &delta)
		return nil
	})
	if err != nil {
		delta.violations = append(delta.violations, "cannot inspect ledger delta: "+err.Error())
		return delta
	}
	for rel := range before.files {
		if seen[rel] {
			continue
		}
		if filepath.Base(rel) == "seq.tmp" {
			continue // transient counter temp consumed by rename
		}
		delta.violations = append(delta.violations, "governed ledger file "+rel+" removed during probe (engine-attributed)")
	}
	return delta
}

// classifyChangedLedgerFile attributes one changed/new ledger file to a legitimate
// producer or to the rented engine.
func classifyChangedLedgerFile(rel string, b []byte, prev ledgerFileMeta, existed bool, delta *ledgerDelta) {
	base := filepath.Base(rel)
	switch {
	case base == "seq" || base == "seq.tmp":
		// nextSeq rewrites the counter atomically (temp+rename); legit if it stays integer.
		if t := strings.TrimSpace(string(b)); t != "" {
			if _, err := strconv.ParseInt(t, 10, 64); err != nil {
				delta.violations = append(delta.violations, "ledger counter "+rel+" holds non-integer bytes (engine-attributed)")
			}
		}
	case strings.HasSuffix(base, ".jsonl"):
		var appended []byte
		if existed {
			if int64(len(b)) >= prev.size && sha256Hex(b[:prev.size]) == prev.hash {
				appended = b[prev.size:] // pure append: the pre-query prefix is byte-identical
			} else {
				delta.violations = append(delta.violations, "governed log "+rel+" rewritten non-append during probe (bypassed the append-only door; engine-attributed)")
				return
			}
		} else {
			appended = b // a producer created a new governed log
		}
		classifyAppendedRows(rel, base, appended, delta)
	default:
		if existed {
			delta.violations = append(delta.violations, "non-log ledger file "+rel+" mutated during probe (engine-attributed)")
		} else {
			delta.violations = append(delta.violations, "new non-log file "+rel+" written under owned ledger (rented engine index must stay outside the ledger; engine-attributed)")
		}
	}
}

// classifyAppendedRows verifies each appended governed row parses and, for the claims log,
// carries producer provenance. A trailing fragment with no newline is an in-flight append
// by a concurrent writer and is skipped, not blamed.
func classifyAppendedRows(rel, base string, appended []byte, delta *ledgerDelta) {
	text := string(appended)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && !strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1] // drop an in-flight trailing fragment
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe json.RawMessage
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			delta.violations = append(delta.violations, "unparseable row appended to "+rel+" (not a governed record; engine-attributed)")
			continue
		}
		if base == "claims.jsonl" {
			var c Claim
			if err := json.Unmarshal([]byte(line), &c); err != nil || strings.TrimSpace(c.Derivation) == "" {
				id := ""
				if c.ID != "" {
					id = " " + c.ID
				}
				delta.violations = append(delta.violations, "claim row"+id+" appended to claims.jsonl without producer provenance (engine-attributed)")
				continue
			}
		}
		delta.rows++
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ledgerDoctorSnapshot pairs generation and bytes under the ledger owner's
// shared lock. The rented query runs outside this lock; only the two observations
// are stabilized so a real owner writer cannot advance between gen and digest.
func (s *Store) ledgerDoctorSnapshot() (ledgerDoctorSnapshot, error) {
	if s.ledgerDoctorSnapshotter != nil {
		return s.ledgerDoctorSnapshotter()
	}
	return s.ledgerDoctorSnapshotFromDisk()
}

func (s *Store) ledgerDoctorSnapshotFromDisk() (ledgerDoctorSnapshot, error) {
	lockPath := filepath.Join(s.home, "lock", "mem.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return ledgerDoctorSnapshot{}, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return ledgerDoctorSnapshot{}, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return ledgerDoctorSnapshot{}, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	digest, files, err := s.ledgerDigestAndInventory()
	if err != nil {
		return ledgerDoctorSnapshot{}, err
	}
	return ledgerDoctorSnapshot{gen: s.currentGen(), digest: digest, files: files}, nil
}

// LedgerDigest hashes every file under COMPOUND_HOME/ledger with stable relative
// paths. It is intentionally about owned ledger bytes, not rented engine caches.
func (s *Store) LedgerDigest() (string, error) {
	digest, _, err := s.ledgerDigestAndInventory()
	return digest, err
}

// ledgerDigestAndInventory computes the ledger digest and a per-file fingerprint map in
// one walk. The digest folding is identical to LedgerDigest's original algorithm, so the
// returned digest stays byte-for-byte compatible with existing before/after comparisons.
func (s *Store) ledgerDigestAndInventory() (string, map[string]ledgerFileMeta, error) {
	root := filepath.Join(s.home, "ledger")
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return "", map[string]ledgerFileMeta{}, nil
		}
		return "", nil, err
	}
	sort.Strings(files)
	inv := make(map[string]ledgerFileMeta, len(files))
	h := sha256.New()
	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", nil, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		inv[rel] = ledgerFileMeta{size: int64(len(b)), hash: sha256Hex(b)}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), inv, nil
}
