package main

// R3A slice 2 deliverable 6: `compound doctor` prints one plain line per
// lane named by the route plan for work.continue.unattended. Tests pin:
//   - the line shape (`unattended execute: <lane> capacity <N>% (<age>),
//     route <eligible|avoid>, grant <present|absent>`);
//   - the eligibility verdict (gate_role eligible vs avoid from the seeded
//     manifest);
//   - the grant presence (with the operator's standing-grants file vs
//     without);
//   - the no-route-plan fallback (single explicit line, not silence).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/routeevidence"
)

// seedRoutePlanForDoctor writes a minimal model-capabilities manifest to
// the test home's registries dir. claimLevel is "capable" (gate_role
// eligible) or "avoid" (gate_role avoid).
func seedRoutePlanForDoctor(t *testing.T, homePath, claimLevel string) {
	t.Helper()
	body := "version: 1\n" +
		"purpose: doctor unattended-execute fixture\n" +
		"models:\n" +
		"  claude-minimax (token plan):\n" +
		"    lane: claude-minimax (token plan)\n" +
		"    harness: claude_minimax\n" +
		"    tier: cheap\n" +
		"    packet_ceiling: multi-file\n" +
		"    capabilities:\n" +
		"      work.continue.unattended:\n" +
		"        level: " + claimLevel + "\n" +
		"        evidence: doctor fixture\n" +
		"routing_defaults:\n" +
		"  work.continue.unattended:\n" +
		"    - claude-minimax (token plan)\n"
	dir := filepath.Join(homePath, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model-capabilities.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedUnattendedGrantForDoctor writes the operator's standing-grants file
// with grant_unattended_work_continue_v1 present. Caller passes present=true
// to install the grant; false leaves the file absent (fail-closed default).
func seedUnattendedGrantForDoctor(t *testing.T, homePath string, present bool) {
	t.Helper()
	if !present {
		return
	}
	dir := filepath.Join(homePath, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":"compound.standing_grants.v0.1","grants":[{` +
		`"id":"grant_unattended_work_continue_v1","capability":"work.continue.unattended",` +
		`"mutation_class":"local_reversible","reversibility_ceiling":"reversible",` +
		`"blast_radius":"local","target_path":"` + homePath + `/**","expires_at":"2099-01-01T00:00:00Z"}]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "standing-grants.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorUnattendedExecutePrintsEligibleLaneLine: a route plan with an
// eligible lane and the grant installed prints the canonical line shape.
func TestDoctorUnattendedExecutePrintsEligibleLaneLine(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedRoutePlanForDoctor(t, homePath, "capable")
	seedUnattendedGrantForDoctor(t, homePath, true)
	// Seed a fresh capacity row so the printed age is finite and the
	// percent is observable (the row only needs to be readable; the
	// doctor falls back to the default reader config).
	if err := os.WriteFile(filepath.Join(homePath, "usage-history.jsonl"),
		[]byte(fmt.Sprintf(`{"provider":"minimax","windowKind":"rolling","windowMinutes":60,"usedPercent":30,"resetsAt":%q,"sampledAt":%q}`+"\n",
			now.Add(time.Hour).UTC().Format(time.RFC3339),
			now.Add(-2*time.Minute).UTC().Format(time.RFC3339))), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pin the capacity config var to the test home so the default reader
	// does not reach into the operator's real CodexBar feed.
	prevConfig := executeStageCapacityConfig
	executeStageCapacityConfig = func(home string) routeevidence.LaneCapacityConfig {
		cfg := routeevidence.DefaultLaneCapacityConfig(home)
		cfg.Paths.UsageHistoryPath = filepath.Join(homePath, "usage-history.jsonl")
		cfg.Paths.QuotaCachePath = ""
		return cfg
	}
	t.Cleanup(func() { executeStageCapacityConfig = prevConfig })

	rows := buildDoctorUnattendedExecuteRows(homePath, now)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1; rows=%+v", len(rows), rows)
	}
	if rows[0].Lane != "claude_minimax" {
		t.Fatalf("lane = %q, want claude_minimax", rows[0].Lane)
	}
	if rows[0].RouteGate != "eligible" {
		t.Fatalf("route_gate = %q, want eligible", rows[0].RouteGate)
	}
	if !rows[0].GrantPresent {
		t.Fatal("grant must read present when the operator's file is installed")
	}
	if !rows[0].CapacityFresh {
		t.Fatalf("capacity must read fresh (2m old, used 30%%); row=%+v", rows[0])
	}

	var buf bytes.Buffer
	printUnattendedExecute(&buf, rows)
	out := buf.String()
	if !strings.Contains(out, "unattended execute: claude_minimax capacity 30% (") {
		t.Fatalf("doctor line must match the canonical shape; got %q", out)
	}
	if !strings.Contains(out, "route eligible") {
		t.Fatalf("doctor line must report route eligible; got %q", out)
	}
	if !strings.Contains(out, "grant present") {
		t.Fatalf("doctor line must report grant present; got %q", out)
	}
}

// TestDoctorUnattendedExecuteMarksAvoidAndAbsentGrant: an avoid-gate-role
// lane plus no grant both surface explicit states; the row is not dropped.
func TestDoctorUnattendedExecuteMarksAvoidAndAbsentGrant(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedRoutePlanForDoctor(t, homePath, "avoid")
	seedUnattendedGrantForDoctor(t, homePath, false)

	rows := buildDoctorUnattendedExecuteRows(homePath, now)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].RouteGate != "avoid" {
		t.Fatalf("route_gate = %q, want avoid", rows[0].RouteGate)
	}
	if rows[0].GrantPresent {
		t.Fatal("grant must read absent when the operator's file is not installed")
	}
	if len(rows[0].Notes) == 0 {
		t.Fatal("avoid + absent grant must surface at least one explanatory note")
	}
}

// TestDoctorUnattendedExecuteEmptyStateIsExplicit: when the manifest names
// no lane for work.continue.unattended the section still renders one
// explicit line — silence would let an operator think the seam was
// simply unconfigured.
func TestDoctorUnattendedExecuteEmptyStateIsExplicit(t *testing.T) {
	homePath := initializedTestHome(t)
	rows := buildDoctorUnattendedExecuteRows(homePath, time.Now().UTC())
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 on an empty manifest", len(rows))
	}
	var buf bytes.Buffer
	printUnattendedExecute(&buf, rows)
	if !strings.Contains(buf.String(), "no route plan names a lane") {
		t.Fatalf("empty doctor section must be explicit; got %q", buf.String())
	}
}

// TestDoctorUnattendedExecuteJSONShapeMatchesText: the JSON shape mirrors
// the plain-text section (a future operator dashboard reads the same
// schema) — pin the keys so a future rename of the route_gate field
// shows up here rather than as a silent dashboard regression.
func TestDoctorUnattendedExecuteJSONShapeMatchesText(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedRoutePlanForDoctor(t, homePath, "capable")
	seedUnattendedGrantForDoctor(t, homePath, true)

	rows := buildDoctorUnattendedExecuteRows(homePath, now)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	encoded, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"lane", "route_gate", "capacity_percent", "capacity_fresh", "grant_present"} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("doctor JSON shape must keep %q; got %s", key, string(encoded))
		}
	}
}
