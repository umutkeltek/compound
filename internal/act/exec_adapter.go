package act

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
)

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
	if cfg.TimeoutS < 0 {
		return execTransportConfig{}, fmt.Errorf("transport %q config: timeout_s must be non-negative", name)
	}
	return cfg, nil
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
		return fmt.Errorf("transport %q timed out after %s; output: %s", strings.TrimSpace(a.Name), timeout, strings.TrimSpace(output.String()))
	}
	if err != nil {
		return fmt.Errorf("transport %q exec: %w; output: %s", strings.TrimSpace(a.Name), err, strings.TrimSpace(output.String()))
	}
	return nil
}
