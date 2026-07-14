package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func TestHomeDoctorSurfacesUnknownRootsAndBridgeDebt(t *testing.T) {
	h := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(h, "local", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, "close-plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, "invented-feature-root"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_HOME", h)

	textOut := captureStdout(t, func() { cmdHome([]string{"doctor"}) })
	for _, want := range []string{
		"habitat: DIRTY",
		"habitat_unknown_root: invented-feature-root",
		"habitat_bridge_root: close-plans",
		"verdict: fail",
	} {
		if !strings.Contains(textOut, want) {
			t.Fatalf("home doctor missing %q:\n%s", want, textOut)
		}
	}

	jsonOut := captureStdout(t, func() { cmdHome([]string{"doctor", "--json"}) })
	var report struct {
		Habitat home.HabitatInventory `json:"habitat"`
		Verdict string                `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("parse home doctor JSON: %v\n%s", err, jsonOut)
	}
	if report.Verdict != "fail" || report.Habitat.OK || len(report.Habitat.UnknownRoots) != 1 || report.Habitat.UnknownRoots[0] != "invented-feature-root" || len(report.Habitat.BridgeRoots) != 1 || report.Habitat.BridgeRoots[0] != "close-plans" {
		t.Fatalf("home doctor JSON lost habitat truth: %+v", report)
	}
}
