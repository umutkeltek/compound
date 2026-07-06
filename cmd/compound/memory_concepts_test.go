package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/home"
	"compound/internal/mem"
)

func TestMemoryConceptCLIVerbs(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	fixture := filepath.Join("..", "..", "fixtures", "memory", "topics.json")
	importOut := captureStdout(t, func() {
		cmdMemory([]string{"import-topics", fixture, "--json"})
	})
	if !strings.Contains(importOut, `"topic_count": 3`) || !strings.Contains(importOut, `"edges_created": 4`) {
		t.Fatalf("import-topics output:\n%s", importOut)
	}

	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	entity := "project:cli-concept-fixture"
	captured, err := store.Capture([]mem.Candidate{{
		Entity:         entity,
		Predicate:      "delivery_mode",
		Object:         "automated",
		Confidence:     0.9,
		Derivation:     "deterministic",
		PromotionBasis: candidates.BasisExplicitConfirmation,
		SourceTraceRef: "trace_cli_concept",
		TopicRefs:      []string{"topic:application-platforms"},
	}}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Adjudicate(captured[0].ID, "promote", "", cliTestNow); err != nil {
		t.Fatal(err)
	}

	foldOut := captureStdout(t, func() {
		cmdMemory([]string{"fold", "--entity", entity, "--json"})
	})
	var fold mem.PositionFoldReport
	if err := json.Unmarshal([]byte(foldOut), &fold); err != nil {
		t.Fatalf("parse fold json: %v\n%s", err, foldOut)
	}
	if fold.Count != 1 || fold.Positions[0].ActiveObject != "automated" {
		t.Fatalf("fold report = %+v", fold)
	}

	positionOut := captureStdout(t, func() {
		cmdMemory([]string{"position", entity, "--topic", "topic:application-platforms", "--json"})
	})
	var position mem.PositionQueryResult
	if err := json.Unmarshal([]byte(positionOut), &position); err != nil {
		t.Fatalf("parse position json: %v\n%s", err, positionOut)
	}
	if len(position.Positions) != 1 || position.Positions[0].Position.ActiveObject != "automated" || len(position.Positions[0].Claims) != 1 {
		t.Fatalf("position result = %+v", position)
	}

	recallOut := captureStdout(t, func() {
		cmdMemory([]string{"recall", entity, "--topic", "topic:application-platforms", "--json"})
	})
	var recall mem.RecallResult
	if err := json.Unmarshal([]byte(recallOut), &recall); err != nil {
		t.Fatalf("parse recall json: %v\n%s", err, recallOut)
	}
	if len(recall.Claims) != 1 || recall.Claims[0].Object != "automated" {
		t.Fatalf("recall result = %+v", recall)
	}
}
