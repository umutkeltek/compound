package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/envelope"
	"compound/internal/eventsource"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/knowledge"
	"compound/internal/mem"
	"compound/internal/panel"
	"compound/internal/policy"
	"compound/internal/product"
	"compound/internal/projection"
	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/runtimebinding"
	"compound/internal/sourceobs"
	"compound/internal/tooltopology"
	"compound/internal/work"
	"gopkg.in/yaml.v3"
)

var cliTestNow = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func writeCLITrace(t *testing.T, homePath, id, workID string) envelope.TraceEnvelope {
	t.Helper()
	trace := envelope.TraceEnvelope{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTraceEnvelope,
			ID:            id,
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "test", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Source:          envelope.TraceSource{System: "test", SourceKind: "cli", EventClass: "observe_run"},
		Actor:           "codex",
		MutationClass:   "local_reversible",
		Summary:         "observed CLI run",
		ToolsModelsUsed: []string{},
		FilesTouched:    []string{},
		Outputs:         []string{"trace-output:cli"},
	}
	if err := trace.Validate(); err != nil {
		t.Fatal(err)
	}
	traceBytes, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(homePath, "events", trace.ID+".json")
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, append(traceBytes, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return trace
}

func containsString(set []string, v string) bool {
	for _, item := range set {
		if item == v {
			return true
		}
	}
	return false
}

func TestSchemaCLIExportAndValidate(t *testing.T) {
	listOut := captureStdout(t, func() {
		cmdSchema([]string{"list"})
	})
	if !strings.Contains(listOut, "context_pack") || !strings.Contains(listOut, "route_evidence") {
		t.Fatalf("schema list output =\n%s", listOut)
	}

	exportOut := captureStdout(t, func() {
		cmdSchema([]string{"export", "context-pack"})
	})
	if !strings.Contains(exportOut, `"compound://schemas/context_pack"`) || !strings.Contains(exportOut, `"capability_offers"`) {
		t.Fatalf("schema export output =\n%s", exportOut)
	}

	dir := t.TempDir()
	allOut := captureStdout(t, func() {
		cmdSchema([]string{"export", "--all", "--dir", dir})
	})
	if !strings.Contains(allOut, "context_pack.schema.json") {
		t.Fatalf("schema export --all output =\n%s", allOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "route_evidence.schema.json")); err != nil {
		t.Fatalf("missing exported route schema: %v", err)
	}

	path := filepath.Join(t.TempDir(), "context-pack.json")
	data, err := json.Marshal(validCLIContextPack())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	validateOut := captureStdout(t, func() {
		cmdSchema([]string{"validate", "context-pack", path})
	})
	if !strings.Contains(validateOut, "ok (context-pack)") {
		t.Fatalf("schema validate output =\n%s", validateOut)
	}
}

func validCLIContextPack() envelope.ContextPack {
	return envelope.ContextPack{
		Header: envelope.Header{
			SchemaVersion: envelope.SVContextPack,
			ID:            "ctx_cli_schema",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound", Version: "test"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		SummaryMD: "schema cli test",
		Included: []envelope.IncludedRef{{
			Ref:         "capability://skill:test",
			SourceClass: "generated",
			Reason:      "schema test",
			Confidence:  1,
			Status:      "current",
		}},
		Excluded: []envelope.ExcludedRef{},
		CapabilityOffers: []envelope.CapabilityOffer{{
			Ref:          "capability://skill:test",
			Kind:         "skill",
			Name:         "Test Skill",
			Score:        1,
			Why:          "trigger",
			CostRiskNote: "offer only; execution still requires WorkEnvelope, ActionIntent, and PolicyDecision",
		}},
		ActiveInstructions: []string{},
		ActiveDecisions:    []string{},
		RequiredChecks:     []string{},
		Avoid:              []string{},
	}
}

func writeCLIContextPackArtifact(t *testing.T, homePath, id string) {
	t.Helper()
	pack := validCLIContextPack()
	pack.ID = id
	path := filepath.Join(homePath, "context", "packs", id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir context packs: %v", err)
	}
	data, err := json.MarshalIndent(pack, "", " ")
	if err != nil {
		t.Fatalf("marshal context pack: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write context pack: %v", err)
	}
}

func TestHomeArchiveCommandsRoundTrip(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "ledger", "durable.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "tmp", "cache.txt"), []byte("drop"), 0o644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "home.tar.gz")
	out := captureStdout(t, func() {
		cmdHome([]string{"backup", "--target", archivePath})
	})
	if !strings.Contains(out, "# home backup") || !strings.Contains(out, "- archive: "+archivePath) {
		t.Fatalf("backup output:\n%s", out)
	}

	out = captureStdout(t, func() {
		cmdHome([]string{"verify", archivePath})
	})
	if !strings.Contains(out, "- verdict: ok") || !strings.Contains(out, "- kind: backup") {
		t.Fatalf("verify output:\n%s", out)
	}

	restorePath := filepath.Join(t.TempDir(), ".compound-restored")
	out = captureStdout(t, func() {
		cmdHome([]string{"restore", archivePath, "--target", restorePath})
	})
	if !strings.Contains(out, "# home restore") || !strings.Contains(out, "- verdict: ok") {
		t.Fatalf("restore output:\n%s", out)
	}
	if got := readFile(t, filepath.Join(restorePath, "ledger", "durable.json")); got != `{"ok":true}` {
		t.Fatalf("restored durable file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(restorePath, "tmp", "cache.txt")); !os.IsNotExist(err) {
		t.Fatalf("backup should not restore tmp cache, stat err=%v", err)
	}

	out = captureStdout(t, func() {
		cmdHome([]string{"compact"})
	})
	if !strings.Contains(out, "# home compact") || !strings.Contains(out, "- removed_files: 1") {
		t.Fatalf("compact output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homePath, "tmp", "cache.txt")); !os.IsNotExist(err) {
		t.Fatalf("compact should remove tmp cache, stat err=%v", err)
	}
}

func TestKnowledgeExtractCLIUsesFixtureAndWritesCandidate(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	var importReport knowledge.ImportReport
	importOut := captureStdout(t, func() {
		cmdKnowledge([]string{"import", filepath.Join("..", "..", "fixtures", "knowledge", "arxiv-export.json"), "--json"})
	})
	if err := json.Unmarshal([]byte(importOut), &importReport); err != nil {
		t.Fatalf("parse import report: %v\n%s", err, importOut)
	}
	if len(importReport.ExtractionIDs) == 0 || len(importReport.EvidenceIDs) == 0 {
		t.Fatalf("import report missing ids: %+v", importReport)
	}
	fixtureDir := t.TempDir()
	writeKnowledgeExtractionFixture(t, fixtureDir, importReport.ExtractionIDs[0], importReport.EvidenceIDs[0])

	var extractReport knowledge.ExtractReport
	extractOut := captureStdout(t, func() {
		cmdKnowledge([]string{"extract", "--item", importReport.ExtractionIDs[0], "--fixtures", fixtureDir, "--json"})
	})
	if err := json.Unmarshal([]byte(extractOut), &extractReport); err != nil {
		t.Fatalf("parse extract report: %v\n%s", err, extractOut)
	}
	if extractReport.Processed != 1 || extractReport.Created != 1 || len(extractReport.CandidateIDs) != 1 {
		t.Fatalf("extract report = %+v", extractReport)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range candidates {
		if c.ID == extractReport.CandidateIDs[0] {
			found = c.Qualifiers["source"] == "knowledge_extraction" && c.State == "proposed"
		}
	}
	if !found {
		t.Fatalf("extracted candidate not found or wrong shape: %s in %+v", extractReport.CandidateIDs[0], candidates)
	}
}

func writeKnowledgeExtractionFixture(t *testing.T, dir, extractionID, evidenceID string) {
	t.Helper()
	body := `{
 "schema_version": "compound.distiller.llm_extraction.v0.1",
 "trace_ref": "knowledge_extraction:` + extractionID + `",
 "extractor": "fixture",
 "model": "fixture-rules",
 "source_refs": [` + strconv.Quote(evidenceID) + `],
 "candidates": [
  {
   "kind": "memory",
   "entity": "paper:retrieval-agents",
   "predicate": "claim",
   "object": "Retrieval agents need grounded evidence to avoid echo amplification.",
   "confidence": 0.78,
   "derivation": "extracted",
   "claim_type": "architecture",
   "qualifiers": {"domain": "knowledge-intake"},
   "evidence_refs": [` + strconv.Quote(evidenceID) + `]
  }
 ]
}`
	if err := os.WriteFile(filepath.Join(dir, extractionID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHomeOperationalHistoryArchiveCommandsHideAndRestoreHotHistory(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "ready work remains hot"}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	done, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "terminal work archives"}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(done.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "verifying", VerificationID: "ver_cli_archive"}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(done.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "archive history CLI proof",
		Changed:   true,
		ProofRefs: []string{"proof:cli-archive"},
	}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewartifact.Write(homePath, "panel-verdicts", "pv_cli_archive", map[string]any{
		"id":     "pv_cli_archive",
		"status": "recorded",
	}, " ", "# Verdict\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewartifact.WriteJSON(homePath, "panel-verdicts", "pv_cli_archive.reviewed.json", map[string]any{
		"id":     "pvr_cli_archive",
		"status": "recorded",
	}, " "); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { cmdHome([]string{"doctor"}) })
	for _, want := range []string{
		"operational_hygiene: DIRTY",
		"hygiene:no-terminal-work-hot: DIRTY",
		"hygiene:no-stale-leases: DIRTY",
		"verdict: ok",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dirty home doctor missing %q:\n%s", want, out)
		}
	}

	out = captureStdout(t, func() {
		cmdHome([]string{"archive-history", "--kind", "work", "--kind", "lease", "--kind", "review"})
	})
	if !strings.Contains(out, "# home archive-history") || !strings.Contains(out, "- work: 1") || !strings.Contains(out, "- lease: 1") || !strings.Contains(out, "- review: 3") {
		t.Fatalf("archive-history output:\n%s", out)
	}
	out = captureStdout(t, func() { cmdHome([]string{"doctor"}) })
	for _, want := range []string{
		"operational_hygiene: ok",
		"hygiene:no-terminal-work-hot: ok",
		"hygiene:no-stale-leases: ok",
		"verdict: ok",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("swept home doctor missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "work", done.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("terminal work should move out of hot store, stat err=%v", err)
	}

	var wb workbenchCLIOutput
	out = captureStdout(t, func() { cmdWorkbench([]string{"--json", "--recent", "10", "--review-area", "panel-verdicts"}) })
	if err := json.Unmarshal([]byte(out), &wb); err != nil {
		t.Fatalf("parse default workbench: %v\n%s", err, out)
	}
	if wb.Counts.Terminal != 0 || wb.ReviewQueue == nil || wb.ReviewQueue.Counts.Total != 0 {
		t.Fatalf("default workbench should hide archived history: counts=%+v review=%+v", wb.Counts, wb.ReviewQueue)
	}

	var reviews reviewartifact.QueueReport
	out = captureStdout(t, func() { cmdReviews([]string{"--json", "--area", "panel-verdicts"}) })
	if err := json.Unmarshal([]byte(out), &reviews); err != nil {
		t.Fatalf("parse default reviews: %v\n%s", err, out)
	}
	if reviews.Counts.Total != 0 {
		t.Fatalf("default reviews should hide archived history: %+v", reviews.Counts)
	}

	out = captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--recent", "10", "--review-area", "panel-verdicts", "--include-archived"})
	})
	if err := json.Unmarshal([]byte(out), &wb); err != nil {
		t.Fatalf("parse archived workbench: %v\n%s", err, out)
	}
	if wb.Counts.Terminal != 1 || wb.ReviewQueue == nil || wb.ReviewQueue.Counts.Total != 1 {
		t.Fatalf("include-archived workbench should show history: counts=%+v review=%+v", wb.Counts, wb.ReviewQueue)
	}

	out = captureStdout(t, func() { cmdReviews([]string{"--json", "--area", "panel-verdicts", "--include-archived"}) })
	if err := json.Unmarshal([]byte(out), &reviews); err != nil {
		t.Fatalf("parse archived reviews: %v\n%s", err, out)
	}
	if reviews.Counts.Total != 1 || reviews.Items[0].ID != "pv_cli_archive" {
		t.Fatalf("include-archived reviews should show history: %+v", reviews)
	}

	out = captureStdout(t, func() { cmdHome([]string{"restore-history", "--all"}) })
	if !strings.Contains(out, "# home restore-history") || !strings.Contains(out, "- restored: 5") {
		t.Fatalf("restore-history output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "work", done.ID+".json")); err != nil {
		t.Fatalf("terminal work should restore to hot store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "reviews", "panel-verdicts", "pv_cli_archive.reviewed.json")); err != nil {
		t.Fatalf("review sidecar should restore to hot store: %v", err)
	}
}

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

func TestContractImportHooksFeedsCompile(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	source := filepath.Join("..", "..", "fixtures", "harness", "codex-hooks-json.json")
	importOut := captureStdout(t, func() {
		cmdContract([]string{"import-hooks", "--codex-hooks-json", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://config/installed-hooks"`,
		`"hook_count": 2`,
		`"authority_status": "compound_primary"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import-hooks output missing %q:\n%s", want, importOut)
		}
	}

	authorityOut := captureStdout(t, func() {
		cmdContract([]string{"hooks-authority"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"hook_count": 2`,
		`"pre_tool_use_readiness": [`,
		`"harness": "codex"`,
		`"status": "missing_gate"`,
		`"adapter_command": "compound hook pre-tool-use --enforce FIXTURE_JSON"`,
		`"source_role": "evidence_only_input_after_import"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("hooks-authority output missing %q:\n%s", want, authorityOut)
		}
	}

	cwd := t.TempDir()
	out := captureStdout(t, func() {
		cmdContract([]string{"compile", "--harness", "codex", "--cwd", cwd, "--task", "use hooks", "--repo-kind", "control-plane"})
	})
	var ec envelope.EffectiveContract
	if err := json.Unmarshal([]byte(out), &ec); err != nil {
		t.Fatalf("parse contract compile: %v\n%s", err, out)
	}
	for _, want := range []string{"hook://codex/SessionStart", "hook://codex/UserPromptSubmit"} {
		if !stringSliceContains(ec.ActiveHooks, want) {
			t.Fatalf("compiled active hooks = %v, want %s", ec.ActiveHooks, want)
		}
	}
	if !sourceRevisionExists(ec.SourceRevisions, "compound://config/installed-hooks") {
		t.Fatalf("compiled source revisions missing installed hooks authority: %+v", ec.SourceRevisions)
	}
	var latest envelope.EffectiveContract
	latestPath := filepath.Join(homePath, "contracts", "effective", "latest.json")
	latestData, err := os.ReadFile(latestPath)
	if err != nil {
		t.Fatalf("read latest effective contract: %v", err)
	}
	if err := json.Unmarshal(latestData, &latest); err != nil {
		t.Fatalf("parse latest effective contract: %v", err)
	}
	if latest.ID != ec.ID {
		t.Fatalf("latest effective contract id = %q, want %q", latest.ID, ec.ID)
	}
	createdAt, err := time.Parse(time.RFC3339, ec.CreatedAt)
	if err != nil {
		t.Fatalf("parse contract created_at %q: %v", ec.CreatedAt, err)
	}
	datedPath := filepath.Join(homePath, "contracts", "effective", createdAt.UTC().Format("2006-01-02"), ec.ID+".json")
	if _, err := os.Stat(datedPath); err != nil {
		t.Fatalf("dated effective contract artifact missing: %v", err)
	}

	orientOut := captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", cwd, "--task", "use hooks", "--repo-kind", "control-plane"})
	})
	var pack envelope.ContextPack
	if err := json.Unmarshal([]byte(orientOut), &pack); err != nil {
		t.Fatalf("parse orient pack: %v\n%s", err, orientOut)
	}
	if pack.IDs.ContractID != ec.ID {
		t.Fatalf("orient contract_id = %q, want %q", pack.IDs.ContractID, ec.ID)
	}
	packPath := filepath.Join(homePath, "context", "packs", pack.ID+".json")
	packData, err := os.ReadFile(packPath)
	if err != nil {
		t.Fatalf("read orient context pack artifact: %v", err)
	}
	var storedPack envelope.ContextPack
	if err := json.Unmarshal(packData, &storedPack); err != nil {
		t.Fatalf("parse orient context pack artifact: %v", err)
	}
	if storedPack.ID != pack.ID || storedPack.IDs.ContractID != ec.ID {
		t.Fatalf("stored orient pack id/contract = %q/%q, want %q/%q", storedPack.ID, storedPack.IDs.ContractID, pack.ID, ec.ID)
	}
	orientOnlyOut := captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--cwd", cwd, "--task", "orient persists contract without precompile", "--repo-kind", "control-plane"})
	})
	var orientOnlyPack envelope.ContextPack
	if err := json.Unmarshal([]byte(orientOnlyOut), &orientOnlyPack); err != nil {
		t.Fatalf("parse orient-only pack: %v\n%s", err, orientOnlyOut)
	}
	if orientOnlyPack.IDs.ContractID == "" {
		t.Fatal("orient-only pack missing contract_id")
	}
	orientCreatedAt, err := time.Parse(time.RFC3339, orientOnlyPack.CreatedAt)
	if err != nil {
		t.Fatalf("parse orient-only created_at %q: %v", orientOnlyPack.CreatedAt, err)
	}
	orientContractPath := filepath.Join(homePath, "contracts", "effective", orientCreatedAt.UTC().Format("2006-01-02"), orientOnlyPack.IDs.ContractID+".json")
	orientContractData, err := os.ReadFile(orientContractPath)
	if err != nil {
		t.Fatalf("read orient-only effective contract artifact: %v", err)
	}
	var orientContract envelope.EffectiveContract
	if err := json.Unmarshal(orientContractData, &orientContract); err != nil {
		t.Fatalf("parse orient-only effective contract artifact: %v", err)
	}
	if orientContract.ID != orientOnlyPack.IDs.ContractID {
		t.Fatalf("orient-only effective contract id = %q, want %q", orientContract.ID, orientOnlyPack.IDs.ContractID)
	}
}

func TestContractImportHookRegistrationFeedsCompile(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	source := filepath.Join("..", "..", "fixtures", "harness", "hook-registration.ts")
	importOut := captureStdout(t, func() {
		cmdContract([]string{"import-hooks", "--hook-registration-ts", source})
	})
	captureStdout(t, func() {
		cmdContract([]string{"import-hooks", "--hook-registration-ts", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://config/installed-hooks"`,
		`"hook_count": 5`,
		`"imported_via": "contract import-hooks --hook-registration-ts"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import-hooks output missing %q:\n%s", want, importOut)
		}
	}
	authorityOut := captureStdout(t, func() {
		cmdContract([]string{"hooks-authority"})
	})
	for _, want := range []string{
		`"harnesses": [`,
		`"claude_code"`,
		`"codex"`,
		`"hook_count": 5`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("hooks-authority output missing %q:\n%s", want, authorityOut)
		}
	}
	var authorityReport struct {
		SourceRoleCounts            map[string]int                           `json:"source_role_counts"`
		RetiredSourceAuthorityCount int                                      `json:"retired_source_authority_count"`
		RetiredSourceAuthorityRefs  []string                                 `json:"retired_source_authority_refs"`
		SourceAuthority             []contract.InstalledHooksSourceAuthority `json:"source_authority"`
	}
	if err := json.Unmarshal([]byte(authorityOut), &authorityReport); err != nil {
		t.Fatalf("parse hooks-authority: %v\n%s", err, authorityOut)
	}
	if len(authorityReport.SourceAuthority) != 1 ||
		authorityReport.SourceRoleCounts["evidence_only_input_after_import"] != 1 ||
		authorityReport.RetiredSourceAuthorityCount != 1 ||
		!stringSliceContains(authorityReport.RetiredSourceAuthorityRefs, "source_view_authority:"+source) {
		t.Fatalf("unexpected hooks authority source summary: %+v", authorityReport)
	}
	out := captureStdout(t, func() {
		cmdContract([]string{"compile", "--harness", "claude_code", "--cwd", t.TempDir(), "--task", "use hooks", "--repo-kind", "control-plane"})
	})
	var ec envelope.EffectiveContract
	if err := json.Unmarshal([]byte(out), &ec); err != nil {
		t.Fatalf("parse contract compile: %v\n%s", err, out)
	}
	for _, want := range []string{"hook://claude_code/SessionStart", "hook://claude_code/UserPromptSubmit", "hook://claude_code/PreToolUse"} {
		if !stringSliceContains(ec.ActiveHooks, want) {
			t.Fatalf("compiled active hooks = %v, want %s", ec.ActiveHooks, want)
		}
	}
}

func TestWorkImportPacketCreatesWorkEnvelopeWithoutClaims(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	out := captureStdout(t, func() {
		cmdWork([]string{"import-packet", packetPath})
	})
	if !strings.Contains(out, "created=true") || !strings.Contains(out, "Compound Goal Packet: Capability Intake") {
		t.Fatalf("import-packet output missing proof:\n%s", out)
	}
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("import-packet output missing JSON:\n%s", out)
	}
	var report work.PacketImportReport
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse import-packet report: %v\n%s", err, out)
	}
	if report.Work.State != "proposed" || report.Work.Owner != "compound-core" {
		t.Fatalf("work state/owner = %s/%s", report.Work.State, report.Work.Owner)
	}
	if report.Work.Authority.SourceClass != "source" || report.Work.Uncertainty.Level != "inferred" {
		t.Fatalf("work authority/uncertainty = %+v / %+v", report.Work.Authority, report.Work.Uncertainty)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("packet import must not create claims ledger, stat err=%v", err)
	}
	out = captureStdout(t, func() {
		cmdWork([]string{"import-packet", packetPath})
	})
	if !strings.Contains(out, "created=false") {
		t.Fatalf("reimport should be idempotent:\n%s", out)
	}
}

func TestWorkCreateCLIWiresEnvelopeFields(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	writeCLIContextPackArtifact(t, homePath, "ctx_cli_create")

	out := captureStdout(t, func() {
		cmdWork([]string{
			"create",
			"--work-id", "work_cli_planned",
			"--owner", "compound-core",
			"--predicate", "continuation envelope preserves fields",
			"--outcome", "continue adaptive capability OS work",
			"--non-goal", "bulk clone,install",
			"--non-goal", "serve marketplace skills",
			"--evidence-ref", "compound://reviews/external-repo-intake",
			"--evidence", "brain://session-state",
			"--context-pack", "ctx_cli_create",
			"--state", "ready",
			"--risk", "tier2",
			"--privacy", "sensitive",
		})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("create output missing JSON:\n%s", out)
	}
	var w envelope.WorkEnvelope
	if err := json.Unmarshal([]byte(out[start:]), &w); err != nil {
		t.Fatalf("parse create work: %v\n%s", err, out)
	}
	if w.Owner != "compound-core" || w.State != "ready" || w.DesiredOutcome != "continue adaptive capability OS work" {
		t.Fatalf("work core fields not wired: %+v", w)
	}
	if w.ID != "work_cli_planned" {
		t.Fatalf("work id = %q, want explicit planned id", w.ID)
	}
	if w.Privacy != "sensitive" || w.Risk != "tier2" || w.IDs.ContextPackID != "ctx_cli_create" {
		t.Fatalf("header fields not wired: privacy=%q risk=%q context=%q", w.Privacy, w.Risk, w.IDs.ContextPackID)
	}
	wantNonGoals := []string{"bulk clone", "install", "serve marketplace skills"}
	if !sameStringSlice(w.NonGoals, wantNonGoals) {
		t.Fatalf("non_goals = %v, want %v", w.NonGoals, wantNonGoals)
	}
	wantEvidence := []string{"compound://reviews/external-repo-intake", "brain://session-state"}
	if !sameStringSlice(w.EvidenceRefs, wantEvidence) {
		t.Fatalf("evidence_refs = %v, want %v", w.EvidenceRefs, wantEvidence)
	}
}

func TestWorkCreatePlanCLIRecordsPolicyWithoutCreating(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{
			"create-plan",
			"--owner", "compound-core",
			"--by", "codex",
			"--predicate", "cli create plan stays proposal-only",
			"--outcome", "show create gate",
			"--context-pack", "ctx_cli_create_plan",
			"--json",
		})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("create-plan output missing JSON:\n%s", out)
	}
	var plan work.CreatePlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse create plan: %v\n%s", err, out)
	}
	if plan.Status != "create_policy_allowed" || !plan.Policy.Allowed || plan.PlanPath == "" {
		t.Fatalf("create plan not allowed/recorded: %+v", plan)
	}
	if !stringSliceContains(plan.CreateCommand, "--work-id") || !stringSliceContains(plan.CreateCommand, plan.WorkID) {
		t.Fatalf("create plan command must preserve planned work id %s: %v", plan.WorkID, plan.CreateCommand)
	}
	if _, err := os.Stat(plan.PlanPath); err != nil {
		t.Fatalf("create plan artifact missing: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	if _, err := store.Get(plan.WorkID); err == nil {
		t.Fatalf("create-plan must not write work %s", plan.WorkID)
	}
}

func TestWorkImportPacketReadyClosesThroughReceiptTransition(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	writeCLIContextPackArtifact(t, homePath, "ctx_packet_cli")
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	out := captureStdout(t, func() {
		cmdWork([]string{"import-packet", packetPath, "--state", "ready", "--context-pack", "ctx_packet_cli"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("import-packet output missing JSON:\n%s", out)
	}
	var report work.PacketImportReport
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse import-packet report: %v\n%s", err, out)
	}
	workID := report.Work.ID
	if report.Work.State != "ready" {
		t.Fatalf("packet work state = %q, want ready", report.Work.State)
	}
	captureStdout(t, func() {
		cmdWork([]string{"claim", workID, "--by", "codex"})
	})
	captureStdout(t, func() {
		cmdWork([]string{"advance", workID, "--to", "running", "--run", "run_packet_cli"})
	})
	captureStdout(t, func() {
		cmdWork([]string{"advance", workID, "--to", "verifying", "--artifact", "artifact_packet_cli", "--verification", "ver_packet_cli"})
	})
	out = captureStdout(t, func() {
		cmdWork([]string{"close", workID, "--outcome", "completed", "--move-type", "control_plane_repair", "--summary", "packet CLI work closed through receipt and transition", "--changed"})
	})
	start = strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("close output missing JSON:\n%s", out)
	}
	var closed work.CloseResult
	if err := json.Unmarshal([]byte(out[start:]), &closed); err != nil {
		t.Fatalf("parse close result: %v\n%s", err, out)
	}
	if closed.Work.ID != workID || closed.Receipt.IDs.WorkID != workID || closed.Transition.IDs.WorkID != workID {
		t.Fatalf("work id chain broken: work=%s receipt=%s transition=%s", closed.Work.ID, closed.Receipt.IDs.WorkID, closed.Transition.IDs.WorkID)
	}
	if closed.Work.IDs.TransitionID != closed.Transition.ID {
		t.Fatalf("work transition id = %q, want %q", closed.Work.IDs.TransitionID, closed.Transition.ID)
	}
	if !stringSliceContains(closed.Transition.ActionRefs, "run_packet_cli") ||
		!stringSliceContains(closed.Transition.AfterRefs, "artifact_packet_cli") ||
		!stringSliceContains(closed.Transition.AfterRefs, closed.Receipt.ID) {
		t.Fatalf("transition refs incomplete: action=%v after=%v receipt=%s", closed.Transition.ActionRefs, closed.Transition.AfterRefs, closed.Receipt.ID)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("packet CLI loop must not create claims ledger, stat err=%v", err)
	}
}

func TestWorkbenchCommandShowsOpenAndTerminalProof(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "ready workbench proof"}, cliTestNow)
	if err != nil {
		t.Fatalf("create ready: %v", err)
	}
	done, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "closed workbench proof"}, cliTestNow)
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	if _, err := store.Claim(done.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim done: %v", err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "verifying", ArtifactID: "artifact_workbench", VerificationID: "ver_workbench"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	closed, err := store.Close(done.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "workbench CLI terminal proof",
		Changed:   true,
		ProofRefs: []string{"proof:workbench-cli"},
	}, cliTestNow)
	if err != nil {
		t.Fatalf("close done: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--recent", "10"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("workbench output missing JSON:\n%s", out)
	}
	var report work.WorkbenchReport
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse workbench report: %v\n%s", err, out)
	}
	if report.Counts.Open != 1 || report.Counts.Terminal != 1 || report.Counts.NeedsClaim != 1 {
		t.Fatalf("counts = %+v", report.Counts)
	}
	if len(report.Open) != 1 || report.Open[0].WorkID != ready.ID || report.Open[0].NextAction != "claim before touching" {
		t.Fatalf("open item = %+v", report.Open)
	}
	if !report.DispatchNext.CanRun || report.DispatchNext.Action != "claim" || report.DispatchNext.WorkID != ready.ID {
		t.Fatalf("dispatch next = %+v", report.DispatchNext)
	}
	if len(report.Recent) != 1 || report.Recent[0].ReceiptID != closed.Receipt.ID || report.Recent[0].TransitionID != closed.Transition.ID {
		t.Fatalf("recent item = %+v", report.Recent)
	}
}

func TestWorkbenchCommandCanAttachCapabilitySurface(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml")})
	})

	out := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--capability-task", "pricing"})
	})
	var report workbenchCLIOutput
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("workbench JSON did not parse: %v\n%s", err, out)
	}
	if report.CapabilitySurface == nil {
		t.Fatalf("capability surface missing: %+v", report)
	}
	if report.Counts.Total != 0 || report.DispatchNext.WaitReason != "no_open_work" {
		t.Fatalf("workbench state changed unexpectedly: counts=%+v dispatch=%+v", report.Counts, report.DispatchNext)
	}
	surface := report.CapabilitySurface
	if surface.SchemaVersion != capgraph.SurfaceSchemaVersion ||
		surface.AuthorityRef != capgraph.CapabilityGraphAuthorityRef ||
		surface.Status != "ok" ||
		surface.Task != "pricing" ||
		surface.Harness != "codex" ||
		surface.OfferCount != len(surface.Offers) ||
		len(surface.OfferSummaries) != len(surface.Offers) {
		t.Fatalf("unexpected capability surface: %+v", surface)
	}
	var ids []string
	for _, offer := range surface.OfferSummaries {
		ids = append(ids, offer.ID)
	}
	if !stringSliceContains(ids, "skill:codex-pricing") || !stringSliceContains(ids, "skill:generic-pricing") {
		t.Fatalf("codex/generic offers missing: %+v", ids)
	}
	if stringSliceContains(ids, "skill:claude-pricing") {
		t.Fatalf("claude-only offer leaked into codex workbench surface: %+v", ids)
	}

	human := captureStdout(t, func() {
		cmdWorkbench([]string{"--capability-task", "pricing"})
	})
	for _, want := range []string{
		`capability_surface status=ok offers=2 showing=2 omitted=0 task="pricing" harness="codex" authority=compound://registries/capability-graph`,
		"capability_offer: skill skill:codex-pricing",
		"capability_offer: skill skill:generic-pricing",
		"open: none",
	} {
		if !strings.Contains(human, want) {
			t.Fatalf("human workbench output missing %q:\n%s", want, human)
		}
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("workbench capability surface must not promote claims, stat err=%v", err)
	}
}

func TestWorkbenchCapabilitySurfaceIsBounded(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capgraph: %v", err)
	}
	for i := 0; i < workbenchCapabilityOfferLimit+3; i++ {
		id := "skill:pricing-" + strconv.Itoa(i)
		err := store.Add(capgraph.Node{
			ID:       id,
			Kind:     "skill",
			Name:     "Pricing " + strconv.Itoa(i),
			Triggers: []string{"pricing"},
			Weight:   float64(workbenchCapabilityOfferLimit+3-i) / 10,
		})
		if err != nil {
			t.Fatalf("add node %s: %v", id, err)
		}
	}

	out := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--capability-task", "pricing"})
	})
	var report workbenchCLIOutput
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("workbench JSON did not parse: %v\n%s", err, out)
	}
	surface := report.CapabilitySurface
	if surface == nil {
		t.Fatalf("capability surface missing: %+v", report)
	}
	if surface.OfferCount != workbenchCapabilityOfferLimit+3 || len(surface.Offers) != workbenchCapabilityOfferLimit || len(surface.OfferSummaries) != workbenchCapabilityOfferLimit {
		t.Fatalf("surface should preserve full count but bound embedded offers: %+v", surface)
	}
	if surface.OfferLimit != workbenchCapabilityOfferLimit || surface.OmittedOfferCount != 3 {
		t.Fatalf("surface limit metadata = limit %d omitted %d", surface.OfferLimit, surface.OmittedOfferCount)
	}
	if surface.OfferSummaries[0].ID != "skill:pricing-0" {
		t.Fatalf("bounded surface lost ranking order: %+v", surface.OfferSummaries[:3])
	}

	out = captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--capability-task", "pricing", "--capability-limit", "3"})
	})
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("workbench JSON with explicit limit did not parse: %v\n%s", err, out)
	}
	surface = report.CapabilitySurface
	if surface == nil || surface.OfferLimit != 3 || len(surface.Offers) != 3 || surface.OmittedOfferCount != workbenchCapabilityOfferLimit {
		t.Fatalf("explicit limit surface = %+v", surface)
	}
}

func TestCapabilityHelpDoesNotConsumeReceipts(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capgraph: %v", err)
	}
	if err := store.Add(capgraph.Node{ID: "skill:pricing", Kind: "skill", Name: "Pricing", Triggers: []string{"pricing"}}); err != nil {
		t.Fatal(err)
	}
	rec := envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            "rec_help_guard",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "test", Name: "compound"},
			IDs:           envelope.IDs{WorkID: "work_help_guard"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:  "completed",
		MoveType: "outward_delta",
		StateDelta: envelope.StateDelta{
			Changed:      true,
			Summary:      "help guard receipt",
			FilesChanged: []string{},
			Artifacts:    []string{},
			Verification: []string{},
		},
		Cost:   envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{Used: []string{"capability://skill:pricing"}, Missed: []string{}, Candidate: []string{}},
		Memory: envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:   envelope.ReceiptNext{SuggestedWork: []string{}},
	}
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homePath, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(rec, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "events", rec.ID+".json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { cmdCapability([]string{"consume-receipts", "--help"}) })
	if !strings.Contains(out, "usage: compound capability") || strings.Contains(out, "receipts_scanned") {
		t.Fatalf("help output should be usage only, got:\n%s", out)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.EdgeCount != 0 {
		t.Fatalf("--help consumed receipts and mutated graph: %+v", stats)
	}
}

func TestCommandHelpShortCircuitsBeforeRuntimeValidation(t *testing.T) {
	t.Setenv("COMPOUND_HOME", filepath.Join(t.TempDir(), "missing-home"))
	tests := []struct {
		name string
		run  func()
		want string
	}{
		{
			name: "work create",
			run:  func() { cmdWork([]string{"create", "--help"}) },
			want: "usage: compound work create",
		},
		{
			name: "work import-packet",
			run:  func() { cmdWork([]string{"import-packet", "--help"}) },
			want: "usage: compound work import-packet",
		},
		{
			name: "memory candidate",
			run:  func() { cmdMemory([]string{"candidate", "--help"}) },
			want: "usage: compound memory candidate",
		},
		{
			name: "memory adjudicate",
			run:  func() { cmdMemory([]string{"adjudicate", "--help"}) },
			want: "usage: compound memory adjudicate",
		},
		{
			name: "route cost-doctor",
			run:  func() { cmdRoute([]string{"cost-doctor", "--help"}) },
			want: "usage: compound route cost-doctor",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, tt.run)
			if !strings.Contains(out, tt.want) {
				t.Fatalf("help output missing %q:\n%s", tt.want, out)
			}
		})
	}
}

func TestWorkbenchCommandIncludesReviewQueueSummary(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "product-reviews", "prp_workbench", map[string]any{
		"id":            "prp_workbench",
		"status":        "review_required",
		"product_id":    "product:focus-notes",
		"hypothesis_id": "hyp_focus",
		"signal_id":     "sig_focus",
	}, " ", "# Product review\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewartifact.WriteRaw(homePath, "product-reviews", "prp_workbench.review.yaml", []byte("schema_version: compound.product_review.v0.1\n")); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--review-area", "product-reviews"})
	})
	var report workbenchCLIOutput
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse workbench review queue JSON: %v\n%s", err, out)
	}
	if report.ReviewQueue == nil ||
		report.ReviewQueue.SchemaVersion != reviewartifact.QueueSchemaVersion ||
		report.ReviewQueue.AuthorityRef != "compound://reviews" ||
		report.ReviewQueue.Area != "product-reviews" ||
		report.ReviewQueue.Counts.Total != 1 ||
		report.ReviewQueue.Counts.ReviewRequired != 1 ||
		len(report.ReviewQueue.Actionable) != 1 ||
		report.ReviewQueue.Actionable[0].ID != "prp_workbench" ||
		report.ReviewQueue.Actionable[0].Readiness != "needs_review" ||
		len(report.ReviewQueue.Actionable[0].Actions) != 1 {
		t.Fatalf("bad workbench review queue: %+v", report.ReviewQueue)
	}

	human := captureStdout(t, func() {
		cmdWorkbench([]string{"--review-area", "product-reviews"})
	})
	for _, want := range []string{
		"review_queue total=1 review_required=1 ready_to_apply=0 ok=0 authority=compound://reviews",
		"review_action: product-reviews prp_workbench",
		"open: none",
	} {
		if !strings.Contains(human, want) {
			t.Fatalf("human workbench review queue output missing %q:\n%s", want, human)
		}
	}
}

func TestWorkbenchCommandShowsDispatchStepRuns(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	if _, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "workbench command sees dispatch run"}, cliTestNow); err != nil {
		t.Fatalf("create work: %v", err)
	}
	run, err := store.DispatchStepRun(cliTestNow, work.DispatchStepRunOptions{
		DispatchStepOptions: work.DispatchStepOptions{Actor: "codex"},
		MaxSteps:            5,
	})
	if err != nil {
		t.Fatalf("dispatch step run: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWorkbench([]string{"--recent", "10"})
	})
	if !strings.Contains(out, "dispatch_step_runs=1") || !strings.Contains(out, "dispatch_wait: run_evidence") || !strings.Contains(out, "wait=run_evidence") || !strings.Contains(out, run.ID) {
		t.Fatalf("workbench output missing dispatch run:\n%s", out)
	}
}

func TestWorkbenchCommandDispatchNextUsesActor(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	created, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "actor claim workbench"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	claimed, err := store.Claim(created.ID, "claude", cliTestNow)
	if err != nil {
		t.Fatalf("claim work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--recent", "10", "--actor", "claude"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("workbench output missing JSON:\n%s", out)
	}
	var report work.WorkbenchReport
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse workbench report: %v\n%s", err, out)
	}
	if !report.DispatchNext.CanRun || report.DispatchNext.Action != "advance" || report.DispatchNext.WorkID != claimed.ID {
		t.Fatalf("dispatch next = %+v", report.DispatchNext)
	}
}

func TestWorkActivePacketProofCommand(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	writeCLIContextPackArtifact(t, homePath, "ctx_packet")
	imported, err := store.ImportPacket(filepath.Join("..", "..", "fixtures", "work", "goal-packet.md"), work.PacketImportOptions{
		Owner:         "compound-core",
		State:         "ready",
		ContextPackID: "ctx_packet",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
	if _, err := store.Claim(imported.Work.ID, "codex", cliTestNow.Add(time.Second)); err != nil {
		t.Fatalf("claim packet: %v", err)
	}
	if _, err := store.Advance(imported.Work.ID, work.AdvanceInput{To: "running", RunID: "run_packet"}, cliTestNow.Add(2*time.Second)); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(imported.Work.ID, work.AdvanceInput{To: "verifying", VerificationID: "packet-work-contract:active-packet-lifecycle"}, cliTestNow.Add(3*time.Second)); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	closed, err := store.Close(imported.Work.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "active packet proof command",
		Changed:   true,
		ProofRefs: []string{"packet-work-contract:active-packet-lifecycle"},
	}, cliTestNow.Add(4*time.Second))
	if err != nil {
		t.Fatalf("close packet: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"active-packet-proof", "--surface", imported.Work.CanonicalSurface, "--json"})
	})
	var report work.ActivePacketProofReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse active packet proof: %v\n%s", err, out)
	}
	if report.Status != "ok" || report.WorkID != imported.Work.ID || report.ReceiptID != closed.Receipt.ID || report.TransitionID != closed.Transition.ID {
		t.Fatalf("active packet proof report = %+v", report)
	}
	if !report.Checks["work_completed"] || !report.Checks["receipt_work_matches"] || !report.Checks["transition_work_matches"] {
		t.Fatalf("active packet proof checks = %+v", report.Checks)
	}

	workbenchOut := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--recent", "10", "--active-packet-surface", imported.Work.CanonicalSurface})
	})
	var workbenchReport work.WorkbenchReport
	if err := json.Unmarshal([]byte(workbenchOut), &workbenchReport); err != nil {
		t.Fatalf("parse workbench report: %v\n%s", err, workbenchOut)
	}
	if workbenchReport.ActivePacketProof == nil ||
		workbenchReport.ActivePacketProof.Status != "ok" ||
		workbenchReport.ActivePacketProof.WorkID != imported.Work.ID ||
		workbenchReport.ActivePacketProof.ReceiptID != closed.Receipt.ID ||
		workbenchReport.ActivePacketProof.TransitionID != closed.Transition.ID {
		t.Fatalf("workbench active packet proof = %+v", workbenchReport.ActivePacketProof)
	}
	if workbenchReport.DispatchNext.CanRun ||
		workbenchReport.DispatchNext.WaitReason != "source_packet_terminal" ||
		workbenchReport.DispatchNext.WorkID != imported.Work.ID {
		t.Fatalf("workbench dispatch next = %+v", workbenchReport.DispatchNext)
	}

	humanOut := captureStdout(t, func() {
		cmdWorkbench([]string{"--recent", "10", "--active-packet-surface", imported.Work.CanonicalSurface})
	})
	if !strings.Contains(humanOut, "active_packet_proof status=ok") ||
		!strings.Contains(humanOut, "dispatch_wait: source_packet_terminal work="+imported.Work.ID) {
		t.Fatalf("workbench human output missing active packet terminal proof:\n%s", humanOut)
	}
}

func TestWorkActivePacketProofDetectsChangedSourcePacket(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	writeCLIContextPackArtifact(t, homePath, "ctx_packet")
	imported, err := store.ImportPacket(packetPath, work.PacketImportOptions{
		Owner:         "compound-core",
		State:         "ready",
		ContextPackID: "ctx_packet",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
	if _, err := store.Claim(imported.Work.ID, "codex", cliTestNow.Add(time.Second)); err != nil {
		t.Fatalf("claim packet: %v", err)
	}
	if _, err := store.Advance(imported.Work.ID, work.AdvanceInput{To: "running", RunID: "run_packet"}, cliTestNow.Add(2*time.Second)); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(imported.Work.ID, work.AdvanceInput{To: "verifying", VerificationID: "packet-work-contract:active-packet-lifecycle"}, cliTestNow.Add(3*time.Second)); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	if _, err := store.Close(imported.Work.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "active packet proof command",
		Changed:   true,
		ProofRefs: []string{"packet-work-contract:active-packet-lifecycle"},
	}, cliTestNow.Add(4*time.Second)); err != nil {
		t.Fatalf("close packet: %v", err)
	}

	data, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatalf("read packet fixture: %v", err)
	}
	changedPacket := filepath.Join(t.TempDir(), "goal-packet.md")
	if err := os.WriteFile(changedPacket, append(data, []byte("\nFreshness proof line.\n")...), 0o644); err != nil {
		t.Fatalf("write changed packet: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"active-packet-proof", "--source-packet", changedPacket, "--json"})
	})
	var report work.ActivePacketProofReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse active packet proof: %v\n%s", err, out)
	}
	if report.Status != "risk" || report.WorkID != imported.Work.ID || report.Checks["source_packet_matches"] {
		t.Fatalf("active packet proof should flag changed source packet, got %+v", report)
	}
	if report.ExpectedPacketEvidenceRef == "" || containsString(report.PacketEvidenceRefs, report.ExpectedPacketEvidenceRef) {
		t.Fatalf("expected packet evidence not recorded as mismatch: %+v", report)
	}

	workbenchOut := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--recent", "10", "--source-packet", changedPacket})
	})
	var workbenchReport work.WorkbenchReport
	if err := json.Unmarshal([]byte(workbenchOut), &workbenchReport); err != nil {
		t.Fatalf("parse workbench report: %v\n%s", err, workbenchOut)
	}
	if workbenchReport.ActivePacketProof == nil ||
		workbenchReport.ActivePacketProof.Status != "risk" ||
		workbenchReport.ActivePacketProof.Checks["source_packet_matches"] ||
		workbenchReport.DispatchNext.WaitReason != "active_packet_proof_risk" {
		t.Fatalf("workbench should surface changed source packet risk: proof=%+v dispatch=%+v", workbenchReport.ActivePacketProof, workbenchReport.DispatchNext)
	}
}

func TestWorkDispatchPlanCommandRecordsProposalWithoutClaiming(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch plan selects this work"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-plan", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-plan output missing JSON:\n%s", out)
	}
	var plan work.DispatchPlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse dispatch plan: %v\n%s", err, out)
	}
	if plan.Status != "ready_to_claim" || plan.Selected == nil || plan.Selected.WorkID != ready.ID {
		t.Fatalf("dispatch plan = %+v, ready=%s", plan, ready.ID)
	}
	if plan.Selected.ClaimPolicy == nil || !plan.Selected.ClaimPolicy.Allowed {
		t.Fatalf("dispatch plan selected item missing allowed claim policy: %+v", plan.Selected)
	}
	if plan.PlanPath == "" {
		t.Fatalf("dispatch plan did not record runtime artifact: %+v", plan)
	}
	if _, err := os.Stat(plan.PlanPath); err != nil {
		t.Fatalf("dispatch plan artifact missing: %v", err)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get ready work: %v", err)
	}
	if after.State != "ready" {
		t.Fatalf("dispatch plan must not claim work, state=%s", after.State)
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "leases", ready.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("dispatch plan must not create lease, stat err=%v", err)
	}
}

func TestWorkDispatchClaimCommandClaimsSelectedWork(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch claim selects work"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-claim", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-claim output missing JSON:\n%s", out)
	}
	var result work.DispatchClaimResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch claim: %v\n%s", err, out)
	}
	if result.Status != "claimed" || result.ClaimedWorkID != ready.ID || result.PolicyDecisionID == "" || result.ActionIntentID == "" {
		t.Fatalf("dispatch claim result = %+v, ready=%s", result, ready.ID)
	}
	if result.DispatchPlanPath == "" || result.ResultPath == "" {
		t.Fatalf("dispatch claim did not record plan/result: %+v", result)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get claimed work: %v", err)
	}
	if after.State != "claimed" || after.Authority.Owner != "codex" || after.PolicyDecisionID != result.PolicyDecisionID {
		t.Fatalf("dispatch claim did not claim through policy path: work=%+v result=%+v", after, result)
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "leases", ready.ID+".json")); err != nil {
		t.Fatalf("dispatch claim must create lease, stat err=%v", err)
	}
	if after.IDs.TransitionID != "" {
		t.Fatalf("dispatch claim must not close work: %+v", after.IDs)
	}
}

func TestWorkDispatchAdvanceCommandStartsClaimedWork(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch advance starts work"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(ready.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-advance", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-advance output missing JSON:\n%s", out)
	}
	var result work.DispatchAdvanceResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch advance: %v\n%s", err, out)
	}
	if result.Status != "running" || result.AdvancedWorkID != ready.ID || result.PolicyDecisionID == "" || result.ActionIntentID == "" {
		t.Fatalf("dispatch advance result = %+v, ready=%s", result, ready.ID)
	}
	if result.ResultPath == "" {
		t.Fatalf("dispatch advance did not record result: %+v", result)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get advanced work: %v", err)
	}
	if after.State != "running" || after.PolicyDecisionID != result.PolicyDecisionID {
		t.Fatalf("dispatch advance did not use policy path: work=%+v result=%+v", after, result)
	}
	if after.IDs.TransitionID != "" {
		t.Fatalf("dispatch advance must not close work: %+v", after.IDs)
	}
}

func TestWorkDispatchClosePlanCommandRecordsClosePlanWithoutClosing(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch close-plan command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(ready.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	running, err := store.Advance(ready.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow)
	if err != nil {
		t.Fatalf("advance running: %v", err)
	}
	trace := writeCLITrace(t, homePath, "trace_cli_dispatch_close_plan", running.ID)
	if _, err := store.ObserveRun(running.ID, work.ObserveRunInput{Actor: "codex", TraceID: trace.ID}, cliTestNow); err != nil {
		t.Fatalf("observe run: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-close-plan", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-close-plan output missing JSON:\n%s", out)
	}
	var result work.DispatchClosePlanResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch close-plan result: %v\n%s", err, out)
	}
	if result.Status != "close_policy_allowed" || result.Selected == nil || result.Selected.WorkID != ready.ID {
		t.Fatalf("dispatch close-plan result = %+v, ready=%s", result, ready.ID)
	}
	if result.ClosePlan == nil || result.ClosePlanPath == "" || result.ResultPath == "" || !containsString(result.ClosePlan.ProofRefs, trace.ID) {
		t.Fatalf("dispatch close-plan missing recorded proof plan: %+v", result)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get close-planned work: %v", err)
	}
	if after.State != "verifying" || after.IDs.TransitionID != "" {
		t.Fatalf("dispatch close-plan must not close work: %+v", after)
	}
}

func TestWorkDispatchCloseCommandClosesProofReadyWork(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch close command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(ready.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	running, err := store.Advance(ready.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow)
	if err != nil {
		t.Fatalf("advance running: %v", err)
	}
	trace := writeCLITrace(t, homePath, "trace_cli_dispatch_close", running.ID)
	if _, err := store.ObserveRun(running.ID, work.ObserveRunInput{Actor: "codex", TraceID: trace.ID}, cliTestNow); err != nil {
		t.Fatalf("observe run: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-close", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-close output missing JSON:\n%s", out)
	}
	var result work.DispatchCloseResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch close result: %v\n%s", err, out)
	}
	if result.Status != "closed" || result.ClosedWorkID != ready.ID || result.ReceiptID == "" || result.TransitionID == "" {
		t.Fatalf("dispatch close result = %+v, ready=%s", result, ready.ID)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get closed work: %v", err)
	}
	if after.State != "completed" || after.IDs.TransitionID != result.TransitionID {
		t.Fatalf("dispatch close did not terminalize work: %+v", after)
	}
}

func TestWorkDispatchStepCommandClaimsReadyWork(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch step command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-step", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-step output missing JSON:\n%s", out)
	}
	var result work.DispatchStepResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch step result: %v\n%s", err, out)
	}
	if result.Status != "claimed" || result.Action != "claim" || result.WorkID != ready.ID {
		t.Fatalf("dispatch step result = %+v, ready=%s", result, ready.ID)
	}
	if result.NextAction != "advance running or release to ready" || result.PostCounts == nil || result.PostCounts.InProgress != 1 {
		t.Fatalf("dispatch step JSON missing post-state: %+v", result)
	}
	if !result.CanContinue || len(result.ContinueCommand) != 5 || result.ContinueCommand[2] != "dispatch-step" || result.ContinueCommand[4] != "codex" {
		t.Fatalf("dispatch step JSON missing continuation: %+v", result)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get claimed work: %v", err)
	}
	if after.State != "claimed" || after.Authority.Owner != "codex" {
		t.Fatalf("dispatch step did not claim ready work: %+v", after)
	}
}

func TestWorkDispatchStepCommandImportsSourcePacket(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-step", "--actor", "codex", "--source-packet", packetPath, "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-step output missing JSON:\n%s", out)
	}
	var result work.DispatchStepResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch step result: %v\n%s", err, out)
	}
	if result.Status != "claimed" || result.Action != "claim" || result.DispatchClaim == nil {
		t.Fatalf("dispatch step result = %+v", result)
	}
	source := result.DispatchClaim.Plan.Source
	if source == nil || source.Kind != "packet" || !source.Created || result.WorkID != source.WorkID {
		t.Fatalf("dispatch step source = %+v result=%+v", source, result)
	}
	claimed, err := store.Get(source.WorkID)
	if err != nil {
		t.Fatalf("get packet work: %v", err)
	}
	if claimed.State != "claimed" || claimed.Authority.Owner != "codex" {
		t.Fatalf("dispatch step did not claim packet work: %+v", claimed)
	}
}

func TestWorkDispatchStepCommandReportsTerminalSourcePacket(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	imported, err := store.ImportPacket(packetPath, work.PacketImportOptions{
		Owner: "compound-core",
		State: "ready",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
	if _, err := store.Claim(imported.Work.ID, "codex", cliTestNow.Add(time.Second)); err != nil {
		t.Fatalf("claim packet: %v", err)
	}
	if _, err := store.Advance(imported.Work.ID, work.AdvanceInput{To: "running", RunID: "run_terminal_packet"}, cliTestNow.Add(2*time.Second)); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(imported.Work.ID, work.AdvanceInput{To: "verifying", VerificationID: "packet-work-contract:terminal-source"}, cliTestNow.Add(3*time.Second)); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	if _, err := store.Close(imported.Work.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "terminal source packet proof",
		Changed:   true,
		ProofRefs: []string{"packet-work-contract:terminal-source"},
	}, cliTestNow.Add(4*time.Second)); err != nil {
		t.Fatalf("close packet: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-step", "--actor", "codex", "--source-packet", packetPath, "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-step output missing JSON:\n%s", out)
	}
	var result work.DispatchStepResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch step result: %v\n%s", err, out)
	}
	if result.Status != "source_packet_terminal" || result.Action != "none" || result.WorkID != imported.Work.ID {
		t.Fatalf("dispatch step result = %+v", result)
	}
	if result.Source == nil || result.Source.Created || result.Source.State != "completed" {
		t.Fatalf("dispatch step source = %+v", result.Source)
	}
	if result.WaitReason != "source_packet_terminal" || !containsString(result.BlockedOn, "work.terminal:"+imported.Work.ID) {
		t.Fatalf("dispatch step wait = %q blocked=%v", result.WaitReason, result.BlockedOn)
	}
}

func TestWorkDispatchStepCommandRunsUntilWait(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch step run command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-step", "--actor", "codex", "--until-wait", "--max-steps", "5", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-step run output missing JSON:\n%s", out)
	}
	var result work.DispatchStepRunResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse dispatch step run result: %v\n%s", err, out)
	}
	if result.Status != "waiting" || result.StepCount != 2 || result.WaitReason != "run_evidence" {
		t.Fatalf("dispatch step run result = %+v", result)
	}
	if len(result.Steps) != 2 || result.Steps[0].Action != "claim" || result.Steps[1].Action != "advance" {
		t.Fatalf("dispatch step run steps = %+v", result.Steps)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch step run result not recorded: %v", err)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get work: %v", err)
	}
	if after.State != "running" {
		t.Fatalf("dispatch step run should stop at running: %+v", after)
	}
}

func TestWorkClaimPlanCommandRecordsPolicyWithoutClaiming(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "claim plan command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"claim-plan", ready.ID, "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("claim-plan output missing JSON:\n%s", out)
	}
	var plan work.ClaimPlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse claim plan: %v\n%s", err, out)
	}
	if plan.Status != "claim_policy_allowed" || !plan.Policy.Allowed {
		t.Fatalf("claim plan policy not allowed: %+v", plan)
	}
	if plan.PlanPath == "" {
		t.Fatalf("claim plan was not recorded: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "leases", ready.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("claim plan must not create lease, stat err=%v", err)
	}
}

func TestWorkAdvancePlanAndAdvanceCommandRecordPolicy(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "advance plan command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(ready.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"advance-plan", ready.ID, "--to", "running", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("advance-plan output missing JSON:\n%s", out)
	}
	var plan work.AdvancePlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse advance plan: %v\n%s", err, out)
	}
	if plan.Status != "advance_policy_allowed" || !plan.Policy.Allowed || plan.PlanPath == "" {
		t.Fatalf("advance plan policy not allowed/recorded: %+v", plan)
	}
	afterPlan, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get after plan: %v", err)
	}
	if afterPlan.State != "claimed" {
		t.Fatalf("advance plan must not mutate work: %+v", afterPlan)
	}
	out = captureStdout(t, func() {
		cmdWork([]string{"advance", ready.ID, "--to", "running", "--by", "codex", "--run", "run_cli"})
	})
	if !strings.Contains(out, "policy=") {
		t.Fatalf("advance output missing policy id:\n%s", out)
	}
	afterAdvance, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get after advance: %v", err)
	}
	if afterAdvance.State != "running" || afterAdvance.PolicyDecisionID == "" || !stringSliceContains(afterAdvance.RunIDs, "run_cli") {
		t.Fatalf("advance command did not record policy/run chain: %+v", afterAdvance)
	}
}

func TestWorkObserveRunCommandAttachesTrace(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	w, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "observe run command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	trace := envelope.TraceEnvelope{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTraceEnvelope,
			ID:            "trace_cli_observe",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "test", Name: "compound"},
			IDs:           envelope.IDs{WorkID: w.ID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Source:          envelope.TraceSource{System: "test", SourceKind: "cli", EventClass: "observe_run"},
		Actor:           "codex",
		MutationClass:   "local_reversible",
		Summary:         "observed CLI run",
		ToolsModelsUsed: []string{},
		FilesTouched:    []string{},
		Outputs:         []string{"trace-output:cli"},
	}
	if err := trace.Validate(); err != nil {
		t.Fatal(err)
	}
	traceBytes, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(homePath, "events", trace.ID+".json")
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, append(traceBytes, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"observe-run", w.ID, "--trace", trace.ID, "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("observe-run output missing JSON:\n%s", out)
	}
	var result work.RunObservationResult
	if err := json.Unmarshal([]byte(out[start:]), &result); err != nil {
		t.Fatalf("parse observe-run result: %v\n%s", err, out)
	}
	if result.Status != "observed" || result.WorkID != w.ID || result.TraceID != trace.ID || result.PolicyDecisionID == "" {
		t.Fatalf("observe-run result = %+v", result)
	}
	after, err := store.Get(w.ID)
	if err != nil {
		t.Fatalf("get observed work: %v", err)
	}
	if after.State != "verifying" || !stringSliceContains(after.VerificationIDs, trace.ID) || after.IDs.TransitionID != "" {
		t.Fatalf("observe-run did not attach trace without closing: %+v", after)
	}
}

func TestWorkClosePlanAndCloseCommandRecordPolicy(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "close plan command"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(ready.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(ready.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow); err != nil {
		t.Fatalf("advance work: %v", err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"close-plan", ready.ID, "--outcome", "completed", "--move-type", "control_plane_repair", "--actor", "codex", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("close-plan output missing JSON:\n%s", out)
	}
	var plan work.ClosePlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse close plan: %v\n%s", err, out)
	}
	if plan.Status != "close_policy_allowed" || !plan.Policy.Allowed || plan.PlanPath == "" {
		t.Fatalf("close plan policy not allowed/recorded: %+v", plan)
	}
	afterPlan, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get after plan: %v", err)
	}
	if afterPlan.State != "running" || afterPlan.IDs.TransitionID != "" {
		t.Fatalf("close plan must not mutate work: %+v", afterPlan)
	}
	out = captureStdout(t, func() {
		cmdWork([]string{"close", ready.ID, "--outcome", "completed", "--move-type", "control_plane_repair", "--summary", "close proof", "--proof", "proof:cli-close", "--proof-ref", "proof:cli-close-ref", "--by", "codex"})
	})
	if !strings.Contains(out, "policy=") {
		t.Fatalf("close output missing policy id:\n%s", out)
	}
	start = strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("close output missing JSON:\n%s", out)
	}
	var closeResult work.CloseResult
	if err := json.Unmarshal([]byte(out[start:]), &closeResult); err != nil {
		t.Fatalf("parse close output: %v\n%s", err, out)
	}
	if !stringSliceContains(closeResult.Receipt.ProofRefs, "proof:cli-close") || !stringSliceContains(closeResult.Receipt.ProofRefs, "proof:cli-close-ref") {
		t.Fatalf("close receipt proof refs = %+v", closeResult.Receipt.ProofRefs)
	}
	closed, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get closed work: %v", err)
	}
	if closed.State != "completed" || closed.PolicyDecisionID == "" || closed.IDs.TransitionID == "" {
		t.Fatalf("close command did not record policy/transition: %+v", closed)
	}
}

func TestValidateKnownFlagsRejectsUnknownFlag(t *testing.T) {
	err := validateKnownFlags(
		[]string{"work_123", "--outcome", "completed", "--proof-ref", "proof:ok", "--proof-rfe", "proof:typo"},
		map[string]bool{"--outcome": true, "--proof-ref": true},
		map[string]bool{"--changed": true},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown flag --proof-rfe") {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if err := validateKnownFlags([]string{"work_123", "--outcome", "completed", "--changed"}, map[string]bool{"--outcome": true}, map[string]bool{"--changed": true}); err != nil {
		t.Fatalf("known flags rejected: %v", err)
	}
}

func TestWorkDispatchPlanCommandImportsSourcePacket(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-plan", "--actor", "codex", "--source-packet", packetPath, "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("dispatch-plan output missing JSON:\n%s", out)
	}
	var plan work.DispatchPlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse dispatch plan: %v\n%s", err, out)
	}
	if plan.Source == nil || plan.Source.Kind != "packet" || !plan.Source.Created {
		t.Fatalf("dispatch plan source missing: %+v", plan.Source)
	}
	if plan.Status != "ready_to_claim" || plan.Selected == nil || plan.Selected.WorkID != plan.Source.WorkID {
		t.Fatalf("dispatch plan did not select packet work: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(homePath, "work", "leases", plan.Source.WorkID+".json")); !os.IsNotExist(err) {
		t.Fatalf("dispatch plan must not create lease, stat err=%v", err)
	}
}

func TestReviewsCommandShowsReviewQueue(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "decision-lifecycle-repairs", "dlrp_cli", map[string]any{
		"id":           "dlrp_cli",
		"status":       "review_required",
		"repair_count": 3,
	}, " ", "# Review\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewartifact.Write(homePath, "route-policy", "rpp_cli", map[string]any{
		"id":     "rpp_cli",
		"status": "ok",
	}, " ", "# OK\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewartifact.Write(homePath, "product-reviews", "prp_cli", map[string]any{
		"id":            "prp_cli",
		"status":        "review_required",
		"product_id":    "product:focus-notes",
		"hypothesis_id": "hyp_focus",
		"signal_id":     "sig_focus",
	}, " ", "# Product review\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewartifact.WriteRaw(homePath, "product-reviews", "prp_cli.review.yaml", []byte("schema_version: compound.product_review.v0.1\n")); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { cmdReviews([]string{"--json"}) })
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("reviews output missing JSON:\n%s", out)
	}
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.SchemaVersion != reviewartifact.QueueSchemaVersion || report.Counts.Total != 3 || report.Counts.ReviewRequired != 2 || report.Counts.OK != 1 {
		t.Fatalf("reviews report = %+v", report)
	}
	if report.Items[0].ID != "dlrp_cli" || report.Items[0].RepairCount != 3 {
		t.Fatalf("first review item = %+v", report.Items[0])
	}
	var productItem reviewartifact.QueueItem
	for _, item := range report.Items {
		if item.ID == "prp_cli" {
			productItem = item
			break
		}
	}
	if productItem.ID == "" || productItem.Readiness != "needs_review" || len(productItem.Actions) != 1 || !productItem.Actions[0].RequiresReview {
		t.Fatalf("product review queue item should be review-gated actionable: %+v", productItem)
	}
	if !strings.Contains(productItem.BlockedReason, "review id product_id hypothesis_id required") {
		t.Fatalf("product review queue item should expose validation blocker: %+v", productItem)
	}
	textOut := captureStdout(t, func() { cmdReviews([]string{"--area", "decision-lifecycle-repairs"}) })
	if !strings.Contains(textOut, "reviews total=1") || !strings.Contains(textOut, "decision-lifecycle-repairs/dlrp_cli") || strings.Contains(textOut, "rpp_cli") {
		t.Fatalf("filtered reviews output =\n%s", textOut)
	}
}

func TestReviewsCommandAnnotatesRoutePromotionHold(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "route-promotions", "route_promotion_cli", map[string]any{
		"schema_version":   "compound.route_promotion_review.v0.1",
		"id":               "route_promotion_cli",
		"status":           "review_required",
		"recommendation":   "hold_insufficient_evidence",
		"reason":           "samples 2 below min_samples 10",
		"lane_id":          "auto/judge",
		"capability_class": "classify-extract",
		"evidence_refs":    []string{"route_a", "route_b"},
	}, " ", "# Route promotion\n"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { cmdReviews([]string{"--area", "route-promotions", "--json"}) })
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 1 || report.Counts.OK != 0 {
		t.Fatalf("route promotion review should remain review debt: %+v", report.Counts)
	}
	if len(report.Items) != 1 || report.Items[0].Readiness != "needs_review" {
		t.Fatalf("route promotion item should be annotated needs_review: %+v", report.Items)
	}
	item := report.Items[0]
	if len(item.Actions) != 0 || !strings.Contains(item.NextAction, "hold lane promotion") {
		t.Fatalf("route promotion item should expose hold/no-mutation next action: %+v", item)
	}
	if len(item.EvidenceRefs) != 2 {
		t.Fatalf("route promotion item should preserve evidence refs: %+v", item)
	}
}

func TestReviewsCommandAnnotatesRoutePromotionRecordedHold(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	paths, err := reviewartifact.Write(homePath, "route-promotions", "route_promotion_recorded_cli", map[string]any{
		"schema_version":                   "compound.route_promotion_review.v0.1",
		"id":                               "route_promotion_recorded_cli",
		"status":                           "review_required",
		"recommendation":                   "hold_insufficient_evidence",
		"reason":                           "samples 2 below min_samples 10",
		"lane_id":                          "auto/judge",
		"capability_class":                 "classify-extract",
		"current_claim_level":              "assumed",
		"model_capabilities_authority_ref": routeevidence.ModelCapabilitiesAuthorityRef,
		"criteria_authority_ref":           routeevidence.PromotionCriteriaAuthorityRef,
		"evidence_refs":                    []string{"route_a", "route_b"},
	}, " ", "# Route promotion\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routeevidence.RecordPromotionHoldReview(homePath, paths.PlanPath, "owner reviewed current samples; keep held until criteria are met", cliTestNow); err != nil {
		t.Fatalf("record promotion hold review: %v", err)
	}

	out := captureStdout(t, func() { cmdReviews([]string{"--area", "route-promotions", "--json"}) })
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 0 || report.Counts.OK != 1 {
		t.Fatalf("recorded route promotion hold should be ok: %+v", report.Counts)
	}
	if report.Counts.ByStatus["recorded"] != 1 {
		t.Fatalf("recorded route promotion status missing: %+v", report.Counts.ByStatus)
	}
	if len(report.Items) != 1 || report.Items[0].Status != "recorded" || report.Items[0].Readiness != "review_recorded" {
		t.Fatalf("route promotion recorded hold should be review_recorded: %+v", report.Items)
	}
	item := report.Items[0]
	if len(item.Actions) != 0 || !strings.Contains(item.NextAction, "keep lane promotion held") {
		t.Fatalf("recorded route promotion should expose no-action next step: %+v", item)
	}
}

func TestReviewsCommandAnnotatesPanelVerdictReview(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "panel-verdicts", "panel_verdict_cli", map[string]any{
		"schema_version": "compound.panel_verdict.v0.1",
		"id":             "panel_verdict_cli",
		"run_id":         "prun_cli",
		"target": map[string]any{
			"kind":    "memory_candidate",
			"ref":     "cand_panel_cli",
			"summary": "candidate needs human adjudication",
		},
		"lens_findings": []map[string]any{
			{
				"lens_ref":           "lens:evidence-strength",
				"route_evidence_ref": "route_a",
				"status":             "ok",
			},
			{
				"lens_ref":           "lens:privacy",
				"route_evidence_ref": "route_b",
				"status":             "failed",
			},
		},
		"synthesis": map[string]any{
			"schema_version": "compound.panel_synthesis.v0.1",
			"decision":       "needs_human",
			"confidence":     0.62,
			"rationale":      "privacy lens failed",
		},
		"evidence_refs": []string{"route_b", "route_a", "route_a"},
	}, " ", "# Panel verdict\n"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { cmdReviews([]string{"--area", "panel-verdicts", "--json"}) })
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 1 || report.Counts.ReadyToApply != 0 || report.Counts.OK != 0 {
		t.Fatalf("panel verdict should remain review debt: %+v", report.Counts)
	}
	if report.Counts.ByStatus["review_required"] != 1 || report.Counts.ByStatus["unknown"] != 0 {
		t.Fatalf("panel verdict projected status should be review_required: %+v", report.Counts.ByStatus)
	}
	if len(report.Items) != 1 || report.Items[0].Status != "review_required" || report.Items[0].Readiness != "needs_review" {
		t.Fatalf("panel verdict item should be annotated needs_review: %+v", report.Items)
	}
	item := report.Items[0]
	if len(item.Actions) != 0 {
		t.Fatalf("panel verdict queue must not expose mutating apply actions: %+v", item)
	}
	if !strings.Contains(item.NextAction, "panel requested human decision") || !strings.Contains(item.NextAction, "memory_candidate/cand_panel_cli") || !strings.Contains(item.NextAction, "failed_lenses=1") {
		t.Fatalf("panel verdict item should expose owner-review context: %+v", item)
	}
	if len(item.EvidenceRefs) != 2 {
		t.Fatalf("panel verdict item should preserve de-duplicated evidence refs: %+v", item)
	}
}

func TestReviewsCommandAnnotatesAppliedPanelVerdictNoAction(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	paths, err := reviewartifact.Write(homePath, "panel-verdicts", "panel_verdict_applied_cli", map[string]any{
		"schema_version": "compound.panel_verdict.v0.1",
		"id":             "panel_verdict_applied_cli",
		"run_id":         "prun_applied_cli",
		"target": map[string]any{
			"kind":    "memory_candidate",
			"ref":     "cand_panel_applied_cli",
			"summary": "candidate already applied by panel run",
		},
		"lens_findings": []map[string]any{{
			"lens_ref":           "lens:evidence-strength",
			"route_evidence_ref": "route_applied",
			"status":             "failed",
		}},
		"synthesis": map[string]any{
			"schema_version": "compound.panel_synthesis.v0.1",
			"decision":       "park",
			"confidence":     0.74,
			"rationale":      "application already recorded no-promote",
		},
		"evidence_refs": []string{"route_applied"},
	}, " ", "# Panel verdict\n")
	if err != nil {
		t.Fatal(err)
	}
	store, err := panel.Open(homePath)
	if err != nil {
		t.Fatalf("open panel store: %v", err)
	}
	run := panel.Run{
		SchemaVersion: panel.RunSchemaVersion,
		ID:            "prun_applied_cli",
		CreatedAt:     cliTestNow.Format(time.RFC3339),
		Status:        "applied",
		TargetKind:    "memory_candidate",
		Findings: []panel.TargetFinding{{
			TargetRef:       "cand_panel_applied_cli",
			VerdictArtifact: paths.PlanPath,
			LensFindings: []panel.LensFinding{{
				LensRef: "lens:evidence-strength",
				Status:  "failed",
			}},
			Synthesis: panel.Synthesis{
				SchemaVersion: panel.SynthesisSchemaVersion,
				Decision:      "park",
				Confidence:    0.74,
				Rationale:     "application already recorded no-promote",
			},
			Application: panel.Application{
				Applied:           true,
				MappedVerdict:     "park",
				ActionIntentRef:   "act_applied_cli",
				PolicyDecisionRef: "pol_applied_cli",
				CandidateRef:      "cand_panel_applied_cli",
				NoPromoteRef:      "nop_applied_cli",
			},
		}},
		CountsBefore: map[string]int{"observed": 1},
		CountsAfter:  map[string]int{"parked": 1},
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("save panel run: %v", err)
	}

	out := captureStdout(t, func() {
		cmdReviews([]string{"--area", "panel-verdicts", "--json"})
	})
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 0 || report.Counts.ReadyToApply != 0 || report.Counts.OK != 1 {
		t.Fatalf("applied panel verdict should be terminal: %+v", report.Counts)
	}
	if report.Counts.ByStatus["applied"] != 1 || report.Counts.ByStatus["review_required"] != 0 {
		t.Fatalf("applied panel verdict projected status should be applied: %+v", report.Counts.ByStatus)
	}
	if len(report.Items) != 1 || report.Items[0].Status != "applied" || report.Items[0].Readiness != "no_action" {
		t.Fatalf("applied panel verdict item should be annotated no_action: %+v", report.Items)
	}
	item := report.Items[0]
	if len(item.Actions) != 0 {
		t.Fatalf("applied panel verdict must not expose mutating apply actions: %+v", item)
	}
	if !strings.Contains(item.NextAction, "already applied by prun_applied_cli") || !strings.Contains(item.NextAction, "memory_candidate/cand_panel_applied_cli") {
		t.Fatalf("applied panel verdict item should expose terminal application context: %+v", item)
	}
}

func TestReviewsCommandResolvesJudgedComparisonPanelVerdict(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "panel-verdicts", "panel_verdict_stale_comparison_cli", map[string]any{
		"schema_version": "compound.panel_verdict.v0.1",
		"id":             "panel_verdict_stale_comparison_cli",
		"run_id":         "prun_stale_comparison_cli",
		"target": map[string]any{
			"kind":    "comparison",
			"ref":     "cmp_stale_comparison_cli",
			"summary": "duplicate comparison verdict after target was judged",
		},
		"lens_findings": []map[string]any{},
		"synthesis": map[string]any{
			"schema_version": "compound.panel_synthesis.v0.1",
			"decision":       "park",
			"confidence":     0.1,
			"rationale":      "insufficient evidence",
		},
		"evidence_refs": []string{"route_stale"},
	}, " ", "# Panel verdict\n"); err != nil {
		t.Fatal(err)
	}
	comparisonDir := filepath.Join(homePath, "inbox", "comparisons")
	if err := os.MkdirAll(comparisonDir, 0o755); err != nil {
		t.Fatalf("create comparison inbox: %v", err)
	}
	candidate := map[string]any{
		"schema_version":   panel.ComparisonCandidateSchemaVersion,
		"id":               "cmp_stale_comparison_cli",
		"capability_class": "judge-critique",
		"input_summary":    "same input",
		"input_digest":     "sha256:cmp-stale-comparison-cli",
		"baseline":         map[string]any{"lane_id": "baseline", "output": "baseline answer", "total_tokens": 100, "latency_ms": 1000},
		"candidate":        map[string]any{"lane_id": "candidate", "output": "candidate answer", "total_tokens": 90, "latency_ms": 900},
		"state":            "judged",
		"verdict":          "inconclusive",
		"verdict_artifact": filepath.Join(homePath, "reviews", "panel-verdicts", "panel_verdict_sibling_comparison_cli.json"),
		"created_at":       cliTestNow.Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatalf("marshal comparison candidate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(comparisonDir, "cmp_stale_comparison_cli.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write comparison candidate: %v", err)
	}
	out := captureStdout(t, func() {
		cmdReviews([]string{"--area", "panel-verdicts", "--json"})
	})
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 0 || report.Counts.OK != 1 {
		t.Fatalf("resolved comparison panel verdict should be terminal: %+v", report.Counts)
	}
	if report.Counts.ByStatus["resolved"] != 1 {
		t.Fatalf("resolved comparison status missing: %+v", report.Counts.ByStatus)
	}
	item := report.Items[0]
	if item.Status != "resolved" || item.Readiness != "no_action" || len(item.Actions) != 0 {
		t.Fatalf("resolved comparison panel verdict should expose no action: %+v", item)
	}
	if !strings.Contains(item.NextAction, "comparison target already judged") || !strings.Contains(item.NextAction, "inconclusive") {
		t.Fatalf("resolved comparison panel verdict should explain terminal target: %+v", item)
	}
}

func TestReviewsCommandAnnotatesCapabilityEvaluationRecorded(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "capability-evaluation-intake", "cep_cli", map[string]any{
		"schema_version": "compound.capability.evaluation_plan.v0.1",
		"id":             "cep_cli",
		"status":         "recorded",
		"evaluation_id":  "capability-evaluation.external.example",
		"capability":     "example-capability",
		"target_path":    "/tmp/capability-manifest.yaml",
		"evidence_refs":  []string{"eval_a", "eval_b", "eval_a"},
	}, " ", "# Capability evaluation\n"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { cmdReviews([]string{"--area", "capability-evaluation-intake", "--json"}) })
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, out)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 0 || report.Counts.ReadyToApply != 0 || report.Counts.OK != 1 {
		t.Fatalf("capability evaluation recorded item should remain terminal: %+v", report.Counts)
	}
	item := report.Items[0]
	if item.Readiness != "no_action" || len(item.Actions) != 0 {
		t.Fatalf("capability evaluation recorded item should be terminal/non-mutating: %+v", item)
	}
	if !strings.Contains(item.NextAction, "separate reviewed manifest import") {
		t.Fatalf("capability evaluation item should expose graph mutation boundary: %+v", item)
	}
	if len(item.EvidenceRefs) != 2 {
		t.Fatalf("capability evaluation item should preserve de-duplicated evidence refs: %+v", item)
	}
}

func TestReviewsRecordFindingConsumesStandingGrantAndRecordsWorkAction(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	grantSource := filepath.Join(t.TempDir(), "standing-grants.yaml")
	if err := os.WriteFile(grantSource, []byte(`generated_at: "2026-07-01T12:00:00Z"
grants:
  - grant_id: grant_review_finding
    capabilities:
      - review.finding
      - review.summary
    target_scope: state/runs/*
    reversibility_ceiling: reversible
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.ImportStandingGrants(homePath, grantSource, cliTestNow); err != nil {
		t.Fatalf("import standing grants: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	w, err := store.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "review finding action is governed by StandingGrant",
		DesiredOutcome:   "review finding recorded under state/runs and linked to WorkEnvelope",
		EvidenceRefs:     []string{"test://standing-grant-review-finding"},
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	runRoot := t.TempDir()
	target := "state/runs/standing-grant-review/finding.json"
	sourceArtifact := "state/runs/agent-session/session-summary.json"
	sourcePath := filepath.Join(runRoot, filepath.FromSlash(sourceArtifact))
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte(`{"finding":"agent session produced review evidence"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceTrace := envelope.TraceEnvelope{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTraceEnvelope,
			ID:            "trace_review_finding_source",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "test", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: w.ID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{"test://trace-source"},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Source:        envelope.TraceSource{System: "test-runner", SourceKind: "session.summary"},
		MutationClass: "read",
		Summary:       "test trace source for review finding",
	}
	traceData, err := json.MarshalIndent(sourceTrace, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(homePath, "events", sourceTrace.ID+".json")
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, append(traceData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdReviews([]string{
			"record-finding",
			"--work-id", w.ID,
			"--target", target,
			"--root", runRoot,
			"--summary", "review finding recorded through standing grant",
			"--source-artifact", sourceArtifact,
			"--source-trace", sourceTrace.ID,
			"--evidence-ref", "test://review-input",
		})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("record finding output missing JSON:\n%s", out)
	}
	var report reviewFindingRecord
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse record finding report: %v\n%s", err, out)
	}
	if report.Status != "recorded" || report.WorkID != w.ID || report.TargetPath != target {
		t.Fatalf("record finding report = %+v", report)
	}
	if report.PolicyOutcome != "allow" || report.StandingGrantID != "grant_review_finding:review-finding" {
		t.Fatalf("policy did not consume standing grant: %+v", report)
	}
	if len(report.SourceArtifacts) != 1 || report.SourceArtifacts[0].Path != sourceArtifact {
		t.Fatalf("source artifact not recorded: %+v", report.SourceArtifacts)
	}
	if !strings.HasPrefix(report.SourceArtifacts[0].Hash, "sha256:") || !stringSliceContains(report.EvidenceRefs, sourceArtifact) || !stringSliceContains(report.EvidenceRefs, report.SourceArtifacts[0].Hash) {
		t.Fatalf("source artifact evidence not recorded: source=%+v refs=%v", report.SourceArtifacts[0], report.EvidenceRefs)
	}
	if len(report.SourceTraces) != 1 || report.SourceTraces[0].ID != sourceTrace.ID {
		t.Fatalf("source trace not recorded: %+v", report.SourceTraces)
	}
	if !stringSliceContains(report.EvidenceRefs, "trace_envelope:"+sourceTrace.ID) || !stringSliceContains(report.EvidenceRefs, "test://trace-source") {
		t.Fatalf("source trace evidence not recorded: traces=%+v refs=%v", report.SourceTraces, report.EvidenceRefs)
	}
	if _, err := os.Stat(filepath.Join(runRoot, target)); err != nil {
		t.Fatalf("finding json not written: %v", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(filepath.Join(runRoot, target), ".json") + ".md"); err != nil {
		t.Fatalf("finding markdown not written: %v", err)
	}
	got, _, err := store.Show(w.ID)
	if err != nil {
		t.Fatalf("show work: %v", err)
	}
	if !stringSliceContains(got.RunIDs, report.ActionIntentID) || got.PolicyDecisionID != report.PolicyDecisionID {
		t.Fatalf("work action chain not recorded: runs=%v policy=%q report=%+v", got.RunIDs, got.PolicyDecisionID, report)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", report.ActionIntentID+".json")); err != nil {
		t.Fatalf("action intent envelope not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", report.PolicyDecisionID+".json")); err != nil {
		t.Fatalf("policy decision envelope not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("review finding action must not promote claims, stat err=%v", err)
	}
}

func TestReviewsRecordFindingAutoWorkClosesWorkEnvelope(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	grantSource := filepath.Join(t.TempDir(), "standing-grants.yaml")
	if err := os.WriteFile(grantSource, []byte(`
generated_at: "2026-07-01T12:00:00Z"
grants:
  - grant_id: grant_review_finding
    capabilities:
      - review.finding
    target_scope: state/runs/*
    reversibility_ceiling: reversible
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.ImportStandingGrants(homePath, grantSource, cliTestNow); err != nil {
		t.Fatalf("import standing grants: %v", err)
	}
	sourceTrace := envelope.TraceEnvelope{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTraceEnvelope,
			ID:            "trace_auto_review_finding_source",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "test", Name: "compound-test"},
			IDs:           envelope.IDs{},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{"test://auto-trace-source"},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Source:        envelope.TraceSource{System: "test-runner", SourceKind: "session.summary"},
		MutationClass: "read",
		Summary:       "auto-work trace source for review finding",
	}
	traceData, err := json.MarshalIndent(sourceTrace, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(homePath, "events", sourceTrace.ID+".json")
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, append(traceData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	runRoot := t.TempDir()
	target := "state/runs/auto-review/finding.json"
	out := captureStdout(t, func() {
		cmdReviews([]string{
			"record-finding",
			"--auto-work",
			"--target", target,
			"--root", runRoot,
			"--summary", "auto-work review finding recorded through standing grant",
			"--source-trace", sourceTrace.ID,
		})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("record finding output missing JSON:\n%s", out)
	}
	var report reviewFindingRecord
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse record finding report: %v\n%s", err, out)
	}
	if report.WorkID == "" || report.PolicyOutcome != "allow" || report.StandingGrantID == "" {
		t.Fatalf("auto-work report did not consume grant: %+v", report)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	got, _, err := store.Show(report.WorkID)
	if err != nil {
		t.Fatalf("show auto work: %v", err)
	}
	if got.State != "completed" || got.IDs.TransitionID == "" || !stringSliceContains(got.RunIDs, report.ActionIntentID) {
		t.Fatalf("auto work not closed through action chain: work=%+v report=%+v", got, report)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", got.IDs.TransitionID+".json")); err != nil {
		t.Fatalf("transition envelope not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runRoot, target)); err != nil {
		t.Fatalf("finding json not written: %v", err)
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

func TestAuthorityJobRosterImportsAndReportsAuthority(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	source := filepath.Join(t.TempDir(), "cron-jobs.json")
	writeCLIJobRosterFixture(t, source)

	importOut := captureStdout(t, func() {
		cmdAuthority([]string{"import-job-roster", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://registries/job-roster"`,
		`"job_count": 2`,
		`"error_count": 1`,
		`"source_role": "evidence_only_input_after_import"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import job roster output missing %q:\n%s", want, importOut)
		}
	}

	usage := filepath.Join(t.TempDir(), "cron-token-usage.json")
	writeCLIJobTokenUsageFixture(t, usage)
	tokenOut := captureStdout(t, func() {
		cmdAuthority([]string{"import-job-token-governance", usage})
	})
	for _, want := range []string{
		`"token_sessions_count": 8`,
		`"enabled_agent_count": 1`,
		`"high_token_enabled_count": 1`,
	} {
		if !strings.Contains(tokenOut, want) {
			t.Fatalf("import job token governance output missing %q:\n%s", want, tokenOut)
		}
	}

	authorityOut := captureStdout(t, func() {
		cmdAuthority([]string{"job-roster-authority", "--json"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"job_count": 2`,
		`"enabled_count": 2`,
		`"error_count": 1`,
		`"name": "ibkr-exit-check-daily"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("job roster authority output missing %q:\n%s", want, authorityOut)
		}
	}
	healthOut := captureStdout(t, func() {
		cmdAuthority([]string{"job-health-plan"})
	})
	for _, want := range []string{
		`"schema_version": "compound.job_health_plan_report.v0.1"`,
		`"plan_count": 1`,
		`"job_name": "ibkr-exit-check-daily"`,
		`"PlanPath":`,
	} {
		if !strings.Contains(healthOut, want) {
			t.Fatalf("job health plan output missing %q:\n%s", want, healthOut)
		}
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("job roster command must not promote claims, stat err=%v", err)
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
	jsonOut := captureStdout(t, func() {
		cmdAuthority([]string{"git", repo, "--json"})
	})
	var report struct {
		SchemaVersion  string `json:"schema_version"`
		Status         string `json:"status"`
		AuthorityRef   string `json:"authority_ref"`
		EntityRef      string `json:"entity_ref"`
		GitRoot        string `json:"git_root"`
		Branch         string `json:"branch"`
		Ahead          int    `json:"ahead"`
		Behind         int    `json:"behind"`
		Unstaged       int    `json:"unstaged"`
		Untracked      int    `json:"untracked"`
		ChangedPaths   int    `json:"changed_paths"`
		CandidateCount int    `json:"candidate_count"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("parse authority git JSON: %v\n%s", err, jsonOut)
	}
	wantRepoInfo, err := os.Stat(repo)
	if err != nil {
		t.Fatal(err)
	}
	gotRepoInfo, err := os.Stat(report.GitRoot)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != "compound.source_control_snapshot.v0.1" ||
		report.Status != "dirty" ||
		report.AuthorityRef != "compound://ledger/source-control" ||
		report.Branch != "main" ||
		report.Ahead != 0 ||
		report.Behind != 0 ||
		report.Unstaged != 1 ||
		report.Untracked != 1 ||
		report.ChangedPaths != 2 ||
		report.CandidateCount == 0 ||
		!strings.HasPrefix(report.EntityRef, "repo:") ||
		!os.SameFile(wantRepoInfo, gotRepoInfo) {
		t.Fatalf("bad authority git JSON: %+v\n%s", report, jsonOut)
	}
	resolved := captureStdout(t, func() {
		cmdAuthority([]string{"resolve", repo})
	})
	if !strings.Contains(resolved, "status: resolved") || !strings.Contains(resolved, "entity: repo:") {
		t.Fatalf("git root did not resolve source-control entity:\n%s", resolved)
	}
}

func TestAuthorityDirtyPlanWritesReviewWithoutMutatingRepo(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	repo := initCompoundCommandGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := compoundCommandGitStatus(t, repo)
	out := captureStdout(t, func() {
		cmdAuthority([]string{"dirty-plan", repo, "--work-id", "work_dirty_gate"})
	})
	for _, want := range []string{
		"# source-control dirty-plan scr_",
		"- status: review_required",
		"- work: work_dirty_gate",
		"- source_status: dirty",
		"- changed_paths: 2",
		"- required_evidence: 4",
		"- evidence_refs: 1",
		"- plan:",
		"- review:",
		"- review_yaml:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dirty-plan output missing %q:\n%s", want, out)
		}
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	reviewYAMLPath := outputValue(out, "- review_yaml:")
	var plan struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		WorkID        string `json:"work_id"`
		ChangedPaths  int    `json:"changed_paths"`
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != "compound.source_control_review_plan.v0.1" || plan.Status != "review_required" || plan.WorkID != "work_dirty_gate" || plan.ChangedPaths != 2 {
		t.Fatalf("bad dirty plan: %+v", plan)
	}
	if _, err := os.Stat(reviewYAMLPath); err != nil {
		t.Fatalf("dirty-plan review yaml missing: %v", err)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not stage") || !strings.Contains(review, "Required Evidence") || !strings.Contains(review, "Review YAML Template") {
		t.Fatalf("dirty-plan review missing no-mutation/procedure text:\n%s", review)
	}
	jsonOut := captureStdout(t, func() {
		cmdAuthority([]string{"dirty-plan", repo, "--work-id", "work_dirty_gate", "--json"})
	})
	var dirtyPlanJSON struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		AuthorityRef  string `json:"authority_ref"`
		ID            string `json:"id"`
		ChangedPaths  int    `json:"changed_paths"`
		Paths         struct {
			PlanPath       string `json:"plan_path"`
			ReviewPath     string `json:"review_path"`
			ReviewYAMLPath string `json:"review_yaml_path"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &dirtyPlanJSON); err != nil {
		t.Fatalf("parse dirty-plan JSON: %v\n%s", err, jsonOut)
	}
	if dirtyPlanJSON.SchemaVersion != "compound.source_control_review_plan.v0.1" ||
		dirtyPlanJSON.Status != "review_required" ||
		dirtyPlanJSON.AuthorityRef != "compound://reviews/source-control-cleanup" ||
		dirtyPlanJSON.ID == "" ||
		dirtyPlanJSON.ChangedPaths != 2 ||
		dirtyPlanJSON.Paths.PlanPath == "" ||
		dirtyPlanJSON.Paths.ReviewPath == "" ||
		dirtyPlanJSON.Paths.ReviewYAMLPath == "" {
		t.Fatalf("bad dirty-plan JSON: %+v\n%s", dirtyPlanJSON, jsonOut)
	}
	reviewYAML := readFile(t, reviewYAMLPath)
	reviewYAML = strings.ReplaceAll(reviewYAML, "decision: TODO_decision_owner_action_required_owner_action_done_parked_no_action", "decision: parked")
	reviewYAML = strings.ReplaceAll(reviewYAML, `rationale: ""`, `rationale: "untracked scratch is parked for owner classification"`)
	reviewYAML = strings.ReplaceAll(reviewYAML, "confidence: -1", "confidence: 0.9")
	reviewYAML = strings.ReplaceAll(reviewYAML, `next_action: "TODO: reviewed source-control cleanup next action"`, `next_action: "leave scratch parked until owner classifies it"`)
	if err := os.WriteFile(reviewYAMLPath, []byte(reviewYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewOut := captureStdout(t, func() {
		cmdAuthority([]string{"dirty-review", reviewYAMLPath, "--json"})
	})
	var reviewReport struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		Decision      string `json:"decision"`
		ReviewPath    string `json:"review_path"`
	}
	if err := json.Unmarshal([]byte(reviewOut), &reviewReport); err != nil {
		t.Fatalf("parse dirty-review JSON: %v\n%s", err, reviewOut)
	}
	if reviewReport.SchemaVersion != "compound.source_control_review_report.v0.1" ||
		reviewReport.Status != "recorded" ||
		reviewReport.Decision != "parked" ||
		reviewReport.ReviewPath == "" {
		t.Fatalf("bad dirty-review JSON: %+v\n%s", reviewReport, reviewOut)
	}
	queueOut := captureStdout(t, func() {
		cmdReviews([]string{"--area", "source-control-cleanup", "--json"})
	})
	var queue reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(queueOut), &queue); err != nil {
		t.Fatalf("parse source-control cleanup queue: %v\n%s", err, queueOut)
	}
	if queue.Counts.Total != 1 || queue.Counts.ReviewRequired != 0 || queue.Counts.OK != 1 || queue.Items[0].Readiness != "review_recorded" {
		t.Fatalf("source-control cleanup queue not recorded: %+v", queue)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dirty-plan must not promote claims, stat err=%v", err)
	}
	after := compoundCommandGitStatus(t, repo)
	if before != after {
		t.Fatalf("dirty-plan mutated repo\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestHookPreToolUseHoldsDirtySourceControlMutation(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	repo := initCompoundCommandGitRepo(t)
	target := filepath.Join(repo, "README.md")
	if err := os.WriteFile(target, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := writeHookFixture(t, hook.Fixture{
		Harness: "claude_code",
		WorkID:  "work_dirty_cli",
		Action: &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            "act_dirty_cli",
				CreatedAt:     cliTestNow.Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
				IDs:           envelope.IDs{WorkID: "work_dirty_cli"},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier1",
				EvidenceRefs:  []string{},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:         "claude_code",
			Capability:    "filesystem.write",
			Action:        "write_file",
			Target:        envelope.ActionTarget{Kind: "file", Path: target},
			MutationClass: "local_reversible",
			Reversibility: "easy",
			BlastRadius:   "repo",
		},
	})
	out := captureStdout(t, func() {
		cmdHook([]string{"pre-tool-use", fixture})
	})
	for _, want := range []string{`"status": "blocked"`, `"outcome": "hold"`, "source_control_status dirty", "source-control:repo:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("hook pre-tool-use output missing %q:\n%s", want, out)
		}
	}
	for _, rel := range []string{filepath.Join("events", "act_dirty_cli.json"), filepath.Join("ledger", "claims.jsonl")} {
		if _, err := os.Stat(filepath.Join(homePath, rel)); err != nil {
			t.Fatalf("missing runtime artifact %s: %v", rel, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(homePath, "events"))
	if err != nil {
		t.Fatal(err)
	}
	foundPolicy := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "pol_") && strings.HasSuffix(entry.Name(), ".json") {
			foundPolicy = true
			break
		}
	}
	if !foundPolicy {
		t.Fatalf("policy decision artifact missing in events dir")
	}
	listOut := captureStdout(t, func() {
		cmdWork([]string{"list"})
	})
	for _, want := range []string{"held", "review held policy gate"} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("work list output missing held review %q:\n%s", want, listOut)
		}
	}
}

func TestHookPreToolUseEnforceReturnsGateExit(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := writeHookFixture(t, hook.Fixture{
		Harness: "claude_code",
		WorkID:  "work_enforce",
		Action: &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            "act_enforce",
				CreatedAt:     cliTestNow.Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
				IDs:           envelope.IDs{WorkID: "work_enforce"},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier2",
				EvidenceRefs:  []string{},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:         "claude_code",
			Capability:    "filesystem.delete",
			Action:        "delete_tree",
			Target:        envelope.ActionTarget{Kind: "path", Path: "build/cache"},
			MutationClass: "destructive",
			Reversibility: "hard",
		},
	})

	var code int
	out := captureStdout(t, func() {
		code = runHookCommand([]string{"pre-tool-use", "--enforce", fixture})
	})
	if code != 3 {
		t.Fatalf("enforced held hook exit = %d, want 3\n%s", code, out)
	}
	for _, want := range []string{`"status": "blocked"`, `"outcome": "hold"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("enforced hook output missing %q:\n%s", want, out)
		}
	}
}

func TestHookPreToolUseEnforceBlocksFailOpenHarness(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := writeHookFixture(t, hook.Fixture{
		Harness: "codex",
		WorkID:  "work_fail_open",
		Action: &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            "act_fail_open",
				CreatedAt:     cliTestNow.Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
				IDs:           envelope.IDs{WorkID: "work_fail_open"},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier2",
				EvidenceRefs:  []string{},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:         "codex",
			Capability:    "filesystem.delete",
			Action:        "delete_tree",
			Target:        envelope.ActionTarget{Kind: "path", Path: "build/cache"},
			MutationClass: "destructive",
			Reversibility: "hard",
		},
	})

	var enforced int
	enforcedOut := captureStdout(t, func() {
		enforced = runHookCommand([]string{"pre-tool-use", "--enforce", fixture})
	})
	if enforced != 3 {
		t.Fatalf("enforced fail-open hook exit = %d, want 3\n%s", enforced, enforcedOut)
	}
	if !strings.Contains(enforcedOut, `"status": "fail_open"`) {
		t.Fatalf("enforced hook should preserve fail_open evidence:\n%s", enforcedOut)
	}

	var observed int
	observedOut := captureStdout(t, func() {
		observed = runHookCommand([]string{"pre-tool-use", fixture})
	})
	if observed != 0 {
		t.Fatalf("non-enforced fixture hook exit = %d, want 0\n%s", observed, observedOut)
	}
}

func TestHookPreToolUseEnforceBlocksPlacementGuard(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := writeHookFixture(t, hook.Fixture{
		Harness: "codex",
		WorkID:  "work_placement",
		Placement: &policy.PlacementGuardContext{
			Mode:         "block",
			Confidence:   "block",
			OwnerRef:     "owner:decisions",
			TargetRef:    "docs/wrong-home.md",
			Reason:       "requested target does not match owner placement",
			EvidenceRefs: []string{"placement:rule:abc123"},
		},
		Action: &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            "act_placement",
				CreatedAt:     cliTestNow.Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
				IDs:           envelope.IDs{WorkID: "work_placement"},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier2",
				EvidenceRefs:  []string{},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:         "codex",
			Capability:    "filesystem.write",
			Action:        "write_file",
			Target:        envelope.ActionTarget{Kind: "file", Path: "docs/wrong-home.md"},
			MutationClass: "local_reversible",
			Reversibility: "easy",
			BlastRadius:   "repo",
		},
	})
	var code int
	out := captureStdout(t, func() {
		code = runHookCommand([]string{"pre-tool-use", "--enforce", fixture})
	})
	if code != 3 {
		t.Fatalf("enforced placement hook exit = %d, want 3\n%s", code, out)
	}
	for _, want := range []string{
		`"status": "fail_open"`,
		`"outcome": "hold"`,
		"placement_guard mode=block confidence=block",
		"placement:rule:abc123",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("enforced placement output missing %q:\n%s", want, out)
		}
	}
}

func TestPolicyPlacementRulesAuthorityFeedsPreToolUse(t *testing.T) {
	homePath := t.TempDir()
	userHome := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("HOME", userHome)
	t.Setenv("PLACEMENT_GUARD_MODE", "block")

	source := filepath.Join("..", "..", "fixtures", "policy", "placement-rules.json")
	importOut := captureStdout(t, func() {
		cmdPolicy([]string{"import-placement-rules", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://registries/placement-rules"`,
		`"owner_count": 1`,
		`"open_write_count": 1`,
		`"authority_status": "compound_primary"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import placement rules output missing %q:\n%s", want, importOut)
		}
	}

	authorityOut := captureStdout(t, func() {
		cmdPolicy([]string{"placement-authority"})
	})
	authorityJSONOut := captureStdout(t, func() {
		cmdPolicy([]string{"placement-authority", "--json"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"placement_guard_mode": "block"`,
		`"policy_source": "built-in"`,
		`"owner_count": 1`,
		`"block_owner_count": 1`,
		`"block_owner_ids": [
    "docs"
  ]`,
		`"source_role": "evidence_only_input_after_import"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("placement authority output missing %q:\n%s", want, authorityOut)
		}
		if !strings.Contains(authorityJSONOut, want) {
			t.Fatalf("placement authority --json output missing %q:\n%s", want, authorityJSONOut)
		}
	}

	canonicalTarget := filepath.Join(userHome, "compound-owners", "docs", "DOC-0001.md")
	canonicalFixture := writeHookFixture(t, hook.Fixture{
		Harness: "codex",
		WorkID:  "work_placement_authority_canonical",
		Action: &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            "act_placement_authority_canonical",
				CreatedAt:     cliTestNow.Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
				IDs:           envelope.IDs{WorkID: "work_placement_authority_canonical"},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier2",
				EvidenceRefs:  []string{},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:         "codex",
			Capability:    "filesystem.write",
			Action:        "write_file",
			Target:        envelope.ActionTarget{Kind: "file", Path: canonicalTarget},
			MutationClass: "local_reversible",
			Reversibility: "easy",
			BlastRadius:   "repo",
		},
	})
	var canonicalCode int
	canonicalOut := captureStdout(t, func() {
		canonicalCode = runHookCommand([]string{"pre-tool-use", "--enforce", canonicalFixture})
	})
	if canonicalCode != 0 {
		t.Fatalf("canonical placement hook exit = %d, want 0\n%s", canonicalCode, canonicalOut)
	}
	if strings.Contains(canonicalOut, "placement-owner:docs") {
		t.Fatalf("canonical owner home should not emit placement context:\n%s", canonicalOut)
	}

	target := filepath.Join(userHome, "outside", "DOC-0001.md")
	fixture := writeHookFixture(t, hook.Fixture{
		Harness: "codex",
		WorkID:  "work_placement_authority",
		Action: &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            "act_placement_authority",
				CreatedAt:     cliTestNow.Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
				IDs:           envelope.IDs{WorkID: "work_placement_authority"},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier2",
				EvidenceRefs:  []string{},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:         "codex",
			Capability:    "filesystem.write",
			Action:        "write_file",
			Target:        envelope.ActionTarget{Kind: "file", Path: target},
			MutationClass: "local_reversible",
			Reversibility: "easy",
			BlastRadius:   "repo",
		},
	})

	var code int
	out := captureStdout(t, func() {
		code = runHookCommand([]string{"pre-tool-use", "--enforce", fixture})
	})
	if code != 3 {
		t.Fatalf("enforced hook exit = %d, want 3\n%s", code, out)
	}
	for _, want := range []string{
		`"status": "fail_open"`,
		`"outcome": "hold"`,
		"compound://registries/placement-rules",
		"placement-owner:docs",
		"canon-filename-outside-owner-home",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("hook output missing %q:\n%s", want, out)
		}
	}
}

func TestPolicyOperatorStateAuthorityFeedsPolicyConfig(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)

	source := filepath.Join("..", "..", "fixtures", "policy", "operator-state-l1.json")
	importOut := captureStdout(t, func() {
		cmdPolicy([]string{"import-operator-state", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://registries/operator-state"`,
		`"autonomy_level": "L1"`,
		`"authority_status": "compound_primary"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import operator-state output missing %q:\n%s", want, importOut)
		}
	}

	authorityOut := captureStdout(t, func() {
		cmdPolicy([]string{"operator-authority"})
	})
	authorityJSONOut := captureStdout(t, func() {
		cmdPolicy([]string{"operator-authority", "--json"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"autonomy_level": "L1"`,
		`"source_role": "evidence_only_input_after_import"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("operator authority output missing %q:\n%s", want, authorityOut)
		}
		if !strings.Contains(authorityJSONOut, want) {
			t.Fatalf("operator authority --json output missing %q:\n%s", want, authorityJSONOut)
		}
	}

	cfg := policyConfig()
	ai := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            "act_operator_state",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: "work_operator_state"},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    "filesystem.write",
		Action:        "write_file",
		Target:        envelope.ActionTarget{Kind: "file", Path: "docs/x.md"},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "repo",
	}
	pd := policy.Evaluate(ai, cfg, cliTestNow)
	if pd.Outcome != "hold" {
		t.Fatalf("L1 operator state should hold local reversible action, got %q reasons=%v", pd.Outcome, pd.Reasons)
	}
	if !stringSliceContains(pd.EvidenceRefs, policy.OperatorStateAuthorityRef) {
		t.Fatalf("policy decision missing operator-state authority evidence: %v", pd.EvidenceRefs)
	}
	if !strings.Contains(strings.Join(pd.Reasons, " "), "operator autonomy ceiling L1") {
		t.Fatalf("policy decision missing autonomy reason: %v", pd.Reasons)
	}
}

func TestPolicyOperatorPromptsAuthorityReportsPromptCards(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)

	source := filepath.Join("..", "..", "fixtures", "policy", "operator-decision-prompts.json")
	importOut := captureStdout(t, func() {
		cmdPolicy([]string{"import-operator-prompts", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://registries/operator-decision-prompts"`,
		`"prompt_count": 1`,
		`"high_severity_count": 1`,
		`"authority_status": "compound_primary"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import operator prompts output missing %q:\n%s", want, importOut)
		}
	}

	authorityOut := captureStdout(t, func() {
		cmdPolicy([]string{"operator-prompts-authority"})
	})
	authorityJSONOut := captureStdout(t, func() {
		cmdPolicy([]string{"operator-prompts-authority", "--json"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"prompt_count": 1`,
		`"source_role": "evidence_only_input_after_import"`,
		`"id": "revenue-primary:fixture"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("operator prompts authority output missing %q:\n%s", want, authorityOut)
		}
		if !strings.Contains(authorityJSONOut, want) {
			t.Fatalf("operator prompts authority --json output missing %q:\n%s", want, authorityJSONOut)
		}
	}
}

func TestPolicyOperatorApprovalFrontierAuthorityReportsDeferredFamilies(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)

	source := filepath.Join("..", "..", "fixtures", "policy", "operator-approval-frontier-summary.json")
	importOut := captureStdout(t, func() {
		cmdPolicy([]string{"import-operator-approval-frontier", source})
	})
	for _, want := range []string{
		`"authority_ref": "compound://registries/operator-approval-frontier"`,
		`"family_count": 2`,
		`"deferred_count": 2`,
		`"authority_status": "compound_primary"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import operator approval frontier output missing %q:\n%s", want, importOut)
		}
	}

	authorityOut := captureStdout(t, func() {
		cmdPolicy([]string{"operator-approval-frontier-authority"})
	})
	authorityJSONOut := captureStdout(t, func() {
		cmdPolicy([]string{"operator-approval-frontier-authority", "--json"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"family_count": 2`,
		`"deferred_count": 2`,
		`"source_role": "evidence_only_input_after_import"`,
		`"id": "operator-decision-prompt-cards"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("operator approval frontier authority output missing %q:\n%s", want, authorityOut)
		}
		if !strings.Contains(authorityJSONOut, want) {
			t.Fatalf("operator approval frontier authority --json output missing %q:\n%s", want, authorityJSONOut)
		}
	}
}

func TestPolicyReleaseGoalHoldsAuthorityReportsHoldBacklog(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	dbPath := filepath.Join(t.TempDir(), "release-orchestrator-state.sqlite")
	createReleaseGoalHoldsDB(t, dbPath)

	importOut := captureStdout(t, func() {
		cmdPolicy([]string{"import-release-goal-holds", dbPath})
	})
	for _, want := range []string{
		`"authority_ref": "compound://registries/release-goal-holds"`,
		`"hold_count": 1`,
		`"authority_status": "compound_primary"`,
		`"source_role": "evidence_only_input_after_import"`,
	} {
		if !strings.Contains(importOut, want) {
			t.Fatalf("import release-goal holds output missing %q:\n%s", want, importOut)
		}
	}
	authorityOut := captureStdout(t, func() {
		cmdPolicy([]string{"release-goal-holds-authority", "--json"})
	})
	for _, want := range []string{
		`"installed": true`,
		`"hold_count": 1`,
		`"goal_id": "release-hold-fixture"`,
		`"app_id": "phone-cleaner"`,
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("release-goal holds authority output missing %q:\n%s", want, authorityOut)
		}
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

func TestImportedSkillOfferCanBecomeValidatedSecondHarnessProductLoop(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml")})
	cmdHook([]string{"user-prompt-submit", writeHookFixture(t, hook.Fixture{
		Harness:  "claude_code",
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
	pack := readContextPack(t, homePath, w.IDs.ContextPackID)
	if !contextPackIncludes(pack, "capability://skill:claude-pricing") {
		t.Fatalf("claude context pack did not include claude-compatible pricing skill; included=%+v", pack.Included)
	}
	if contextPackIncludes(pack, "capability://skill:codex-pricing") {
		t.Fatalf("claude context pack included codex-only skill; included=%+v", pack.Included)
	}

	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "ready"}, cliTestNow); err != nil {
		t.Fatalf("advance ready: %v", err)
	}
	if _, err := store.Claim(w.ID, "claude_code", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.RecordAction(
		w.ID,
		testActionIntent(w.ID, "act_imported_claude_skill_used", "capability://skill:claude-pricing"),
		testPolicyDecision(w.ID, "pol_imported_claude_skill_used", "act_imported_claude_skill_used"),
		cliTestNow,
	); err != nil {
		t.Fatalf("record action: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	cmdHook([]string{"session-end", writeHookFixture(t, hook.Fixture{
		Harness:  "claude_code",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "pricing paywall review used imported claude skill",
	})})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:claude-pricing") {
		t.Fatalf("session-end did not validate imported claude skill; edges=%+v", edges)
	}
	if hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:codex-pricing") {
		t.Fatalf("claude loop validated codex-only skill; edges=%+v", edges)
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

func TestDistillerEvalPlanUsesOperationalizedEvalCandidateWithoutMutatingCorpus(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
 "trace_ref": "trace_eval_plan_candidate",
 "candidates": [{
  "kind": "eval_case",
  "subject": "distill.capability_feedback",
  "statement": "distiller should preserve missed capability feedback as regression coverage",
  "confidence": 0.93,
  "evidence_refs": ["trace:trace_eval_plan_candidate", "receipt:rec_eval_plan"]
 }]
}`)
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	evalRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.EvalCase)))
	if len(evalRows) != 1 || evalRows[0].Verdict != distiller.Operationalize {
		t.Fatalf("eval candidates = %+v", evalRows)
	}
	corpusDir := filepath.Join(t.TempDir(), "distiller-evals")
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	corpusFile := filepath.Join(corpusDir, "existing-eval.json")
	original := `{"existing":true}` + "\n"
	if err := os.WriteFile(corpusFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdDistiller([]string{"eval-plan", "--candidate", evalRows[0].ID, "--corpus", corpusDir})
	})
	if !strings.Contains(out, "# distiller eval-plan evp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_artifacts: 3") ||
		!strings.Contains(out, "- evidence_refs: 2") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("eval-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	var plan distiller.EvalPlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != distiller.EvalPlanSchemaVersion ||
		plan.CandidateID != evalRows[0].ID ||
		plan.CandidateVerdict != distiller.Operationalize ||
		plan.FixtureDraft.SchemaVersion != distiller.EvalCaseSchemaVersion ||
		plan.FixtureDraft.ProposedCaseID == "" ||
		plan.FixtureDraft.RequestTraceRef != "trace:trace_eval_plan_candidate" {
		t.Fatalf("bad eval plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate the eval corpus") ||
		!strings.Contains(review, "Required Before Corpus Apply") {
		t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
	}
	if got := readFile(t, corpusFile); got != original {
		t.Fatalf("corpus mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("eval-plan must not promote claims, stat err=%v", err)
	}
}

func TestDistillerImprovementPlanUsesMethodAndPolicyCandidatesWithoutMutatingTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
 "trace_ref": "trace_improvement_plan_candidates",
 "candidates": [
  {
   "kind": "method_upgrade",
   "subject": "distill.memory.dates",
   "statement": "ground relative dates against trace timestamp before emitting memory candidates",
   "detail": "date_grounding",
   "confidence": 0.91,
   "evidence_refs": ["trace:dates", "eval:before"]
  },
  {
   "kind": "policy_patch",
   "subject": "distill.promotion.policy",
   "statement": "reject model-supplied promotion_basis unless source adapter already proved basis",
   "detail": "promotion_basis_guard",
   "confidence": 0.93,
   "evidence_refs": ["trace:policy", "eval:adversarial"]
  }
 ]
}`)
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	methodRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade)))
	policyRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.PolicyPatch)))
	if len(methodRows) != 1 || methodRows[0].Verdict != distiller.Park {
		t.Fatalf("method candidates = %+v", methodRows)
	}
	if len(policyRows) != 1 || policyRows[0].Verdict != distiller.Park {
		t.Fatalf("policy candidates = %+v", policyRows)
	}
	targetPath := filepath.Join(t.TempDir(), "distiller-policy.md")
	original := "# Distiller Policy\n\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		row      distiller.Candidate
		wantKind string
		wantArea string
		wantKey  string
		wantText string
	}{
		{"method", methodRows[0], distiller.KindMethodUpgrade, "method", "distill-memory-dates", "trace timestamp"},
		{"policy", policyRows[0], distiller.KindPolicyPatch, "policy", "distill-promotion-policy", "promotion_basis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				cmdDistiller([]string{"improvement-plan", "--candidate", tc.row.ID, "--target", targetPath})
			})
			if !strings.Contains(out, "# distiller improvement-plan imp_") ||
				!strings.Contains(out, "- status: review_required") ||
				!strings.Contains(out, "- candidate_kind: "+tc.wantKind) ||
				!strings.Contains(out, "- area: "+tc.wantArea) ||
				!strings.Contains(out, "- improvement_key: "+tc.wantKey) ||
				!strings.Contains(out, "- target_exists: true") ||
				!strings.Contains(out, "- required_evidence: 4") ||
				!strings.Contains(out, "- evidence_refs: 2") ||
				!strings.Contains(out, "- plan:") ||
				!strings.Contains(out, "- review:") {
				t.Fatalf("improvement-plan output missing proof lines:\n%s", out)
			}
			planPath := outputValue(out, "- plan:")
			reviewPath := outputValue(out, "- review:")
			var plan distiller.ImprovementPlan
			planData, err := os.ReadFile(planPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(planData, &plan); err != nil {
				t.Fatal(err)
			}
			if plan.SchemaVersion != distiller.ImprovementPlanSchemaVersion ||
				plan.CandidateID != tc.row.ID ||
				plan.CandidateKind != tc.wantKind ||
				plan.Draft.Area != tc.wantArea ||
				!strings.Contains(plan.Draft.ProposedChange, tc.wantText) {
				t.Fatalf("bad improvement plan: %+v", plan)
			}
			review := readFile(t, reviewPath)
			if !strings.Contains(review, "does not mutate distiller policy") ||
				!strings.Contains(review, "Required Evidence Before Apply") {
				t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
			}
		})
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("distiller target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("improvement-plan must not promote claims, stat err=%v", err)
	}
}

func TestDistillerRulePlanUsesFailureCandidateWithoutMutatingRuleTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
 "trace_ref": "trace_failure_rule_candidate",
 "candidates": [{
  "kind": "failure_pattern",
  "subject": "dirty-source-control-hold",
  "statement": "agent repeatedly tries mutating a dirty repo without cleanup review",
  "detail": "repeated_policy_hold",
  "confidence": 0.91,
  "evidence_refs": ["trace:dirty-hold", "policy:hold"]
 }]
}`)
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	failureRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.FailurePattern)))
	if len(failureRows) != 1 || failureRows[0].Verdict != distiller.Park {
		t.Fatalf("failure candidates = %+v", failureRows)
	}
	targetPath := filepath.Join(t.TempDir(), "rules.md")
	original := "# Rules\n\nExisting rule source.\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdDistiller([]string{"rule-plan", "--candidate", failureRows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# distiller rule-plan frp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- failure_key: dirty-source-control-hold") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_evidence: 4") ||
		!strings.Contains(out, "- evidence_refs: 2") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("rule-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	var plan distiller.FailureRulePlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != distiller.FailureRulePlanSchemaVersion ||
		plan.CandidateID != failureRows[0].ID ||
		plan.CandidateVerdict != distiller.Park ||
		plan.RuleDraft.RuleID == "" ||
		!strings.Contains(plan.RuleDraft.Trigger, "agent repeatedly tries") {
		t.Fatalf("bad failure rule plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate doctrine or policy") ||
		!strings.Contains(review, "Required Evidence Before Apply") {
		t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("rule target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("rule-plan must not promote claims, stat err=%v", err)
	}
}

func TestDistillerTastePlanUsesTasteCandidateWithoutMutatingTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
 "trace_ref": "trace_taste_plan_candidate",
 "candidates": [{
  "kind": "taste",
  "subject": "product:sample-mobile-app:visuals",
  "statement": "screenshots should feel native, specific, and inspection-ready rather than generic marketing cards",
  "detail": "app_store_visuals",
  "confidence": 0.88,
  "evidence_refs": ["trace:taste", "asset:before-after"]
 }]
}`)
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	tasteRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.Taste)))
	if len(tasteRows) != 1 || tasteRows[0].Verdict != distiller.Park {
		t.Fatalf("taste candidates = %+v", tasteRows)
	}
	targetPath := filepath.Join(t.TempDir(), "taste.md")
	original := "# Taste\n\nExisting standard source.\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdDistiller([]string{"taste-plan", "--candidate", tasteRows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# distiller taste-plan tsp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- taste_key: product-sample-mobile-app-visuals") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_evidence: 4") ||
		!strings.Contains(out, "- evidence_refs: 2") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("taste-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	var plan distiller.TastePlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != distiller.TastePlanSchemaVersion ||
		plan.CandidateID != tasteRows[0].ID ||
		plan.CandidateVerdict != distiller.Park ||
		plan.StandardDraft.StandardID == "" ||
		!strings.Contains(plan.StandardDraft.PositiveStandard, "inspection-ready") {
		t.Fatalf("bad taste plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate doctrine or product files") ||
		!strings.Contains(review, "Required Evidence Before Apply") {
		t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("taste target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("taste-plan must not promote claims, stat err=%v", err)
	}
}

func TestRoutePlanUsesRouteObservationWithoutMutatingTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
 "trace_ref": "trace_route_policy_candidate",
 "candidates": [{
  "kind": "route_observation",
  "subject": "task:cheap-extract",
  "statement": "use cheap extraction lane for bounded candidate-only summaries when eval gate passes",
  "detail": "model_router_cost_guard",
  "confidence": 0.87,
  "evidence_refs": ["route:req_123", "transition:tr_route"]
 }]
}`)
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	routeRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.RouteObservation)))
	if len(routeRows) != 1 || routeRows[0].Verdict != distiller.Park {
		t.Fatalf("route candidates = %+v", routeRows)
	}
	targetPath := filepath.Join(t.TempDir(), "route-policy.json")
	original := `{"routes":[]}` + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdRoute([]string{"plan", "--candidate", routeRows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# route plan rpp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- route_key: task-cheap-extract") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_evidence: 4") ||
		!strings.Contains(out, "- evidence_refs: 2") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("route plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	var plan routeevidence.RoutePolicyPlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != routeevidence.RoutePolicyPlanSchemaVersion ||
		plan.CandidateID != routeRows[0].ID ||
		plan.CandidateVerdict != distiller.Park ||
		plan.PolicyDraft.PolicyID == "" ||
		!strings.Contains(plan.PolicyDraft.RoutePreference, "cheap extraction lane") {
		t.Fatalf("bad route policy plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate gateway/provider config") ||
		!strings.Contains(review, "Required Evidence Before Apply") {
		t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("route target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route plan must not promote claims, stat err=%v", err)
	}
}

func TestRouteOutcomeReviewUsesLedgerEvidenceAndReceiptWithoutMutatingTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "route", "provider-calls.ndjson")
	importOut := captureStdout(t, func() {
		cmdRoute([]string{"import-provider-ledger", fixture, "--tail", "1"})
	})
	routeID := outputValue(importOut, "- last_route_evidence:")
	if routeID == "" {
		t.Fatalf("import output missing route evidence id:\n%s", importOut)
	}
	receiptPath := writeJSONFile(t, `{
 "schema_version": "compound.receipt.v0.1",
 "id": "rec_route_outcome_cli",
 "created_at": "2026-06-29T12:00:00Z",
 "producer": {"kind": "fixture", "name": "compound"},
 "ids": {"work_id": "run_fixture_provider_2"},
 "scope": {"entity_id": "system:compound", "surface": "route"},
 "authority": {"source_class": "runtime", "status": "current"},
 "privacy": "private",
 "risk": "tier1",
 "evidence_refs": ["receipt:evidence"],
 "uncertainty": {"level": "known"},
 "outcome": "failed",
 "move_type": "outward_delta",
 "state_delta": {
  "changed": false,
  "summary": "cheap route failed quality gate",
  "verification": ["fixture quality review failed"]
 },
 "cost": {"model_calls": ["route_fixture"]},
 "skills": {"used": [], "missed": [], "candidate": []},
 "memory": {"candidates": [], "promoted": [], "parked": []},
 "next": {"suggested_work": [], "review_trigger": "review route quality before cheap-lane reuse"}
}`)
	targetPath := filepath.Join(t.TempDir(), "route-policy.json")
	original := `{"routes":[]}` + "\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdRoute([]string{"outcome-review", "--route-id", routeID, "--receipt", receiptPath, "--target", targetPath, "--record"})
	})
	for _, want := range []string{
		"# route outcome-review ror_",
		"- receipt_outcome: failed",
		"- assessment_class: route_remorse",
		"- should_review_policy: true",
		"- candidate: route_outcome_",
		"- no_promote: nop_",
		"- verdict: route_outcome_review_not_policy",
		"- plan:",
		"- review:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("outcome-review output missing %q:\n%s", want, out)
		}
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	var review routeevidence.RouteOutcomeReview
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &review); err != nil {
		t.Fatal(err)
	}
	if review.SchemaVersion != routeevidence.RouteOutcomeReviewSchemaVersion ||
		review.RouteEvidenceID != routeID ||
		review.ReceiptID != "rec_route_outcome_cli" ||
		review.CandidateID == "" ||
		review.NoPromoteID == "" {
		t.Fatalf("bad route outcome review: %+v", review)
	}
	text := readFile(t, reviewPath)
	if !strings.Contains(text, "does not mutate gateway/provider config") ||
		!strings.Contains(text, "Required Evidence Before Policy Change") ||
		!strings.Contains(text, "not billing truth") {
		t.Fatalf("review missing no-mutation/non-claim text:\n%s", text)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("route target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route outcome-review must not promote claims, stat err=%v", err)
	}
}

func TestRouteImportProviderLedgerWritesEvidenceWithoutClaims(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "route", "provider-calls.ndjson")
	out := captureStdout(t, func() {
		cmdRoute([]string{"import-provider-ledger", fixture, "--tail", "1"})
	})
	if !strings.Contains(out, "- imported: 1") || !strings.Contains(out, "- verdict: metadata_only_not_task_success") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	trID := outputValue(out, "- last_transition:")
	if trID == "" {
		t.Fatalf("missing transition in output:\n%s", out)
	}
	rowData := readFile(t, filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl"))
	if strings.Count(string(rowData), "\n") != 1 ||
		!strings.Contains(string(rowData), "req_fixture_provider_2") ||
		strings.Contains(string(rowData), "req_fixture_provider_1") ||
		strings.Contains(string(rowData), "ignored") {
		t.Fatalf("bad imported route evidence:\n%s", rowData)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", trID+".json")); err != nil {
		t.Fatalf("transition not recorded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("provider-ledger import must not promote claims, stat err=%v", err)
	}
}

func TestRouteCostDoctorRecordsReviewableObservation(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "route", "provider-calls-cost-gaps.ndjson")
	out := captureStdout(t, func() {
		cmdRoute([]string{"cost-doctor", fixture, "--record"})
	})
	if !strings.Contains(out, "- status: review") ||
		!strings.Contains(out, "- token_usage_missing_cost: 1") ||
		!strings.Contains(out, "- verdict: route_cost_observation_not_policy") ||
		!strings.Contains(out, "- candidate:") ||
		!strings.Contains(out, "- no_promote:") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	candidateID := outputValue(out, "- candidate:")
	candidatePath := filepath.Join(homePath, candidates.InboxDir(candidates.RouteObservation), candidateID+".json")
	candidateData := readFile(t, candidatePath)
	if !strings.Contains(string(candidateData), "provider ledger has 1 token-usage row") ||
		strings.Contains(string(candidateData), "ignored") {
		t.Fatalf("bad route-cost candidate:\n%s", candidateData)
	}
	noPromoteRows := readNoPromoteDecisionRows(t, homePath)
	if len(noPromoteRows) != 1 ||
		noPromoteRows[0].CandidateRef != candidateID ||
		noPromoteRows[0].OwnerTarget.Kind != "route_cost_coverage" {
		t.Fatalf("no-promote rows = %+v", noPromoteRows)
	}
	targetPath := filepath.Join(t.TempDir(), "route-policy.md")
	original := []byte("# route policy\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	planOut := captureStdout(t, func() {
		cmdRoute([]string{"plan", "--candidate", candidateID, "--target", targetPath})
	})
	if !strings.Contains(planOut, "# route plan") || !strings.Contains(planOut, "- status: review_required") {
		t.Fatalf("route plan did not consume cost observation:\n%s", planOut)
	}
	if got := readFile(t, targetPath); string(got) != string(original) {
		t.Fatalf("route policy target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("cost doctor must not promote claims, stat err=%v", err)
	}
}

func TestCapabilitySkillPlanUsesSkillCandidateWithoutMutatingGraphOrTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := writeJSONFile(t, `{
 "trace_ref": "trace_skill_candidate",
 "candidates": [{
  "kind": "skill",
  "subject": "frontend performance lens",
  "statement": "review React component rendering paths before adding memoization",
  "detail": "react_performance_review",
  "confidence": 0.88,
  "evidence_refs": ["trace:skill", "receipt:frontend"]
 }]
}`)
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	skillRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.Skill)))
	if len(skillRows) != 1 || skillRows[0].Verdict != distiller.Park {
		t.Fatalf("skill candidates = %+v", skillRows)
	}
	targetPath := filepath.Join(t.TempDir(), "SKILL.md")
	original := "# Existing Skill\n\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdCapability([]string{"skill-plan", "--candidate", skillRows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# capability skill-plan skp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- skill_key: frontend-performance-lens") ||
		!strings.Contains(out, "- node_id: skill:frontend-performance-lens") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_evidence: 4") ||
		!strings.Contains(out, "- evidence_refs: 2") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("skill-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	var plan capgraph.SkillPlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != capgraph.SkillPlanSchemaVersion ||
		plan.CandidateID != skillRows[0].ID ||
		plan.CandidateVerdict != distiller.Park ||
		plan.NodeDraft.NodeID != "skill:frontend-performance-lens" ||
		!strings.Contains(plan.NodeDraft.Implementation, "React component") {
		t.Fatalf("bad skill plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate CapabilityGraph") ||
		!strings.Contains(review, "Required Evidence Before Import") {
		t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("skill target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "registries", "capability-nodes.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skill-plan must not mutate capability graph, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skill-plan must not promote claims, stat err=%v", err)
	}
}

func TestCapabilitySkillCandidateReviewWritesReviewOnly(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	nodesPath := filepath.Join(homePath, "registries", "capability-nodes.jsonl")
	beforeNodes, err := os.ReadFile(nodesPath)
	if err != nil {
		t.Fatal(err)
	}
	candidatePath := writeJSONFile(t, `{
 "id": "example/skills/pricing",
 "name": "Pricing",
 "slug": "pricing",
 "description": "Review pricing and paywall packaging decisions.",
 "source": "example/skills",
 "source_type": "github",
 "install_url": "https://github.com/example/skills",
 "url": "https://skills.sh/example/skills/pricing",
 "installs": 428400,
 "official": true,
 "files": [{"path": "SKILL.md", "contents": "---\nname: pricing\n---\n"}],
 "audits": [{"provider": "Socket", "status": "pass", "summary": "No alerts", "risk_level": "LOW"}]
}`)
	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte("# staged only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-review", candidatePath, "--target", targetDir})
	})
	for _, want := range []string{
		"# capability skill-candidate-review scr_",
		"- candidate_kind: skill",
		"- classification: shared_skill",
		"- lifecycle_stage: staged_candidate",
		"- disposition: merge",
		"- overlaps: ",
		"- plan: ",
		"- review: ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skill-candidate-review output missing %q:\n%s", want, out)
		}
	}
	jsonOut := captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-review", candidatePath, "--target", targetDir, "--json"})
	})
	var parsed struct {
		Review capgraph.SkillCandidateReview `json:"review"`
		Paths  struct {
			PlanPath       string `json:"plan_path"`
			ReviewPath     string `json:"review_path"`
			ReviewYAMLPath string `json:"review_yaml_path"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("skill-candidate-review --json should parse: %v\n%s", err, jsonOut)
	}
	if parsed.Review.ID == "" || parsed.Review.GraphCoherence.DecisionHint != "merge_existing" {
		t.Fatalf("json review missing graph coherence: %+v", parsed.Review)
	}
	if parsed.Paths.PlanPath == "" || parsed.Paths.ReviewPath == "" || parsed.Paths.ReviewYAMLPath == "" {
		t.Fatalf("json paths missing: %+v", parsed.Paths)
	}
	if entries, err := os.ReadDir(filepath.Join(homePath, "reviews", "skill-candidates")); err != nil || len(entries) < 2 {
		t.Fatalf("expected skill candidate review artifacts, entries=%v err=%v", entries, err)
	}
	afterNodes, err := os.ReadFile(nodesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterNodes) != string(beforeNodes) {
		t.Fatalf("skill-candidate-review must not mutate graph nodes")
	}
	if entries, err := os.ReadDir(filepath.Join(homePath, candidates.InboxDir(candidates.Skill))); err == nil && len(entries) != 0 {
		t.Fatalf("skill-candidate-review must not write skill inbox candidate, entries=%v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read skill inbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skill-candidate-review must not promote claims, stat err=%v", err)
	}
}

func TestCapabilitySkillCandidateDecisionJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	candidatePath := writeJSONFile(t, `{
		"id": "example/skills/web-design",
		"name": "Web Design",
		"description": "Review web UI layout and accessibility.",
		"source": "example/skills",
		"url": "https://skills.sh/example/skills/web-design",
		"files": [{"path": "SKILL.md", "contents": "---\nname: web-design\n---\n"}]
	}`)
	captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-review", candidatePath})
	})
	reviewDir := filepath.Join(homePath, "reviews", "skill-candidates")
	var reviewYAML string
	entries, err := os.ReadDir(reviewDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".review.yaml") {
			reviewYAML = filepath.Join(reviewDir, entry.Name())
		}
	}
	if reviewYAML == "" {
		t.Fatal("review yaml not written")
	}
	body := readFile(t, reviewYAML)
	body = strings.ReplaceAll(body, "TODO_decision_create_candidate_merge_existing_reject_reference_only_keep_needs_review", "merge_existing")
	body = strings.ReplaceAll(body, "rationale: \"\"", "rationale: \"existing skill owner should absorb useful parts\"")
	body = strings.ReplaceAll(body, "confidence: -1", "confidence: 0.75")
	if err := os.WriteFile(reviewYAML, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonOut := captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-decision", reviewYAML, "--json"})
	})
	var report capgraph.SkillCandidateDecisionReport
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("skill-candidate-decision --json should parse: %v\n%s", err, jsonOut)
	}
	if report.Status != "recorded" || report.Decision != "merge_existing" || report.ReviewID == "" {
		t.Fatalf("unexpected decision report: %+v", report)
	}
	if report.CandidateID != "" || report.CandidatePath != "" {
		t.Fatalf("merge_existing should not create a skill candidate: %+v", report)
	}
	reviewedYAML := readFile(t, reviewYAML)
	refreshJSONOut := captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-review", candidatePath, "--json"})
	})
	var refreshParsed struct {
		Review capgraph.SkillCandidateReview `json:"review"`
		Paths  struct {
			ReviewYAMLPath string `json:"review_yaml_path"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(refreshJSONOut), &refreshParsed); err != nil {
		t.Fatalf("refreshed skill-candidate-review --json should parse: %v\n%s", err, refreshJSONOut)
	}
	if refreshParsed.Review.Status != "recorded" || refreshParsed.Review.NextAction != "skill candidate intake decision recorded" {
		t.Fatalf("refreshed review should keep terminal decision status: %+v", refreshParsed.Review)
	}
	if got := readFile(t, reviewYAML); got != reviewedYAML || strings.Contains(got, "TODO_decision") {
		t.Fatalf("review refresh should preserve reviewed YAML:\nbefore=%s\nafter=%s", reviewedYAML, got)
	}
	if refreshParsed.Paths.ReviewYAMLPath != reviewYAML {
		t.Fatalf("refreshed review yaml path = %q, want %q", refreshParsed.Paths.ReviewYAMLPath, reviewYAML)
	}
}

func TestCapabilitySkillCandidateDecisionFeedsSkillPlan(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	nodesPath := filepath.Join(homePath, "registries", "capability-nodes.jsonl")
	beforeNodes, err := os.ReadFile(nodesPath)
	if err != nil {
		t.Fatal(err)
	}
	candidatePath := writeJSONFile(t, `{
 "id": "example/skills/pricing",
 "name": "Pricing",
 "slug": "pricing",
 "description": "Review pricing and paywall packaging decisions.",
 "source": "example/skills",
 "source_type": "github",
 "url": "https://skills.sh/example/skills/pricing",
 "files": [{"path": "SKILL.md", "contents": "---\nname: pricing\n---\n"}]
}`)
	captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-review", candidatePath})
	})
	reviewDir := filepath.Join(homePath, "reviews", "skill-candidates")
	var reviewYAML string
	entries, err := os.ReadDir(reviewDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".review.yaml") {
			reviewYAML = filepath.Join(reviewDir, entry.Name())
		}
	}
	if reviewYAML == "" {
		t.Fatal("review yaml not written")
	}
	body := readFile(t, reviewYAML)
	body = strings.ReplaceAll(body, "TODO_decision_create_candidate_merge_existing_reject_reference_only_keep_needs_review", "create_candidate")
	body = strings.ReplaceAll(body, "rationale: \"\"", "rationale: \"reviewed source should enter skill-plan intake\"")
	body = strings.ReplaceAll(body, "confidence: -1", "confidence: 0.8")
	if err := os.WriteFile(reviewYAML, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdCapability([]string{"skill-candidate-decision", reviewYAML})
	})
	for _, want := range []string{
		"# capability skill-candidate-decision scdr_",
		"- status: candidate_recorded",
		"- decision: create_candidate",
		"- candidate: example-skills-pricing",
		"- candidate_path: ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skill-candidate-decision output missing %q:\n%s", want, out)
		}
	}
	planOut := captureStdout(t, func() {
		cmdCapability([]string{"skill-plan", "--candidate", "example-skills-pricing"})
	})
	if !strings.Contains(planOut, "# capability skill-plan skp_") || !strings.Contains(planOut, "- candidate: example-skills-pricing") {
		t.Fatalf("skill-plan did not consume reviewed candidate:\n%s", planOut)
	}
	afterNodes, err := os.ReadFile(nodesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterNodes) != string(beforeNodes) {
		t.Fatalf("skill-candidate decision/plan must not mutate graph nodes")
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skill-candidate decision/plan must not promote claims, stat err=%v", err)
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
		cmdAuthority([]string{"resolve", "appstore:app-demo-sourcecontract"})
	})
	if !strings.Contains(resolved, "status: resolved") || !strings.Contains(resolved, "entity: product:source_contract_demo") {
		t.Fatalf("app store identifier did not resolve to product entity:\n%s", resolved)
	}
	authJSON := captureStdout(t, func() {
		cmdProduct([]string{"authority", "--json"})
	})
	var auth product.AuthorityReport
	if err := json.Unmarshal([]byte(authJSON), &auth); err != nil {
		t.Fatalf("parse product authority JSON: %v\n%s", err, authJSON)
	}
	if auth.AuthorityRef != product.AuthorityRef || auth.ProductCount != 1 || auth.Products[0].ProductID != "product:source_contract_demo" {
		t.Fatalf("product authority = %+v", auth)
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

func TestProductChangeStreamImportsSourceIssues(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "product", "change-stream-candidates.md")
	out := captureStdout(t, func() {
		cmdProduct([]string{"change-stream", fixture})
	})
	if !strings.Contains(out, "# product change-stream change_stream_candidates:change-stream-candidates.md") ||
		!strings.Contains(out, "- candidates_read: 2") ||
		!strings.Contains(out, "- issues_created: 2") ||
		!strings.Contains(out, "- product: product:focus_notes") {
		t.Fatalf("change-stream output missing proof:\n%s", out)
	}
	replay := captureStdout(t, func() {
		cmdProduct([]string{"change-stream", fixture})
	})
	if !strings.Contains(replay, "- issues_created: 0") || !strings.Contains(replay, "- issues_existing: 2") {
		t.Fatalf("change-stream replay should be idempotent:\n%s", replay)
	}
	doctor, err := product.Doctor(homePath, "product:focus_notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.SourceIssueCount != 2 {
		t.Fatalf("product doctor should surface change-stream source issues: %+v", doctor)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("change-stream import should not create promoted claims ledger: %v", err)
	}
}

func TestProductSourceIssuePlanUsesOpenSourceIssueWithoutClosingIt(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "product", "change-stream-candidates.md")
	importOut := captureStdout(t, func() {
		cmdProduct([]string{"change-stream", fixture})
	})
	issueID := outputValue(importOut, "- issue:")
	if issueID == "" {
		t.Fatalf("change-stream output missing issue:\n%s", importOut)
	}
	targetPath := filepath.Join(t.TempDir(), "product-review.md")
	original := "# Product Review\n\nExisting product review notes.\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{"source-issue-plan", "--product", "product:focus_notes", "--issue", issueID, "--target", targetPath})
	})
	if !strings.Contains(out, "# product source-issue-plan psip_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- product: product:focus_notes") ||
		!strings.Contains(out, "- issue: "+issueID) ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_evidence: 4") ||
		!strings.Contains(out, "- evidence_refs:") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") ||
		!strings.Contains(out, "- review_yaml:") {
		t.Fatalf("source-issue-plan output missing proof:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	reviewYAMLPath := outputValue(out, "- review_yaml:")
	var plan product.SourceIssuePlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != product.SourceIssuePlanSchemaVersion || plan.IssueID != issueID || plan.ReviewDraft.ClosureCommand == "" {
		t.Fatalf("bad source issue plan = %+v", plan)
	}
	reviewData, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reviewData), "result: resolved|waived|false_positive") {
		t.Fatalf("review artifact missing closure template:\n%s", string(reviewData))
	}
	reviewYAMLData, err := os.ReadFile(reviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reviewYAMLData), "TODO_resolved_waived_or_false_positive") {
		t.Fatalf("review YAML should require explicit review result:\n%s", string(reviewYAMLData))
	}
	draft, err := product.LoadSourceIssueReview(reviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := product.ValidateSourceIssueReview(draft); err == nil {
		t.Fatal("generated review YAML draft should not validate before reviewer selects a result")
	}
	afterTarget, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterTarget) != original {
		t.Fatal("source-issue-plan mutated target")
	}
	doctor, err := product.Doctor(homePath, "product:focus_notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.SourceIssueCount != 2 {
		t.Fatalf("source issue plan should not close issue, doctor = %+v", doctor)
	}
	reviewedYAML := strings.ReplaceAll(string(reviewYAMLData), "TODO_resolved_waived_or_false_positive", "false_positive")
	reviewedYAML = strings.ReplaceAll(reviewedYAML, "TODO: reviewed source owner evidence", "reviewed source issue was not durable product change")
	reviewedPath := filepath.Join(t.TempDir(), "source-issue-review.yaml")
	if err := os.WriteFile(reviewedPath, []byte(reviewedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewOut := captureStdout(t, func() {
		cmdProduct([]string{"source-issue-review", reviewedPath})
	})
	if !strings.Contains(reviewOut, "# product source issue review "+plan.ReviewDraft.ReviewID) ||
		!strings.Contains(reviewOut, "- status: closed") ||
		!strings.Contains(reviewOut, "- issue: "+issueID) ||
		!strings.Contains(reviewOut, "- transition:") {
		t.Fatalf("source-issue-review output missing closure proof:\n%s", reviewOut)
	}
	afterClosure, err := product.Doctor(homePath, "product:focus_notes")
	if err != nil {
		t.Fatal(err)
	}
	if afterClosure.Status != "needs_review" || afterClosure.SourceIssueCount != 1 {
		t.Fatalf("closing one generated source issue should leave one open issue, doctor = %+v", afterClosure)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("source issue plan should not create promoted claims ledger: %v", err)
	}
}

func TestProductSourceIssueReviewResolvedFollowUpCreatesHypothesisCandidate(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "product", "change-stream-candidates.md")
	importOut := captureStdout(t, func() {
		cmdProduct([]string{"change-stream", fixture})
	})
	issueID := outputValue(importOut, "- issue:")
	if issueID == "" {
		t.Fatalf("change-stream output missing issue:\n%s", importOut)
	}
	reviewPath := filepath.Join(t.TempDir(), "source-issue-review.yaml")
	reviewYAML := `schema_version: compound.product_source_issue_review.v0.1
id: source_issue_review_follow_up_cli
product_id: product:focus_notes
issue_id: ` + issueID + `
result: resolved
rationale: "reviewed source issue represents a real change worth measuring, not a metric claim yet"
evidence_refs:
  - fixture:source-issue-review
follow_up:
  change_event: "Reviewed change-stream candidate as real App Store screenshot change"
  expected_effect: "clearer first screenshot may improve product page conversion"
  metrics:
    - app_store_product_page_conversion
  observation_window: "14d after reviewed change"
  next_action: "import an explicit ProductSignal after the analytics window"
  evidence_refs:
    - fixture:source-issue-follow-up
`
	if err := os.WriteFile(reviewPath, []byte(reviewYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{"source-issue-review", reviewPath})
	})
	hypothesisID := outputValue(out, "- hypothesis:")
	if !strings.Contains(out, "- status: closed") ||
		!strings.Contains(out, "- transition:") ||
		hypothesisID == "" {
		t.Fatalf("source-issue-review follow-up output missing proof:\n%s", out)
	}
	tl, err := product.LoadTimeline(homePath, "product:focus_notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Hypotheses) != 1 || tl.Hypotheses[0].ID != hypothesisID {
		t.Fatalf("timeline hypotheses = %+v, want %s", tl.Hypotheses, hypothesisID)
	}
	doctor, err := product.Doctor(homePath, "product:focus_notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.PendingReviewCount != 1 || doctor.SourceIssueCount != 1 {
		t.Fatalf("doctor should keep follow-up review debt visible: %+v", doctor)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "product-signals.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("follow-up should not create ProductSignal ledger: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("follow-up should not create promoted claims ledger: %v", err)
	}
}

func TestProductCanonicalMetricsCLI(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "product", "canonical-latest.json")
	out := captureStdout(t, func() {
		cmdProduct([]string{"canonical-metrics", fixture, "--product", "product:focus-notes"})
	})
	if !strings.Contains(out, "# product canonical-metrics product_metrics:canonical_latest:focus_notes:2026_07_13") ||
		!strings.Contains(out, "- signals_created: 2") ||
		!strings.Contains(out, "- hypotheses_created: 1") ||
		!strings.Contains(out, "- hypothesis: focus-notes.appstore-low-conversion") {
		t.Fatalf("canonical-metrics output missing proof:\n%s", out)
	}
	doctor, err := product.Doctor(homePath, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.ReviewReadyCount != 2 {
		t.Fatalf("doctor = %+v", doctor)
	}
}

func TestProductHypothesisPlanUsesDistillerCandidateWithoutMutatingTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	tracePath := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(tracePath, []byte(`{
  "trace_ref": "trace_product_hypothesis",
  "candidates": [{
    "kind": "product_hypothesis",
    "subject": "product:focus-notes:app-store",
    "statement": "screenshot clarity may improve product page conversion",
    "detail": "Changed first screenshot to show fast capture moment",
    "confidence": 0.82,
    "evidence_refs": ["trace:product-hypothesis", "product_signal:sig_focus"]
  }]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		cmdDistiller([]string{"run", tracePath})
	})
	rows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.ProductHypothesis)))
	if len(rows) != 1 || rows[0].Verdict != distiller.Park {
		t.Fatalf("product hypothesis candidates = %+v", rows)
	}
	targetPath := filepath.Join(t.TempDir(), "product-strategy.md")
	original := "# Product Strategy\n\nExisting strategy.\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{"hypothesis-plan", "--candidate", rows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# product hypothesis-plan php_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- product: product:focus-notes") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- required_evidence: 4") ||
		!strings.Contains(out, "- evidence_refs: 2") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") ||
		!strings.Contains(out, "- signal_draft:") {
		t.Fatalf("hypothesis-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	signalDraftPath := outputValue(out, "- signal_draft:")
	var plan product.HypothesisPlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != product.HypothesisPlanSchemaVersion ||
		plan.CandidateID != rows[0].ID ||
		plan.CandidateVerdict != distiller.Park ||
		plan.ProductID != "product:focus-notes" ||
		plan.ReviewDraft.DecisionGate == "" ||
		!strings.Contains(plan.ExpectedEffect, "conversion") {
		t.Fatalf("bad hypothesis plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate product strategy") ||
		!strings.Contains(review, "Required Evidence Before Action") {
		t.Fatalf("review missing no-mutation/procedure text:\n%s", review)
	}
	signalDraft := readFile(t, signalDraftPath)
	if !strings.Contains(signalDraft, "TODO_before_value") ||
		!strings.Contains(signalDraft, "product_hypothesis:"+rows[0].ID) {
		t.Fatalf("signal draft missing guarded placeholders/evidence:\n%s", signalDraft)
	}
	if _, err := product.LoadSignal(signalDraftPath); err == nil {
		t.Fatal("generated signal draft should not load before reviewer supplies metric values")
	}
	signalYAML := strings.ReplaceAll(signalDraft, "TODO_provider", "app_store_connect")
	signalYAML = strings.ReplaceAll(signalYAML, "TODO_surface", "app-store-page")
	signalYAML = strings.ReplaceAll(signalYAML, "TODO_before_value", "0.041")
	signalYAML = strings.ReplaceAll(signalYAML, "TODO_after_value", "0.049")
	signalPath := filepath.Join(t.TempDir(), "product-signal.yaml")
	if err := os.WriteFile(signalPath, []byte(signalYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	signalOut := captureStdout(t, func() {
		cmdProduct([]string{"signal", signalPath})
	})
	if !strings.Contains(signalOut, "# product signal") ||
		!strings.Contains(signalOut, "- status: ok") ||
		!strings.Contains(signalOut, "- transition:") {
		t.Fatalf("edited signal draft did not import as ProductSignal:\n%s", signalOut)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("hypothesis plan mutated target:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("hypothesis plan must not promote claims, stat err=%v", err)
	}
}

func TestProductReviewPlanUsesSignalEvidenceWithoutResolvingHypothesis(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	candidateID := "hyp_cli_review_plan"
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := product.HypothesisCandidate{
		SchemaVersion:     product.ProductHypothesisCandidate,
		ID:                candidateID,
		Kind:              string(candidates.ProductHypothesis),
		ProductID:         "product:focus_notes",
		ChangeEvent:       "Changed first screenshot to show faster capture.",
		ExpectedEffect:    "product page conversion improves",
		Metrics:           []string{"app_store_product_page_conversion"},
		ObservationWindow: "14d",
		Result:            "unknown",
		NextAction:        "review explicit signal evidence",
		Verdict:           candidates.Park,
		EvidenceRefs:      []string{"product_source_issue_review:cli"},
	}
	candidateData, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, candidateID+".json"), append(candidateData, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	signalID := "sig_cli_review_plan"
	signalPath := filepath.Join(t.TempDir(), "signal.yaml")
	if err := os.WriteFile(signalPath, []byte(`schema_version: compound.product_signal.v0.1
source:
 kind: app_store_connect
 ref: fixture:cli-review-plan
signal:
 id: sig_cli_review_plan
 product_id: product:focus_notes
 surface: app-store-page
 change_event: "Changed first screenshot to show faster capture."
 metric_name: app_store_product_page_conversion
 before: 0.041
 after: 0.049
 window: "14d"
 confidence: 0.66
 confounders:
  - "paid acquisition changed during window"
 evidence_refs:
  - asc:focus:conversion-window
`), 0o644); err != nil {
		t.Fatal(err)
	}
	signalOut := captureStdout(t, func() {
		cmdProduct([]string{"signal", signalPath})
	})
	if !strings.Contains(signalOut, "# product signal "+signalID) ||
		!strings.Contains(signalOut, "- status: ok") ||
		!strings.Contains(signalOut, "- transition:") {
		t.Fatalf("signal output missing proof:\n%s", signalOut)
	}
	doctorOut := captureStdout(t, func() {
		cmdProduct([]string{"doctor", "product:focus_notes"})
	})
	if !strings.Contains(doctorOut, "- review_ready: 1") ||
		!strings.Contains(doctorOut, "- review_plan: compound product review-plan --hypothesis "+candidateID+" --signal "+signalID) ||
		!strings.Contains(doctorOut, "match: metric_match app_store_product_page_conversion 14d") {
		t.Fatalf("doctor should surface review-ready signal pair:\n%s", doctorOut)
	}
	targetPath := filepath.Join(t.TempDir(), "product-review.md")
	original := "# Product Review\n\nExisting review notes.\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdProduct([]string{"review-plan", "--hypothesis", candidateID, "--signal", signalID, "--target", targetPath})
	})
	if !strings.Contains(out, "# product review-plan prp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- hypothesis: "+candidateID) ||
		!strings.Contains(out, "- signal: "+signalID) ||
		!strings.Contains(out, "- review_yaml:") {
		t.Fatalf("review-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewYAMLPath := outputValue(out, "- review_yaml:")
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan product.ReviewPlan
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != product.ReviewPlanSchemaVersion ||
		plan.HypothesisID != candidateID ||
		plan.SignalID != signalID ||
		plan.ReviewDraft.ReviewID == "" {
		t.Fatalf("bad review plan: %+v", plan)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	unedited, err := product.LoadReview(reviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.ApplyReview(homePath, store, unedited, reviewYAMLPath, cliTestNow.Add(time.Hour)); err == nil {
		t.Fatal("unedited review YAML should be rejected")
	}
	reviewYAML := readFile(t, reviewYAMLPath)
	reviewYAML = strings.ReplaceAll(reviewYAML, "TODO_result_supported_refuted_or_inconclusive", "supported")
	reviewYAML = strings.ReplaceAll(reviewYAML, "rationale: \"\"", "rationale: \"signal moved in the expected direction after confounder review\"")
	reviewYAML = strings.ReplaceAll(reviewYAML, "confidence: -1", "confidence: 0.61")
	if err := os.WriteFile(reviewYAMLPath, []byte(reviewYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewOut := captureStdout(t, func() {
		cmdProduct([]string{"review", reviewYAMLPath})
	})
	if !strings.Contains(reviewOut, "# product review "+plan.ReviewDraft.ReviewID) ||
		!strings.Contains(reviewOut, "- status: updated") ||
		!strings.Contains(reviewOut, "- transition:") {
		t.Fatalf("product review output missing proof:\n%s", reviewOut)
	}
	doctor, err := product.Doctor(homePath, "product:focus_notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ok" || doctor.PendingReviewCount != 0 {
		t.Fatalf("review should clear pending hypothesis debt: %+v", doctor)
	}
	reviewsOut := captureStdout(t, func() {
		cmdReviews([]string{"--area", "product-reviews", "--json"})
	})
	var queue reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(reviewsOut), &queue); err != nil {
		t.Fatalf("parse review queue: %v\n%s", err, reviewsOut)
	}
	if queue.Counts.Total != 1 || queue.Counts.ReviewRequired != 0 || queue.Counts.OK != 1 {
		t.Fatalf("review queue should treat recorded product review terminal: %+v", queue.Counts)
	}
	if len(queue.Items) != 1 || queue.Items[0].Status != "recorded" || queue.Items[0].Readiness != "no_action" || len(queue.Items[0].Actions) != 0 {
		t.Fatalf("product review queue item should be terminal: %+v", queue.Items)
	}
	afterDoctorOut := captureStdout(t, func() {
		cmdProduct([]string{"doctor", "product:focus_notes"})
	})
	if !strings.Contains(afterDoctorOut, "- review_ready: 0") ||
		strings.Contains(afterDoctorOut, "- review_plan:") {
		t.Fatalf("doctor should clear review-ready pair after review:\n%s", afterDoctorOut)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("review-plan mutated target:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("review-plan must not promote claims, stat err=%v", err)
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

func TestCapabilityImportSkillsRecordsSourceAuthority(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	})
	if !strings.Contains(out, "- source_authority:") {
		t.Fatalf("import-skills output missing source authority marker:\n%s", out)
	}
	captureStdout(t, func() {
		cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	})

	records, err := capgraph.LoadSourceAuthority(homePath)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("source authority records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.AuthorityStatus != "compound_authoritative" || rec.SourceRole != "evidence_only_input" {
		t.Fatalf("wrong authority status/role: %+v", rec)
	}
	if rec.Retires != "source_view_authority:fixtures/skills/skill-manifest.yaml" {
		t.Fatalf("retired source authority = %q", rec.Retires)
	}
	if !stringSliceContains(rec.NodeIDs, "skill:pricing") || rec.SourceObservationID == "" || len(rec.NoPromoteIDs) != 1 {
		t.Fatalf("authority marker missing proof fields: %+v", rec)
	}

	authorityOut := captureStdout(t, func() { cmdCapability([]string{"authority"}) })
	if !strings.Contains(authorityOut, rec.ID) || !strings.Contains(authorityOut, "compound_authoritative") {
		t.Fatalf("capability authority output missing marker:\n%s", authorityOut)
	}
	if strings.Count(authorityOut, rec.ID) != 1 {
		t.Fatalf("capability authority output should collapse replayed imports:\n%s", authorityOut)
	}
	authorityJSON := captureStdout(t, func() { cmdCapability([]string{"authority", "--json"}) })
	var authorityReport struct {
		SchemaVersion               string                           `json:"schema_version"`
		AuthorityRef                string                           `json:"authority_ref"`
		SourceRoleCounts            map[string]int                   `json:"source_role_counts"`
		RetiredSourceAuthorityCount int                              `json:"retired_source_authority_count"`
		RetiredSourceAuthorityRefs  []string                         `json:"retired_source_authority_refs"`
		Records                     []capgraph.SourceAuthorityRecord `json:"records"`
	}
	if err := json.Unmarshal([]byte(authorityJSON), &authorityReport); err != nil {
		t.Fatalf("parse authority json: %v\n%s", err, authorityJSON)
	}
	if authorityReport.SchemaVersion != capgraph.SourceAuthoritySchemaVersion ||
		authorityReport.AuthorityRef != "compound://registries/capability-graph" ||
		authorityReport.SourceRoleCounts["evidence_only_input"] != 1 ||
		authorityReport.RetiredSourceAuthorityCount != 1 ||
		!stringSliceContains(authorityReport.RetiredSourceAuthorityRefs, "source_view_authority:fixtures/skills/skill-manifest.yaml") ||
		len(authorityReport.Records) != 1 ||
		authorityReport.Records[0].ID != rec.ID ||
		authorityReport.Records[0].SourceRole != "evidence_only_input" {
		t.Fatalf("unexpected authority json report: %+v", authorityReport)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("capability source authority must not promote claims, stat err=%v", err)
	}
}

func TestCapabilityImportPluginBoardRecordsSourceAuthority(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"import-skills", "--os-plugin-board-json", filepath.Join("..", "..", "fixtures", "skills", "os-plugin-board.json")})
	})
	if !strings.Contains(out, "- source_authority:") || !strings.Contains(out, "- node: adapter:plugin:browser-openai-bundled") {
		t.Fatalf("plugin-board import output missing authority/node marker:\n%s", out)
	}
	records, err := capgraph.LoadSourceAuthority(homePath)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("source authority records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.SourceClass != "os-plugin-serving-board-json" || rec.SourceObservationID == "" || len(rec.NoPromoteIDs) != 1 {
		t.Fatalf("plugin authority marker missing proof fields: %+v", rec)
	}
	if !stringSliceContains(rec.NodeIDs, "adapter:plugin:browser-openai-bundled") {
		t.Fatalf("plugin authority missing adapter node: %+v", rec.NodeIDs)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("plugin-board import must not promote claims, stat err=%v", err)
	}
}

func TestCapabilityImportTopologyRecordsSourceObservationNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"import-topology", filepath.Join("..", "..", "fixtures", "tooltopology", "browser-cli-vs-mcp.yaml")})
	})
	for _, want := range []string{"# tool topology topo_fixture_browser", "- node: tool:browser-cli", "- edge: fallback_for:mcp:browser->tool:browser-cli", "- source_observation:", "- source_candidate:", "- source_no_promote:", "- source_authority:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-topology output missing %q:\n%s", want, out)
		}
	}
	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdFallbackFor, "mcp:browser") {
		t.Fatalf("fallback edge not imported: %+v", edges)
	}
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read capability observations: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("capability observation entries = %d, want 1", len(entries))
	}
	noPromoteRows := readNoPromoteDecisionRows(t, homePath)
	if len(noPromoteRows) != 1 || noPromoteRows[0].OwnerTarget.Kind != "source_observation" {
		t.Fatalf("no-promote rows = %+v", noPromoteRows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("topology import should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestCapabilityImportMCPorterCatalogRecordsSourceObservationNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdCapability([]string{"import-topology", "--mcporter-json", filepath.Join("..", "..", "fixtures", "tooltopology", "mcporter-list.json")})
	})
	for _, want := range []string{
		"# tool topology topo_mcporter_",
		"- node: mcp:context7",
		"- node: tool:context7.get-library-docs",
		"- node: mcp:cloudflare-api",
		"- source_observation:",
		"- source_candidate:",
		"- source_no_promote:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mcporter import output missing %q:\n%s", want, out)
		}
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Surface("current library docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Node.ID != "tool:context7.get-library-docs" {
		t.Fatalf("mcporter docs tool did not surface: %+v", hits)
	}
	authHits, err := store.Surface("cloudflare api")
	if err != nil {
		t.Fatal(err)
	}
	if len(authHits) != 0 {
		t.Fatalf("auth-blocked mcporter server should not surface: %+v", authHits)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("mcporter source import should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestCapabilityImportSkillsRecordsSourceObservationNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	})
	for _, want := range []string{"# capability import-skills skill_manifest_fixture", "- node: skill:pricing", "- edge: requires:skill:pricing->tool:analytics", "- source_observation:", "- source_candidate:", "- source_no_promote:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-skills output missing %q:\n%s", want, out)
		}
	}
	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdRequires, "skill:pricing") {
		t.Fatalf("requires edge not imported: %+v", edges)
	}
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read capability observations: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("capability observation entries = %d, want 1", len(entries))
	}
	noPromoteRows := readNoPromoteDecisionRows(t, homePath)
	if len(noPromoteRows) != 1 || noPromoteRows[0].OwnerTarget.Kind != "source_observation" {
		t.Fatalf("no-promote rows = %+v", noPromoteRows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("skill import should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestCapabilitySurfaceJSONFiltersByHarness(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml")})
	})

	out := captureStdout(t, func() {
		cmdCapability([]string{"surface", "--harness", "codex", "--json", "pricing"})
	})
	var report capgraph.SurfaceReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("surface JSON did not parse: %v\n%s", err, out)
	}
	if report.SchemaVersion != capgraph.SurfaceSchemaVersion ||
		report.AuthorityRef != capgraph.CapabilityGraphAuthorityRef ||
		report.Status != "ok" ||
		report.Task != "pricing" ||
		report.Harness != "codex" ||
		report.NodeCount == 0 ||
		report.OfferCount != len(report.Offers) ||
		len(report.OfferSummaries) != len(report.Offers) {
		t.Fatalf("unexpected surface metadata: %+v", report)
	}
	var ids []string
	for _, offer := range report.Offers {
		ids = append(ids, offer.Node.ID)
	}
	if !stringSliceContains(ids, "skill:codex-pricing") || !stringSliceContains(ids, "skill:generic-pricing") {
		t.Fatalf("codex/generic offers missing: %+v", ids)
	}
	if stringSliceContains(ids, "skill:claude-pricing") {
		t.Fatalf("claude-only offer leaked into codex surface: %+v", ids)
	}
	var summaryIDs []string
	for _, offer := range report.OfferSummaries {
		summaryIDs = append(summaryIDs, offer.ID)
		if offer.ID == "skill:codex-pricing" && (offer.Kind != "skill" || offer.Name == "" || offer.CostRiskNote == "") {
			t.Fatalf("codex summary missing flat fields: %+v", offer)
		}
	}
	if !stringSliceContains(summaryIDs, "skill:codex-pricing") || stringSliceContains(summaryIDs, "skill:claude-pricing") {
		t.Fatalf("summary ids = %+v", summaryIDs)
	}
	if !hasMatchedTokenCount(report.MatchedTokenCounts, "pricing", 2) {
		t.Fatalf("matched token counts = %+v, want pricing=2", report.MatchedTokenCounts)
	}
}

func TestCapabilityImportEvaluationsRecordsGraphEvidenceAndSourceNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"add", "skill:web-design-guidelines", "skill", "Web Design Guidelines", "--triggers", "web,design"})
		cmdCapability([]string{"import-evaluations", filepath.Join("..", "..", "fixtures", "capability-evaluations", "web-design-guidelines.json")})
	})
	for _, want := range []string{"# capability import-evaluations", "- eval_node: eval:capability-evaluation-external-vercel-web-design-guidelines", "- edge: evaluated_by:skill:web-design-guidelines->eval:capability-evaluation-external-vercel-web-design-guidelines", "- edge: validated_by:skill:web-design-guidelines->eval:capability-evaluation-external-vercel-web-design-guidelines", "- source_observation:", "- source_candidate:", "- source_no_promote:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-evaluations output missing %q:\n%s", want, out)
		}
	}
	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdEvaluatedBy, "skill:web-design-guidelines") || !hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:web-design-guidelines") {
		t.Fatalf("expected evaluation edges, got %+v", edges)
	}
	surfaceOut := captureStdout(t, func() {
		cmdCapability([]string{"surface", "web design guidelines"})
	})
	if !strings.Contains(surfaceOut, "skill:web-design-guidelines") {
		t.Fatalf("surface output missing skill offer:\n%s", surfaceOut)
	}
	if strings.Contains(surfaceOut, "\n  eval:capability-evaluation") {
		t.Fatalf("evaluation evidence node must not surface as a capability offer row:\n%s", surfaceOut)
	}
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read capability observations: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("capability observation entries = %d, want 1", len(entries))
	}
	noPromoteRows := readNoPromoteDecisionRows(t, homePath)
	if len(noPromoteRows) != 1 || noPromoteRows[0].OwnerTarget.Kind != "source_observation" {
		t.Fatalf("no-promote rows = %+v", noPromoteRows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("evaluation import should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestCapabilityImportEvaluationsSurfacesUnknownReviewDebt(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"import-evaluations", filepath.Join("..", "..", "fixtures", "capability-evaluations", "web-design-guidelines.json")})
	})
	for _, want := range []string{
		"- status: review",
		"- evaluations_read: 1",
		"- applied_evaluations: 0",
		"- skipped_unknown_count: 1",
		"- skipped_unknown: web-design-guidelines",
		"- source_no_promote:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-evaluations output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("unknown evaluation import must not promote claims, stat err=%v", err)
	}
}

func TestCapabilityImportSurfacingMissesRecordsFeedbackAndSourceNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	cmdCapability([]string{"add", "skill:web-design-guidelines", "skill", "Web Design Guidelines", "--triggers", "css"})
	beforeOut := captureStdout(t, func() {
		cmdCapability([]string{"surface", "app store design review"})
	})
	if strings.Contains(beforeOut, "skill:web-design-guidelines") {
		t.Fatalf("precondition: skill surfaced before report feedback:\n%s", beforeOut)
	}
	out := captureStdout(t, func() {
		cmdCapability([]string{"import-surfacing-misses", filepath.Join("..", "..", "fixtures", "capability-surfacing", "skill-surfacing-proposals.json")})
	})
	for _, want := range []string{
		"# capability import-surfacing-misses",
		"- status: review",
		"- misses_read: 2",
		"- false_triggers_read: 0",
		"- applied_misses: 1",
		"- applied_false_triggers: 0",
		"- edge: missed_when:context:app->skill:web-design-guidelines",
		"- skipped_unknown_count: 1",
		"- skipped_unknown: production-audit",
		"- source_observation:",
		"- source_candidate:",
		"- source_no_promote:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-surfacing-misses output missing %q:\n%s", want, out)
		}
	}
	afterOut := captureStdout(t, func() {
		cmdCapability([]string{"surface", "app store design review"})
	})
	if !strings.Contains(afterOut, "skill:web-design-guidelines") {
		t.Fatalf("surfacing miss feedback should surface skill:\n%s", afterOut)
	}
	authorityJSON := captureStdout(t, func() {
		cmdCapability([]string{"authority", "--json"})
	})
	var authority struct {
		NodeCount         int            `json:"node_count"`
		EdgeCount         int            `json:"edge_count"`
		EdgeKindCounts    map[string]int `json:"edge_kind_counts"`
		SourceRecordCount int            `json:"source_record_count"`
		SourceEdgeCount   int            `json:"source_edge_count"`
	}
	if err := json.Unmarshal([]byte(authorityJSON), &authority); err != nil {
		t.Fatalf("capability authority --json should parse: %v\n%s", err, authorityJSON)
	}
	if authority.EdgeKindCounts[capgraph.EdMissedWhen] == 0 {
		t.Fatalf("authority should expose feedback edge kind counts: %+v", authority.EdgeKindCounts)
	}
	if authority.NodeCount != 1 || authority.EdgeCount == 0 || authority.SourceRecordCount != 0 || authority.SourceEdgeCount != 0 {
		t.Fatalf("authority should expose current graph feedback separately from source handoff totals: %+v", authority)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("surfacing miss import must not promote claims, stat err=%v", err)
	}
}

func TestCapabilitySkillSourceDoctorReportsRawSkillSource(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "pricing")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: pricing
description: "Pricing workflow"
---
# pricing
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{"skill-source-doctor", root, "--json"})
	})
	for _, want := range []string{`"schema_version": "compound.skill_source_doctor.v0.1"`, `"status": "ok"`, `"skill_file_count": 1`, `"path": "skills/pricing/SKILL.md"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("skill-source-doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestCapabilityEvaluationPlanUsesUnknownEvaluationWithoutMutatingGraph(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	targetPath := filepath.Join(t.TempDir(), "skill-manifest.yaml")
	if err := os.WriteFile(targetPath, []byte("existing: skill manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdCapability([]string{
			"evaluation-plan",
			filepath.Join("..", "..", "fixtures", "capability-evaluations", "web-design-guidelines.json"),
			"--capability", "web-design-guidelines",
			"--target", targetPath,
		})
	})
	for _, want := range []string{
		"# capability evaluation-plan",
		"- status: review_required",
		"- evaluation_set:",
		"- evaluation: capability-evaluation.external.vercel.web-design-guidelines",
		"- capability: web-design-guidelines",
		"- verdict: adapt",
		"- node_draft: skill:web-design-guidelines",
		"- node_kind: skill",
		"- required_evidence: 5",
		"- evidence_refs:",
		"- plan:",
		"- review:",
		"- review_yaml:",
		"- manifest_draft:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("evaluation-plan output missing %q:\n%s", want, out)
		}
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	reviewYAMLPath := outputValue(out, "- review_yaml:")
	manifestDraftPath := outputValue(out, "- manifest_draft:")
	var plan capgraph.CapabilityEvaluationPlan
	if err := json.Unmarshal([]byte(readFile(t, planPath)), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.NodeDraft.NodeID != "skill:web-design-guidelines" || plan.TargetHash == "" {
		t.Fatalf("plan = %+v", plan)
	}
	manifest, err := capgraph.LoadSkillManifest(manifestDraftPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0].Status != "needs_review" || manifest.Skills[0].ID != "skill:web-design-guidelines" {
		t.Fatalf("manifest draft = %+v", manifest)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate CapabilityGraph") || !strings.Contains(review, "Required Review") {
		t.Fatalf("review missing guardrails:\n%s", review)
	}
	if got := readFile(t, targetPath); got != "existing: skill manifest\n" {
		t.Fatalf("target mutated: %q", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "registries", "capability-nodes.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("evaluation-plan must not mutate capability graph, stat err=%v", err)
	}
	if _, err := capgraph.LoadCapabilityEvaluationReview(reviewYAMLPath); err == nil {
		t.Fatal("unedited evaluation review YAML should be rejected")
	}
	reviewYAML := readFile(t, reviewYAMLPath)
	reviewYAML = strings.ReplaceAll(reviewYAML, "TODO_activate_reject_or_keep_needs_review", "activate")
	reviewYAML = strings.ReplaceAll(reviewYAML, "rationale: \"\"", "rationale: \"reviewed trigger scope and validation evidence\"")
	reviewYAML = strings.ReplaceAll(reviewYAML, "confidence: -1", "confidence: 0.84")
	editedReviewYAMLPath := filepath.Join(t.TempDir(), "capability-evaluation-review.yaml")
	if err := os.WriteFile(editedReviewYAMLPath, []byte(reviewYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewOut := captureStdout(t, func() {
		cmdCapability([]string{"evaluation-review", editedReviewYAMLPath})
	})
	for _, want := range []string{
		"# capability evaluation-review",
		"- status: manifest_ready",
		"- decision: activate",
		"- node: skill:web-design-guidelines",
		"- node_status: active",
		"- manifest:",
	} {
		if !strings.Contains(reviewOut, want) {
			t.Fatalf("evaluation-review output missing %q:\n%s", want, reviewOut)
		}
	}
	activeManifestPath := outputValue(reviewOut, "- manifest:")
	if _, err := os.Stat(filepath.Join(homePath, "registries", "capability-nodes.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("evaluation-review must not mutate capability graph, stat err=%v", err)
	}
	importOut := captureStdout(t, func() {
		cmdCapability([]string{"import-skills", manifestDraftPath})
	})
	if !strings.Contains(importOut, "node: skill:web-design-guidelines") {
		t.Fatalf("manifest draft import output =\n%s", importOut)
	}
	surfaceOut := captureStdout(t, func() {
		cmdCapability([]string{"surface", "web design guidelines"})
	})
	if strings.Contains(surfaceOut, "skill:web-design-guidelines") {
		t.Fatalf("needs_review manifest draft must not surface:\n%s", surfaceOut)
	}
	activeImportOut := captureStdout(t, func() {
		cmdCapability([]string{"import-skills", activeManifestPath})
	})
	if !strings.Contains(activeImportOut, "node: skill:web-design-guidelines") {
		t.Fatalf("active manifest import output =\n%s", activeImportOut)
	}
	activeSurfaceOut := captureStdout(t, func() {
		cmdCapability([]string{"surface", "web design guidelines"})
	})
	if !strings.Contains(activeSurfaceOut, "skill:web-design-guidelines") {
		t.Fatalf("reviewed active manifest should surface:\n%s", activeSurfaceOut)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("evaluation-plan must not promote claims, stat err=%v", err)
	}
}

func TestEventImportOSWritesTraceEnvelopeWithoutPromotion(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := filepath.Join("..", "..", "fixtures", "events", "os-events.ndjson")

	out := captureStdout(t, func() {
		cmdEvent([]string{"import-os", fixture, "--stream", "control-events"})
	})
	if !strings.Contains(out, "- authority: compound://events/trace-envelopes") ||
		!strings.Contains(out, "- source_authority: evsrc_") ||
		!strings.Contains(out, "imported: 2") ||
		!strings.Contains(out, "skipped: 0") {
		t.Fatalf("event import output =\n%s", out)
	}
	authority, err := eventsource.LoadSourceAuthority(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(authority) != 1 ||
		authority[0].AuthorityRef != eventsource.TraceEventsAuthorityRef ||
		authority[0].AuthorityStatus != "compound_primary" {
		t.Fatalf("event source authority = %+v", authority)
	}
	authorityOut := captureStdout(t, func() {
		cmdEvent([]string{"authority", "--json"})
	})
	var authorityReport eventsource.AuthorityReport
	if err := json.Unmarshal([]byte(authorityOut), &authorityReport); err != nil {
		t.Fatalf("parse event authority: %v\n%s", err, authorityOut)
	}
	if authorityReport.AuthorityRef != eventsource.TraceEventsAuthorityRef || !authorityReport.Installed || authorityReport.TraceCount != 2 {
		t.Fatalf("event authority report = %+v", authorityReport)
	}
	if len(authorityReport.SourceAuthority) != 1 || authorityReport.SourceAuthority[0].ID == "" {
		t.Fatalf("event authority source rows = %+v", authorityReport.SourceAuthority)
	}
	if len(authorityReport.RecentTraces) != 2 || authorityReport.RecentTraces[0].ID == "" {
		t.Fatalf("event authority recent traces = %+v", authorityReport.RecentTraces)
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
	if !strings.Contains(replay, "- source_authority: evsrc_") ||
		!strings.Contains(replay, "imported: 0") ||
		!strings.Contains(replay, "skipped: 2") {
		t.Fatalf("event replay output =\n%s", replay)
	}
	if lines := splitJSONLines(readFile(t, tracePath)); len(lines) != 2 {
		t.Fatalf("replay should not append duplicate traces, lines=%d", len(lines))
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("event import must not write claims ledger, stat err=%v", err)
	}
}

func TestEventImportOSCanProduceTraceBackedReviewFinding(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	grantSource := filepath.Join(t.TempDir(), "standing-grants.yaml")
	if err := os.WriteFile(grantSource, []byte(`generated_at: "2026-07-01T12:00:00Z"
grants:
  - grant_id: grant_review_finding
    capabilities:
      - review.finding
      - review.summary
    target_scope: state/runs/*
    reversibility_ceiling: reversible
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.ImportStandingGrants(homePath, grantSource, cliTestNow); err != nil {
		t.Fatalf("import standing grants: %v", err)
	}
	runRoot := t.TempDir()
	fixture := filepath.Join("..", "..", "fixtures", "events", "os-events.ndjson")
	out := captureStdout(t, func() {
		cmdEvent([]string{
			"import-os", fixture,
			"--stream", "control-events",
			"--limit", "1",
			"--review-root", runRoot,
			"--review-summary", "event import producer review finding",
		})
	})
	if !strings.Contains(out, "imported: 1") || !strings.Contains(out, "- review_findings: 1") {
		t.Fatalf("event review import output =\n%s", out)
	}
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("missing review finding JSON:\n%s", out)
	}
	var report reviewFindingRecord
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("parse review finding report: %v\n%s", err, out)
	}
	if report.PolicyOutcome != "allow" || report.StandingGrantID == "" || len(report.SourceTraces) != 1 {
		t.Fatalf("review finding report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(runRoot, filepath.FromSlash(report.TargetPath))); err != nil {
		t.Fatalf("review finding artifact missing: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	got, events, err := store.Show(report.WorkID)
	if err != nil {
		t.Fatalf("show work: %v", err)
	}
	receiptSeen := false
	for _, ev := range events {
		for _, ref := range ev.Attached {
			if strings.HasPrefix(ref, "rec_") {
				receiptSeen = true
			}
		}
	}
	if got.State != "completed" || got.IDs.TransitionID == "" || !receiptSeen || !stringSliceContains(got.RunIDs, report.ActionIntentID) {
		t.Fatalf("auto review work not closed: work=%+v report=%+v", got, report)
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

func TestDistillerImportConversationMiningRecordsSourceNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "agent-conversation-mining-report-latest.json")
	tagsPath := filepath.Join(dir, "agent-conversation-mined-tags-latest.jsonl")
	lossinessPath := filepath.Join(dir, "agent-conversation-lossiness-latest.json")
	if err := os.WriteFile(tagsPath, []byte(`{"accept_for_dream":true,"importance":10,"knowledge_type":"operational","quality_score":10,"tags":["architecture","distillation"],"decision_reasons":["accepted"],"summary":"high-signal system discussion","model_provider":"deterministic","model_name":"rules-v1","candidate":{"source":"codex","raw_path":"raw/session.jsonl","clean_path":"clean/session.md","clean_status":"mapped","stable":true,"importance":19,"quality_score":10,"knowledge_type":"operational","dream_candidate":true,"reasons":["architecture"],"retained_ratio":0.02,"excerpt":"<<ref>>"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lossinessPath, []byte(`{"known_lossy_rules":{"drops":"tool results"},"claude_clean_vs_raw_size_ratio":0.044,"codex_clean_vs_raw_size_ratio":0.012}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte(`{"ok":true,"updated_at":"2026-06-20T17:19:19Z","queue":"queue.jsonl","processed":1,"accepted":1,"rejected":0,"providers":{"deterministic":1},"out":"`+tagsPath+`","report":"`+reportPath+`","dream_pack":"dream-pack","dream_pack_files":1,"note":"Prepared high-fidelity Dream pack only. Dream execution remains separate gated."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdDistiller([]string{"import-conversation-mining", reportPath, "--lossiness", lossinessPath})
	})
	for _, want := range []string{
		"# distiller import-conversation-mining",
		"- status: ok",
		"- mined_rows: 1",
		"- accepted_for_review: 1",
		"- dream_execution: gated",
		"- lossiness_recorded: true",
		"- top_tag: architecture",
		"- source_observation:",
		"- source_candidate:",
		"- review_command: compound distiller improvement-plan --candidate",
		"- source_no_promote:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-conversation-mining output missing %q:\n%s", want, out)
		}
	}
	sourceCandidateID := outputValue(out, "- source_candidate:")
	if sourceCandidateID == "" {
		t.Fatalf("source candidate missing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("conversation mining import must not write claims ledger, stat err=%v", err)
	}
	methodDir := filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade))
	files, err := os.ReadDir(methodDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("method upgrade candidates = %d, want 1", len(files))
	}
	planOut := captureStdout(t, func() {
		cmdDistiller([]string{"improvement-plan", "--candidate", sourceCandidateID})
	})
	if !strings.Contains(planOut, "# distiller improvement-plan") || !strings.Contains(planOut, "- candidate_kind: method_upgrade") {
		t.Fatalf("conversation mining source candidate did not enter improvement-plan path:\n%s", planOut)
	}
	planPath := outputValue(planOut, "- plan:")
	var plan distiller.ImprovementPlan
	if err := json.Unmarshal([]byte(readFile(t, planPath)), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.CandidateID != sourceCandidateID || plan.CandidateVerdict != candidates.NoPromote || plan.Draft.Area != "method" {
		t.Fatalf("improvement plan = %+v", plan)
	}
	reviewYAMLPath := outputValue(planOut, "- review_yaml:")
	if reviewYAMLPath == "" {
		t.Fatalf("improvement plan did not print review_yaml:\n%s", planOut)
	}
	var review distiller.ImprovementReview
	if err := yaml.Unmarshal([]byte(readFile(t, reviewYAMLPath)), &review); err != nil {
		t.Fatal(err)
	}
	review.Decision = distiller.ImprovementReviewDecisionAccept
	review.Rationale = "reviewed conversation-mining import replay; keep source evidence candidate-only while improving future extraction review."
	review.Confidence = 0.77
	evalOut := captureStdout(t, func() {
		cmdDistiller([]string{"eval", filepath.Join("..", "..", "fixtures", "distiller", "llm-offline-eval.json"), "--record"})
	})
	proofRef := outputValue(evalOut, "- proof_ref:")
	if proofRef == "" {
		t.Fatalf("distiller eval --record did not print proof_ref:\n%s", evalOut)
	}
	review.EvalProofRefs = []string{proofRef}
	reviewData, err := yaml.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	editedReviewPath := filepath.Join(homePath, "reviews", "distiller-improvements", "edited-conversation-mining.review.yaml")
	if err := os.WriteFile(editedReviewPath, reviewData, 0o644); err != nil {
		t.Fatal(err)
	}
	reviewOut := captureStdout(t, func() {
		cmdDistiller([]string{"improvement-review", editedReviewPath})
	})
	if !strings.Contains(reviewOut, "# distiller improvement-review") || !strings.Contains(reviewOut, "- decision: accept") {
		t.Fatalf("conversation mining source candidate did not enter improvement-review path:\n%s", reviewOut)
	}
	reviewedPath := outputValue(reviewOut, "- reviewed:")
	if reviewedPath == "" {
		t.Fatalf("improvement-review did not print reviewed artifact:\n%s", reviewOut)
	}
	var reviewed distiller.ReviewedImprovement
	if err := json.Unmarshal([]byte(readFile(t, reviewedPath)), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Status != "accepted" || reviewed.PlanID != plan.ID || reviewed.CandidateID != sourceCandidateID || len(reviewed.EvalProofRefs) != 1 {
		t.Fatalf("reviewed improvement = %+v", reviewed)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("conversation mining improvement-plan/review must not write claims ledger, stat err=%v", err)
	}
}

func TestDistillerImportConversationCoverageRecordsSourceNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "agent-conversation-coverage-latest.json")
	if err := os.WriteFile(reportPath, []byte(`{
		"ok": true,
		"updated_at": "2026-06-20T15:13:24Z",
		"sources": {
			"claude": {
				"raw_files": 23665,
				"mapped_clean": 21783,
				"unmapped_raw": 1882,
				"stable": 23659,
				"raw_mb": 2514.53,
				"clean_mb": 107.18,
				"avg_retained_ratio": 0.08021,
				"dream_candidates": 69,
				"missing_embeddings": 0
			},
			"codex": {
				"raw_files": 1829,
				"mapped_clean": 1357,
				"unmapped_raw": 472,
				"stable": 1822,
				"raw_mb": 8845.39,
				"clean_mb": 106.17,
				"avg_retained_ratio": 0.09318,
				"dream_candidates": 4,
				"missing_embeddings": 0
			}
		},
		"total_rows": 25494,
		"top_tags": [
			{"source":"claude","raw_basename":"session.jsonl","clean_status":"mapped","importance":19,"quality_score":10,"knowledge_type":"operational","dream_candidate":true,"reasons":["architecture"]}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdDistiller([]string{"import-conversation-coverage", reportPath})
	})
	for _, want := range []string{
		"# distiller import-conversation-coverage conversation_coverage:",
		"- unmapped_raw: 2354",
		"- dream_candidates: 73",
		"- review_debt: 6",
		"- source_observation:",
		"- source_candidate:",
		"- review_command: compound distiller improvement-plan --candidate",
		"- source_no_promote:",
		"- row_import: no",
		"- dream_execution: no",
		"- memory_promotion: no",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("import-conversation-coverage output missing %q:\n%s", want, out)
		}
	}
	sourceCandidateID := outputValue(out, "- source_candidate:")
	if sourceCandidateID == "" {
		t.Fatalf("source candidate missing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("conversation coverage import should not write claims ledger, stat err=%v", err)
	}
	methodDir := filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade))
	files, err := os.ReadDir(methodDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("method upgrade candidates = %d, want 1", len(files))
	}
	planOut := captureStdout(t, func() {
		cmdDistiller([]string{"improvement-plan", "--candidate", sourceCandidateID})
	})
	if !strings.Contains(planOut, "# distiller improvement-plan") || !strings.Contains(planOut, "- candidate_kind: method_upgrade") {
		t.Fatalf("conversation coverage source candidate did not enter improvement-plan path:\n%s", planOut)
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

func TestDistillerEventRequestSummaryCanDriveParkedMemoryCandidate(t *testing.T) {
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
	var imported envelope.TraceEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &imported); err != nil {
		t.Fatalf("parse imported trace: %v", err)
	}
	requestOut := captureStdout(t, func() {
		cmdDistiller([]string{
			"request",
			"--trace-ref", "event-batch:control-events",
			"--event-trace", imported.ID,
		})
	})
	var req distiller.LLMExtractionRequest
	if err := json.Unmarshal([]byte(requestOut), &req); err != nil {
		t.Fatalf("parse request: %v\n%s", err, requestOut)
	}
	if len(req.Envelopes) != 1 || req.Envelopes[0].Trace == nil {
		t.Fatalf("request missing structured trace envelope: %+v", req.Envelopes)
	}
	eventEnv := req.Envelopes[0]
	if eventEnv.Trace.Source.SourceID == "" ||
		eventEnv.Trace.Scope.ProjectID == "" ||
		!stringSliceContains(eventEnv.Trace.FilesTouched, "/workspace/snapshots/daily.json") {
		t.Fatalf("request trace summary is not useful source material: %+v", eventEnv.Trace)
	}
	traceRef := "trace_envelope:" + eventEnv.ID
	confidence := 0.91
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "trace_event_request_summary_candidate",
		SourceRefs:    []string{traceRef},
		Candidates: []distiller.LLMCandidate{{
			Kind:         distiller.KindMemory,
			Entity:       "project:" + eventEnv.Trace.Scope.ProjectID,
			Predicate:    "event_observed",
			Object:       eventEnv.Trace.Source.SourceID,
			Confidence:   &confidence,
			Derivation:   "llm_extracted_from_trace_envelope_summary",
			EvidenceRefs: []string{traceRef},
		}},
	}
	responseData, err := json.MarshalIndent(response, "", " ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	responsePath := filepath.Join(t.TempDir(), "event-request-summary-response.json")
	if err := os.WriteFile(responsePath, append(responseData, '\n'), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
	out := captureStdout(t, func() {
		cmdDistiller([]string{"run", "--llm-response", responsePath})
	})
	if !strings.Contains(out, "# distilled trace_event_request_summary_candidate") ||
		!strings.Contains(out, "park") ||
		!strings.Contains(out, "no promotion basis") {
		t.Fatalf("distiller run output missing parked proof:\n%s", out)
	}
	candidateRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory)))
	if len(candidateRows) != 1 ||
		candidateRows[0].Entity != "project:control" ||
		candidateRows[0].Object != eventEnv.Trace.Source.SourceID ||
		candidateRows[0].Verdict != distiller.Park ||
		!stringSliceContains(candidateRows[0].EvidenceRefs, traceRef) {
		t.Fatalf("event request summary candidate not parked with trace evidence: %+v", candidateRows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("event request summary candidate must not promote claims, stat err=%v", err)
	}
}

func TestDecisionCandidatePlanUsesDistillerCandidateWithoutMutatingTarget(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	confidence := 0.88
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "trace_decision_candidate",
		SourceRefs:    []string{"trace_envelope:trace_decision_candidate"},
		Candidates: []distiller.LLMCandidate{{
			Kind:       distiller.KindDecision,
			Subject:    "decision:route-free-model-judging",
			Statement:  "Use paid frontier lanes for final judgment only after cheap lanes produce evidence.",
			Detail:     "Keeps routing cost controlled while preserving quality gates.",
			Confidence: &confidence,
			Derivation: "llm_extracted_from_session",
			Qualifiers: map[string]string{
				"kind":           decisions.KindOperational,
				"owner":          "compound",
				"scope":          "routing",
				"falsifier":      "cheap lanes repeatedly miss material defects",
				"review_trigger": "after three route-evidence regressions",
			},
			EvidenceRefs: []string{"trace_envelope:trace_decision_candidate", "receipt:routing"},
		}},
	}
	responseData, err := json.MarshalIndent(response, "", " ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	responsePath := filepath.Join(t.TempDir(), "decision-response.json")
	if err := os.WriteFile(responsePath, append(responseData, '\n'), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
	captureStdout(t, func() {
		cmdDistiller([]string{"run", "--llm-response", responsePath})
	})
	rows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.Decision)))
	if len(rows) != 1 || rows[0].Verdict != distiller.Park || rows[0].Kind != distiller.KindDecision {
		t.Fatalf("decision candidates = %+v", rows)
	}
	targetPath := filepath.Join(t.TempDir(), "DEC-routing.md")
	original := "# Existing Decision\n\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdDecisions([]string{"candidate-plan", "--candidate", rows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# decisions candidate-plan dcp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- decision_key: route-free-model-judging") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("candidate-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	if planPath == "" || reviewPath == "" {
		t.Fatalf("missing artifact paths:\n%s", out)
	}
	var plan decisions.DecisionCandidatePlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != decisions.DecisionCandidatePlanSchemaVersion ||
		plan.CandidateID != rows[0].ID ||
		plan.LifecycleDraft.EntityID != "decision:route-free-model-judging" ||
		plan.TargetHash == "" {
		t.Fatalf("bad decision candidate plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate decision source files") ||
		!strings.Contains(review, "## Draft DecisionLifecycle") {
		t.Fatalf("review missing no-mutation warning:\n%s", review)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("decision target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("candidate-plan must not promote claims, stat err=%v", err)
	}
}

func TestMemoryWritePlanUsesParkedDistillerCandidateWithoutMutatingBrain(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	confidence := 0.86
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "trace_brain_write_candidate",
		SourceRefs:    []string{"trace_envelope:trace_brain_write_candidate"},
		Candidates: []distiller.LLMCandidate{{
			Kind:         distiller.KindMemory,
			Entity:       "project:sample-mobile-app",
			Predicate:    "release_gate",
			Object:       "review App Store screenshots before release",
			Confidence:   &confidence,
			Derivation:   "llm_extracted_from_session",
			ClaimType:    "constraint",
			EvidenceRefs: []string{"trace_envelope:trace_brain_write_candidate"},
		}},
	}
	responseData, err := json.MarshalIndent(response, "", " ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	responsePath := filepath.Join(t.TempDir(), "brain-write-response.json")
	if err := os.WriteFile(responsePath, append(responseData, '\n'), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
	captureStdout(t, func() {
		cmdDistiller([]string{"run", "--llm-response", responsePath})
	})
	candidateRows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory)))
	if len(candidateRows) != 1 || candidateRows[0].Verdict != distiller.Park {
		t.Fatalf("parked candidates = %+v", candidateRows)
	}
	targetPath := filepath.Join(t.TempDir(), "brain", "products", "sample-mobile-app.md")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# SampleMobileApp\n\nExisting curated product memory.\n"
	if err := os.WriteFile(targetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdMemory([]string{"write-plan", "--candidate", candidateRows[0].ID, "--target", targetPath})
	})
	if !strings.Contains(out, "# memory write-plan mwp_") ||
		!strings.Contains(out, "- status: review_required") ||
		!strings.Contains(out, "- target_exists: true") ||
		!strings.Contains(out, "- evidence_refs: 1") ||
		!strings.Contains(out, "- plan:") ||
		!strings.Contains(out, "- review:") {
		t.Fatalf("write-plan output missing proof lines:\n%s", out)
	}
	planPath := outputValue(out, "- plan:")
	reviewPath := outputValue(out, "- review:")
	if planPath == "" || reviewPath == "" {
		t.Fatalf("missing artifact paths:\n%s", out)
	}
	var plan mem.MeaningWritePlan
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != mem.MeaningWritePlanSchemaVersion ||
		plan.CandidateID != candidateRows[0].ID ||
		plan.Entity != "project:sample-mobile-app" ||
		plan.TargetHash == "" ||
		!strings.Contains(plan.ProposedMarkdown, "review App Store screenshots before release") {
		t.Fatalf("bad meaning write plan: %+v", plan)
	}
	review := readFile(t, reviewPath)
	if !strings.Contains(review, "does not mutate curated memory source files") {
		t.Fatalf("review missing no-mutation warning:\n%s", review)
	}
	if got := readFile(t, targetPath); got != original {
		t.Fatalf("brain target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("write-plan must not promote claims, stat err=%v", err)
	}
}

func TestDistillerRunGroundsLLMResponseWithRequest(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	request := distiller.LLMExtractionRequest{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "runtime:work_receipt_proof",
		SourceRefs: []string{
			"receipt:rec_receipt_proof",
			"context_injection:hook_receipt_proof.md",
			"proof:hook-readiness",
		},
		Policy: distiller.DefaultPolicy(),
	}
	confidence := 0.91
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "runtime:work_receipt_proof",
		Extractor:     "fixture-offline",
		Model:         "offline-fixture",
		Candidates: []distiller.LLMCandidate{{
			Kind:         distiller.KindMethodUpgrade,
			Subject:      "distiller.receipt-proof-learning",
			Statement:    "Offline distiller runs should bind response evidence to saved request source refs.",
			Confidence:   &confidence,
			EvidenceRefs: []string{"proof:hook-readiness", "context_injection:hook_receipt_proof.md"},
		}},
	}

	requestPath := filepath.Join(t.TempDir(), "request.json")
	requestData, err := json.MarshalIndent(request, "", " ")
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err := os.WriteFile(requestPath, append(requestData, '\n'), 0o644); err != nil {
		t.Fatalf("write request: %v", err)
	}
	responsePath := filepath.Join(t.TempDir(), "response.json")
	responseData, err := json.MarshalIndent(response, "", " ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := os.WriteFile(responsePath, append(responseData, '\n'), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}

	out := captureStdout(t, func() {
		cmdDistiller([]string{"run", "--llm-response", responsePath, "--request", requestPath})
	})
	if !strings.Contains(out, "method_upgrade") || !strings.Contains(out, "park") {
		t.Fatalf("distiller output missing parked method upgrade:\n%s", out)
	}

	rows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade)))
	if len(rows) != 1 {
		t.Fatalf("method upgrade candidates = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.Verdict != distiller.Park {
		t.Fatalf("candidate verdict = %q, want park", got.Verdict)
	}
	for _, want := range []string{"proof:hook-readiness", "context_injection:hook_receipt_proof.md"} {
		found := false
		for _, ref := range got.EvidenceRefs {
			if ref == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("candidate evidence refs missing %q: %+v", want, got.EvidenceRefs)
		}
	}
}

func TestDistillerRequestRecordPersistsReplayablePacket(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	rawPath := filepath.Join(t.TempDir(), "trace.txt")
	if err := os.WriteFile(rawPath, []byte("hook output refs must remain replayable evidence\n"), 0o644); err != nil {
		t.Fatalf("write raw trace: %v", err)
	}

	out := captureStdout(t, func() {
		cmdDistiller([]string{"request", "--trace-ref", "trace_recorded_request", "--raw", rawPath, "--record"})
	})
	var stdoutReq distiller.LLMExtractionRequest
	if err := json.Unmarshal([]byte(out), &stdoutReq); err != nil {
		t.Fatalf("parse stdout request: %v\n%s", err, out)
	}
	if stdoutReq.TraceRef != "trace_recorded_request" || !stringSliceContains(stdoutReq.SourceRefs, "raw_trace:trace_recorded_request") {
		t.Fatalf("stdout request not grounded in raw trace: %+v", stdoutReq)
	}

	requestDir := filepath.Join(homePath, "reviews", "distiller-requests")
	entries, err := os.ReadDir(requestDir)
	if err != nil {
		t.Fatalf("read request records: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("request record count = %d, want 1", len(entries))
	}
	recordedPath := filepath.Join(requestDir, entries[0].Name())
	recordedRef := distiller.LLMRequestRecordRefPrefix + strings.TrimSuffix(entries[0].Name(), ".json")
	var recordedReq distiller.LLMExtractionRequest
	if err := json.Unmarshal([]byte(readFile(t, recordedPath)), &recordedReq); err != nil {
		t.Fatalf("parse recorded request: %v", err)
	}
	if recordedReq.TraceRef != stdoutReq.TraceRef || !stringSliceContains(recordedReq.SourceRefs, "raw_trace:trace_recorded_request") {
		t.Fatalf("recorded request changed replay material: %+v", recordedReq)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("distiller request --record must not promote claims, stat err=%v", err)
	}

	confidence := 0.9
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      "trace_recorded_request",
		Extractor:     "fixture-offline",
		Model:         "offline-fixture",
		Candidates: []distiller.LLMCandidate{{
			Kind:         distiller.KindMethodUpgrade,
			Subject:      "distiller.recorded-request-replay",
			Statement:    "Recorded distiller request packets should remain reusable for grounded offline replay.",
			Confidence:   &confidence,
			EvidenceRefs: []string{"raw_trace:trace_recorded_request"},
		}},
	}
	responsePath := filepath.Join(t.TempDir(), "response.json")
	responseData, err := json.MarshalIndent(response, "", " ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := os.WriteFile(responsePath, append(responseData, '\n'), 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}

	runOut := captureStdout(t, func() {
		cmdDistiller([]string{"run", "--llm-response", responsePath, "--request", recordedRef})
	})
	if !strings.Contains(runOut, "method_upgrade") || !strings.Contains(runOut, "park") {
		t.Fatalf("recorded request did not replay through grounded run:\n%s", runOut)
	}
	rows := readCandidateRows(t, filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade)))
	if len(rows) != 1 || !stringSliceContains(rows[0].EvidenceRefs, "raw_trace:trace_recorded_request") {
		t.Fatalf("replayed candidate not grounded in recorded request: %+v", rows)
	}
}

func TestMemoryImportPageCreatesEvidenceAndCandidatesWithoutClaims(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	pagePath := filepath.Join("..", "..", "fixtures", "brain", "knowledge-page.md")

	out := captureStdout(t, func() {
		cmdMemory([]string{"import-page", pagePath})
	})
	if !strings.Contains(out, "# memory import-page kg-synthetic-visual-standard") ||
		!strings.Contains(out, "- candidates: 2") ||
		!strings.Contains(out, "- created: 2") {
		t.Fatalf("import-page output =\n%s", out)
	}
	cands, err := os.ReadDir(filepath.Join(homePath, candidates.InboxDir(candidates.Memory)))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidate files = %d, want 2", len(cands))
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("import-page must not promote claims, stat err=%v", err)
	}

	out = captureStdout(t, func() {
		cmdMemory([]string{"import-page", pagePath})
	})
	if !strings.Contains(out, "- created: 0") || !strings.Contains(out, "- reused: 2") {
		t.Fatalf("second import should be idempotent:\n%s", out)
	}
}

func TestMemoryTimelinePrintsEvidenceAndSupersessionRefs(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Capture([]mem.Candidate{{
		Entity:          "MemoryEngine",
		Predicate:       "authority_status",
		Object:          "rented index",
		ObservationDate: cliTestNow.Format(time.RFC3339),
		Confidence:      0.9,
		Derivation:      "deterministic",
		ClaimType:       "architecture",
		PromotionBasis:  candidates.BasisRuntimeProof,
		SourceTraceRef:  "compound://runtime-proof/first",
		SnippetHash:     "sha256:first",
		Visibility:      "operational",
	}}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	firstResult, err := store.Adjudicate(first[0].ID, "promote", "work_timeline_proof", cliTestNow)
	if err != nil {
		t.Fatalf("promote first: %v", err)
	}
	second, err := store.Capture([]mem.Candidate{{
		Entity:          "MemoryEngine",
		Predicate:       "authority_status",
		Object:          "rented index with reference coverage",
		ObservationDate: cliTestNow.Format(time.RFC3339),
		Confidence:      0.96,
		Derivation:      "deterministic",
		ClaimType:       "architecture",
		PromotionBasis:  candidates.BasisRuntimeProof,
		SourceTraceRef:  "compound://runtime-proof/second",
		SnippetHash:     "sha256:second",
		Visibility:      "operational",
	}}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := store.Adjudicate(second[0].ID, "promote", "work_timeline_proof", cliTestNow)
	if err != nil {
		t.Fatalf("promote second: %v", err)
	}
	out := captureStdout(t, func() {
		cmdMemory([]string{"timeline", "MemoryEngine"})
	})
	for _, want := range []string{
		firstResult.Claim.ID,
		secondResult.Claim.ID,
		"evidence=" + first[0].ID + ",compound://runtime-proof/first",
		"evidence=" + second[0].ID + ",compound://runtime-proof/second",
		"supersedes=" + firstResult.Claim.ID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("timeline output missing %q:\n%s", want, out)
		}
	}
}

func TestMemoryCandidateRelationsRemainStructuralEdges(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdMemory([]string{"candidate", filepath.Join("..", "..", "fixtures", "traces", "microservice-graph.json")})
	})
	if !strings.Contains(out, "service:gateway -depends_on-> service:auth") ||
		!strings.Contains(out, "service:auth -depends_on-> lib:jwt") {
		t.Fatalf("memory candidate did not persist structural relations:\n%s", out)
	}
	edges := readFile(t, filepath.Join(homePath, "ledger", "edges.jsonl"))
	if !strings.Contains(edges, `"kind":"depends_on"`) ||
		!strings.Contains(edges, `"from":"service:gateway"`) ||
		!strings.Contains(edges, `"to":"lib:jwt"`) {
		t.Fatalf("relation edges not recorded structurally:\n%s", edges)
	}
	if _, err := os.Stat(filepath.Join(homePath, "inbox", "relation-candidates")); !os.IsNotExist(err) {
		t.Fatalf("relation must not create candidate inbox, stat err=%v", err)
	}
}

func TestMemoryDoctorUsesConfiguredSidecarEngine(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{{
		Entity:         "project:api",
		Predicate:      "language",
		Object:         "go",
		Confidence:     0.9,
		Derivation:     "extracted",
		PromotionBasis: candidates.BasisTrustedDeterministic,
		SourceTraceRef: "trace_cli_memory_doctor",
		SnippetHash:    "sha256:test",
	}}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Adjudicate(got[0].ID, "promote", "work_cli_memory_doctor", cliTestNow); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "compound-mock-engine")
	cmd := exec.Command("go", "build", "-o", bin, "../compound-mock-engine")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock engine: %v\n%s", err, out)
	}
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", bin)
	t.Setenv("COMPOUND_MEM_ENGINE_EMBEDDER", "reference:lexical:0:0")

	out := captureStdout(t, func() { cmdMemory([]string{"doctor"}) })
	if !strings.Contains(out, "sync/embedding") || !strings.Contains(out, "ok       reference") || !strings.Contains(out, "ledger_unchanged=true") {
		t.Fatalf("memory doctor should prove configured engine hop:\n%s", out)
	}
	jsonOut := captureStdout(t, func() { cmdMemory([]string{"doctor", "--json"}) })
	var report mem.Report
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("memory doctor --json should parse: %v\n%s", err, jsonOut)
	}
	if !report.OK {
		t.Fatalf("memory doctor --json should be ok: %+v", report)
	}
	var foundEngineHop bool
	for _, hop := range report.Hops {
		if hop.Name == "sync/embedding" && hop.Status == "ok" && strings.Contains(hop.Detail, "ledger_unchanged=true") {
			foundEngineHop = true
		}
	}
	if !foundEngineHop {
		t.Fatalf("memory doctor --json missing configured engine hop: %+v", report.Hops)
	}
}

func TestMemoryDoctorEngineProfileWiresReadOnlyCLISidecar(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{{
		Entity:         "project:api",
		Predicate:      "language",
		Object:         "go",
		Confidence:     0.9,
		Derivation:     "extracted",
		PromotionBasis: candidates.BasisTrustedDeterministic,
		SourceTraceRef: "trace_cli_memory_profile",
		SnippetHash:    "sha256:test",
	}}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Adjudicate(got[0].ID, "promote", "work_cli_memory_profile", cliTestNow); err != nil {
		t.Fatal(err)
	}
	fakeCLI := filepath.Join(t.TempDir(), "memory-cli")
	if err := os.WriteFile(fakeCLI, []byte("#!/bin/sh\necho \"project api language go\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEMORY_CLI_BIN", fakeCLI)
	t.Setenv("COMPOUND_MEMORY_CLI_LIMIT", "1")

	jsonOut := captureStdout(t, func() {
		cmdMemory([]string{"doctor", "--engine-profile", "memory-cli", "--json"})
	})
	var report mem.Report
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("memory doctor --engine-profile --json parse: %v\n%s", err, jsonOut)
	}
	var foundEngineHop bool
	for _, hop := range report.Hops {
		if hop.Name == "sync/embedding" && hop.Status == "ok" && strings.Contains(hop.Detail, "memory-cli") && strings.Contains(hop.Detail, "ledger_unchanged=true") {
			foundEngineHop = true
		}
	}
	if !foundEngineHop {
		t.Fatalf("memory doctor --engine-profile missing configured engine hop: %+v", report.Hops)
	}
}

func TestMemoryEngineConfigPersistsDefaultProfile(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	out := captureStdout(t, func() {
		cmdMemory([]string{"engine-config", "--profile", "gbrain"})
	})
	if !strings.Contains(out, "- configured: true") || !strings.Contains(out, "- profile: gbrain") {
		t.Fatalf("engine-config output =\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(homePath, "config", "memory-engine.yaml"))
	if err != nil {
		t.Fatalf("read memory engine config: %v", err)
	}
	if !strings.Contains(string(data), "schema_version: compound.memory_engine_config.v0.1") ||
		!strings.Contains(string(data), "profile: gbrain") {
		t.Fatalf("memory engine config not persisted as expected:\n%s", data)
	}

	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEMORY_CLI_PROFILE", "")
	restore, err := configureMemoryEngineFromHome(homePath, "")
	if err != nil {
		t.Fatalf("configure from persisted profile: %v", err)
	}
	defer restore()
	if got := os.Getenv("COMPOUND_MEMORY_CLI_PROFILE"); got != "gbrain" {
		t.Fatalf("configured profile = %q, want gbrain", got)
	}
	if got := os.Getenv("COMPOUND_MEM_ENGINE_BIN"); strings.TrimSpace(got) == "" {
		t.Fatal("persisted profile should configure a sidecar command")
	}
}

func TestMemoryEngineProfileFlagOverridesPersistedConfig(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := writeMemoryEngineConfig(homePath, "gbrain"); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEMORY_CLI_PROFILE", "")

	restore, err := configureMemoryEngineFromHome(homePath, "memory-cli")
	if err != nil {
		t.Fatalf("configure explicit profile: %v", err)
	}
	defer restore()
	if got := os.Getenv("COMPOUND_MEMORY_CLI_PROFILE"); got != "memory-cli" {
		t.Fatalf("explicit profile = %q, want memory-cli", got)
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

func TestProjectionRepairReviewCLIRecordsReviewedSidecar(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	planOut := captureStdout(t, func() {
		cmdProjection([]string{"repair-plan", filepath.Join("..", "..", "fixtures", "projections", "stale-freshness.yaml")})
	})
	if !strings.Contains(planOut, "# projection repair-plan") || !strings.Contains(planOut, "- review_yaml:") {
		t.Fatalf("projection repair-plan output missing review YAML:\n%s", planOut)
	}
	queue, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: projection.ProjectionRegenerationReviewArea}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	projection.AnnotateProjectionRegenerationQueue(&queue)
	reviewartifact.Recount(&queue)
	if queue.Counts.Total != 1 || queue.Counts.ReviewRequired != 1 || queue.Items[0].Readiness != "needs_review" {
		t.Fatalf("initial projection regeneration queue = %+v", queue)
	}
	reviewYAML := ""
	for _, path := range queue.Items[0].ExtraPaths {
		if strings.HasSuffix(path, ".review.yaml") {
			reviewYAML = path
			break
		}
	}
	if reviewYAML == "" {
		t.Fatalf("review YAML missing from queue item: %+v", queue.Items[0])
	}
	review, err := projection.LoadRegenerationReview(reviewYAML)
	if err != nil {
		t.Fatalf("load review YAML: %v", err)
	}
	review.Decision = "owner_action_required"
	review.Rationale = "owner reviewed stale projection; regenerate through owner generator before trusting dashboard"
	review.Confidence = 0.86
	review.NextAction = "run owner generator and rerun projection doctor"
	edited, err := yaml.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewYAML, edited, 0o644); err != nil {
		t.Fatalf("write edited review YAML: %v", err)
	}

	reviewOut := captureStdout(t, func() {
		cmdProjection([]string{"repair-review", reviewYAML})
	})
	if !strings.Contains(reviewOut, "# projection repair-review") ||
		!strings.Contains(reviewOut, "- status: recorded") ||
		!strings.Contains(reviewOut, "- decision: owner_action_required") ||
		!strings.Contains(reviewOut, "- reviewed:") {
		t.Fatalf("projection repair-review output missing proof lines:\n%s", reviewOut)
	}
	queue, err = reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: projection.ProjectionRegenerationReviewArea}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	projection.AnnotateProjectionRegenerationQueue(&queue)
	reviewartifact.Recount(&queue)
	if queue.Counts.OK != 1 || queue.Counts.ReviewRequired != 0 || queue.Items[0].Status != "recorded" || queue.Items[0].Readiness != "review_recorded" {
		t.Fatalf("recorded projection regeneration queue = %+v", queue)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("projection repair-review should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestHarnessObserveWritesCapabilityNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() { cmdHarness([]string{"observe"}) })
	for _, want := range []string{"# harness observe", "- source_observation: srcobs_", "- source_candidate:", "- source_no_promote:", "- verdict: capability_evidence_only_not_authority"} {
		if !strings.Contains(out, want) {
			t.Fatalf("harness observe output missing %q:\n%s", want, out)
		}
	}
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatalf("read capability observation dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("capability observation entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(candidateDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read capability observation: %v", err)
	}
	var candidate struct {
		ID                  string `json:"id"`
		Kind                string `json:"kind"`
		Subject             string `json:"subject"`
		Verdict             string `json:"verdict"`
		SourceObservationID string `json:"source_observation_id"`
		PromoteAllowed      bool   `json:"promote_allowed"`
	}
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatalf("parse capability observation: %v", err)
	}
	if candidate.Kind != string(candidates.CapabilityObs) || candidate.Subject != "harness_registry:default" || candidate.Verdict != candidates.NoPromote || candidate.PromoteAllowed || candidate.SourceObservationID == "" {
		t.Fatalf("candidate = %+v", candidate)
	}
	rows := readNoPromoteDecisionRows(t, homePath)
	if len(rows) != 1 || rows[0].CandidateRef != candidate.ID || rows[0].OwnerTarget.Kind != "source_observation" || rows[0].OwnerTarget.Ref != candidate.SourceObservationID {
		t.Fatalf("no-promote rows = %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("harness observation should not promote claims, stat err=%v", err)
	}
}

func TestHarnessObserveManifestWritesCapabilityNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := filepath.Join("..", "..", "fixtures", "harness", "harness-registry.yaml")
	out := captureStdout(t, func() {
		cmdHarness([]string{"observe", fixture})
	})
	for _, want := range []string{"# harness observe", "- source_observation: srcobs_", "- source_candidate:", "- source_no_promote:", "- verdict: capability_evidence_only_not_authority"} {
		if !strings.Contains(out, want) {
			t.Fatalf("harness manifest observe output missing %q:\n%s", want, out)
		}
	}
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("capability candidate entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(candidateDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var candidate sourceobs.CandidateRecord
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.Subject != "harness_registry:fixture-harness-registry" || candidate.Verdict != candidates.NoPromote || candidate.PromoteAllowed {
		t.Fatalf("candidate = %+v", candidate)
	}
	rows := readNoPromoteDecisionRows(t, homePath)
	if len(rows) != 1 || rows[0].CandidateRef != candidate.ID {
		t.Fatalf("no-promote rows = %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("harness manifest observation should not promote claims, stat err=%v", err)
	}
}

func TestHarnessObserveOSEntrypointsWritesCapabilityNoPromote(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	fixture := filepath.Join("..", "..", "fixtures", "harness", "os-harness-entrypoints.yaml")
	out := captureStdout(t, func() {
		cmdHarness([]string{"observe", "--os-entrypoints", fixture})
	})
	for _, want := range []string{"# harness observe", "- source_observation: srcobs_", "- source_candidate:", "- source_no_promote:", "- verdict: capability_evidence_only_not_authority"} {
		if !strings.Contains(out, want) {
			t.Fatalf("OS entrypoints observe output missing %q:\n%s", want, out)
		}
	}
	candidateDir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs))
	entries, err := os.ReadDir(candidateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("capability candidate entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(candidateDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var candidate sourceobs.CandidateRecord
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.Subject != "harness_registry:os-harness-entrypoints" || candidate.Verdict != candidates.NoPromote || candidate.PromoteAllowed {
		t.Fatalf("candidate = %+v", candidate)
	}
	rows := readNoPromoteDecisionRows(t, homePath)
	if len(rows) != 1 || rows[0].CandidateRef != candidate.ID {
		t.Fatalf("no-promote rows = %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("OS entrypoint observation should not promote claims, stat err=%v", err)
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

func TestRuntimeRepairPlanCreatesGateAndReview(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdRuntime([]string{"repair-plan", filepath.Join("..", "..", "fixtures", "runtime", "broken-service.yaml")})
	})
	for _, want := range []string{
		"# runtime repair-plan rrp_",
		"- failed_checks: 1",
		"- writeback_gate: owg_",
		"- work: work_",
		"- receipt: rec_",
		"- transition: tr_",
		"- plan:",
		"- review:",
		"- review_yaml:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runtime repair-plan output missing %q:\n%s", want, out)
		}
	}
	planDir := filepath.Join(homePath, "reviews", "runtime-repair")
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
		SchemaVersion   string `json:"schema_version"`
		Status          string `json:"status"`
		FailedCount     int    `json:"failed_count"`
		WritebackGateID string `json:"writeback_gate_id"`
		ReceiptID       string `json:"receipt_id"`
		TransitionID    string `json:"transition_id"`
	}
	data, err := os.ReadFile(planFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != runtimebinding.RepairPlanSchemaVersion ||
		plan.Status != "review_required" ||
		plan.FailedCount != 1 ||
		!strings.HasPrefix(plan.WritebackGateID, "owg_") ||
		!strings.HasPrefix(plan.ReceiptID, "rec_") ||
		!strings.HasPrefix(plan.TransitionID, "tr_") {
		t.Fatalf("bad runtime repair plan: %+v", plan)
	}
	review, err := os.ReadFile(reviewFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "does not mutate runtime owner files") {
		t.Fatalf("review did not preserve no-direct-write warning:\n%s", review)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("runtime repair-plan should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestRuntimeRepairReviewCLIRecordsReviewedSidecar(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	planOut := captureStdout(t, func() {
		cmdRuntime([]string{"repair-plan", filepath.Join("..", "..", "fixtures", "runtime", "broken-service.yaml")})
	})
	reviewYAML := outputValue(planOut, "- review_yaml:")
	if reviewYAML == "" {
		t.Fatalf("runtime repair-plan did not print review_yaml:\n%s", planOut)
	}
	body, err := os.ReadFile(reviewYAML)
	if err != nil {
		t.Fatal(err)
	}
	reviewedYAML := strings.ReplaceAll(string(body), "TODO_decision_owner_action_required_owner_action_done_defer_no_action", "owner_action_required")
	reviewedYAML = strings.ReplaceAll(reviewedYAML, "rationale: \"\"", "rationale: \"owner reviewed runtime repair path\"")
	reviewedYAML = strings.ReplaceAll(reviewedYAML, "confidence: -1", "confidence: 0.91")
	reviewedYAML = strings.ReplaceAll(reviewedYAML, "TODO: reviewed runtime repair next action", "rerun runtime doctor after owner contract edit")
	if err := os.WriteFile(reviewYAML, []byte(reviewedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewOut := captureStdout(t, func() {
		cmdRuntime([]string{"repair-review", reviewYAML})
	})
	if !strings.Contains(reviewOut, "# runtime repair-review") ||
		!strings.Contains(reviewOut, "- status: recorded") ||
		!strings.Contains(reviewOut, "- decision: owner_action_required") ||
		!strings.Contains(reviewOut, "- reviewed:") {
		t.Fatalf("runtime repair-review output missing proof:\n%s", reviewOut)
	}
	queueOut := captureStdout(t, func() {
		cmdReviews([]string{"--area", runtimebinding.RuntimeRepairReviewArea, "--json"})
	})
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(queueOut), &report); err != nil {
		t.Fatalf("parse review queue: %v\n%s", err, queueOut)
	}
	if report.Counts.Total != 1 || report.Counts.OK != 1 || report.Counts.ReviewRequired != 0 {
		t.Fatalf("runtime repair review should close queue debt: %+v", report.Counts)
	}
	if len(report.Items) != 1 || report.Items[0].Status != "recorded" || report.Items[0].Readiness != "review_recorded" {
		t.Fatalf("runtime repair item not recorded: %+v", report.Items)
	}
	if !strings.Contains(report.Items[0].NextAction, "rerun runtime doctor") {
		t.Fatalf("reviewed next action missing: %+v", report.Items[0])
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("runtime repair-review should not promote claims, claims ledger stat err=%v", err)
	}
}

func TestRuntimeDoctorLiveProbeIsOptIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-live.yaml")
	body := `runtime_binding:
  service_id: service:live-api
  owner_entity: product:live
  host_binding: host:local-test
  deployment_binding: process:live-api
  data_bindings: [sqlite:live.db]
  health_check_refs: [` + srv.URL + `/health]
  risk_tier: tier1
  evidence_refs: [fixture:runtime-binding]
  status: active
service_contract:
  service_id: service:live-api
  interface_refs: [http:get:/health]
  owner_entity: product:live
  risk_tier: tier1
  evidence_refs: [fixture:service-contract]
  status: active
runtime_probes:
  - id: health
    kind: http
    target: ` + srv.URL + `/health
    expected_status: 204
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdRuntime([]string{"doctor", path})
	})
	if strings.Contains(out, "live:health") {
		t.Fatalf("structural doctor should not run live probe without --live:\n%s", out)
	}

	out = captureStdout(t, func() {
		cmdRuntime([]string{"doctor", "--live", path})
	})
	if !strings.Contains(out, "- live:health: ok") || !strings.Contains(out, "- status: ok") {
		t.Fatalf("live doctor output:\n%s", out)
	}
}

func TestProjectionDashboardReadModelSourceObserve(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.json"), []byte(`{"ok":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "read-model.json")
	body := `{
  "version": 1,
  "generated_at": "2000-01-01T00:00:00Z",
  "source_paths": ["source.json"],
  "cards": [
    {
      "id": "runtime-health",
      "title": "Runtime health",
      "lane": "control",
      "status": "risk",
      "owner": "system",
      "source_paths": ["source.json"],
      "next_action": "review runtime evidence",
      "visibility": "operator"
    }
  ]
}
`
	if err := os.WriteFile(modelPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdProjection([]string{"dashboard-read-model", modelPath, "--max-age-hours", "1", "--source-observe"})
	})
	for _, want := range []string{"# projection doctor", "- projection: projection:dashboard-read-model", "- status: stale", "- source_observation: srcobs_", "- source_candidate:", "- source_no_promote:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard read-model output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dashboard read-model source observe must not promote claims, stat err=%v", err)
	}
}

func TestProjectionContextRecordFlowsIntoOrient(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	out := captureStdout(t, func() {
		cmdProjection([]string{"doctor", filepath.Join("..", "..", "fixtures", "projections", "stale-freshness.yaml"), "--record-context", "--repo-kind", "control-plane"})
	})
	if !strings.Contains(out, "- context_record: pctx_") || !strings.Contains(out, "- context_repo_kinds: control-plane") {
		t.Fatalf("projection doctor should record orientation context:\n%s", out)
	}
	out = captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--repo-kind", "control-plane", "--task", "runtime dashboard"})
	})
	var control envelope.ContextPack
	if err := json.Unmarshal([]byte(out), &control); err != nil {
		t.Fatalf("parse control context pack: %v\n%s", err, out)
	}
	if !contextPackIncludes(control, "projection://synthetic-runtime-dashboard") {
		t.Fatalf("control-plane orient should include projection context: %+v", control.Included)
	}
	var projectionRef envelope.IncludedRef
	for _, included := range control.Included {
		if included.Ref == "projection://synthetic-runtime-dashboard" {
			projectionRef = included
			break
		}
	}
	if projectionRef.SourceClass != "generated" || projectionRef.Status != "stale" ||
		!strings.Contains(projectionRef.Reason, "generated-view context evidence") {
		t.Fatalf("projection context ref should be generated/stale evidence, got %+v", projectionRef)
	}
	out = captureStdout(t, func() {
		cmdOrient([]string{"--harness", "codex", "--repo-kind", "product", "--task", "runtime dashboard"})
	})
	var product envelope.ContextPack
	if err := json.Unmarshal([]byte(out), &product); err != nil {
		t.Fatalf("parse product context pack: %v\n%s", err, out)
	}
	if contextPackIncludes(product, "projection://synthetic-runtime-dashboard") {
		t.Fatalf("product orient should not inherit control-plane projection context: %+v", product.Included)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("projection context orientation must not promote claims, stat err=%v", err)
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
	plan, err := decisions.LoadLifecycleRepairPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	plan.Status = "reviewed"
	planData, err := json.MarshalIndent(plan, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, append(planData, '\n'), 0o644); err != nil {
		t.Fatal(err)
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

func TestDecisionsGenericSourceDirImportRepairAndAuthorityCoverage(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	sourceDir := t.TempDir()
	writeSourceFile := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSourceFile("README.md", "# Decision index")
	writeSourceFile("decision-template.md", "# Template")
	writeSourceFile("2026-06-30-complete.md", `---
title: Complete note decision
status: accepted
falsifier: Complete decision no longer protects the behavior.
review_trigger: Re-check when behavior changes.
---
# Complete note decision

## Decision
Use Compound as the decision authority.
`)
	writeSourceFile("2026-06-30-incomplete.md", `---
title: Incomplete note decision
status: accepted
---
# Incomplete note decision

## Decision
Park until lifecycle proof is reviewed.
`)
	importOut := captureStdout(t, func() {
		cmdDecisions([]string{"import", "--source-dir", sourceDir, "--source-kind", "note"})
	})
	if !strings.Contains(importOut, "processed 2 decision sources") ||
		!strings.Contains(importOut, "compound_primary") ||
		!strings.Contains(importOut, "compound_parked") ||
		!strings.Contains(importOut, "- source note-") {
		t.Fatalf("import output =\n%s", importOut)
	}
	repairOut := captureStdout(t, func() {
		cmdDecisions([]string{"repair-plan", "--source-dir", sourceDir, "--source-kind", "note"})
	})
	if !strings.Contains(repairOut, "source_kind: note") || !strings.Contains(repairOut, "repairs: 1") {
		t.Fatalf("repair output =\n%s", repairOut)
	}
	authorityOut := captureStdout(t, func() {
		cmdDecisions([]string{"authority", "--source-dir", sourceDir, "--source-kind", "note"})
	})
	for _, want := range []string{
		"# decisions authority",
		"- status: ok",
		"- eligible: 2",
		"- covered: 2",
		"- missing: 0",
		"source_kind.note: 2",
		"authority_status.compound_primary: 1",
		"authority_status.compound_parked: 1",
	} {
		if !strings.Contains(authorityOut, want) {
			t.Fatalf("authority output missing %q:\n%s", want, authorityOut)
		}
	}
	authorityJSON := captureStdout(t, func() {
		cmdDecisions([]string{"authority", "--source-dir", sourceDir, "--source-kind", "note", "--json"})
	})
	var authorityReport struct {
		SchemaVersion  string         `json:"schema_version"`
		Status         string         `json:"status"`
		AuthorityRef   string         `json:"authority_ref"`
		RecordCount    int            `json:"record_count"`
		Eligible       int            `json:"eligible"`
		Covered        int            `json:"covered"`
		Missing        int            `json:"missing"`
		CountsByKind   map[string]int `json:"counts_by_kind"`
		CountsByStatus map[string]int `json:"counts_by_status"`
	}
	if err := json.Unmarshal([]byte(authorityJSON), &authorityReport); err != nil {
		t.Fatalf("parse authority JSON: %v\n%s", err, authorityJSON)
	}
	if authorityReport.SchemaVersion != "compound.decision_source_authority_coverage.v0.1" ||
		authorityReport.Status != "ok" ||
		authorityReport.AuthorityRef != "compound://registries/decision-source-authority" ||
		authorityReport.RecordCount != 2 ||
		authorityReport.Eligible != 2 ||
		authorityReport.Covered != 2 ||
		authorityReport.Missing != 0 ||
		authorityReport.CountsByKind["note"] != 2 ||
		authorityReport.CountsByStatus["compound_primary"] != 1 ||
		authorityReport.CountsByStatus["compound_parked"] != 1 {
		t.Fatalf("bad authority JSON: %+v\n%s", authorityReport, authorityJSON)
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

func writeCLIJobRosterFixture(t *testing.T, path string) {
	t.Helper()
	data := `{
  "version": 1,
  "jobs": [
    {
      "job_id": "d422776dde1c",
      "name": "ibkr-exit-check-daily",
      "enabled": true,
      "state": "scheduled",
      "schedule": "33 19 * * 1-5",
      "script": "ibkr_exit_check_daily.sh",
      "lane": "no_agent_script",
      "last_status": "error",
      "last_error": "missing python venv",
      "last_run_at": "2026-07-01T10:00:00Z"
    },
    {
      "job_id": "digest",
      "name": "brain digest",
      "enabled": true,
      "state": "scheduled",
      "schedule": "every 6h",
      "script": "recall_digest.ts",
      "last_status": "ok",
      "last_run_at": "2026-07-01T09:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCLIJobTokenUsageFixture(t *testing.T, path string) {
	t.Helper()
	data := `{
  "version": 1,
  "generated_at": "2026-07-01T12:00:00Z",
  "metric": "all_tokens",
  "sessions_count": 8,
  "jobs": [
    {
      "job_id": "digest",
      "name": "brain digest",
      "enabled": true,
      "no_agent": false,
      "runs": 4,
      "total_tokens": 1200000,
      "avg_tokens_per_run": 300000,
      "risk_class": "high_token_agent_job"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRouteImportModelCapabilitiesFeedsCapabilityPlan(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")

	importOut := captureStdout(t, func() {
		cmdRoute([]string{"import-model-capabilities", source})
	})
	if !strings.Contains(importOut, "# route import-model-capabilities") ||
		!strings.Contains(importOut, "- authority: compound://registries/model-router") ||
		!strings.Contains(importOut, "- models: 4") ||
		!strings.Contains(importOut, "- verdict: compound_primary") {
		t.Fatalf("bad import output:\n%s", importOut)
	}

	planOut := captureStdout(t, func() {
		cmdRoute([]string{"plan", "--capability-class", "judge-critique"})
	})
	if !strings.Contains(planOut, "# route capability-plan crp_") ||
		!strings.Contains(planOut, "- routing_rule: routing_defaults[judge-critique]") ||
		!strings.Contains(planOut, "lane_justification: routing_defaults[judge-critique][0] -> auto/judge") ||
		!strings.Contains(planOut, "lane_justification: routing_defaults[judge-critique][1] -> scout/chat") ||
		!strings.Contains(planOut, "cost_class=api-marginal") ||
		!strings.Contains(planOut, "harness=switchback provider_route=auto/judge effort=high") ||
		!strings.Contains(planOut, "role=tripwire_only") ||
		!strings.Contains(planOut, "- verdict: metadata_only_not_task_success") {
		t.Fatalf("bad route plan output:\n%s", planOut)
	}
	planJSONOut := captureStdout(t, func() {
		cmdRoute([]string{"plan", "--capability-class", "judge-critique", "--json"})
	})
	var plan routeevidence.CapabilityRoutePlan
	if err := json.Unmarshal([]byte(planJSONOut), &plan); err != nil {
		t.Fatalf("parse route plan JSON: %v\n%s", err, planJSONOut)
	}
	if plan.SchemaVersion != routeevidence.CapabilityRoutePlanSchemaVersion ||
		plan.AuthorityRef != routeevidence.ModelCapabilitiesAuthorityRef ||
		plan.CapabilityClass != "judge-critique" ||
		plan.RoutingRule != "routing_defaults[judge-critique]" ||
		len(plan.Chain) != 2 {
		t.Fatalf("bad route plan JSON: %+v", plan)
	}
	costFixture := filepath.Join("..", "..", "fixtures", "route", "provider-calls-cost-gaps.ndjson")
	costOut := captureStdout(t, func() {
		cmdRoute([]string{"cost-doctor", costFixture, "--record"})
	})
	if !strings.Contains(costOut, "- candidate: route_cost_") ||
		!strings.Contains(costOut, "- no_promote: nop_") {
		t.Fatalf("bad route cost-doctor output:\n%s", costOut)
	}
	planWithDebtOut := captureStdout(t, func() {
		cmdRoute([]string{"plan", "--capability-class", "judge-critique"})
	})
	if !strings.Contains(planWithDebtOut, "- pending_route_observations: 1") ||
		!strings.Contains(planWithDebtOut, "match=global:provider_cost_coverage") ||
		!strings.Contains(planWithDebtOut, "review=\"compound route plan --candidate route_cost_") ||
		!strings.Contains(planWithDebtOut, "- warning: pending route observations require owner review before routing policy changes") {
		t.Fatalf("route plan missing pending observation debt:\n%s", planWithDebtOut)
	}
	if _, err := os.Stat(filepath.Join(homePath, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route model capability import/plan must not promote claims, stat err=%v", err)
	}
}

func TestRouteEvidenceAuthorityCommandShowsProviderLedgerRows(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	fixture := filepath.Join("..", "..", "fixtures", "route", "provider-calls.ndjson")
	captureStdout(t, func() {
		cmdRoute([]string{"import-provider-ledger", fixture, "--tail", "2"})
	})
	out := captureStdout(t, func() {
		cmdRoute([]string{"evidence-authority", "--json", "--recent", "1"})
	})
	var report routeevidence.AuthorityReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse route evidence authority: %v\n%s", err, out)
	}
	if report.SchemaVersion != routeevidence.RouteEvidenceAuthorityReportSchema ||
		report.AuthorityRef != routeevidence.RouteEvidenceAuthorityRef ||
		report.Status != "ok" ||
		report.RouteEvidenceCount != 2 ||
		report.ProviderLedgerEvidenceCount != 2 ||
		len(report.Recent) != 1 {
		t.Fatalf("bad route evidence authority report: %+v", report)
	}
	if report.Recent[0].ID == "" || report.Recent[0].Provider == "" {
		t.Fatalf("recent route summary missing id/provider: %+v", report.Recent[0])
	}
}

func TestCapabilityToolBoundaryDoctorJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdCapability([]string{"import-topology", "--mcporter-json", filepath.Join("..", "..", "fixtures", "tooltopology", "mcporter-list.json")})
	})
	captureStdout(t, func() {
		cmdCapability([]string{"import-skills", "--os-plugin-board-json", filepath.Join("..", "..", "fixtures", "skills", "os-plugin-board.json")})
	})
	out := captureStdout(t, func() {
		cmdCapability([]string{"tool-boundary-doctor", "--json"})
	})
	var report tooltopology.BoundaryDoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("tool-boundary-doctor --json should parse: %v\n%s", err, out)
	}
	if report.Status != "ok" || report.MCPCount == 0 || report.ToolCount == 0 || report.PluginAdapterCount == 0 || report.PluginSurfaceCount != 0 {
		t.Fatalf("bad boundary doctor report: %+v", report)
	}
}

func TestCapabilityExternalIntakeReportJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	batchDir := filepath.Join(homePath, "inbox", "external-repo-intake", "2026-07-01-test")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(batchDir, "external-repo-intake-map.json")
	if err := os.WriteFile(mapPath, []byte(`{
  "boundaries": ["review-only", "no install"],
  "items": [
    {
      "repo": "example/research",
      "owner": "deep-research",
      "next_review_step": "run freshness eval",
      "not_to_do": "do not auto-run broad research"
    }
  ]
}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(batchDir, "run-summary.json")
	sourceObservationPath := filepath.Join(batchDir, "source-observation.json")
	if err := os.WriteFile(summaryPath, []byte(`{
  "schema_version": "compound.external_repo_intake_run.v0.1",
  "id": "2026-07-01-test",
  "created_at": "2026-07-01T12:00:00Z",
  "map_path": "`+filepath.ToSlash(mapPath)+`",
  "source_observation_path": "`+filepath.ToSlash(sourceObservationPath)+`",
  "candidate_count": 1,
  "source_observe_output": [
    "# source observation srcobs_test",
    "- source_candidate: src_cand_test",
    "- source_no_promote: nop_test"
  ],
  "counts_by_decision": {"keep_needs_review": 1},
  "counts_by_lane": {"freshness_research_skill_candidate": 1},
  "decisions": [
    {
      "repo": "example/research",
      "decision": "keep_needs_review",
      "lane": "freshness_research_skill_candidate",
      "status": "recorded",
      "evidence_strength": 0.91,
      "duplicate_risk": 0.1,
      "review_id": "scr_test"
    }
  ]
}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonOut := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--json"})
	})
	var report capgraph.ExternalRepoIntakeReport
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("external-intake --json should parse: %v\n%s", err, jsonOut)
	}
	if report.Status != "ok" || report.CandidateCount != 1 || report.OpenReviewCount != 1 {
		t.Fatalf("bad report: %+v", report)
	}
	if report.SourceObservationID != "srcobs_test" || len(report.NoPromoteIDs) != 1 || report.NoPromoteIDs[0] != "nop_test" {
		t.Fatalf("source evidence not surfaced: %+v", report)
	}
	if len(report.HighValue) != 1 || report.HighValue[0].Owner != "deep-research" || report.HighValue[0].NextReviewStep != "run freshness eval" {
		t.Fatalf("high-value metadata missing: %+v", report.HighValue)
	}
	filteredJSON := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--repo", "github.com/example/research", "--json"})
	})
	var filtered capgraph.ExternalRepoIntakeReport
	if err := json.Unmarshal([]byte(filteredJSON), &filtered); err != nil {
		t.Fatalf("external-intake --repo --json should parse: %v\n%s", err, filteredJSON)
	}
	if filtered.RepoQuery != "github.com/example/research" || len(filtered.Matches) != 1 || filtered.Matches[0].Repo != "example/research" {
		t.Fatalf("filtered repo report missing match: %+v", filtered)
	}
	humanOut := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--repo", "research"})
	})
	if !strings.Contains(humanOut, "- repo_query: research matches=1") || !strings.Contains(humanOut, "- repo_match: example/research decision=keep_needs_review") {
		t.Fatalf("human repo lookup missing match:\n%s", humanOut)
	}
	sourceListPath := filepath.Join(homePath, "repos.md")
	if err := os.WriteFile(sourceListPath, []byte("https://github.com/example/research\nhttps://github.com/example/unknown\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceListJSON := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--source-list", sourceListPath, "--json"})
	})
	var sourceListReport capgraph.ExternalRepoIntakeReport
	if err := json.Unmarshal([]byte(sourceListJSON), &sourceListReport); err != nil {
		t.Fatalf("external-intake --source-list --json parse: %v\n%s", err, sourceListJSON)
	}
	if sourceListReport.SourceListTotal != 2 || sourceListReport.SourceListKnown != 1 || sourceListReport.SourceListUnknown != 1 {
		t.Fatalf("source-list coverage missing counts: %+v", sourceListReport)
	}
	if sourceListReport.SourceListByDecision["keep_needs_review"] != 1 || sourceListReport.SourceListByLane["freshness_research_skill_candidate"] != 1 || sourceListReport.SourceListByOwner["deep-research"] != 1 {
		t.Fatalf("source-list aggregate counts missing: decision=%+v lane=%+v owner=%+v", sourceListReport.SourceListByDecision, sourceListReport.SourceListByLane, sourceListReport.SourceListByOwner)
	}
	if sourceListReport.SourceListByOwnerReviewStatus["pending"] != 1 || sourceListReport.SourceListByCapabilityEvaluationStatus["blocked_on_owner_review"] != 1 {
		t.Fatalf("source-list status counts missing: owner_review=%+v capability_eval=%+v", sourceListReport.SourceListByOwnerReviewStatus, sourceListReport.SourceListByCapabilityEvaluationStatus)
	}
	if len(sourceListReport.SourceListReviewNext) != 1 || sourceListReport.SourceListReviewNext[0].Matches[0].Repo != "example/research" {
		t.Fatalf("source-list review_next missing match: %+v", sourceListReport.SourceListReviewNext)
	}
	if sourceListReport.SourceListReviewNextCount == nil || *sourceListReport.SourceListReviewNextCount != 1 || sourceListReport.SourceListEvalNextCount == nil || *sourceListReport.SourceListEvalNextCount != 0 {
		t.Fatalf("source-list follow-up counts missing: review=%v eval=%v", sourceListReport.SourceListReviewNextCount, sourceListReport.SourceListEvalNextCount)
	}
	if sourceListReport.SourceListReviewNextMeaning != "owner_review_suggestions_not_open_review_debt" || sourceListReport.SourceListEvalNextMeaning != "focused_capability_evaluation_follow_up_queue" {
		t.Fatalf("source-list follow-up meanings missing: review=%q eval=%q", sourceListReport.SourceListReviewNextMeaning, sourceListReport.SourceListEvalNextMeaning)
	}
	if sourceListReport.SourceListItems[0].Status != "known" || sourceListReport.SourceListItems[0].Matches[0].Repo != "example/research" {
		t.Fatalf("source-list known item missing match: %+v", sourceListReport.SourceListItems)
	}
	sourceListText := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--source-list", sourceListPath})
	})
	for _, want := range []string{
		"- source_list_owner_review.pending: 1",
		"- source_list_capability_evaluation.blocked_on_owner_review: 1",
		"- source_list_review_next: example/research",
		"- source_list_known_omitted: 1 details=--json",
	} {
		if !strings.Contains(sourceListText, want) {
			t.Fatalf("source-list text missing %q:\n%s", want, sourceListText)
		}
	}
	if strings.Contains(sourceListText, "- source_list_known:") {
		t.Fatalf("source-list text should not dump known rows by default:\n%s", sourceListText)
	}
	reviewPlanJSON := captureStdout(t, func() {
		cmdCapability([]string{"external-intake-review-plan", "--source-list", sourceListPath, "--json"})
	})
	var reviewPlan capgraph.ExternalRepoIntakeReviewPlanReport
	if err := json.Unmarshal([]byte(reviewPlanJSON), &reviewPlan); err != nil {
		t.Fatalf("external-intake-review-plan --json parse: %v\n%s", err, reviewPlanJSON)
	}
	if reviewPlan.Status != "ok" || reviewPlan.WroteCount != 1 || len(reviewPlan.Items) != 1 || reviewPlan.Items[0].Repo != "example/research" {
		t.Fatalf("external-intake-review-plan report missing artifact: %+v", reviewPlan)
	}
	if _, err := os.Stat(reviewPlan.Items[0].JSONPath); err != nil {
		t.Fatalf("review artifact JSON not written: %v", err)
	}
	if reviewPlan.Items[0].ReviewYAMLPath == "" {
		t.Fatalf("review YAML not written: %+v", reviewPlan.Items[0])
	}
	reviewDraft, err := os.ReadFile(reviewPlan.Items[0].ReviewYAMLPath)
	if err != nil {
		t.Fatalf("read review YAML: %v", err)
	}
	editedReview := strings.ReplaceAll(string(reviewDraft), `rationale: ""`, `rationale: "reviewed source-list repo against current Compound owners"`)
	if err := os.WriteFile(reviewPlan.Items[0].ReviewYAMLPath, []byte(editedReview), 0o644); err != nil {
		t.Fatalf("write edited review YAML: %v", err)
	}
	reviewJSON := captureStdout(t, func() {
		cmdCapability([]string{"external-intake-review", reviewPlan.Items[0].ReviewYAMLPath, "--json"})
	})
	var reviewReport capgraph.ExternalRepoIntakeReviewReport
	if err := json.Unmarshal([]byte(reviewJSON), &reviewReport); err != nil {
		t.Fatalf("external-intake-review --json parse: %v\n%s", err, reviewJSON)
	}
	if reviewReport.Status != "recorded" || reviewReport.Repo != "example/research" || reviewReport.Decision != "keep_needs_review" {
		t.Fatalf("external-intake-review report missing recorded review: %+v", reviewReport)
	}
	if reviewReport.Rationale != "reviewed source-list repo against current Compound owners" || reviewReport.SourceHash == "" || reviewReport.SourceRef == "" || reviewReport.ReviewedAt == "" || len(reviewReport.Boundaries) == 0 {
		t.Fatalf("external-intake-review report should preserve reviewed evidence: %+v", reviewReport)
	}
	reviewsJSON := captureStdout(t, func() {
		cmdReviews([]string{"--area", "external-repo-intake", "--json"})
	})
	var reviews reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(reviewsJSON), &reviews); err != nil {
		t.Fatalf("reviews external-repo-intake parse: %v\n%s", err, reviewsJSON)
	}
	if reviews.Counts.Total != 1 || reviews.Counts.ReviewRequired != 0 || reviews.Counts.OK != 1 || reviews.Items[0].Status != "recorded" || reviews.Items[0].Readiness != "review_recorded" {
		t.Fatalf("external repo review should close queue item: counts=%+v item=%+v", reviews.Counts, reviews.Items[0])
	}
	afterReviewJSON := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--source-list", sourceListPath, "--json"})
	})
	var afterReview capgraph.ExternalRepoIntakeReport
	if err := json.Unmarshal([]byte(afterReviewJSON), &afterReview); err != nil {
		t.Fatalf("external-intake after review --json parse: %v\n%s", err, afterReviewJSON)
	}
	if len(afterReview.SourceListReviewNext) != 0 || len(afterReview.SourceListEvalNext) != 1 {
		t.Fatalf("recorded owner review should move source-list item from review-next to evaluation-next: %+v", afterReview)
	}
	if afterReview.SourceListReviewNextCount == nil || *afterReview.SourceListReviewNextCount != 0 || afterReview.SourceListEvalNextCount == nil || *afterReview.SourceListEvalNextCount != 1 {
		t.Fatalf("recorded owner review follow-up counts missing: review=%v eval=%v", afterReview.SourceListReviewNextCount, afterReview.SourceListEvalNextCount)
	}
	evalMatch := afterReview.SourceListEvalNext[0].Matches[0]
	if evalMatch.OwnerReviewStatus != "recorded" || evalMatch.OwnerReviewPath == "" || evalMatch.OwnerReviewNextAction == "" {
		t.Fatalf("evaluation follow-up missing recorded review context: %+v", evalMatch)
	}
	humanAfterReview := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--source-list", sourceListPath})
	})
	if !strings.Contains(humanAfterReview, "- source_list_follow_up: review_next=0 meaning=owner_review_suggestions_not_open_review_debt evaluation_next=1 meaning=focused_capability_evaluation_follow_up_queue") {
		t.Fatalf("human source-list output should label follow-up meanings:\n%s", humanAfterReview)
	}
	if !strings.Contains(humanAfterReview, "- source_list_evaluation_next: example/research") || strings.Contains(humanAfterReview, "- source_list_review_next: example/research") {
		t.Fatalf("human source-list output should show evaluation-next only after review:\n%s", humanAfterReview)
	}
	evalPlanJSON := captureStdout(t, func() {
		cmdCapability([]string{"external-intake-evaluation-plan", "--source-list", sourceListPath, "--repo", "github.com/example/research", "--json"})
	})
	var evalPlan capgraph.ExternalRepoCapabilityEvaluationPlanReport
	if err := json.Unmarshal([]byte(evalPlanJSON), &evalPlan); err != nil {
		t.Fatalf("external-intake-evaluation-plan --json parse: %v\n%s", err, evalPlanJSON)
	}
	if evalPlan.Status != "review_required" || evalPlan.Repo != "example/research" || evalPlan.OwnerReviewStatus != "recorded" {
		t.Fatalf("external-intake-evaluation-plan report = %+v", evalPlan)
	}
	if evalPlan.EvaluationPath == "" || evalPlan.PlanPath == "" || evalPlan.ReviewYAMLPath == "" || evalPlan.ManifestDraftPath == "" {
		t.Fatalf("external-intake-evaluation-plan paths missing: %+v", evalPlan)
	}
	if _, err := os.Stat(filepath.Join(homePath, "registries", "capability-nodes.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("external-intake-evaluation-plan must not mutate capability graph, stat err=%v", err)
	}
	evalReviewDraft, err := os.ReadFile(evalPlan.ReviewYAMLPath)
	if err != nil {
		t.Fatalf("read evaluation review YAML: %v", err)
	}
	editedEvalReview := strings.ReplaceAll(string(evalReviewDraft), "decision: TODO_activate_reject_or_keep_needs_review", "decision: keep_needs_review")
	editedEvalReview = strings.ReplaceAll(editedEvalReview, `rationale: ""`, `rationale: "reviewed external repo evaluation; keep manifest parked for explicit graph import"`)
	editedEvalReview = strings.ReplaceAll(editedEvalReview, "confidence: -1", "confidence: 0.82")
	if err := os.WriteFile(evalPlan.ReviewYAMLPath, []byte(editedEvalReview), 0o644); err != nil {
		t.Fatalf("write evaluation review YAML: %v", err)
	}
	evalReviewText := captureStdout(t, func() {
		cmdCapability([]string{"evaluation-review", evalPlan.ReviewYAMLPath})
	})
	if !strings.Contains(evalReviewText, "- status: manifest_ready") || !strings.Contains(evalReviewText, "- node_status: needs_review") {
		t.Fatalf("evaluation-review text missing reviewed manifest status:\n%s", evalReviewText)
	}
	humanAfterEvalReview := captureStdout(t, func() {
		cmdCapability([]string{"external-intake", "--source-list", sourceListPath})
	})
	for _, want := range []string{
		"- source_list_capability_graph.reviewed_manifest_pending_import: 1",
		"- source_list_graph_import_follow_up: graph_import_next=1 omitted=0 meaning=reviewed_manifest_import_queue_not_graph_mutation",
		"- source_list_graph_import_next: example/research",
	} {
		if !strings.Contains(humanAfterEvalReview, want) {
			t.Fatalf("human source-list output missing graph-import follow-up %q:\n%s", want, humanAfterEvalReview)
		}
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

func createReleaseGoalHoldsDB(t *testing.T, dbPath string) {
	t.Helper()
	stmt := `
create table goals (
  goal_id text primary key,
  app_id text not null,
  objective text not null,
  state text not null,
  artifact_dir text not null,
  created_at text not null,
  updated_at text not null
);
create table packets (
  packet_id text primary key,
  goal_id text not null,
  lane text not null,
  state text not null,
  packet_json text not null,
  updated_at text not null
);
create table events (
  id integer primary key autoincrement,
  goal_id text not null,
  event_type text not null,
  payload_json text not null,
  created_at text not null
);
insert into goals(goal_id, app_id, objective, state, artifact_dir, created_at, updated_at) values
  ('release-hold-fixture', 'phone-cleaner', 'Keep phone-cleaner release current', 'HOLD', '/tmp/release-hold-fixture', '2026-07-01T00:00:00', '2026-07-01T00:00:00'),
  ('release-hold-before-gate', 'old-app', 'Old hold', 'HOLD', '/tmp/old-hold', '2026-06-10T11:00:00', '2026-06-10T11:00:00'),
  ('release-approved-fixture', 'approved-app', 'Approved release', 'APPROVED', '/tmp/approved', '2026-07-01T00:00:00', '2026-07-01T00:00:00');
`
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(stmt)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create release-goal sqlite fixture: %v\n%s", err, out)
	}
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

func sameStringSlice(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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

func readCandidateRows(t *testing.T, dir string) []distiller.Candidate {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read candidate dir: %v", err)
	}
	var out []distiller.Candidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read candidate %s: %v", entry.Name(), err)
		}
		var c distiller.Candidate
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("parse candidate %s: %v", entry.Name(), err)
		}
		out = append(out, c)
	}
	return out
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

func hasMatchedTokenCount(values []capgraph.MatchedTokenCount, token string, count int) bool {
	for _, value := range values {
		if value.Token == token && value.Count == count {
			return true
		}
	}
	return false
}

func sourceRevisionExists(revs []envelope.SourceRev, source string) bool {
	for _, rev := range revs {
		if rev.Source == source {
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

func compoundCommandGitStatus(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "-b")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status failed: %v\n%s", err, string(out))
	}
	return string(out)
}
