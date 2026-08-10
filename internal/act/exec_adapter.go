package act

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"compound/internal/envelope"
)

// ExecTransportAdapter delivers an outbound body through one named, home-configured
// executable transport. Its unexported write method keeps execution behind Execute's
// PolicyDecision gate.
type ExecTransportAdapter struct {
	Name string
	Home string
}

var _ Adapter = ExecTransportAdapter{}

func NewExecTransportAdapter(name, home string) Adapter {
	return ExecTransportAdapter{Name: name, Home: home}
}

// ReadCurrent returns the empty outbound-state sentinel: a post has no meaningful
// remote pre-image available through this transport.
func (ExecTransportAdapter) ReadCurrent(envelope.ActionTarget) ([]byte, error) {
	return []byte("{}"), nil
}

type execTransportConfig struct {
	Argv     []string `json:"argv"`
	TimeoutS int      `json:"timeout_s,omitempty"`
}

const (
	defaultExecTransportTimeout = 60 * time.Second
	maxExecTransportOutput      = 64 * 1024
	maxExecErrorOutputSummary   = 200
)

// execTransportAllowedNames is the allowlist of executable basenames a transport
// config may invoke without an absolute path. It keeps the "named, home-configured
// executable transport" model intentional: a transport is a deliberately enrolled
// local program, not a shell or arbitrary command lookup.
var execTransportAllowedNames = map[string]bool{
	"claude":   true,
	"codex":    true,
	"compound": true,
	"zai":      true,
	"qwen":     true,
	"minimax":  true,
}

// shellUnsafeRe rejects arguments that look like shell control operators, redirects,
// command separators, or variable/command substitutions. The adapter does not use a
// shell, but the config file is operator-edited, so we fail closed on characters that
// could be misinterpreted by a downstream shell or by exec.Command's argument splitting.
var shellUnsafeRe = regexp.MustCompile(`[;|&<>(){}\[\]\$\` + "`" + `*?\\\n\r]`)

// shellExecutableDenylist rejects well-known interactive shells even when they are
// referenced by an absolute path. A transport is a deliberately enrolled executable,
// not a shell invocation.
var shellExecutableDenylist = map[string]bool{
	"sh":      true,
	"bash":    true,
	"zsh":     true,
	"dash":    true,
	"csh":     true,
	"tcsh":    true,
	"ksh":     true,
	"fish":    true,
	"python":  true,
	"python3": true,
	"perl":    true,
	"ruby":    true,
}

type cappedTransportOutput struct {
	bytes.Buffer
	limit int
}

func (w *cappedTransportOutput) Write(p []byte) (int, error) {
	remaining := w.limit - w.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = w.Buffer.Write(p[:remaining])
	}
	return len(p), nil
}

func (a ExecTransportAdapter) loadConfig() (execTransportConfig, error) {
	name := strings.TrimSpace(a.Name)
	if name == "" || filepath.Base(name) != name || name == "." {
		return execTransportConfig{}, fmt.Errorf("transport %q config: invalid transport name", a.Name)
	}
	if strings.TrimSpace(a.Home) == "" {
		return execTransportConfig{}, fmt.Errorf("transport %q config: home is required", name)
	}
	path := filepath.Join(a.Home, "actuator", "transports", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return execTransportConfig{}, fmt.Errorf("transport %q config: read %s: %w", name, path, err)
	}
	var cfg execTransportConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return execTransportConfig{}, fmt.Errorf("transport %q config: parse %s: %w", name, path, err)
	}
	if len(cfg.Argv) == 0 || strings.TrimSpace(cfg.Argv[0]) == "" {
		return execTransportConfig{}, fmt.Errorf("transport %q config: argv must contain an executable", name)
	}
	if err := validateExecArgv(name, cfg.Argv); err != nil {
		return execTransportConfig{}, err
	}
	if cfg.TimeoutS < 0 {
		return execTransportConfig{}, fmt.Errorf("transport %q config: timeout_s must be non-negative", name)
	}
	return cfg, nil
}

// validateExecArgv enforces the executable allowlist / absolute-path + argument
// validation rule. argv[0] must be either a clean absolute path to a non-shell
// executable, or one of the allowed basenames. Every argument must be free of shell
// metacharacters.
func validateExecArgv(transportName string, argv []string) error {
	exe := argv[0]
	if strings.TrimSpace(exe) == "" {
		return fmt.Errorf("transport %q config: argv[0] executable is empty", transportName)
	}
	if filepath.IsAbs(exe) {
		if clean := filepath.Clean(exe); clean != exe || clean == "/" || strings.Contains(exe, "..") {
			return fmt.Errorf("transport %q config: argv[0] must be a clean absolute path", transportName)
		}
		if shellExecutableDenylist[filepath.Base(exe)] {
			return fmt.Errorf("transport %q config: argv[0] is a shell/interpreter and cannot be used as a transport", transportName)
		}
	} else {
		if filepath.Base(exe) != exe || filepath.Dir(exe) != "." || !execTransportAllowedNames[exe] {
			return fmt.Errorf("transport %q config: argv[0] %q is not an allowed transport executable", transportName, exe)
		}
	}
	for i, arg := range argv {
		if shellUnsafeRe.MatchString(arg) {
			return fmt.Errorf("transport %q config: argv[%d] contains unsafe characters", transportName, i)
		}
	}
	return nil
}

// summarizeOutput returns a redacted, bounded summary of combined stdout/stderr for
// error messages. The full output is retained in the cappedTransportOutput buffer for
// separate recording if needed, but it is never echoed verbatim to callers.
func summarizeOutput(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return "<no output>"
	}
	// Collapse runs of whitespace and strip control characters for a compact summary.
	out = strings.Join(strings.Fields(out), " ")
	if len(out) > maxExecErrorOutputSummary {
		return out[:maxExecErrorOutputSummary] + "... [output redacted]"
	}
	return out
}

func (a ExecTransportAdapter) write(_ envelope.ActionTarget, newValue []byte) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	timeout := defaultExecTransportTimeout
	if cfg.TimeoutS > 0 {
		timeout = time.Duration(cfg.TimeoutS) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Stdin = bytes.NewReader(newValue)
	output := &cappedTransportOutput{limit: maxExecTransportOutput}
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("transport %q timed out after %s; output: %s", strings.TrimSpace(a.Name), timeout, summarizeOutput(output.String()))
	}
	if err != nil {
		return fmt.Errorf("transport %q exec: %w; output: %s", strings.TrimSpace(a.Name), err, summarizeOutput(output.String()))
	}
	return nil
}
