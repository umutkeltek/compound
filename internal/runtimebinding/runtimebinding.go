// Package runtimebinding models where a service actually runs and how to check it.
// It is the first RuntimeBinding/ServiceContract slice: public contract + fixture
// doctor now, real infra-os adapters later.
package runtimebinding

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	StatusCandidate = "candidate"
	StatusActive    = "active"
	StatusStale     = "stale"
	StatusRetired   = "retired"
)

var statuses = []string{StatusCandidate, StatusActive, StatusStale, StatusRetired}

type Bundle struct {
	RuntimeBinding  RuntimeBinding  `json:"runtime_binding" yaml:"runtime_binding"`
	ServiceContract ServiceContract `json:"service_contract" yaml:"service_contract"`
}

type RuntimeBinding struct {
	ServiceID         string   `json:"service_id" yaml:"service_id"`
	OwnerEntity       string   `json:"owner_entity" yaml:"owner_entity"`
	HostBinding       string   `json:"host_binding" yaml:"host_binding"`
	DeploymentBinding string   `json:"deployment_binding" yaml:"deployment_binding"`
	DataBindings      []string `json:"data_bindings" yaml:"data_bindings"`
	CredentialRefs    []string `json:"credential_refs,omitempty" yaml:"credential_refs,omitempty"`
	HealthCheckRefs   []string `json:"health_check_refs" yaml:"health_check_refs"`
	RiskTier          string   `json:"risk_tier" yaml:"risk_tier"`
	EvidenceRefs      []string `json:"evidence_refs" yaml:"evidence_refs"`
	Status            string   `json:"status" yaml:"status"`
}

type ServiceContract struct {
	ServiceID     string   `json:"service_id" yaml:"service_id"`
	InterfaceRefs []string `json:"interface_refs" yaml:"interface_refs"`
	Dependencies  []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	OwnerEntity   string   `json:"owner_entity" yaml:"owner_entity"`
	RiskTier      string   `json:"risk_tier" yaml:"risk_tier"`
	EvidenceRefs  []string `json:"evidence_refs" yaml:"evidence_refs"`
	Status        string   `json:"status" yaml:"status"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DoctorReport struct {
	ServiceID string  `json:"service_id"`
	Owner     string  `json:"owner"`
	Status    string  `json:"status"`
	Checks    []Check `json:"checks"`
}

func Load(path string) (Bundle, error) {
	var b Bundle
	data, err := os.ReadFile(path)
	if err != nil {
		return b, fmt.Errorf("read runtime binding %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("parse runtime binding %s: %w", path, err)
	}
	return b, nil
}

func Doctor(b Bundle) DoctorReport {
	rb := b.RuntimeBinding
	sc := b.ServiceContract
	report := DoctorReport{ServiceID: rb.ServiceID, Owner: rb.OwnerEntity, Status: "ok"}
	add := func(name string, ok bool, detail string) {
		status := "ok"
		if !ok {
			status = "fail"
			report.Status = "fail"
		}
		report.Checks = append(report.Checks, Check{Name: name, Status: status, Detail: detail})
	}

	add("service", rb.ServiceID != "" && sc.ServiceID != "" && rb.ServiceID == sc.ServiceID, joinNonEmpty(rb.ServiceID, sc.ServiceID))
	add("owner", rb.OwnerEntity != "" && sc.OwnerEntity != "" && rb.OwnerEntity == sc.OwnerEntity, joinNonEmpty(rb.OwnerEntity, sc.OwnerEntity))
	add("host", rb.HostBinding != "", rb.HostBinding)
	add("deploy", rb.DeploymentBinding != "", rb.DeploymentBinding)
	add("data", len(rb.DataBindings) > 0, strings.Join(rb.DataBindings, ", "))
	add("health", len(rb.HealthCheckRefs) > 0, strings.Join(rb.HealthCheckRefs, ", "))
	add("interface", len(sc.InterfaceRefs) > 0, strings.Join(sc.InterfaceRefs, ", "))
	add("risk", rb.RiskTier != "" && sc.RiskTier != "" && rb.RiskTier == sc.RiskTier, joinNonEmpty(rb.RiskTier, sc.RiskTier))
	add("evidence", len(rb.EvidenceRefs) > 0 && len(sc.EvidenceRefs) > 0, joinNonEmpty(strings.Join(rb.EvidenceRefs, ", "), strings.Join(sc.EvidenceRefs, ", ")))
	add("status", validStatus(rb.Status) && validStatus(sc.Status), joinNonEmpty(rb.Status, sc.Status))

	return report
}

func validStatus(s string) bool {
	for _, allowed := range statuses {
		if s == allowed {
			return true
		}
	}
	return false
}

func joinNonEmpty(vals ...string) string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, " / ")
}
