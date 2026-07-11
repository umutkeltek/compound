package mem

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
}

type EngineDoctorHit struct {
	ClaimID   string `json:"claim_id"`
	Entity    string `json:"entity"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Seq       int64  `json:"seq"`
}

// EngineDoctor runs one breadth query through the supplied rented engine and
// verifies the owned ledger is byte-identical before and after the query. Missing
// or unavailable breadth degrades explicitly; identity/protocol errors remain
// hard failures.
func (s *Store) EngineDoctor(query string, k int, eng Engine) (EngineDoctorReport, error) {
	if strings.TrimSpace(query) == "" {
		return EngineDoctorReport{}, fmt.Errorf("query required")
	}
	if k <= 0 {
		k = 10
	}
	report, err := s.engineDoctorAttempt(query, k, eng)
	if err == nil || report.LedgerUnchanged {
		return report, err
	}
	afterGen := report.LedgerGenAfter
	if afterGen > report.LedgerGen && report.Engine.CorpusGen == afterGen {
		retry, retryErr := s.engineDoctorAttempt(query, k, eng)
		retry.RetryCount = 1
		retry.OwnerLedgerAdvanced = true
		return retry, retryErr
	}
	return report, err
}

func (s *Store) engineDoctorAttempt(query string, k int, eng Engine) (EngineDoctorReport, error) {
	before, err := s.ledgerDoctorSnapshot()
	if err != nil {
		return EngineDoctorReport{}, err
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
		return report, derr
	}
	report.LedgerGenAfter = after.gen
	report.LedgerHashAfter = after.digest
	report.LedgerUnchanged = before.digest == after.digest
	if qerr != nil {
		report.Error = qerr.Error()
		if errors.Is(qerr, ErrUnsupported) || errors.Is(qerr, ErrEngineUnavailable) {
			report.Degraded = true
		} else {
			return report, qerr
		}
	}
	if !report.LedgerUnchanged {
		return report, fmt.Errorf("rented engine mutated owned ledger")
	}
	return report, nil
}

type ledgerDoctorSnapshot struct {
	gen    int64
	digest string
}

// ledgerDoctorSnapshot pairs generation and bytes under the ledger owner's
// shared lock. The rented query runs outside this lock; only the two observations
// are stabilized so a real owner writer cannot advance between gen and digest.
func (s *Store) ledgerDoctorSnapshot() (ledgerDoctorSnapshot, error) {
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
	digest, err := s.LedgerDigest()
	if err != nil {
		return ledgerDoctorSnapshot{}, err
	}
	return ledgerDoctorSnapshot{gen: s.currentGen(), digest: digest}, nil
}

// LedgerDigest hashes every file under COMPOUND_HOME/ledger with stable relative
// paths. It is intentionally about owned ledger bytes, not rented engine caches.
func (s *Store) LedgerDigest() (string, error) {
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
			return "", nil
		}
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
