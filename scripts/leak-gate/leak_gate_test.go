// Package leakgate runs the leak-gate's own Python suite inside the Go test run, so the
// presubmit selects it when this folder changes and the full post-land run covers it.
package leakgate

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestLeakGateSuite(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-B", "test_leak_gate.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("leak-gate suite failed: %v\n%s", err, output)
	}
}
