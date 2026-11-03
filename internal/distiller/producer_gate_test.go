package distiller

import (
	"os"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// TestMain switches the automatic candidate producers ON for fixtures that
// predate the 2026-11-03 opt-in gate; TestDistillerRunIsOffByDefault pins it.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv(candidates.ProducersEnv); !set {
		_ = os.Setenv(candidates.ProducersEnv, "1")
	}
	os.Exit(m.Run())
}

func TestDistillerRunIsOffByDefault(t *testing.T) {
	t.Setenv(candidates.ProducersEnv, "")
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(home, store, []byte(`{}`), DefaultPolicy(), nil, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), candidates.ProducersEnv) {
		t.Fatalf("distiller run must refuse with the opt-in reason, got %v", err)
	}
	if entries, _ := os.ReadDir(home + "/inbox"); len(entries) > 0 {
		for _, e := range entries {
			if sub, _ := os.ReadDir(home + "/inbox/" + e.Name()); len(sub) > 0 {
				t.Fatalf("producer off wrote into inbox/%s", e.Name())
			}
		}
	}
}
