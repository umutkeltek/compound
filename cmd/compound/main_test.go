package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/product"
	"compound/internal/projection"
	"compound/internal/sourceobs"
	"compound/internal/work"
)

var cliTestNow = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func TestOrientUsesRepoKindRegistry(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := os.MkdirAll(filepath.Join(homePath, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	controlRoot := t.TempDir()
	controlCwd := filepath.Join(controlRoot, "umut-os")
	if err := os.MkdirAll(controlCwd, 0o755); err != nil {
		t.Fatal(err)
	}
	productRoot := t.TempDir()
	productCwd := filepath.Join(productRoot, "ios-app")
	if err := os.MkdirAll(productCwd, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := "schema_version: compound.repo_kind_registry.v0.1\nrepos:\n  - kind: control-plane\n    roots:\n      - " + controlRoot + "\n  - kind: product\n    roots:\n      - " + productRoot + "\n"
	if err := os.WriteFile(filepath.Join(homePath, "config", "repo-kinds.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}

	var control envelope.ContextPack
	out := captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", controlCwd, "--task", "audit hooks"})
	})
	if err := json.Unmarshal([]byte(out), &control); err != nil {
		t.Fatalf("parse control orient: %v\n%s", err, out)
	}
	if !contextPackIncludes(control, "control-plane://workflow-contract") {
		t.Fatalf("control-plane pack should include workflow contract: %+v", control.Included)
	}
	if control.Uncertainty.Level != "known" {
		t.Fatalf("registry-resolved pack should be known, got %q", control.Uncertainty.Level)
	}

	var product envelope.ContextPack
	out = captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", productCwd, "--task", "prepare release"})
	})
	if err := json.Unmarshal([]byte(out), &product); err != nil {
		t.Fatalf("parse product orient: %v\n%s", err, out)
	}
	if contextPackIncludes(product, "control-plane://workflow-contract") {
		t.Fatalf("product pack should not include workflow contract: %+v", product.Included)
	}
	if !contextPackExcludes(product, "control-plane://workflow-contract") {
		t.Fatalf("product pack should record workflow contract exclusion: %+v", product.Excluded)
	}
}

func TestHookSessionEndAutoConsumesCapabilityFeedback(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	graph, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capability graph: %v", err)
	}
	if err := graph.Add(capgraph.Node{
		ID:       "skill:session-end-proof",
		Kind:     "skill",
		Name:     "Session End Proof",
		Triggers: []string{"session end proof"},
	}); err != nil {
		t.Fatalf("add capability node: %v", err)
	}

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w, err := store.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "session-end validates used capability",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.RecordAction(
		w.ID,
		testActionIntent(w.ID, "act_session_end_used", "capability://skill:session-end-proof"),
		testPolicyDecision(w.ID, "pol_session_end_used", "act_session_end_used"),
		cliTestNow,
	); err != nil {
		t.Fatalf("record action: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	fixturePath := filepath.Join(t.TempDir(), "session-end.json")
	writeJSONFixture(t, fixturePath, hook.Fixture{
		Harness:  "codex",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "session-end closure used capability",
	})

	cmdHook([]string{"session-end", fixturePath})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:session-end-proof") {
		t.Fatalf("session-end did not auto-validate used capability; edges=%+v", edges)
	}
}

func TestAuthorityPathRegistryImportsOSRegistryAndResolvesMovedPaths(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("HOME", "/home/tester")
	fixture := filepath.Join("..", "..", "fixtures", "authority", "os-path-authority.yaml")

	out := captureStdout(t, func() {
		cmdAuthority([]string{"path-registry", fixture})
	})
	if !strings.Contains(out, "entities: 5") || !strings.Contains(out, "status: ok") {
		t.Fatalf("path-registry output =\n%s", out)
	}

	active := captureStdout(t, func() {
		cmdAuthority([]string{"resolve", "/home/tester/Projects/control-spine/root"})
	})
	if !strings.Contains(active, "status: resolved") || !strings.Contains(active, "entity: source:control-spine") {
		t.Fatalf("active path resolve output =\n%s", active)
	}

	retired := captureStdout(t, func() {
		cmdAuthority([]string{"resolve", "/home/tester/Projects/old-control-spine"})
	})
	if !strings.Contains(retired, "status: resolved") || !strings.Contains(retired, "entity: source:old-control-spine") {
		t.Fatalf("retired path resolve output =\n%s", retired)
	}

	oldAuthority := captureStdout(t, func() {
		cmdAuthority([]string{"source:old-control-spine"})
	})
	if strings.Contains(oldAuthority, "**current_path**") {
		t.Fatalf("retired root must not be current authority:\n%s", oldAuthority)
	}
	if !strings.Contains(oldAuthority, "**known_path_retired**: /home/tester/Projects/old-control-spine") {
		t.Fatalf("retired authority output missing known retired path:\n%s", oldAuthority)
	}
}

func TestAuthorityGitImportsSourceControlState(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	repo := initCompoundCommandGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdAuthority([]string{"git", repo})
	})
	for _, want := range []string{"# source control", "- status: dirty", "- branch: main", "- unstaged: 1", "- untracked: 1", "- changed_paths: 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("authority git output missing %q:\n%s", want, out)
		}
	}
	resolved := captureStdout(t, func() {
		cmdAuthority([]string{"resolve", repo})
	})
	if !strings.Contains(resolved, "status: resolved") || !strings.Contains(resolved, "entity: repo:") {
		t.Fatalf("git root did not resolve source-control entity:\n%s", resolved)
	}
}

func TestImportedSkillOfferCanBecomeValidatedSessionEndFeedback(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	cmdHook([]string{"user-prompt-submit", writeHookFixture(t, hook.Fixture{
		Harness:  "codex",
		Cwd:      "/repo/product",
		RepoKind: "product",
		Task:     "pricing paywall",
		Prompt:   "prepare pricing paywall review",
	})})

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w := onlyWork(t, store)
	if w.IDs.ContextPackID == "" {
		t.Fatal("user-prompt work did not preserve context_pack_id")
	}
	pack := readContextPack(t, homePath, w.IDs.ContextPackID)
	if !contextPackIncludes(pack, "capability://skill:pricing") {
		t.Fatalf("context pack did not include imported pricing capability; included=%+v", pack.Included)
	}

	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "ready"}, cliTestNow); err != nil {
		t.Fatalf("advance ready: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.RecordAction(
		w.ID,
		testActionIntent(w.ID, "act_imported_skill_used", "capability://skill:pricing"),
		testPolicyDecision(w.ID, "pol_imported_skill_used", "act_imported_skill_used"),
		cliTestNow,
	); err != nil {
		t.Fatalf("record action: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	cmdHook([]string{"session-end", writeHookFixture(t, hook.Fixture{
		Harness:  "codex",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "pricing paywall review used imported skill",
	})})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:pricing") {
		t.Fatalf("session-end did not validate imported used skill; edges=%+v", edges)
	}
}

func TestWorkCloseExplicitFeedbackAutoConsumes(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	graph, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capability graph: %v", err)
	}
	for _, node := range []capgraph.Node{
		{ID: "skill:missed-cli", Kind: "skill", Name: "Missed CLI", Triggers: []string{"pricing"}},
		{ID: "skill:false-cli", Kind: "skill", Name: "False CLI", Triggers: []string{"pricing"}},
	} {
		if err := graph.Add(node); err != nil {
			t.Fatalf("add node %s: %v", node.ID, err)
		}
	}

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w, err := store.Create(work.CreateInput{Owner: "codex", SuccessPredicate: "record explicit feedback"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	cmdWork([]string{
		"close", w.ID,
		"--outcome", "failed",
		"--move-type", "outward_delta",
		"--summary", "pricing review",
		"--changed",
		"--missed", "capability://skill:missed-cli",
		"--false-triggered", "capability://skill:false-cli",
	})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdgeTo(edges, capgraph.EdMissedWhen, "skill:missed-cli") {
		t.Fatalf("work close did not auto-consume missed feedback; edges=%+v", edges)
	}
	if !hasCapabilityEdgeTo(edges, capgraph.EdFalseTriggeredBy, "skill:false-cli") {
		t.Fatalf("work close did not auto-consume false-trigger feedback; edges=%+v", edges)
	}
}

func TestDistillerRunAutoConsumesCapabilityObservation(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	graph, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capability graph: %v", err)
	}
	if err := graph.Add(capgraph.Node{ID: "skill:distill-pricing", Kind: "skill", Name: "Distill Pricing", Triggers: []string{"pricing"}}); err != nil {
		t.Fatalf("add node: %v", err)
	}

	tracePath := writeJSONFile(t, `{
		"trace_ref":"trace_distiller_feedback",
		"candidates":[{
			"kind":"capability_observation",
			"subject":"skill:distill-pricing",
			"statement":"pricing review",
			"detail":"missed_when",
			"confidence":0.9
		}]
	}`)
	cmdDistiller([]string{"run", tracePath})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdgeTo(edges, capgraph.EdMissedWhen, "skill:distill-pricing") {
		t.Fatalf("distiller run did not auto-consume capability observation; edges=%+v", edges)
	}
}

func TestDistillerRunDoesNotCreateCapabilityGraph(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
		"trace_ref":"trace_no_graph",
		"candidates":[{
			"kind":"capability_observation",
			"subject":"skill:no-graph",
			"statement":"pricing review",
			"detail":"missed_when",
			"confidence":0.9
		}]
	}`)
	cmdDistiller([]string{"run", tracePath})

	if _, err := os.Stat(filepath.Join(homePath, "registries", "capability-nodes.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("distiller run created capability graph unexpectedly: %v", err)
	}
}

func TestRuntimeFeedbackReachesDistillerRequestAndGraph(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	cmdHook([]string{"user-prompt-submit", writeHookFixture(t, hook.Fixture{
		Harness:  "codex",
		Cwd:      "/repo/product",
		RepoKind: "product",
		Task:     "pricing paywall",
		Prompt:   "prepare pricing paywall review",
	})})

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w := onlyWork(t, store)
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "ready"}, cliTestNow); err != nil {
		t.Fatalf("advance ready: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	cmdHook([]string{"session-end", writeHookFixture(t, hook.Fixture{
		Harness:        "codex",
		WorkID:         w.ID,
		MoveType:       "outward_delta",
		Outcome:        "failed",
		Summary:        "pricing paywall feedback",
		Missed:         []string{"capability://skill:pricing"},
		FalseTriggered: []string{"capability://skill:pricing"},
	})})

	reqJSON := captureStdout(t, func() {
		cmdDistiller([]string{"request", "--trace-ref", "trace_runtime_feedback", "--work-id", w.ID})
	})
	var req distiller.LLMExtractionRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("parse distiller request: %v\n%s", err, reqJSON)
	}
	if len(req.CapabilityFeedback) != 2 {
		t.Fatalf("capability_feedback = %+v, want missed + false-trigger", req.CapabilityFeedback)
	}
	if req.CapabilityFeedback[0].Subject != "skill:pricing" || req.CapabilityFeedback[0].Detail != "missed_when" {
		t.Fatalf("missed feedback not preserved: %+v", req.CapabilityFeedback[0])
	}
	if req.CapabilityFeedback[1].Subject != "skill:pricing" || req.CapabilityFeedback[1].Detail != "false_triggered_by" {
		t.Fatalf("false-trigger feedback not preserved: %+v", req.CapabilityFeedback[1])
	}
	if !strings.Contains(strings.Join(req.Instructions, "\n"), "Do not infer missed_when or false_triggered_by from skills.candidate offers") {
		t.Fatalf("request missing no-inference instruction: %v", req.Instructions)
	}

	before := len(readCapabilityEdges(t, homePath))
	responsePath := writeJSONFile(t, `{
		"schema_version":"compound.distiller.llm_extraction.v0.1",
		"trace_ref":"trace_runtime_feedback_response",
		"source_refs":["receipt:runtime_feedback"],
		"candidates":[
			{"kind":"capability_observation","subject":"skill:pricing","statement":"pricing paywall feedback","detail":"missed_when","confidence":0.9,"evidence_refs":["receipt:runtime_feedback"]},
			{"kind":"capability_observation","subject":"skill:pricing","statement":"pricing paywall feedback","detail":"false_triggered_by","confidence":0.9,"evidence_refs":["receipt:runtime_feedback"]}
		]
	}`)
	cmdDistiller([]string{"run", "--llm-response", responsePath})
	after := readCapabilityEdges(t, homePath)
	if len(after) <= before {
		t.Fatalf("distiller feedback did not add graph edges; before=%d after=%d edges=%+v", before, len(after), after)
	}
	if countCapabilityEdgeTo(after, capgraph.EdMissedWhen, "skill:pricing") < 2 {
		t.Fatalf("missed feedback did not flow through receipt and distiller paths; edges=%+v", after)
	}
	if countCapabilityEdgeTo(after, capgraph.EdFalseTriggeredBy, "skill:pricing") < 2 {
		t.Fatalf("false-trigger feedback did not flow through receipt and distiller paths; edges=%+v", after)
	}
}

func TestProductImportProjectContractPromotesSourceInPlaceIdentity(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{
			"import",
			"--project-contract", filepath.Join("..", "..", "fixtures", "product", "project-contract.yaml"),
			"--release", filepath.Join("..", "..", "fixtures", "product", "release-contract.yaml"),
		})
	})
	if !strings.Contains(out, "# product import product:source_contract_demo") ||
		!strings.Contains(out, "- claims: 4") ||
		!strings.Contains(out, "- signals: 0") ||
		!strings.Contains(out, "- hypotheses: 0") {
		t.Fatalf("product import output missing source-contract proof:\n%s", out)
	}
	resolved := captureStdout(t, func() {
		cmdAuthority([]string{"resolve", "appstore:9876543210"})
	})
	if !strings.Contains(resolved, "status: resolved") || !strings.Contains(resolved, "entity: product:source_contract_demo") {
		t.Fatalf("app store identifier did not resolve to product entity:\n%s", resolved)
	}
}

func TestProductSignalImportsASCDatapointsSourceInPlace(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{
			"signal",
			"--asc-datapoints", filepath.Join("..", "..", "fixtures", "product", "asc-datapoints.json"),
			"--product", "product:focus-notes",
			"--app-id", "123456789",
			"--metric", "review_count",
		})
	})
	if !strings.Contains(out, "# product signal sig_") || !strings.Contains(out, "- product: product:focus-notes") {
		t.Fatalf("product signal output missing ASC import proof:\n%s", out)
	}
	tl, err := product.LoadTimeline(homePath, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Signals) != 1 || tl.Signals[0].MetricName != "app_store_connect_review_count" || tl.Signals[0].Before != tl.Signals[0].After {
		t.Fatalf("ASC signal timeline = %+v", tl.Signals)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("ASC signal import should not create promoted claims ledger: %v", err)
	}
}

func TestProductSignalImportsASCSalesSeriesWindows(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{
			"signal",
			"--asc-sales-series", filepath.Join("..", "..", "fixtures", "product", "asc-sales-series.jsonl"),
			"--product", "product:focus-notes",
			"--app-id", "123456789",
			"--metric", "units",
			"--before", "2026-07-01/2026-07-03",
			"--after", "2026-07-08/2026-07-10",
			"--change-event", "Changed App Store screenshot order.",
		})
	})
	if !strings.Contains(out, "# product signal sig_") || !strings.Contains(out, "- product: product:focus-notes") {
		t.Fatalf("product signal output missing ASC sales import proof:\n%s", out)
	}
	tl, err := product.LoadTimeline(homePath, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Signals) != 1 || tl.Signals[0].MetricName != "app_store_connect_units" || tl.Signals[0].Before != 9 || tl.Signals[0].After != 18 {
		t.Fatalf("ASC sales signal timeline = %+v", tl.Signals)
	}
	if len(tl.Hypotheses) != 0 {
		t.Fatalf("ASC sales signal should not create hypotheses: %+v", tl.Hypotheses)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("ASC sales signal import should not create promoted claims ledger: %v", err)
	}
}

func TestSourceObserveWritesCandidateAndNoPromoteWithoutPromotion(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdSource([]string{"observe", filepath.Join("..", "..", "fixtures", "sourceobs", "generated-skill-manifest.yaml")})
	})
	if !strings.Contains(out, "# source observation srcobs_fixture_skill_manifest") ||
		!strings.Contains(out, "- candidate:") ||
		!strings.Contains(out, "- no_promote:") {
		t.Fatalf("source observe output missing proof lines:\n%s", out)
	}

	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read candidate dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("candidate entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(candidateDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	var candidate sourceobs.CandidateRecord
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatalf("parse candidate: %v", err)
	}
	if candidate.Kind != string(candidates.CapabilityObs) ||
		candidate.Verdict != candidates.NoPromote ||
		candidate.PromoteAllowed {
		t.Fatalf("candidate promotion state = kind %q verdict %q allowed %v", candidate.Kind, candidate.Verdict, candidate.PromoteAllowed)
	}
	if candidate.SourceObservationID != "srcobs_fixture_skill_manifest" {
		t.Fatalf("candidate source observation = %q", candidate.SourceObservationID)
	}

	noPromoteRows := readNoPromoteDecisionRows(t, homePath)
	if len(noPromoteRows) != 1 {
		t.Fatalf("no-promote rows = %d, want 1", len(noPromoteRows))
	}
	if noPromoteRows[0].CandidateRef != candidate.ID ||
		noPromoteRows[0].OwnerTarget.Kind != "source_observation" ||
		noPromoteRows[0].OwnerTarget.Ref != "srcobs_fixture_skill_manifest" {
		t.Fatalf("no-promote row not tied to source observation candidate: %+v candidate=%s", noPromoteRows[0], candidate.ID)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("source observe should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestEventImportOSWritesTraceEnvelopeWithoutPromotion(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := filepath.Join("..", "..", "fixtures", "events", "os-events.ndjson")

	out := captureStdout(t, func() {
		cmdEvent([]string{"import-os", fixture, "--stream", "control-events"})
	})
	if !strings.Contains(out, "imported: 2") || !strings.Contains(out, "skipped: 0") {
		t.Fatalf("event import output =\n%s", out)
	}
	tracePath := filepath.Join(homePath, "events", "traces.ndjson")
	lines := splitJSONLines(readFile(t, tracePath))
	if len(lines) != 2 {
		t.Fatalf("trace lines = %d, want 2", len(lines))
	}
	var tr envelope.TraceEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &tr); err != nil {
		t.Fatalf("parse trace: %v", err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("trace invalid: %v", err)
	}
	if tr.Kind() != "trace_envelope" || tr.Source.System != "os-event" || tr.MutationClass != "read" {
		t.Fatalf("trace = %+v", tr)
	}

	replay := captureStdout(t, func() {
		cmdEvent([]string{"import-os", fixture, "--stream", "control-events"})
	})
	if !strings.Contains(replay, "imported: 0") || !strings.Contains(replay, "skipped: 2") {
		t.Fatalf("event replay output =\n%s", replay)
	}
	if lines := splitJSONLines(readFile(t, tracePath)); len(lines) != 2 {
		t.Fatalf("replay should not append duplicate traces, lines=%d", len(lines))
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("event import must not write claims ledger, stat err=%v", err)
	}
}

func TestDistillerRequestLoadsImportedEventTraces(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "events", "os-events.ndjson")
	captureStdout(t, func() {
		cmdEvent([]string{"import-os", fixture, "--stream", "control-events"})
	})

	var traces []envelope.TraceEnvelope
	for _, line := range splitJSONLines(readFile(t, filepath.Join(homePath, "events", "traces.ndjson"))) {
		var tr envelope.TraceEnvelope
		if err := json.Unmarshal([]byte(line), &tr); err != nil {
			t.Fatalf("parse trace line: %v", err)
		}
		traces = append(traces, tr)
	}
	if len(traces) != 2 {
		t.Fatalf("traces = %d, want 2", len(traces))
	}

	out := captureStdout(t, func() {
		cmdDistiller([]string{
			"request",
			"--trace-ref", "event-batch:control-events",
			"--event-trace", traces[0].ID,
			"--event-trace", traces[1].ID,
		})
	})
	var req distiller.LLMExtractionRequest
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatalf("parse distiller request: %v\n%s", err, out)
	}
	if req.TraceRef != "event-batch:control-events" || req.RawTrace != "" {
		t.Fatalf("request trace/raw = %q/%q", req.TraceRef, req.RawTrace)
	}
	traceEnvelopeCount := 0
	for _, env := range req.Envelopes {
		if env.Kind == "trace_envelope" {
			traceEnvelopeCount++
		}
	}
	if traceEnvelopeCount != 2 {
		t.Fatalf("trace envelope count = %d, envelopes=%+v", traceEnvelopeCount, req.Envelopes)
	}
	for _, tr := range traces {
		if !stringSliceContains(req.SourceRefs, "trace_envelope:"+tr.ID) {
			t.Fatalf("request source refs missing trace %s: %v", tr.ID, req.SourceRefs)
		}
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("distiller request over event traces must not write claims ledger, stat err=%v", err)
	}
}

func TestDistillerRunLLMEventResponseParksMemoryWithoutPromotion(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "events", "os-events.ndjson")
	captureStdout(t, func() {
		cmdEvent([]string{"import-os", fixture, "--stream", "control-events"})
	})

	lines := splitJSONLines(readFile(t, filepath.Join(homePath, "events", "traces.ndjson")))
	if len(lines) == 0 {
		t.Fatal("expected imported trace envelopes")
	}
	var trace envelope.TraceEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &trace); err != nil {
		t.Fatalf("parse trace line: %v", err)
	}
	traceRef := "trace_envelope:" + trace.ID
	confidence := 0.88
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "trace_event_runtime_candidate",
		SourceRefs:    []string{traceRef},
		Candidates: []distiller.LLMCandidate{{
			Kind:         distiller.KindMemory,
			Entity:       "project:" + trace.Scope.ProjectID,
			Predicate:    "event_observed",
			Object:       trace.Source.SourceID,
			Confidence:   &confidence,
			Derivation:   "llm_extracted_from_imported_trace",
			EvidenceRefs: []string{traceRef},
		}},
	}
	responseData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	responsePath := filepath.Join(t.TempDir(), "event-response.json")
	if err := os.WriteFile(responsePath, append(responseData, '\n'), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}

	out := captureStdout(t, func() {
		cmdDistiller([]string{"run", "--llm-response", responsePath})
	})
	if !strings.Contains(out, "# distilled trace_event_runtime_candidate") ||
		!strings.Contains(out, "memory") ||
		!strings.Contains(out, "park") ||
		!strings.Contains(out, "no promotion basis") {
		t.Fatalf("distiller run output missing parked memory proof:\n%s", out)
	}

	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.Memory))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read memory candidate dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("memory candidate entries = %d, want 1", len(entries))
	}
	candidateData, err := os.ReadFile(filepath.Join(candidateDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read memory candidate: %v", err)
	}
	var candidate distiller.Candidate
	if err := json.Unmarshal(candidateData, &candidate); err != nil {
		t.Fatalf("parse memory candidate: %v", err)
	}
	if candidate.Verdict != distiller.Park || !stringSliceContains(candidate.EvidenceRefs, traceRef) {
		t.Fatalf("candidate did not preserve parked trace evidence: %+v", candidate)
	}

	decisionLines := splitJSONLines(readFile(t, filepath.Join(homePath, "ledger", "adjudications.jsonl")))
	if len(decisionLines) != 1 {
		t.Fatalf("adjudication rows = %d, want 1", len(decisionLines))
	}
	var decision distiller.AdjudicationDecision
	if err := json.Unmarshal([]byte(decisionLines[0]), &decision); err != nil {
		t.Fatalf("parse adjudication: %v", err)
	}
	if decision.Kind != distiller.KindMemory || decision.Verdict != distiller.Park || !stringSliceContains(decision.EvidenceRefs, traceRef) {
		t.Fatalf("adjudication did not preserve parked trace evidence: %+v", decision)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("event-derived llm response must not promote claims, stat err=%v", err)
	}
}

func TestProjectionDoctorSourceObserveWritesMethodUpgradeNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdProjection([]string{"doctor", filepath.Join("..", "..", "fixtures", "projections", "stale-freshness.yaml"), "--source-observe"})
	})
	if !strings.Contains(out, "- status: stale") ||
		!strings.Contains(out, "- source_observation: srcobs_") ||
		!strings.Contains(out, "- source_candidate:") ||
		!strings.Contains(out, "- source_no_promote:") {
		t.Fatalf("projection source-observe output missing proof lines:\n%s", out)
	}

	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read method-upgrade dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("method-upgrade entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(candidateDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read method-upgrade candidate: %v", err)
	}
	var candidate sourceobs.CandidateRecord
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatalf("parse method-upgrade candidate: %v", err)
	}
	if candidate.Kind != string(candidates.MethodUpgrade) || candidate.Verdict != candidates.NoPromote || candidate.PromoteAllowed {
		t.Fatalf("candidate = %+v", candidate)
	}
	if candidate.Subject != "projection:synthetic-runtime-dashboard" ||
		!strings.HasPrefix(candidate.SourceObservationID, "srcobs_") ||
		candidate.SourceRef != "generated-dashboard.md" {
		t.Fatalf("candidate did not keep projection source identity: %+v", candidate)
	}

	rows := readNoPromoteDecisionRows(t, homePath)
	if len(rows) != 1 {
		t.Fatalf("no-promote rows = %d, want 1", len(rows))
	}
	if rows[0].CandidateRef != candidate.ID || rows[0].OwnerTarget.Kind != "source_observation" || !strings.HasPrefix(rows[0].OwnerTarget.Ref, "srcobs_") {
		t.Fatalf("no-promote row not tied to projection source observation: %+v", rows[0])
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("projection source-observe should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestProjectionRepairPlanCreatesGateSourceObservationAndReview(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdProjection([]string{"repair-plan", filepath.Join("..", "..", "fixtures", "projections", "stale-freshness.yaml")})
	})
	if !strings.Contains(out, "# projection repair-plan") ||
		!strings.Contains(out, "- writeback_gate: owg_") ||
		!strings.Contains(out, "- source_observation: srcobs_") ||
		!strings.Contains(out, "- source_candidate:") ||
		!strings.Contains(out, "- source_no_promote:") ||
		!strings.Contains(out, "- review:") ||
		!strings.Contains(out, "- artifact:") {
		t.Fatalf("repair-plan output missing proof lines:\n%s", out)
	}

	planDir := filepath.Join(homePath, "reviews", "projection-regeneration")
	planFiles, err := filepath.Glob(filepath.Join(planDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	reviewFiles, err := filepath.Glob(filepath.Join(planDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(planFiles) != 1 || len(reviewFiles) != 1 {
		t.Fatalf("plan/review files = %d/%d", len(planFiles), len(reviewFiles))
	}
	var plan struct {
		SchemaVersion       string   `json:"schema_version"`
		Status              string   `json:"status"`
		WritebackGateID     string   `json:"writeback_gate_id"`
		SourceObservationID string   `json:"source_observation_id"`
		CandidateIDs        []string `json:"candidate_ids"`
		NoPromoteIDs        []string `json:"no_promote_ids"`
	}
	data, err := os.ReadFile(planFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != projection.RegenerationPlanSchemaVersion || plan.Status != "review_required" ||
		!strings.HasPrefix(plan.WritebackGateID, "owg_") || !strings.HasPrefix(plan.SourceObservationID, "srcobs_") ||
		len(plan.CandidateIDs) != 1 || len(plan.NoPromoteIDs) != 1 {
		t.Fatalf("bad regeneration plan: %+v", plan)
	}
	review, err := os.ReadFile(reviewFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "does not regenerate or edit the generated view") {
		t.Fatalf("review did not preserve no-direct-write warning:\n%s", review)
	}
	rows := readNoPromoteDecisionRows(t, homePath)
	if len(rows) != 1 || rows[0].CandidateRef != plan.CandidateIDs[0] {
		t.Fatalf("no-promote rows = %+v plan=%+v", rows, plan)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("projection repair-plan should not promote claims, claims ledger stat err=%v", err)
	}
}

func testActionIntent(workID, id, capability string) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    capability,
		Action:        "write_receipt",
		Target:        envelope.ActionTarget{Kind: "path", Path: "events"},
		MutationClass: "local_reversible",
		Reversibility: "easy",
	}
}

func testPolicyDecision(workID, id, actionID string) envelope.PolicyDecision {
	return envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision,
			ID:            id,
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "policy", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: actionID,
		Outcome:        "allow_with_obligations",
		Reasons:        []string{"test action is local and reversible"},
		Obligations:    []string{"emit receipt"},
	}
}

func TestDecisionsRepairPlanBrainWritesReviewArtifactWithoutPromotion(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	brainPath := filepath.Join(t.TempDir(), "brain", "wiki", "reflections", "decision.md")
	if err := os.MkdirAll(filepath.Dir(brainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brainPath, []byte(`---
type: reflection
title: 2026 06 11 Memory Engine Ownership Decision 537a02
date: '2026-06-11T00:00:00.000Z'
---

# memory engine ownership decision - own contract, rent engine

## decision

The verdict:

> **"Keep the rented memory engine continuously updated. Own the contract seam."**

## How apply

When ReleaseX ships or is externally blocked, execute the parked spec.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdDecisions([]string{"repair-plan", "--brain", brainPath})
	})
	if !strings.Contains(out, "# decisions repair-plan") ||
		!strings.Contains(out, "source_kind: brain") ||
		!strings.Contains(out, "repairs: 1") ||
		!strings.Contains(out, "missing_falsifier: 1") ||
		!strings.Contains(out, "missing_review_trigger: 0") {
		t.Fatalf("repair-plan output =\n%s", out)
	}
	planPath := outputValue(out, "plan:")
	if planPath == "" {
		t.Fatalf("missing plan path in output:\n%s", out)
	}
	plan, err := decisions.LoadLifecycleRepairPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceKind != "brain" || plan.RepairCount != 1 || len(plan.Repairs) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.HasPrefix(plan.Repairs[0].EvidenceRef, "brain_source:") {
		t.Fatalf("evidence ref = %q", plan.Repairs[0].EvidenceRef)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("repair-plan should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestDecisionsRepairApplyBrainReviewedCopyClosesDebt(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	brainPath := filepath.Join(t.TempDir(), "brain", "wiki", "reflections", "decision.md")
	if err := os.MkdirAll(filepath.Dir(brainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brainPath, []byte(`---
type: reflection
title: 2026 06 11 Memory Engine Ownership Decision 537a02
date: '2026-06-11T00:00:00.000Z'
---

# memory engine ownership decision - own contract, rent engine

## decision

The verdict:

> **"Keep the rented memory engine continuously updated. Own the contract seam."**

## How apply

When ReleaseX ships or is externally blocked, execute the parked spec.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	planOut := captureStdout(t, func() {
		cmdDecisions([]string{"repair-plan", "--brain", brainPath})
	})
	planPath := outputValue(planOut, "plan:")
	if planPath == "" {
		t.Fatalf("missing plan path:\n%s", planOut)
	}
	applyOut := captureStdout(t, func() {
		cmdDecisions([]string{"repair-apply", "--plan", planPath, "--write-reviewed"})
	})
	if !strings.Contains(applyOut, "dry_run: false") ||
		!strings.Contains(applyOut, "applied_fields: 1") ||
		!strings.Contains(applyOut, "changed_files: 1") {
		t.Fatalf("repair-apply output =\n%s", applyOut)
	}
	body := readFile(t, brainPath)
	if !strings.Contains(body, "falsifier:") {
		t.Fatalf("reviewed brain copy missing applied falsifier:\n%s", body)
	}

	afterOut := captureStdout(t, func() {
		cmdDecisions([]string{"repair-plan", "--brain", brainPath})
	})
	afterPlanPath := outputValue(afterOut, "plan:")
	if afterPlanPath == "" {
		t.Fatalf("missing after plan path:\n%s", afterOut)
	}
	afterPlan, err := decisions.LoadLifecycleRepairPlan(afterPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if afterPlan.RepairCount != 0 || afterPlan.Status != "ok" {
		t.Fatalf("after plan = %+v", afterPlan)
	}

	captureStdout(t, func() {
		cmdDecisions([]string{"import", "--brain", brainPath})
	})
	claims := splitJSONLines(readFile(t, filepath.Join(homePath, "ledger", "claims.jsonl")))
	if len(claims) != 1 {
		t.Fatalf("claims = %v", claims)
	}
}

func writeHookFixture(t *testing.T, fixture hook.Fixture) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.json")
	writeJSONFixture(t, path, fixture)
	return path
}

func onlyWork(t *testing.T, store *work.Store) envelope.WorkEnvelope {
	t.Helper()
	works, err := store.List()
	if err != nil {
		t.Fatalf("list work: %v", err)
	}
	if len(works) != 1 {
		t.Fatalf("work count = %d, want 1: %+v", len(works), works)
	}
	return works[0]
}

func readContextPack(t *testing.T, homePath, id string) envelope.ContextPack {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(homePath, "context", "packs", id+".json"))
	if err != nil {
		t.Fatalf("read context pack %s: %v", id, err)
	}
	var pack envelope.ContextPack
	if err := json.Unmarshal(b, &pack); err != nil {
		t.Fatalf("parse context pack %s: %v", id, err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("context pack %s invalid: %v", id, err)
	}
	return pack
}

func contextPackIncludes(pack envelope.ContextPack, ref string) bool {
	for _, included := range pack.Included {
		if included.Ref == ref {
			return true
		}
	}
	return false
}

func contextPackExcludes(pack envelope.ContextPack, ref string) bool {
	for _, excluded := range pack.Excluded {
		if excluded.Ref == ref && excluded.Reason != "" {
			return true
		}
	}
	return false
}

func writeJSONFixture(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func outputValue(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			return strings.TrimSpace(strings.TrimPrefix(line, key))
		}
	}
	return ""
}

func readCapabilityEdges(t *testing.T, homePath string) []capgraph.Edge {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(homePath, "registries", "capability-edges.jsonl"))
	if err != nil {
		t.Fatalf("read capability edges: %v", err)
	}
	var edges []capgraph.Edge
	for _, line := range splitJSONLines(string(b)) {
		var edge capgraph.Edge
		if err := json.Unmarshal([]byte(line), &edge); err != nil {
			t.Fatalf("parse edge %q: %v", line, err)
		}
		edges = append(edges, edge)
	}
	return edges
}

type noPromoteDecisionRow struct {
	CandidateRef string `json:"candidate_ref"`
	OwnerTarget  struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
	} `json:"owner_target"`
}

func readNoPromoteDecisionRows(t *testing.T, homePath string) []noPromoteDecisionRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(homePath, "ledger", "no-promote-decisions.jsonl"))
	if err != nil {
		t.Fatalf("read no-promote rows: %v", err)
	}
	var rows []noPromoteDecisionRow
	for _, line := range splitJSONLines(string(data)) {
		var row noPromoteDecisionRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse no-promote row %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func splitJSONLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasCapabilityEdge(edges []capgraph.Edge, kind, from string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from {
			return true
		}
	}
	return false
}

func hasCapabilityEdgeTo(edges []capgraph.Edge, kind, to string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.To == to {
			return true
		}
	}
	return false
}

func countCapabilityEdgeTo(edges []capgraph.Edge, kind, to string) int {
	count := 0
	for _, edge := range edges {
		if edge.Kind == kind && edge.To == to {
			count++
		}
	}
	return count
}

func initCompoundCommandGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runCommandGit(t, repo, "init", "-b", "main")
	runCommandGit(t, repo, "config", "user.email", "test@example.invalid")
	runCommandGit(t, repo, "config", "user.name", "Compound Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, repo, "add", "README.md")
	runCommandGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runCommandGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
