package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/home"
)

// The retained failed proof route restores the original owned report through the EXISTING
// --out custody: the same rooted, CAS-guarded write the battery uses. A recovery that wrote
// the report by another route would not be the same evidence projection.
func TestProofCheckRestoreRetainedReportUsesTheExistingOutCustody(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, home.HomeKindGlobal, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schema_version":"compound.proof_check_report.v0.6","status":"failed","steps":[]}`)
	ref, err := artifacts.Put(body)
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "failed-proof.json")
	report, err := proofCheckRestoreRetainedReport(homePath, ref, outPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" {
		t.Fatalf("the restored report is not the retained failed verdict: %+v", report)
	}
	published, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(published, body) {
		t.Fatal("the restored --out bytes differ from the immutable owned artifact")
	}
	if _, err := proofCheckRestoreRetainedReport(homePath, ref, outPath); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, body) {
		t.Fatal("a repeated restore changed the restored --out bytes")
	}
}
