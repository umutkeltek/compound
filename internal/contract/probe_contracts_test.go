package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
)

func TestProbeTopicClassifierUsesCaseFoldedTokenBoundaries(t *testing.T) {
	topics := []mem.Entity{
		{Slug: "topic:retrieval-core", Kind: "topic", Aliases: []string{"Memory Retrieval"}},
		{Slug: "topic:cart", Kind: "topic", Aliases: []string{"cart"}},
	}
	if got := classifyProbeTopics("MEMORY retrieval inspection", topics); len(got) != 1 || got[0] != "topic:retrieval-core" {
		t.Fatalf("matched topics = %v, want topic:retrieval-core", got)
	}
	if got := classifyProbeTopics("cartography inspection", topics); len(got) != 0 {
		t.Fatalf("substring crossed token boundary: matched topics = %v", got)
	}
}

type probeTopicStoreStub struct {
	topics []mem.Entity
	calls  int
}

func (s *probeTopicStoreStub) TopicEntities() ([]mem.Entity, error) {
	s.calls++
	return append([]mem.Entity(nil), s.topics...), nil
}

func TestBuildProbeSelectionPlanConsumesTopicOnlyStore(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.CurrentView()
	if err != nil {
		t.Fatal(err)
	}
	topics := &probeTopicStoreStub{topics: []mem.Entity{
		{Slug: "system:widget", Kind: "topic", Aliases: []string{"system"}},
		{Slug: "standing-law", Kind: "topic"},
		{Slug: "trap", Kind: "topic"},
		{Slug: "opportunity", Kind: "topic"},
	}}
	plan, err := buildProbeSelectionPlan(home, Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "system inspection",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}, topics, view)
	if err != nil {
		t.Fatal(err)
	}
	if topics.calls != 1 {
		t.Fatalf("topic reader calls = %d, want 1", topics.calls)
	}
	if plan == nil || plan.Class != "system-work" {
		t.Fatalf("probe plan = %+v, want system-work", plan)
	}
}

func TestPackWithCapabilitiesSelectsDeterministicBudgetedProbeGroups(t *testing.T) {
	home := t.TempDir()
	writeProbeContracts(t, home, `{
	  "schema_version": "compound.probe_contracts.v0.1",
	  "default_class": "general",
	  "task_classes": [{
	    "class": "system-work",
	    "match": {"topics_any": ["system:widget"]},
	    "probes": [
	      {"group": "standing-law", "budget": 1},
	      {"group": "trap", "budget": 2},
	      {"group": "system:widget", "budget": 1},
	      {"group": "opportunity", "budget": 1}
	    ]
	  }]
	}`)
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	importProbeTopics(t, store)
	candidatesIn := []mem.Candidate{
		probeCandidate("RuleAlpha", "standing_rule_alpha", "first fictional standing rule", "standing-law", "architecture", "contextual"),
		probeCandidate("RuleBeta", "standing_rule_beta", "newest fictional standing rule", "standing-law", "architecture", "contextual"),
		probeCandidate("topic:retrieval-core", "authority_status", "fictional breadth owner", "topic:retrieval-core", "architecture", "contextual"),
		probeCandidate("WidgetAuxiliary", "retrieval_hint", "retrieval fallback", "topic:retrieval-core", "architecture", "contextual"),
		probeCandidate("WidgetDecision", "selection_decision", "fictional decision stays on decision refs", "trap", "decision", "contextual"),
		probeCandidate("repo:widget", "head", "fictional mutable head", "trap", "status", "operational"),
		probeCandidate("operator", "preference", "fictional personal preference", "trap", "preference", "private"),
	}
	promoteProbeCandidates(t, store, candidatesIn)

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "MEMORY retrieval inspection",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.ActivePositions) != 2 {
		t.Fatalf("active_positions = %+v, want one standing-law and one system position", pack.ActivePositions)
	}
	wantStances := map[string]bool{
		"newest fictional standing rule": true,
		"fictional breadth owner":        true,
	}
	for _, position := range pack.ActivePositions {
		if !wantStances[position.Stance] {
			t.Fatalf("budget or seed ranking selected unexpected position: %+v", position)
		}
		wantStances[position.Stance] = false
		if !includedReasonContains(pack, position.Ref, "probe:system-work/") {
			t.Fatalf("included reason for %s lacks probe class/group: %+v", position.Ref, pack.Included)
		}
	}
	for stance, missing := range wantStances {
		if missing {
			t.Fatalf("expected stance %q missing from %+v", stance, pack.ActivePositions)
		}
	}
	if !hasExcluded(pack, "probe://trap") || !excludedReasonContains(pack, "probe://trap", "probed-and-empty") {
		t.Fatalf("empty trap probe not recorded mechanically: %+v", pack.Excluded)
	}
	if !hasExcluded(pack, "probe://opportunity") {
		t.Fatalf("empty opportunity probe not recorded: %+v", pack.Excluded)
	}
	if !strings.Contains(pack.SummaryMD, "Probe system-work/standing-law:") ||
		!strings.Contains(pack.SummaryMD, "Probe system-work/trap: probed-and-empty.") ||
		!strings.Contains(pack.SummaryMD, "Probe system-work/system:widget:") {
		t.Fatalf("summary lacks group-labelled probe lines: %q", pack.SummaryMD)
	}
	for _, position := range pack.ActivePositions {
		if position.Topic == "trap" {
			t.Fatalf("absolute-gated trap position surfaced: %+v", position)
		}
	}
}

func TestPackWithCapabilitiesUsesBuiltInProbeContractsWhenTopicTaxonomyTriggers(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	importProbeTopics(t, store)
	promoteProbeCandidates(t, store, []mem.Candidate{
		probeCandidate("WidgetEngine", "authority_status", "fictional built-in probe stance", "topic:retrieval-core", "architecture", "contextual"),
	})
	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "retrieval inspection",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.ActivePositions) != 1 || !includedReasonContains(pack, pack.ActivePositions[0].Ref, "probe:system-work/system") {
		t.Fatalf("built-in system probe did not consume registered taxonomy: positions=%+v included=%+v", pack.ActivePositions, pack.Included)
	}
	if sourceRev(pack.SourceRevisions, probeContractsAuthorityRef) == "" {
		t.Fatalf("active built-in probe contract missing source revision: %+v", pack.SourceRevisions)
	}
}

func TestPackWithCapabilitiesPopulatedTaxonomyWithoutClassifierSeedStaysFlat(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	importProbeTopics(t, store)
	promoteProbeCandidates(t, store, []mem.Candidate{
		probeCandidate("WidgetEngine", "authority_status", "fictional unclassified stance", "topic:retrieval-core", "architecture", "contextual"),
	})
	in := Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "WidgetEngine authority check",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}
	trace := EngineDependencyTrace{Outcome: EngineOutcomeNotAttempted}
	snapshot, err := loadRuntimeSnapshot(in, DefaultPolicy(), home, &trace)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.probePlan != nil {
		t.Fatalf("built-ins activated without a classifier seed: %+v", snapshot.probePlan)
	}
	pack := projectRuntimePack(Pack(in, DefaultPolicy(), fixedNow), in, snapshot)
	if len(pack.ActivePositions) != 1 || includedReasonContains(pack, pack.ActivePositions[0].Ref, "probe:") {
		t.Fatalf("unclassified task did not preserve flat position selection: positions=%+v included=%+v", pack.ActivePositions, pack.Included)
	}
}

func TestPackWithCapabilitiesWithoutProbeTriggerMatchesFlatPackByteForByte(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProbeCandidates(t, store, []mem.Candidate{
		probeCandidate("WidgetEngine", "authority_status", "fictional flat stance", "miscellaneous", "architecture", "contextual"),
	})
	in := Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "widget authority",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}
	got, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	trace := EngineDependencyTrace{Outcome: EngineOutcomeNotAttempted}
	snapshot, err := loadRuntimeSnapshot(in, DefaultPolicy(), home, &trace)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.probePlan != nil {
		t.Fatalf("home without topic registry unexpectedly activated probes: %+v", snapshot.probePlan)
	}
	view, err := store.CurrentView()
	if err != nil {
		t.Fatal(err)
	}
	dependencies := captureRuntimeMemoryDependencies(home, in, view)
	flat := snapshot
	flat.memoryRefs, flat.excludedMemoryRefs, err = loadMemoryRefsFromView(in, DefaultPolicy().Memory[in.RepoKind], nil, view, &dependencies)
	if err != nil {
		t.Fatal(err)
	}
	flat.positionRefs, flat.excludedPositionRefs, err = loadPositionRefsFromView(in, DefaultPolicy().Memory[in.RepoKind], nil, view, dependencies.TopicSet)
	if err != nil {
		t.Fatal(err)
	}
	want := projectRuntimePack(Pack(in, DefaultPolicy(), fixedNow), in, flat)
	want, err = appendPackOnlyRuntime(want, in, home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("no-trigger pack diverged from flat path\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestPackWithCapabilitiesRejectsInvalidExplicitProbeBudget(t *testing.T) {
	home := t.TempDir()
	writeProbeContracts(t, home, `{
	  "schema_version": "compound.probe_contracts.v0.1",
	  "default_class": "general",
	  "task_classes": [{"class": "general", "probes": [{"group": "trap", "budget": 0}]}]
	}`)
	_, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "widget",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "budget must be >0") {
		t.Fatalf("invalid explicit probe budget error = %v", err)
	}
}

func TestPackWithCapabilitiesTopicScopeUsesFoldedPositionTopic(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	topics := []byte(`{
	  "schema_version": "compound.memory_topics.v0.1",
	  "topics": [
	    {"slug": "system:widget", "narrows_to": ["topic:retrieval"]},
	    {"slug": "topic:retrieval", "part_of": ["system:widget"]}
	  ]
	}`)
	if _, err := store.ImportTopics(topics, fixedNow); err != nil {
		t.Fatal(err)
	}
	captured, err := store.Capture([]mem.Candidate{{
		Entity:          "WidgetEngine",
		Predicate:       "authority_status",
		Object:          "WidgetEngine owns the fictional retrieval index",
		Confidence:      0.91,
		Derivation:      "deterministic",
		ClaimType:       "architecture",
		Qualifiers:      map[string]string{"domain": "topic:retrieval"},
		PromotionBasis:  candidates.BasisCuratedPage,
		SourceTraceRef:  "ev_widget_retrieval",
		SnippetHash:     "sha256:widget-retrieval",
		Visibility:      "contextual",
		ObservationDate: "2026-07-01T00:00:00Z",
	}}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Adjudicate(captured[0].ID, "promote", "test", fixedNow); err != nil {
		t.Fatal(err)
	}

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: "/repo/widget", Task: "widget retrieval authority",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute", Topic: "system:widget",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.ActivePositions) != 1 || pack.ActivePositions[0].Topic != "topic:retrieval" {
		t.Fatalf("topic-scoped active_positions = %+v, want folded topic:retrieval position inside system:widget subtree", pack.ActivePositions)
	}
}

func writeProbeContracts(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe-contracts.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func importProbeTopics(t *testing.T, store *mem.Store) {
	t.Helper()
	topics := []byte(`{
	  "schema_version": "compound.memory_topics.v0.1",
	  "topics": [
	    {"slug": "system:widget", "aliases": ["system"], "narrows_to": ["topic:retrieval-core"]},
	    {"slug": "topic:retrieval-core", "aliases": ["retrieval"], "part_of": ["system:widget"]},
	    {"slug": "standing-law"},
	    {"slug": "trap"},
	    {"slug": "opportunity"}
	  ]
	}`)
	if _, err := store.ImportTopics(topics, fixedNow); err != nil {
		t.Fatal(err)
	}
}

func probeCandidate(entity, predicate, object, topic, claimType, visibility string) mem.Candidate {
	return mem.Candidate{
		Entity: entity, Predicate: predicate, Object: object,
		Confidence: 0.91, Derivation: "deterministic", ClaimType: claimType,
		Qualifiers: map[string]string{"domain": topic}, PromotionBasis: candidates.BasisCuratedPage,
		SourceTraceRef: "ev_" + predicate, SnippetHash: "sha256:" + predicate,
		Visibility: visibility, ObservationDate: "2026-07-01T00:00:00Z",
	}
}

func promoteProbeCandidates(t *testing.T, store *mem.Store, candidatesIn []mem.Candidate) {
	t.Helper()
	captured, err := store.Capture(candidatesIn, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range captured {
		if _, err := store.Adjudicate(candidate.ID, "promote", "test", fixedNow); err != nil {
			t.Fatal(err)
		}
	}
}

func includedReasonContains(pack envelope.ContextPack, ref, want string) bool {
	for _, included := range pack.Included {
		if included.Ref == ref && strings.Contains(included.Reason, want) {
			return true
		}
	}
	return false
}
