package hook

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"compound/internal/envelope"
)

type injectionCap struct {
	name string
	cap  int
}

// budgetedInjectionSectionCaps mirrors, in declaration order, every cap passed to
// writeBudgetedInjectionSection — per surface. A new budgeted section must be added
// here in the same change that introduces it; that coupling is the point, because
// the cap sum is only an enforceable budget if the list is complete.
var budgetedInjectionSectionCaps = map[injectionSurface][]injectionCap{
	injectionSurfaceTask: {
		{"header", maxInjectionHeaderChars},
		{"orientation note", maxInjectionOrientationNoteChars},
		{"active memory", maxInjectionActiveMemoryChars},
		{"negatives", maxInjectionNegativeChars},
		{"active decisions", maxInjectionActiveDecisionChars},
		{"required checks", maxInjectionRequiredChecksChars},
		{"forbidden recall scopes", maxInjectionAvoidChars},
		{"capability offers", maxInjectionCapabilityOfferChars},
	},
	injectionSurfaceOrientation: {
		{"header", maxInjectionHeaderChars},
		{"summary", maxOrientationSummaryChars},
		{"active instructions", maxOrientationActiveInstructionChars},
		{"active hooks", maxOrientationActiveHookChars},
		{"included", maxOrientationIncludedChars},
		{"excluded", maxOrientationExcludedChars},
		{"hook readiness + roster", maxOrientationHookReadinessChars},
	},
}

// TestInjectionSectionCapsFitBudget pins the invariant a section cap exists to
// provide: the caps must sum within MaxContextChars.
//
// This is a regression test for a real landed defect, not a theoretical one. When
// the negatives section took a 900-rune cap it was documented as "funded by" the
// 137 runes the Active Decisions demotion freed. The arithmetic was never checked,
// the sum reached 6320 against a 6000 budget, and the caps silently stopped
// bounding anything: capInjectionChars then decided what survived by truncating
// whatever rendered last. Caps that oversubscribe their budget do not degrade
// gracefully — they relocate the loss to the tail, out of sight of every test that
// only asserts a section is present.
func TestInjectionSectionCapsFitBudget(t *testing.T) {
	for _, surface := range []injectionSurface{injectionSurfaceTask, injectionSurfaceOrientation} {
		t.Run(string(surface), func(t *testing.T) {
			sections := budgetedInjectionSectionCaps[surface]
			if len(sections) == 0 {
				t.Fatalf("surface %q has no caps listed; the budget cannot be enforced for a surface this test does not know about", surface)
			}
			sum := 0
			for _, section := range sections {
				if section.cap <= 0 {
					t.Errorf("section %q has non-positive cap %d", section.name, section.cap)
				}
				sum += section.cap
			}

			if sum > MaxContextChars {
				t.Fatalf("%s surface caps oversubscribe the injection budget: sum=%d > MaxContextChars=%d (over by %d).\n"+
					"A cap sum above the budget does not truncate the section that grew — it amputates whatever renders last.\n"+
					"Fund a raise by lowering another cap on the SAME surface, do not raise the sum.",
					surface, sum, MaxContextChars, sum-MaxContextChars)
			}
			t.Logf("%s surface: caps sum=%d of MaxContextChars=%d, headroom=%d runes",
				surface, sum, MaxContextChars, MaxContextChars-sum)
		})
	}
}

// TestInjectionSurfacesPartitionTheSectionsWithoutOverlap pins the property that
// makes the split worth doing: each section is served by exactly ONE surface.
//
// A section on both surfaces is the duplication the split removed, paid for twice
// per turn. A section on neither is worse — it silently stops reaching agents
// while every cap and every per-surface test stays green, which is precisely how
// the orientation tail went undelivered for 2714 digests.
func TestInjectionSurfacesPartitionTheSectionsWithoutOverlap(t *testing.T) {
	full := envelope.ContextPack{
		SummaryMD:          "summary body",
		ActiveInstructions: []string{"AGENTS.md"},
		ActiveHooks:        []string{"hook://claude_code/PreToolUse"},
		ActiveDecisions:    []string{"decision://DEC-0001"},
		RequiredChecks:     []string{"pre_write_gate claude_code/PreToolUse status=missing_gate: run X before mutation"},
		Avoid:              []string{"self-model"},
		ActiveMemory:       []string{"memory://one"},
		Negatives: []envelope.NegativeCard{
			{Source: "failure_pattern", ApproachKey: "approach-one", Ref: "candidate://failure_pattern/cand_one"},
		},
		CapabilityOffers: []envelope.CapabilityOffer{
			{Ref: "capability://skill:example", Kind: "skill", Name: "example", Score: 1},
		},
		Included: []envelope.IncludedRef{
			{Ref: "AGENTS.md", SourceClass: "source", Status: "current", Confidence: 1, Reason: "ambient"},
		},
		Excluded: []envelope.ExcludedRef{{Ref: "control-plane://orientation-map", Reason: "out of scope"}},
		HookReadiness: []envelope.HookReadiness{
			{Harness: "claude_code", Hook: "PreToolUse", Status: "missing_gate", Installed: false},
		},
		HookRoster: []envelope.HookRosterEntry{
			{Ref: "hook://claude_code/PreToolUse", Harness: "claude_code", Hook: "PreToolUse", Event: "PreToolUse", Status: "installed"},
		},
	}
	full.Header.ID = "ctx_partition"

	task := renderInjection(injectionRender{HookID: "hookev_task", Pack: full, Surface: injectionSurfaceTask, OrientationNote: "- Orientation served this session at `hookev_x`"})
	orientation := renderInjection(injectionRender{HookID: "hookev_orientation", Pack: full, Surface: injectionSurfaceOrientation})

	// heading -> the surface that must carry it.
	for heading, want := range map[string]injectionSurface{
		"## Active Memory":            injectionSurfaceTask,
		"## Already Tried And Failed": injectionSurfaceTask,
		"## Active Decisions":         injectionSurfaceTask,
		"## Required Checks":          injectionSurfaceTask,
		"## Forbidden Recall Scopes":  injectionSurfaceTask,
		"## Capability Offers":        injectionSurfaceTask,
		"## Summary":                  injectionSurfaceOrientation,
		"## Active Instructions":      injectionSurfaceOrientation,
		"## Active Hooks":             injectionSurfaceOrientation,
		"## Included":                 injectionSurfaceOrientation,
		"## Excluded":                 injectionSurfaceOrientation,
		"## Hook Gate Readiness":      injectionSurfaceOrientation,
		"## Hook Roster":              injectionSurfaceOrientation,
	} {
		inTask := strings.Contains(task, heading)
		inOrientation := strings.Contains(orientation, heading)
		switch {
		case inTask && inOrientation:
			t.Errorf("%q renders on BOTH surfaces; the split exists to stop paying for it twice per turn", heading)
		case !inTask && !inOrientation:
			t.Errorf("%q renders on NEITHER surface; it has silently stopped reaching agents", heading)
		case want == injectionSurfaceTask && !inTask:
			t.Errorf("%q must render on the task surface (it is computed per prompt), got orientation", heading)
		case want == injectionSurfaceOrientation && !inOrientation:
			t.Errorf("%q must render on the orientation surface (it does not vary with the prompt), got task", heading)
		}
	}

	// Neither surface may amputate a realistic pack — the whole point of splitting.
	for name, text := range map[string]string{"task": task, "orientation": orientation} {
		if strings.Contains(text, "Compound context truncated") {
			t.Errorf("%s surface truncated a realistic pack:\n%s", name, text)
		}
		if got := utf8.RuneCountInString(text); got > MaxContextChars {
			t.Errorf("%s surface exceeded MaxContextChars: %d > %d", name, got, MaxContextChars)
		}
	}
}

// TestRenderInjectionOmitsEmptyForbiddenRecallScopes pins that an unset recall-scope
// restriction renders nothing at all.
//
// The channel carries mem.Forbidden, which is empty for control-plane repos, so
// "## Forbidden Recall Scopes / - none" was the rendered result on essentially every
// prompt. That is a false all-clear in a section whose heading promises a checked
// constraint: a reader cannot distinguish "no scope is forbidden" from "nothing
// produces this channel". Silence is the honest rendering of an absent constraint.
func TestRenderInjectionOmitsEmptyForbiddenRecallScopes(t *testing.T) {
	pack := envelope.ContextPack{SummaryMD: "summary"}
	pack.Header.ID = "ctx_test"

	text := renderInjection(injectionRender{HookID: "hookev_test", Pack: pack, Surface: injectionSurfaceTask})

	if strings.Contains(text, "Forbidden Recall Scopes") {
		t.Fatalf("empty recall-scope restriction still rendered a section:\n%s", text)
	}
	if strings.Contains(text, "## Avoid") {
		t.Fatalf("empty recall-scope restriction rendered the superseded label:\n%s", text)
	}

	// Non-empty must still render: suppression is about absence, not about the
	// channel being unimportant.
	pack.Avoid = []string{"self-model"}
	text = renderInjection(injectionRender{HookID: "hookev_test", Pack: pack, Surface: injectionSurfaceTask})
	if !strings.Contains(text, "## Forbidden Recall Scopes") || !strings.Contains(text, "self-model") {
		t.Fatalf("configured recall-scope restriction did not render:\n%s", text)
	}
}

// TestRenderInjectionSeparatesSectionHeadings pins the blank line before every
// rendered section heading.
//
// Three writers (capability offers, summary, included) emitted their heading with
// no leading newline and relied on a trailing newline from whichever section
// happened to precede them. Once an upstream section was suppressed or truncated
// mid-line, the heading fused to the previous line — observed live as
// "- none## Capability Offers" — and a fused ATX heading stops being a heading in
// every markdown reader that consumes the digest.
func TestRenderInjectionSeparatesSectionHeadings(t *testing.T) {
	pack := envelope.ContextPack{
		SummaryMD: "summary",
		Avoid:     []string{"self-model"},
		CapabilityOffers: []envelope.CapabilityOffer{
			{Ref: "capability://skill:example", Kind: "skill", Name: "example", Score: 1},
		},
		ActiveInstructions: []string{"AGENTS.md"},
		ActiveHooks:        []string{"hook://claude_code/PreToolUse"},
		Included: []envelope.IncludedRef{
			{Ref: "AGENTS.md", SourceClass: "source", Status: "current", Confidence: 1, Reason: "ambient"},
		},
	}
	pack.Header.ID = "ctx_test"

	text := renderInjection(injectionRender{HookID: "hookev_test", Pack: pack, Surface: injectionSurfaceTask})

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			continue
		}
		if idx := strings.Index(line, "## "); idx > 0 {
			t.Errorf("section heading fused to preceding content, so it no longer parses as a heading: %q", line)
		}
	}
}

// TestOrientationNoteHasThreeStatesNotTwo pins the pointer's honesty.
//
// The pointer exists so a session that never received its orientation surface can
// tell. The failure mode a two-state pointer walks into is the mirror of the one it
// fixes: with no home or no session id there is no marker to look for, so a
// served/not-served pointer would report "NOT served" from missing plumbing and
// manufacture an alarm. A false "orientation missing" trains the reader to ignore
// the line, which costs exactly what the silent gap cost.
func TestOrientationNoteHasThreeStatesNotTwo(t *testing.T) {
	home := t.TempDir()

	t.Run("served names the hook that served it", func(t *testing.T) {
		if err := recordSessionOrientationServed(home, "session-served", "hookev_orient_1", fixedNow); err != nil {
			t.Fatal(err)
		}
		note := orientationNoteForSession(home, "session-served", "claude_code")
		if !strings.Contains(note, "Orientation served this session") || !strings.Contains(note, "hookev_orient_1") {
			t.Fatalf("served note must cite the serving hook: %q", note)
		}
		if !strings.Contains(note, "--harness claude_code") {
			t.Fatalf("note must carry a runnable re-serve command for THIS harness: %q", note)
		}
	})

	t.Run("provably not served says so", func(t *testing.T) {
		note := orientationNoteForSession(home, "session-never-started", "claude_code")
		if !strings.Contains(note, "Orientation NOT served this session") {
			t.Fatalf("a session with no marker must report the gap: %q", note)
		}
	})

	t.Run("unprovable claims neither", func(t *testing.T) {
		for name, note := range map[string]string{
			"no home":       orientationNoteForSession("", "session-x", "claude_code"),
			"no session id": orientationNoteForSession(home, "", "claude_code"),
		} {
			if strings.Contains(note, "NOT served") {
				t.Errorf("%s: cannot observe the session, so must not assert a missing orientation: %q", name, note)
			}
			if strings.Contains(note, "served this session at") {
				t.Errorf("%s: cannot observe the session, so must not assert it was served: %q", name, note)
			}
			if !strings.Contains(note, "compound orient") {
				t.Errorf("%s: note must still say how to get orientation: %q", name, note)
			}
		}
	})

	t.Run("session id never appears in clear on disk", func(t *testing.T) {
		const secret = "session-secret-abc123"
		if err := recordSessionOrientationServed(home, secret, "hookev_orient_2", fixedNow); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(sessionOrientationPath(home, secret))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), secret) {
			t.Fatalf("session id must be hashed, not stored in clear: %s", body)
		}
		if strings.Contains(sessionOrientationPath(home, secret), secret) {
			t.Fatalf("session id must be hashed in the path too: %s", sessionOrientationPath(home, secret))
		}
	})
}

// TestCapabilityOfferDropsWholeFieldsWorstFirst pins the field priority.
//
// The failure being prevented is specific and was observed live: the section cap
// truncated by rune count and cut INSIDE a `conflicts=` boundary list, so the offer
// rendered carrying one partial conflict when it actually had several. A severed
// constraint list does not read as a truncated offer — it reads as an offer with
// fewer conditions than it has, which is a policy surface quietly weakening itself.
// So truncation must drop WHOLE fields, worst-first, and say which.
func TestCapabilityOfferDropsWholeFieldsWorstFirst(t *testing.T) {
	long := func(prefix string, n int) []envelope.CapabilityOfferConstraint {
		out := make([]envelope.CapabilityOfferConstraint, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, envelope.CapabilityOfferConstraint{
				Kind: "boundary",
				Ref:  fmt.Sprintf("boundary://%s-%02d", prefix, i),
			})
		}
		return out
	}
	offer := envelope.CapabilityOffer{
		Ref:       "capability://procedure:example",
		Kind:      "procedure",
		Name:      "Example",
		Score:     7.8,
		Why:       "trigger×weight=2.0",
		Conflicts: long("must-not", 3),
		Required:  long("needs", 3),
		Evidence:  long("validated-by", 8),
		Harnesses: long("surfaces-with", 3),
	}

	// A budget that cannot hold everything: something must go.
	line, dropped := renderCapabilityOffer(offer, 300)

	if len(dropped) == 0 {
		t.Fatal("a budget too small for every field must report what it dropped")
	}
	// The head is identity — an offer without its ref is unactionable, so it is never
	// what gets sacrificed.
	if !strings.Contains(line, "capability://procedure:example") {
		t.Errorf("offer head must always survive: %q", line)
	}
	// why and conflicts are what a reader acts on: why it was offered, and what it
	// must not be used for.
	for _, want := range []string{"why=", "conflicts="} {
		if !strings.Contains(line, want) {
			t.Errorf("%s must outrank droppable fields, got %q (dropped %v)", want, line, dropped)
		}
	}
	// evidence and note repeat or merely justify; they go first.
	if !containsString(dropped, "evidence") {
		t.Errorf("evidence must be dropped before conflicts, dropped=%v", dropped)
	}
	if containsString(dropped, "conflicts") {
		t.Errorf("conflicts must never be dropped while lower-priority fields remain, dropped=%v", dropped)
	}

	// No field may be emitted partially: every `label=` present must be whole.
	// A half-rendered constraint list is the exact defect this replaces.
	for _, label := range []string{"conflicts=", "required=", "evidence=", "harnesses="} {
		if !strings.Contains(line, label) {
			continue
		}
		seg := line[strings.Index(line, label)+len(label):]
		if cut := strings.Index(seg, " "); cut >= 0 {
			seg = seg[:cut]
		}
		if strings.HasSuffix(seg, ",") || strings.HasSuffix(seg, "boundary:") {
			t.Errorf("field %s rendered partially (%q) — truncation must drop whole fields", label, seg)
		}
	}

	// Budget is respected including the omission marker the caller appends.
	if got := utf8.RuneCountInString(line); got > 300 {
		t.Errorf("rendered offer exceeded its per-offer budget: %d > 300", got)
	}
}

// TestCapabilityOfferSectionReportsOmittedOffers pins that offers dropped whole are
// counted. Silently rendering 3 of 9 offers reads as "there are 3 offers".
func TestCapabilityOfferSectionReportsOmittedOffers(t *testing.T) {
	offers := make([]envelope.CapabilityOffer, 0, 40)
	for i := 0; i < 40; i++ {
		offers = append(offers, envelope.CapabilityOffer{
			Ref:   fmt.Sprintf("capability://skill:example-%02d", i),
			Kind:  "skill",
			Name:  fmt.Sprintf("Example %02d", i),
			Score: 1,
			Why:   strings.Repeat("reason ", 20),
		})
	}
	var b strings.Builder
	writeCapabilityOfferList(&b, offers)
	out := b.String()

	shown := strings.Count(out, "- `capability://skill:example-")
	if shown == len(offers) {
		t.Skip("budget held every offer; nothing to report")
	}
	if !strings.Contains(out, "more offer(s) omitted from digest budget") {
		t.Fatalf("dropped offers must be counted, not silently absent (showed %d of %d):\n%s", shown, len(offers), out)
	}
}

// TestCapabilityOfferSectionNeverTruncatesMidField is the end-to-end guarantee.
//
// Built from the shape observed live: several verbose procedure offers whose
// `conflicts=` lists are long enough that the old renderer cut one in half. The
// section must stay inside its cap WITHOUT the generic section truncator ever having
// to fire, because that truncator cuts by rune count and cannot respect a field
// boundary.
func TestCapabilityOfferSectionNeverTruncatesMidField(t *testing.T) {
	boundaries := func(n int) []envelope.CapabilityOfferConstraint {
		out := make([]envelope.CapabilityOfferConstraint, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, envelope.CapabilityOfferConstraint{
				Kind:   "boundary",
				Ref:    fmt.Sprintf("boundary://landed-not-working-exercise-real-flow-observe-behavior-%02d", i),
				Detail: "landed is not working — exercise the real flow and observe behavior, not just tests",
			})
		}
		return out
	}
	offers := make([]envelope.CapabilityOffer, 0, 6)
	for i := 0; i < 6; i++ {
		offers = append(offers, envelope.CapabilityOffer{
			Ref:          fmt.Sprintf("capability://procedure:ceremony-%02d", i),
			Kind:         "procedure",
			Name:         "Compound Verify — prove a change is ALIVE (wired, fed, cut over) before calling it done",
			Score:        7.8,
			Why:          "trigger×weight=2.0 +semantic=0.64 +validated_by=5.0",
			Conflicts:    boundaries(3),
			Required:     boundaries(2),
			Evidence:     boundaries(4),
			CostRiskNote: "offer only; execution still requires WorkEnvelope, ActionIntent, and PolicyDecision",
		})
	}

	var raw strings.Builder
	writeCapabilityOfferList(&raw, offers)
	section := raw.String()

	// The renderer must fit its own cap unaided.
	if got := utf8.RuneCountInString(section); got > maxInjectionCapabilityOfferChars {
		t.Fatalf("offer section exceeded its cap unaided: %d > %d\n%s", got, maxInjectionCapabilityOfferChars, section)
	}
	// And therefore the generic truncator must have nothing to do.
	if capped := capInjectionSection(section, maxInjectionCapabilityOfferChars); strings.Contains(capped, "Section truncated") {
		t.Fatalf("offer section still needed rune-count truncation, which cuts inside fields:\n%s", capped)
	}

	// Every rendered field is whole: no line may end mid-constraint. A trailing comma
	// or a bare "boundary:" is the signature of the severed list observed live.
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "- `capability://") {
			continue
		}
		if strings.HasSuffix(line, ",") || strings.HasSuffix(line, "boundary:") || strings.HasSuffix(line, "=") {
			t.Errorf("offer line ends mid-field: %q", line)
		}
	}
	// Whatever was left out is stated, so the reader is not silently under-informed.
	shown := strings.Count(section, "- `capability://procedure:ceremony-")
	if shown < len(offers) && !strings.Contains(section, "more offer(s) omitted") {
		t.Errorf("showed %d of %d offers without saying so:\n%s", shown, len(offers), section)
	}
	t.Logf("rendered %d of %d offers in %d runes (cap %d)", shown, len(offers), utf8.RuneCountInString(section), maxInjectionCapabilityOfferChars)
}

// TestCapabilityOfferKeepsAConstraintCountWhenTheContentCannotFit pins the safety
// signal.
//
// With many offers sharing the section, no single offer can carry a long conflicts
// list whole. The first version of this renderer then dropped conflicts entirely
// while lower-priority fields survived (observed live: every offer rendered
// `harnesses=` and reported `conflicts` omitted), because the packer skipped an
// oversized field and kept filling with smaller ones. An offer whose conditions are
// invisible reads as unconditional, which is the failure mode the whole change
// exists to remove — so when the content cannot fit, the COUNT must survive.
func TestCapabilityOfferKeepsAConstraintCountWhenTheContentCannotFit(t *testing.T) {
	verbose := func(n int) []envelope.CapabilityOfferConstraint {
		out := make([]envelope.CapabilityOfferConstraint, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, envelope.CapabilityOfferConstraint{
				Kind:   "boundary",
				Ref:    fmt.Sprintf("boundary://a-very-long-boundary-reference-that-will-not-fit-%02d", i),
				Detail: "a long human-readable explanation of the boundary that pushes this field well past any per-offer budget",
			})
		}
		return out
	}
	offer := envelope.CapabilityOffer{
		Ref:       "capability://procedure:example",
		Kind:      "procedure",
		Name:      "Example",
		Score:     7.8,
		Why:       "trigger×weight=2.0",
		Conflicts: verbose(3),
		Required:  verbose(2),
		Harnesses: []envelope.CapabilityOfferConstraint{{Kind: "surfaces_with", Ref: "harness:claude_code"}},
	}

	line, dropped := renderCapabilityOffer(offer, 260)

	// The count form survives even though the content cannot.
	if !strings.Contains(line, "conflicts=3(omitted)") {
		t.Errorf("a conflicts list too long to render must still report its count, got %q (dropped %v)", line, dropped)
	}
	if !strings.Contains(line, "required=2(omitted)") {
		t.Errorf("a required list too long to render must still report its count, got %q", line)
	}
	// And "conflicts" must NOT be reported as fully dropped, because it wasn't.
	if containsString(dropped, "conflicts") {
		t.Errorf("conflicts reported as dropped while its count rendered: %q dropped=%v", line, dropped)
	}
	if got := utf8.RuneCountInString(line); got > 260 {
		t.Errorf("offer exceeded budget: %d > 260", got)
	}

	// Non-safety fields do not get a count form — they are genuinely droppable, and
	// spending budget announcing their absence would crowd out the counts that matter.
	if strings.Contains(line, "evidence=") || strings.Contains(line, "harnesses=1(omitted)") {
		t.Errorf("non-safety fields must not consume budget on count forms: %q", line)
	}
}
