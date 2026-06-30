package runtimebinding

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorValidSyntheticService(t *testing.T) {
	b := validBundle()
	report := Doctor(b)
	if report.Status != "ok" {
		t.Fatalf("status = %q, checks = %+v", report.Status, report.Checks)
	}
	if report.ServiceID != "service:synthetic-api" || report.Owner != "product:synthetic" {
		t.Fatalf("identity not preserved: %+v", report)
	}
	if len(report.Checks) != 10 {
		t.Fatalf("checks = %d, want 10", len(report.Checks))
	}
}

func TestDoctorFailsMissingHost(t *testing.T) {
	b := validBundle()
	b.RuntimeBinding.HostBinding = ""
	report := Doctor(b)
	if report.Status != "fail" {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	if got := checkStatus(report, "host"); got != "fail" {
		t.Fatalf("host check = %q, want fail", got)
	}
}

func TestDoctorWithLiveProbes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	filePath := filepath.Join(t.TempDir(), "service.db")
	if err := os.WriteFile(filePath, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNTHETIC_RUNTIME_TOKEN", "present")

	b := validBundle()
	b.RuntimeProbes = []RuntimeProbe{
		{ID: "health", Kind: "http", Target: srv.URL + "/health", ExpectedStatus: http.StatusNoContent},
		{ID: "port", Kind: "tcp", Target: ln.Addr().String()},
		{ID: "data", Kind: "file", Target: filePath},
		{ID: "credential", Kind: "env", Target: "SYNTHETIC_RUNTIME_TOKEN"},
	}
	report := DoctorWithLiveProbes(context.Background(), b, time.Now())
	if report.Status != "ok" {
		t.Fatalf("status = %q, checks = %+v", report.Status, report.Checks)
	}
	for _, name := range []string{"live:health", "live:port", "live:data", "live:credential"} {
		if got := checkStatus(report, name); got != "ok" {
			t.Fatalf("%s = %q, checks=%+v", name, got, report.Checks)
		}
	}
}

func TestDoctorWithLiveProbesFailsUnavailableEvidence(t *testing.T) {
	b := validBundle()
	b.RuntimeProbes = []RuntimeProbe{
		{ID: "credential", Kind: "env", Target: "COMPOUND_TEST_MISSING_RUNTIME_TOKEN"},
		{ID: "data", Kind: "file", Target: filepath.Join(t.TempDir(), "missing.db")},
	}
	report := DoctorWithLiveProbes(context.Background(), b, time.Now())
	if report.Status != "fail" {
		t.Fatalf("status = %q, checks = %+v", report.Status, report.Checks)
	}
	if got := checkStatus(report, "live:credential"); got != "fail" {
		t.Fatalf("credential check = %q, want fail", got)
	}
	if got := checkStatus(report, "live:data"); got != "fail" {
		t.Fatalf("data check = %q, want fail", got)
	}
	if !NeedsWriteback(report) {
		t.Fatalf("live runtime failure should need writeback")
	}
}

func TestWritebackGateForFailedRuntime(t *testing.T) {
	b := validBundle()
	b.RuntimeBinding.HostBinding = ""
	report := Doctor(b)
	if !NeedsWriteback(report) {
		t.Fatalf("failed runtime doctor should need writeback")
	}
	gate := WritebackGate(report, "act_runtime", "pol_runtime", time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC))
	if gate.OwnerTarget.Kind != "runtime_binding" || gate.OwnerTarget.Ref != "service:synthetic-api" {
		t.Fatalf("wrong owner target: %+v", gate.OwnerTarget)
	}
	if gate.PolicyDecisionRef != "pol_runtime" || gate.ActionIntentRef != "act_runtime" {
		t.Fatalf("wrong gate refs: %+v", gate)
	}
	if len(gate.EvidenceRefs) < len(report.Checks)+3 {
		t.Fatalf("expected runtime evidence refs, got %+v", gate.EvidenceRefs)
	}
}

func TestBuildRuntimeRepairPlan(t *testing.T) {
	b := validBundle()
	b.RuntimeBinding.HostBinding = ""
	report := Doctor(b)
	plan, err := BuildRepairPlan(report, RepairRefs{
		WritebackGateID: "owg_runtime",
		WorkID:          "work_runtime",
		ReceiptID:       "rec_runtime",
		TransitionID:    "tr_runtime",
	}, time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != RepairPlanSchemaVersion || plan.Status != "review_required" || plan.FailedCount != 1 {
		t.Fatalf("bad plan: %+v", plan)
	}
	if plan.WritebackGateID != "owg_runtime" || plan.OwnerTarget != "runtime_binding:service:synthetic-api" {
		t.Fatalf("bad refs: %+v", plan)
	}
	if len(plan.ReviewActions) != 1 || plan.ReviewActions[0].Check != "host" {
		t.Fatalf("review actions = %+v", plan.ReviewActions)
	}
	review := RenderRepairReview(plan)
	if !strings.Contains(review, "does not mutate runtime owner files") || !strings.Contains(review, "declare the host") {
		t.Fatalf("review did not preserve gate warning/action:\n%s", review)
	}
}

func TestRecordRuntimeRepairPlan(t *testing.T) {
	b := validBundle()
	b.RuntimeBinding.HostBinding = ""
	report := Doctor(b)
	plan, err := BuildRepairPlan(report, RepairRefs{WritebackGateID: "owg_runtime"}, time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := RecordRepairPlan(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.PlanPath); err != nil {
		t.Fatalf("plan missing: %v", err)
	}
	if _, err := os.Stat(paths.ReviewPath); err != nil {
		t.Fatalf("review missing: %v", err)
	}
}

func TestLoadFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.yaml")
	body := `runtime_binding:
  service_id: service:synthetic-api
  owner_entity: product:synthetic
  host_binding: host:local-dev
  deployment_binding: process:synthetic-api
  data_bindings: [sqlite:synthetic.db]
  health_check_refs: [http://127.0.0.1:18080/health]
  risk_tier: tier1
  evidence_refs: [fixture:runtime-binding]
  status: active
service_contract:
  service_id: service:synthetic-api
  interface_refs: [http:get:/health]
  dependencies: [sqlite]
  owner_entity: product:synthetic
  risk_tier: tier1
  evidence_refs: [fixture:service-contract]
  status: active
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if report := Doctor(b); report.Status != "ok" {
		t.Fatalf("loaded fixture failed doctor: %+v", report)
	}
}

func validBundle() Bundle {
	return Bundle{
		RuntimeBinding: RuntimeBinding{
			ServiceID:         "service:synthetic-api",
			OwnerEntity:       "product:synthetic",
			HostBinding:       "host:local-dev",
			DeploymentBinding: "process:synthetic-api",
			DataBindings:      []string{"sqlite:synthetic.db"},
			HealthCheckRefs:   []string{"http://127.0.0.1:18080/health"},
			RiskTier:          "tier1",
			EvidenceRefs:      []string{"fixture:runtime-binding"},
			Status:            StatusActive,
		},
		ServiceContract: ServiceContract{
			ServiceID:     "service:synthetic-api",
			InterfaceRefs: []string{"http:get:/health"},
			Dependencies:  []string{"sqlite"},
			OwnerEntity:   "product:synthetic",
			RiskTier:      "tier1",
			EvidenceRefs:  []string{"fixture:service-contract"},
			Status:        StatusActive,
		},
	}
}

func checkStatus(r DoctorReport, name string) string {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return ""
}
