package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/product"
)

func TestProductGrowthParseArgs(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	opts, asJSON, err := parseProductGrowthArgs([]string{"--product", "product:alpha", "--json", "--as-of", "2026-11-01", "--window", "7", "--series-days", "60", "--stale-days", "3"}, now)
	if err != nil || !asJSON || opts.ProductID != "product:alpha" || opts.WindowDays != 7 || opts.SeriesDays != 60 || opts.StaleDays != 3 || opts.AsOf.Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("opts=%+v json=%v err=%v", opts, asJSON, err)
	}
	for _, bad := range [][]string{{"--as-of", "soon"}, {"--window", "0"}, {"--product"}, {"--nope"}} {
		if _, _, err := parseProductGrowthArgs(bad, now); err == nil {
			t.Fatalf("%v must fail", bad)
		}
	}
}

// Run the real CLI against a fixture home and confirm it reads without writing.
func TestProductGrowthCLIJSONAndText(t *testing.T) {
	binary := compoundTestBinary(t)
	homePath := initializedTestHome(t)
	ledger := filepath.Join(homePath, "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 5; i++ {
		day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -i).Format("2006-01-02")
		line, _ := json.Marshal(product.Signal{ID: fmt.Sprintf("sig_%d", i), ProductID: "product:gamma_web", MetricName: "gsc_clicks", After: 4, Window: "snapshot " + day, ObservedAt: "2026-11-12T00:00:00Z"})
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(ledger, "product-signals.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := exec.Command(binary, append([]string{"product", "growth"}, args...)...)
		cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	before := dirFingerprint(t, homePath)
	out := run("--json", "--as-of", "2026-11-12", "--product", "gamma-web")
	var g product.Growth
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(g.Apps) != 1 || g.Apps[0].ProductID != "product:gamma_web" || *g.Apps[0].Kpis["web_clicks"].Value != 20 {
		t.Fatalf("apps = %+v", g.Apps)
	}
	text := run("--as-of", "2026-11-12")
	if !strings.Contains(text, "Gamma Web [product:gamma_web]") || !strings.Contains(text, "focus measure") {
		t.Fatalf("text = %s", text)
	}
	if after := dirFingerprint(t, homePath); after != before {
		t.Fatalf("product growth changed the home:\n%s\n---\n%s", before, after)
	}
	if !strings.Contains(productUsage(), "growth [--product <id>]") {
		t.Fatal("usage must list growth")
	}
}

func dirFingerprint(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		// The memory reader's shared flock creates an empty lock/mem.lock on first
		// read (same as `product where`); it carries no data.
		if err == nil && !info.IsDir() && !strings.Contains(p, string(filepath.Separator)+"lock"+string(filepath.Separator)) {
			lines = append(lines, fmt.Sprintf("%s %d %d", p, info.Size(), info.ModTime().UnixNano()))
		}
		return nil
	})
	return strings.Join(lines, "\n")
}
