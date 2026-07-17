// Package statussurface reduces harness-native context and capacity metadata into one adaptive,
// body-free projection for terminal status surfaces. Harnesses remain authoritative for live
// context and routeevidence remains authoritative for capacity.
package statussurface

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersion = "compound.status_surface.v0.1"
	defaultTTL    = 2 * time.Minute

	ThemePAINexus = "pai-nexus"
	ThemePAIFocus = "pai-focus"
	ThemePAIOps   = "pai-ops"
	ThemePAIMono  = "pai-mono"
	ThemeAuto     = "auto"
)

type Harness string

const (
	HarnessClaude Harness = "claude"
	HarnessCodex  Harness = "codex"
)

type MetricStatus string

const (
	StatusLive    MetricStatus = "live"
	StatusStale   MetricStatus = "stale"
	StatusUnknown MetricStatus = "unknown"
)

type Mode string

const (
	ModeFlow     Mode = "flow"
	ModeWatch    Mode = "watch"
	ModeConserve Mode = "conserve"
	ModeCritical Mode = "critical"
	ModeUnknown  Mode = "unknown"
)

type RouteGuidance string

const (
	GuidanceNormal           RouteGuidance = "normal"
	GuidancePreferEfficient  RouteGuidance = "prefer_efficient"
	GuidancePreservePrimary  RouteGuidance = "preserve_primary"
	GuidanceRouteAlternative RouteGuidance = "route_alternative"
	GuidanceConfirmCapacity  RouteGuidance = "confirm_capacity"
)

// InferenceClass selects an information profile, not merely a color palette. It is derived
// deterministically from current capacity state and can be overridden by choosing a concrete theme.
type InferenceClass string

const (
	InferenceFocus    InferenceClass = "focus"
	InferenceBalanced InferenceClass = "balanced"
	InferenceCapacity InferenceClass = "capacity"
	InferenceFallback InferenceClass = "fallback"
)

type LimitKind string

const (
	LimitFiveHour       LimitKind = "five_hour"
	LimitSevenDay       LimitKind = "seven_day"
	LimitSevenDayOpus   LimitKind = "seven_day_opus"
	LimitSevenDaySonnet LimitKind = "seven_day_sonnet"
)

type Metric struct {
	Status           MetricStatus `json:"status"`
	UsedPercent      float64      `json:"used_percent,omitempty"`
	RemainingPercent float64      `json:"remaining_percent,omitempty"`
	WindowTokens     int64        `json:"window_tokens,omitempty"`
	Source           string       `json:"source,omitempty"`
}

type LimitWindow struct {
	Kind             LimitKind    `json:"kind"`
	Status           MetricStatus `json:"status"`
	UsedPercent      float64      `json:"used_percent,omitempty"`
	RemainingPercent float64      `json:"remaining_percent,omitempty"`
	ResetsAt         time.Time    `json:"resets_at,omitempty"`
	Source           string       `json:"source,omitempty"`
}

type Snapshot struct {
	SchemaVersion    string         `json:"schema_version"`
	GeneratedAt      time.Time      `json:"generated_at"`
	FreshUntil       time.Time      `json:"fresh_until,omitempty"`
	Harness          Harness        `json:"harness"`
	Model            string         `json:"model,omitempty"`
	Context          Metric         `json:"context"`
	Limits           []LimitWindow  `json:"limits"`
	Mode             Mode           `json:"mode"`
	RouteGuidance    RouteGuidance  `json:"route_guidance"`
	InferenceClass   InferenceClass `json:"inference_class"`
	RecommendedTheme string         `json:"recommended_theme"`
	InputRetention   string         `json:"input_retention"`
}

// SelectMode applies entry thresholds plus exit hysteresis. The prior mode only delays a
// downgrade; fresh evidence can always escalate immediately.
func SelectMode(primary Metric, additional []Metric, previous Mode) Mode {
	worst := -1.0
	for _, metric := range append([]Metric{primary}, additional...) {
		if metric.Status == StatusLive && metric.UsedPercent > worst {
			worst = metric.UsedPercent
		}
	}
	if worst < 0 {
		return ModeUnknown
	}
	switch {
	case worst >= 90:
		return ModeCritical
	case previous == ModeCritical && worst >= 82:
		return ModeCritical
	case worst >= 75:
		return ModeConserve
	case previous == ModeConserve && worst >= 68:
		return ModeConserve
	case worst >= 50:
		return ModeWatch
	case previous == ModeWatch && worst >= 45:
		return ModeWatch
	default:
		return ModeFlow
	}
}

func guidanceForMode(mode Mode) RouteGuidance {
	switch mode {
	case ModeFlow:
		return GuidanceNormal
	case ModeWatch:
		return GuidancePreferEfficient
	case ModeConserve:
		return GuidancePreservePrimary
	case ModeCritical:
		return GuidanceRouteAlternative
	default:
		return GuidanceConfirmCapacity
	}
}

func InferDisplayClass(mode Mode) (InferenceClass, string) {
	switch mode {
	case ModeFlow:
		return InferenceFocus, ThemePAIFocus
	case ModeWatch:
		return InferenceBalanced, ThemePAINexus
	case ModeConserve, ModeCritical:
		return InferenceCapacity, ThemePAIOps
	default:
		return InferenceFallback, ThemePAIMono
	}
}

type claudeStatusInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow *struct {
		WindowTokens int64   `json:"context_window_size"`
		UsedPercent  float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour       *nativeLimit `json:"five_hour"`
		SevenDay       *nativeLimit `json:"seven_day"`
		SevenDayOpus   *nativeLimit `json:"seven_day_opus"`
		SevenDaySonnet *nativeLimit `json:"seven_day_sonnet"`
	} `json:"rate_limits"`
}

type nativeLimit struct {
	UsedPercent *float64        `json:"used_percentage"`
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

func BuildClaudeSnapshot(body []byte, now time.Time, previous Snapshot) (Snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var input claudeStatusInput
	if err := decoder.Decode(&input); err != nil {
		return Snapshot{}, fmt.Errorf("decode Claude status input: %w", err)
	}

	snapshot := Snapshot{
		SchemaVersion:  SchemaVersion,
		GeneratedAt:    now.UTC(),
		Harness:        HarnessClaude,
		Model:          safeLabel(input.Model.DisplayName, 80),
		Context:        Metric{Status: StatusUnknown, Source: "claude/statusline.context_window"},
		Limits:         []LimitWindow{},
		InputRetention: "derived_metadata_only;raw_discarded",
	}
	if input.ContextWindow != nil && finitePercent(input.ContextWindow.UsedPercent) {
		used := clampPercent(input.ContextWindow.UsedPercent)
		snapshot.Context = Metric{
			Status:           StatusLive,
			UsedPercent:      used,
			RemainingPercent: 100 - used,
			WindowTokens:     input.ContextWindow.WindowTokens,
			Source:           "claude/statusline.context_window",
		}
	}
	if input.RateLimits != nil {
		for _, pair := range []struct {
			kind  LimitKind
			value *nativeLimit
		}{
			{LimitFiveHour, input.RateLimits.FiveHour},
			{LimitSevenDay, input.RateLimits.SevenDay},
			{LimitSevenDayOpus, input.RateLimits.SevenDayOpus},
			{LimitSevenDaySonnet, input.RateLimits.SevenDaySonnet},
		} {
			if pair.value != nil {
				snapshot.Limits = append(snapshot.Limits, reduceNativeLimit(pair.kind, pair.value))
			}
		}
	}
	additional := make([]Metric, 0, len(snapshot.Limits))
	for _, limit := range snapshot.Limits {
		additional = append(additional, Metric{Status: limit.Status, UsedPercent: limit.UsedPercent, RemainingPercent: limit.RemainingPercent})
	}
	previousMode := ModeUnknown
	if previous.SchemaVersion == SchemaVersion && previous.Harness == HarnessClaude {
		previousMode = previous.Mode
	}
	snapshot.Mode = SelectMode(snapshot.Context, additional, previousMode)
	snapshot.RouteGuidance = guidanceForMode(snapshot.Mode)
	snapshot.InferenceClass, snapshot.RecommendedTheme = InferDisplayClass(snapshot.Mode)
	return snapshot, nil
}

func reduceNativeLimit(kind LimitKind, input *nativeLimit) LimitWindow {
	out := LimitWindow{Kind: kind, Status: StatusUnknown, Source: "claude/statusline.rate_limits"}
	value := input.UsedPercent
	if value == nil {
		value = input.Utilization
	}
	if value != nil && finitePercent(*value) {
		out.Status = StatusLive
		out.UsedPercent = clampPercent(*value)
		out.RemainingPercent = 100 - out.UsedPercent
	}
	out.ResetsAt = parseNativeReset(input.ResetsAt)
	return out
}

func parseNativeReset(raw json.RawMessage) time.Time {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return time.Time{}
	}
	if strings.HasPrefix(trimmed, "\"") {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
				return parsed.UTC()
			}
		}
		return time.Time{}
	}
	epoch, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return time.Time{}
	}
	if epoch > 10_000_000_000 {
		epoch /= 1000
	}
	return time.Unix(epoch, 0).UTC()
}

func finitePercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func safeLabel(value string, maxRunes int) string {
	var out []rune
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsControl(r) {
			continue
		}
		out = append(out, r)
		if len(out) == maxRunes {
			break
		}
	}
	return string(out)
}

type Store struct {
	Home string
	TTL  time.Duration
}

func SnapshotPath(home string, harness Harness) string {
	return filepath.Join(home, "projections", "status-surface", string(harness)+".json")
}

func (s Store) Write(snapshot Snapshot) error {
	if snapshot.SchemaVersion != SchemaVersion {
		return fmt.Errorf("status surface schema = %q, want %q", snapshot.SchemaVersion, SchemaVersion)
	}
	if snapshot.Harness != HarnessClaude && snapshot.Harness != HarnessCodex {
		return fmt.Errorf("unsupported status surface harness %q", snapshot.Harness)
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	snapshot.FreshUntil = snapshot.GeneratedAt.Add(ttl).UTC()
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	path := SnapshotPath(s.Home, snapshot.Harness)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (s Store) ReadFresh(harness Harness, now time.Time) (Snapshot, bool, error) {
	body, err := os.ReadFile(SnapshotPath(s.Home, harness))
	if errors.Is(err, os.ErrNotExist) {
		return unknownSnapshot(harness, now), false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return Snapshot{}, false, fmt.Errorf("decode status surface snapshot: %w", err)
	}
	if snapshot.SchemaVersion != SchemaVersion || snapshot.Harness != harness {
		return Snapshot{}, false, fmt.Errorf("status surface snapshot identity mismatch")
	}
	freshUntil := snapshot.FreshUntil
	if freshUntil.IsZero() {
		ttl := s.TTL
		if ttl <= 0 {
			ttl = defaultTTL
		}
		freshUntil = snapshot.GeneratedAt.Add(ttl)
	}
	if now.After(freshUntil) {
		snapshot.Mode = ModeUnknown
		snapshot.RouteGuidance = GuidanceConfirmCapacity
		snapshot.InferenceClass, snapshot.RecommendedTheme = InferDisplayClass(ModeUnknown)
		if snapshot.Context.Status == StatusLive {
			snapshot.Context.Status = StatusStale
		}
		for i := range snapshot.Limits {
			if snapshot.Limits[i].Status == StatusLive {
				snapshot.Limits[i].Status = StatusStale
			}
		}
		return snapshot, false, nil
	}
	return snapshot, true, nil
}

func unknownSnapshot(harness Harness, now time.Time) Snapshot {
	return Snapshot{SchemaVersion: SchemaVersion, GeneratedAt: now.UTC(), Harness: harness,
		Context: Metric{Status: StatusUnknown}, Limits: []LimitWindow{}, Mode: ModeUnknown,
		RouteGuidance: GuidanceConfirmCapacity, InferenceClass: InferenceFallback,
		RecommendedTheme: ThemePAIMono, InputRetention: "derived_metadata_only;raw_discarded"}
}

type RenderOptions struct {
	Theme string
	Width int
	Color bool
}

func ThemeIDs() []string {
	return []string{ThemePAINexus, ThemePAIFocus, ThemePAIOps, ThemePAIMono}
}

func Render(snapshot Snapshot, options RenderOptions) (string, error) {
	if options.Theme == "" || options.Theme == ThemeAuto {
		options.Theme = snapshot.RecommendedTheme
		if options.Theme == "" {
			_, options.Theme = InferDisplayClass(snapshot.Mode)
		}
	}
	if !validTheme(options.Theme) {
		return "", fmt.Errorf("unknown status surface theme %q", options.Theme)
	}
	ctx := percentText(snapshot.Context)
	five := limitText(snapshot.Limits, LimitFiveHour)
	week := limitText(snapshot.Limits, LimitSevenDay)
	mode := strings.ToUpper(string(snapshot.Mode))
	if mode == "" {
		mode = strings.ToUpper(string(ModeUnknown))
	}
	var line string
	switch options.Theme {
	case ThemePAINexus:
		line = fmt.Sprintf("◈ %s · ctx %s · 5h %s · wk %s · %s", strings.ToUpper(string(snapshot.Harness)), ctx, five, week, mode)
	case ThemePAIFocus:
		line = fmt.Sprintf("ctx %s · wk %s · %s", ctx, week, mode)
	case ThemePAIOps:
		line = fmt.Sprintf("[%s] ctx %s | 5h %s | wk %s", mode, ctx, five, week)
	case ThemePAIMono:
		line = fmt.Sprintf("%s ctx %s 5h %s wk %s %s", strings.ToUpper(string(snapshot.Harness)), ctx, five, week, mode)
	}
	if options.Width > 0 {
		line = truncateDisplay(line, options.Width)
	}
	if options.Color && options.Theme != ThemePAIMono {
		line = colorForMode(snapshot.Mode) + line + "\x1b[0m"
	}
	return line, nil
}

func validTheme(theme string) bool {
	for _, candidate := range ThemeIDs() {
		if theme == candidate {
			return true
		}
	}
	return false
}

func percentText(metric Metric) string {
	if metric.Status != StatusLive {
		return "?"
	}
	return fmt.Sprintf("%.0f%%", math.Round(metric.RemainingPercent))
}

func limitText(limits []LimitWindow, kind LimitKind) string {
	for _, limit := range limits {
		if limit.Kind == kind {
			if limit.Status != StatusLive {
				return "?"
			}
			return fmt.Sprintf("%.0f%%", math.Round(limit.RemainingPercent))
		}
	}
	return "?"
}

func colorForMode(mode Mode) string {
	switch mode {
	case ModeFlow:
		return "\x1b[38;5;81m"
	case ModeWatch:
		return "\x1b[38;5;220m"
	case ModeConserve:
		return "\x1b[38;5;208m"
	case ModeCritical:
		return "\x1b[38;5;196m"
	default:
		return "\x1b[38;5;244m"
	}
}

func truncateDisplay(value string, width int) string {
	if width <= 0 || utf8.RuneCountInString(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}

func displayWidth(value string) int {
	plain := make([]rune, 0, len(value))
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && runes[i] != 'm' {
				i++
			}
			continue
		}
		plain = append(plain, runes[i])
	}
	return len(plain)
}

type codexPalette struct {
	Name, Background, Foreground, Accent, Muted, Warning string
}

func RenderCodexTheme(theme string) (string, error) {
	palettes := map[string]codexPalette{
		ThemePAINexus: {"PAI Nexus", "#08131A", "#D8F3F0", "#46D9C5", "#6F8F96", "#FFB454"},
		ThemePAIFocus: {"PAI Focus", "#111512", "#E5EBD8", "#A8D26D", "#77806E", "#E6C36A"},
		ThemePAIOps:   {"PAI Ops", "#0E1116", "#DDE5EF", "#63A9FF", "#778292", "#FF9D5C"},
		ThemePAIMono:  {"PAI Mono", "#101010", "#E6E6E6", "#FFFFFF", "#858585", "#C8C8C8"},
	}
	palette, ok := palettes[theme]
	if !ok {
		return "", fmt.Errorf("unknown Codex theme %q", theme)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>name</key><string>%s</string>
  <key>settings</key>
  <array>
    <dict><key>settings</key><dict><key>background</key><string>%s</string><key>foreground</key><string>%s</string><key>caret</key><string>%s</string><key>selection</key><string>%s</string></dict></dict>
    <dict><key>scope</key><string>comment, punctuation.definition.comment</string><key>settings</key><dict><key>foreground</key><string>%s</string></dict></dict>
    <dict><key>scope</key><string>keyword, storage, entity.name.function, markup.heading</string><key>settings</key><dict><key>foreground</key><string>%s</string></dict></dict>
    <dict><key>scope</key><string>invalid, message.error, markup.deleted</string><key>settings</key><dict><key>foreground</key><string>%s</string></dict></dict>
  </array>
</dict>
</plist>
`, palette.Name, palette.Background, palette.Foreground, palette.Foreground, palette.Accent+"55", palette.Muted, palette.Accent, palette.Warning), nil
}
