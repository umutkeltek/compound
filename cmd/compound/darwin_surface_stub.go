//go:build !darwin

// Non-darwin stubs for the darwin-only observation surfaces. The command
// registry references cmdSchedule/cmdBrowser unconditionally, so without
// these stubs a non-darwin `go build ./cmd/compound` fails to compile
// (undefined: cmdSchedule) — a public/build blocker. The projections
// observe launchd/crontab and macOS process surfaces; off-darwin there is
// nothing to observe, so the commands refuse loudly (exit 2) instead of
// serving an empty projection that reads as "nothing scheduled".

package main

import (
	"fmt"
	"os"
)

func cmdSchedule(_ []string) {
	fmt.Fprintln(os.Stderr, "compound schedule is only supported on darwin (launchd/crontab projection)")
	os.Exit(2)
}

func cmdBrowser(_ []string) {
	fmt.Fprintln(os.Stderr, "compound browser is only supported on darwin (macOS process projection)")
	os.Exit(2)
}
