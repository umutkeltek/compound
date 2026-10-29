package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestApprovedMemoryTriggerUsesGovernedResumeAndIgnoresReplay(t *testing.T) {
	hp, cwd := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(hp, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := []byte("corpus_lanes:\n  - lane: fixture\n    source_family: fixture\n    privacy: private\n    source:\n      root: " + cwd + "\n      format: markdown\n")
	if err := os.WriteFile(filepath.Join(hp, "config", "memory-engine.yaml"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(hp))
	id := "work_memory_refresh_" + hex.EncodeToString(sum[:])[:16]
	if err := os.MkdirAll(filepath.Join(hp, "work", "work"), 0700); err != nil {
		t.Fatal(err)
	}
	w := envelope.WorkEnvelope{Header: envelope.Header{ID: id}, Owner: memoryRefreshActor, State: "verifying"}
	body, _ := json.Marshal(w)
	if err := os.WriteFile(filepath.Join(hp, "work", "work", id+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	old := startMemoryRefresh
	defer func() { startMemoryRefresh = old }()
	calls := 0
	startMemoryRefresh = func(binary, home, workspace string, args []string) error {
		calls++
		joined := strings.Join(args, " ")
		for _, part := range []string{"work advance " + id, "--execute --resume-verifying", "--harness os-harness-entrypoints", "-- corpus refresh"} {
			if part == "-- corpus refresh" {
				if args[len(args)-2] != "corpus" || args[len(args)-1] != "refresh" {
					t.Fatal(args)
				}
				continue
			}
			if !strings.Contains(joined, part) {
				t.Fatalf("missing %s: %s", part, joined)
			}
		}
		if home != hp || workspace != cwd {
			t.Fatal("wrong scope")
		}
		return nil
	}
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	if got := refreshApprovedMemory(hp, fx, time.Now()); !strings.HasPrefix(got, "requested:") {
		t.Fatalf("trigger %s", got)
	}
	fx.Live = false
	refreshApprovedMemory(hp, fx, time.Now())
	fx.Live = true
	fx.Hook = "UserPromptSubmit"
	refreshApprovedMemory(hp, fx, time.Now())
	if calls != 1 {
		t.Fatalf("unexpected launches %d", calls)
	}
}
