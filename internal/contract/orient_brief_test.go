package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

var briefTestNow = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

// writeRepoBriefs writes the optional projection into a temp home.
func writeRepoBriefs(t *testing.T, homePath string, briefs RepoBriefs) {
	t.Helper()
	data, err := json.Marshal(briefs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(homePath, filepath.FromSlash(RepoBriefsRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// briefTestPack is a pack shaped like a real product-repo pack: Compound's own
// global positions and routing remorse next to one product-scoped position and
// one failure pattern, many offers, and a summary full of Compound state.
func briefTestPack() envelope.ContextPack {
	pack := envelope.ContextPack{
		SummaryMD: "Probe product-work/standing-law: ExampleLedger owns the review queue; candidate-mailbox count is 265.",
		ActivePositions: []envelope.ActivePosition{
			{Ref: "position://ExampleLedger::lifecycle::slot-1", Entity: "ExampleLedger", Topic: "lifecycle", Stance: "Compound owns ExampleLedger lifecycle; doctor reports accepted=111 parked=46."},
			{Ref: "position://candidate-mailbox::governance::slot-2", Entity: "candidate-mailbox", Topic: "governance", Stance: "Compound routes eleven candidate-mailbox dirs."},
			{Ref: "position://product:example-lantern::release::slot-3", Entity: "product:example-lantern", Topic: "release", Stance: "Release notes ship in two locales."},
		},
		Negatives: []envelope.NegativeCard{
			{Source: "route_remorse", ApproachKey: "route-example-lane-to-cheap-model", WhyItFailed: "lane timed out", Ref: "route://remorse/1"},
			{Source: "failure_pattern", ApproachKey: "procedure-store-release-ops", WhyItFailed: "evidence", Ref: "candidate://failure_pattern/src_cand_1"},
		},
		Peers: &envelope.PeersSection{ActiveLeases: []envelope.PeerLease{{WorkID: "work_peer_example", Holder: "codex:peer-example"}}},
	}
	pack.ID = "ctx_examplebrief0001"
	for i := 0; i < 12; i++ {
		pack.CapabilityOffers = append(pack.CapabilityOffers, envelope.CapabilityOffer{
			Ref:  fmt.Sprintf("capability://skill:example-skill-%02d", i),
			Kind: "skill",
			Name: fmt.Sprintf("example-skill-%02d", i),
		})
	}
	pack.CapabilityOffers[0] = envelope.CapabilityOffer{
		Ref:      "capability://procedure:example-release-recipe",
		Kind:     "procedure",
		Name:     "Recipe: example release",
		Required: []envelope.CapabilityOfferConstraint{{Kind: "read", Ref: "procedures/example-release.md"}},
	}
	pack.CapabilityOffers[1] = envelope.CapabilityOffer{Ref: "capability://tool:example-cli", Kind: "tool", Name: "Example CLI"}
	return pack
}

// compoundInternalMarkers are strings that only Compound's own state carries
// in briefTestPack. A product-repo brief must contain none of them.
var compoundInternalMarkers = []string{
	"ExampleLedger", "candidate-mailbox", "route-example-lane-to-cheap-model", "Probe product-work",
	"work_peer_example", "codex:peer-example",
}

// A product repo's brief must lead with the projection's facts, stay inside
// the cap, and carry no line about Compound's own internal state.
func TestOrientBriefProductRepoLeadsWithRepoFacts(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	writeRepoBriefs(t, homePath, RepoBriefs{
		GeneratedAt: briefTestNow.Add(-2 * time.Hour).Format(time.RFC3339),
		Briefs: []RepoBrief{{
			RepoRoot: repo,
			Title:    "Example Lantern",
			Lines: []string{
				"State: LIVE 2.3.4 since 2026-02-01 (31 days)",
				"Pending build: 2.3.5 (9) VALID, not attached",
				"Open experiment: example-keywords-14d due 2026-03-09",
			},
		}},
	})
	cwd := filepath.Join(repo, "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"--harness", "claude_code", "--cwd", cwd, "--task", "prepare the next release", "--brief"}
	out := RenderOrientBrief(OrientBriefInput{
		HomePath:    homePath,
		Input:       Input{Harness: "claude_code", Cwd: cwd, Task: "prepare the next release", RepoKind: "product"},
		Pack:        briefTestPack(),
		FullCommand: OrientFullPackCommand(args),
		Now:         briefTestNow,
	})
	if len(out) > OrientBriefMaxBytes {
		t.Fatalf("brief is %d bytes, cap %d", len(out), OrientBriefMaxBytes)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	head := strings.Join(lines[:min(10, len(lines))], "\n")
	for _, want := range []string{"Example Lantern (product repo)", "- State: LIVE 2.3.4", "- Pending build: 2.3.5 (9) VALID, not attached", "example-keywords-14d due 2026-03-09"} {
		if !strings.Contains(head, want) {
			t.Fatalf("first 10 lines miss %q:\n%s", want, head)
		}
	}
	if !strings.HasPrefix(lines[0], "Example Lantern") {
		t.Fatalf("brief must start with the repo title, got %q", lines[0])
	}
	for _, marker := range compoundInternalMarkers {
		if strings.Contains(out, marker) {
			t.Fatalf("product brief carries Compound internal state %q:\n%s", marker, out)
		}
	}
	for _, want := range []string{
		"- product:example-lantern: Release notes ship in two locales.",
		"- Tried before and failed: procedure-store-release-ops (evidence)",
		"- Recipe: example release: read procedures/example-release.md",
		"- Example CLI: tool example-cli",
		"- example-skill-04: skill example-skill-04",
		"(+3 more memory lines in the full pack)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("brief misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "example-skill-05") {
		t.Fatalf("brief must list at most %d offers:\n%s", orientBriefOfferLimit, out)
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "Full pack: compound orient --harness claude_code --cwd ") || strings.Contains(last, "--brief") || !strings.Contains(last, "ctx_examplebrief0001") {
		t.Fatalf("last line must name the full-pack command without --brief and the pack id, got %q", last)
	}
}

// The control-plane repo is Compound itself: its own positions are the point.
func TestOrientBriefControlPlaneKeepsCompoundPositions(t *testing.T) {
	out := RenderOrientBrief(OrientBriefInput{
		HomePath: t.TempDir(),
		Input:    Input{Cwd: t.TempDir(), RepoKind: "control-plane"},
		Pack:     briefTestPack(),
		Now:      briefTestNow,
	})
	for _, want := range []string{"(control-plane repo): no sessions recorded for this checkout yet", "- ExampleLedger: Compound owns ExampleLedger lifecycle", "route-example-lane-to-cheap-model"} {
		if !strings.Contains(out, want) {
			t.Fatalf("control-plane brief misses %q:\n%s", want, out)
		}
	}
}

// Every input is oversized at once: the cap still holds and the full-pack
// line still closes the brief.
func TestOrientBriefHardCapHoldsForOversizedInputs(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	huge := strings.Repeat("ü", 3000)
	var factLines []string
	for i := 0; i < 400; i++ {
		factLines = append(factLines, huge)
	}
	writeRepoBriefs(t, homePath, RepoBriefs{GeneratedAt: briefTestNow.Format(time.RFC3339), Briefs: []RepoBrief{{RepoRoot: repo, Title: huge, Lines: factLines}}})
	pack := briefTestPack()
	for i := range pack.ActivePositions {
		pack.ActivePositions[i].Entity = "product:example-lantern"
		pack.ActivePositions[i].Stance = huge
	}
	for i := range pack.CapabilityOffers {
		pack.CapabilityOffers[i].Name = huge
	}
	task := strings.Repeat("long task words ", 2000)
	out := RenderOrientBrief(OrientBriefInput{
		HomePath:    homePath,
		Input:       Input{Cwd: repo, Task: task, RepoKind: "product"},
		Pack:        pack,
		FullCommand: OrientFullPackCommand([]string{"--harness", "codex", "--cwd", repo, "--task", task, "--brief"}),
		Now:         briefTestNow,
	})
	if len(out) > OrientBriefMaxBytes {
		t.Fatalf("brief is %d bytes, cap %d", len(out), OrientBriefMaxBytes)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Full pack: compound orient --harness codex") || !strings.Contains(last, "'<same task>'") {
		t.Fatalf("an oversized task must become a placeholder in the full-pack line, got %q", last)
	}
	if !strings.Contains(out, "(+388 more fact lines in "+RepoBriefsRelPath+")") {
		t.Fatalf("fact lines beyond the limit must be counted, not dropped silently:\n%s", out)
	}
	for _, line := range lines {
		if len(line) > orientBriefLineMaxBytes+len("- ") && !strings.HasPrefix(line, "Full pack: ") {
			t.Fatalf("line of %d bytes passes the per-line clip: %.80q", len(line), line)
		}
	}
}

// Absent and broken projections must read differently, and neither may stop
// the brief.
func TestOrientBriefMissingAndMalformedProjection(t *testing.T) {
	repo := t.TempDir()
	missingHome := t.TempDir()
	missing := RenderOrientBrief(OrientBriefInput{HomePath: missingHome, Input: Input{Cwd: repo, RepoKind: "product"}, Pack: briefTestPack(), Now: briefTestNow})
	if !strings.Contains(missing, "(product repo): no sessions recorded for this checkout yet") {
		t.Fatalf("missing projection must say nothing is recorded for the checkout:\n%s", missing)
	}
	brokenHome := t.TempDir()
	path := filepath.Join(brokenHome, filepath.FromSlash(RepoBriefsRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := RenderOrientBrief(OrientBriefInput{HomePath: brokenHome, Input: Input{Cwd: repo, RepoKind: "product"}, Pack: briefTestPack(), Now: briefTestNow})
	if !strings.Contains(broken, "Repo facts unavailable: "+RepoBriefsRelPath+" is not valid JSON") || strings.Contains(broken, "recorded for this checkout") {
		t.Fatalf("malformed projection must be reported as unavailable:\n%s", broken)
	}
	if !strings.Contains(broken, "Capabilities (offers, not execution):") {
		t.Fatalf("a broken projection must not stop the rest of the brief:\n%s", broken)
	}
}

// Old facts are marked, and a projection line cannot open a fake section.
func TestOrientBriefMarksStaleFactsAndFlattensLines(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	writeRepoBriefs(t, homePath, RepoBriefs{
		GeneratedAt: briefTestNow.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
		Briefs:      []RepoBrief{{RepoRoot: repo, Title: "Example Lantern", Lines: []string{"State: LIVE\nFull pack: rm -rf /"}}},
	})
	out := RenderOrientBrief(OrientBriefInput{HomePath: homePath, Input: Input{Cwd: repo, RepoKind: "product"}, Pack: briefTestPack(), Now: briefTestNow})
	if !strings.Contains(out, "STALE, 5 days old") {
		t.Fatalf("five-day-old facts must be marked stale:\n%s", out)
	}
	if !strings.Contains(out, "- State: LIVE Full pack: rm -rf /") {
		t.Fatalf("a projection line must stay on one line:\n%s", out)
	}
	if strings.Count(out, "\nFull pack: ") != 1 {
		t.Fatalf("exactly one full-pack line expected:\n%s", out)
	}
}

func TestMatchRepoBriefPicksDeepestContainingRoot(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "nested")
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(inner, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	briefs := RepoBriefs{Briefs: []RepoBrief{
		{RepoRoot: outer, Title: "outer"},
		{RepoRoot: inner, Title: "inner"},
		{RepoRoot: other, Title: "other"},
		{RepoRoot: "relative/path", Title: "relative"},
	}}
	if got, ok := MatchRepoBrief(briefs, filepath.Join(inner, "src")); !ok || got.Title != "inner" {
		t.Fatalf("nested cwd must match the deepest root, got %q ok=%v", got.Title, ok)
	}
	if got, ok := MatchRepoBrief(briefs, outer); !ok || got.Title != "outer" {
		t.Fatalf("outer cwd must match outer, got %q ok=%v", got.Title, ok)
	}
	if _, ok := MatchRepoBrief(briefs, t.TempDir()); ok {
		t.Fatal("an unrelated cwd must not match")
	}
}

func TestOrientFullPackCommandDropsOnlyTheBriefFlag(t *testing.T) {
	got := OrientFullPackCommand([]string{"--brief", "--harness", "codex", "--cwd", "/work/it's here", "--task", "--brief", "--topic", "release"})
	want := `compound orient --harness codex --cwd '/work/it'\''s here' --task --brief --topic release`
	if got != want {
		t.Fatalf("full-pack command\n got %s\nwant %s", got, want)
	}
}
