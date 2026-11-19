package console

// The console supervisor owns this ephemeral transport authority. It never
// grants policy permission: callers still need the exact native action intent.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	OperatorSessionEnv   = "COMPOUND_CONSOLE_OPERATOR_SESSION"
	OperatorSocketEnv    = "COMPOUND_CONSOLE_OPERATOR_SOCKET"
	OperatorBrokerKeyEnv = "COMPOUND_CONSOLE_OPERATOR_BROKER_KEY"
	OperatorInstanceEnv  = "COMPOUND_CONSOLE_OPERATOR_INSTANCE"
	OperatorOrigin       = "http://127.0.0.1:3777"
	OperatorPairTTL      = 5 * time.Minute
	OperatorSessionTTL   = time.Hour
	operatorMaxCodes     = 3
	operatorMaxSessions  = 4
)

type operatorSession struct {
	CSRF    string
	Expires time.Time
}

// OperatorSessionBroker lives exactly as long as one console supervisor. The
// Unix socket is private to the current OS user; Next workers share this owner.
type OperatorSessionBroker struct {
	mu                          sync.Mutex
	home, instance, key, socket string
	codes                       map[[32]byte]time.Time
	sessions                    map[[32]byte]operatorSession
	now                         func() time.Time
	server                      *http.Server
	lock                        *os.File
	closeOnce                   sync.Once
	ownedSocket                 bool
	closed                      bool
}

type operatorSessionRequest struct {
	Operation string `json:"operation"`
	Home      string `json:"home"`
	Origin    string `json:"origin"`
	Instance  string `json:"instance"`
	Code      string `json:"code,omitempty"`
	Session   string `json:"session,omitempty"`
	CSRF      string `json:"csrf,omitempty"`
}

type operatorSessionReply struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code,omitempty"`
	Session   string `json:"session,omitempty"`
	CSRF      string `json:"csrf,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Instance  string `json:"instance,omitempty"`
}

func randomOperatorToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func operatorTokenDigest(value string) [32]byte { return sha256.Sum256([]byte(value)) }

func operatorTokenEqual(a, b string) bool {
	ax, bx := operatorTokenDigest(a), operatorTokenDigest(b)
	return len(a) == 64 && subtle.ConstantTimeCompare(ax[:], bx[:]) == 1
}

func operatorPrivatePath(path string, mode os.FileMode, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || info.Mode().Perm() != mode {
		return errors.New("console operator runtime path must be owned by this user with private permissions")
	}
	return nil
}

func operatorRuntimeDirectory(homePath string) (string, error) {
	// A service and interactive terminal can have different TMPDIR values.
	// Use one OS temporary root, partitioned by OS user and explicit home.
	base, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return "", err
	}
	digest := operatorTokenDigest(filepath.Clean(homePath))
	return filepath.Join(base, fmt.Sprintf("cc-%d-%x-3777", os.Getuid(), digest[:8])), nil
}

// OperatorSocketPath locates transport only. No key or home authority is read
// from a descriptor, current directory, browser, or persistent config file.
func OperatorSocketPath(homePath string) (string, error) {
	dir, err := operatorRuntimeDirectory(homePath)
	if err != nil {
		return "", err
	}
	if err := operatorPrivatePath(dir, 0o700, true); err != nil {
		return "", err
	}
	return filepath.Join(dir, "operator.sock"), nil
}

func EnableOperatorSession(plan LaunchPlan) (LaunchPlan, error) {
	if !plan.validated || !plan.MutationsEnabled || (plan.Lifecycle != LifecycleDev && plan.Lifecycle != LifecycleServe) || plan.Env["COMPOUND_HOME"] == "" {
		return plan, errors.New("operator session requires a validated dev/serve mutation posture and explicit COMPOUND_HOME")
	}
	plan.OperatorSessionEnabled = true
	return plan, nil
}

// BuildOperatorSessionLaunchPlan enables only the native session transport.
// Ambient legacy capabilities are stripped rather than generated or inherited.
func BuildOperatorSessionLaunchPlan(lifecycle, consoleDir string, privateFallbacks, dryRun bool) (LaunchPlan, error) {
	if strings.TrimSpace(os.Getenv("COMPOUND_HOME")) == "" || (lifecycle != LifecycleDev && lifecycle != LifecycleServe) {
		return LaunchPlan{}, errors.New("operator session requires explicit COMPOUND_HOME and dev/serve lifecycle")
	}
	plan, err := BuildLifecycleLaunchPlan(lifecycle, consoleDir, false, privateFallbacks, dryRun)
	if err != nil {
		return LaunchPlan{}, err
	}
	plan.MutationsEnabled = true
	plan.Env[MutationsEnv] = "1"
	plan.CapabilityRequired = false
	plan.OperatorSessionEnabled = true
	return plan, nil
}

func StartOperatorSessionBroker(homePath string) (*OperatorSessionBroker, error) {
	if homePath == "" || !filepath.IsAbs(homePath) {
		return nil, errors.New("operator session requires an absolute pinned home")
	}
	dir, err := operatorRuntimeDirectory(homePath)
	if err != nil {
		return nil, err
	}
	return startOperatorSessionBrokerAt(homePath, dir)
}

func startOperatorSessionBrokerAt(homePath, dir string) (*OperatorSessionBroker, error) {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := operatorPrivatePath(dir, 0o700, true); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := operatorPrivatePath(lock.Name(), 0o600, false); err != nil {
		lock.Close()
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("console operator broker already running")
	}
	b := &OperatorSessionBroker{home: filepath.Clean(homePath), lock: lock, now: time.Now, codes: make(map[[32]byte]time.Time), sessions: make(map[[32]byte]operatorSession)}
	defer func() {
		if b.server == nil {
			b.Close()
		}
	}()
	if b.key, err = randomOperatorToken(); err != nil {
		return nil, err
	}
	if b.instance, err = randomOperatorToken(); err != nil {
		return nil, err
	}
	b.socket = filepath.Join(dir, "operator.sock")
	if info, statErr := os.Lstat(b.socket); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("console operator socket path is not an owned socket")
		}
		if err := operatorPrivatePath(b.socket, 0o600, false); err != nil {
			return nil, err
		}
		if err := os.Remove(b.socket); err != nil {
			return nil, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	listener, err := net.Listen("unix", b.socket)
	if err != nil {
		return nil, err
	}
	b.ownedSocket = true
	if err := os.Chmod(b.socket, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	b.server = &http.Server{Handler: http.HandlerFunc(b.serveHTTP), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: time.Second}
	go func() { _ = b.server.Serve(listener) }()
	return b, nil
}

func (b *OperatorSessionBroker) Close() {
	b.closeOnce.Do(func() {
		if b.server != nil {
			_ = b.server.Close()
		}
		b.mu.Lock()
		b.closed = true
		clear(b.codes)
		clear(b.sessions)
		b.mu.Unlock()
		// The lock is retained as an empty runtime primitive. Removing a lockfile
		// while another process opens it could create two independent owners.
		if b.ownedSocket {
			_ = os.Remove(b.socket)
		}
		if b.lock != nil {
			_ = syscall.Flock(int(b.lock.Fd()), syscall.LOCK_UN)
			_ = b.lock.Close()
		}
	})
}

func (b *OperatorSessionBroker) ChildEnv(env []string) []string {
	entry := func(name, value string) string { return name + "=" + value }
	return append(env,
		entry(OperatorSessionEnv, "1"),
		entry(OperatorSocketEnv, b.socket),
		entry(OperatorBrokerKeyEnv, b.key),
		entry(OperatorInstanceEnv, b.instance),
	)
}

func (b *OperatorSessionBroker) prune(now time.Time) {
	for code, expires := range b.codes {
		if !now.Before(expires) {
			delete(b.codes, code)
		}
	}
	for id, session := range b.sessions {
		if !now.Before(session.Expires) {
			delete(b.sessions, id)
		}
	}
}

func (b *OperatorSessionBroker) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	refuse := func(status int) { http.Error(w, "console operator request refused", status) }
	if r.Method != http.MethodPost || r.URL.Path != "/operator-session" || r.URL.RawQuery != "" {
		refuse(403)
		return
	}
	var in operatorSessionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		refuse(400)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		refuse(400)
		return
	}
	if in.Home != b.home || in.Origin != OperatorOrigin {
		refuse(403)
		return
	}
	if in.Operation != "issue" && (!operatorTokenEqual(r.Header.Get("X-Compound-Operator-Broker"), b.key) || in.Instance != b.instance) {
		refuse(403)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		refuse(503)
		return
	}
	now := b.now()
	b.prune(now)
	out := operatorSessionReply{OK: true, Instance: b.instance}
	switch in.Operation {
	case "issue":
		if len(b.codes) >= operatorMaxCodes {
			refuse(429)
			return
		}
		code, err := randomOperatorToken()
		if err != nil {
			refuse(503)
			return
		}
		expires := now.Add(OperatorPairTTL)
		b.codes[operatorTokenDigest(code)] = expires
		out.Code, out.ExpiresAt = code, expires.UTC().Format(time.RFC3339)
	case "pair":
		digest := operatorTokenDigest(in.Code)
		if _, exists := b.codes[digest]; !exists || len(in.Code) != 64 {
			refuse(403)
			return
		}
		if len(b.sessions) >= operatorMaxSessions {
			refuse(429)
			return
		}
		id, err := randomOperatorToken()
		if err != nil {
			refuse(503)
			return
		}
		csrf, err := randomOperatorToken()
		if err != nil {
			refuse(503)
			return
		}
		delete(b.codes, digest) // one shared owner consumes before replying
		session := operatorSession{CSRF: csrf, Expires: now.Add(OperatorSessionTTL)}
		b.sessions[operatorTokenDigest(id)] = session
		out.Session, out.CSRF, out.ExpiresAt = id, csrf, session.Expires.UTC().Format(time.RFC3339)
	case "status", "authorize", "revoke":
		id := operatorTokenDigest(in.Session)
		session, exists := b.sessions[id]
		if !exists || len(in.Session) != 64 {
			refuse(403)
			return
		}
		if in.Operation != "status" && !operatorTokenEqual(in.CSRF, session.CSRF) {
			refuse(403)
			return
		}
		if in.Operation == "revoke" {
			delete(b.sessions, id)
		} else {
			out.CSRF, out.ExpiresAt = session.CSRF, session.Expires.UTC().Format(time.RFC3339)
		}
	default:
		refuse(400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// IssueOperatorPairingCode is a local OS-user operation over the private UDS.
// The caller must select an interactive/private output before requesting it.
func IssueOperatorPairingCode(homePath string) (code, expires string, err error) {
	socket, err := OperatorSocketPath(homePath)
	if err != nil {
		return "", "", err
	}
	if err := operatorPrivatePath(socket, 0o600, false); err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body, _ := json.Marshal(operatorSessionRequest{Operation: "issue", Home: filepath.Clean(homePath), Origin: OperatorOrigin})
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://console.invalid/operator-session", bytes.NewReader(body))
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return "", "", errors.New("console operator broker unavailable; start console with --operator-session")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("console pairing refused (%d); check home, instance and outstanding codes", response.StatusCode)
	}
	var reply operatorSessionReply
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&reply); err != nil || !reply.OK || len(reply.Code) != 64 {
		return "", "", errors.New("invalid console pairing response")
	}
	return reply.Code, reply.ExpiresAt, nil
}
