package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"compound/internal/candidates"
	"compound/internal/negatives"
)

func writeCLIFailurePattern(t *testing.T, homePath, id, subject, statement, detail string) {
	t.Helper()
	doc := map[string]any{
		"id":         id,
		"kind":       string(candidates.FailurePattern),
		"subject":    subject,
		"statement":  statement,
		"detail":     detail,
		"confidence": 0.8,
		"verdict":    candidates.Park,
	}
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.FailurePattern))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLIJSONFile(t, filepath.Join(dir, id+".json"), doc)
}

func TestCmdNegativesBuildAndListJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	writeCLIFailurePattern(t, homePath, "fp_cli_negatives", "cli-negatives-check", "cli negatives statement", "cli-negatives-detail")

	emptyOut := captureStdout(t, func() { cmdNegatives([]string{"list", "--json"}) })
	var empty negatives.Projection
	if err := json.Unmarshal([]byte(emptyOut), &empty); err != nil {
		t.Fatalf("parse empty list: %v\n%s", err, emptyOut)
	}
	if len(empty.Records) != 0 {
		t.Fatalf("expected no records before build, got %+v", empty)
	}

	buildOut := captureStdout(t, func() { cmdNegatives([]string{"build", "--json"}) })
	var built negatives.Projection
	if err := json.Unmarshal([]byte(buildOut), &built); err != nil {
		t.Fatalf("parse build: %v\n%s", err, buildOut)
	}
	if len(built.Records) != 1 || built.Records[0].RefID != "fp_cli_negatives" {
		t.Fatalf("expected one built record, got %+v", built)
	}

	listOut := captureStdout(t, func() { cmdNegatives([]string{"list", "--json"}) })
	var list negatives.Projection
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("parse list: %v\n%s", err, listOut)
	}
	if len(list.Records) != 1 || list.Records[0].RefID != "fp_cli_negatives" {
		t.Fatalf("expected the persisted record readable via list, got %+v", list)
	}
}
