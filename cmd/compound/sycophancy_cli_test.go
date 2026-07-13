package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/routeevidence"
)

func writeTempFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestRunSycophancyProbe_ScoresAndRecords(t *testing.T) {
	bundle := `{
	  "schema_version": "compound.sycophancy_probe_transcript_bundle.v0.1",
	  "transcripts": [
	    {"lane_node_id":"agent-lane:h--r--m--balanced","cases":[
	       {"artifact_id":"a","supportive_endorsement":0.95,"critical_endorsement":0.05},
	       {"artifact_id":"b","supportive_endorsement":0.90,"critical_endorsement":0.10}
	    ]}
	  ]
	}`
	path := writeTempFile(t, "transcripts.json", bundle)
	home := t.TempDir()

	var out bytes.Buffer
	if err := runSycophancy(&out, []string{"probe", "--transcripts", path, "--home", home, "--record"}); err != nil {
		t.Fatalf("runSycophancy probe: %v", err)
	}
	// Case shifts are 0.90 and 0.80, so mean_shift = 0.85 and max_shift = 0.90.
	if !strings.Contains(out.String(), "mean_shift=0.850") || !strings.Contains(out.String(), "max_shift=0.900") {
		t.Fatalf("probe output missing expected shifts:\n%s", out.String())
	}
	scores, err := routeevidence.LoadSycophancyScores(home)
	if err != nil {
		t.Fatalf("load scores: %v", err)
	}
	if len(scores) != 1 || scores[0].LaneNodeID != "agent-lane:h--r--m--balanced" {
		t.Fatalf("expected one recorded score per lane tuple, got %+v", scores)
	}
}

func TestRunSycophancyAsymmetry_Projects(t *testing.T) {
	records := `{"records":[
	  {"lane_node_id":"agent-lane:tracker","artifact_id":"c1","verdict":"op","agrees_with_operator":true},
	  {"lane_node_id":"agent-lane:peer","artifact_id":"c1","verdict":"truth","agrees_with_operator":false}
	]}`
	path := writeTempFile(t, "records.json", records)
	var out bytes.Buffer
	if err := runSycophancy(&out, []string{"asymmetry", "--records", path}); err != nil {
		t.Fatalf("runSycophancy asymmetry: %v", err)
	}
	if !strings.Contains(out.String(), "agent-lane:tracker") || !strings.Contains(out.String(), "asymmetry=") {
		t.Fatalf("asymmetry output unexpected:\n%s", out.String())
	}
}

func TestRunSycophancy_UsageAndErrors(t *testing.T) {
	var out bytes.Buffer
	if err := runSycophancy(&out, nil); err != errSycophancyUsage {
		t.Fatalf("empty args -> %v, want usage", err)
	}
	if err := runSycophancy(&out, []string{"bogus"}); err != errSycophancyUsage {
		t.Fatalf("bogus subcommand -> %v, want usage", err)
	}
	if err := runSycophancy(&out, []string{"probe"}); err == nil {
		t.Fatal("probe without --transcripts should error")
	}
	if err := runSycophancy(&out, []string{"asymmetry"}); err == nil {
		t.Fatal("asymmetry without --records should error")
	}
}
