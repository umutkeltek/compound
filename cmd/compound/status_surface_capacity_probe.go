package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"

	"compound/internal/statussurface"
)

const codexBarCapacityFreshness = 10 * time.Minute
const codexBarProbeTimeout = 8 * time.Second
const maxCodexBarProbeBytes = 1 << 20

var errCodexBarProbeOutputTooLarge = errors.New("CodexBar usage exceeds 1 MiB")

type statusCapacityCommandRunner func(context.Context, string, ...string) ([]byte, error)

type codexBarCapacityWindow struct {
	UsedPercent   *float64 `json:"usedPercent"`
	WindowMinutes int      `json:"windowMinutes"`
	ResetsAt      string   `json:"resetsAt"`
}

type codexBarUsage struct {
	Provider string `json:"provider"`
	Usage    *struct {
		UpdatedAt string                  `json:"updatedAt"`
		Primary   *codexBarCapacityWindow `json:"primary"`
		Secondary *codexBarCapacityWindow `json:"secondary"`
		Extra     []struct {
			Window codexBarCapacityWindow `json:"window"`
		} `json:"extraRateWindows"`
	} `json:"usage"`
}

type statusCapacityEvidence struct {
	FiveHour statussurface.CapacityEvidence
	Weekly   statussurface.CapacityEvidence
}

type claudeCapacityEvidence = statusCapacityEvidence

func parseCodexBarClaudeCapacity(body []byte, now time.Time) (claudeCapacityEvidence, error) {
	return parseCodexBarCapacity(body, "claude", now)
}

func parseCodexBarCapacity(body []byte, provider string, now time.Time) (statusCapacityEvidence, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return statusCapacityEvidence{}, errors.New("CodexBar provider is required")
	}
	var rows []codexBarUsage
	if err := json.Unmarshal(body, &rows); err != nil {
		return statusCapacityEvidence{}, fmt.Errorf("decode CodexBar %s usage: %w", provider, err)
	}
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.Provider), provider) || row.Usage == nil {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(row.Usage.UpdatedAt))
		if err != nil {
			return statusCapacityEvidence{}, fmt.Errorf("CodexBar %s usage has no valid updatedAt", provider)
		}
		age := now.Sub(updatedAt)
		if age < -5*time.Minute || age > codexBarCapacityFreshness {
			return statusCapacityEvidence{}, fmt.Errorf("CodexBar %s usage is stale by %s", provider, age.Round(time.Second))
		}

		windows := make([]codexBarCapacityWindow, 0, 2+len(row.Usage.Extra))
		if row.Usage.Primary != nil {
			windows = append(windows, *row.Usage.Primary)
		}
		if row.Usage.Secondary != nil {
			windows = append(windows, *row.Usage.Secondary)
		}
		for _, extra := range row.Usage.Extra {
			windows = append(windows, extra.Window)
		}
		var evidence statusCapacityEvidence
		if selected, found := selectMostConstrainingCodexBarWindow(windows, 5*60); found {
			evidence.FiveHour = reduceCodexBarWindow(selected)
		}
		if selected, found := selectMostConstrainingCodexBarWindow(windows, 7*24*60); found {
			evidence.Weekly = reduceCodexBarWindow(selected)
		}
		if evidence.FiveHour.Status != statussurface.StatusLive && evidence.Weekly.Status != statussurface.StatusLive {
			return statusCapacityEvidence{}, fmt.Errorf("CodexBar %s usage has no five-hour or weekly window", provider)
		}
		return evidence, nil
	}
	return statusCapacityEvidence{}, fmt.Errorf("CodexBar output has no %s usage row", provider)
}

func selectMostConstrainingCodexBarWindow(windows []codexBarCapacityWindow, minutes int) (codexBarCapacityWindow, bool) {
	var selected codexBarCapacityWindow
	found := false
	for _, window := range windows {
		if !validCodexBarWindow(window, minutes) {
			continue
		}
		if !found || *window.UsedPercent > *selected.UsedPercent {
			selected = window
			found = true
		}
	}
	return selected, found
}

func validCodexBarWindow(window codexBarCapacityWindow, minutes int) bool {
	return window.WindowMinutes == minutes && window.UsedPercent != nil &&
		!math.IsNaN(*window.UsedPercent) && !math.IsInf(*window.UsedPercent, 0) &&
		*window.UsedPercent >= 0 && *window.UsedPercent <= 100
}

func reduceCodexBarWindow(window codexBarCapacityWindow) statussurface.CapacityEvidence {
	var resetsAt time.Time
	if value := strings.TrimSpace(window.ResetsAt); value != "" {
		resetsAt, _ = time.Parse(time.RFC3339, value)
	}
	return statussurface.CapacityEvidence{
		Status:           statussurface.StatusLive,
		RemainingPercent: 100 - *window.UsedPercent,
		ResetsAt:         resetsAt.UTC(),
		Source:           "compound/codexbar-live",
	}
}

func probeCodexBarClaudeCapacity(now time.Time) (claudeCapacityEvidence, error) {
	return probeCodexBarCapacity("claude", now)
}

func probeCodexBarClaudeCapacityWithRunner(now time.Time, run statusCapacityCommandRunner) (claudeCapacityEvidence, error) {
	return probeCodexBarCapacityWithRunner("claude", now, run)
}

func probeCodexBarCapacity(provider string, now time.Time) (statusCapacityEvidence, error) {
	return probeCodexBarCapacityWithRunner(provider, now, runBoundedStatusCapacityCommand)
}

func probeCodexBarCapacityWithRunner(provider string, now time.Time, run statusCapacityCommandRunner) (statusCapacityEvidence, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return statusCapacityEvidence{}, errors.New("CodexBar provider is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexBarProbeTimeout)
	defer cancel()
	body, err := run(ctx, "codexbar", "usage", "--provider", provider, "--source", "auto", "--format", "json", "--no-color")
	if err != nil {
		return statusCapacityEvidence{}, fmt.Errorf("probe CodexBar %s usage: %w", provider, err)
	}
	if len(body) > maxCodexBarProbeBytes {
		return statusCapacityEvidence{}, errCodexBarProbeOutputTooLarge
	}
	return parseCodexBarCapacity(body, provider, now)
}

func runBoundedStatusCapacityCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open CodexBar stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(stdout, maxCodexBarProbeBytes+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("read CodexBar stdout: %w", readErr)
	}
	if len(body) > maxCodexBarProbeBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errCodexBarProbeOutputTooLarge
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return body, nil
}
