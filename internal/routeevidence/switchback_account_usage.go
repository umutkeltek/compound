package routeevidence

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

// Account selection stays with Switchback. This adapter only reduces its saved
// usage observations to the existing provider-grain capacity policy.
type switchbackAccountUsage struct {
	Schema   string `json:"schema"`
	Purpose  string `json:"purpose"`
	Accounts []struct {
		Provider        string  `json:"provider"`
		Slot            string  `json:"slot"`
		Enabled         *bool   `json:"enabled"`
		State           string  `json:"state"`
		RefreshError    *string `json:"refresh_error"`
		ReloginRequired bool    `json:"relogin_required"`
		Estimated       bool    `json:"estimated"`
		UsageKind       string  `json:"usage_kind"`
		Windows         []struct {
			Name      string   `json:"name"`
			Used      *float64 `json:"used_percent"`
			Remaining *float64 `json:"remaining_percent"`
			SampledAt string   `json:"sampled_at"`
			ResetsAt  *string  `json:"resets_at"`
			State     string   `json:"state"`
			Scope     string   `json:"scope"`
			Estimated bool     `json:"estimated"`
			UsageKind string   `json:"usage_kind"`
		} `json:"windows"`
	} `json:"accounts"`
	Coverage []struct {
		Provider string `json:"provider"`
		State    string `json:"state"`
	} `json:"coverage"`
}

func switchbackUsageProviderFromRef(ref string) (string, error) {
	const prefix = "switchback://account-usage/"
	if !strings.HasPrefix(ref, prefix) {
		return "", errors.New("account usage source must use switchback://account-usage/<provider>")
	}
	provider := strings.TrimPrefix(ref, prefix)
	if !nativePoolName(provider) || provider != strings.ToLower(provider) {
		return "", errors.New("invalid account usage provider")
	}
	return provider, nil
}

func guardSwitchbackAccountUsage(homePath, lane, class string, source LaneCapacitySourceRef, now time.Time, expectedMinutes int, confirm bool, alternatives []string, suppress bool, cfg LaneCapacityConfig) (LaneCapacityDecision, error) {
	provider, err := switchbackUsageProviderFromRef(source.Ref)
	if err != nil {
		return LaneCapacityDecision{}, err
	}
	if source.FreshnessMinutes <= 0 || source.FreshnessMinutes > 60 {
		return LaneCapacityDecision{}, errors.New("account usage freshness must be between 1 and 60 minutes")
	}
	expectedMinutes = capacityExpectedMinutes(expectedMinutes)
	if int64(expectedMinutes) > math.MaxInt64/int64(time.Minute) {
		return LaneCapacityDecision{}, errors.New("account usage execution horizon is not representable")
	}
	cfg = normalizeLaneCapacityConfig(cfg, homePath)
	cfg.ExpectedMinutes, cfg.SuppressRefusalEvidence = expectedMinutes, suppress
	run := cfg.AccountUsageRunner
	if run == nil {
		run = RunBoundedCapacityCommand
	}
	ctx, cancel := context.WithTimeout(context.Background(), CodexBarProbeTimeout)
	defer cancel()
	body, err := run(ctx, "sb", "limits", "--provider", provider, "--json")
	if err != nil {
		// Subprocess errors can contain account identity or private source text.
		return LaneCapacityDecision{}, errors.New("Switchback account usage observation unavailable")
	}
	capacity, err := parseSwitchbackAccountUsage(body, provider, now, time.Duration(source.FreshnessMinutes)*time.Minute, expectedMinutes)
	if err != nil {
		return LaneCapacityDecision{}, err
	}
	alternatives = capacityAlternativeLadder(class, alternatives, freshRoutingDefaults(homePath))
	return decideLaneCapacity(homePath, lane, now, confirm, alternatives, cfg, capacity)
}

func parseSwitchbackAccountUsage(body []byte, provider string, now time.Time, freshness time.Duration, expectedMinutes int) (LaneCapacity, error) {
	bad := func() (LaneCapacity, error) {
		return LaneCapacity{}, errors.New("invalid Switchback account usage observation")
	}
	if len(body) > MaxCodexBarProbeBytes || freshness <= 0 || expectedMinutes <= 0 || int64(expectedMinutes) > math.MaxInt64/int64(time.Minute) {
		return bad()
	}
	var report switchbackAccountUsage
	if err := json.Unmarshal(body, &report); err != nil {
		return bad()
	}
	if report.Schema != "switchback/account-usage@1" || report.Purpose != "usage_observation_not_admission_authority" || len(report.Accounts) > 128 || len(report.Coverage) > 128 {
		return bad()
	}
	base := LaneCapacity{State: CapacityUnknown, SourceInstalled: true, Source: "switchback/account-usage", DataRef: "source_ref=switchback://account-usage/" + provider}
	unknown := func(reason string) LaneCapacity {
		c := base
		c.DataRef += "; reason=" + reason
		return c
	}
	// Coverage lists gaps, not successful providers. A matching negative
	// observation overrides retained rows without requiring a success entry.
	for _, coverage := range report.Coverage {
		if coverage.Provider == provider {
			return unknown("coverage_unavailable"), nil
		}
	}
	members := []LaneCapacity{}
	seen := map[string]bool{}
	matched := false
	for _, account := range report.Accounts {
		if account.Provider != provider {
			continue
		}
		matched = true
		if account.Enabled != nil && !*account.Enabled {
			continue
		}
		if account.Slot == "" || seen[account.Slot] || len(account.Windows) > 32 {
			return bad()
		}
		seen[account.Slot] = true
		member := base
		member.State, member.Remaining = CapacityAlive, 100
		if account.ReloginRequired {
			member.State = CapacityDead
		} else if account.RefreshError != nil && *account.RefreshError != "" {
			member = unknown("refresh_error")
		} else if account.Estimated || account.UsageKind == "estimated" {
			member = unknown("estimated_usage")
		} else if account.State != "fresh" || len(account.Windows) == 0 {
			member = unknown("usage_unavailable")
		} else {
			windowNames := map[string]bool{}
			applicableWindows := 0
			for _, window := range account.Windows {
				// MCP/tool quotas do not constrain generation capacity.
				if window.Scope == "tools" {
					continue
				}
				if window.Scope != "" && window.Scope != "general" && window.Scope != "generation" {
					member = unknown("window_scope_unknown")
					break
				}
				if window.Estimated || window.UsageKind == "estimated" {
					member = unknown("estimated_usage")
					break
				}
				applicableWindows++
				if window.Name == "" || windowNames[window.Name] {
					return bad()
				}
				windowNames[window.Name] = true
				if window.State != "fresh" || window.Used == nil || math.IsNaN(*window.Used) || math.IsInf(*window.Used, 0) || *window.Used < 0 || *window.Used > 100 {
					member = unknown("window_unavailable")
					break
				}
				if window.Remaining != nil && (math.IsNaN(*window.Remaining) || math.IsInf(*window.Remaining, 0) || math.Abs(*window.Remaining-(100-*window.Used)) > 0.000001) {
					return bad()
				}
				sampled, err := time.Parse(time.RFC3339Nano, window.SampledAt)
				if err != nil || now.Sub(sampled) < -capacityClockSkew || now.Sub(sampled) > freshness {
					member = unknown("window_stale")
					break
				}
				if member.LastProven.IsZero() || sampled.Before(member.LastProven) {
					member.LastProven = sampled.UTC()
				}
				member.Remaining = math.Min(member.Remaining, 100-*window.Used)
				if window.ResetsAt != nil {
					reset, err := time.Parse(time.RFC3339Nano, *window.ResetsAt)
					if err != nil || !reset.After(now) {
						// Passing a reset never establishes replenished quota.
						member = unknown("reset_passed_or_invalid")
						break
					}
					if member.ResetsAt.IsZero() || reset.Before(member.ResetsAt) {
						member.ResetsAt = reset.UTC()
					}
				}
			}
			if member.State == CapacityAlive && applicableWindows == 0 {
				member = unknown("no_generation_windows")
			}
			if member.State == CapacityAlive {
				if member.Remaining <= 0 {
					member.State = CapacityWalled
				} else if !member.ResetsAt.IsZero() && !member.ResetsAt.After(now.Add(time.Duration(expectedMinutes)*time.Minute)) {
					member.State = CapacityExpiring
				}
			}
		}
		members = append(members, member)
	}
	if !matched {
		return unknown("no_provider_observation"), nil
	}
	if len(members) == 0 {
		base.State = CapacityDead
		return base, nil
	}
	if len(members) == 1 {
		return members[0], nil
	}
	// A provider observation does not bind execution to a particular account.
	// Admit multiple enabled accounts only when every account independently
	// outlives the run. Never select the best account or sum their percentages.
	result := members[0]
	for _, member := range members {
		if member.State != CapacityAlive {
			allWalled, allDead := true, true
			for _, candidate := range members {
				allWalled = allWalled && candidate.State == CapacityWalled
				allDead = allDead && candidate.State == CapacityDead
			}
			if allWalled {
				base.State = CapacityWalled
				return base, nil
			}
			if allDead {
				base.State = CapacityDead
				return base, nil
			}
			return unknown("multiple_accounts_not_all_usable"), nil
		}
		if member.Remaining < result.Remaining {
			result.Remaining = member.Remaining
		}
		if member.LastProven.Before(result.LastProven) {
			result.LastProven = member.LastProven
		}
		if !member.ResetsAt.IsZero() && (result.ResetsAt.IsZero() || member.ResetsAt.Before(result.ResetsAt)) {
			result.ResetsAt = member.ResetsAt
		}
	}
	return result, nil
}
