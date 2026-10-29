package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"compound/internal/corpus"
	"compound/internal/work"
)

const memoryRefreshActor = "automation:memory-refresh"

var startMemoryRefresh = func(binary, homePath, cwd string, args []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	// The native work command owns the RunAttempt and captures the actual refresh
	// child's output. Detach only this admission command from the short-lived hook.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// refreshApprovedMemory never captures/indexes in the prompt injection process.
// The existing native execution rail checks current policy and the exact Work
// generation before the child can mutate. Competing triggers lose that CAS.
func refreshApprovedMemory(hp string, fx Fixture, now time.Time) string {
	if !fx.Live || (fx.Hook != "PostToolUse" && fx.Hook != "SessionEnd" && fx.Hook != "Stop") {
		return ""
	}
	source := ""
	if fx.Hook == "SessionEnd" {
		source = fx.TranscriptPath
	}
	due, err := corpus.RefreshDue(hp, now, source)
	if err != nil {
		return "configuration_unavailable"
	}
	if !due {
		return ""
	}
	sum := sha256.Sum256([]byte(hp))
	id := "work_memory_refresh_" + hex.EncodeToString(sum[:])[:16]
	store, err := work.OpenReadOnly(hp)
	if err != nil {
		return "work_unavailable"
	}
	w, err := store.Get(id)
	if err != nil {
		return "setup_required"
	}
	if w.Owner != memoryRefreshActor || w.State != "verifying" {
		return "work_" + w.State
	}
	cwd := fx.Cwd
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) == filepath.Clean(hp) {
		return "workspace_unavailable"
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "workspace_unavailable"
	}
	binary, err := os.Executable()
	if err != nil {
		return "binary_unavailable"
	}
	args := []string{"work", "advance", id, "--to", "running", "--by", memoryRefreshActor,
		"--execute", "--resume-verifying", "--harness", "os-harness-entrypoints",
		"--workspace", cwd, "--timeout", "5m", "--", binary, "corpus", "refresh"}
	if err := startMemoryRefresh(binary, hp, cwd, args); err != nil {
		return "launch_failed"
	}
	return fmt.Sprintf("requested:%s", id)
}
