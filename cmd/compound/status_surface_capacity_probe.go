package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"compound/internal/statussurface"
)

const codexBarCapacityFreshness = 10 * time.Minute
const codexBarProbeTimeout = 8 * time.Second
const maxCodexBarProbeBytes = 1 << 20

type statusCapacityCommandRunner func(context.Context, string, ...string) ([]byte, error)

type codexBarCapacityWindow struct {
	UsedPercent   *float64 `json:"usedPercent"`
	WindowMinutes int      `json:"windowMinutes"`
	ResetsAt      string   `json:"resetsAt"`
}

type codexBarClaudeUsage struct {
	Provider string `json:"provider"`
	Usage    *struct {
		UpdatedAt string                  `json:"updatedAt"`
		Secondary *codexBarCapacityWindow `json:"secondary"`
		Extra     []struct {
			Window codexBarCapacityWindow `json:"window"`
		} `json:"extraRateWindows"`
	} `json:"usage"`
}

func parseCodexBarClaudeCapacity(body []byte, now time.Time) (statussurface.CapacityEvidence, error) {
	var rows []codexBarClaudeUsage
	if err := json.Unmarshal(body, &rows); err != nil {
		return statussurface.CapacityEvidence{}, fmt.Errorf("decode CodexBar Claude usage: %w", err)
	}
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.Provider), "claude") || row.Usage == nil {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(row.Usage.UpdatedAt))
		if err != nil {
			return statussurface.CapacityEvidence{}, errors.New("CodexBar Claude usage has no valid updatedAt")
		}
		age := now.Sub(updatedAt)
		if age < -5*time.Minute || age > codexBarCapacityFreshness {
			return statussurface.CapacityEvidence{}, fmt.Errorf("CodexBar Claude usage is stale by %s", age.Round(time.Second))
		}

		windows := make([]codexBarCapacityWindow, 0, 1+len(row.Usage.Extra))
		if row.Usage.Secondary != nil {
			windows = append(windows, *row.Usage.Secondary)
		}
		for _, extra := range row.Usage.Extra {
			windows = append(windows, extra.Window)
		}
		var selected codexBarCapacityWindow
		found := false
		for _, window := range windows {
			if window.WindowMinutes != 7*24*60 || window.UsedPercent == nil || math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) || *window.UsedPercent < 0 || *window.UsedPercent > 100 {
				continue
			}
			if !found || *window.UsedPercent > *selected.UsedPercent {
				selected = window
				found = true
			}
		}
		if !found {
			return statussurface.CapacityEvidence{}, errors.New("CodexBar Claude usage has no weekly window")
		}

		var resetsAt time.Time
		if value := strings.TrimSpace(selected.ResetsAt); value != "" {
			resetsAt, _ = time.Parse(time.RFC3339, value)
		}
		return statussurface.CapacityEvidence{
			Status:           statussurface.StatusLive,
			RemainingPercent: 100 - *selected.UsedPercent,
			ResetsAt:         resetsAt.UTC(),
			Source:           "compound/codexbar-live",
		}, nil
	}
	return statussurface.CapacityEvidence{}, errors.New("CodexBar output has no Claude usage row")
}

func probeCodexBarClaudeCapacity(now time.Time) (statussurface.CapacityEvidence, error) {
	return probeCodexBarClaudeCapacityWithRunner(now, func(ctx context.Context, command string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, command, args...).Output()
	})
}

func probeCodexBarClaudeCapacityWithRunner(now time.Time, run statusCapacityCommandRunner) (statussurface.CapacityEvidence, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexBarProbeTimeout)
	defer cancel()
	body, err := run(ctx, "codexbar", "usage", "--provider", "claude", "--source", "auto", "--format", "json", "--no-color")
	if err != nil {
		return statussurface.CapacityEvidence{}, fmt.Errorf("probe CodexBar Claude usage: %w", err)
	}
	if len(body) > maxCodexBarProbeBytes {
		return statussurface.CapacityEvidence{}, errors.New("CodexBar Claude usage exceeds 1 MiB")
	}
	return parseCodexBarClaudeCapacity(body, now)
}
