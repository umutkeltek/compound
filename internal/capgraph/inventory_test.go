package capgraph

import (
	"strings"
	"testing"
)

func TestHarnessFilterKeepsGenericCapabilities(t *testing.T) {
	// A generic capability really IS offered to every harness. Filtering it out
	// of a per-harness list would make this view disagree with what the executor
	// is actually handed -- and a list that disagrees with reality is worse than
	// no list, because it gets trusted.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	addNode(t, s, "tool:generic", "tool", false)
	addNode(t, s, "tool:mine", "tool", false)
	addNode(t, s, "tool:theirs", "tool", false)
	addEdge(t, s, EdSurfacesWith, "tool:mine", "harness:alpha")
	addEdge(t, s, EdSurfacesWith, "tool:theirs", "harness:beta")

	inv, err := s.CapabilityInventory()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	got := inv.Filter("alpha", "", false, false, false)
	ids := map[string]bool{}
	for _, st := range got {
		ids[st.ID] = true
	}
	if !ids["tool:generic"] {
		t.Fatal("generic capability hidden from a harness view; it is offered to that harness in reality")
	}
	if !ids["tool:mine"] {
		t.Fatal("capability scoped to this harness missing from its own view")
	}
	if ids["tool:theirs"] {
		t.Fatal("capability scoped to ANOTHER harness leaked into this view")
	}
}

func TestHarnessFilterAcceptsBareAndPrefixedNames(t *testing.T) {
	// The graph stores "harness:claude_code"; an operator types "claude_code".
	// Making them type the prefix is the kind of friction that ends with the
	// command not being used.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	addNode(t, s, "tool:mine", "tool", false)
	addEdge(t, s, EdSurfacesWith, "tool:mine", "harness:alpha")
	inv, err := s.CapabilityInventory()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(inv.Filter("alpha", "", false, false, false)) != 1 {
		t.Fatal("bare harness name did not match")
	}
	if len(inv.Filter("harness:alpha", "", false, false, false)) != 1 {
		t.Fatal("prefixed harness name did not match")
	}
}

func TestRetiredCapabilitiesAreHiddenUnlessAskedFor(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	addNode(t, s, "tool:live", "tool", false)
	addNode(t, s, "tool:dead", "tool", true)
	inv, err := s.CapabilityInventory()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if n := len(inv.Filter("", "", false, false, false)); n != 1 {
		t.Fatalf("default listing showed %d, want 1 (retired hidden)", n)
	}
	if n := len(inv.Filter("", "", false, false, true)); n != 2 {
		t.Fatalf("--include-retired showed %d, want 2", n)
	}
}

func TestPlaceholderSurfaceIsNotShownAsReachable(t *testing.T) {
	// The row view must apply the same rule as the rollup, or an operator
	// drilling into a gap finds rows that contradict the number that sent them.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	addNode(t, s, "tool:hollow", "tool", false)
	if _, err := s.AddConnectionSurface(ConnectionSurface{
		NodeID: "tool:hollow", AssertingOwner: "someone",
	}); err != nil {
		t.Fatalf("add surface: %v", err)
	}
	inv, err := s.CapabilityInventory()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if inv.State("tool:hollow").Reachable {
		t.Fatal("a surface with neither endpoint nor config path reported reachable in the row view")
	}
}

func TestFlagsColumnStaysFixedWidth(t *testing.T) {
	// The column is scanned vertically. A ragged one defeats the only reason it
	// exists.
	states := []CapabilityState{
		{},
		{Reachable: true},
		{Reachable: true, Unlockable: true, Harnesses: []string{"harness:a"}, Missed: 2},
		{Missed: 1},
	}
	for _, st := range states {
		if got := len(st.Flags()); got != 4 {
			t.Fatalf("flags %q width %d, want 4", st.Flags(), got)
		}
	}
}

func TestUnscopedRowsSayGenericOutLoud(t *testing.T) {
	// "generic" is the finding, not an absence. Rendering nothing would let the
	// most common and most costly state read as unremarkable.
	st := CapabilityState{ID: "tool:x"}
	if !strings.Contains(st.Note(), "generic") {
		t.Fatalf("unscoped row note %q does not say generic", st.Note())
	}
	scoped := CapabilityState{ID: "tool:y", Harnesses: []string{"harness:alpha"}}
	if strings.Contains(scoped.Note(), "generic") {
		t.Fatalf("scoped row note %q wrongly says generic", scoped.Note())
	}
	if !strings.Contains(scoped.Note(), "alpha") {
		t.Fatalf("scoped row note %q does not name the harness", scoped.Note())
	}
}

func TestCorrectionCountsUseTheCapabilityEnd(t *testing.T) {
	// Same direction trap as the rollup: these edges run situation -> capability.
	// Getting it backwards yields a silent zero that reads as "nothing to learn".
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	addNode(t, s, "tool:corrected", "tool", false)
	addEdge(t, s, EdMissedWhen, "context:some-situation", "tool:corrected")
	addEdge(t, s, EdFalseTriggeredBy, "context:other-situation", "tool:corrected")
	inv, err := s.CapabilityInventory()
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	st := inv.State("tool:corrected")
	if st.Missed != 1 || st.FalseTriggered != 1 {
		t.Fatalf("state = %+v, want 1 missed / 1 false-triggered", st)
	}
	if !strings.Contains(st.Flags(), "!") {
		t.Fatalf("corrected capability not flagged: %q", st.Flags())
	}
}
