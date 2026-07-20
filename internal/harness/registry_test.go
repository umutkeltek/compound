package harness

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"compound/internal/runattempt"
)

// TestAdapterRegistryNewHasBuiltins pins that the built-in adapters
// are pre-registered. The list is the public surface of the registry;
// the CLI's `compound harness list` and the architecture doctor both
// depend on it.
func TestAdapterRegistryNewHasBuiltins(t *testing.T) {
	r := NewAdapterRegistry(runattempt.Open(t.TempDir()))
	got := r.Names()
	sort.Strings(got)
	want := []string{"claude", "codex", "os-harness-entrypoints"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NewAdapterRegistry names = %v, want %v", got, want)
	}
}

// TestAdapterRegistryAdapterByName: every built-in is reachable by Name.
func TestAdapterRegistryAdapterByName(t *testing.T) {
	r := NewAdapterRegistry(runattempt.Open(t.TempDir()))
	for _, name := range []string{"os-harness-entrypoints", "claude", "codex"} {
		a, ok := r.AdapterByName(name)
		if !ok {
			t.Fatalf("AdapterByName(%q) returned !ok", name)
		}
		if a.Name() != name {
			t.Fatalf("adapter Name() = %q, want %q", a.Name(), name)
		}
	}
	// Lookup miss returns false + nil adapter.
	if a, ok := r.AdapterByName("nonexistent"); ok || a != nil {
		t.Fatalf("AdapterByName(nonexistent) = (%v, %v), want (nil, false)", a, ok)
	}
}

// TestAdapterRegistryRegisterOverride: a custom adapter can override a
// built-in. Pinned so future slices that wrap built-ins (e.g. a
// "claude-with-logging" wrapper) have a clear extension point.
func TestAdapterRegistryRegisterOverride(t *testing.T) {
	r := NewAdapterRegistry(runattempt.Open(t.TempDir()))
	stub := stubAdapter{name: "claude"}
	if err := r.RegisterAdapter(stub); err != nil {
		t.Fatalf("RegisterAdapter: %v", err)
	}
	got, ok := r.AdapterByName("claude")
	if !ok {
		t.Fatalf("claude adapter missing after override")
	}
	if got.Name() != "claude" {
		t.Fatalf("claude Name() = %q, want claude", got.Name())
	}
	if _, ok := got.(stubAdapter); !ok {
		t.Fatalf("override did not take effect: got %T, want stubAdapter", got)
	}
}

// TestAdapterRegistryRegisterRejectsNil: defensive contract -- the
// registry refuses to register a nil or nameless adapter.
func TestAdapterRegistryRegisterRejectsNil(t *testing.T) {
	r := NewAdapterRegistry(runattempt.Open(t.TempDir()))
	if err := r.RegisterAdapter(nil); err == nil {
		t.Fatalf("RegisterAdapter(nil) must fail")
	}
	if err := r.RegisterAdapter(stubAdapter{name: ""}); err == nil {
		t.Fatalf("RegisterAdapter(name=\"\") must fail")
	}
}

// stubAdapter is a no-op Adapter used to test RegisterOverride. It
// exists only in tests.
type stubAdapter struct{ name string }

func (s stubAdapter) Name() string { return s.name }
func (s stubAdapter) Run(_ context.Context, _ RunInput) (RunOutput, error) {
	return RunOutput{}, nil
}
