package routeevidence

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/work"
)

type LaneCapacityState string

const (
	CapacityAlive    LaneCapacityState = "alive"
	CapacityWalled   LaneCapacityState = "walled"
	CapacityExpiring LaneCapacityState = "expiring"
	CapacityUnknown  LaneCapacityState = "unknown"
	CapacityDead     LaneCapacityState = "dead"

	defaultExpectedMinutes   = 60
	capacityWallFreshness    = 6 * time.Hour
	capacityRecentCluster    = 30 * time.Minute
	capacityClockSkew        = 5 * time.Minute
	codexCapacityFreshness   = 15 * time.Minute
	claudeCapacityFreshness  = 15 * time.Minute
	zaiCapacityFreshness     = 30 * time.Minute
	capacityEvidenceProtocol = "capacity-snapshot"
	capacityEvidenceProfile  = "compound.capacity-veto"
	capacityRefusalStatus    = "capacity_refused"

	// laneAliasesRelPath is the home-relative path to the operator-owned lane-alias override file.
	laneAliasesRelPath = "projections/lane-aliases.json"
	// laneAliasesSchema versions the lane-alias override projection body.
	laneAliasesSchema = "compound.lane_aliases.v0.1"
)

type LaneCapacity struct {
	Remaining  float64           `json:"remaining"`
	ResetsAt   time.Time         `json:"resets_at,omitempty"`
	LastProven time.Time         `json:"last_proven,omitempty"`
	State      LaneCapacityState `json:"state"`
	Source     string            `json:"source,omitempty"`
	DataRef    string            `json:"data_ref,omitempty"`
	// SourceInstalled reports whether ANY configured capacity source file exists. Unknown while
	// unwired fails OPEN (warning only) — a fresh adopter home without CodexBar/quota/projection
	// must not demand --confirm forever; unknown while wired stays confirmation-gated.
	SourceInstalled bool `json:"source_installed"`
}

type LaneCapacityPaths struct {
	UsageHistoryPath      string
	QuotaCachePath        string
	AccountProjectionPath string
	// AliasesPath is the operator-owned lane-alias override projection
	// (`<COMPOUND_HOME>/projections/lane-aliases.json`). It is NOT a capacity source — it only
	// renames a lane to a family before the structural rules run — so it is deliberately excluded
	// from capacitySourceInstalled.
	AliasesPath string
}

type LaneCapacityConfig struct {
	Paths                   LaneCapacityPaths
	ExpectedMinutes         int
	SuppressRefusalEvidence bool
}

// DefaultLaneCapacityConfig points at the operator-owned read projections without embedding any
// personal absolute path. Tests use ReadLaneCapacityWithConfig with temp fixtures instead.
func DefaultLaneCapacityConfig(homePath string) LaneCapacityConfig {
	var paths LaneCapacityPaths
	if userHome, err := os.UserHomeDir(); err == nil {
		paths.UsageHistoryPath = filepath.Join(userHome, "Library", "Application Support", "CodexBar", "usage-history.jsonl")
		paths.QuotaCachePath = filepath.Join(userHome, ".codex", "multi-auth", "quota-cache.json")
	}
	if strings.TrimSpace(homePath) != "" {
		paths.AccountProjectionPath = AccountProjectionPath(homePath)
		paths.AliasesPath = LaneAliasesPath(homePath)
	}
	return LaneCapacityConfig{Paths: paths, ExpectedMinutes: defaultExpectedMinutes}
}

// LaneAliasesPath is the absolute path the operator-owned lane-alias override projection is read
// from (mirrors AccountProjectionPath's home-relative convention).
func LaneAliasesPath(homePath string) string {
	return filepath.Join(homePath, filepath.FromSlash(laneAliasesRelPath))
}

// ReadLaneCapacity is the production front door. COMPOUND_HOME is used only to locate Compound's
// own reduced account projection; callers that need deterministic paths use the config variant.
func ReadLaneCapacity(lane string, now time.Time) (LaneCapacity, error) {
	return ReadLaneCapacityWithConfig(lane, now, DefaultLaneCapacityConfig(os.Getenv("COMPOUND_HOME")))
}

func ReadLaneCapacityWithConfig(lane string, now time.Time, cfg LaneCapacityConfig) (LaneCapacity, error) {
	cfg = normalizeLaneCapacityConfig(cfg, "")
	key, aliasNote := resolveLaneCapacityKey(lane, cfg)
	installed := capacitySourceInstalled(cfg.Paths)
	capacity := LaneCapacity{State: CapacityUnknown, DataRef: "no matching capacity row", SourceInstalled: installed}
	if !installed {
		capacity.DataRef = "no capacity source installed"
	}
	if key == "" {
		if aliasNote != "" {
			capacity.DataRef = capacity.DataRef + "; " + aliasNote
		}
		return capacity, nil
	}

	usage, ok, err := readCodexBarCapacity(cfg.Paths.UsageHistoryPath, key, now, cfg.ExpectedMinutes)
	if err != nil {
		return LaneCapacity{}, err
	}
	if ok {
		capacity = usage
	} else if usage.DataRef != "" {
		// Annotated unknown: readCodexBarCapacity returns a non-empty LaneCapacity even when
		// no fresh row was found, so callers can tell WHY the lane is unknown — zero rows vs
		// only-stale rows. Carry the annotation forward; wall/dead checks below may still
		// override if their own fresh evidence arrives.
		capacity.DataRef = usage.DataRef
		if !usage.LastProven.IsZero() {
			capacity.LastProven = usage.LastProven
		}
		if usage.Source != "" {
			capacity.Source = usage.Source
		}
	}

	wall, ok, err := readFreshQuotaWall(cfg.Paths.QuotaCachePath, key, now)
	if err != nil {
		return LaneCapacity{}, err
	}
	if ok {
		wall.Remaining = capacity.Remaining
		wall.ResetsAt = capacity.ResetsAt
		capacity = wall
	}

	dead, ok, err := readDeadAccountProjection(cfg.Paths.AccountProjectionPath, key, now)
	if err != nil {
		return LaneCapacity{}, err
	}
	if ok {
		dead.Remaining = capacity.Remaining
		dead.ResetsAt = capacity.ResetsAt
		capacity = dead
	}
	capacity.SourceInstalled = installed
	// Compose malformed-alias note onto any final-unknown state. Earlier branches already
	// appended the note when key==""; this covers the resolved-but-stale branch where the
	// lane resolves to a family but only stale rows are available.
	if capacity.State == CapacityUnknown && aliasNote != "" && !strings.Contains(capacity.DataRef, aliasNote) {
		capacity.DataRef = capacity.DataRef + "; " + aliasNote
	}
	return capacity, nil
}

func capacitySourceInstalled(paths LaneCapacityPaths) bool {
	for _, path := range []string{paths.UsageHistoryPath, paths.QuotaCachePath, paths.AccountProjectionPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

type codexBarCapacityRow struct {
	Provider      string  `json:"provider"`
	WindowKind    string  `json:"windowKind"`
	WindowMinutes int     `json:"windowMinutes"`
	UsedPercent   float64 `json:"usedPercent"`
	ResetsAt      string  `json:"resetsAt"`
	SampledAt     string  `json:"sampledAt"`
	sampled       time.Time
}

func readCodexBarCapacity(path, lane string, now time.Time, expectedMinutes int) (LaneCapacity, bool, error) {
	if strings.TrimSpace(path) == "" {
		return LaneCapacity{}, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LaneCapacity{}, false, nil
		}
		return LaneCapacity{}, false, fmt.Errorf("read capacity usage history: %w", err)
	}
	defer file.Close()

	var rows []codexBarCapacityRow
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row codexBarCapacityRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if normalizeLaneCapacityKey(row.Provider) != lane || math.IsNaN(row.UsedPercent) || math.IsInf(row.UsedPercent, 0) {
			continue
		}
		sampled, err := time.Parse(time.RFC3339, strings.TrimSpace(row.SampledAt))
		if err != nil || sampled.After(now.Add(capacityClockSkew)) {
			continue
		}
		row.sampled = sampled
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return LaneCapacity{}, false, fmt.Errorf("scan capacity usage history: %w", err)
	}
	if len(rows) == 0 {
		return LaneCapacity{
			State:           CapacityUnknown,
			SourceInstalled: true,
			DataRef:         fmt.Sprintf("no usage rows for family: %s", lane),
		}, false, nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].sampled.After(rows[j].sampled) })
	newest := rows[0].sampled
	if age := now.Sub(newest); age < 0 || age > capacityFreshness(lane) {
		freshness := capacityFreshness(lane)
		return LaneCapacity{
			State:           CapacityUnknown,
			SourceInstalled: true,
			LastProven:      newest.UTC(),
			Source:          "codexbar/usage-history",
			DataRef: fmt.Sprintf("codexbar/usage-history:no_fresh_rows,family=%s,newest=%s,age=%dm,freshness=%dm",
				lane, newest.UTC().Format(time.RFC3339), int(age.Minutes()), int(freshness.Minutes())),
		}, false, nil
	}

	recentCutoff := newest.Add(-capacityRecentCluster)
	latestByWindow := map[string]codexBarCapacityRow{}
	for _, row := range rows {
		if row.sampled.Before(recentCutoff) {
			continue
		}
		group := fmt.Sprintf("%s|%d|%s", row.WindowKind, row.WindowMinutes, row.ResetsAt)
		if previous, ok := latestByWindow[group]; !ok || row.sampled.After(previous.sampled) {
			latestByWindow[group] = row
		}
	}
	var constraining codexBarCapacityRow
	found := false
	for _, row := range latestByWindow {
		if !found || row.UsedPercent > constraining.UsedPercent {
			constraining = row
			found = true
		}
	}
	if !found {
		return LaneCapacity{}, false, nil
	}

	remaining := 100 - constraining.UsedPercent
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	out := LaneCapacity{
		Remaining:  remaining,
		LastProven: constraining.sampled.UTC(),
		State:      CapacityUnknown,
		Source:     "codexbar/usage-history",
		DataRef: fmt.Sprintf(
			"codexbar:provider=%s,window=%s/%dm,sampled_at=%s",
			lane,
			strings.TrimSpace(constraining.WindowKind),
			constraining.WindowMinutes,
			constraining.sampled.UTC().Format(time.RFC3339),
		),
	}
	reset, err := time.Parse(time.RFC3339, strings.TrimSpace(constraining.ResetsAt))
	if err != nil || !reset.After(now) {
		return out, true, nil
	}
	out.ResetsAt = reset.UTC()
	switch {
	case constraining.UsedPercent >= 100:
		out.State = CapacityWalled
	case !reset.After(now.Add(time.Duration(expectedMinutes) * time.Minute)):
		out.State = CapacityExpiring
	default:
		out.State = CapacityAlive
	}
	return out, true, nil
}

func readFreshQuotaWall(path, lane string, now time.Time) (LaneCapacity, bool, error) {
	if lane != "codex" || strings.TrimSpace(path) == "" {
		return LaneCapacity{}, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LaneCapacity{}, false, nil
		}
		return LaneCapacity{}, false, fmt.Errorf("stat quota cache: %w", err)
	}
	observed := info.ModTime()
	age := now.Sub(observed)
	if age < -capacityClockSkew || age > capacityWallFreshness {
		return LaneCapacity{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LaneCapacity{}, false, fmt.Errorf("read quota cache: %w", err)
	}
	var cache struct {
		ByAccountID map[string]struct {
			Status int `json:"status"`
		} `json:"byAccountId"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return LaneCapacity{}, false, fmt.Errorf("parse quota cache: %w", err)
	}
	for _, account := range cache.ByAccountID {
		if account.Status == 429 {
			return LaneCapacity{
				State:      CapacityWalled,
				LastProven: observed.UTC(),
				Source:     "codex-multi-auth/quota-cache",
				DataRef:    "quota-cache:status=429,modified_at=" + observed.UTC().Format(time.RFC3339),
			}, true, nil
		}
	}
	return LaneCapacity{}, false, nil
}

func readDeadAccountProjection(path, lane string, now time.Time) (LaneCapacity, bool, error) {
	if strings.TrimSpace(path) == "" {
		return LaneCapacity{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LaneCapacity{}, false, nil
		}
		return LaneCapacity{}, false, fmt.Errorf("read account projection: %w", err)
	}
	var projection AccountProjection
	if err := json.Unmarshal(data, &projection); err != nil {
		return LaneCapacity{}, false, fmt.Errorf("parse account projection: %w", err)
	}
	importedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(projection.ImportedAt))
	if err != nil {
		return LaneCapacity{}, false, nil
	}
	age := now.Sub(importedAt)
	if age < -capacityClockSkew || age > accountProjectionFreshnessWindow {
		return LaneCapacity{}, false, nil
	}
	entry, ok := projection.Providers[lane]
	if !ok || entry.TotalAccountCount <= 0 || entry.HealthyAccountCount > 0 {
		return LaneCapacity{}, false, nil
	}
	observed := importedAt
	if sampled, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.FreshestSampledAt)); err == nil && !sampled.After(now.Add(capacityClockSkew)) {
		observed = sampled
	}
	return LaneCapacity{
		State:      CapacityDead,
		LastProven: observed.UTC(),
		Source:     "switchback/account-projection",
		DataRef: fmt.Sprintf(
			"account-projection:provider=%s,healthy=%d,total=%d,observed_at=%s",
			lane,
			entry.HealthyAccountCount,
			entry.TotalAccountCount,
			observed.UTC().Format(time.RFC3339),
		),
	}, true, nil
}

type LaneCapacityDecision struct {
	Capacity             LaneCapacity
	Refused              bool
	ConfirmationRequired bool
	Message              string
	AlternativeLadder    []string
}

func GuardLaneCapacity(homePath, lane, capabilityClass string, now time.Time, confirm bool, alternatives []string, cfg LaneCapacityConfig) (LaneCapacityDecision, error) {
	cfg = normalizeLaneCapacityConfig(cfg, homePath)
	capacity, err := ReadLaneCapacityWithConfig(lane, now, cfg)
	if err != nil {
		return LaneCapacityDecision{}, err
	}
	alternatives = capacityAlternativeLadder(homePath, capabilityClass, alternatives)
	decision := LaneCapacityDecision{
		Capacity:          capacity,
		AlternativeLadder: alternatives,
	}
	switch capacity.State {
	case CapacityWalled, CapacityDead:
		decision.Refused = true
		decision.Message = capacityDecisionMessage(lane, capacity, alternatives, "refused")
		if !cfg.SuppressRefusalEvidence {
			if err := recordCapacityRefusal(homePath, lane, capacity, now); err != nil {
				return LaneCapacityDecision{}, err
			}
		}
	case CapacityUnknown, CapacityExpiring:
		if capacity.State == CapacityUnknown && !capacity.SourceInstalled {
			// Fail open when UNWIRED: no capacity source exists at all (fresh adopter home).
			// Annotate and warn; demanding --confirm forever would gate every dispatch on a
			// feed the home never had. Wired-but-blind lanes stay confirmation-gated below.
			decision.Message = capacityDecisionMessage(lane, capacity, alternatives, "unguarded (no capacity source installed)")
			break
		}
		if confirm {
			decision.Message = capacityDecisionMessage(lane, capacity, alternatives, "admitted by --confirm")
		} else {
			decision.ConfirmationRequired = true
			decision.Message = capacityDecisionMessage(lane, capacity, alternatives, "--confirm required")
		}
	}
	return decision, nil
}

func WorkLaneCapacityGuard(cfg LaneCapacityConfig) work.LaneCapacityGuardFunc {
	return func(req work.LaneCapacityGuardRequest) (work.LaneCapacityGuardResult, error) {
		effective := cfg
		effective.ExpectedMinutes = req.ExpectedMinutes
		effective.SuppressRefusalEvidence = req.ReadOnly
		decision, err := GuardLaneCapacity(
			req.HomePath,
			req.Lane,
			req.CapabilityClass,
			req.Now,
			req.Confirm,
			req.Alternatives,
			effective,
		)
		if err != nil {
			return work.LaneCapacityGuardResult{}, err
		}
		return work.LaneCapacityGuardResult{
			Capacity: work.DispatchLaneCapacity{
				Remaining:  decision.Capacity.Remaining,
				ResetsAt:   formatCapacityTime(decision.Capacity.ResetsAt),
				LastProven: formatCapacityTime(decision.Capacity.LastProven),
				State:      string(decision.Capacity.State),
				DataRef:    decision.Capacity.DataRef,
			},
			Refused:              decision.Refused,
			ConfirmationRequired: decision.ConfirmationRequired,
			Message:              decision.Message,
		}, nil
	}
}

func init() {
	work.LaneCapacityGuard = WorkLaneCapacityGuard(LaneCapacityConfig{})
}

func normalizeLaneCapacityConfig(cfg LaneCapacityConfig, homePath string) LaneCapacityConfig {
	defaults := DefaultLaneCapacityConfig(homePath)
	if strings.TrimSpace(cfg.Paths.UsageHistoryPath) == "" {
		cfg.Paths.UsageHistoryPath = defaults.Paths.UsageHistoryPath
	}
	if strings.TrimSpace(cfg.Paths.QuotaCachePath) == "" {
		cfg.Paths.QuotaCachePath = defaults.Paths.QuotaCachePath
	}
	if strings.TrimSpace(cfg.Paths.AccountProjectionPath) == "" {
		cfg.Paths.AccountProjectionPath = defaults.Paths.AccountProjectionPath
	}
	if strings.TrimSpace(cfg.Paths.AliasesPath) == "" {
		cfg.Paths.AliasesPath = defaults.Paths.AliasesPath
	}
	if cfg.ExpectedMinutes <= 0 {
		cfg.ExpectedMinutes = defaultExpectedMinutes
	}
	return cfg
}

// exactLaneCapacityFamily maps a literal, already-lowercased/trimmed lane name to its capacity
// family. It returns "" for anything not one of the known exact names — the caller decides what
// to do next (retry on a stripped base, infer, or give up).
func exactLaneCapacityFamily(value string) string {
	switch value {
	case "codex":
		return "codex"
	case "claude", "claude-code", "anthropic":
		return "claude"
	case "zai", "z.ai", "glm":
		return "zai"
	case "qwen":
		return "qwen"
	case "minimax":
		return "minimax"
	default:
		return ""
	}
}

// inferLaneCapacityFamily applies structural family inference to an already sub-lane-stripped,
// lowercased base name. Precedence: zai/glm beats qwen/minimax beats generic codex/claude
// prefixes — so "claude-zai" resolves to zai, "claude-qwen" resolves to qwen, and
// "claude-minimax" resolves to minimax, never the generic claude row.
func inferLaneCapacityFamily(base string) string {
	switch {
	case strings.Contains(base, "zai") || strings.Contains(base, "glm"):
		return "zai"
	case strings.Contains(base, "minimax"):
		return "minimax"
	case strings.Contains(base, "qwen"):
		return "qwen"
	case strings.HasPrefix(base, "codex"):
		return "codex"
	case strings.HasPrefix(base, "claude"), strings.HasPrefix(base, "anthropic"):
		return "claude"
	default:
		return ""
	}
}

// normalizeLaneCapacityKey is a PURE function: structural rules only, no config/IO. Order:
//  1. exact switch on the full (lowercased, trimmed) value — unchanged legacy semantics;
//  2. strip everything from the first ':' (sub-lane suffix) and retry the exact switch on the base;
//  3. family inference (prefix/contains) on that stripped base.
//
// Real dispatch lane names carry sub-lane suffixes (`claude-main:sub-lane`) or operator-chosen
// prefixes (`claude-opus-x`) that the legacy 3-entry switch never recognized — this is the
// structural half of lane-capacity-alias resolution; resolveLaneCapacityKey layers the
// operator-owned alias override on top of it.
func normalizeLaneCapacityKey(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if key := exactLaneCapacityFamily(trimmed); key != "" {
		return key
	}
	base := trimmed
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[:idx]
	}
	if key := exactLaneCapacityFamily(base); key != "" {
		return key
	}
	return inferLaneCapacityFamily(base)
}

// laneAliasesFile is the on-disk shape of the operator-owned lane-alias override projection
// (`<COMPOUND_HOME>/projections/lane-aliases.json`). It is config, not a governed registry: a
// missing file is a no-op (fail open); nothing here is append-only evidence.
type laneAliasesFile struct {
	SchemaVersion string            `json:"schema_version"`
	Aliases       map[string]string `json:"aliases"`
}

// loadLaneCapacityAliases reads and normalizes the lane-alias override file. A missing file
// returns (nil, nil) — fail open, no overrides. A malformed file returns a non-nil error; the
// caller decides whether to surface that (only when the lane would otherwise resolve unknown) —
// this function itself never swallows the distinction between "no file" and "bad file".
func loadLaneCapacityAliases(path string) (map[string]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read lane aliases: %w", err)
	}
	var file laneAliasesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse lane aliases: %w", err)
	}
	aliases := make(map[string]string, len(file.Aliases))
	for lane, family := range file.Aliases {
		lane = strings.ToLower(strings.TrimSpace(lane))
		family = strings.ToLower(strings.TrimSpace(family))
		if lane == "" || family == "" {
			continue
		}
		aliases[lane] = family
	}
	return aliases, nil
}

// resolveLaneCapacityKey layers the operator-owned alias override on top of the pure structural
// normalizeLaneCapacityKey rules. Precedence: an exact built-in switch hit (the literal family
// name, or its sub-lane-stripped base) is never overridden by an alias; short of that, an alias
// match (checked on the full lowercased/trimmed name, then the ':'-stripped base) beats family
// inference; a missing alias file is a no-op; a malformed alias file behaves like "no aliases"
// but is reported via the returned note so ReadLaneCapacityWithConfig can surface it — composed
// onto the DataRef only when the final state is unknown, per the packet's "no silent swallow,
// no fleet-wide error either" rule. The alias file is loaded eagerly (even when a structural
// match is found) so a malformed file can still surface on structurally-resolved lanes when
// downstream evidence leaves the state unknown.
func resolveLaneCapacityKey(lane string, cfg LaneCapacityConfig) (key string, unreadableAliasNote string) {
	trimmed := strings.ToLower(strings.TrimSpace(lane))
	if key := exactLaneCapacityFamily(trimmed); key != "" {
		return aliasObservation(key, "", cfg)
	}
	base := trimmed
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[:idx]
	}
	if key := exactLaneCapacityFamily(base); key != "" {
		return aliasObservation(key, "", cfg)
	}

	aliases, err := loadLaneCapacityAliases(cfg.Paths.AliasesPath)
	if err == nil {
		if family, ok := aliases[trimmed]; ok {
			return family, ""
		}
		if family, ok := aliases[base]; ok {
			return family, ""
		}
	}

	inferred := inferLaneCapacityFamily(base)
	if inferred == "" && err != nil {
		return "", "lane-aliases.json unreadable"
	}
	if err != nil {
		return inferred, "lane-aliases.json unreadable"
	}
	return inferred, ""
}

// aliasObservation loads the alias file (if any) for a structurally-resolved key purely to
// observe whether it is malformed, so the unreadable-note can compose onto a final-unknown
// DataRef downstream. The note is returned only when the file exists but fails to parse;
// a missing file (IsNotExist) or a successfully-parsed file produces no note.
func aliasObservation(key string, _ string, cfg LaneCapacityConfig) (string, string) {
	aliases, err := loadLaneCapacityAliases(cfg.Paths.AliasesPath)
	if err != nil {
		return key, "lane-aliases.json unreadable"
	}
	_ = aliases
	return key, ""
}

func capacityFreshness(lane string) time.Duration {
	switch lane {
	case "zai":
		return zaiCapacityFreshness
	case "claude":
		return claudeCapacityFreshness
	default:
		return codexCapacityFreshness
	}
}

func capacityAlternativeLadder(homePath, capabilityClass string, provided []string) []string {
	if len(provided) > 0 {
		return append([]string(nil), provided...)
	}
	class := strings.TrimSpace(capabilityClass)
	if class == "" {
		return nil
	}
	manifest, _, _, err := loadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return nil
	}
	return append([]string(nil), manifest.RoutingDefaults[class]...)
}

func capacityDecisionMessage(lane string, capacity LaneCapacity, alternatives []string, outcome string) string {
	timestamp := "unknown"
	if !capacity.LastProven.IsZero() {
		timestamp = capacity.LastProven.UTC().Format(time.RFC3339)
	}
	ladder := "none recorded"
	if len(alternatives) > 0 {
		ladder = strings.Join(alternatives, " -> ")
	}
	return fmt.Sprintf(
		"lane %s capacity %s: state=%s data_row_timestamp=%s data_ref=%s alternative_ladder=[%s]",
		strings.TrimSpace(lane),
		outcome,
		capacity.State,
		timestamp,
		capacity.DataRef,
		ladder,
	)
}

func recordCapacityRefusal(homePath, lane string, capacity LaneCapacity, now time.Time) error {
	key := normalizeLaneCapacityKey(lane)
	if key == "" {
		key = strings.TrimSpace(lane)
	}
	rowTimestamp := formatCapacityTime(capacity.LastProven)
	requestID := idFor("capacity_refusal_", key, string(capacity.State), rowTimestamp, now.UTC().Format(time.RFC3339))
	evidence, err := Build(BuildInput{
		CreatedAt:     now,
		RequestID:     requestID,
		ClientProfile: capacityEvidenceProfile,
		Protocol:      capacityEvidenceProtocol,
		Provider:      key,
		Model:         key,
		Status:        capacityRefusalStatus,
		EvidenceRefs: []string{
			"capacity-state:" + string(capacity.State),
			"capacity-row:" + orCapacityUnknown(rowTimestamp),
			"capacity-source:" + orCapacityUnknown(capacity.Source),
		},
	})
	if err != nil {
		return err
	}
	return RecordEvidence(homePath, evidence)
}

func formatCapacityTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func orCapacityUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
