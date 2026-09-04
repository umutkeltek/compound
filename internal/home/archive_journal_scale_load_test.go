package home

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// journalScaleLoadAverageOneMinute returns the 1-minute load average, or
// (0, false) when neither the darwin sysctl nor the linux /proc/loadavg
// surface is readable. It mirrors promptScaleLoadAverageOneMinute in
// internal/hook (added by the R3E hook slice for the same reason); the two are
// separate because a test helper cannot cross package boundaries without
// promoting it to a shipped package, and this one is test-only.
func journalScaleLoadAverageOneMinute() (float64, bool) {
	if value, err := syscall.Sysctl("vm.loadavg"); err == nil && strings.TrimSpace(value) != "" {
		if fields := strings.Fields(value); len(fields) >= 1 {
			if f, parseErr := strconv.ParseFloat(fields[0], 64); parseErr == nil {
				return f, true
			}
		}
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) >= 1 {
			if f, parseErr := strconv.ParseFloat(fields[0], 64); parseErr == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// journalScaleWallClockMeaningful reports whether a wall-clock budget is worth
// asserting right now. The bytes-written, append-only-shape and peak-heap
// assertions in these scale tests are properties of the ALGORITHM and are
// asserted unconditionally. Duration is a property of the DISK and the
// machine's current load, and this suite does 20,000 real fsyncs.
//
// Measured 2026-09-05 on the branch that made this journal linear:
// TestOperationalArchiveJournalBytes20k takes 1m1s standalone (ceiling 120s,
// comfortable) and 366s inside the certifying battery, where internal/home
// runs alongside every other package and the box sits above load 9. Six times
// slower, no regression — the duration assertion must skip under contention,
// not red. The linearity proof does not depend on it: the 1k/2k test pins the
// doubling ratio (linear ~2x vs quadratic ~4x) and that assertion always runs.
func journalScaleWallClockMeaningful(t *testing.T) bool {
	t.Helper()
	threshold := runtime.NumCPU() / 2
	if threshold < 1 {
		threshold = 1
	}
	if load, ok := journalScaleLoadAverageOneMinute(); ok && load > float64(threshold) {
		t.Logf("load %.1f exceeds %d (half of NumCPU=%d); skipping the wall-clock budget, algorithmic assertions still enforced",
			load, threshold, runtime.NumCPU())
		return false
	}
	return true
}
