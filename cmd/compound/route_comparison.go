package main

import (
	"os"

	routeevidencecli "compound/internal/routeevidence/cli"
)

// tryRouteComparisonCommand runs `compound route comparison`.
//
// The command body lives with its owner in internal/routeevidence/cli, so
// cmd/compound keeps only the dispatch and the cmd/compound LOC budget does
// not grow.
func tryRouteComparisonCommand(args []string, homePath string) bool {
	if len(args) == 0 || args[0] != "comparison" {
		return false
	}
	if code := routeevidencecli.RunComparison(args[1:], homePath, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
	return true
}
