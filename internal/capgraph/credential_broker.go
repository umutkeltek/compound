package capgraph

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CredentialUsageSchemaVersion tags the credential-usage audit records appended
// when a credential is resolved for a surface. Each record names WHO used WHICH surface WHEN —
// surface id, work id, env name, kind — and NEVER the credential value, so
// "which lane used which credential when" stays answerable without a secret
// ever entering a transcript or a repo. The record is evidence (named consumer:
// operator audit of credential use), not authority; the capability graph and
// the custodian's store retain custody.
const CredentialUsageSchemaVersion = "compound.credential_usage.v0.1"

var ErrCredentialSurfaceNotRegistered = errors.New("credential surface not registered")

// ResolveCredentialForSurface returns one child-environment assignment from a
// registered, Compound-custodied secret. It never returns the value in an
// error or audit record. Browser tasks use this instead of caller shell state.
func ResolveCredentialForSurface(homePath, surfaceID, workID string, expected CredentialHandle) (string, error) {
	info, err := os.Lstat(filepath.Join(homePath, "registries", "capability-connection-surfaces.jsonl"))
	if os.IsNotExist(err) {
		return "", fmt.Errorf("%w: %s", ErrCredentialSurfaceNotRegistered, surfaceID)
	} else if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("credential surface registry is not a regular file")
	}
	store, err := OpenReadOnly(homePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrCredentialSurfaceNotRegistered, surfaceID)
		}
		return "", err
	}
	surfaces, err := store.ConnectionSurfaces()
	if err != nil {
		return "", err
	}
	for _, surface := range surfaces {
		if surface.NodeID != surfaceID {
			continue
		}
		if surface.CredentialHandle != expected {
			return "", fmt.Errorf("credential surface %q has an unexpected handle", surfaceID)
		}
		if !strings.HasPrefix(surface.CredentialHandle.Ref, "home-secret://") {
			return "", fmt.Errorf("credential surface %q needs a Compound home secret", surfaceID)
		}
		env, broker, err := resolveCredentialEnv(homePath, surface.CredentialHandle)
		if err != nil {
			return "", err
		}
		if broker != "" || len(env) != 1 {
			return "", fmt.Errorf("credential surface %q did not resolve one environment value", surfaceID)
		}
		if err := recordCredentialUsage(homePath, surfaceID, workID, surface.CredentialHandle, "resolved"); err != nil {
			return "", err
		}
		return env[0], nil
	}
	return "", fmt.Errorf("credential surface %q absent from existing registry; refuse environment fallback", surfaceID)
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
		info, err := os.Lstat(path)
		if err != nil {
			return nil, "", fmt.Errorf("home-secret %s: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return nil, "", fmt.Errorf("home-secret %s must be a regular mode-0600 file", path)
		}
		realHome, err := filepath.EvalSymlinks(homePath)
		if err != nil {
			return nil, "", err
		}
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, "", err
		}
		if !strings.HasPrefix(realPath, filepath.Join(realHome, "secrets")+string(os.PathSeparator)) {
			return nil, "", fmt.Errorf("home-secret %s leaves Compound custody", path)
		}
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

// recordCredentialUsage audits credential resolution, not provider contact or
// child launch. The phase field prevents a policy-held task from appearing as
// a completed credential use. It records only surface/work metadata, no value.
// Failure is hard: even secret resolution must leave an audit trail.
func recordCredentialUsage(homePath, surfaceID, workID string, h CredentialHandle, phase string) error {
	path := filepath.Join(homePath, "registries", "credential-usage.jsonl")
	rec := struct {
		Schema    string    `json:"schema_version"`
		SurfaceID string    `json:"surface_id"`
		WorkID    string    `json:"work_id,omitempty"`
		EnvName   string    `json:"env_name,omitempty"`
		Kind      string    `json:"kind,omitempty"`
		Phase     string    `json:"phase"`
		At        time.Time `json:"at"`
	}{
		Schema:    CredentialUsageSchemaVersion,
		SurfaceID: surfaceID,
		WorkID:    strings.TrimSpace(workID),
		EnvName:   strings.TrimSpace(h.EnvName),
		Phase:     phase,
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
