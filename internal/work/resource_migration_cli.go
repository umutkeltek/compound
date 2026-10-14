package work

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"compound/internal/home"
)

func RunResourceMigrationCLI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("land-lease migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeArg := flags.String("home", "", "home to cut over")
	workID := flags.String("work", "", "current owner work")
	actor := flags.String("actor", "", "current work lease holder")
	operator := flags.String("operator", "", "operator provenance")
	evidence := flags.String("evidence", "", "actual all-writer pause evidence")
	paused := flags.Bool("writers-paused", false, "all participating writers are paused")
	expires := flags.String("expires", "", "pause expiry RFC3339, at most five minutes")
	backing := flags.String("backing-home", "", "shared owning home for host proof capacity")
	flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected arguments")
		return 2
	}
	end, err := time.Parse(time.RFC3339Nano, *expires)
	if err != nil {
		fmt.Fprintln(stderr, "invalid --expires:", err)
		return 2
	}
	root, err := home.Resolve(*homeArg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := Open(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result, err := store.MigrateCoordinationResources(*workID, ResourceMigrationInput{
		Actor: *actor, Operator: *operator, EvidenceRef: *evidence,
		BackingHome: *backing, WritersPaused: *paused, ExpiresAt: end,
	}, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "resource migration:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
