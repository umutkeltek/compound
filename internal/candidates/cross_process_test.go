package candidates

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const candidateHelperActionEnv = "COMPOUND_CANDIDATE_HELPER_ACTION"

type candidateHelperResult struct {
	output []byte
	err    error
}

// TestCandidateSubprocess is the process boundary used by the concurrency
// falsifiers below. A normal test run leaves the action unset and returns.
func TestCandidateSubprocess(t *testing.T) {
	action := os.Getenv(candidateHelperActionEnv)
	if action == "" {
		return
	}
	home := os.Getenv("COMPOUND_CANDIDATE_HELPER_HOME")
	id := os.Getenv("COMPOUND_CANDIDATE_HELPER_ID")
	marker := os.Getenv("COMPOUND_CANDIDATE_HELPER_MARKER")
	ready := os.Getenv("COMPOUND_CANDIDATE_HELPER_READY")
	if err := os.WriteFile(ready, []byte("ready\n"), 0o644); err != nil {
		t.Fatalf("publish helper readiness: %v", err)
	}
	payload := struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Marker string `json:"marker"`
	}{ID: id, Kind: string(Memory), Marker: marker}

	switch action {
	case "write":
		if err := WriteCandidate(home, Memory, id, payload); err != nil {
			t.Fatalf("write candidate: %v", err)
		}
		fmt.Println("WROTE=true")
	case "create":
		created, err := CreateCandidateIfAbsent(home, Memory, id, payload)
		if err != nil {
			t.Fatalf("create candidate: %v", err)
		}
		fmt.Printf("CREATED=%t\n", created)
	default:
		t.Fatalf("unknown candidate helper action %q", action)
	}
}

func TestWriteCandidateAndCreateCandidateIfAbsentShareLock(t *testing.T) {
	home := t.TempDir()
	lockPath := filepath.Join(home, "lock", "candidates.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		t.Fatal(err)
	}
	locked := true
	t.Cleanup(func() {
		if locked {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		}
		_ = lockFile.Close()
	})

	writeReady, writeDone := startCandidateHelper(t, home, "write", "replace", "replacement")
	createReady, createDone := startCandidateHelper(t, home, "create", "create", "create-only")
	waitForCandidateHelperReady(t, writeReady)
	waitForCandidateHelperReady(t, createReady)

	early := make([]candidateHelperResult, 0, 2)
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for len(early) == 0 {
		select {
		case result := <-writeDone:
			early = append(early, result)
		case result := <-createDone:
			early = append(early, result)
		case <-timer.C:
			goto release
		}
	}

release:
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	locked = false
	if len(early) > 0 {
		t.Fatalf("candidate publication completed while candidates.lock was held: %+v", early)
	}
	waitForCandidateHelper(t, "write", writeDone)
	waitForCandidateHelper(t, "create", createDone)
}

func startCandidateHelper(t *testing.T, home, action, id, marker string) (string, <-chan candidateHelperResult) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCandidateSubprocess$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		candidateHelperActionEnv+"="+action,
		"COMPOUND_CANDIDATE_HELPER_HOME="+home,
		"COMPOUND_CANDIDATE_HELPER_ID="+id,
		"COMPOUND_CANDIDATE_HELPER_MARKER="+marker,
		"COMPOUND_CANDIDATE_HELPER_READY="+ready,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s helper: %v", action, err)
	}
	done := make(chan candidateHelperResult, 1)
	go func() {
		err := cmd.Wait()
		done <- candidateHelperResult{output: output.Bytes(), err: err}
	}()
	return ready, done
}

func waitForCandidateHelperReady(t *testing.T, path string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect helper readiness: %v", err)
		}
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for helper readiness at %s", path)
		case <-ticker.C:
		}
	}
}

func waitForCandidateHelper(t *testing.T, name string, done <-chan candidateHelperResult) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("%s helper failed: %v\n%s", name, result.err, result.output)
		}
	case <-timer.C:
		t.Fatalf("timed out waiting for %s helper", name)
	}
}
