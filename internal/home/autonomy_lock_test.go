package home

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAutonomyAdmissionLockSerializesWriters(t *testing.T) {
	home := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- WithAutonomyAdmissionLock(home, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- WithAutonomyAdmissionLock(home, func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second autonomy writer entered before the first released")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second autonomy writer did not enter after release")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestAutonomyAdmissionLockSerializesProcesses(t *testing.T) {
	home := t.TempDir()
	var cmd *exec.Cmd
	var entered <-chan string
	err := WithAutonomyAdmissionLock(home, func() error {
		var startErr error
		cmd, entered, startErr = startAutonomyLockHelper(home, "enter")
		if startErr != nil {
			return startErr
		}
		select {
		case line := <-entered:
			return fmt.Errorf("child crossed parent process lock before release: %q", line)
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		t.Fatal(err)
	}
	select {
	case line := <-entered:
		if line != "entered" {
			t.Fatalf("unexpected child handshake after release: %q", line)
		}
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child process did not acquire lock after parent released")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestAutonomyAdmissionLockReleasesWhenProcessDies(t *testing.T) {
	home := t.TempDir()
	cmd, entered, err := startAutonomyLockHelper(home, "hold")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-entered:
		if line != "entered" {
			t.Fatalf("unexpected child handshake: %q", line)
		}
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child process did not acquire lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	done := make(chan error, 1)
	go func() { done <- WithAutonomyAdmissionLock(home, func() error { return nil }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("lock remained held after child process died")
	}
}

func TestOperationalArchiveAndRestoreKeepMovePlusIndexAtomicToAdmission(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".compound")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	if err := Init(home, "test", now); err != nil {
		t.Fatal(err)
	}
	hotPath := filepath.Join(home, "work", "work", "work_archive_lock.json")
	if err := os.MkdirAll(filepath.Dir(hotPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hotPath, []byte(`{"id":"work_archive_lock","state":"completed"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	type gapState struct {
		operation string
		entries   []OperationalArchiveEntry
	}
	gap := make(chan gapState)
	releaseGap := make(chan struct{})
	previousHook := operationalArchiveBeforeIndexCommit
	operationalArchiveBeforeIndexCommit = func(operation string, entries []OperationalArchiveEntry) {
		gap <- gapState{operation: operation, entries: append([]OperationalArchiveEntry(nil), entries...)}
		<-releaseGap
	}
	defer func() { operationalArchiveBeforeIndexCommit = previousHook }()

	assertAdmissionReaderWaits := func() <-chan error {
		t.Helper()
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			close(started)
			done <- WithAutonomyAdmissionLock(home, func() error { return nil })
		}()
		<-started
		select {
		case err := <-done:
			t.Fatalf("admission reader crossed archive move/index gap: %v", err)
		case <-time.After(30 * time.Millisecond):
		}
		return done
	}

	archiveDone := make(chan error, 1)
	go func() {
		_, err := ArchiveOperationalHistory(home, OperationalArchiveOptions{Kinds: []string{"work"}}, now)
		archiveDone <- err
	}()
	archivedGap := <-gap
	if archivedGap.operation != "archived" || len(archivedGap.entries) != 1 {
		t.Fatalf("archive gap = %+v", archivedGap)
	}
	if _, err := os.Stat(hotPath); !os.IsNotExist(err) {
		t.Fatalf("hot work should already be moved in archive gap: %v", err)
	}
	if active, err := LoadOperationalArchiveIndex(home); err != nil || len(active) != 0 {
		t.Fatalf("archive index should still describe the old state in gap: active=%+v err=%v", active, err)
	}
	readerDone := assertAdmissionReaderWaits()
	releaseGap <- struct{}{}
	if err := <-archiveDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}

	restoreDone := make(chan error, 1)
	go func() {
		_, err := RestoreOperationalArchive(home, OperationalRestoreOptions{All: true}, now.Add(time.Minute))
		restoreDone <- err
	}()
	restoredGap := <-gap
	if restoredGap.operation != "restored" || len(restoredGap.entries) != 1 {
		t.Fatalf("restore gap = %+v", restoredGap)
	}
	if _, err := os.Stat(hotPath); err != nil {
		t.Fatalf("hot work should already be restored in restore gap: %v", err)
	}
	if active, err := LoadOperationalArchiveIndex(home); err != nil || len(active) != 1 {
		t.Fatalf("restore index should still describe the archived state in gap: active=%+v err=%v", active, err)
	}
	readerDone = assertAdmissionReaderWaits()
	releaseGap <- struct{}{}
	if err := <-restoreDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	if active, err := LoadOperationalArchiveIndex(home); err != nil || len(active) != 0 {
		t.Fatalf("restore index did not commit: active=%+v err=%v", active, err)
	}
}

func TestAutonomyAdmissionLockProcessHelper(t *testing.T) {
	mode := os.Getenv("COMPOUND_AUTONOMY_LOCK_HELPER")
	if mode == "" {
		return
	}
	home := os.Getenv("COMPOUND_AUTONOMY_LOCK_HOME")
	fmt.Println("attempting")
	if err := WithAutonomyAdmissionLock(home, func() error {
		fmt.Println("entered")
		if mode == "hold" {
			for {
				time.Sleep(time.Hour)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func startAutonomyLockHelper(home, mode string) (*exec.Cmd, <-chan string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestAutonomyAdmissionLockProcessHelper$")
	cmd.Env = append(os.Environ(),
		"COMPOUND_AUTONOMY_LOCK_HELPER="+mode,
		"COMPOUND_AUTONOMY_LOCK_HOME="+home,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "attempting" {
		_ = cmd.Process.Kill()
		return nil, nil, fmt.Errorf("child did not announce lock attempt")
	}
	entered := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			entered <- scanner.Text()
			return
		}
		entered <- ""
	}()
	return cmd, entered, nil
}
