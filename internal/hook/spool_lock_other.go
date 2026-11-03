//go:build !unix

package hook

import "os"

// Without flock the spool relies on O_APPEND single-write appends and a
// single drainer by convention.
func lockSpoolFile(*os.File, bool) error { return nil }

func unlockSpoolFile(*os.File) {}
