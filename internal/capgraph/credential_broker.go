package capgraph

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	homepkg "compound/internal/home"
)

// CredentialUsageSchemaVersion tags the credential-usage audit records appended
// by `compound credential exec`. Each record names WHO used WHICH surface WHEN —
// surface id, work id, env name, kind — and NEVER the credential value, so
// "which lane used which credential when" stays answerable without a secret
// ever entering a transcript or a repo. The record is evidence (named consumer:
// operator audit of credential use), not authority; the capability graph and
// the custodian's store retain custody.
const CredentialUsageSchemaVersion = "compound.credential_usage.v0.1"

// CredentialExecCommand implements `compound credential exec` — the one verb
// that turns a ConnectionSurface's CredentialHandle into a live child process.
//
//	compound credential exec --surface <node-id> [--work <work_id>] [--env K=V]... -- <cmd> [args...]
//
// It resolves the surface's credential handle, obtains the value from whichever
// custodian the handle's kind names, sets exactly the handle's CredentialEnvName
// (plus any --env overrides) in the child's environment, and execs. The value
// exists in the child process and nowhere else — not in a transcript, not in a
// file Compound reads back, not in a repo. Fail-closed: unresolvable surface,
// unknown kind, unreadable custody, empty value, or missing command → error, no
// exec. Every use is recorded (surface + work id + env name, never the value).
//
// The four kinds differ only in WHERE the value comes from; the consumer never
// knows which case it is in:
//
//   - home-secret: Compound custodies it (COMPOUND_HOME/secrets/<slug>/<file>).
//   - env-file:    an operator dotenv at <path>#<VAR> holds it.
//   - selector:    a non-secret selector variable (profile name); no value.
//   - brokered:    the custodian's own broker command injects it; Compound runs
//     the broker with the child argv appended and never reads the value.
func CredentialExecCommand(args []string, stdout, stderr io.Writer) int {
	if usageRequested(args) {
		fmt.Fprintln(stderr, "usage: compound credential exec --surface <node-id> [--work <work_id>] [--env K=V]... -- <cmd> [args...]")
		return 2
	}
	dash := indexOf(args, "--")
	// Only compound's own flags live before `--`; anything after it is the
	// child argv and must never be parsed as a credential flag (a child could
	// legitimately take --env / --surface itself).
	flagScope := args
	if dash >= 0 {
		flagScope = args[:dash]
	}
	surfaceID := flagValue(flagScope, "--surface")
	workID := flagValue(flagScope, "--work")
	var extraEnv []string
	for i := 0; i < len(flagScope); i++ {
		if flagScope[i] != "--env" {
			continue
		}
		if i+1 >= len(flagScope) {
			fmt.Fprintln(stderr, "error: credential exec: --env needs K=V")
			return 2
		}
		extraEnv = append(extraEnv, flagScope[i+1])
		i++
	}
	var child []string
	if dash >= 0 {
		child = args[dash+1:]
	}
	if strings.TrimSpace(surfaceID) == "" {
		fmt.Fprintln(stderr, "error: credential exec: --surface <node-id> is required")
		return 2
	}
	if len(child) == 0 || strings.TrimSpace(child[0]) == "" {
		fmt.Fprintln(stderr, "error: credential exec: a command after `--` is required")
		return 2
	}

	homePath, err := homepkg.Resolve("")
	if err != nil {
		fmt.Fprintln(stderr, "error: credential exec:", err)
		return 1
	}
	store, err := Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error: credential exec:", err)
		return 1
	}
	surfaces, err := store.ConnectionSurfaces()
	if err != nil {
		fmt.Fprintln(stderr, "error: credential exec:", err)
		return 1
	}
	var surface *ConnectionSurface
	for i := range surfaces {
		if surfaces[i].NodeID == surfaceID {
			surface = &surfaces[i]
			break
		}
	}
	if surface == nil {
		fmt.Fprintf(stderr, "error: credential exec: no connection surface for node %q\n", surfaceID)
		return 1
	}

	env, broker, err := resolveCredentialEnv(homePath, surface.CredentialHandle)
	if err != nil {
		fmt.Fprintln(stderr, "error: credential exec:", err)
		return 1
	}
	if err := recordCredentialUsage(homePath, surfaceID, workID, surface.CredentialHandle); err != nil {
		fmt.Fprintln(stderr, "error: credential exec: record usage:", err)
		return 1
	}

	var cmd *exec.Cmd
	if broker != "" {
		// The custodian's broker injects the value; Compound never reads it.
		// The broker receives the child argv so it can exec the child with the
		// credential already in the environment (e.g. `sb tenant exec -- <cmd>`).
		parts := strings.Fields(broker)
		if len(parts) == 0 {
			fmt.Fprintln(stderr, "error: credential exec: brokered handle has an empty broker command")
			return 1
		}
		cmd = exec.Command(parts[0], append(parts[1:], child...)...)
	} else {
		cmd = exec.Command(child[0], child[1:]...)
	}
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(stderr, "error: credential exec:", err)
		return 1
	}
	return 0
}

// resolveCredentialEnv returns the env pairs to set in the child (compound-side
// kinds) plus a broker argv when the handle delegates injection to a custodian
// command. Exactly one of the two is non-empty. The value is never logged.
func resolveCredentialEnv(homePath string, h CredentialHandle) (env []string, broker string, err error) {
	if strings.TrimSpace(h.Ref) == "" {
		return nil, "", nil // no credential; caller just execs with --env
	}
	kindName, locator, _ := strings.Cut(strings.TrimSpace(h.Ref), "://")
	envName := strings.TrimSpace(h.EnvName)
	switch kindName {
	case "home-secret":
		// locator is <slug>/<file> under COMPOUND_HOME/secrets.
		rel := filepath.FromSlash(locator)
		if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
			return nil, "", fmt.Errorf("home-secret locator %q is not a relative secrets path", locator)
		}
		path := filepath.Join(homePath, "secrets", rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("home-secret %s: %w", path, err)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if value == "" {
			return nil, "", fmt.Errorf("home-secret %s is empty", path)
		}
		return []string{envName + "=" + value}, "", nil
	case "env-file":
		pathPart, varPart, _ := strings.Cut(locator, "#")
		if varPart == "" {
			return nil, "", fmt.Errorf("env-file locator %q must name the variable as <path>#<VAR>", locator)
		}
		value, err := readDotenvVar(pathPart, varPart)
		if err != nil {
			return nil, "", err
		}
		if value == "" {
			return nil, "", fmt.Errorf("env-file %s has empty %s", pathPart, varPart)
		}
		return []string{envName + "=" + value}, "", nil
	case "selector":
		// A selector is not a secret: the locator (e.g. a profile name) IS the value.
		return []string{envName + "=" + locator}, "", nil
	case "brokered":
		return nil, strings.TrimSpace(h.Broker), nil
	default:
		return nil, "", fmt.Errorf("unknown credential kind %q", kindName)
	}
}

// readDotenvVar reads one variable from an operator-managed dotenv. Minimal,
// deliberately: comments and blank lines skipped, KEY=VALUE split on the first
// '=', surrounding whitespace and one pair of quotes trimmed. A missing file or
// a missing variable is an error (fail closed — a handle pointing at nothing is
// not a design).
func readDotenvVar(path, key string) (string, error) {
	// Expand a leading ~/ to the operator home so locators read naturally.
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(h, strings.TrimPrefix(path, "~/"))
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("env-file %s: %w", path, err)
	}
	defer f.Close()
	want := key + "="
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, want) {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, want))
		val = strings.Trim(val, `"'`)
		return val, nil
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("env-file %s: %w", path, err)
	}
	return "", fmt.Errorf("env-file %s has no variable %s", path, key)
}

// recordCredentialUsage appends one audit line naming WHO used WHICH surface
// WHEN. It records surface id, work id, env name, and kind — never the value.
// A recording failure is a hard error: an unlogged use is exactly the
// "which lane used which credential" question the record exists to answer.
func recordCredentialUsage(homePath, surfaceID, workID string, h CredentialHandle) error {
	path := filepath.Join(homePath, "registries", "credential-usage.jsonl")
	rec := struct {
		Schema    string    `json:"schema_version"`
		SurfaceID string    `json:"surface_id"`
		WorkID    string    `json:"work_id,omitempty"`
		EnvName   string    `json:"env_name,omitempty"`
		Kind      string    `json:"kind,omitempty"`
		At        time.Time `json:"at"`
	}{
		Schema:    CredentialUsageSchemaVersion,
		SurfaceID: surfaceID,
		WorkID:    strings.TrimSpace(workID),
		EnvName:   strings.TrimSpace(h.EnvName),
		At:        time.Now().UTC(),
	}
	if kind, _, ok := strings.Cut(strings.TrimSpace(h.Ref), "://"); ok {
		rec.Kind = kind
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func usageRequested(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

func flagValue(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func indexOf(args []string, token string) int {
	for i, a := range args {
		if a == token {
			return i
		}
	}
	return -1
}
