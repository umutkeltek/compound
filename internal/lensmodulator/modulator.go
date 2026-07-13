// Package lensmodulator is the GOVERNED MODULATOR — the first compound organ that ACTS on a
// recorded behavioral-lens signal instead of only observing it. It reads InferredSignals
// (behaviorallens.LoadSignals, operator-local) and, when a signal crosses an operator-armed
// threshold, applies a REVERSIBLE route/role adjustment (demote the flagged lane) THROUGH the
// generic governed write seam (internal/act). It never mutates without an allowing PolicyDecision.
//
// Three load-bearing invariants:
//   - DEFAULT OFF / fail-closed. No armed threshold => the modulator does nothing. A stranger clone
//     never wrote a threshold config, so it can never act.
//   - GOVERNED BY CONSTRUCTION. Every demote is an ActionIntent evaluated by policy and executed by
//     act.Execute, which refuses without a matching allowing PolicyDecision. No standing grant =>
//     the demote holds and NOTHING is written. Revert is itself a governed act.
//   - CALIBRATION GUARD. A threshold cannot be armed until >=1 real measured signal for that lens
//     exists — you cannot arm on an assumed/marketing number.
//
// Visibility boundary: an operator_private signal's modulation stays operator-local — it is written
// to a QUARANTINED key root under COMPOUND_HOME and excluded from the public route projection. Only
// public_safe modulations may reach a shared route surface. All state lives under COMPOUND_HOME,
// never the repo, and no path makes a provider/network call.
//
// NOTE: this file is the NAIVE (ungated) first step — it writes the demote directly and arms without
// calibration, so the three falsifiers go RED. The governed version composes act + the guard.
package lensmodulator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/behaviorallens"
	"compound/internal/policy"

	"gopkg.in/yaml.v3"
)

// ThresholdSchemaVersion versions the per-home armed-threshold config.
const ThresholdSchemaVersion = "compound.lens_modulator_thresholds.v0.1"

// CapabilityDemote is the capability every demote/revert ActionIntent declares, so an operator's
// standing grant can pre-authorize exactly this modulation and nothing else.
const CapabilityDemote = "lensmodulator.route_role.demote"

// thresholdsRelPath — per-home armed-threshold config, relative to COMPOUND_HOME. ABSENT = OFF.
const thresholdsRelPath = "config/lens-modulator-thresholds.json"

// routeStoreRelPath — the operator-local route/role adjustment store the modulator repoints through
// the governed act seam. Under COMPOUND_HOME, never the repo, never a tracked/public surface.
const routeStoreRelPath = "config/lane-route-adjustments.yaml"

// actionLedgerRelPath — append-only record of every governed demote/revert, relative to
// COMPOUND_HOME. It persists the act.Result refs so a later Revert can undo a demote.
const actionLedgerRelPath = "ledger/lens-modulator-actions.jsonl"

const (
	// publicLaneRoot is the SHARED route surface: public_safe-driven demotes land here, and this is
	// the only root a public route projection may read.
	publicLaneRoot = "lanes"
	// privateLaneRoot quarantines operator_private-driven demotes: operator-local only, never shared.
	privateLaneRoot = "operator_private_lanes"
)

// Threshold is one operator-armed rule: for lens LensID (optionally scoped to a single Subject), a
// recorded signal whose Value is at or above MinValue demotes its subject lane to DemoteTo. Baseline
// is the value a lane starts at if it has no entry yet.
type Threshold struct {
	LensID       string  `json:"lens_id"`
	SubjectScope string  `json:"subject_scope,omitempty"`
	MinValue     float64 `json:"min_value"`
	DemoteTo     string  `json:"demote_to"`
	Baseline     string  `json:"baseline,omitempty"`
}

// ThresholdConfig is the per-home set of armed thresholds. Empty/absent = modulator OFF.
type ThresholdConfig struct {
	SchemaVersion string      `json:"schema_version,omitempty"`
	Thresholds    []Threshold `json:"thresholds,omitempty"`
}

// Modulation is one lane the modulator matched. Applied is true iff a governed write happened;
// Refused is true iff policy declined (no allowing PolicyDecision). Visibility carries the driving
// signal's exposure class so the public projection can enforce the boundary on the row itself.
type Modulation struct {
	LensID       string                     `json:"lens_id"`
	Lane         string                     `json:"lane"`
	Root         string                     `json:"root"`
	Visibility   behaviorallens.Visibility  `json:"visibility"`
	SignalValue  float64                    `json:"signal_value"`
	Threshold    float64                    `json:"threshold"`
	DemoteTo     string                     `json:"demote_to"`
	Baseline     string                     `json:"baseline"`
	Applied      bool                       `json:"applied"`
	Refused      bool                       `json:"refused,omitempty"`
	Reason       string                     `json:"reason,omitempty"`
	PreImageRef  string                     `json:"pre_image_ref,omitempty"`
	PostImageRef string                     `json:"post_image_ref,omitempty"`
	ReceiptID    string                     `json:"receipt_id,omitempty"`
	TransitionID string                     `json:"transition_id,omitempty"`
	MoveType     string                     `json:"move_type,omitempty"`
}

// Skip records a matched-lens signal the modulator did NOT act on, and why (below threshold, not
// keyable, already demoted) — so the gate is legible, never silent.
type Skip struct {
	LensID     string                    `json:"lens_id"`
	Lane       string                    `json:"lane"`
	Visibility behaviorallens.Visibility `json:"visibility"`
	Reason     string                    `json:"reason"`
}

// ModulationResult is the outcome of one modulate/plan pass.
type ModulationResult struct {
	Modulations []Modulation `json:"modulations"`
	Skipped     []Skip       `json:"skipped,omitempty"`
}

// PublicProjection returns the shareable view: ONLY public_safe modulations. operator_private
// modulations are dropped BY CONSTRUCTION here, so their existence never leaves the home through a
// shared route projection.
func (r ModulationResult) PublicProjection() ModulationResult {
	out := ModulationResult{}
	for _, m := range r.Modulations {
		if m.Visibility == behaviorallens.VisibilityOperatorPrivate {
			continue
		}
		out.Modulations = append(out.Modulations, m)
	}
	for _, s := range r.Skipped {
		if s.Visibility == behaviorallens.VisibilityOperatorPrivate {
			continue
		}
		out.Skipped = append(out.Skipped, s)
	}
	return out
}

// RevertOutcome is the result of undoing a lane's latest demote.
type RevertOutcome struct {
	Lane          string `json:"lane"`
	Reverted      bool   `json:"reverted"`
	RestoredValue string `json:"restored_value,omitempty"`
	ReceiptID     string `json:"receipt_id,omitempty"`
	TransitionID  string `json:"transition_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// RouteStorePath is the operator-local route/role store path for a home. Exported so the operator's
// standing grant (and the CLI) can scope to exactly this target.
func RouteStorePath(home string) string {
	return filepath.Join(home, filepath.FromSlash(routeStoreRelPath))
}

func thresholdsPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(thresholdsRelPath))
}

func laneRoot(v behaviorallens.Visibility) string {
	if v == behaviorallens.VisibilityOperatorPrivate {
		return privateLaneRoot
	}
	return publicLaneRoot
}

// LoadThresholds reads a home's armed thresholds. An ABSENT file returns the zero config (OFF) — the
// fail-closed default a stranger clone gets.
func LoadThresholds(home string) (ThresholdConfig, error) {
	if strings.TrimSpace(home) == "" {
		return ThresholdConfig{}, fmt.Errorf("no COMPOUND_HOME")
	}
	data, err := os.ReadFile(thresholdsPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return ThresholdConfig{}, nil
		}
		return ThresholdConfig{}, err
	}
	var cfg ThresholdConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ThresholdConfig{}, fmt.Errorf("parse thresholds %s: %w", thresholdsPath(home), err)
	}
	if v := strings.TrimSpace(cfg.SchemaVersion); v != "" && v != ThresholdSchemaVersion {
		return ThresholdConfig{}, fmt.Errorf("thresholds schema_version=%q, want %q", v, ThresholdSchemaVersion)
	}
	return cfg, nil
}

func (t Threshold) validate() error {
	if strings.TrimSpace(t.LensID) == "" {
		return fmt.Errorf("threshold lens_id required")
	}
	if strings.TrimSpace(t.DemoteTo) == "" {
		return fmt.Errorf("threshold demote_to required")
	}
	return nil
}

// Arm appends a threshold and persists the config.
//
// NAIVE STEP: this version does NOT enforce the calibration guard — it arms even on an empty signal
// ledger, so TestArmRefusesWithoutMeasuredSignal is RED until the governed version gates it.
func Arm(home string, th Threshold, now time.Time) error {
	if err := th.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(th.Baseline) == "" {
		th.Baseline = "active"
	}
	cfg, err := LoadThresholds(home)
	if err != nil {
		return err
	}
	cfg.SchemaVersion = ThresholdSchemaVersion
	cfg.Thresholds = append(cfg.Thresholds, th)
	return writeThresholds(home, cfg)
}

func writeThresholds(home string, cfg ThresholdConfig) error {
	cfg.SchemaVersion = ThresholdSchemaVersion
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := thresholdsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// matchOne returns the first armed threshold that a signal crosses (lens id match, subject-scope
// match if set, value at or above MinValue).
func matchOne(cfg ThresholdConfig, sig behaviorallens.InferredSignal) (Threshold, bool) {
	for _, th := range cfg.Thresholds {
		if th.LensID != sig.LensID {
			continue
		}
		if s := strings.TrimSpace(th.SubjectScope); s != "" && s != sig.Subject {
			continue
		}
		if sig.Value >= th.MinValue {
			return th, true
		}
	}
	return Threshold{}, false
}

// Plan is the DRY-RUN default: it names the lanes that WOULD be demoted without touching the route
// store or evaluating policy. It writes nothing.
func Plan(home string, now time.Time) (ModulationResult, error) {
	cfg, err := LoadThresholds(home)
	if err != nil {
		return ModulationResult{}, err
	}
	var res ModulationResult
	if len(cfg.Thresholds) == 0 {
		return res, nil
	}
	signals, err := behaviorallens.LoadSignals(home)
	if err != nil {
		return ModulationResult{}, err
	}
	for _, sig := range signals {
		th, ok := matchOne(cfg, sig)
		if !ok {
			continue
		}
		if strings.Contains(sig.Subject, ".") {
			res.Skipped = append(res.Skipped, Skip{LensID: sig.LensID, Lane: sig.Subject, Visibility: sig.Visibility, Reason: "subject not route-store keyable (contains '.')"})
			continue
		}
		res.Modulations = append(res.Modulations, Modulation{
			LensID: sig.LensID, Lane: sig.Subject, Root: laneRoot(sig.Visibility), Visibility: sig.Visibility,
			SignalValue: sig.Value, Threshold: th.MinValue, DemoteTo: th.DemoteTo, Baseline: th.Baseline,
			Applied: false, Reason: "dry-run: would demote",
		})
	}
	return res, nil
}

// Modulate reads recorded signals and, for each crossing signal, applies a demote.
//
// NAIVE STEP: this version writes the demote DIRECTLY to the route store, ignoring policyCfg and the
// act seam entirely — so it mutates even with no allowing PolicyDecision. TestModulate* falsifiers
// are RED until the governed version routes every write through act.Execute.
func Modulate(home string, policyCfg policy.Config, workID string, now time.Time) (ModulationResult, error) {
	_ = policyCfg
	cfg, err := LoadThresholds(home)
	if err != nil {
		return ModulationResult{}, err
	}
	var res ModulationResult
	if len(cfg.Thresholds) == 0 {
		return res, nil
	}
	signals, err := behaviorallens.LoadSignals(home)
	if err != nil {
		return ModulationResult{}, err
	}
	for _, sig := range signals {
		th, ok := matchOne(cfg, sig)
		if !ok {
			continue
		}
		if strings.Contains(sig.Subject, ".") {
			res.Skipped = append(res.Skipped, Skip{LensID: sig.LensID, Lane: sig.Subject, Visibility: sig.Visibility, Reason: "subject not route-store keyable (contains '.')"})
			continue
		}
		root := laneRoot(sig.Visibility)
		if err := ensureLaneEntry(RouteStorePath(home), root, sig.Subject, th.Baseline); err != nil {
			return ModulationResult{}, err
		}
		// NAIVE: direct write, no pre-image capture, no receipt, no policy.
		if err := setLaneValue(RouteStorePath(home), root, sig.Subject, th.DemoteTo); err != nil {
			return ModulationResult{}, err
		}
		res.Modulations = append(res.Modulations, Modulation{
			LensID: sig.LensID, Lane: sig.Subject, Root: root, Visibility: sig.Visibility,
			SignalValue: sig.Value, Threshold: th.MinValue, DemoteTo: th.DemoteTo, Baseline: th.Baseline,
			Applied: true, Reason: "naive direct write",
		})
	}
	return res, nil
}

// Revert undoes a lane's latest demote.
//
// NAIVE STEP: restores the baseline directly (no governed act, no persisted-result consumption).
func Revert(home string, policyCfg policy.Config, lane, workID string, now time.Time) (RevertOutcome, error) {
	_ = policyCfg
	_ = workID
	for _, root := range []string{publicLaneRoot, privateLaneRoot} {
		if _, ok, err := currentValue(RouteStorePath(home), root, lane); err != nil {
			return RevertOutcome{}, err
		} else if ok {
			if err := setLaneValue(RouteStorePath(home), root, lane, "active"); err != nil {
				return RevertOutcome{}, err
			}
			return RevertOutcome{Lane: lane, Reverted: true, RestoredValue: "active"}, nil
		}
	}
	return RevertOutcome{Lane: lane, Reverted: false, Reason: "no demote record for lane"}, nil
}

// CurrentAdjustment reads the current route/role value for a lane under the visibility-appropriate
// key root. ok=false means no entry exists.
func CurrentAdjustment(home string, v behaviorallens.Visibility, lane string) (string, bool, error) {
	return currentValue(RouteStorePath(home), laneRoot(v), lane)
}

// --- route-store helpers (shared with the governed version) ---

func loadStore(path string) (map[string]any, error) {
	m := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse route store %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func saveStore(path string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ensureLaneEntry guarantees root.lane exists in the store, initialized to baseline if absent, so the
// governed ConfigKeyAdapter (which repoints, never creates) has a key to repoint.
func ensureLaneEntry(path, root, lane, baseline string) error {
	m, err := loadStore(path)
	if err != nil {
		return err
	}
	sub, _ := m[root].(map[string]any)
	if sub == nil {
		sub = map[string]any{}
		m[root] = sub
	}
	if _, ok := sub[lane]; ok {
		return nil
	}
	sub[lane] = baseline
	return saveStore(path, m)
}

func setLaneValue(path, root, lane, value string) error {
	m, err := loadStore(path)
	if err != nil {
		return err
	}
	sub, _ := m[root].(map[string]any)
	if sub == nil {
		sub = map[string]any{}
		m[root] = sub
	}
	sub[lane] = value
	return saveStore(path, m)
}

func currentValue(path, root, lane string) (string, bool, error) {
	m, err := loadStore(path)
	if err != nil {
		return "", false, err
	}
	sub, _ := m[root].(map[string]any)
	if sub == nil {
		return "", false, nil
	}
	v, ok := sub[lane]
	if !ok {
		return "", false, nil
	}
	return fmt.Sprintf("%v", v), true, nil
}

func idFor(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(sum[:])[:12]
}
