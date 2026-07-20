package harness

import (
	"fmt"
	"sync"

	"compound/internal/runattempt"
)

// AdapterRegistry is the runtime lookup table for harness adapters. The
// Registry type (Harness -> Capabilities) in harness.go and this
// AdapterRegistry (Name -> Adapter) are deliberately separate:
// capabilities are a static manifest, executors are a runtime
// construct. A future slice joins them -- "given the work envelope,
// which adapter drives it?" -- but today they live side by side.
//
// NewAdapterRegistry returns a registry pre-populated with the built-in
// adapters (os-entrypoints, claude, codex). Callers may RegisterAdapter
// to add custom adapters (e.g. a test harness) or override the
// built-ins (e.g. a wrapper that adds structured logging).
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewAdapterRegistry returns an AdapterRegistry with the built-in
// adapters pre-registered. The built-ins share a single runattempt.Store;
// callers that need per-adapter stores can construct adapters manually
// and RegisterAdapter them.
func NewAdapterRegistry(store *runattempt.Store) *AdapterRegistry {
	r := &AdapterRegistry{adapters: make(map[string]Adapter)}
	r.RegisterAdapter(NewOSEntrypointsAdapter(store))
	r.RegisterAdapter(NewClaudeAdapter(store))
	r.RegisterAdapter(NewCodexAdapter(store))
	return r
}

// RegisterAdapter adds (or replaces) an adapter under its Name(). Returns
// an error if the adapter is nil or has an empty Name.
func (r *AdapterRegistry) RegisterAdapter(a Adapter) error {
	if a == nil {
		return fmt.Errorf("adapter registry: cannot register nil adapter")
	}
	if a.Name() == "" {
		return fmt.Errorf("adapter registry: adapter has empty Name()")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Name()] = a
	return nil
}

// AdapterByName returns the adapter registered under name, plus a bool
// indicating whether the lookup succeeded. Read-only; safe for
// concurrent callers.
func (r *AdapterRegistry) AdapterByName(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// Names returns the sorted list of registered adapter names. Useful for
// `compound harness list` and for the architecture doctor checks
// (every harness in the manifest must have an executor).
func (r *AdapterRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for n := range r.adapters {
		out = append(out, n)
	}
	// Sort for deterministic output (operator-visible list, doctor
	// check, test assertions).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
