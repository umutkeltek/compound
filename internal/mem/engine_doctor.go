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
)

// EngineDoctorReport proves the rented breadth engine can serve recall without
// becoming a ledger owner. Engines may build private indexes; they may not mutate
// COMPOUND_HOME/ledger.
type EngineDoctorReport struct {
	Query            string             `json:"query"`
	Engine           EngineCapabilities `json:"engine"`
	LedgerGen        int64              `json:"ledger_gen"`
	LedgerHashBefore string             `json:"ledger_hash_before"`
	LedgerHashAfter  string             `json:"ledger_hash_after"`
	LedgerUnchanged  bool               `json:"ledger_unchanged"`
	Hits             []EngineDoctorHit  `json:"hits,omitempty"`
	Degraded         bool               `json:"degraded,omitempty"`
	Error            string             `json:"error,omitempty"`
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
	before, err := s.LedgerDigest()
	if err != nil {
		return EngineDoctorReport{}, err
	}
	report := EngineDoctorReport{
		Query:            query,
		LedgerGen:        s.currentGen(),
		LedgerHashBefore: before,
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
	after, derr := s.LedgerDigest()
	if derr != nil {
		return report, derr
	}
	report.LedgerHashAfter = after
	report.LedgerUnchanged = before == after
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
