package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"compound/internal/home"
	"compound/internal/product"
)

func TestProductLoopsJSONDecodesModel(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdProduct([]string{
			"signal",
			"--gsc-report", filepath.Join("..", "..", "fixtures", "product", "gsc-report.json"),
			"--product", "product:cli-loop",
			"--site", "example.com",
			"--metric", "clicks",
		})
	})
	out := captureStdout(t, func() {
		cmdProduct([]string{"loops", "--json", "--product", "product:cli-loop", "--recent", "2", "--stale-days", "14"})
	})
	var model product.LoopModel
	if err := json.Unmarshal([]byte(out), &model); err != nil {
		t.Fatalf("decode loops JSON: %v\n%s", err, out)
	}
	if err := model.Validate(); err != nil {
		t.Fatalf("validate loops JSON: %v\n%s", err, out)
	}
	if len(model.Products) != 1 || model.Products[0].ProductID != "product:cli-loop" {
		t.Fatalf("model products = %+v", model.Products)
	}
	signals := model.Products[0].SignalsByMetric["gsc_clicks"]
	if len(signals) != 1 || signals[0].Provider != "google_search_console" {
		t.Fatalf("loop signals = %+v", signals)
	}
}
