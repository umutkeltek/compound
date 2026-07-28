package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"compound/internal/routeevidence"
)

type stringListFlag []string

func (s *stringListFlag) String() string     { return fmt.Sprint([]string(*s)) }
func (s *stringListFlag) Set(v string) error { *s = append(*s, v); return nil }

func tryRouteExperimentPlanCommand(args []string) bool {
	if len(args) == 0 || args[0] != "experiment-plan" {
		return false
	}
	code := runRouteExperimentPlanCommand(args[1:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
	return true
}

func runRouteExperimentPlanCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("route experiment-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	role := fs.String("role", "opus", "Claude role alias: default|opus|sonnet|haiku|subagent")
	root := fs.String("profiles-root", filepath.Join(os.Getenv("HOME"), "Projects/systems/switchback/.switchback/state/profile-conformance"), "Switchback non-secret conformance projection root")
	jsonOut := fs.Bool("json", false, "emit JSON")
	var profiles stringListFlag
	fs.Var(&profiles, "profile", "launch profile id (repeat at least twice)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "route experiment-plan: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	plan, err := routeevidence.PlanRouteExperiment(*root, *role, profiles)
	if err != nil {
		fmt.Fprintln(stderr, "route experiment-plan:", err)
		return 2
	}
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "# route experiment-plan %s\n- role_alias: %s\n", plan.ID, plan.RoleAlias)
	for _, lane := range plan.Lanes {
		fmt.Fprintf(stdout, "- lane: profile=%s role=%s requested_alias=%s expected_route=%s observed_provider=unknown observed_model=unknown observed_account=unknown evidence=%s\n", lane.ProfileID, lane.RoleAlias, lane.RequestedAlias, lane.ExpectedRoute, lane.EvidenceStatus)
	}
	fmt.Fprintf(stdout, "- independence: %s\n- execution: %s\n- verdict: %s\n", plan.Independence, plan.Execution, plan.Verdict)
	return 0
}
