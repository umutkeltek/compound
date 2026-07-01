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
	Supervised            bool     `json:"supervised"`
	Unsupervised          bool     `json:"-"`
	Budget                Budget   `json:"budget"`
}

func BuildPlan(homePath string, in PlanInput, nowFn func() time.Time) (Run, error) {
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
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
	supervised := in.Supervised
	if !supervised && !in.Unsupervised {
		supervised = true
	}
	run := Run{
		SchemaVersion: RunSchemaVersion,
		ID:            newRunID(now, targetKind, targets),
		CreatedAt:     now.Format(time.RFC3339),
		Status:        "planned",
		TargetKind:    targetKind,
		Supervised:    supervised,
		Targets:       targets,
		LensSet:       LensSet{Included: selection.IncludedRefs, Excluded: selection.Excluded},
		Budget:        in.Budget,
		Findings:      []TargetFinding{},
		CountsBefore:  countsBefore,
		CountsAfter:   copyCounts(countsBefore),
		EvidenceRefs:  []string{},
	}
	if supervised {
		run.Supervision = "operator"
	}

	privacySensitive := in.PrivacySensitive
	privacyReason := strings.TrimSpace(in.PrivacyOverrideReason)
	if targetKind == "memory_candidate" && !in.PrivacySensitive {
		if privacyReason == "" {
			privacySensitive = true
		} else {
			run.EvidenceRefs = append(run.EvidenceRefs, "override:privacy_not_sensitive:"+privacyReason)
		}
	} else if targetKind != "memory_candidate" && !in.PrivacySensitive {
		if privacyReason != "" {
			run.EvidenceRefs = append(run.EvidenceRefs, "override:privacy_not_sensitive:"+privacyReason)
		} else {
			run.EvidenceRefs = append(run.EvidenceRefs, "privacy_default:not_sensitive:"+targetKind)
		}
	}

	lanes, err := SelectLanes(homePath, privacySensitive, supervised, now)
	if err != nil {
		var blocked ErrBlockedNoLane
		if errors.As(err, &blocked) {
			run.Route = RoutePlan(lanes)
			run.Status = "blocked"
			run.StoppedReason = "blocked_no_lane:" + blocked.Class
			store, openErr := Open(homePath)
			if openErr != nil {
				return Run{}, openErr
			}
			if saveErr := store.SaveRun(run); saveErr != nil {
				return Run{}, saveErr
			}
			return run, err
		}
		return Run{}, err
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
	inputs, err := loadPlanTargetInputs(homePath, targetKind, states)
	if err != nil {
		return nil, nil, err
	}
	wanted := stringSet(refs)
	stateFilter := stringSet(states)
	counts := map[string]int{}
	targets := []Target{}
	seen := map[string]bool{}
	for _, input := range inputs {
		if len(wanted) > 0 && !wanted[input.Ref] {
			continue
		}
		state := targetState(input.Payload)
		if len(stateFilter) > 0 && !stateFilter[state] {
			continue
		}
		counts[state]++
		seen[input.Ref] = true
		if limit > 0 && len(targets) >= limit {
			continue
		}
		targets = append(targets, Target{Ref: input.Ref, Privacy: input.Visibility, StateBefore: state})
	}
	for ref := range wanted {
		if !seen[ref] {
			return nil, nil, fmt.Errorf("target %q not found kind %s states %s", ref, targetKind, strings.Join(cleanStrings(states), ","))
		}
	}
	return targets, counts, nil
}

func loadPlanTargetInputs(homePath, targetKind string, states []string) ([]TargetInput, error) {
	switch targetKind {
	case "memory_candidate":
		return LoadMemoryCandidateTargets(homePath, states, 0)
	case "skill_candidate":
		return LoadSkillCandidateTargets(homePath, 0)
	case "capability_observation":
		return LoadCapabilityObservationTargets(homePath, 0)
	default:
		return nil, fmt.Errorf("unsupported target kind %q", targetKind)
	}
}

func targetState(payload json.RawMessage) string {
	var obj struct {
		Kind    string `json:"kind"`
		State   string `json:"state"`
		Status  string `json:"status"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(payload, &obj); err != nil {
		return "unknown"
	}
	for _, value := range []string{obj.State, obj.Status, obj.Verdict} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if obj.Kind == "capability_observation" {
		return "awaiting_verdict"
	}
	return "unknown"
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
