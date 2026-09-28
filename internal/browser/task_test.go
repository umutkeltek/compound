package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/policy"
)

func testTime() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }

func taskPolicy() policy.Config {
	cfg := policy.DefaultConfig()
	maxUSD := 0.10
	cfg.StandingGrants = append(cfg.StandingGrants, policy.StandingGrant{
		ID: "synthetic-browser-budget", Capability: "browser.goal.jev",
		MutationClass: "financial", Reversibility: "none", BlastRadius: "account", MaxUSD: &maxUSD,
	})
	return cfg
}

func testTask(home string) TaskRequest {
	return TaskRequest{Home: home, WorkID: "work_browser_test", URL: "https://example.com/", Goal: "Read the heading", ExpectText: "Example Domain", MaxSteps: 2, MaxProviderUSD: 0.10}
}

func testBridge(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJevBridgeHelper$")
	cmd.Env = append(os.Environ(), "COMPOUND_TEST_JEV_HELPER=1")
	return cmd
}

func TestJevUsesCompoundCredentialAndPricingWithoutCallerEnvironment(t *testing.T) {
	home := t.TempDir()
	settings := []byte(`{"schema_version":"compound.browser_settings.v0.2","jev_enabled":true,"provider_model":"jev-1.13.0","provider_max_usd_per_request":0.003}`)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SettingsPath(home), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(home, "secrets", "typesafe", "jev-api-key")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte("synthetic-typesafe-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := capgraph.AddConnectionSurfaceGoverned(home, capgraph.ConnectionSurface{
		NodeID: "tool:browser-jev-typesafe",
		CredentialHandle: capgraph.CredentialHandle{
			Ref: "home-secret://typesafe/jev-api-key", Custodian: "compound-home",
			EnvName: "TYPESAFE_API_KEY",
		},
	}, testTime())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "shadow-key")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.00001")
	t.Setenv("TYPESAFE_MODEL", "other-model")
	req := testTask(home)
	req.MaxProviderUSD = 0.02
	var launched *exec.Cmd
	result, err := runTask(context.Background(), req, taskPolicy(), func(ctx context.Context) *exec.Cmd {
		launched = testBridge(ctx)
		return launched
	})
	if err != nil || result.Status != "verified" || launched == nil {
		t.Fatalf("registered Jev task: result=%+v err=%v", result, err)
	}
	joined := strings.Join(launched.Env, "\n")
	if strings.Contains(joined, "TYPESAFE_API_KEY=") || !strings.Contains(joined, "TYPESAFE_MODEL=jev-1.13.0") {
		t.Fatal("secret reached setup process or pinned provider model missing")
	}
	if strings.Contains(joined, "shadow-key") || strings.Contains(joined, "other-model") {
		t.Fatal("caller provider override leaked into Jev child")
	}
	if os.Getenv("TYPESAFE_API_KEY") != "shadow-key" {
		t.Fatal("credential leaked into caller environment")
	}
}

func TestJevBridgeHelper(t *testing.T) {
	if os.Getenv("COMPOUND_TEST_JEV_HELPER") != "1" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "ready"})
	decoder := json.NewDecoder(os.Stdin)
	var req map[string]any
	if err := decoder.Decode(&req); err != nil {
		os.Exit(2)
	}
	if req["typesafe_api_key"] == "" {
		os.Exit(2)
	}
	mode := os.Getenv("COMPOUND_TEST_JEV_MODE")
	if mode == "error" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "error", "code": "predict_ValueError"})
		os.Exit(1)
	}
	proposal := map[string]any{"event": "proposal", "step": 1, "kind": "done", "role": "terminal", "url": "https://example.com/"}
	if mode == "held" {
		proposal["kind"], proposal["role"] = "fill", "textbox"
	}
	if mode == "link" {
		proposal["kind"], proposal["role"], proposal["target_url"] = "click", "link", "https://example.com/other"
	}
	if mode == "offorigin" {
		proposal["url"] = "https://other.example/"
	}
	_ = json.NewEncoder(os.Stdout).Encode(proposal)
	var reply map[string]bool
	if err := decoder.Decode(&reply); err != nil {
		os.Exit(2)
	}
	if mode == "held" || mode == "offorigin" || mode == "link" {
		if reply["allow"] {
			os.Exit(3)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "result", "status": "held", "steps": 0})
		os.Exit(0)
	}
	if !reply["allow"] {
		os.Exit(3)
	}
	actedStatus := "done"
	if mode == "badstatus" {
		actedStatus = "stale"
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "acted", "step": 1, "status": actedStatus, "url": "https://example.com/"})
	if mode == "hang" {
		time.Sleep(30 * time.Second)
		os.Exit(2)
	}
	status, verified := "verified", true
	if mode == "unverified" {
		status, verified = "unverified", false
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "result", "status": status, "verified": verified, "steps": 1, "url": "https://example.com/"})
	os.Exit(0)
}

func TestJevDisabledAndProfileNeverLaunch(t *testing.T) {
	home := t.TempDir()
	called := 0
	launch := func(ctx context.Context) *exec.Cmd { called++; return testBridge(ctx) }
	result, err := runTask(context.Background(), testTask(home), taskPolicy(), launch)
	if !errors.Is(err, ErrRoutedToPlaywright) || result.Backend != "playwright" || result.Status != "routed" || called != 0 {
		t.Fatalf("disabled route: %+v err=%v launches=%d", result, err, called)
	}
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	req := testTask(home)
	req.ProfileID = "profile_auth"
	result, err = runTask(context.Background(), req, taskPolicy(), launch)
	if !errors.Is(err, ErrRoutedToPlaywright) || result.Backend != "playwright" || result.Status != "routed" || called != 0 {
		t.Fatalf("profile route: %+v err=%v launches=%d", result, err, called)
	}
}

func TestJevSyntheticVerifiedAndHeld(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	for _, tc := range []struct {
		mode, status string
		receipts     int
	}{{"", "verified", 3}, {"unverified", "unverified", 3}, {"held", "held", 2}, {"offorigin", "held", 2}, {"link", "held", 2}} {
		t.Run(fmt.Sprintf("mode_%s", tc.mode), func(t *testing.T) {
			t.Setenv("COMPOUND_TEST_JEV_MODE", tc.mode)
			result, err := runTask(context.Background(), testTask(home), taskPolicy(), testBridge)
			if err != nil || result.Status != tc.status || result.Verified != (tc.status == "verified") || len(result.ReceiptIDs) != tc.receipts || len(result.TransitionIDs) != tc.receipts {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			store, err := eventstore.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range result.ReceiptIDs {
				if _, err := store.Read(id); err != nil {
					t.Fatalf("missing receipt %s: %v", id, err)
				}
			}
			providerBytes, _ := store.Read(result.ReceiptIDs[len(result.ReceiptIDs)-1])
			var providerReceipt envelope.Receipt
			if err := json.Unmarshal(providerBytes.Bytes, &providerReceipt); err != nil || providerReceipt.Cost.USDEstimated == nil || math.Abs(*providerReceipt.Cost.USDEstimated-0.06) > 1e-9 || providerReceipt.Cost.USDActual != nil {
				t.Fatalf("provider cost receipt = %+v, %v", providerReceipt.Cost, err)
			}
			if tc.status == "verified" {
				first, _ := store.Read(result.ReceiptIDs[0])
				var provisional envelope.Receipt
				if err := json.Unmarshal(first.Bytes, &provisional); err != nil || provisional.Outcome != "no_op" {
					t.Fatalf("DONE must be provisional: %+v %v", provisional, err)
				}
			}
			for _, id := range result.TransitionIDs {
				if _, err := store.Read(id); err != nil {
					t.Fatalf("missing transition %s: %v", id, err)
				}
			}
		})
	}
}

func TestJevRejectsInvalidScopeBeforeLaunch(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	called := 0
	launch := func(ctx context.Context) *exec.Cmd { called++; return testBridge(ctx) }
	for _, raw := range []string{"http://example.com/", "https://localhost/", "https://example.com/?token=x", "https://127.0.0.1/"} {
		req := testTask(home)
		req.URL = raw
		if _, err := runTask(context.Background(), req, taskPolicy(), launch); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if called != 0 {
		t.Fatalf("unsafe requests launched %d times", called)
	}
}

func TestJevMissingCredentialNeverLaunches(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "")
	called := 0
	launch := func(ctx context.Context) *exec.Cmd { called++; return testBridge(ctx) }
	if _, err := runTask(context.Background(), testTask(home), taskPolicy(), launch); err == nil || called != 0 {
		t.Fatalf("missing key should stop before launch: err=%v launches=%d", err, called)
	}
}

func TestJevCorruptRegistryNeverFallsBackToShellKey(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "registries"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "registries", "capability-connection-surfaces.jsonl"), []byte("{torn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic-shell-key")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.003")
	called := 0
	_, err := runTask(context.Background(), testTask(home), taskPolicy(), func(ctx context.Context) *exec.Cmd {
		called++
		return testBridge(ctx)
	})
	if err == nil || called != 0 {
		t.Fatalf("corrupt registry used shell fallback: err=%v launches=%d", err, called)
	}
}

func TestJevProviderCostPreflightStopsBeforeLaunch(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	called := 0
	launch := func(ctx context.Context) *exec.Cmd { called++; return testBridge(ctx) }
	for _, tc := range []struct {
		price, model string
		budget       float64
	}{
		{"", "", 0.1},
		{"0.000001", "", 0.1},
		{"0.05", "", 0.1},
		{"0.01", "other-model", 0.1},
		{"0.01", "", 0},
	} {
		t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", tc.price)
		t.Setenv("TYPESAFE_MODEL", tc.model)
		req := testTask(home)
		req.MaxProviderUSD = tc.budget
		if _, err := runTask(context.Background(), req, taskPolicy(), launch); err == nil {
			t.Fatalf("accepted provider cost contract %+v", tc)
		}
	}
	if called != 0 {
		t.Fatalf("provider preflight launched bridge %d times", called)
	}
}

func TestJevProviderSpendNeedsPolicyGrant(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	called := 0
	launch := func(ctx context.Context) *exec.Cmd { called++; return testBridge(ctx) }
	result, err := runTask(context.Background(), testTask(home), policy.DefaultConfig(), launch)
	if err == nil || result.Status != "held" || called != 0 || len(result.ReceiptIDs) != 1 {
		t.Fatalf("provider spend escaped policy: result=%+v err=%v launches=%d", result, err, called)
	}
}

func TestJevPolicyHoldStopsBeforeBrowserLaunch(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	cfg := policy.DefaultConfig()
	cfg.ClassOutcome["financial"] = policy.ClassRule{Outcome: "hold"}
	called := 0
	launch := func(ctx context.Context) *exec.Cmd { called++; return testBridge(ctx) }
	if _, err := runTask(context.Background(), testTask(home), cfg, launch); err == nil || called != 0 {
		t.Fatalf("policy hold should stop before launch: err=%v launches=%d", err, called)
	}
}

func TestJevBridgeErrorHasFailedReceipt(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	t.Setenv("COMPOUND_TEST_JEV_MODE", "error")
	result, err := runTask(context.Background(), testTask(home), taskPolicy(), testBridge)
	if err == nil || result.Status != "failed" || result.Verified || len(result.ReceiptIDs) != 1 || len(result.TransitionIDs) != 1 {
		t.Fatalf("failed run = %+v, %v", result, err)
	}
	store, err := eventstore.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(result.ReceiptIDs[0]); err != nil {
		t.Fatal(err)
	}
}

func TestJevUnexpectedActStatusFails(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	t.Setenv("COMPOUND_TEST_JEV_MODE", "badstatus")
	result, err := runTask(context.Background(), testTask(home), taskPolicy(), testBridge)
	if err == nil || result.Status != "failed" || result.Verified || len(result.ReceiptIDs) != 1 {
		t.Fatalf("unexpected act status = %+v, %v", result, err)
	}
	store, err := eventstore.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Read(result.ReceiptIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	var receipt envelope.Receipt
	if err := json.Unmarshal(record.Bytes, &receipt); err != nil || receipt.Outcome != "failed" {
		t.Fatalf("receipt = %+v, %v", receipt, err)
	}
}

func TestJevCancellationPreservesPartialProgress(t *testing.T) {
	home := t.TempDir()
	if _, err := SetJevEnabled(home, "work_browser_test", true, policy.DefaultConfig(), testTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TYPESAFE_PROVIDER_MAX_USD_PER_REQUEST", "0.01")
	t.Setenv("COMPOUND_TEST_JEV_MODE", "hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { time.Sleep(time.Second); cancel() }()
	result, err := runTask(ctx, testTask(home), taskPolicy(), testBridge)
	if err == nil || result.Status != "failed" || result.Steps != 1 || len(result.ReceiptIDs) != 2 || len(result.TransitionIDs) != 2 {
		t.Fatalf("cancelled result = %+v, %v", result, err)
	}
}

func TestJevProxyRejectsForeignAndPrivateTargets(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	script := `import runpy, socket, threading
ns = runpy.run_path("jev_bridge.py")
proxy = ns["ScopedProxy"]("example.com")
thread = threading.Thread(target=proxy.serve_forever, daemon=True)
thread.start()
def request(authority):
    client = socket.socket()
    client.settimeout(3)
    client.connect(proxy.server_address)
    client.sendall(("CONNECT " + authority + " HTTP/1.1\r\n\r\n").encode())
    answer = client.recv(128)
    client.close()
    return answer
assert b"403" in request("other.example:443")
assert b"403" in request("127.0.0.1:443")
original = socket.getaddrinfo
def private_dns(host, port, **kwargs):
    return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("127.0.0.1", 443))]
socket.getaddrinfo = private_dns
try:
    assert b"403" in request("example.com:443")
finally:
    socket.getaddrinfo = original
    proxy.shutdown()
    proxy.server_close()
thread.join(timeout=2)
print("proxy scope held")`
	cmd := exec.Command(python, "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("proxy scope: %v: %s", err, output)
	}
}
