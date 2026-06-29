// Package projection checks GeneratedViewObservation / ProjectionFreshness.
// A generated view can prove freshness or drift; it cannot promote truth.
package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	StatusFresh   = "fresh"
	StatusStale   = "stale"
	StatusDrifted = "drifted"
)

type Manifest struct {
	ProjectionID         string            `yaml:"projection_id" json:"projection_id"`
	ViewRef              string            `yaml:"view_ref" json:"view_ref"`
	SourceRefs           []string          `yaml:"source_refs" json:"source_refs"`
	SourceLedgerSeq      int64             `yaml:"source_ledger_seq" json:"source_ledger_seq"`
	BuiltAt              string            `yaml:"built_at" json:"built_at"`
	MaxAgeHours          int               `yaml:"max_age_hours" json:"max_age_hours"`
	Generator            string            `yaml:"generator" json:"generator"`
	CoverageVerdict      string            `yaml:"coverage_verdict" json:"coverage_verdict"`
	ExpectedOutputHash   string            `yaml:"expected_output_hash" json:"expected_output_hash"`
	ExpectedSourceHashes map[string]string `yaml:"expected_source_hashes" json:"expected_source_hashes"`
}

type SourceObservation struct {
	SourceRef    string `json:"source_ref"`
	CurrentHash  string `json:"current_hash"`
	ExpectedHash string `json:"expected_hash,omitempty"`
	Status       string `json:"status"`
}

type Freshness struct {
	ProjectionID    string              `json:"projection_id"`
	ViewRef         string              `json:"view_ref"`
	SourceLedgerSeq int64               `json:"source_ledger_seq"`
	BuiltAt         string              `json:"built_at"`
	Staleness       string              `json:"staleness"`
	CoverageVerdict string              `json:"coverage_verdict"`
	Status          string              `json:"status"`
	OutputHash      string              `json:"output_hash"`
	Sources         []SourceObservation `json:"sources"`
	Checks          []Check             `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func LoadManifest(path string) (Manifest, string, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, "", fmt.Errorf("read projection manifest %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, "", fmt.Errorf("parse projection manifest %s: %w", path, err)
	}
	return m, filepath.Dir(path), nil
}

func DoctorFile(path string, now time.Time) (Freshness, error) {
	m, baseDir, err := LoadManifest(path)
	if err != nil {
		return Freshness{}, err
	}
	return Doctor(m, baseDir, now)
}

func Doctor(m Manifest, baseDir string, now time.Time) (Freshness, error) {
	f := Freshness{
		ProjectionID:    m.ProjectionID,
		ViewRef:         m.ViewRef,
		SourceLedgerSeq: m.SourceLedgerSeq,
		BuiltAt:         m.BuiltAt,
		CoverageVerdict: m.CoverageVerdict,
		Status:          StatusFresh,
	}
	add := func(name string, ok bool, detail string) {
		status := "ok"
		if !ok {
			status = "fail"
			if f.Status == StatusFresh {
				f.Status = StatusDrifted
			}
		}
		f.Checks = append(f.Checks, Check{Name: name, Status: status, Detail: detail})
	}

	add("identity", strings.TrimSpace(m.ProjectionID) != "" && strings.TrimSpace(m.ViewRef) != "", m.ProjectionID+" "+m.ViewRef)
	add("source_ledger_seq", m.SourceLedgerSeq > 0, fmt.Sprintf("%d", m.SourceLedgerSeq))
	add("generator", strings.TrimSpace(m.Generator) != "", m.Generator)

	outputHash, err := hashFile(baseDir, m.ViewRef)
	if err != nil {
		return f, err
	}
	f.OutputHash = outputHash
	if m.ExpectedOutputHash != "" {
		add("output_hash", outputHash == m.ExpectedOutputHash, outputHash)
	}

	if len(m.SourceRefs) == 0 {
		add("sources", false, "no source refs")
	}
	for _, ref := range m.SourceRefs {
		current, err := hashFile(baseDir, ref)
		if err != nil {
			return f, err
		}
		expected := ""
		status := "ok"
		if m.ExpectedSourceHashes != nil {
			expected = m.ExpectedSourceHashes[ref]
			if expected != "" && expected != current {
				status = "fail"
				f.Status = StatusDrifted
			}
		}
		f.Sources = append(f.Sources, SourceObservation{SourceRef: ref, CurrentHash: current, ExpectedHash: expected, Status: status})
	}
	add("source_hashes", sourceHashStatus(f.Sources), fmt.Sprintf("%d source(s)", len(f.Sources)))

	coverageOK := strings.EqualFold(m.CoverageVerdict, "complete") || strings.EqualFold(m.CoverageVerdict, "covered")
	if !coverageOK && f.Status == StatusFresh {
		f.Status = StatusStale
	}
	f.Checks = append(f.Checks, Check{Name: "coverage", Status: boolStatus(coverageOK), Detail: m.CoverageVerdict})

	if m.BuiltAt != "" && m.MaxAgeHours > 0 {
		built, err := time.Parse(time.RFC3339, m.BuiltAt)
		if err != nil {
			return f, fmt.Errorf("parse built_at %q: %w", m.BuiltAt, err)
		}
		age := now.Sub(built)
		if age < 0 {
			age = 0
		}
		f.Staleness = age.Round(time.Second).String()
		if age > time.Duration(m.MaxAgeHours)*time.Hour && f.Status == StatusFresh {
			f.Status = StatusStale
		}
		f.Checks = append(f.Checks, Check{Name: "age", Status: boolStatus(age <= time.Duration(m.MaxAgeHours)*time.Hour), Detail: f.Staleness})
	}

	return f, nil
}

func hashFile(baseDir, ref string) (string, error) {
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, ref)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", ref, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sourceHashStatus(sources []SourceObservation) bool {
	if len(sources) == 0 {
		return false
	}
	for _, s := range sources {
		if s.Status != "ok" {
			return false
		}
	}
	return true
}

func boolStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}
