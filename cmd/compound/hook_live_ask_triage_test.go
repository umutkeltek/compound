package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/decisions"
	"compound/internal/envelope"
	"compound/internal/harvest"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

type askStopResponse struct {
	EventID  string `json:"event_id"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func TestInstalledHookRuntimeSupportIncludesCodexStop(t *testing.T) {
	for _, support := range installedHookRuntimeSupport() {
		if support.Harness != "codex" || support.Hook != "Stop" {
			continue
		}
		if !support.FixturePresent || support.FixtureRef != "fixtures/hooks/stop.codex.json" || !support.LiveAdapter ||
			support.LiveCommand != "compound hook stop --live --harness codex" {
			t.Fatalf("Codex Stop runtime support incomplete: %+v", support)
		}
		return
	}
	t.Fatal("Codex Stop is live but absent from hook runtime authority")
}

func TestHookLiveCodexStopCoveredAskPersistsCanonicalResume(t *testing.T) {
	homePath := t.TempDir()
	userHome := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("HOME", userHome)
	if err := home.Init(homePath, "ask-triage-test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	memStore, err := mem.Open(homePath)
	if err != nil {
		t.Fatalf("open memory: %v", err)
	}
	ruling, err := decisions.Record(memStore, homePath, decisions.RecordInput{
		Kind:       decisions.KindRuling,
		ID:         "operator-ruling-reversible-envelope",
		Title:      "Reversible actions inside a scoped grant proceed without operator approval.",
		ReviewedBy: "operator:test",
		Scope:      "reversible work inside an existing envelope",
		Now:        cliTestNow,
	})
	if err != nil {
		t.Fatalf("record ruling: %v", err)
	}
	claimID := ruling.Import.ClaimID
	if claimID == "" {
		t.Fatalf("recorded ruling lacks claim: %+v", ruling)
	}

	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	w, err := workStore.Create(work.CreateInput{
		Owner:            "codex",
		DesiredOutcome:   "finish a scoped local implementation",
		SuccessPredicate: "the reversible implementation is verified",
		NonGoals:         []string{"external or irreversible actions"},
		State:            "ready",
		Privacy:          "private",
		Risk:             "tier1",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := workStore.Claim(w.ID, "codex", cliTestNow.Add(time.Second)); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "codex", Note: "executor started"}, cliTestNow.Add(2*time.Second)); err != nil {
		t.Fatalf("advance work: %v", err)
	}
	before, err := workStore.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeKinds := askEventKindCounts(t, homePath)

	const threadID = "019f0000-0000-7000-8000-000000000111"
	const turnID = "019f0000-0000-7000-8000-000000000222"
	writeAskCodexJob(t, threadID, turnID, w.ID)
	writeAskCodexJobNamed(t, "decoy.json", "019f0000-0000-7000-8000-000000000999", turnID, w.ID)
	transcript := writeAskCodexTranscript(t,
		"The current work is a scoped reversible edit inside the existing envelope.",
		"May I continue with this reversible edit inside the envelope?",
	)

	first := runAskStop(t, threadID, turnID, transcript, false)
	if first.Decision != "block" || !strings.Contains(first.Reason, claimID) {
		t.Fatalf("covered Stop response = %+v, want cited continuation for %s", first, claimID)
	}
	after, err := workStore.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || len(after.RunIDs) != len(before.RunIDs)+1 || after.PolicyDecisionID == before.PolicyDecisionID {
		t.Fatalf("parent work did not gain exactly one interim chain: before=%+v after=%+v", before, after)
	}
	actionID := after.RunIDs[len(after.RunIDs)-1]
	var action envelope.ActionIntent
	readJSONFile(t, filepath.Join(homePath, "events", actionID+".json"), &action)
	if action.IDs.WorkID != w.ID || action.IDs.SessionID != threadID || action.Action != "resume_lane" ||
		action.Target.Kind != "harness_turn" || !strings.Contains(action.Target.ID, threadID) || !strings.Contains(action.Target.ID, turnID) {
		t.Fatalf("resume action lost exact lane binding: %+v", action)
	}
	var decision envelope.PolicyDecision
	readJSONFile(t, filepath.Join(homePath, "events", after.PolicyDecisionID+".json"), &decision)
	if decision.IDs.WorkID != w.ID || decision.ActionIntentID != action.ID || !policy.Allowed(decision) {
		t.Fatalf("resume policy is not an allowed parent chain: %+v", decision)
	}
	var event envelope.HookEvent
	readJSONFile(t, filepath.Join(homePath, "events", first.EventID+".json"), &event)
	receiptID := askSideEffectID(event.SideEffects, "rec_")
	transitionID := askSideEffectID(event.SideEffects, "tr_")
	if event.IDs.WorkID != w.ID || event.IDs.SessionID != threadID ||
		!askSliceContainsID(event.SideEffects, action.ID) || !askSliceContainsID(event.SideEffects, decision.ID) ||
		receiptID == "" || transitionID == "" {
		t.Fatalf("Stop HookEvent lacks delivery chain: %+v", event)
	}
	var receipt envelope.Receipt
	readJSONFile(t, filepath.Join(homePath, "events", receiptID+".json"), &receipt)
	var transition envelope.Transition
	readJSONFile(t, filepath.Join(homePath, "events", transitionID+".json"), &transition)
	if receipt.IDs.WorkID != w.ID || !askSliceContainsID(receipt.ProofRefs, "claim:"+claimID) ||
		transition.IDs.WorkID != w.ID || !askSliceContainsID(transition.ActionRefs, action.ID) ||
		!askSliceContainsID(transition.AfterRefs, receipt.ID) {
		t.Fatalf("resume receipt/transition broke the cited same-work chain: receipt=%+v transition=%+v", receipt, transition)
	}
	afterKinds := askEventKindCounts(t, homePath)
	if afterKinds["receipt"] != beforeKinds["receipt"]+1 || afterKinds["transition"] != beforeKinds["transition"]+1 {
		t.Fatalf("interim resume did not create one receipted transition: before=%v after=%v", beforeKinds, afterKinds)
	}

	replay := runAskStop(t, threadID, turnID, transcript, true)
	if replay.Decision != "" || replay.Reason != "" {
		t.Fatalf("active Stop replay must not continue again: %+v", replay)
	}
	replayed, err := workStore.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.RunIDs) != len(after.RunIDs) || replayed.PolicyDecisionID != after.PolicyDecisionID {
		t.Fatalf("active Stop replay duplicated the parent chain: after=%+v replay=%+v", after, replayed)
	}
	replayKinds := askEventKindCounts(t, homePath)
	if replayKinds["receipt"] != afterKinds["receipt"] || replayKinds["transition"] != afterKinds["transition"] {
		t.Fatalf("active Stop replay created terminal proof: after=%v replay=%v", afterKinds, replayKinds)
	}
}

func TestHookLiveCodexStopNovelOrRiskAskQueuesExistingDecisionBrief(t *testing.T) {
	tests := []struct {
		name        string
		ask         string
		disposition string
	}{
		{name: "novel", ask: "Should the new partner receive a beta invite?", disposition: "novel"},
		{name: "risk floor beats matching law", ask: "May I publish this reversible envelope change externally?", disposition: "risk_floor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			homePath, store, w, _, threadID, turnID := seedAskStopRuntime(t)
			before, err := store.Get(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeKinds := askEventKindCounts(t, homePath)
			transcript := writeAskCodexTranscript(t, "The lane is waiting for a decision.", tc.ask)

			response := runAskStop(t, threadID, turnID, transcript, false)
			if response.Decision != "" || response.Reason != "" {
				t.Fatalf("%s ask pretended to auto-resume: %+v", tc.disposition, response)
			}
			after, err := store.Get(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.State != before.State || len(after.RunIDs) != len(before.RunIDs) || after.PolicyDecisionID != before.PolicyDecisionID {
				t.Fatalf("%s ask mutated parent work: before=%+v after=%+v", tc.disposition, before, after)
			}
			assertAskTriageBrief(t, homePath, tc.ask, tc.disposition)
			afterKinds := askEventKindCounts(t, homePath)
			if afterKinds["receipt"] != beforeKinds["receipt"] || afterKinds["transition"] != beforeKinds["transition"] {
				t.Fatalf("%s residue created success proof: before=%v after=%v", tc.disposition, beforeKinds, afterKinds)
			}

			replay := runAskStop(t, threadID, turnID, transcript, false)
			if replay.Decision != "" || replay.Reason != "" {
				t.Fatalf("%s replay pretended to auto-resume: %+v", tc.disposition, replay)
			}
			assertAskTriageBrief(t, homePath, tc.ask, tc.disposition)
		})
	}
}

func TestHookLiveCodexStopWithoutExactLaneBindingNeverPretendsResume(t *testing.T) {
	homePath, store, w, claimID, threadID, turnID := seedAskStopRuntime(t)
	before, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeKinds := askEventKindCounts(t, homePath)
	ask := "May I continue with this reversible edit inside the envelope?"
	transcript := writeAskCodexTranscript(t, "The current work is scoped and reversible.", ask)

	response := runAskStop(t, threadID+"-unbound", turnID, transcript, false)
	if response.Decision != "" || response.Reason != "" || strings.Contains(response.Reason, claimID) {
		t.Fatalf("unbound Stop pretended to resume: %+v", response)
	}
	after, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || len(after.RunIDs) != len(before.RunIDs) || after.PolicyDecisionID != before.PolicyDecisionID {
		t.Fatalf("unbound Stop mutated parent work: before=%+v after=%+v", before, after)
	}
	assertAskTriageBrief(t, homePath, ask, "delivery_unavailable")
	afterKinds := askEventKindCounts(t, homePath)
	if afterKinds["receipt"] != beforeKinds["receipt"] || afterKinds["transition"] != beforeKinds["transition"] {
		t.Fatalf("unbound Stop created success proof: before=%v after=%v", beforeKinds, afterKinds)
	}
}

func seedAskStopRuntime(t *testing.T) (string, *work.Store, envelope.WorkEnvelope, string, string, string) {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("HOME", t.TempDir())
	if err := home.Init(homePath, "ask-triage-test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	memStore, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	ruling, err := decisions.Record(memStore, homePath, decisions.RecordInput{
		Kind: decisions.KindRuling, ID: "operator-ruling-reversible-envelope",
		Title:      "Reversible actions inside a scoped grant proceed without operator approval.",
		ReviewedBy: "operator:test", Scope: "reversible work inside an existing envelope", Now: cliTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{
		Owner: "codex", DesiredOutcome: "finish a scoped local implementation",
		SuccessPredicate: "the reversible implementation is verified", State: "ready", Privacy: "private", Risk: "tier1",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	const threadID = "019f0000-0000-7000-8000-000000000333"
	const turnID = "019f0000-0000-7000-8000-000000000444"
	writeAskCodexJob(t, threadID, turnID, w.ID)
	return homePath, store, w, ruling.Import.ClaimID, threadID, turnID
}

func assertAskTriageBrief(t *testing.T, homePath, headline, disposition string) {
	t.Helper()
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: harvest.AskTriageReviewArea}, cliTestNow)
	if err != nil {
		t.Fatalf("ask-triage queue: %v", err)
	}
	if len(report.Items) != 1 {
		t.Fatalf("ask-triage items=%d, want exactly one: %+v", len(report.Items), report.Items)
	}
	item := report.Items[0]
	if item.Status != "review_required" || item.Band != reviewartifact.BandNeedsYou || item.Brief == nil {
		t.Fatalf("ask-triage item is not a needs-you brief: %+v", item)
	}
	brief := item.Brief
	if brief.SchemaVersion != reviewartifact.DecisionBriefSchemaVersion || brief.Headline != headline || !brief.ProposeOnly ||
		len(brief.Findings) < 2 || len(brief.Options) < 2 || brief.Options[0].ConsequenceApprove == "" ||
		brief.Recommendation.Option == "" || brief.PreparedBy == "" {
		t.Fatalf("ask-triage brief incomplete: %+v", brief)
	}
	var core harvest.AskTriageReview
	readJSONFile(t, item.JSONPath, &core)
	if core.Disposition != disposition || core.WorkID == "" && disposition != "delivery_unavailable" {
		t.Fatalf("ask-triage core disposition/binding = %+v", core)
	}
}

func runAskStop(t *testing.T, threadID, turnID, transcript string, active bool) askStopResponse {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name":  "Stop",
		"session_id":       threadID,
		"turn_id":          turnID,
		"transcript_path":  transcript,
		"stop_hook_active": active,
		"cwd":              "/workspace/example",
		"model":            "codex-test",
		"permission_mode":  "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		withStdin(t, string(payload), func() {
			code = runHookCommand([]string{"stop", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("Stop exit=%d output=%s", code, out)
	}
	var response askStopResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("parse Stop response: %v\n%s", err, out)
	}
	return response
}

func writeAskCodexTranscript(t *testing.T, userText, assistantText string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []map[string]any{
		{"timestamp": "2026-07-11T09:14:00Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": userText}}}},
		{"timestamp": "2026-07-11T09:15:00Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": assistantText}}}},
	}
	var body strings.Builder
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(data)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeAskCodexJob(t *testing.T, threadID, turnID, workID string) {
	t.Helper()
	writeAskCodexJobNamed(t, "matched.json", threadID, turnID, workID)
}

func writeAskCodexJobNamed(t *testing.T, name, threadID, turnID, workID string) {
	t.Helper()
	root, ok := work.DefaultCodexJobsStateDir()
	if !ok {
		t.Fatal("Codex jobs root unavailable")
	}
	dir := filepath.Join(root, "compound-test", "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"id":       strings.TrimSuffix(name, ".json"),
		"status":   "running",
		"threadId": threadID,
		"turnId":   turnID,
		"request":  map[string]any{"prompt": "Execute Compound envelope " + workID, "write": true},
	}
	writeCLIJSONFixture(t, filepath.Join(dir, name), payload)
}

func askEventKindCounts(t *testing.T, homePath string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	entries, err := os.ReadDir(filepath.Join(homePath, "events"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(homePath, "events", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		kind, err := envelope.ValidateBytes(data)
		if err == nil {
			counts[kind]++
		}
	}
	return counts
}

func askSliceContainsID(values []string, id string) bool {
	for _, value := range values {
		if strings.Contains(value, id) {
			return true
		}
	}
	return false
}

func askSideEffectID(values []string, prefix string) string {
	for _, value := range values {
		base := strings.TrimSuffix(filepath.Base(value), filepath.Ext(value))
		if strings.HasPrefix(base, prefix) {
			return base
		}
	}
	return ""
}
