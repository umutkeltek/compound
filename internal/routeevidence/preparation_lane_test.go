package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/laneselect"
)

var prepNow = time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

// seedThroughput appends `runs` completed route-evidence records for provider/model,
// each carrying completionTokens over latencyMS, straight to the ledger jsonl.
func seedThroughput(t *testing.T, home, provider, model string, completionTokens int, latencyMS int64, runs int) {
	t.Helper()
	ledger := ExecutionRouteEvidenceLedgerPath(home)
	if err := os.MkdirAll(filepath.Dir(ledger), 0o755); err != nil {
		t.Fatalf("mkdir ledger: %v", err)
	}
	f, err := os.OpenFile(ledger, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer f.Close()
	for i := 0; i < runs; i++ {
		ev, err := Build(BuildInput{
			CreatedAt:     prepNow.Add(time.Duration(i) * time.Minute),
			RequestID:     provider + "/" + model + "/seed/" + time.Duration(i).String(),
			ClientProfile: "compound.prep.seed",
			Protocol:      "openai",
			Provider:      provider,
			Model:         model,
			Usage:         Usage{CompletionTokens: completionTokens, TotalTokens: completionTokens},
			LatencyMS:     latencyMS,
			Status:        "completed",
		})
		if err != nil {
			t.Fatalf("build seed evidence: %v", err)
		}
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal seed evidence: %v", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatalf("write seed evidence: %v", err)
		}
	}
}

func laneScoreByID(r laneselect.PreparationLaneRanking, id string) (laneselect.PreparationLaneScore, bool) {
	for _, c := range r.Candidates {
		if c.Lane == id {
			return c, true
		}
	}
	return laneselect.PreparationLaneScore{}, false
}

// Adapter: reads the Compound model-router authority + route-evidence throughput and produces
// a ranking. Proves observed tokens/sec (not the curated routing_defaults order) picks the winner.
func TestPlanPreparationLaneReadsRegistryAndThroughput(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, prepNow); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
	// scout/chat is fast (600 t/s); mac/qwen is slow (5 t/s). auto/extract is adaptive with no
	// throughput evidence -> unknown. Floor 50 t/s.
	seedThroughput(t, home, "scout", "scout/chat", 600, 1000, 3)
	seedThroughput(t, home, "lmstudio", "qwen-fixture-served", 5, 1000, 3)

	got, err := PlanPreparationLane(home, "classify-extract", PreparationLaneOptions{FloorTokensPerSecond: 50}, prepNow)
	if err != nil {
		t.Fatalf("plan preparation lane: %v", err)
	}
	if got.Winner == nil {
		t.Fatalf("expected a winner: %+v", got)
	}
	// auto/extract is routing_defaults[0] (primary) but has unknown throughput; scout/chat is
	// capable + measured-fast -> speed must promote scout/chat over the curated top lane.
	if got.Winner.Lane != "scout/chat" {
		t.Fatalf("measured-fast lane must win over unknown-throughput curated top; winner=%s candidates=%+v", got.Winner.Lane, got.Candidates)
	}
	auto, ok := laneScoreByID(got, "auto/extract")
	if !ok || auto.MeetsFloor {
		t.Fatalf("adaptive lane with no evidence must be unknown/below-floor: %+v", auto)
	}
	scout, _ := laneScoreByID(got, "scout/chat")
	if scout.ThroughputRuns != 3 || scout.TokensPerSecond < 50 {
		t.Fatalf("scout throughput must aggregate the seeded runs: %+v", scout)
	}
}

// A non-preparation class is refused by the adapter (defense in depth; the dispatch surface
// also gates on IsPreparationClass).
func TestPlanPreparationLaneRefusesNonPreparationClass(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, prepNow); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
	if _, err := PlanPreparationLane(home, "judge-critique", PreparationLaneOptions{}, prepNow); err == nil {
		t.Fatalf("judge-critique is not a preparation class; adapter must refuse")
	}
}
