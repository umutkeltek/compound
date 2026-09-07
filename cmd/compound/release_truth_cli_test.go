package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
	"compound/internal/product"
	"compound/internal/sourceobs"
)

func TestReleaseTruthMixedStateActualCLI(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	bundle := product.Bundle{SchemaVersion: product.SchemaVersion, Product: product.Product{
		ID: "product:truth-cli", Name: "Truth CLI", OwnerEntity: "project:truth-cli", Surfaces: []product.Surface{{ID: "web", Kind: "web", Ref: "https://truth.invalid"}},
		Channels: []string{"direct", "store"}, Positioning: "Synthetic release proof", PrivacyPosture: "local_first", ReleaseIdentity: product.ReleaseIdentity{Version: "1.0", Build: "10"},
	}, EvidenceRefs: []string{"fixture:truth-cli"}}
	if _, err := product.Apply(homePath, store, bundle, "fixture://truth-cli", now); err != nil {
		t.Fatal(err)
	}
	binary := compoundTestBinary(t)
	run := func() ([]byte, error) {
		t.Helper()
		before := releaseCLIHomeSnapshot(t, homePath)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "product", "loops", "--product", bundle.Product.ID, "--json")
		out, err := cmd.CombinedOutput()
		if !reflect.DeepEqual(before, releaseCLIHomeSnapshot(t, homePath)) {
			t.Fatal("actual CLI mutated home")
		}
		return out, err
	}
	add := func(channel, state, version string, age time.Duration, invalid bool) string {
		t.Helper()
		obs := sourceobs.ReleaseObservation{ProductID: bundle.Product.ID, Channel: channel, Platform: "desktop", ObservedVersion: version, ObservedBuild: "build-" + version, ObservedState: state, ObservedAt: now.Add(-age).Format(time.RFC3339), SourceRevision: "rev-" + version, EvidenceRef: "https://evidence.invalid/" + channel + "/" + version}
		src, err := sourceobs.FromReleaseObservation(obs, now)
		if err != nil {
			t.Fatal(err)
		}
		if invalid {
			src.ID += "_future"
			src.Release.ObservedAt = now.Add(time.Hour).Format(time.RFC3339)
		}
		report, err := sourceobs.Apply(homePath, src, now)
		if err != nil {
			t.Fatal(err)
		}
		return report.CandidateIDs[0]
	}
	assertState := func(status string) product.ReleaseAwareness {
		t.Helper()
		out, err := run()
		if err != nil {
			t.Fatalf("CLI: %v\n%s", err, out)
		}
		var model product.LoopModel
		if err := json.Unmarshal(out, &model); err != nil {
			t.Fatalf("CLI JSON: %v\n%s", err, out)
		}
		if len(model.Products) != 1 || model.Products[0].ReleaseAwareness == nil {
			t.Fatalf("missing release view: %s", out)
		}
		got := *model.Products[0].ReleaseAwareness
		if got.Status != status {
			t.Fatalf("CLI status=%s want %s: %s", got.Status, status, out)
		}
		orient := orientReleaseAwarenessFrom(&got)
		if len(orient.ChannelPlatforms) != len(got.ChannelPlatforms) {
			t.Fatal("orient lost channels")
		}
		raw, err := json.Marshal(orient)
		if err != nil {
			t.Fatal(err)
		}
		var projected struct {
			Channels []struct {
				Status         string `json:"status"`
				CurrentVersion string `json:"current_version"`
			} `json:"channel_platforms"`
		}
		if err := json.Unmarshal(raw, &projected); err != nil {
			t.Fatal(err)
		}
		for i, view := range got.ChannelPlatforms {
			if projected.Channels[i].Status != view.Status || projected.Channels[i].CurrentVersion != view.CurrentVersion {
				t.Fatal("orient lost channel proof state")
			}
		}
		t.Logf("actual CLI status=%s latest=%s evidence=%d home unchanged", got.Status, got.LatestVersion, got.ObservationCount)
		return got
	}
	add("direct", "live", "2.0", 8*24*time.Hour, false)
	add("store", "candidate", "3.0", time.Hour, false)
	partial := assertState("partial")
	for _, view := range partial.ChannelPlatforms {
		if view.CurrentVersion != "" {
			t.Fatalf("CLI made stale/candidate live: %+v", view)
		}
	}
	add("direct", "live", "2.1", 30*time.Minute, false)
	current := assertState("current")
	if !current.OutdatedContract {
		t.Fatal("live contract mismatch hidden")
	}
	invalidID := add("direct", "live", "9.0", time.Minute, true)
	invalid := assertState("invalid")
	if !strings.Contains(strings.Join(invalid.MissingEvidence, "\n"), invalidID) {
		t.Fatal("invalid candidate provenance hidden")
	}
	for _, view := range invalid.ChannelPlatforms {
		if view.Channel == "direct" && view.CurrentVersion != "" {
			t.Fatal("invalid channel claimed current")
		}
	}
	spec, _ := candidates.SpecFor(candidates.RouteObservation)
	if err := os.WriteFile(filepath.Join(homePath, spec.InboxDir, invalidID+".json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run()
	if err == nil || !strings.Contains(string(out), "release evidence") {
		t.Fatalf("corrupt inventory hidden by CLI: %v\n%s", err, out)
	}
}
