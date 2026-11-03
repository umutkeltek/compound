package capgraph

import (
	"strings"
	"testing"
)

// Capability offers on four review tasks, plus a planning task.
//
// The shapes mirror the live graph: imported skills carry their description sentence plus
// their id as triggers, curated tools carry hand-written words, and imported MCP tools carry
// their server name, tool name and description word for word, stamped "mcp_tool". Product
// names are neutral fixtures.
//
// Measured on the live graph 2026-11-03 before the prose rule: a browser image-listing tool
// ranked #1 (4.8) on a vision/plan reassessment task on four description words with the
// semantic lane judging it unrelated.
func reviewTaskStore(t *testing.T) *Store {
	t.Helper()
	_, s := newStore(t)
	add := func(n Node) {
		t.Helper()
		if n.Weight == 0 {
			n.Weight = 1
		}
		if err := s.Add(n); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(id, description string) {
		add(Node{ID: "skill:" + id, Kind: "skill", Name: id, Triggers: []string{description, id}})
	}
	proseTool := func(server, tool, description string) {
		add(Node{
			ID: "tool:" + server + "." + strings.ReplaceAll(tool, "_", "-"), Kind: "tool",
			Name: server + "." + tool, Weight: 1.2,
			Triggers: append(strings.Fields(strings.ReplaceAll(server, "-", " ")+" "+strings.ReplaceAll(tool, "_", " ")+" "+description), "mcp_tool"),
		})
	}

	// The right answers.
	skill("store-visuals-pipeline", "use when producing app store screenshot sets end to end: fresh native captures, chatgpt image generation with the real capture attached, caption grid, go/no-go gate")
	skill("search-ads-review", "search ads read-only investigation: pull campaigns, keywords and reports, compute cpi and spend, and decide cap, negative, pause or scale for a keyword")
	skill("social-post", "use when posting or scheduling social posts: announce a release, schedule a post, add a tracking link, find out why a post was refused")
	add(Node{ID: "tool:store-cli", Kind: "tool", Name: "store connect cli", Weight: 1.5,
		Triggers: []string{"app", "store", "connect", "cli", "metadata", "release", "status", "review", "publish"}})
	proseTool("imagegen", "chat_image", "agent friendly wrapper for browser image generation; selects the mode, waits, downloads images and returns saved paths")
	skill("writing-plans", "write or revise an implementation plan: next step, sequencing, decide what to build first")

	// Plausible wrong answers that share words with the tasks.
	skill("store-screenshots", "deterministic app store screenshot decks for many locales from a fixed template")
	add(Node{ID: "service:post-scheduler", Kind: "service", Name: "post scheduler", Triggers: []string{"deploy", "dns", "infra", "scheduler"}})
	proseTool("browser-tools", "browser_get_images", "get a list of all images on the current page with their urls and alt text; useful for finding images to analyze with the vision tool; requires navigate to be called first")
	proseTool("search-console", "keyword_report", "returns search queries, clicks and impressions for a site; check how each keyword performed yesterday")
	proseTool("analytics", "run_report", "run a report of current findings for a property; analyze and decide on the next step")
	return s
}

func topIDs(hits []Surfaced, n int) []string {
	var out []string
	for i := 0; i < len(hits) && i < n; i++ {
		out = append(out, hits[i].Node.ID)
	}
	return out
}

func TestReviewTasksSurfaceTheRightToolInTopFive(t *testing.T) {
	s := reviewTaskStore(t)
	cases := []struct {
		name, task string
		want       []string // each must be in the top 5
		notTop5    []string
	}{
		{"store visuals",
			"Make App Store screenshots for Ledgerly using ChatGPT image generation",
			// The prose-trigger tool is named by the task ("chat", "image"), so the prose
			// rule must still let it through: it guards against over-pruning.
			[]string{"skill:store-visuals-pipeline", "tool:imagegen.chat-image"}, nil},
		{"ads spend",
			"Check yesterday's search ads spend and CPI for Ledgerly and decide whether to pause a keyword",
			// The keyword-report tool's own name says "search" and "keyword", so it is a
			// fair candidate here; the rule only refuses matches on description words.
			[]string{"skill:search-ads-review"}, nil},
		{"release post",
			"Post the Ledgerly release announcement to the social scheduler",
			[]string{"skill:social-post"}, nil},
		{"review state",
			"Check whether Ledgerly 1.8.11 is waiting for review in App Store Connect",
			[]string{"tool:store-cli"}, nil},
		{"planning",
			"Reassess the current vision and plan: analyze the findings and decide the next step",
			[]string{"skill:writing-plans"},
			// Before the prose rule this tool was #1 here on "current", "vision",
			// "analyze", "finding"; the analytics tool rode "current findings ... analyze
			// ... decide ... next step" the same way.
			[]string{"tool:browser-tools.browser-get-images", "tool:analytics.run-report"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits, err := s.SurfaceForHarness(c.task, "claude_code")
			if err != nil {
				t.Fatal(err)
			}
			top := topIDs(hits, 5)
			for _, want := range c.want {
				if !containsString(top, want) {
					t.Errorf("%s not in top 5 for %q; top 5 = %v", want, c.task, top)
				}
			}
			for _, bad := range c.notTop5 {
				if containsString(top, bad) {
					t.Errorf("%s in top 5 for %q on description words alone; top 5 = %v", bad, c.task, top)
				}
			}
		})
	}
}

// A prose-only match is not a candidate unless the semantic lane agrees: the rule keys on
// the semantic verdict, not on the tool kind.
func TestProseOnlyMatchNeedsSemanticAgreement(t *testing.T) {
	s := reviewTaskStore(t)
	const task = "Reassess the current vision and plan: analyze the findings and decide the next step"
	hits, err := s.SurfaceForHarness(task, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if hasSurfaced(hits, "tool:browser-tools.browser-get-images") {
		t.Fatalf("prose-only match surfaced without semantic support: %v", topIDs(hits, 10))
	}

	nodes, err := s.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	var target Node
	for _, n := range nodes {
		if n.ID == "tool:browser-tools.browser-get-images" {
			target = n
		}
	}
	stub := newSemanticStub("prose-rule")
	stub.vectors[task] = []float32{1, 0}
	stub.vectors[nodeEmbedText(target)] = []float32{1, 0}
	hits, err = s.SurfaceForContext(SurfaceContext{Task: task, Harness: "claude_code", Embedder: stub})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSurfaced(hits, target.ID) {
		t.Fatalf("semantic lane agreed but the prose match was still refused: %v", topIDs(hits, 10))
	}
}
