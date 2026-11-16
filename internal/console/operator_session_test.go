package console

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func operatorTestState(t *testing.T) *OperatorSessionBroker {
	t.Helper()
	return &OperatorSessionBroker{home: "/synthetic/compound-home", key: strings.Repeat("a", 64), instance: strings.Repeat("b", 64), codes: make(map[[32]byte]time.Time), sessions: make(map[[32]byte]operatorSession), now: time.Now}
}

func operatorTestCall(t *testing.T, b *OperatorSessionBroker, operation string, patch func(*operatorSessionRequest)) (int, operatorSessionReply) {
	t.Helper()
	in := operatorSessionRequest{Operation: operation, Home: b.home, Origin: OperatorOrigin, Instance: b.instance}
	if patch != nil {
		patch(&in)
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://console.invalid/operator-session", bytes.NewReader(body))
	r.Header.Set("X-Compound-Operator-Broker", b.key)
	w := httptest.NewRecorder()
	b.serveHTTP(w, r)
	var out operatorSessionReply
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, out
}

func operatorTestPair(t *testing.T, b *OperatorSessionBroker) operatorSessionReply {
	t.Helper()
	status, ticket := operatorTestCall(t, b, "issue", nil)
	if status != 200 {
		t.Fatalf("issue: %d", status)
	}
	status, session := operatorTestCall(t, b, "pair", func(in *operatorSessionRequest) { in.Code = ticket.Code })
	if status != 200 {
		t.Fatalf("pair: %d", status)
	}
	return session
}

func TestOperatorSessionOneUseAcrossConcurrentWorkers(t *testing.T) {
	b := operatorTestState(t)
	_, ticket := operatorTestCall(t, b, "issue", nil)
	var wg sync.WaitGroup
	statuses := make(chan int, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := operatorTestCall(t, b, "pair", func(in *operatorSessionRequest) { in.Code = ticket.Code })
			statuses <- status
		}()
	}
	wg.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == 200 {
			successes++
		} else if status != 403 {
			t.Fatalf("unexpected status: %d", status)
		}
	}
	if successes != 1 || len(b.sessions) != 1 {
		t.Fatalf("one code authorized %d workers and %d sessions", successes, len(b.sessions))
	}
}

func TestOperatorSessionCurrentHomeOriginInstanceCSRFAndRevocation(t *testing.T) {
	b := operatorTestState(t)
	session := operatorTestPair(t, b)
	base := func(in *operatorSessionRequest) { in.Session = session.Session; in.CSRF = session.CSRF }
	for name, change := range map[string]func(*operatorSessionRequest){
		"home":     func(in *operatorSessionRequest) { in.Home = "/synthetic/other-home" },
		"origin":   func(in *operatorSessionRequest) { in.Origin = "http://console.invalid" },
		"instance": func(in *operatorSessionRequest) { in.Instance = strings.Repeat("c", 64) },
		"csrf":     func(in *operatorSessionRequest) { in.CSRF = strings.Repeat("d", 64) },
		"session":  func(in *operatorSessionRequest) { in.Session = strings.Repeat("e", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := operatorTestCall(t, b, "authorize", func(in *operatorSessionRequest) { base(in); change(in) })
			if status != 403 {
				t.Fatalf("drift accepted: %d", status)
			}
		})
	}
	if status, _ := operatorTestCall(t, b, "authorize", base); status != 200 {
		t.Fatalf("valid session rejected: %d", status)
	}
	if status, _ := operatorTestCall(t, b, "revoke", base); status != 200 {
		t.Fatalf("logout rejected: %d", status)
	}
	if status, _ := operatorTestCall(t, b, "authorize", base); status != 403 {
		t.Fatalf("revoked session accepted: %d", status)
	}
	_ = operatorTestPair(t, b) // logout does not require restarting for new codes
}

func TestOperatorSessionAbsoluteExpiryAndBoundedOutstandingState(t *testing.T) {
	b := operatorTestState(t)
	now := time.Now()
	b.now = func() time.Time { return now }
	_, ticket := operatorTestCall(t, b, "issue", nil)
	now = now.Add(OperatorPairTTL)
	if status, _ := operatorTestCall(t, b, "pair", func(in *operatorSessionRequest) { in.Code = ticket.Code }); status != 403 {
		t.Fatalf("expired code accepted: %d", status)
	}
	session := operatorTestPair(t, b)
	now = now.Add(OperatorSessionTTL)
	if status, _ := operatorTestCall(t, b, "authorize", func(in *operatorSessionRequest) { in.Session = session.Session; in.CSRF = session.CSRF }); status != 403 {
		t.Fatalf("expired session accepted: %d", status)
	}
	for range operatorMaxCodes {
		if status, _ := operatorTestCall(t, b, "issue", nil); status != 200 {
			t.Fatalf("issue: %d", status)
		}
	}
	if status, _ := operatorTestCall(t, b, "issue", nil); status != 429 {
		t.Fatalf("unbounded codes: %d", status)
	}
	now = now.Add(OperatorPairTTL)
	for range operatorMaxSessions {
		_ = operatorTestPair(t, b)
	}
	_, ticket = operatorTestCall(t, b, "issue", nil)
	if status, _ := operatorTestCall(t, b, "pair", func(in *operatorSessionRequest) { in.Code = ticket.Code }); status != 429 {
		t.Fatalf("unbounded sessions: %d", status)
	}
}

func TestOperatorSessionTransportRejectsMalformedAndUnauthenticatedRequests(t *testing.T) {
	b := operatorTestState(t)
	for _, body := range []string{`{}`, `null`, `{"operation":"issue","extra":"untrusted"}`, strings.Repeat("x", 9000), `{} {}`} {
		r := httptest.NewRequest("POST", "http://console.invalid/operator-session", strings.NewReader(body))
		w := httptest.NewRecorder()
		b.serveHTTP(w, r)
		if w.Code < 400 || strings.Contains(w.Body.String(), b.key) {
			t.Fatalf("malformed request accepted/disclosed key: %d", w.Code)
		}
	}
	body, _ := json.Marshal(operatorSessionRequest{Operation: "pair", Home: b.home, Origin: OperatorOrigin, Instance: b.instance})
	r := httptest.NewRequest("POST", "http://console.invalid/operator-session", bytes.NewReader(body))
	w := httptest.NewRecorder()
	b.serveHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("missing broker credential accepted: %d", w.Code)
	}
	b.Close()
	if status, _ := operatorTestCall(t, b, "issue", nil); status != 503 {
		t.Fatalf("closed broker minted code: %d", status)
	}
}

func TestOperatorSessionUnixOwnerRestartAndStaleSocketRecovery(t *testing.T) {
	// Short private test runtime avoids the Unix socket path length ceiling.
	dir, err := os.MkdirTemp("/tmp", "cc-session-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	b, err := startOperatorSessionBrokerAt("/synthetic/compound-home", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := operatorPrivatePath(dir, 0o700, true); err != nil {
		t.Fatal(err)
	}
	if err := operatorPrivatePath(b.socket, 0o600, false); err != nil {
		t.Fatal(err)
	}
	if _, err := startOperatorSessionBrokerAt(b.home, dir); err == nil {
		t.Fatal("second owner replaced live socket")
	}
	client := func() *http.Client {
		return &http.Client{Timeout: time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", b.socket)
		}}}
	}
	body, _ := json.Marshal(operatorSessionRequest{Operation: "issue", Home: b.home, Origin: OperatorOrigin})
	response, err := client().Post("http://console.invalid/operator-session", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || strings.Contains(string(data), b.key) {
		t.Fatalf("native client failed/leaked server key: %d", response.StatusCode)
	}
	session := operatorTestPair(t, b)
	oldInstance := b.instance
	b.Close()
	b2, err := startOperatorSessionBrokerAt(b.home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if b2.instance == oldInstance {
		t.Fatal("restart reused instance")
	}
	if status, _ := operatorTestCall(t, b2, "authorize", func(in *operatorSessionRequest) { in.Session = session.Session; in.CSRF = session.CSRF }); status != 403 {
		t.Fatalf("restart reused browser session: %d", status)
	}
}

func TestOperatorSessionRefusesUnsafeRuntimePathsWithoutDeletingThem(t *testing.T) {
	for _, kind := range []string{"permissive", "symlink", "foreign-socket-path"} {
		t.Run(kind, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "cc-unsafe-test-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			dir := filepath.Join(root, "runtime")
			switch kind {
			case "permissive":
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(root, dir); err != nil {
					t.Fatal(err)
				}
			case "foreign-socket-path":
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "operator.sock"), []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := startOperatorSessionBrokerAt("/synthetic/compound-home", dir); err == nil {
				t.Fatal("unsafe runtime accepted")
			}
			if _, err := os.Lstat(dir); err != nil {
				t.Fatal("refusal removed runtime")
			}
			if kind == "foreign-socket-path" {
				if data, err := os.ReadFile(filepath.Join(dir, "operator.sock")); err != nil || string(data) != "preserve" {
					t.Fatal("refusal deleted foreign file")
				}
			}
		})
	}
}

func TestOperatorSessionLaunchRequiresExistingValidatedMutationPosture(t *testing.T) {
	if _, err := EnableOperatorSession(LaunchPlan{MutationsEnabled: true, Lifecycle: LifecycleDev, Env: map[string]string{"COMPOUND_HOME": "/synthetic/home"}}); err == nil {
		t.Fatal("unvalidated plan enabled browser mutation")
	}
	for _, key := range []string{OperatorSessionEnv, OperatorSocketEnv, OperatorBrokerKeyEnv, OperatorInstanceEnv} {
		t.Setenv(key, "ambient-must-not-survive")
	}
	for _, entry := range LaunchEnv(LaunchPlan{}) {
		if strings.Contains(entry, "ambient-must-not-survive") {
			t.Fatal("ambient broker authority entered child")
		}
	}
	data, err := json.Marshal(LaunchPlan{Env: map[string]string{OperatorBrokerKeyEnv: "private-key", OperatorSocketEnv: "private-socket", OperatorInstanceEnv: "private-instance"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-") {
		t.Fatalf("launch JSON leaked broker material: %s", data)
	}
}

func TestOperatorSessionOnlyLaunchNeverNeedsOrInheritsLegacyCapability(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COMPOUND_HOME", filepath.Join(root, "home"))
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CapabilityEnv, "")
	plan, err := BuildOperatorSessionLaunchPlan(LifecycleDev, root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.MutationsEnabled || !plan.OperatorSessionEnabled || plan.CapabilityRequired || plan.Env[MutationsEnv] != "1" {
		t.Fatal("session-only launch changed extension posture")
	}
	t.Setenv(CapabilityEnv, "ambient-must-not-enter-session-only-launch")
	for _, entry := range LaunchEnv(plan) {
		if strings.HasPrefix(entry, CapabilityEnv+"=") {
			t.Fatal("session-only launch inherited legacy capability")
		}
	}
	if _, err := BuildOperatorSessionLaunchPlan(LifecycleBuild, root, false, true); err == nil {
		t.Fatal("build enabled operator session")
	}
	t.Setenv("COMPOUND_HOME", " ")
	if _, err := BuildOperatorSessionLaunchPlan(LifecycleDev, root, false, true); err == nil {
		t.Fatal("blank home enabled operator session")
	}
}

func TestOperatorSessionDiscoveryIndependentOfShellTemporaryDirectory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	first, err := operatorRuntimeDirectory("/synthetic/operator-home")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	second, err := operatorRuntimeDirectory("/synthetic/operator-home")
	if err != nil {
		t.Fatal(err)
	}
	other, err := operatorRuntimeDirectory("/synthetic/other-home")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == other {
		t.Fatal("broker discovery depends on service TMPDIR or aliases another home")
	}
}
