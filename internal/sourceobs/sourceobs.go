// Package sourceobs imports SourceObservation records from existing systems.
//
// Source observations are evidence/candidate inputs, not authority. They let
// Compound dogfood generated views, registries, dashboards, and source docs
// without making those files a parallel truth owner.
package sourceobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/projection"
	"compound/internal/tooltopology"
	"compound/internal/writeback"
	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion          = "compound.source_observation.v0.1"
	CandidateSchemaVersion = "compound.source_candidate.v0.1"
)

type SourceObservation struct {
	SchemaVersion string        `json:"schema_version" yaml:"schema_version"`
	ID            string        `json:"id" yaml:"id"`
	AdapterID     string        `json:"adapter_id" yaml:"adapter_id"`
	SourceSystem  string        `json:"source_system" yaml:"source_system"`
	ObservedAt    string        `json:"observed_at" yaml:"observed_at"`
	SourceRef     SourceRef     `json:"source_ref" yaml:"source_ref"`
	GeneratedView GeneratedView `json:"generated_view,omitempty" yaml:"generated_view,omitempty"`
	MapsTo        []MapTarget   `json:"maps_to" yaml:"maps_to"`
	EvidenceRefs  []string      `json:"evidence_refs" yaml:"evidence_refs"`
}

type SourceRef struct {
	Path        string `json:"path" yaml:"path"`
	Kind        string `json:"kind" yaml:"kind"`
	Owner       string `json:"owner" yaml:"owner"`
	SourceClass string `json:"source_class" yaml:"source_class"`
	Revision    string `json:"revision" yaml:"revision"`
}

type GeneratedView struct {
	ViewKind      string      `json:"view_kind" yaml:"view_kind"`
	Consumer      string      `json:"consumer" yaml:"consumer"`
	GeneratedFrom []SourceRef `json:"generated_from,omitempty" yaml:"generated_from,omitempty"`
	Generator     string      `json:"generator" yaml:"generator"`
	Marker        string      `json:"marker" yaml:"marker"`
	Freshness     string      `json:"freshness" yaml:"freshness"`
	DriftReason   string      `json:"drift_reason,omitempty" yaml:"drift_reason,omitempty"`
}

type MapTarget struct {
	ObjectFamily   string `json:"object_family" yaml:"object_family"`
	CandidateKind  string `json:"candidate_kind" yaml:"candidate_kind"`
	EntityHint     string `json:"entity_hint" yaml:"entity_hint"`
	PromoteAllowed bool   `json:"promote_allowed" yaml:"promote_allowed"`
}

type CandidateRecord struct {
	SchemaVersion       string   `json:"schema_version"`
	ID                  string   `json:"id"`
	Kind                string   `json:"kind"`
	Subject             string   `json:"subject"`
	Statement           string   `json:"statement"`
	Detail              string   `json:"detail,omitempty"`
	Confidence          float64  `json:"confidence"`
	EvidenceRefs        []string `json:"evidence_refs"`
	SourceObservationID string   `json:"source_observation_id"`
	SourceRef           string   `json:"source_ref"`
	PromoteAllowed      bool     `json:"promote_allowed"`
	Verdict             string   `json:"verdict"`
	CreatedAt           string   `json:"created_at"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	ObservationID string   `json:"observation_id"`
	Status        string   `json:"status"`
	Checks        []Check  `json:"checks"`
	CandidateIDs  []string `json:"candidate_ids"`
	NoPromoteIDs  []string `json:"no_promote_ids,omitempty"`
}

func Load(path string) (SourceObservation, error) {
	var obs SourceObservation
	data, err := os.ReadFile(path)
	if err != nil {
		return obs, fmt.Errorf("read source observation %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &obs); err != nil {
		return obs, fmt.Errorf("parse source observation %s: %w", path, err)
	}
	return obs, nil
}

func Validate(obs SourceObservation) Report {
	r := Report{ObservationID: obs.ID, Status: "ok"}
	add := func(name string, ok bool, detail string) {
		status := "ok"
		if !ok {
			status = "fail"
			r.Status = "fail"
		}
		r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
	}
	add("schema", obs.SchemaVersion == SchemaVersion, obs.SchemaVersion)
	add("identity", strings.TrimSpace(obs.ID) != "" && strings.TrimSpace(obs.AdapterID) != "" && strings.TrimSpace(obs.SourceSystem) != "", obs.ID)
	add("source_ref", strings.TrimSpace(obs.SourceRef.Path) != "" && strings.TrimSpace(obs.SourceRef.Kind) != "" && strings.TrimSpace(obs.SourceRef.Owner) != "" && strings.TrimSpace(obs.SourceRef.SourceClass) != "" && strings.TrimSpace(obs.SourceRef.Revision) != "", obs.SourceRef.Path)
	add("evidence", len(obs.EvidenceRefs) > 0, strings.Join(obs.EvidenceRefs, ","))
	add("maps", len(obs.MapsTo) > 0, fmt.Sprintf("%d", len(obs.MapsTo)))
	for _, m := range obs.MapsTo {
		_, known := candidates.SpecFor(candidates.Kind(m.CandidateKind))
		add("candidate_kind:"+m.CandidateKind, known, m.EntityHint)
		add("map_target:"+m.EntityHint, strings.TrimSpace(m.ObjectFamily) != "" && strings.TrimSpace(m.EntityHint) != "", m.ObjectFamily)
	}
	if obs.SourceRef.Kind == "generated_view" || obs.GeneratedView.ViewKind != "" {
		add("generated_view", strings.TrimSpace(obs.GeneratedView.ViewKind) != "" && strings.TrimSpace(obs.GeneratedView.Consumer) != "" && strings.TrimSpace(obs.GeneratedView.Generator) != "" && strings.TrimSpace(obs.GeneratedView.Marker) != "" && strings.TrimSpace(obs.GeneratedView.Freshness) != "", obs.GeneratedView.ViewKind)
	}
	return r
}

func Apply(homePath string, obs SourceObservation, now time.Time) (Report, error) {
	report := Validate(obs)
	if report.Status != "ok" {
		return report, fmt.Errorf("source observation invalid")
	}
	wb, err := writeback.Open(homePath)
	if err != nil {
		return report, err
	}
	for _, m := range obs.MapsTo {
		c := CandidateFromMap(obs, m, now)
		if err := WriteCandidate(homePath, c); err != nil {
			return report, err
		}
		report.CandidateIDs = append(report.CandidateIDs, c.ID)
		if !m.PromoteAllowed {
			nop, _, err := wb.RecordNoPromote(NoPromoteDecision(obs, c, now), now)
			if err != nil {
				return report, err
			}
			report.NoPromoteIDs = append(report.NoPromoteIDs, nop.ID)
		}
	}
	return report, nil
}

// FromProjectionFreshness converts a generated-view freshness report into the
// generic source observation contract. Projection drift stays candidate/no-promote
// evidence; this adapter never promotes generated-view truth.
func FromProjectionFreshness(f projection.Freshness, now time.Time) SourceObservation {
	revision := f.OutputHash
	if revision == "" {
		revision = "status:" + f.Status
	}
	return SourceObservation{
		SchemaVersion: SchemaVersion,
		ID:            idFor("srcobs_", "projection", f.ProjectionID, f.ViewRef, f.Status, revision),
		AdapterID:     "compound.projection",
		SourceSystem:  "compound",
		ObservedAt:    rfc3339(now),
		SourceRef: SourceRef{
			Path:        f.ViewRef,
			Kind:        "generated_view",
			Owner:       f.ProjectionID,
			SourceClass: "generated",
			Revision:    revision,
		},
		GeneratedView: GeneratedView{
			ViewKind:      "projection_freshness",
			Consumer:      "compound projection doctor",
			GeneratedFrom: projectionSourceRefs(f),
			Generator:     "compound projection doctor",
			Marker:        "projection_freshness",
			Freshness:     f.Status,
			DriftReason:   projectionDriftReason(f),
		},
		MapsTo: []MapTarget{
			{
				ObjectFamily:   "projection",
				CandidateKind:  string(candidates.MethodUpgrade),
				EntityHint:     f.ProjectionID,
				PromoteAllowed: false,
			},
		},
		EvidenceRefs: projection.EvidenceRefs(f),
	}
}

// FromToolTopology converts a generated/imported tool topology source into the
// generic SourceObservation contract. The topology may update CapabilityGraph
// offers, but the source view itself stays no-promote evidence rather than
// promoted memory or source-owner truth.
func FromToolTopology(topo tooltopology.Topology, now time.Time) SourceObservation {
	revision := strings.TrimSpace(topo.SourceRef.Revision)
	if revision == "" {
		revision = "topology:" + topo.ID
	}
	evidenceRefs := append([]string{}, topo.EvidenceRefs...)
	if len(evidenceRefs) == 0 {
		evidenceRefs = []string{"tool_topology:" + revision}
	}
	return SourceObservation{
		SchemaVersion: SchemaVersion,
		ID:            idFor("srcobs_", "tool_topology", topo.ID, topo.SourceRef.Path, revision),
		AdapterID:     "compound.tool-topology",
		SourceSystem:  "compound",
		ObservedAt:    rfc3339(now),
		SourceRef: SourceRef{
			Path:        topo.SourceRef.Path,
			Kind:        topo.SourceRef.Kind,
			Owner:       firstNonEmpty(topo.SourceRef.Owner, topo.ID),
			SourceClass: topo.SourceRef.SourceClass,
			Revision:    revision,
		},
		GeneratedView: GeneratedView{
			ViewKind:    "tool_topology",
			Consumer:    "compound capability import-topology",
			Generator:   firstNonEmpty(topo.AdapterID, "compound tool topology adapter"),
			Marker:      tooltopology.SchemaVersion,
			Freshness:   "observed",
			DriftReason: fmt.Sprintf("entries=%d recommendations=%d", len(topo.Entries), len(topo.Recommendations)),
		},
		MapsTo: []MapTarget{
			{
				ObjectFamily:   "capability",
				CandidateKind:  string(candidates.CapabilityObs),
				EntityHint:     topo.ID,
				PromoteAllowed: false,
			},
		},
		EvidenceRefs: evidenceRefs,
	}
}

func CandidateFromMap(obs SourceObservation, m MapTarget, now time.Time) CandidateRecord {
	verdict := candidates.Park
	if !m.PromoteAllowed {
		verdict = candidates.NoPromote
	}
	subject := m.EntityHint
	statement := m.ObjectFamily + ":" + obs.SourceRef.Path
	detail := obs.GeneratedView.Freshness
	if detail == "" {
		detail = obs.SourceRef.SourceClass
	}
	return CandidateRecord{
		SchemaVersion:       CandidateSchemaVersion,
		ID:                  idFor("src_cand_", obs.ID, m.CandidateKind, m.EntityHint, obs.SourceRef.Revision),
		Kind:                m.CandidateKind,
		Subject:             subject,
		Statement:           statement,
		Detail:              detail,
		Confidence:          1.0,
		EvidenceRefs:        evidenceRefs(obs),
		SourceObservationID: obs.ID,
		SourceRef:           obs.SourceRef.Path,
		PromoteAllowed:      m.PromoteAllowed,
		Verdict:             verdict,
		CreatedAt:           rfc3339(now),
	}
}

func NoPromoteDecision(obs SourceObservation, c CandidateRecord, now time.Time) writeback.NoPromoteDecision {
	return writeback.NoPromoteDecision{
		SchemaVersion: writeback.NoPromoteSchemaVersion,
		CreatedAt:     rfc3339(now),
		CandidateRef:  c.ID,
		CandidateKind: c.Kind,
		OwnerTarget:   writeback.OwnerTarget{Kind: "source_observation", Ref: obs.ID},
		Basis:         "source observation map forbids promotion",
		EvidenceRefs:  evidenceRefs(obs),
		Status:        writeback.NoPromoteActive,
	}
}

func WriteCandidate(homePath string, c CandidateRecord) error {
	if _, ok := candidates.SpecFor(candidates.Kind(c.Kind)); !ok {
		return fmt.Errorf("candidate kind %q unknown", c.Kind)
	}
	rel := candidates.InboxDir(candidates.Kind(c.Kind))
	if rel == "" {
		return fmt.Errorf("candidate kind %q has no inbox dir", c.Kind)
	}
	dir := filepath.Join(homePath, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, c.ID+".json"), append(data, '\n'), 0o644)
}

func evidenceRefs(obs SourceObservation) []string {
	refs := append([]string(nil), obs.EvidenceRefs...)
	refs = append(refs, "source:"+obs.SourceRef.Path+"#"+obs.SourceRef.Revision)
	return refs
}

func projectionSourceRefs(f projection.Freshness) []SourceRef {
	refs := make([]SourceRef, 0, len(f.Sources))
	for _, source := range f.Sources {
		revision := source.CurrentHash
		if revision == "" {
			revision = "unknown"
		}
		refs = append(refs, SourceRef{
			Path:        source.SourceRef,
			Kind:        "source",
			Owner:       f.ProjectionID,
			SourceClass: "projection_source",
			Revision:    revision,
		})
	}
	return refs
}

func projectionDriftReason(f projection.Freshness) string {
	var reasons []string
	if f.Status != "" {
		reasons = append(reasons, "status="+f.Status)
	}
	for _, check := range f.Checks {
		if check.Status != "ok" {
			reason := check.Name
			if check.Detail != "" {
				reason += ":" + check.Detail
			}
			reasons = append(reasons, reason)
		}
	}
	for _, source := range f.Sources {
		if source.Status != "ok" {
			reasons = append(reasons, "source:"+source.SourceRef+"="+source.Status)
		}
	}
	return strings.Join(reasons, "; ")
}

func idFor(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(sum[:])[:12]
}

func rfc3339(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
