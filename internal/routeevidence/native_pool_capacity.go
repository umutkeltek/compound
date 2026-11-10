package routeevidence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Switchback owns identity, native login and pool observations. This adapter
// consumes its read-only projection through the existing capacity policy; it
// never refreshes credentials, changes membership or grants away execution.
type nativePoolObservation struct {
	Schema            string         `json:"schema"`
	Pool              string         `json:"pool"`
	Provider          string         `json:"provider"`
	AuthorityRevision string         `json:"authority_revision"`
	GeneratedAtMS     int64          `json:"generated_at_ms"`
	FreshnessSeconds  int            `json:"freshness_seconds"`
	State             string         `json:"state"`
	ModelScope        string         `json:"model_scope"`
	Purpose           string         `json:"purpose"`
	Counts            map[string]int `json:"counts"`
	Accounts          []struct {
		ID         string `json:"id"`
		State      string `json:"state"`
		Reason     string `json:"reason"`
		ResetsAtMS *int64 `json:"resets_at_ms"`
		Windows    []struct {
			Name      string   `json:"name"`
			Used      *float64 `json:"used_percent"`
			SampledAt string   `json:"sampled_at"`
			ResetsAt  *string  `json:"resets_at"`
		} `json:"windows"`
	} `json:"accounts"`
}

func nativePoolFromRef(ref string) (string, error) {
	const prefix = "switchback://account-pools/"
	if !strings.HasPrefix(ref, prefix) {
		return "", errors.New("native pool source must use switchback://account-pools/<pool>")
	}
	name := strings.TrimPrefix(ref, prefix)
	if !nativePoolName(name) {
		return "", errors.New("invalid native pool source name")
	}
	return name, nil
}

func nativePoolName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		if i > 0 && (ch == '_' || ch == '-') {
			continue
		}
		return false
	}
	return true
}

func guardNativePoolCapacity(homePath, lane, class string, source LaneCapacitySourceRef, now time.Time, expectedMinutes int, confirm bool, alternatives []string, suppress bool, cfg LaneCapacityConfig) (LaneCapacityDecision, error) {
	pool, err := nativePoolFromRef(source.Ref)
	if err != nil {
		return LaneCapacityDecision{}, err
	}
	if source.FreshnessMinutes <= 0 || source.FreshnessMinutes > 60 {
		return LaneCapacityDecision{}, errors.New("native pool freshness must be between 1 and 60 minutes")
	}
	cfg = normalizeLaneCapacityConfig(cfg, homePath)
	if expectedMinutes <= 0 {
		expectedMinutes = defaultExpectedMinutes
	}
	if int64(expectedMinutes) > math.MaxInt64/int64(time.Minute) {
		return LaneCapacityDecision{}, errors.New("native pool execution horizon is not representable")
	}
	cfg.ExpectedMinutes, cfg.SuppressRefusalEvidence = expectedMinutes, suppress
	run := cfg.NativePoolRunner
	if run == nil {
		run = RunBoundedCapacityCommand
	}
	ctx, cancel := context.WithTimeout(context.Background(), CodexBarProbeTimeout)
	defer cancel()
	body, err := run(ctx, "sb", "profile", "pools", "capacity", pool, "--freshness-seconds", strconv.Itoa(source.FreshnessMinutes*60), "--json")
	if err != nil {
		return LaneCapacityDecision{}, fmt.Errorf("native pool observation unavailable: %w", err)
	}
	capacity, err := parseNativePoolCapacity(body, pool, now, time.Duration(source.FreshnessMinutes)*time.Minute, expectedMinutes)
	if err != nil {
		return LaneCapacityDecision{}, err
	}
	alternatives = capacityAlternativeLadder(class, alternatives, freshRoutingDefaults(homePath))
	return decideLaneCapacity(homePath, lane, now, confirm, alternatives, cfg, capacity)
}

func parseNativePoolCapacity(body []byte, pool string, now time.Time, freshness time.Duration, expectedMinutes int) (LaneCapacity, error) {
	bad := func() (LaneCapacity, error) {
		return LaneCapacity{}, errors.New("invalid or stale Switchback native pool observation")
	}
	if len(body) > MaxCodexBarProbeBytes {
		return bad()
	}
	var report nativePoolObservation
	if err := json.Unmarshal(body, &report); err != nil {
		return bad()
	}
	if report.Schema != "switchback/native-pool-capacity@1" || report.Pool != pool || report.ModelScope != "general" || report.Purpose != "capacity_observation_not_admission_authority" || (report.Provider != "claude" && report.Provider != "codex") || len(report.Accounts) == 0 || len(report.Accounts) > 16 || report.FreshnessSeconds != int(freshness.Seconds()) {
		return bad()
	}
	rev, err := hex.DecodeString(strings.TrimPrefix(report.AuthorityRevision, "sha256:"))
	if err != nil || len(rev) != 32 || !strings.HasPrefix(report.AuthorityRevision, "sha256:") {
		return bad()
	}
	generated := time.UnixMilli(report.GeneratedAtMS)
	if age := now.Sub(generated); age < -capacityClockSkew || age > freshness {
		return bad()
	}
	capacity := LaneCapacity{State: CapacityUnknown, SourceInstalled: true, Source: "switchback/native-pool", DataRef: fmt.Sprintf("source_ref=switchback://account-pools/%s; authority_revision=%s", pool, report.AuthorityRevision)}
	counts := map[string]int{"available": 0, "exhausted": 0, "needs_login": 0, "disabled": 0, "unknown": 0}
	ids := map[string]bool{}
	var available []LaneCapacity
	var earliestReset time.Time
	for _, account := range report.Accounts {
		if !nativePoolName(account.ID) || ids[account.ID] {
			return bad()
		}
		ids[account.ID] = true
		if _, ok := counts[account.State]; !ok {
			return bad()
		}
		counts[account.State]++
		if account.State == "exhausted" && account.ResetsAtMS != nil {
			reset := time.UnixMilli(*account.ResetsAtMS)
			if !reset.After(now) {
				return bad()
			}
			if earliestReset.IsZero() || reset.Before(earliestReset) {
				earliestReset = reset
			}
		}
		if account.State != "available" {
			continue
		}
		required := []string{"five_hour", "seven_day"}
		if report.Provider == "codex" {
			required = []string{"primary", "secondary"}
		}
		member := capacity
		member.State = CapacityAlive
		member.Remaining = 100
		for _, name := range required {
			matched := 0
			for _, window := range account.Windows {
				if window.Name != name {
					continue
				}
				matched++
				if window.Used == nil || math.IsNaN(*window.Used) || math.IsInf(*window.Used, 0) || *window.Used < 0 || *window.Used >= 100 {
					return bad()
				}
				at, err := time.Parse(time.RFC3339Nano, window.SampledAt)
				if err != nil || now.Sub(at) < -capacityClockSkew || now.Sub(at) > freshness {
					return bad()
				}
				if member.LastProven.IsZero() || at.Before(member.LastProven) {
					member.LastProven = at
				}
				member.Remaining = math.Min(member.Remaining, 100-*window.Used)
				if window.ResetsAt != nil {
					reset, err := time.Parse(time.RFC3339Nano, *window.ResetsAt)
					if err != nil || !reset.After(now) {
						return bad()
					}
					if member.ResetsAt.IsZero() || reset.Before(member.ResetsAt) {
						member.ResetsAt = reset
					}
					if !reset.After(now.Add(time.Duration(expectedMinutes) * time.Minute)) {
						member.State = CapacityExpiring
					}
				}
			}
			if matched != 1 {
				return bad()
			}
		}
		available = append(available, member)
	}
	if len(report.Counts) != len(counts) {
		return bad()
	}
	for key, count := range counts {
		if observed, present := report.Counts[key]; !present || observed != count {
			return bad()
		}
	}
	want := "unavailable"
	if counts["exhausted"] > 0 {
		want = "exhausted"
	}
	if counts["unknown"] > 0 {
		want = "unknown"
	}
	if len(available) > 0 {
		want = "available"
	}
	if report.State != want {
		return bad()
	}
	if len(available) > 0 {
		// Preserve pool order, but prefer a member whose quota window outlives
		// this run. Never sum unrelated account percentages into invented quota.
		capacity = available[0]
		for _, member := range available {
			if member.State == CapacityAlive {
				capacity = member
				break
			}
		}
	} else if want == "exhausted" {
		capacity.State, capacity.ResetsAt = CapacityWalled, earliestReset
	} else if want == "unavailable" {
		capacity.State = CapacityDead
	}
	return capacity, nil
}
