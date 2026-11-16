package tooltopology

import (
	"strings"
	"testing"
)

func sampleSection(heading string, lines int, word string) string {
	var b strings.Builder
	b.WriteString("## " + heading + "\n")
	for i := 0; i < lines; i++ {
		b.WriteString("- " + word + " " + strings.Repeat(word[:1], 90) + "\n")
	}
	return b.String()
}

// The operator's rules reach agents whole. A rule section that sits past the
// old byte cap (as his standing decisions do) is served in full, no rule is
// cut by an "N bytes omitted" line, and only sections that are background,
// not rules, are left out when the budget is short. Each one left out is
// named, so nothing is dropped silently.
func TestServeOperatorModelServesEveryRuleSectionWhole(t *testing.T) {
	constraints := sampleSection("Standing constraints for agents", 20, "constraint")
	who := sampleSection("Who", 8, "person")
	background := sampleSection("Background", 30, "history")
	decisions := sampleSection("Standing decisions", 15, "decision")
	style := sampleSection("How to write to him", 10, "style")
	body := "# Sample operator model\n\n" + constraints + "\n" + who + "\n" + background + "\n" + decisions + "\n" + style
	if len(body) <= MaxOperatorModelBytes {
		t.Fatalf("fixture is %d bytes; it must exceed the %d-byte budget to test the cut", len(body), MaxOperatorModelBytes)
	}
	home := t.TempDir()
	writeOperatorModel(t, home, body)
	got := ServeOperatorModel(home)

	for _, rule := range []string{constraints, decisions, style} {
		if !strings.Contains(got, strings.TrimRight(rule, "\n")) {
			t.Fatalf("rule section %q not served whole:\n%s", strings.SplitN(rule, "\n", 2)[0], tail(got, 400))
		}
	}
	if strings.Contains(got, "truncated:") || strings.Contains(got, "bytes omitted") {
		t.Fatalf("a byte cut fell inside the model:\n%s", tail(got, 300))
	}
	if !strings.Contains(got, strings.TrimRight(who, "\n")) {
		t.Fatalf("background section that fits the budget was dropped:\n%s", tail(got, 300))
	}
	if strings.Contains(got, "- history") {
		t.Fatalf("background section past the budget was served")
	}
	wantNotice := `(not served here, to keep the session start within ` + itoaBytes(MaxOperatorModelBytes) + ` bytes: "Background" (`
	if !strings.Contains(got, wantNotice) || !strings.HasSuffix(got, "full text in config/operator-model.md)") {
		t.Fatalf("left-out section not named in a visible notice:\n%s", tail(got, 300))
	}
	if again := ServeOperatorModel(home); again != got {
		t.Fatal("serving must be byte-deterministic")
	}
}

// When the rules alone are larger than the budget they are still served
// whole: the budget only ever leaves out background, never a rule.
func TestServeOperatorModelRulesOverBudgetStillWhole(t *testing.T) {
	rules := sampleSection("Standing constraints", 40, "constraint") + "\n" + sampleSection("Standing decisions", 40, "decision")
	if len(rules) <= MaxOperatorModelBytes {
		t.Fatalf("fixture is %d bytes; want more than the %d budget", len(rules), MaxOperatorModelBytes)
	}
	home := t.TempDir()
	writeOperatorModel(t, home, rules)
	if got, want := ServeOperatorModel(home), wantOperatorModelHeader+"\n"+strings.TrimRight(rules, "\n"); got != want {
		t.Fatalf("rules over budget not served whole: got %d bytes, want %d\n%s", len(got), len(want), tail(got, 200))
	}
}

func itoaBytes(n int) string {
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Edges: a model at exactly the budget is whole; a model with no sections is
// one block of rules and is whole however long; bytes past the read limit are
// named in the closing line.
func TestServeOperatorModelSectionEdges(t *testing.T) {
	exact := strings.Repeat("x", MaxOperatorModelBytes)
	home := t.TempDir()
	writeOperatorModel(t, home, exact+"\n\n")
	if got := ServeOperatorModel(home); got != wantOperatorModelHeader+"\n"+exact {
		t.Fatalf("model at exactly the budget not served whole (len %d)", len(got))
	}
	flat := strings.Repeat("- one standing preference line\n", 400)
	home = t.TempDir()
	writeOperatorModel(t, home, flat)
	if got := ServeOperatorModel(home); got != wantOperatorModelHeader+"\n"+strings.TrimRight(flat, "\n") {
		t.Fatalf("model without sections was cut: got %d bytes", len(got))
	}
	if got := renderOperatorModel("## Standing rule\n- keep it\n## Who\n- someone\n", 12); !strings.HasSuffix(got, `(not served here, to keep the session start within `+itoaBytes(MaxOperatorModelBytes)+` bytes: 12 bytes past the 1048576-byte read limit; full text in config/operator-model.md)`) ||
		!strings.Contains(got, "- keep it\n## Who\n- someone") {
		t.Fatalf("unread bytes not named, or sections dropped:\n%s", got)
	}
}
