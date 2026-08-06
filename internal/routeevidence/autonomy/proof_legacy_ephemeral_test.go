package autonomy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"time"
)

// A legacy work_live_ record persisted in a terminal state is historical debris:
// it can never validate as a durable envelope, so eligibility compilation must
// exclude it visibly instead of failing closed over the entire class. A
// non-legacy invalid durable record must still fail the compilation.
func TestCompileExcludesTerminalLegacyEphemeralWorkRecords(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "genuine", proofNow.Add(-48*time.Hour), sampleOptions{})

	writeRawWork(t, home, "work_live_deadbeef0001", map[string]any{
		"schema_version":    "compound.work.v0.1",
		"id":                "work_live_deadbeef0001",
		"created_at":        proofNow.Add(-240 * time.Hour).UTC().Format(time.RFC3339),
		"state":             "completed",
		"owner":             "agent:legacy-hook",
		"success_predicate": "unattributed live-hook mutation (legacy)",
	})

	artifact := compile(t, home, plan, 1)
	if artifact.ExcludedInvalidLegacyCount != 1 {
		t.Fatalf("legacy ephemeral record not visibly excluded: %+v", artifact)
	}
	for _, sample := range artifact.Samples {
		if strings.HasPrefix(sample.WorkID, "work_live_") {
			t.Fatalf("legacy ephemeral record leaked into samples: %+v", sample)
		}
	}

	writeRawWork(t, home, "work_badd00d0000001", map[string]any{
		"schema_version": "compound.work.v0.1",
		"id":             "work_badd00d0000001",
		"created_at":     proofNow.Add(-240 * time.Hour).UTC().Format(time.RFC3339),
		"state":          "completed",
	})
	if _, err := compileArtifact(home, plan, 1); err == nil {
		t.Fatal("non-legacy invalid durable record must still fail compilation closed")
	}
}

func writeRawWork(t *testing.T, home, id string, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "work", "work", id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
