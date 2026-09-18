//go:build darwin

package hook

import (
	"strconv"
	"strings"
	"syscall"
)

// sysctlLoadAverageOneMinute is the darwin probe behind the contention skip
// in promptScaleLoadAverageOneMinute; it lives behind a build tag because
// syscall.Sysctl does not exist on linux, and the battery's go-vet gate
// compiles test files on both (CI run 35304076022, go-vet gate).
func sysctlLoadAverageOneMinute() (float64, bool) {
	value, err := syscall.Sysctl("vm.loadavg")
	if err != nil || strings.TrimSpace(value) == "" {
		return 0, false
	}
	fields := strings.Fields(value)
	if len(fields) < 1 {
		return 0, false
	}
	f, parseErr := strconv.ParseFloat(fields[0], 64)
	if parseErr != nil {
		return 0, false
	}
	return f, true
}
