package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/routeevidence"
)

func TestRoutePromotionCLIHappyPath(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdRoute([]string{"import-model-capabilities", filepath.Join("..", "..", "fixtures", "panel", "model-capabilities.yaml")})
	})
	importOut := captureStdout(t, func() {
		cmdRoute([]string{"import-promotion-criteria", filepath.Join("..", "..", "fixtures", "route", "promotion-criteria.yaml")})
	})
	if !strings.Contains(importOut, "# route import-promotion-criteria") || !strings.Contains(importOut, "- entries: 3") || !strings.Contains(importOut, "- verdict: compound_primary") {
		t.Fatalf("bad import output:\n%s", importOut)
	}
	laneID := "panel/free-judge-tripwire"
	recordCLIPromotionRows(t, homePath, laneID, 20, 4*time.Hour)

	out := captureStdout(t, func() {
		cmdRoute([]string{"promotion-review", "--lane", laneID, "--json"})
	})
	var reviews []routeevidence.PromotionReview
	if err := json.Unmarshal([]byte(out), &reviews); err != nil {
		t.Fatalf("parse promotion review JSON: %v\n%s", err, out)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %+v, want one", reviews)
	}
	review := reviews[0]
	if review.LaneID != laneID || review.CurrentClaimLevel != "capable" || review.Recommendation != "promote" || review.RecommendedClaimLevel != "primary" || review.Tallies.Samples != 20 {
		t.Fatalf("bad promotion review: %+v", review)
	}

	human := captureStdout(t, func() {
		cmdRoute([]string{"promotion-review", "--lane", laneID})
	})
	for _, want := range []string{"# route promotion-review", "claim=capable", "samples=20", "recommendation=promote", "review_artifacts_only_no_authority_mutation"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human output missing %q:\n%s", want, human)
		}
	}
}

func recordCLIPromotionRows(t *testing.T, homePath, laneID string, count int, spacing time.Duration) {
	t.Helper()
	base := cliTestNow
	for i := 0; i < count; i++ {
		ev, err := routeevidence.Build(routeevidence.BuildInput{
			CreatedAt:     base.Add(time.Duration(i) * spacing),
			RequestID:     fmt.Sprintf("cli-promotion:%s:%d", strings.ReplaceAll(laneID, "/", "_"), i),
			ClientProfile: "compound.panel.judge",
			Protocol:      "panel-lane",
			Provider:      "compound-route-plan",
			Model:         laneID,
			Status:        "completed",
			EvidenceRefs:  []string{routeevidence.ModelCapabilitiesAuthorityRef},
		})
		if err != nil {
			t.Fatalf("build route evidence: %v", err)
		}
		if err := routeevidence.RecordEvidence(homePath, ev); err != nil {
			t.Fatalf("record route evidence: %v", err)
		}
	}
}
