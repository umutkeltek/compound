package panel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PlanInput struct {
	TargetKind            string   `json:"target_kind"`
	TargetRefs            []string `json:"target_refs"`
	States                []string `json:"states"`
	Limit                 int      `json:"limit"`
	PrivacySensitive      bool     `json:"privacy_sensitive"`
	PrivacyOverrideReason string   `json:"privacy_override_reason,omitempty"`
	Budget                Budget   `json:"budget"`
}

func BuildPlan(homePath string, in PlanInput) (Run, error) {
	now := time.Now().UTC()
	targetKind := strings.TrimSpace(in.TargetKind)
	if targetKind == "" {
		return Run{}, fmt.Errorf("target kind required")
	}
	targets, countsBefore, err := resolvePlanTargets(homePath, targetKind, in.TargetRefs, in.States, in.Limit)
	if err != nil {
		return Run{}, err
	}
	selection, err := SelectLenses(homePath, targetKind)
	if err != nil {
		return Run{}, err
	}
	run := Run{
		SchemaVersion: RunSchemaVersion,
		ID:            newRunID(now, targetKind, targets),
		CreatedAt:     now.Format(time.RFC3339),
		Status:        "planned",
		TargetKind:    targetKind,
		Targets:       targets,
		LensSet:       LensSet{Included: selection.IncludedRefs, Excluded: selection.Excluded},
		Budget:        in.Budget,
		Findings:      []TargetFinding{},
		CountsBefore:  countsBefore,
		CountsAfter:   copyCounts(countsBefore),
		EvidenceRefs:  []string{},
	}
	privacySensitive := in.PrivacySensitive
	if targetKind == "memory_candidate" && !in.PrivacySensitive {
		if strings.TrimSpace(in.PrivacyOverrideReason) == "" {
			privacySensitive = true
		} else {
			run.EvidenceRefs = append(run.EvidenceRefs, "override:privacy_not_sensitive:"+strings.TrimSpace(in.PrivacyOverrideReason))
		}
	}
	lanes, err := SelectLanes(homePath, privacySensitive, now)
	if err != nil {
		var blocked ErrBlockedNoLane
		if errors.As(err, &blocked) {
			run.Route = RoutePlan(lanes)
			run.Status = "blocked"
			run.StoppedReason = "blocked_no_lane:" + blocked.Class
			store, openErr := Open(homePath)
			if openErr != nil {
				return run, openErr
			}
			if saveErr := store.SaveRun(run); saveErr != nil {
				return run, saveErr
			}
		}
		return run, err
	}
	run.Route = RoutePlan(lanes)
	store, err := Open(homePath)
	if err != nil {
		return Run{}, err
	}
	if err := store.SaveRun(run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func resolvePlanTargets(homePath, targetKind string, refs, states []string, limit int) ([]Target, map[string]int, error) {
	if targetKind != "memory_candidate" {
		return nil, nil, fmt.Errorf("unsupported target kind %q", targetKind)
	}
	inputs, err := LoadMemoryCandidateTargets(homePath, states, 0)
	if err != nil {
		return nil, nil, err
	}
	wanted := stringSet(refs)
	counts := map[string]int{}
	targets := []Target{}
	seen := map[string]bool{}
	for _, input := range inputs {
		if len(wanted) > 0 && !wanted[input.Ref] {
			continue
		}
		state := targetState(input.Payload)
		counts[state]++
		seen[input.Ref] = true
		if limit > 0 && len(targets) >= limit {
			continue
		}
		targets = append(targets, Target{Ref: input.Ref, Privacy: input.Visibility, StateBefore: state})
	}
	for ref := range wanted {
		if !seen[ref] {
			return nil, nil, fmt.Errorf("target %q not found for kind %s and states %s", ref, targetKind, strings.Join(cleanStrings(states), ","))
		}
	}
	return targets, counts, nil
}

func targetState(payload json.RawMessage) string {
	var obj struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(payload, &obj); err != nil {
		return "unknown"
	}
	state := strings.TrimSpace(obj.State)
	if state == "" {
		return "unknown"
	}
	return state
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func copyCounts(in map[string]int) map[string]int {
	out := map[string]int{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func newRunID(now time.Time, targetKind string, targets []Target) string {
	parts := []string{targetKind, now.UTC().Format(time.RFC3339Nano)}
	for _, target := range targets {
		parts = append(parts, target.Ref)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "prun_" + now.UTC().Format("20060102T150405Z") + "_" + hex.EncodeToString(sum[:])[:12]
}
