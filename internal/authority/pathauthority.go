package authority

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/candidates"
	"compound/internal/mem"

	"gopkg.in/yaml.v3"
)

const PathAuthoritySchemaVersion = "compound.path_authority.v0.1"

type PathAuthority struct {
	SchemaVersion       string          `json:"schema_version" yaml:"schema_version"`
	EntityRef           string          `json:"entity_ref" yaml:"entity_ref"`
	EntityKind          string          `json:"entity_kind" yaml:"entity_kind"`
	Aliases             []string        `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	AuthoritativeSource string          `json:"authoritative_source" yaml:"authoritative_source"`
	PrecedenceRank      int             `json:"precedence_rank,omitempty" yaml:"precedence_rank,omitempty"`
	SourceBindings      []SourceBinding `json:"source_bindings,omitempty" yaml:"source_bindings,omitempty"`
	PathBindings        []PathBinding   `json:"path_bindings" yaml:"path_bindings"`
	HostBindings        []HostBinding   `json:"host_bindings,omitempty" yaml:"host_bindings,omitempty"`
	EvidenceRefs        []string        `json:"evidence_refs" yaml:"evidence_refs"`
}

type SourceBinding struct {
	Kind  string `json:"kind" yaml:"kind"`
	Value string `json:"value" yaml:"value"`
}

type PathBinding struct {
	Path   string `json:"path" yaml:"path"`
	Host   string `json:"host,omitempty" yaml:"host,omitempty"`
	Status string `json:"status" yaml:"status"` // active | stale | retired
}

type HostBinding struct {
	Host   string `json:"host" yaml:"host"`
	Status string `json:"status" yaml:"status"` // active | stale | retired
}

type PathAuthorityReport struct {
	EntityRef      string   `json:"entity_ref"`
	Status         string   `json:"status"`
	Identifiers    []string `json:"identifiers"`
	CandidateCount int      `json:"candidate_count"`
}

func ImportPathAuthority(path string, existing []mem.Entity) (PathAuthority, mem.Entity, []mem.Candidate, PathAuthorityReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PathAuthority{}, mem.Entity{}, nil, PathAuthorityReport{}, err
	}
	var pa PathAuthority
	if err := yaml.Unmarshal(data, &pa); err != nil {
		return PathAuthority{}, mem.Entity{}, nil, PathAuthorityReport{}, err
	}
	if err := validatePathAuthority(pa); err != nil {
		return pa, mem.Entity{}, nil, PathAuthorityReport{EntityRef: pa.EntityRef, Status: "fail"}, err
	}

	entity := mergePathAuthorityEntity(pa, existing)
	src := "path-authority:" + filepath.Base(path)
	var facts []mem.Candidate
	add := func(pred, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		facts = append(facts, mem.Candidate{
			Entity:         pa.EntityRef,
			Predicate:      pred,
			Object:         val,
			Confidence:     1.0,
			Derivation:     "deterministic",
			ClaimType:      "status",
			Qualifiers:     map[string]string{"domain": pred},
			PromotionBasis: candidates.BasisTrustedDeterministic,
			SourceTraceRef: src,
			SnippetHash:    src + ":" + pa.EntityRef + ":" + pred + ":" + val,
			Visibility:     "operational",
		})
	}
	add("authoritative_source", pa.AuthoritativeSource)
	for _, sb := range pa.SourceBindings {
		add("source_"+sb.Kind, sb.Value)
	}
	for _, pb := range pa.PathBindings {
		if pb.Status == "active" {
			add("current_path", pb.Path)
		} else {
			add("known_path_"+pb.Status, pb.Path)
		}
	}
	for _, hb := range pa.HostBindings {
		if hb.Status == "active" {
			add("current_host", hb.Host)
		} else {
			add("known_host_"+hb.Status, hb.Host)
		}
	}

	report := PathAuthorityReport{
		EntityRef:      pa.EntityRef,
		Status:         "ok",
		Identifiers:    entity.Identifiers,
		CandidateCount: len(facts),
	}
	return pa, entity, facts, report, nil
}

func validatePathAuthority(pa PathAuthority) error {
	if pa.SchemaVersion != PathAuthoritySchemaVersion {
		return fmt.Errorf("schema_version must be %s, got %q", PathAuthoritySchemaVersion, pa.SchemaVersion)
	}
	if strings.TrimSpace(pa.EntityRef) == "" {
		return fmt.Errorf("entity_ref required")
	}
	if strings.TrimSpace(pa.EntityKind) == "" {
		return fmt.Errorf("entity_kind required")
	}
	if strings.TrimSpace(pa.AuthoritativeSource) == "" {
		return fmt.Errorf("authoritative_source required")
	}
	if len(pa.PathBindings) == 0 && len(pa.SourceBindings) == 0 {
		return fmt.Errorf("at least one path_binding or source_binding required")
	}
	if len(pa.EvidenceRefs) == 0 {
		return fmt.Errorf("evidence_refs required")
	}
	for _, pb := range pa.PathBindings {
		if strings.TrimSpace(pb.Path) == "" {
			return fmt.Errorf("path_binding path required")
		}
		if !oneOf(pb.Status, []string{"active", "stale", "retired"}) {
			return fmt.Errorf("path_binding %q bad status %q", pb.Path, pb.Status)
		}
	}
	for _, sb := range pa.SourceBindings {
		if strings.TrimSpace(sb.Kind) == "" || strings.TrimSpace(sb.Value) == "" {
			return fmt.Errorf("source_binding kind and value required")
		}
	}
	return nil
}

func mergePathAuthorityEntity(pa PathAuthority, existing []mem.Entity) mem.Entity {
	e := mem.Entity{
		Slug:       pa.EntityRef,
		Kind:       pa.EntityKind,
		Visibility: "operational",
	}
	for _, ex := range existing {
		if ex.Slug == pa.EntityRef {
			e = ex
			e.Kind = pa.EntityKind
			if e.Visibility == "" {
				e.Visibility = "operational"
			}
			break
		}
	}
	e.Aliases = mergeStrings(e.Aliases, pa.Aliases...)
	var ids []string
	for _, sb := range pa.SourceBindings {
		ids = append(ids, sb.Value)
		ids = append(ids, sb.Kind+":"+sb.Value)
	}
	for _, pb := range pa.PathBindings {
		ids = append(ids, pb.Path)
	}
	for _, hb := range pa.HostBindings {
		ids = append(ids, "host:"+hb.Host)
	}
	e.Identifiers = mergeStrings(e.Identifiers, ids...)
	return e
}

func mergeStrings(values []string, more ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range append(values, more...) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
