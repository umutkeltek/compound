package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/runattempt"
)

// Every runner launch hands the child its own RunAttempt id. A provider lane's
// PreToolUse installed with --enforce-governed blocks only when it sees the id,
// so without it governed runs on a watch-only lane profile would not block.
// An inherited or adapter-supplied value is replaced, never trusted.
func TestRunnerHandsTheChildItsRunAttemptID(t *testing.T) {
	homePath := t.TempDir()
	store := runattempt.Open(homePath)
	seen := filepath.Join(t.TempDir(), "seen")
	out, err := runWithSubprocess(context.Background(), store, "os-harness-entrypoints", RunInput{
		WorkID: "work_run_attempt_env", Harness: "os-harness-entrypoints", Workspace: homePath,
		Command: "/bin/sh", Now: time.Now(),
	}, func(ctx context.Context, _ RunInput) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s' "$`+runattempt.LaunchEnvRunAttemptID+`" > "$0"`, seen)
		cmd.Env = append(os.Environ(), runattempt.LaunchEnvRunAttemptID+"=ratt_forged_by_adapter")
		return cmd
	}, defaultHeartbeatInterval)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, readErr := os.ReadFile(seen)
	if readErr != nil {
		t.Fatalf("child never ran: %v", readErr)
	}
	if out.RunAttempt.ID == "" || string(got) != out.RunAttempt.ID {
		t.Fatalf("child saw %s=%q, want its own RunAttempt id %q", runattempt.LaunchEnvRunAttemptID, got, out.RunAttempt.ID)
	}
}

func TestWithLaunchRunAttemptIDKeepsTheInheritedEnvironment(t *testing.T) {
	t.Setenv(runattempt.LaunchEnvRunAttemptID, "ratt_parent")
	env := withLaunchRunAttemptID(nil, "ratt_child")
	var ids []string
	hasPath := false
	for _, item := range env {
		if v, ok := strings.CutPrefix(item, runattempt.LaunchEnvRunAttemptID+"="); ok {
			ids = append(ids, v)
		}
		if strings.HasPrefix(item, "PATH=") {
			hasPath = true
		}
	}
	if len(ids) != 1 || ids[0] != "ratt_child" {
		t.Fatalf("want exactly the child's id, got %q", ids)
	}
	if !hasPath {
		t.Fatal("a nil adapter env means inherit: PATH must survive")
	}
}
