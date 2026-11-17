package operatornotice

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writePingURL(t *testing.T, homePath, url string, mode os.FileMode) {
	t.Helper()
	path := DeadmanPingURLPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(url+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // umask must not soften the fixture
		t.Fatal(err)
	}
}

type pingRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (p *pingRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.paths = append(p.paths, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (p *pingRecorder) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

func runDeadman(t *testing.T, homePath string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runDeadmanCLI(homePath, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestDeadmanNotConfiguredMakesNoRequest(t *testing.T) {
	homePath := t.TempDir()
	var rec pingRecorder
	rec.server(t)
	code, out, errOut := runDeadman(t, homePath, "ping")
	if code != 0 || strings.TrimSpace(out) != "deadman: not configured" {
		t.Fatalf("exit %d out=%q err=%q", code, out, errOut)
	}
	if code, _, _ := runDeadman(t, homePath, "ping", "--fail"); code != 0 {
		t.Fatalf("--fail without a file must also be a quiet no-op, got exit %d", code)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("unconfigured deadman made requests: %v", got)
	}
}

func TestDeadmanPingAndFail(t *testing.T) {
	homePath := t.TempDir()
	var rec pingRecorder
	srv := rec.server(t)
	writePingURL(t, homePath, srv.URL+"/ping/secret-uuid", 0o600)

	code, out, errOut := runDeadman(t, homePath, "ping")
	if code != 0 {
		t.Fatalf("ping exit %d: %s", code, errOut)
	}
	if strings.Contains(out+errOut, "secret-uuid") {
		t.Fatalf("ping output leaked the ping URL: %q %q", out, errOut)
	}
	code, _, errOut = runDeadman(t, homePath, "ping", "--fail")
	if code != 0 {
		t.Fatalf("ping --fail exit %d: %s", code, errOut)
	}
	got := rec.got()
	want := []string{"GET /ping/secret-uuid", "GET /ping/secret-uuid/fail"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}

func TestDeadmanRefusesWorldReadableURLFile(t *testing.T) {
	homePath := t.TempDir()
	var rec pingRecorder
	srv := rec.server(t)
	writePingURL(t, homePath, srv.URL+"/ping/secret-uuid", 0o644)
	code, _, errOut := runDeadman(t, homePath, "ping")
	if code == 0 || !strings.Contains(errOut, "world-readable") {
		t.Fatalf("world-readable ping file must be refused: exit %d err=%q", code, errOut)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("refused deadman still made requests: %v", got)
	}
}

func TestDeadmanServerErrorIsAFailure(t *testing.T) {
	homePath := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	writePingURL(t, homePath, srv.URL+"/ping/secret-uuid", 0o600)
	if code, _, errOut := runDeadman(t, homePath, "ping"); code == 0 || strings.Contains(errOut, "secret-uuid") {
		t.Fatalf("a non-2xx ping must fail without leaking the URL: exit %d err=%q", code, errOut)
	}
}

// reasonRecorder keeps the path and the decoded reason of every request.
type reasonRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (p *reasonRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		entry := r.URL.Path
		if r.URL.Query().Has("reason") {
			entry += " reason=" + r.URL.Query().Get("reason")
		}
		p.seen = append(p.seen, entry)
		p.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (p *reasonRecorder) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// A fail ping can say why, so the alert names the cause instead of
// "unspecified". Both flag forms; characters a URL treats specially arrive intact.
func TestDeadmanFailCarriesReason(t *testing.T) {
	homePath := t.TempDir()
	var rec reasonRecorder
	srv := rec.server(t)
	writePingURL(t, homePath, srv.URL+"/ping/secret-uuid", 0o600)
	text := "permission prompt waiting on the Mac screen: click Allow & retry #1 (100%) é"
	if code, out, errOut := runDeadman(t, homePath, "ping", "--fail", "--reason", text); code != 0 || strings.TrimSpace(out) != "deadman: fail sent (HTTP 200)" {
		t.Fatalf("exit %d out=%q err=%q", code, out, errOut)
	}
	if code, _, errOut := runDeadman(t, homePath, "ping", "--reason=disk full", "--fail"); code != 0 {
		t.Fatalf("--reason= form: exit %d err=%q", code, errOut)
	}
	if code, _, errOut := runDeadman(t, homePath, "ping", "--fail"); code != 0 {
		t.Fatalf("plain --fail: exit %d err=%q", code, errOut)
	}
	want := []string{"/ping/secret-uuid/fail reason=" + text, "/ping/secret-uuid/fail reason=disk full", "/ping/secret-uuid/fail"}
	if got := rec.got(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %q, want %q", got, want)
	}
}

// The reason leaves the machine in an alert: one printable line, bounded.
func TestDeadmanReasonIsOneBoundedLine(t *testing.T) {
	homePath := t.TempDir()
	var rec reasonRecorder
	srv := rec.server(t)
	writePingURL(t, homePath, srv.URL+"/ping/secret-uuid", 0o600)
	long := "line one\n\tline\x00two\x1b[31m  " + strings.Repeat("x", 500)
	if code, _, errOut := runDeadman(t, homePath, "ping", "--fail", "--reason", long); code != 0 {
		t.Fatalf("exit %d err=%q", code, errOut)
	}
	got := rec.got()
	if len(got) != 1 {
		t.Fatalf("requests = %q", got)
	}
	reason := strings.TrimPrefix(got[0], "/ping/secret-uuid/fail reason=")
	if !strings.HasPrefix(reason, "line one line two [31m xxx") || !strings.HasSuffix(reason, "...") {
		t.Fatalf("reason not cleaned to one line: %q", reason)
	}
	if n := len([]rune(reason)); n > deadmanReasonMax+3 {
		t.Fatalf("reason is %d characters, more than the %d bound", n, deadmanReasonMax)
	}
	for _, r := range reason {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("reason kept a control character %U: %q", r, reason)
		}
	}
}

// A reason that repeats the ping token would put the credential in a chat
// message: refused, nothing sent, and the error does not echo it either.
func TestDeadmanReasonNeverCarriesThePingCredential(t *testing.T) {
	homePath := t.TempDir()
	var rec reasonRecorder
	srv := rec.server(t)
	token := "0f3c9a7e-secret-token-uuid"
	writePingURL(t, homePath, srv.URL+"/ping/"+token, 0o600)
	for _, reason := range []string{"see " + token, "ping " + srv.URL + "/ping/" + token + " failed"} {
		code, out, errOut := runDeadman(t, homePath, "ping", "--fail", "--reason", reason)
		if code != 1 || strings.Contains(out+errOut, token) {
			t.Fatalf("credential in reason not refused cleanly: exit %d out=%q err=%q", code, out, errOut)
		}
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("refused reason still sent: %q", got)
	}
}

// Usage errors exit 2 and send nothing, which is what lets a caller detect an
// older binary and fall back to a plain fail ping.
func TestDeadmanReasonUsageSendsNothing(t *testing.T) {
	homePath := t.TempDir()
	var rec reasonRecorder
	srv := rec.server(t)
	writePingURL(t, homePath, srv.URL+"/ping/secret-uuid", 0o600)
	for _, args := range [][]string{
		{"ping", "--reason", "no failure to explain"},
		{"ping", "--fail", "--reason"},
		{"ping", "--fail", "--reason", " \t\n "},
		{"ping", "--fail", "--why", "x"},
	} {
		if code, _, errOut := runDeadman(t, homePath, args...); code != 2 || !strings.Contains(errOut, "usage:") {
			t.Fatalf("%q: exit %d err=%q", args, code, errOut)
		}
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("usage errors sent requests: %q", got)
	}
}
