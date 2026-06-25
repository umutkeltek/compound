package engine

import "testing"

func TestNewConfiguredUsesNullWithoutPrivateEngineDefault(t *testing.T) {
	t.Setenv("COMPOUND_ENGINE_BIN", "")

	if got := NewConfigured().Capabilities().Name; got != "null" {
		t.Fatalf("expected null engine without configured CLI, got %q", got)
	}
}

func TestNewConfiguredUsesCLIOverlay(t *testing.T) {
	t.Setenv("COMPOUND_ENGINE_BIN", "synthetic-engine")

	if got := NewConfigured().Capabilities().Name; got != "synthetic-engine" {
		t.Fatalf("expected configured engine name, got %q", got)
	}
}
