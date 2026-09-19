//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func readTestProcessSnapshots() ([]testProcessSnapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	snapshots := make([]testProcessSnapshot, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		body, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		// A process can exit between the readdir and this read: linux then
		// yields ENOENT (gone before open) or ESRCH (gone after open, read
		// fails) — both mean the pid vanished mid-scan and must be skipped
		// like ps does, not fail the whole snapshot (run 35376991764).
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read process %d stat: %w", pid, err)
		}
		snapshot, err := parseLinuxTestProcessStat(pid, string(body))
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func parseLinuxTestProcessStat(pid int, body string) (testProcessSnapshot, error) {
	closing := strings.LastIndexByte(body, ')')
	if closing < 0 {
		return testProcessSnapshot{}, fmt.Errorf("read process %d stat: missing command terminator", pid)
	}
	fields := strings.Fields(body[closing+1:])
	if len(fields) <= 19 {
		return testProcessSnapshot{}, fmt.Errorf("read process %d stat: got %d fields after command", pid, len(fields))
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil {
		// A non-numeric group means our field alignment is wrong; that is
		// a parse defect, not scan noise.
		return testProcessSnapshot{}, fmt.Errorf("read process %d stat: invalid process group %q", pid, fields[2])
	}
	if pgid <= 0 {
		// Kernel threads (kthreadd, ksoftirqd/...) carry process group 0.
		// A host VM's /proc lists them; a container's pid namespace does
		// not — which is why this surfaced only on CI runners (run
		// 35453308318): one kernel thread failed the whole scan and every
		// snapshot-dependent supervisor test died instantly. They are not
		// part of any test group; skip like any other scan noise.
		return testProcessSnapshot{}, nil
	}
	state := testProcessStateRunning
	if fields[0] == "Z" || fields[0] == "X" || fields[0] == "x" {
		state = testProcessStateZombie
	}
	return testProcessSnapshot{
		pid:     pid,
		pgid:    pgid,
		birthID: fields[19],
		state:   state,
	}, nil
}

func TestParseLinuxTestProcessStat(t *testing.T) {
	stat := func(state, pgid string) string {
		return "73 (command with ) inside) " + state + " 1 " + pgid + " 73 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 987654 0"
	}
	for _, tc := range []struct {
		name  string
		state string
		want  testProcessState
	}{
		{name: "running", state: "S", want: testProcessStateRunning},
		{name: "zombie", state: "Z", want: testProcessStateZombie},
		{name: "dead upper", state: "X", want: testProcessStateZombie},
		{name: "dead lower", state: "x", want: testProcessStateZombie},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLinuxTestProcessStat(73, stat(tc.state, "73"))
			if err != nil {
				t.Fatal(err)
			}
			if got.pid != 73 || got.pgid != 73 || got.birthID != "987654" || got.state != tc.want {
				t.Fatalf("snapshot = %+v, want pid/pgid 73 birth 987654 state %v", got, tc.want)
			}
		})
	}
	for _, body := range []string{"missing terminator", stat("S", "bad"), "73 (short) S 1 73"} {
		if _, err := parseLinuxTestProcessStat(73, body); err == nil {
			t.Fatalf("malformed stat %q was accepted", body)
		}
	}
	// Kernel threads (kthreadd, ksoftirqd/...) carry process group 0. A
	// host VM's /proc lists them; a container's pid namespace does not.
	// They must yield an empty snapshot, never a scan-fatal error (run
	// 35453308318: one kernel thread failed the whole snapshot read).
	if got, err := parseLinuxTestProcessStat(2, stat("S", "0")); err != nil || got.pid != 0 {
		t.Fatalf("kernel-thread stat: got (%+v, %v), want zero snapshot, nil error", got, err)
	}
}
