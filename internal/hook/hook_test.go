package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/harness"
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
	if req.IntentTextRef != "sha256:"+hashStr("audit") {
		t.Fatalf("intent_text_ref = %q, want task hash", req.IntentTextRef)
	}
	if req.RequestedMode != "execute" || req.Constraints.AllowRawCorpus || req.Constraints.AllowPrivateMemory || req.Constraints.AllowProvisionalReviews {
		t.Fatalf("unexpected context request constraints: %+v mode=%q", req.Constraints, req.RequestedMode)
	}
	if res.Injection != nil {
		t.Fatalf("session-start without HomePath should not write runtime injection, got %+v", res.Injection)
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

	deps := baseDeps()
	deps.HomePath = homePath
	fx := Fixture{
		Hook:     "session-start",
		Harness:  "codex",
		Cwd:      "/repo/productX",
		Task:     "improve pricing",
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
	if req.TaskGuess != "improve pricing" || req.Harness != "codex" {
		t.Fatalf("unexpected request body: %+v", req)
	}
	if strings.Contains(string(reqBytes), "DO NOT LEAK RAW PROMPT BODY") {
		t.Fatalf("context request leaked raw prompt:\n%s", string(reqBytes))
	}
	b, err := os.ReadFile(res.Injection.Path)
	if err != nil {
		t.Fatalf("read injection: %v", err)
	}
	text := string(b)
	for _, want := range []string{
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
	if got := res.Event.SideEffects; len(got) != 3 || got[0] != res.Injection.ContextRequestRef || got[1] != res.Injection.ContextPackRef || got[2] != res.Injection.Ref {
		t.Fatalf("side effects = %v, want request, context pack, injection refs", got)
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
	fx := Fixture{Hook: "session-end", Harness: "claude_code", WorkID: w.ID, MoveType: "outward_delta", Summary: "shipped"}
	res, err := Run(fx, deps, fixedNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := kinds(res.Produced); len(got) != 2 || got[0] != "receipt" || got[1] != "transition" {
		t.Fatalf("produced = %v, want [receipt transition]", got)
	}
	tr := res.Produced[1].(*envelope.Transition)
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
