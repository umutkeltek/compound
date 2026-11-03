package observation

import (
	"os"
	"testing"

	"compound/internal/candidates"
)

// TestMain switches the automatic candidate producers ON for fixtures that
// predate the 2026-11-03 opt-in gate (knowledge-import candidates and review
// candidates are automatic producers).
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv(candidates.ProducersEnv); !set {
		_ = os.Setenv(candidates.ProducersEnv, "1")
	}
	os.Exit(m.Run())
}
