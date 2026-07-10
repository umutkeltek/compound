package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"compound/internal/capgraph"
	"compound/internal/home"
	"compound/internal/routeevidence"
)

func TestCapabilityResolveComposesOwnedSurfaceAndRouteWithoutMutation(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(capgraph.Node{ID: "tool:fixture-extractor", Kind: "tool", Name: "fixture extractor", Triggers: []string{"extract", "metrics"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := routeevidence.ImportModelCapabilitiesYAML(homePath, filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml"), cliTestNow); err != nil {
		t.Fatal(err)
	}

	before := snapshotCapabilityResolveHome(t, homePath)
	out := captureStdout(t, func() {
		cmdCapability([]string{"resolve", "--harness", "codex", "--json", "extract metrics from the export"})
	})
	after := snapshotCapabilityResolveHome(t, homePath)
	if before != after {
		t.Fatalf("read-only capability resolve mutated COMPOUND_HOME\nbefore=%s\nafter=%s", before, after)
	}

	var report capabilityResolutionReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse capability resolve output: %v\n%s", err, out)
	}
	if report.SchemaVersion != capabilityResolutionSchema || report.Status != "ok" {
		t.Fatalf("unexpected resolution header: %+v", report)
	}
	if report.Classification.CapabilityClass != "classify-extract" {
		t.Fatalf("wrong classification: %+v", report.Classification)
	}
	if report.CapabilitySurface.AuthorityRef != capgraph.CapabilityGraphAuthorityRef || report.CapabilitySurface.Harness != "codex" {
		t.Fatalf("capability authority/harness missing: %+v", report.CapabilitySurface)
	}
	if report.CapabilitySurface.OfferCount < 1 || report.CapabilitySurface.Offers[0].Node.ID != "tool:fixture-extractor" {
		t.Fatalf("expected fixture offer: %+v", report.CapabilitySurface)
	}
	if report.RoutePlan.AuthorityRef != routeevidence.ModelCapabilitiesAuthorityRef || report.RoutePlan.SourceAuthorityID == "" || report.RoutePlan.SourceRevision == "" {
		t.Fatalf("model-router authority proof missing: %+v", report.RoutePlan)
	}
	if len(report.RoutePlan.Chain) != 3 {
		t.Fatalf("unexpected route chain: %+v", report.RoutePlan.Chain)
	}
	if report.AuthorityRefs[0] != capgraph.CapabilityGraphAuthorityRef || report.AuthorityRefs[1] != routeevidence.ModelCapabilitiesAuthorityRef {
		t.Fatalf("authority refs = %v", report.AuthorityRefs)
	}
}

func TestCapabilityResolveAppliesPrivacyAtCompoundRouteOwner(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(capgraph.Node{ID: "tool:fixture-extractor", Kind: "tool", Name: "fixture extractor", Triggers: []string{"extract"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := routeevidence.ImportModelCapabilitiesYAML(homePath, filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml"), cliTestNow); err != nil {
		t.Fatal(err)
	}

	report, err := buildCapabilityResolutionReport(store, homePath, "extract private records", "", true, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.RoutePlan.Chain) != 1 || report.RoutePlan.Chain[0].Tier != "local-batch" {
		t.Fatalf("privacy route must contain only Compound-selected local lanes: %+v", report.RoutePlan)
	}
	if len(report.RoutePlan.Omitted) != 2 {
		t.Fatalf("privacy route must report omitted non-local lanes: %+v", report.RoutePlan.Omitted)
	}
}

func snapshotCapabilityResolveHome(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
