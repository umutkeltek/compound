//go:build !darwin

package schedule

import (
	"fmt"
	"os"
)

// Run refuses loudly off darwin: the projection observes launchd/crontab and
// there is nothing to observe on other platforms — an empty projection would
// read as "nothing scheduled". The command registry references schedule.Run
// unconditionally, so this stub keeps non-darwin builds compiling (pattern:
// the parked darwin_surface_stub.go preserved in archive commit 40af455b).
func Run(_ []string) {
	fmt.Fprintln(os.Stderr, "compound schedule is only supported on darwin (launchd/crontab projection)")
	os.Exit(2)
}
