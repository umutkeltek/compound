package main

// Compound `game` surface — studio-facing operator surface.
//
// Subcommands:
//   game doctor                                    — reachability + freshness summary (compound/game-doctor.v0.1)
//   game observe <steamweb|steamspy> --appids <csv> — pull signals into SourceObservation store
//   game genres                                    — genre catalog projection (compound/game-genres.v0.1)
//   game companies                                 — company catalog projection (compound/game-companies.v0.1)
//   game trending [--limit N] [--days D]            — top-N momentum view (compound/game-trending.v0.1)

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/game"
	"compound/internal/home"
)

const gameUsage = "usage: compound game <doctor|observe|genres|companies|trending> [flags]\n" +
	"       compound game observe <steamweb|steamspy> --appids <csv> [--genres <csv>] [--companies <csv>] [--json] [--home P]\n" +
	"       compound game doctor [--json] [--home P]\n" +
	"       compound game genres [--json] [--home P]\n" +
	"       compound game companies [--json] [--home P]\n" +
	"       compound game trending [--limit N] [--days D] [--json] [--home P]\n" +
	"  Read/write game-source observations."

func cmdGame(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, gameUsage)
		os.Exit(2)
	}
	var code int
	switch args[0] {
	case "doctor":
		code = cmdGameDoctor(args[1:])
	case "observe":
		code = cmdGameObserve(args[1:])
	case "genres":
		code = cmdGameGenres(args[1:])
	case "companies":
		code = cmdGameCompanies(args[1:])
	case "trending":
		code = cmdGameTrending(args[1:])
	default:
		fmt.Fprintln(os.Stderr, gameUsage)
		os.Exit(2)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func resolveGameHome(args []string) (string, error) {
	return home.Resolve(valueFlag(args, "--home"))
}

type gameObserveResult struct {
	Schema       string   `json:"schema"`
	AdapterID    string   `json:"adapter_id"`
	SourceSystem string   `json:"source_system"`
	Revision     string   `json:"revision,omitempty"`
	Written      int      `json:"written"`
	Failed       int      `json:"failed"`
	CandidateIDs []string `json:"candidate_ids,omitempty"`
	NoPromoteIDs []string `json:"no_promote_ids,omitempty"`
}

const gameObserveSchema = "compound/game-observe-result.v0.1"

func cmdGameObserve(args []string) int {
	if err := validateGameFlags("observe", args, map[string]bool{
		"--json": true, "--home": true, "--appids": true, "--genres": true, "--companies": true,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: adapter required (steamweb|steamspy)")
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	adapter := args[0]
	appidsFlag := strings.TrimSpace(valueFlag(args, "--appids"))
	if appidsFlag == "" {
		fmt.Fprintln(os.Stderr, "error: --appids required")
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	jsonOut := hasFlag(args, "--json")
	homePath, err := resolveGameHome(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	appids, err := parseGameAppIDs(appidsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	genres := gameSplitCSV(valueFlag(args, "--genres"))
	companies := gameSplitCSV(valueFlag(args, "--companies"))

	result, err := runGameObserve(adapter, appids, genres, companies, homePath, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	if jsonOut {
		printJSON(struct {
			Schema string            `json:"schema"`
			Result gameObserveResult `json:"result"`
		}{Schema: gameObserveSchema, Result: result})
		return 0
	}
	fmt.Printf("# game observe (%s)\n", gameObserveSchema)
	fmt.Printf("adapter:     %s\n", result.AdapterID)
	fmt.Printf("source_sys:  %s\n", result.SourceSystem)
	if result.Revision != "" {
		fmt.Printf("revision:    %s\n", result.Revision)
	}
	fmt.Printf("written:     %d\n", result.Written)
	if result.Failed > 0 {
		fmt.Printf("failed:      %d\n", result.Failed)
	}
	return 0
}

func cmdGameDoctor(args []string) int {
	if err := validateGameFlags("doctor", args, map[string]bool{"--json": true, "--home": true}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	jsonOut := hasFlag(args, "--json")
	homePath, err := resolveGameHome(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	report, err := game.Doctor(homePath, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if jsonOut {
		printJSON(report)
		return 0
	}
	fmt.Printf("# game doctor (%s)\n", report.Schema)
	fmt.Printf("generated: %s\n", report.Generated.Format(time.RFC3339))
	for _, w := range report.Warnings {
		fmt.Printf("warning:   %s\n", w)
	}
	if len(report.Counts) > 0 {
		fmt.Println("counts:")
		for _, k := range gameSortedStringKeys(report.Counts) {
			fmt.Printf("  %s: %d\n", k, report.Counts[k])
		}
	}
	if len(report.Findings) > 0 {
		fmt.Println("findings:")
		for _, f := range report.Findings {
			fmt.Printf("  %s — %s\n", f.Code, f.Detail)
		}
	} else {
		fmt.Println("findings: none")
	}
	return 0
}

func cmdGameGenres(args []string) int {
	if err := validateGameFlags("genres", args, map[string]bool{"--json": true, "--home": true}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	jsonOut := hasFlag(args, "--json")
	homePath, err := resolveGameHome(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	report, err := game.ProjectGenres(homePath, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if jsonOut {
		printJSON(report)
		return 0
	}
	printGameCatalogHuman(report, "genres")
	return 0
}

func cmdGameCompanies(args []string) int {
	if err := validateGameFlags("companies", args, map[string]bool{"--json": true, "--home": true}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	jsonOut := hasFlag(args, "--json")
	homePath, err := resolveGameHome(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	report, err := game.ProjectCompanies(homePath, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if jsonOut {
		printJSON(report)
		return 0
	}
	printGameCatalogHuman(report, "companies")
	return 0
}

func cmdGameTrending(args []string) int {
	if err := validateGameFlags("trending", args, map[string]bool{
		"--json": true, "--home": true, "--limit": true, "--days": true,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, gameUsage)
		return 2
	}
	jsonOut := hasFlag(args, "--json")
	homePath, err := resolveGameHome(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	limit := parseGameIntFlag(valueFlag(args, "--limit"), 10)
	days := parseGameIntFlag(valueFlag(args, "--days"), 7)
	if limit <= 0 || limit > 1000 {
		fmt.Fprintln(os.Stderr, "error: --limit must be 1..1000")
		return 2
	}
	if days <= 0 || days > 365 {
		fmt.Fprintln(os.Stderr, "error: --days must be 1..365")
		return 2
	}
	report, err := game.ProjectTrending(homePath, time.Now().UTC(), limit, days)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if jsonOut {
		printJSON(report)
		return 0
	}
	fmt.Printf("# game trending (%s)\n", report.Schema)
	fmt.Printf("window:    %s\n", report.Window)
	if len(report.Entries) == 0 {
		fmt.Println("- no observations in window")
		return 0
	}
	for _, e := range report.Entries {
		fmt.Printf("- [%s] %s — %d hits (last %s)\n",
			e.Kind, e.Label, e.Hits, e.LastSeen.Format(time.RFC3339))
	}
	return 0
}

func printGameCatalogHuman(r game.CatalogReport, label string) {
	fmt.Printf("# game %s (%s)\n", label, r.Schema)
	fmt.Printf("generated: %s  stale_after: %s\n", r.Generated.Format(time.RFC3339), r.StaleAfter)
	for _, w := range r.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	if len(r.Entries) == 0 {
		fmt.Println("- (empty)")
		return
	}
	for _, e := range r.Entries {
		state := "fresh"
		if e.Stale {
			state = "stale"
		}
		apps := ""
		if len(e.Apps) > 0 {
			apps = fmt.Sprintf(" apps=%s", strings.Join(e.Apps, ","))
		}
		roles := ""
		if len(e.Roles) > 0 {
			roles = fmt.Sprintf(" roles=%s", strings.Join(e.Roles, ","))
		}
		fmt.Printf("- [%s] %s (%s, source=%s, n=%d)%s%s\n",
			state, e.Label, e.SourceKey, e.Source, e.ObservationCount, apps, roles)
	}
}

func validateGameFlags(subcommand string, args []string, allowed map[string]bool) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name := a
		if eq := strings.Index(a, "="); eq >= 0 {
			name = a[:eq]
		}
		if i == 0 && subcommand == "observe" {
			continue
		}
		if !allowed[name] {
			return fmt.Errorf("%s: unknown flag %s", subcommand, a)
		}
		switch name {
		case "--home", "--appids", "--genres", "--companies", "--limit", "--days":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
			}
		}
	}
	return nil
}

func parseGameAppIDs(csv string) ([]int64, error) {
	parts := gameSplitCSV(csv)
	if len(parts) == 0 {
		return nil, fmt.Errorf("--appids must contain at least one appid")
	}
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		var v int64
		if _, err := fmt.Sscanf(p, "%d", &v); err != nil {
			return nil, fmt.Errorf("invalid appid %q: %w", p, err)
		}
		if v <= 0 {
			return nil, fmt.Errorf("invalid appid %q (must be positive)", p)
		}
		out = append(out, v)
	}
	return out, nil
}

func gameSplitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseGameIntFlag(s string, fallback int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return fallback
	}
	return v
}

func gameSortedStringKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
