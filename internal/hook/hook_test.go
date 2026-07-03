package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/work"
)

var fixedNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func baseDeps() Deps {
	return Deps{
		Registry: harness.DefaultRegistry(),
		Orient:   contract.DefaultPolicy(),
		Policy:   policy.DefaultConfig(),
	}
}

func kinds(produced []envelope.Envelope) []string {
	out := make([]string, len(produced))
	for i, e := range produced {
		out[i] = e.Kind()
	}
	return out
}

func TestSessionStartProducesContract(t *testing.T) {
	fx := Fixture{Hook: "session-start", Harness: "claude_code", Cwd: "/repo/x", Task: "audit", RepoKind: "product"}
	res, err := Run(fx, baseDeps(), fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Event.Status != "ok" {
		t.Errorf("status = %q, want ok", res.Event.Status)
	}
	if got := kinds(res.Produced); len(got) != 3 || got[0] != "context_request" || got[1] != "effective_contract" || got[2] != "context_pack" {
		t.Errorf("produced = %v, want [context_request effective_contract context_pack]", got)
	}
	if res.Summary == "" {
		t.Error("session-start should return an orientation summary")
	}
	req := res.Produced[0].(*envelope.ContextRequest)
	ec := res.Produced[1].(*envelope.EffectiveContract)
	if req.IntentTextRef != "sha256:"+hashStr("audit") {
		t.Fatalf("intent_text_ref = %q, want task hash", req.IntentTextRef)
	}
	if req.RequestedMode != "observe" || ec.TaskMode != "observe" || req.Constraints.AllowRawCorpus || req.Constraints.AllowPrivateMemory || req.Constraints.AllowProvisionalReviews {
		t.Fatalf("unexpected context request constraints: %+v mode=%q", req.Constraints, req.RequestedMode)
	}
	if res.Injection != nil {
		t.Fatalf("session-start without HomePath should not write runtime injection, got %+v", res.Injection)
	}
}

func TestHookModeAffectsContextRequestIDs(t *testing.T) {
	base := Fixture{Hook: "session-start", Harness: "claude_code", Cwd: "/repo/x", Task: "audit", RepoKind: "product"}
	observe := base
	observe.Mode = "observe"
	execute := base
	execute.Mode = "execute"

	observeRes, err := Run(observe, baseDeps(), fixedNow)
	if err != nil {
		t.Fatalf("observe run: %v", err)
	}
	executeRes, err := Run(execute, baseDeps(), fixedNow)
	if err != nil {
		t.Fatalf("execute run: %v", err)
	}
	observeReq := observeRes.Produced[0].(*envelope.ContextRequest)
	executeReq := executeRes.Produced[0].(*envelope.ContextRequest)
	if observeReq.ID == executeReq.ID {
		t.Fatalf("context request IDs should differ by mode: observe=%s execute=%s", observeReq.ID, executeReq.ID)
	}
	if observeReq.IDs.ContractID == executeReq.IDs.ContractID {
		t.Fatalf("context request contract IDs should differ by mode: observe=%s execute=%s", observeReq.IDs.ContractID, executeReq.IDs.ContractID)
	}
}

func TestHookEventIDIncludesInputHash(t *testing.T) {
	homePath := t.TempDir()
	deps := baseDeps()
	deps.HomePath = homePath

	first, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/x", Task: "audit", RepoKind: "product"}, deps, fixedNow)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/x", Task: "ship", RepoKind: "product"}, deps, fixedNow)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.Event.ID == second.Event.ID {
		t.Fatalf("same-second hook events should differ by input hash: first=%s second=%s", first.Event.ID, second.Event.ID)
	}
	if first.Injection == nil || second.Injection == nil {
		t.Fatalf("expected both runs to persist injections: first=%+v second=%+v", first.Injection, second.Injection)
	}
	if first.Injection.Ref == second.Injection.Ref {
		t.Fatalf("same-second hook injections should not collide: first=%s second=%s", first.Injection.Ref, second.Injection.Ref)
	}
	for _, path := range []string{first.Injection.Path, second.Injection.Path} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing injection artifact %s: %v", path, err)
		}
	}
}

func TestContextHookInputHashIncludesNormalizedScope(t *testing.T) {
	homePath := t.TempDir()
	deps := baseDeps()
	deps.HomePath = homePath

	first, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/a", Task: "audit", RepoKind: "product"}, deps, fixedNow)
	if err != nil {
		t.Fatalf("first session-start: %v", err)
	}
	second, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/b", Task: "audit", RepoKind: "product"}, deps, fixedNow)
	if err != nil {
		t.Fatalf("second session-start: %v", err)
	}
	if first.Event.InputHash == second.Event.InputHash || first.Event.ID == second.Event.ID {
		t.Fatalf("session-start identity should include cwd: first=%s/%s second=%s/%s", first.Event.InputHash, first.Event.ID, second.Event.InputHash, second.Event.ID)
	}

	raw := "same raw prompt"
	promptA, err := Run(Fixture{Hook: "user-prompt-submit", Harness: "codex", Cwd: "/repo/x", Task: "pricing", RepoKind: "product", Prompt: raw}, deps, fixedNow)
	if err != nil {
		t.Fatalf("first user-prompt-submit: %v", err)
	}
	promptB, err := Run(Fixture{Hook: "user-prompt-submit", Harness: "codex", Cwd: "/repo/x", Task: "onboarding", RepoKind: "product", Prompt: raw}, deps, fixedNow)
	if err != nil {
		t.Fatalf("second user-prompt-submit: %v", err)
	}
	if promptA.Event.InputHash == promptB.Event.InputHash || promptA.Event.ID == promptB.Event.ID {
		t.Fatalf("user-prompt identity should include task as well as prompt hash: first=%s/%s second=%s/%s", promptA.Event.InputHash, promptA.Event.ID, promptB.Event.InputHash, promptB.Event.ID)
	}
	for _, res := range []Result{promptA, promptB} {
		blob, _ := json.Marshal(res)
		if strings.Contains(string(blob), raw) {
			t.Fatalf("raw prompt leaked into result:\n%s", string(blob))
		}
	}
}

func TestSessionStartWritesHarnessFilteredInjection(t *testing.T) {
	homePath := t.TempDir()
	manifest, err := capgraph.LoadSkillManifest(filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml"))
	if err != nil {
		t.Fatalf("load skill manifest: %v", err)
	}
	if _, err := capgraph.ApplySkillManifest(homePath, manifest, fixedNow); err != nil {
		t.Fatalf("apply skill manifest: %v", err)
	}
	if _, err := contract.ImportCodexHooksJSON(homePath, filepath.Join("..", "..", "fixtures", "harness", "codex-hooks-json.json"), fixedNow); err != nil {
		t.Fatalf("import codex hooks: %v", err)
	}

	deps := baseDeps()
	deps.HomePath = homePath
	fx := Fixture{
		Hook:     "session-start",
		Harness:  "codex",
		Cwd:      "/repo/productX",
		Task:     "edit pricing",
		RepoKind: "product",
		Prompt:   "DO NOT LEAK RAW PROMPT BODY",
	}
	res, err := Run(fx, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Injection == nil {
		t.Fatal("session-start should record an injection artifact when HomePath is set")
	}
	if filepath.Base(res.Injection.Path) != res.Event.Header.ID+".md" {
		t.Fatalf("injection path = %q, want hook event id filename", res.Injection.Path)
	}
	if _, err := os.Stat(res.Injection.ContextRequestPath); err != nil {
		t.Fatalf("context request json missing: %v", err)
	}
	if _, err := os.Stat(res.Injection.ContextPackPath); err != nil {
		t.Fatalf("context pack json missing: %v", err)
	}
	reqBytes, err := os.ReadFile(res.Injection.ContextRequestPath)
	if err != nil {
		t.Fatalf("read context request: %v", err)
	}
	var req envelope.ContextRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		t.Fatalf("parse context request: %v", err)
	}
	if req.IntentTextRef != "sha256:"+hashStr("DO NOT LEAK RAW PROMPT BODY") {
		t.Fatalf("intent_text_ref = %q, want prompt hash", req.IntentTextRef)
	}
	if req.TaskGuess != "edit pricing" || req.Harness != "codex" {
		t.Fatalf("unexpected request body: %+v", req)
	}
	if strings.Contains(string(reqBytes), "DO NOT LEAK RAW PROMPT BODY") {
		t.Fatalf("context request leaked raw prompt:\n%s", string(reqBytes))
	}
	packBytes, err := os.ReadFile(res.Injection.ContextPackPath)
	if err != nil {
		t.Fatalf("read context pack: %v", err)
	}
	var pack envelope.ContextPack
	if err := json.Unmarshal(packBytes, &pack); err != nil {
		t.Fatalf("parse context pack: %v", err)
	}
	if !containsString(pack.ActiveHooks, "hook://codex/SessionStart") {
		t.Fatalf("context pack active hooks = %v, want hook://codex/SessionStart", pack.ActiveHooks)
	}
	if readiness := findContextHookReadiness(pack.HookReadiness, "codex", "PreToolUse"); readiness == nil || readiness.Status != "missing_gate" || !readiness.AdapterRequired {
		t.Fatalf("context pack hook readiness = %+v, want codex missing_gate adapter-required", pack.HookReadiness)
	}
	if pack.IDs.ContractID == "" {
		t.Fatal("context pack missing contract_id")
	}
	contractRef := filepath.Join("contracts", "effective", fixedNow.UTC().Format("2006-01-02"), pack.IDs.ContractID+".json")
	contractPath := filepath.Join(homePath, contractRef)
	if res.Injection.ContractID != pack.IDs.ContractID || res.Injection.ContractRef != contractRef || res.Injection.ContractPath != contractPath {
		t.Fatalf("contract injection artifact = %q/%q/%q, want %q/%q/%q", res.Injection.ContractID, res.Injection.ContractRef, res.Injection.ContractPath, pack.IDs.ContractID, contractRef, contractPath)
	}
	contractBytes, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read effective contract artifact: %v", err)
	}
	var ec envelope.EffectiveContract
	if err := json.Unmarshal(contractBytes, &ec); err != nil {
		t.Fatalf("parse effective contract artifact: %v", err)
	}
	if ec.ID != pack.IDs.ContractID {
		t.Fatalf("effective contract id = %q, want %q", ec.ID, pack.IDs.ContractID)
	}
	b, err := os.ReadFile(res.Injection.Path)
	if err != nil {
		t.Fatalf("read injection: %v", err)
	}
	text := string(b)
	for _, want := range []string{
		"## Active Hooks",
		"hook://codex/SessionStart",
		"## Hook Gate Readiness",
		"`codex/PreToolUse` status=missing_gate",
		"adapter_required=true",
		"command=`compound hook pre-tool-use --enforce FIXTURE_JSON`",
		"## Hook Roster",
		"capability://skill:codex-pricing",
		"capability://skill:generic-pricing",
		"[offer, not execution]",
		"Capability refs are offers only",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("injection missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{
		"capability://skill:claude-pricing",
		"DO NOT LEAK RAW PROMPT BODY",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("injection leaked forbidden content %q:\n%s", forbidden, text)
		}
	}
	if got := res.Event.SideEffects; len(got) != 4 || got[0] != res.Injection.ContextRequestRef || got[1] != contractRef || got[2] != res.Injection.ContextPackRef || got[3] != res.Injection.Ref {
		t.Fatalf("side effects = %v, want request, contract, context pack, injection refs", got)
	}
}

func TestSessionStartInjectionIncludesRelevantCompoundMemory(t *testing.T) {
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{{
		Entity:          "compound",
		Predicate:       "capability_authority_surface",
		Object:          "Compound owns the capability graph runtime surface",
		Confidence:      0.91,
		Derivation:      "deterministic",
		ClaimType:       "architecture",
		Qualifiers:      map[string]string{"domain": "capability_authority"},
		PromotionBasis:  candidates.BasisCuratedPage,
		SourceTraceRef:  "ev_compound",
		SnippetHash:     "sha256:compound",
		Visibility:      "contextual",
		ObservationDate: "2026-06-30T00:00:00Z",
	}}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := store.Adjudicate(got[0].ID, "promote", "test", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	fx := Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/compound", Task: "resume compound absorption", RepoKind: "control-plane"}
	res, err := Run(fx, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantLine := "Compound memory (owned ledger): compound.capability_authority_surface"
	if !strings.Contains(res.Summary, wantLine) {
		t.Fatalf("summary missing Compound memory line:\n%s", res.Summary)
	}
	if res.Injection == nil {
		t.Fatal("session-start should persist injection with HomePath")
	}
	b, err := os.ReadFile(res.Injection.Path)
	if err != nil {
		t.Fatalf("read injection: %v", err)
	}
	text := string(b)
	if !strings.Contains(text, wantLine) {
		t.Fatalf("injection missing Compound memory line:\n%s", text)
	}
	if !strings.Contains(text, "claim://"+promoted.Claim.ID) {
		t.Fatalf("injection missing claim ref claim://%s:\n%s", promoted.Claim.ID, text)
	}
	if !strings.Contains(text, "## Active Memory") {
		t.Fatalf("injection missing Active Memory section:\n%s", text)
	}
}

func TestRenderInjectionEmitsActiveMemorySection(t *testing.T) {
	pack := envelope.ContextPack{
		SummaryMD:    "summary",
		ActiveMemory: []string{"claim://claim_a", "claim://claim_b"},
		Included: []envelope.IncludedRef{{
			Ref:    "claim://claim_a",
			Reason: "Compound memory ledger claim; source stores indexes evidence, not authority: compound.capability_authority_surface = Compound owns capability graph runtime surface",
		}},
	}
	pack.Header.ID = "ctx_test"
	text := renderInjection("hookev_test", pack)
	if !strings.Contains(text, "## Active Memory") {
		t.Fatalf("injection missing Active Memory section:\n%s", text)
	}
	for _, want := range []string{
		"`claim://claim_a` - compound.capability_authority_surface = Compound owns capability graph runtime surface",
		"`claim://claim_b`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("injection missing active memory ref %q:\n%s", want, text)
		}
	}

	empty := envelope.ContextPack{SummaryMD: "summary"}
	empty.Header.ID = "ctx_test"
	if got := renderInjection("hookev_test", empty); strings.Contains(got, "## Active Memory") {
		t.Fatalf("empty active memory should not emit a section header:\n%s", got)
	}
}

func TestRenderInjectionCapsActiveMemorySection(t *testing.T) {
	refs := make([]string, 0, maxInjectionActiveMemoryRefs+2)
	for i := 0; i < maxInjectionActiveMemoryRefs+2; i++ {
		refs = append(refs, fmt.Sprintf("claim://claim_%02d", i))
	}
	pack := envelope.ContextPack{SummaryMD: "summary", ActiveMemory: refs}
	pack.Header.ID = "ctx_test"
	text := renderInjection("hookev_test", pack)
	for _, want := range refs[:maxInjectionActiveMemoryRefs] {
		if !strings.Contains(text, "`"+want+"`") {
			t.Fatalf("injection missing active memory ref %q:\n%s", want, text)
		}
	}
	for _, forbidden := range refs[maxInjectionActiveMemoryRefs:] {
		if strings.Contains(text, forbidden) {
			t.Fatalf("injection should cap active memory refs, got %q:\n%s", forbidden, text)
		}
	}
	if !strings.Contains(text, "2 more active memory ref(s) omitted") {
		t.Fatalf("injection missing omission note for capped active memory:\n%s", text)
	}
}

func TestUserPromptProducesCandidateAndHidesPrompt(t *testing.T) {
	raw := "the onboarding copy is confusing, please fix it"
	fx := Fixture{Hook: "user-prompt-submit", Harness: "claude_code", Task: "fix onboarding", RepoKind: "product", Prompt: raw}
	res, err := Run(fx, baseDeps(), fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 1 || got[0] != "work" {
		t.Fatalf("produced = %v, want [work]", got)
	}
	cand := res.Produced[0].(*envelope.WorkEnvelope)
	if cand.State != "proposed" {
		t.Errorf("candidate state = %q, want proposed", cand.State)
	}
	// Privacy: the raw prompt must appear NOWHERE in the produced output, only its hash.
	blob, _ := json.Marshal(res)
	if strings.Contains(string(blob), raw) {
		t.Error("raw prompt leaked into hook output; only input_hash should be stored")
	}
	if res.Event.InputHash == "" {
		t.Error("user-prompt-submit must record an input_hash")
	}
}

func TestUserPromptPersistsProposedWorkWithContext(t *testing.T) {
	homePath := t.TempDir()
	manifest, err := capgraph.LoadSkillManifest(filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml"))
	if err != nil {
		t.Fatalf("load skill manifest: %v", err)
	}
	if _, err := capgraph.ApplySkillManifest(homePath, manifest, fixedNow); err != nil {
		t.Fatalf("apply skill manifest: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	deps.Store = store
	raw := "DO NOT LEAK RAW PROMPT BODY"
	fx := Fixture{
		Hook:     "user-prompt-submit",
		Harness:  "codex",
		Cwd:      "/repo/productX",
		Task:     "improve pricing",
		RepoKind: "product",
		Prompt:   raw,
	}
	res, err := Run(fx, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 4 || got[0] != "context_request" || got[1] != "effective_contract" || got[2] != "context_pack" || got[3] != "work" {
		t.Fatalf("produced = %v, want [context_request effective_contract context_pack work]", got)
	}
	req := res.Produced[0].(*envelope.ContextRequest)
	ec := res.Produced[1].(*envelope.EffectiveContract)
	pack := res.Produced[2].(*envelope.ContextPack)
	cand := res.Produced[3].(*envelope.WorkEnvelope)
	if req.IntentTextRef != "sha256:"+hashStr(raw) {
		t.Fatalf("intent_text_ref = %q, want prompt hash", req.IntentTextRef)
	}
	if req.RequestedMode != "execute" || ec.TaskMode != "execute" {
		t.Fatalf("hook mode mismatch: request=%q contract=%q", req.RequestedMode, ec.TaskMode)
	}
	if cand.State != "proposed" || cand.IDs.ContextPackID != pack.Header.ID {
		t.Fatalf("work state/context = %q/%q, want proposed/%q", cand.State, cand.IDs.ContextPackID, pack.Header.ID)
	}
	contractRef := filepath.Join("contracts", "effective", fixedNow.UTC().Format("2006-01-02"), pack.IDs.ContractID+".json")
	if res.Injection.ContractID != pack.IDs.ContractID || res.Injection.ContractRef != contractRef {
		t.Fatalf("contract injection artifact = %q/%q, want %q/%q", res.Injection.ContractID, res.Injection.ContractRef, pack.IDs.ContractID, contractRef)
	}
	stored, events, err := store.Show(cand.ID)
	if err != nil {
		t.Fatalf("show stored work: %v", err)
	}
	if stored.IDs.ContextPackID != pack.Header.ID {
		t.Fatalf("stored context_pack_id = %q, want %q", stored.IDs.ContextPackID, pack.Header.ID)
	}
	if len(events) != 1 || events[0].To != "proposed" || !containsString(events[0].Attached, pack.Header.ID) {
		t.Fatalf("lifecycle events = %+v, want proposed attached to context pack", events)
	}
	hookEventPath := filepath.Join(homePath, "events", res.Event.ID+".json")
	hookStreamPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	for _, path := range []string{res.Injection.ContextRequestPath, res.Injection.ContextPackPath, res.Injection.Path, filepath.Join(homePath, "work", "work", cand.ID+".json"), hookEventPath, hookStreamPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected persisted artifact %s: %v", path, err)
		}
	}
	hookBytes, err := os.ReadFile(hookEventPath)
	if err != nil {
		t.Fatalf("read hook event: %v", err)
	}
	var persisted envelope.HookEvent
	if err := json.Unmarshal(hookBytes, &persisted); err != nil {
		t.Fatalf("parse hook event: %v", err)
	}
	if persisted.IDs.WorkID != cand.ID || persisted.IDs.ContextPackID != pack.ID {
		t.Fatalf("hook ids = %+v, want work %s context %s", persisted.IDs, cand.ID, pack.ID)
	}
	streamBytes, err := os.ReadFile(hookStreamPath)
	if err != nil {
		t.Fatalf("read hook stream: %v", err)
	}
	if !strings.Contains(string(streamBytes), res.Event.ID) {
		t.Fatalf("hook stream missing event %s:\n%s", res.Event.ID, string(streamBytes))
	}
	blob, _ := json.Marshal(res)
	if strings.Contains(string(blob), raw) {
		t.Fatalf("raw prompt leaked into result:\n%s", string(blob))
	}
	if strings.Contains(string(hookBytes), raw) || strings.Contains(string(streamBytes), raw) {
		t.Fatalf("raw prompt leaked into hook persistence")
	}
	if !containsString(res.Event.SideEffects, filepath.Join("work", "work", cand.ID+".json")) {
		t.Fatalf("side effects = %v, want persisted work ref", res.Event.SideEffects)
	}
	if !containsString(res.Event.SideEffects, contractRef) {
		t.Fatalf("side effects = %v, want contract ref %s", res.Event.SideEffects, contractRef)
	}
}

func TestPromptWorkClosesWithContextTransition(t *testing.T) {
	homePath := t.TempDir()
	manifest, err := capgraph.LoadSkillManifest(filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml"))
	if err != nil {
		t.Fatalf("load skill manifest: %v", err)
	}
	if _, err := capgraph.ApplySkillManifest(homePath, manifest, fixedNow); err != nil {
		t.Fatalf("apply skill manifest: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	deps.Store = store

	start, err := Run(Fixture{
		Hook:     "user-prompt-submit",
		Harness:  "codex",
		Cwd:      "/repo/productX",
		Task:     "improve pricing",
		RepoKind: "product",
		Prompt:   "prepare a pricing test",
	}, deps, fixedNow)
	if err != nil {
		t.Fatalf("user prompt: %v", err)
	}
	pack := start.Produced[2].(*envelope.ContextPack)
	w := start.Produced[3].(*envelope.WorkEnvelope)
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "ready", Note: "admit prompt work"}, fixedNow); err != nil {
		t.Fatalf("advance ready: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", RunID: "run_pricing_test"}, fixedNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying", ArtifactID: "artifact_pricing_plan", VerificationID: "ver_pricing_plan"}, fixedNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	closed, err := Run(Fixture{
		Hook:     "session-end",
		Harness:  "codex",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "pricing test prepared",
	}, deps, fixedNow)
	if err != nil {
		t.Fatalf("session end: %v", err)
	}
	if got := kinds(closed.Produced); len(got) != 2 || got[0] != "receipt" || got[1] != "transition" {
		t.Fatalf("closed produced = %v, want [receipt transition]", got)
	}
	rec := closed.Produced[0].(*envelope.Receipt)
	tr := closed.Produced[1].(*envelope.Transition)
	if rec.IDs.WorkID != w.ID || tr.IDs.WorkID != w.ID {
		t.Fatalf("work id chain broken: receipt=%q transition=%q work=%q", rec.IDs.WorkID, tr.IDs.WorkID, w.ID)
	}
	if rec.IDs.ContextPackID != pack.Header.ID || tr.IDs.ContextPackID != pack.Header.ID {
		t.Fatalf("context pack chain broken: receipt=%q transition=%q pack=%q", rec.IDs.ContextPackID, tr.IDs.ContextPackID, pack.Header.ID)
	}
	for _, want := range []string{"capability://skill:codex-pricing", "capability://skill:generic-pricing"} {
		if !containsString(rec.Skills.Candidate, want) {
			t.Fatalf("receipt candidate skills = %v, missing %s", rec.Skills.Candidate, want)
		}
		if !containsString(tr.Projections.Skill, want) {
			t.Fatalf("transition skill projections = %v, missing %s", tr.Projections.Skill, want)
		}
	}
	if containsString(rec.Skills.Candidate, "capability://skill:claude-pricing") || containsString(tr.Projections.Skill, "capability://skill:claude-pricing") {
		t.Fatalf("closed chain leaked claude-only offer: receipt=%v transition=%v", rec.Skills.Candidate, tr.Projections.Skill)
	}
	if len(tr.BeforeRefs) != 1 || tr.BeforeRefs[0] != pack.Header.ID {
		t.Fatalf("transition before_refs = %v, want [%s]", tr.BeforeRefs, pack.Header.ID)
	}
	if !containsString(tr.ActionRefs, "run_pricing_test") {
		t.Fatalf("transition action_refs = %v, want run_pricing_test", tr.ActionRefs)
	}
	if !containsString(tr.AfterRefs, "artifact_pricing_plan") || !containsString(tr.AfterRefs, rec.ID) {
		t.Fatalf("transition after_refs = %v, want artifact and receipt", tr.AfterRefs)
	}
	final, _, err := store.Show(w.ID)
	if err != nil {
		t.Fatalf("show final work: %v", err)
	}
	if final.State != "completed" || final.IDs.TransitionID != tr.ID {
		t.Fatalf("final work state/transition = %q/%q, want completed/%q", final.State, final.IDs.TransitionID, tr.ID)
	}
}

// The headline P5 proof: the SAME held action is enforced differently by harness
// capability — claude_code blocks; codex fails open (must be enforced via a wrapper).
func TestPreToolUseCapabilityAware(t *testing.T) {
	mkFx := func(h string) Fixture {
		ai := &envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent, ID: "act_h", CreatedAt: fixedNow.Format(time.RFC3339),
				Producer: envelope.Producer{Kind: "hook", Name: "compound"}, IDs: envelope.IDs{WorkID: "work_demo"},
				Authority: envelope.Authority{SourceClass: "runtime"}, Privacy: "private", Risk: "tier2",
				EvidenceRefs: []string{}, Uncertainty: envelope.Uncertainty{Level: "known"},
			},
			Capability: "filesystem.delete", Action: "delete_tree",
			Target:        envelope.ActionTarget{Kind: "path", Path: "build/cache"},
			MutationClass: "destructive", Reversibility: "hard",
		}
		return Fixture{Hook: "pre-tool-use", Harness: h, WorkID: "work_demo", Action: ai}
	}

	claude, err := Run(mkFx("claude_code"), baseDeps(), fixedNow)
	if err != nil {
		t.Fatalf("claude run: %v", err)
	}
	if claude.Event.Status != "blocked" {
		t.Errorf("claude_code should BLOCK a held destructive action, got status %q", claude.Event.Status)
	}

	codex, err := Run(mkFx("codex"), baseDeps(), fixedNow)
	if err != nil {
		t.Fatalf("codex run: %v", err)
	}
	if codex.Event.Status != "fail_open" {
		t.Errorf("codex cannot block natively → should be fail_open, got %q", codex.Event.Status)
	}
	// Both compute the same decision (hold); only the ENFORCEMENT differs.
	for _, res := range []Result{claude, codex} {
		pd := res.Produced[1].(*envelope.PolicyDecision)
		if pd.Outcome != "hold" {
			t.Errorf("destructive action should evaluate to hold, got %q", pd.Outcome)
		}
	}
}

func TestPreToolUseImportsSourceControlAndHoldsDirtyMutation(t *testing.T) {
	homePath := t.TempDir()
	repo := initHookGitRepo(t)
	target := filepath.Join(repo, "README.md")
	if err := os.WriteFile(target, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	deps.Store = store
	ai := &envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            "act_dirty_write",
			CreatedAt:     fixedNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{WorkID: "work_dirty"},
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
	}
	res, err := Run(Fixture{Hook: "pre-tool-use", Harness: "claude_code", WorkID: "work_dirty", Action: ai}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Event.Status != "blocked" {
		t.Fatalf("event status = %q, want blocked", res.Event.Status)
	}
	if !containsString(res.Event.SideEffects, filepath.Join("ledger", "claims.jsonl")) {
		t.Fatalf("side effects = %v, want claims ledger", res.Event.SideEffects)
	}
	if !containsString(res.Event.SideEffects, filepath.Join("events", "act_dirty_write.json")) {
		t.Fatalf("side effects = %v, want action intent artifact", res.Event.SideEffects)
	}
	pd := res.Produced[1].(*envelope.PolicyDecision)
	if pd.Outcome != "hold" {
		t.Fatalf("policy outcome = %q reasons=%v", pd.Outcome, pd.Reasons)
	}
	if !containsString(res.Event.SideEffects, filepath.Join("events", pd.ID+".json")) {
		t.Fatalf("side effects = %v, want policy decision artifact %s", res.Event.SideEffects, pd.ID)
	}
	if len(pd.EvidenceRefs) == 0 || !strings.HasPrefix(pd.EvidenceRefs[0], "source-control:repo:") {
		t.Fatalf("policy evidence refs = %v", pd.EvidenceRefs)
	}
	if !reasonHas(pd.Reasons, "source_control_status dirty") {
		t.Fatalf("policy reasons = %v", pd.Reasons)
	}
	memStore, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := memStore.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if !hasClaim(claims, "source_control_status", "dirty") {
		t.Fatalf("source control claims = %+v, want dirty status", claims)
	}
	works, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	review := findWorkByPredicate(works, "review held policy gate")
	if review.ID == "" {
		t.Fatalf("held review work missing: %+v", works)
	}
	if review.State != "held" {
		t.Fatalf("review work state = %q, want held", review.State)
	}
	if !containsString(review.EvidenceRefs, pd.ID) || !containsString(review.EvidenceRefs, pd.EvidenceRefs[0]) {
		t.Fatalf("review work evidence refs = %v, want policy and source-control refs", review.EvidenceRefs)
	}
	for _, ref := range []string{filepath.Join("events", "act_dirty_write.json"), filepath.Join("events", pd.ID+".json")} {
		if _, err := os.Stat(filepath.Join(homePath, ref)); err != nil {
			t.Fatalf("missing persisted gate artifact %s: %v", ref, err)
		}
	}
}

func TestPreToolUseResolvesRelativeTargetAgainstHookCwd(t *testing.T) {
	homePath := t.TempDir()
	repo := initHookGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	deps := baseDeps()
	deps.HomePath = homePath
	deps.Store = store
	ai := &envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent, ID: "act_dirty_relative", CreatedAt: fixedNow.Format(time.RFC3339),
			Producer: envelope.Producer{Kind: "hook", Name: "compound"}, IDs: envelope.IDs{WorkID: "work_dirty_relative"},
			Authority: envelope.Authority{SourceClass: "runtime"}, Privacy: "private", Risk: "tier1",
			EvidenceRefs: []string{}, Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		Actor: "claude_code", Capability: "filesystem.write", Action: "write_file",
		Target:        envelope.ActionTarget{Kind: "file", Path: "README.md"},
		MutationClass: "local_reversible", Reversibility: "easy", BlastRadius: "repo",
	}
	res, err := Run(Fixture{
		Hook: "pre-tool-use", Harness: "claude_code", WorkID: "work_dirty_relative",
		Cwd: repo, Action: ai,
	}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	gotAction := res.Produced[0].(*envelope.ActionIntent)
	wantTargetPath := filepath.Join(repo, "README.md")
	if gotAction.Target.Path != wantTargetPath {
		t.Fatalf("produced action target path = %q, want resolved %q", gotAction.Target.Path, wantTargetPath)
	}
	actionBytes, err := os.ReadFile(filepath.Join(homePath, "events", "act_dirty_relative.json"))
	if err != nil {
		t.Fatalf("read persisted action: %v", err)
	}
	var persistedAction envelope.ActionIntent
	if err := json.Unmarshal(actionBytes, &persistedAction); err != nil {
		t.Fatalf("parse persisted action: %v", err)
	}
	if persistedAction.Target.Path != wantTargetPath {
		t.Fatalf("persisted action target path = %q, want resolved %q", persistedAction.Target.Path, wantTargetPath)
	}
	pd := res.Produced[1].(*envelope.PolicyDecision)
	if res.Event.Status != "blocked" || pd.Outcome != "hold" {
		t.Fatalf("event/policy = %q/%q reasons=%v, want blocked/hold", res.Event.Status, pd.Outcome, pd.Reasons)
	}
	if !reasonHas(pd.Reasons, "source_control_status dirty") {
		t.Fatalf("policy reasons = %v, want dirty source-control hold", pd.Reasons)
	}
	if len(pd.EvidenceRefs) == 0 || !strings.HasPrefix(pd.EvidenceRefs[0], "source-control:repo:") {
		t.Fatalf("policy evidence refs = %v, want source-control evidence", pd.EvidenceRefs)
	}
}

func TestSessionEndClosesWork(t *testing.T) {
	store, err := work.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := store.Create(work.CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	store.Claim(w.ID, "claude_code", fixedNow)
	store.Advance(w.ID, work.AdvanceInput{To: "running"}, fixedNow)
	store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, fixedNow)

	deps := baseDeps()
	deps.Store = store
	fx := Fixture{
		Hook:           "session-end",
		Harness:        "claude_code",
		WorkID:         w.ID,
		MoveType:       "outward_delta",
		Summary:        "shipped",
		Missed:         []string{"capability://skill:pricing"},
		FalseTriggered: []string{"capability://skill:generic-copy"},
	}
	res, err := Run(fx, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 2 || got[0] != "receipt" || got[1] != "transition" {
		t.Fatalf("produced = %v, want [receipt transition]", got)
	}
	rec := res.Produced[0].(*envelope.Receipt)
	tr := res.Produced[1].(*envelope.Transition)
	if !containsString(rec.Skills.Missed, "capability://skill:pricing") {
		t.Fatalf("receipt missed skills = %v", rec.Skills.Missed)
	}
	if !containsString(rec.Skills.FalseTriggered, "capability://skill:generic-copy") {
		t.Fatalf("receipt false_triggered skills = %v", rec.Skills.FalseTriggered)
	}
	if tr.IDs.WorkID != w.ID {
		t.Errorf("transition work_id = %q, want %q", tr.IDs.WorkID, w.ID)
	}
	closed, _ := store.Get(w.ID)
	if closed.State != "completed" {
		t.Errorf("work should be completed after session-end, got %q", closed.State)
	}
}

func TestUnknownHookRejected(t *testing.T) {
	if _, err := Run(Fixture{Hook: "frobnicate", Harness: "claude_code"}, baseDeps(), fixedNow); err == nil {
		t.Error("unknown hook should be rejected")
	}
}

func initHookGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runHookGit(t, repo, "init", "-b", "main")
	runHookGit(t, repo, "config", "user.email", "test@example.invalid")
	runHookGit(t, repo, "config", "user.name", "Compound Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHookGit(t, repo, "add", "README.md")
	runHookGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runHookGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func reasonHas(xs []string, want string) bool {
	for _, x := range xs {
		if strings.Contains(x, want) {
			return true
		}
	}
	return false
}

func hasClaim(claims []mem.Claim, predicate, object string) bool {
	for _, c := range claims {
		if c.Predicate == predicate && c.Object == object {
			return true
		}
	}
	return false
}

func findWorkByPredicate(works []envelope.WorkEnvelope, needle string) envelope.WorkEnvelope {
	for _, w := range works {
		if strings.Contains(w.SuccessPredicate, needle) {
			return w
		}
	}
	return envelope.WorkEnvelope{}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func findContextHookReadiness(readiness []envelope.HookReadiness, harness, hookName string) *envelope.HookReadiness {
	for i := range readiness {
		if readiness[i].Harness == harness && readiness[i].Hook == hookName {
			return &readiness[i]
		}
	}
	return nil
}
