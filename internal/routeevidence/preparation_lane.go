package routeevidence

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/laneselect"
)

// PreparationLaneOptions configures the adapter that gathers evidence for the pure
// laneselect.RankPreparationLanes ranking.
type PreparationLaneOptions struct {
	FloorTokensPerSecond float64
	PrivacySensitive     bool
	// Liveness is an optional lane(model id) -> alive|down|unknown map (a probe seam). Absent
	// lanes default to unknown; the pure ranking never assumes a lane is alive.
	Liveness map[string]string
	// Filters are the veto seam (capacity planner, work_697dedbfca03). Nil in this packet.
	Filters []laneselect.PreparationLaneFilter
}

// PlanPreparationLane reads the Compound model-router authority (fit + cost, account/privacy
// gated) and the route-evidence ledger (observed tokens/sec) for a preparation-class
// capability, then ranks via the pure laneselect ranking. It is the impure adapter over the
// pure ranking; the registry and ledger are read through their existing owners, never cached
// or rebuilt.
func PlanPreparationLane(homePath, class string, opts PreparationLaneOptions, now time.Time) (laneselect.PreparationLaneRanking, error) {
	if !laneselect.IsPreparationClass(class) {
		return laneselect.PreparationLaneRanking{}, fmt.Errorf("capability class %q is not a preparation class", class)
	}
	plan, err := PlanCapabilityRoute(homePath, class, CapabilityRoutePlanOptions{
		PrivacySensitive:            opts.PrivacySensitive,
		SuppressPendingObservations: true,
	}, now)
	if err != nil {
		return laneselect.PreparationLaneRanking{}, err
	}
	targetStats, profileStats, err := loadRouteThroughput(homePath)
	if err != nil {
		return laneselect.PreparationLaneRanking{}, err
	}
	inputs := make([]laneselect.PreparationLaneInput, 0, len(plan.Chain))
	for _, lane := range plan.Chain {
		tps, runs, known := laneThroughput(lane, targetStats, profileStats)
		liveness := laneselect.LaneLivenessUnknown
		if opts.Liveness != nil {
			if v, ok := opts.Liveness[lane.ModelID]; ok {
				liveness = laneselect.NormalizeLiveness(v)
			}
		}
		inputs = append(inputs, laneselect.PreparationLaneInput{
			Lane:            lane.ModelID,
			Label:           lane.Lane,
			ClaimLevel:      lane.ClaimLevel,
			RoutingPosition: lane.Position,
			CostClass:       lane.CostClass,
			Tier:            lane.Tier,
			Harness:         lane.Harness,
			ProviderRoute:   lane.ProviderRoute,
			TokensPerSecond: tps,
			ThroughputKnown: known,
			ThroughputRuns:  runs,
			Liveness:        liveness,
		})
	}
	ranking := laneselect.RankPreparationLanes(class, opts.FloorTokensPerSecond, inputs, opts.Filters...)
	ranking.Warnings = append(ranking.Warnings, plan.Warnings...)
	return ranking, nil
}

type laneThroughputStat struct {
	completionTokens int
	latencyMS        int64
	runs             int
}

// loadRouteThroughput scans the route-evidence ledger and aggregates completed-run token
// throughput, indexed by (provider/model) and by client_profile. Only completed records with
// positive latency and completion tokens contribute — a failed or timing-less run is not
// evidence of speed.
func loadRouteThroughput(homePath string) (targetStats, profileStats map[string]laneThroughputStat, err error) {
	targetStats = map[string]laneThroughputStat{}
	profileStats = map[string]laneThroughputStat{}
	path := ExecutionRouteEvidenceLedgerPath(homePath)
	f, openErr := os.Open(path)
	if openErr != nil {
		if os.IsNotExist(openErr) {
			return targetStats, profileStats, nil
		}
		return nil, nil, fmt.Errorf("read route evidence ledger: %w", openErr)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		ev, decErr := Decode([]byte(raw))
		if decErr != nil {
			return nil, nil, fmt.Errorf("decode route evidence ledger: %w", decErr)
		}
		if !strings.EqualFold(ev.Status, "completed") || ev.LatencyMS <= 0 || ev.Usage.CompletionTokens <= 0 {
			continue
		}
		selectedKey := throughputKey(ev.SelectedTarget.Provider, ev.SelectedTarget.Model)
		accumulateThroughput(targetStats, selectedKey, ev)
		if strings.TrimSpace(ev.ResolvedProvider) != "" && strings.TrimSpace(ev.ResolvedModel) != "" {
			if resolved := throughputKey(ev.ResolvedProvider, ev.ResolvedModel); resolved != selectedKey {
				accumulateThroughput(targetStats, resolved, ev)
			}
		}
		if profile := strings.ToLower(strings.TrimSpace(ev.ClientProfile)); profile != "" {
			accumulateThroughput(profileStats, profile, ev)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, nil, fmt.Errorf("scan route evidence ledger: %w", scanErr)
	}
	return targetStats, profileStats, nil
}

func accumulateThroughput(stats map[string]laneThroughputStat, key string, ev Evidence) {
	if key == "" {
		return
	}
	s := stats[key]
	s.completionTokens += ev.Usage.CompletionTokens
	s.latencyMS += ev.LatencyMS
	s.runs++
	stats[key] = s
}

// laneThroughput resolves observed tokens/sec for a lane by matching route evidence to its
// resolution targets (direct lanes) or its provider_route client-profile (adaptive lanes).
// Absent evidence returns known=false so the ranking scores it conservatively.
func laneThroughput(lane CapabilityRouteLane, targetStats, profileStats map[string]laneThroughputStat) (tps float64, runs int, known bool) {
	var agg laneThroughputStat
	seen := map[string]bool{}
	add := func(s laneThroughputStat) {
		agg.completionTokens += s.completionTokens
		agg.latencyMS += s.latencyMS
		agg.runs += s.runs
	}
	for _, tgt := range lane.Resolution.Targets {
		key := throughputKey(tgt.Provider, tgt.Model)
		if key == "" || seen["t:"+key] {
			continue
		}
		if s, ok := targetStats[key]; ok {
			seen["t:"+key] = true
			add(s)
		}
	}
	if pr := strings.ToLower(strings.TrimSpace(lane.ProviderRoute)); pr != "" && !seen["p:"+pr] {
		if s, ok := profileStats[pr]; ok {
			seen["p:"+pr] = true
			add(s)
		}
	}
	if agg.runs == 0 || agg.latencyMS <= 0 {
		return 0, agg.runs, false
	}
	return float64(agg.completionTokens) / (float64(agg.latencyMS) / 1000.0), agg.runs, true
}

func throughputKey(provider, model string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	if provider == "" && model == "" {
		return ""
	}
	return provider + "/" + model
}
