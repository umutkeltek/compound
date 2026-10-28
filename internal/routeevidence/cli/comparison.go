package routeevidencecli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"compound/internal/routeevidence"
)

// ComparisonUsage is the usage line for `compound route comparison`.
const ComparisonUsage = "usage: compound route comparison --input <file> [--record]"

// RunComparison assesses one route-change decision against the route outcomes
// collected for it and prints the verdict.
//
// The comparison is EVIDENCE. It says whether the collected evidence can
// recommend replacing the incumbent route with the candidate route, and names
// every reason it cannot. It does not change routing policy, mutate a
// gateway, provider, or model-router config, authorize spend, rank providers,
// or claim task success.
func RunComparison(args []string, homePath string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("route comparison", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "comparison input file")
	record := flags.Bool("record", false, "record the comparison under COMPOUND_HOME")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "route comparison: unexpected argument %q\n", flags.Arg(0))
		fmt.Fprintln(stderr, ComparisonUsage)
		return 2
	}
	if strings.TrimSpace(*inputPath) == "" {
		fmt.Fprintln(stderr, ComparisonUsage)
		return 2
	}
	input, err := routeevidence.LoadRouteComparisonInput(*inputPath)
	if err != nil {
		fmt.Fprintln(stderr, "route comparison:", err)
		return 1
	}
	comparison := routeevidence.BuildRouteComparison(input)
	if *record {
		path, err := routeevidence.RecordRouteComparison(homePath, comparison)
		if err != nil {
			fmt.Fprintln(stderr, "route comparison:", err)
			return 1
		}
		fmt.Fprintf(stdout, "- recorded: %s\n", path)
	}
	writeComparison(stdout, comparison)
	return 0
}

// writeComparison renders the verdict.
func writeComparison(stdout io.Writer, comparison routeevidence.RouteComparison) {
	assessment := comparison.Assessment
	fmt.Fprintf(stdout, "# route comparison %s\n", comparison.ID)
	fmt.Fprintf(stdout, "- decision: %s\n", comparison.DecisionID)
	fmt.Fprintf(stdout, "- incumbent_route: %s/%s (%s)\n", comparison.IncumbentRoute.Provider, comparison.IncumbentRoute.Model, comparison.IncumbentRoute.Protocol)
	fmt.Fprintf(stdout, "- candidate_route: %s/%s (%s)\n", comparison.CandidateRoute.Provider, comparison.CandidateRoute.Model, comparison.CandidateRoute.Protocol)
	fmt.Fprintf(stdout, "- assessment_class: %s\n", assessment.Class)
	fmt.Fprintf(stdout, "- recommend_replacement: %t\n", assessment.RecommendReplacement)
	fmt.Fprintf(stdout, "- matched_cases: %d\n", assessment.MatchedCases)
	fmt.Fprintf(stdout, "- incumbent_cases: %d\n", assessment.IncumbentCases)
	fmt.Fprintf(stdout, "- candidate_cases: %d\n", assessment.CandidateCases)
	fmt.Fprintf(stdout, "- min_repetitions: %d\n", assessment.MinRepetitions)
	fmt.Fprintf(stdout, "- incumbent_successes: %d\n", assessment.IncumbentSuccesses)
	fmt.Fprintf(stdout, "- candidate_successes: %d\n", assessment.CandidateSuccesses)
	fmt.Fprintf(stdout, "- candidate_failures: %d\n", assessment.CandidateFailures)
	fmt.Fprintf(stdout, "- critical_regressions: %d\n", assessment.CriticalRegressions)
	fmt.Fprintf(stdout, "- incumbent_cost_micros: %d\n", assessment.IncumbentCostMicros)
	fmt.Fprintf(stdout, "- candidate_cost_micros: %d\n", assessment.CandidateCostMicros)
	fmt.Fprintf(stdout, "- incumbent_latency_ms: %d\n", assessment.IncumbentLatencyMS)
	fmt.Fprintf(stdout, "- candidate_latency_ms: %d\n", assessment.CandidateLatencyMS)
	fmt.Fprintf(stdout, "- rationale: %s\n", assessment.Rationale)
	for _, reason := range assessment.BlockingReasons {
		fmt.Fprintf(stdout, "- blocked: %s\n", reason)
	}
}
