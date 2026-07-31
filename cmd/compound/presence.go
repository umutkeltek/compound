package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

const presenceUsage = "usage: compound presence [--json] [--ttl DURATION] [--include-stale] [--tail-bytes N]"

// cmdPresence answers "who is here right now" from evidence Compound already
// holds. It is deliberately NOT sourced from delegate bindings: an agent this
// runtime never launched is still present the moment it touches the ledger.
func cmdPresence(args []string) {
	jsonOut := false
	opts := work.PresenceOptions{}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json":
			jsonOut = true
		case "--include-stale":
			opts.IncludeStale = true
		case "--ttl":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, presenceUsage)
				os.Exit(2)
			}
			parsed, err := time.ParseDuration(strings.TrimSpace(args[i]))
			if err != nil || parsed <= 0 {
				fmt.Fprintln(os.Stderr, "error: --ttl must be a positive duration (e.g. 30m)")
				os.Exit(2)
			}
			opts.TTL = parsed
		case "--tail-bytes":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, presenceUsage)
				os.Exit(2)
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(args[i]), 10, 64)
			if err != nil || parsed <= 0 {
				fmt.Fprintln(os.Stderr, "error: --tail-bytes must be a positive integer")
				os.Exit(2)
			}
			opts.TailBytes = parsed
		default:
			fmt.Fprintln(os.Stderr, presenceUsage)
			os.Exit(2)
		}
	}

	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	projection, err := store.PresenceProjectionFor(time.Now(), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(projection); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	printPresence(projection)
}

func printPresence(projection work.PresenceProjection) {
	fmt.Printf("# presence (ttl %ds)\n\n", projection.TTLSeconds)
	if len(projection.Present) == 0 {
		fmt.Println("no actor seen inside the ttl window")
	}
	for _, row := range projection.Present {
		marker := "*"
		if !row.Fresh {
			marker = "-"
		}
		fmt.Printf("%s %s\n", marker, row.Actor)
		fmt.Printf("    last seen %s (%s ago) · leases %d · events %d\n",
			row.LastSeen, presenceAge(row.AgeSeconds), row.Leases, row.Events)
		for _, observation := range row.Latest {
			detail := observation.Detail
			if detail != "" {
				detail = " " + detail
			}
			fmt.Printf("    %-5s %s%s\n", observation.Kind, observation.WorkID, detail)
		}
	}
	fmt.Printf("\nactors %d · fresh %d · stale %d · lease-only %d · event-only %d · both %d\n",
		projection.Counts.Actors, projection.Counts.Fresh, projection.Counts.Stale,
		projection.Counts.LeaseOnly, projection.Counts.EventOnly, projection.Counts.BothSources)
	if projection.TailTruncated {
		fmt.Printf("event evidence read from the last %d bytes of the work log\n", projection.TailBytes)
	}
	for _, note := range projection.Integrity {
		fmt.Println("integrity:", note)
	}
}

func presenceAge(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%dm", seconds/60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
	default:
		return fmt.Sprintf("%dd%dh", seconds/86400, (seconds%86400)/3600)
	}
}
