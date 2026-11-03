package knowledge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// TestMain switches the automatic candidate producers ON for this package's
// fixtures, which predate the 2026-11-03 opt-in gate and assert candidate
// fan-out. The gate itself is pinned by TestKnowledgeProducersOffByDefault.
func TestMain(m *testing.M) {
	if _, set := os.LookupEnv(candidates.ProducersEnv); !set {
		_ = os.Setenv(candidates.ProducersEnv, "1")
	}
	os.Exit(m.Run())
}

// With the producers off (the production default), knowledge import still
// records evidence and the extraction request but files no metadata
// candidate, and extraction does not run the model or file a candidate; the
// request stays pending for a later opt-in run.
func TestKnowledgeProducersOffByDefault(t *testing.T) {
	t.Setenv(candidates.ProducersEnv, "")
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ImportFile(homePath, store, fixturePath("arxiv-export.json"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.EvidenceIDs) != 3 || len(report.ExtractionIDs) != 3 {
		t.Fatalf("evidence and extraction requests must still land: %+v", report)
	}
	if len(report.CandidateIDs) != 0 || report.ProducerNote != candidates.ProducerDisabledNote {
		t.Fatalf("metadata candidates must be skipped with a note, got ids=%v note=%q", report.CandidateIDs, report.ProducerNote)
	}
	all, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.Qualifiers["source"] == "knowledge_import" || c.Qualifiers["source"] == "knowledge_extraction" {
			t.Fatalf("producer off but candidate filed: %+v", c)
		}
	}

	fixtures := t.TempDir()
	writeExtractionFixture(t, fixtures, report.ExtractionIDs[0], report.EvidenceIDs[0])
	extract, err := Extract(homePath, store, ExtractOptions{ItemID: report.ExtractionIDs[0], FixturesDir: fixtures}, testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if extract.Processed != 0 || len(extract.CandidateIDs) != 0 || extract.ProducerNote == "" {
		t.Fatalf("extraction must not run with producers off: %+v", extract)
	}
	var req ExtractionRequest
	readJSON(t, filepath.Join(homePath, "inbox", "knowledge-extractions", report.ExtractionIDs[0]+".json"), &req)
	if req.State != ExtractionStatePending {
		t.Fatalf("request must stay pending for a later opt-in run, got %q", req.State)
	}
}
