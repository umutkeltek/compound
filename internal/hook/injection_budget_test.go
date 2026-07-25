package hook

import (
	"strings"
	"testing"

	"compound/internal/envelope"
)

// budgetedInjectionSectionCaps mirrors, in declaration order, every cap passed to
// writeBudgetedInjectionSection by renderInjection. A new budgeted section must be
// added here in the same change that introduces it — that coupling is the point:
// the cap sum is only an enforceable budget if the list is complete.
var budgetedInjectionSectionCaps = []struct {
	name string
	cap  int
}{
	{"header", maxInjectionHeaderChars},
	{"active memory", maxInjectionActiveMemoryChars},
	{"negatives", maxInjectionNegativeChars},
	{"active decisions", maxInjectionActiveDecisionChars},
	{"required checks", maxInjectionRequiredChecksChars},
	{"forbidden recall scopes", maxInjectionAvoidChars},
	{"capability offers", maxInjectionCapabilityOfferChars},
	{"summary", maxInjectionSummaryChars},
	{"active instructions", maxInjectionActiveInstructionChars},
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
	sum := 0
	for _, section := range budgetedInjectionSectionCaps {
		if section.cap <= 0 {
			t.Errorf("section %q has non-positive cap %d", section.name, section.cap)
		}
		sum += section.cap
	}

	if sum > MaxContextChars {
		t.Fatalf("budgeted section caps oversubscribe the injection budget: sum=%d > MaxContextChars=%d (over by %d).\n"+
			"A cap sum above the budget does not truncate the section that grew — it amputates whatever renders last.\n"+
			"Fund a raise by lowering another cap in the same change, do not raise the sum.",
			sum, MaxContextChars, sum-MaxContextChars)
	}

	// The tail after Active Instructions (Active Hooks, Included, Excluded, Hook
	// Gate Readiness, Hook Roster) is unbudgeted, so whatever the caps do not claim
	// is all the room it can ever have. Surfaced as a logged figure rather than an
	// asserted floor: the honest number today is smaller than the tail needs, and
	// the fix is to move the stable orientation tail onto SessionStart, not to
	// assert a floor here that renderInjection cannot yet honor.
	t.Logf("budgeted caps sum=%d of MaxContextChars=%d; unbudgeted tail headroom=%d runes",
		sum, MaxContextChars, MaxContextChars-sum)
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

	text := renderInjection("hookev_test", pack)

	if strings.Contains(text, "Forbidden Recall Scopes") {
		t.Fatalf("empty recall-scope restriction still rendered a section:\n%s", text)
	}
	if strings.Contains(text, "## Avoid") {
		t.Fatalf("empty recall-scope restriction rendered the superseded label:\n%s", text)
	}

	// Non-empty must still render: suppression is about absence, not about the
	// channel being unimportant.
	pack.Avoid = []string{"self-model"}
	text = renderInjection("hookev_test", pack)
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

	text := renderInjection("hookev_test", pack)

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			continue
		}
		if idx := strings.Index(line, "## "); idx > 0 {
			t.Errorf("section heading fused to preceding content, so it no longer parses as a heading: %q", line)
		}
	}
}
