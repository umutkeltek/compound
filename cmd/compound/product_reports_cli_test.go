package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/product"
)

func TestProductReportsParseArgs(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	opts, asJSON, err := parseProductReportsArgs([]string{"--product", "product:example-app", "--check", "--json", "--days", "30", "--as-of", "2026-11-01"}, now)
	if err != nil || !asJSON || !opts.Check || opts.ProductID != "product:example-app" || opts.Days != 30 || opts.AsOf.Format("2006-01-02") != "2026-11-01" || !opts.Now.Equal(now) {
		t.Fatalf("opts=%+v json=%v err=%v", opts, asJSON, err)
	}
	for _, bad := range [][]string{{"--days", "0"}, {"--as-of", "soon"}, {"--product"}, {"--product", "--json"}, {"--window", "7"}} {
		if _, _, err := parseProductReportsArgs(bad, now); err == nil {
			t.Fatalf("%v must fail", bad)
		}
	}
}

// Run the real CLI against a fixture home and confirm it reads without writing.
func TestProductReportsCLIJSONAndText(t *testing.T) {
	binary := compoundTestBinary(t)
	homePath := initializedTestHome(t)
	write := func(rel, body string) {
		p := filepath.Join(homePath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ref, _ := json.Marshal(product.Signal{ID: "sig_ref", ProductID: "product:example-app", MetricName: "app_store_connect_review_count", After: 1,
		Window: "daily snapshot 2026-06-01", ObservedAt: "2026-06-01T00:00:00Z", EvidenceRefs: []string{"asc:app:9000000001"}})
	write("ledger/product-signals.jsonl", string(ref)+"\n")
	var series strings.Builder
	for i := 3; i < 20; i++ {
		day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -i).Format("2006-01-02")
		fmt.Fprintf(&series, `{"date":%q,"apple_identifier":"9000000001","units":3,"first_downloads":1,"updates":2,"redownloads":0,"iap_units":0,"other_units":0,"developer_proceeds_usd":0,"final":true}`+"\n", day)
	}
	write("private/metrics/raw/asc-sales-backfill/daily-app-series.jsonl", series.String())

	run := func(args ...string) string {
		cmd := exec.Command(binary, append([]string{"product", "reports"}, args...)...)
		cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	before := dirFingerprint(t, homePath)
	out := run("--json", "--as-of", "2026-11-12", "--check")
	var m product.StoreReports
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != product.StoreReportsSchemaVersion || !m.CheckRun || m.Days != 60 || len(m.Apps) != 1 || m.Apps[0].ProductID != "product:example-app" {
		t.Fatalf("model = %+v", m)
	}
	text := run("--as-of", "2026-11-12", "--product", "example-app")
	if !strings.Contains(text, "# store reports (as of 2026-11-12, 60-day window)") || !strings.Contains(text, "Example App [product:example-app] app 9000000001") || !strings.Contains(text, "raw checks not run") {
		t.Fatalf("text = %s", text)
	}
	cmd := exec.Command(binary, "product", "reports", "--nope")
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	usage, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(usage), "usage: compound product reports") {
		t.Fatalf("bad flag: want usage exit 2, got %v\n%s", err, usage)
	}
	if after := dirFingerprint(t, homePath); after != before {
		t.Fatalf("product reports changed the home:\n%s\n---\n%s", before, after)
	}
	if !strings.Contains(productUsage(), "reports [--product <id>] [--check]") {
		t.Fatal("usage must list reports")
	}
}
