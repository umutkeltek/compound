//go:build !unix

package work

// pidAlive is the non-unix fallback. Without a signal-0 syscall we cannot prove
// a process is dead, so we assume a recorded pid is alive rather than reporting
// a false stall. The job-liveness probe is opt-in and only meaningful on unix.
func pidAlive(pid int) bool {
	return pid > 0
}
