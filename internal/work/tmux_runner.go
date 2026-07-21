package work

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// TmuxRunner is the thin supervised-session adapter for governed delegation.
// It owns no meaning: work/session identity and lifecycle evidence remain in
// Store; tmux is only the observable, steerable process container.
type TmuxRunner struct{}

func NewTmuxRunner() *TmuxRunner { return &TmuxRunner{} }

func (r *TmuxRunner) Available() error {
	_, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux is unavailable: %w", err)
	}
	return nil
}

func (r *TmuxRunner) Has(name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, errors.New("tmux session is required")
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return false, fmt.Errorf("tmux is unavailable: %w", err)
	}
	err = exec.Command(path, "has-session", "-t", name).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("tmux has-session failed: %w", err)
}

func (r *TmuxRunner) Create(name, dir, command string, args []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tmux session is required")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("tmux workspace is required")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("tmux command is required")
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux is unavailable: %w", err)
	}
	argv := []string{"new-session", "-d", "-s", name, "-c", dir, "--", command}
	argv = append(argv, args...)
	if err := exec.Command(path, argv...).Run(); err != nil {
		// Deliberately omit argv: prompt bodies may be present there and must
		// never flow into the durable spawn_failed event that records this error.
		return fmt.Errorf("tmux new-session failed: %w", err)
	}
	return nil
}

func (r *TmuxRunner) SendKeys(name, text string, enter bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tmux session is required")
	}
	if text == "" {
		return errors.New("tmux steer text is required")
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux is unavailable: %w", err)
	}
	if err := exec.Command(path, "send-keys", "-t", name, "-l", "--", text).Run(); err != nil {
		// Never include text in the error: Steer may surface it to an event or
		// operator log in later slices.
		return fmt.Errorf("tmux send-keys failed: %w", err)
	}
	if enter {
		if err := exec.Command(path, "send-keys", "-t", name, "Enter").Run(); err != nil {
			return fmt.Errorf("tmux send Enter failed: %w", err)
		}
	}
	return nil
}

func (r *TmuxRunner) Kill(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tmux session is required")
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux is unavailable: %w", err)
	}
	if err := exec.Command(path, "kill-session", "-t", name).Run(); err != nil {
		return fmt.Errorf("tmux kill-session failed: %w", err)
	}
	return nil
}

func (r *TmuxRunner) AttachArgv(name string) []string {
	return []string{"tmux", "attach-session", "-t", strings.TrimSpace(name)}
}

var _ SessionRunner = (*TmuxRunner)(nil)
