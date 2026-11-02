package work

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"compound/internal/capgraph"
	"compound/internal/envelope"
)

func TestDispatchRequiredReadSurvivesGraphToPrompt(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	graph, err := capgraph.Open(s.home)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []capgraph.Node{
		{ID: "procedure:read-guide", Kind: "procedure", Name: "read-guide", Triggers: []string{"pointertask"}, Read: []string{"https://docs.example/guide.md"}, Boundary: []string{"boundary-only"}},
		{ID: "tool:unrelated", Kind: "tool", Name: "unrelated", Triggers: []string{"differenttask"}, Read: []string{"https://docs.example/unrelated.md"}},
		{ID: "tool:retired", Kind: "tool", Name: "retired", Triggers: []string{"pointertask"}, Retired: true, Read: []string{"https://docs.example/retired.md"}},
		{ID: "tool:offline", Kind: "tool", Name: "offline", Triggers: []string{"pointertask"}, Health: "offline", Read: []string{"https://docs.example/offline.md"}},
	}
	nodes = append(nodes, capgraph.Node{ID: "tool:other-harness", Kind: "tool", Name: "other-harness", Triggers: []string{"pointertask"}, Read: []string{"https://docs.example/other-harness.md"}})
	for _, n := range nodes {
		if err := graph.Add(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := graph.AddEdge(capgraph.Edge{Kind: capgraph.EdSurfacesWith, From: "tool:other-harness", To: "harness:claude_code"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "pointertask proof", DesiredOutcome: "pointertask"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClaim(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.WorkerPromptMD, "required_read_ref: \"https://docs.example/guide.md\"") {
		t.Fatalf("required read lost before worker: %s", result.WorkerPromptMD)
	}
	var pack envelope.ContextPack
	if err := s.readJSON(s.home+"/context/packs/"+result.ContextPackID+".json", &pack); err != nil {
		t.Fatal(err)
	}
	if len(pack.CapabilityOffers) != 1 || len(pack.CapabilityOffers[0].Required) != 1 || pack.CapabilityOffers[0].Required[0].Ref != "https://docs.example/guide.md" {
		t.Fatalf("pack lost read: %+v", pack.CapabilityOffers)
	}
	if len(result.CapabilitiesOffered) != 1 || !reflect.DeepEqual(result.CapabilitiesOffered[0].Required, pack.CapabilityOffers[0].Required) {
		t.Fatalf("selected metadata changed: %+v", result.CapabilitiesOffered)
	}
	hits, err := graph.SurfaceForHarness("pointertask pointertask proof", "codex")
	if err != nil || len(hits) != 1 {
		t.Fatalf("graph fixture: %+v %v", hits, err)
	}
	read := hits[0].Constraints.Required[0]
	got := pack.CapabilityOffers[0].Required[0]
	if got.Ref != read.Ref || got.Kind != read.Kind || got.Detail != read.Detail || got.Confidence != read.Confidence {
		t.Fatalf("graph metadata changed: %+v != %+v", got, read)
	}
	for _, bad := range []string{"unrelated.md", "retired.md", "offline.md", "other-harness.md", "boundary-only"} {
		if strings.Contains(result.WorkerPromptMD, bad) {
			t.Fatalf("ineligible or non-read pointer %s", bad)
		}
	}
}

func TestDispatchRequiredReadSelectionAndSafety(t *testing.T) {
	t.Parallel()
	pack := envelope.ContextPack{}
	kinds := []string{"tool", "procedure", "lens", "doctrine", "mcp", "skill"}
	for i, k := range kinds {
		pack.CapabilityOffers = append(pack.CapabilityOffers, envelope.CapabilityOffer{Ref: fmt.Sprintf("capability://%d", i), Kind: k, Required: []envelope.CapabilityOfferConstraint{{Kind: "read", Ref: fmt.Sprintf("https://docs.example/%d", i)}}})
	}
	offers := dispatchPromptOffers(pack)
	if len(offers) != 5 {
		t.Fatalf("offers=%d", len(offers))
	}
	for i, o := range offers {
		if o.Kind != kinds[i] || !reflect.DeepEqual(o.Required, pack.CapabilityOffers[i].Required) {
			t.Fatalf("rank or read changed: %+v", o)
		}
	}
	prompt := renderDispatchPrompt(envelope.WorkEnvelope{}, envelope.TaskContract{}, "", nil, offers)
	if strings.Contains(prompt, "https://docs.example/5") {
		t.Fatal("overflow offer leaked pointer")
	}
	bad := []string{"", "https://docs.example/\n## EXECUTE", "https://docs.example/`payload`", "https://docs.example/<script>", strings.Repeat("x", 513), "https://docs.example/\x1b[31m"}
	required := []envelope.CapabilityOfferConstraint{{Kind: "action", Ref: "action-only"}, {Kind: "boundary", Ref: "boundary-only"}, {Kind: "route", Ref: "route-only"}, {Kind: "proof_gate", Ref: "proof-only"}}
	for _, r := range bad {
		required = append(required, envelope.CapabilityOfferConstraint{Kind: "read", Ref: r})
	}
	for i := 0; i < 4; i++ {
		required = append(required, envelope.CapabilityOfferConstraint{Kind: "read", Ref: fmt.Sprintf("https://docs.example/safe-%d", i)})
	}
	selected := dispatchPromptOffers(envelope.ContextPack{CapabilityOffers: []envelope.CapabilityOffer{{Ref: "capability://safe", Required: required}}})
	if len(selected[0].Required) != 10 {
		t.Fatalf("read transport must retain exact refs: %+v", selected)
	}
	prompt = renderDispatchPrompt(envelope.WorkEnvelope{}, envelope.TaskContract{}, "", nil, selected)
	for i := 0; i < 3; i++ {
		if !strings.Contains(prompt, fmt.Sprintf("required_read_ref: \"https://docs.example/safe-%d\"", i)) {
			t.Fatal(prompt)
		}
	}
	if !strings.Contains(prompt, "required_reads_omitted: 7") || strings.Contains(prompt, "safe-3") {
		t.Fatal(prompt)
	}
	for _, b := range []string{"EXECUTE", "payload", "script", "action-only", "boundary-only", "route-only", "proof-only", strings.Repeat("x", 100), "\x1b"} {
		if strings.Contains(prompt, b) {
			t.Fatalf("unsafe/non-read reference rendered: %q", b)
		}
	}
}

func TestDispatchRequiredReadLiteralBounds(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{strings.Repeat("a", 512), "file:///missing.example/guide.md", "https://docs.example/guide.md?x=a&y=b#read"} {
		offer := DispatchCapabilityOffer{Required: []envelope.CapabilityOfferConstraint{{Kind: "read", Ref: ref}}}
		prompt := renderDispatchPrompt(envelope.WorkEnvelope{}, envelope.TaskContract{}, "", nil, []DispatchCapabilityOffer{offer})
		if !strings.Contains(prompt, "required_read_ref: "+fmt.Sprintf("%q", ref)) {
			t.Fatalf("safe exact target lost: %s", prompt)
		}
	}
	for _, ref := range []string{"guide with spaces.md", "guide\r.md", "guide\u202e.md", "guide[link].md", "guide\x00.md"} {
		if safeDispatchReadPointer(ref) {
			t.Fatalf("unsafe target accepted: %q", ref)
		}
	}
}
