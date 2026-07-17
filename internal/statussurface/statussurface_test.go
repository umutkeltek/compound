package statussurface

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

var testNow = time.Date(2026, 7, 17, 1, 15, 0, 0, time.UTC)

const claudeFixture = `{
  "session_id": "session-must-not-survive",
  "message": "BODY_MUST_NOT_SURVIVE",
  "model": {"display_name": "Opus 4.8"},
  "workspace": {"current_dir": "/private/project"},
  "context_window": {
    "context_window_size": 200000,
    "used_percentage": 41.5,
    "total_input_tokens": 83000
  },
  "rate_limits": {
    "five_hour": {"used_percentage": 34, "resets_at": "2026-07-17T04:00:00Z"},
    "seven_day": {"utilization": 82, "resets_at": 946684800},
    "seven_day_opus": {"used_percentage": 51},
    "seven_day_sonnet": {"used_percentage": 12}
  }
}`

func TestBuildClaudeSnapshotReducesNativeContextAndLimitsWithoutBody(t *testing.T) {
	snapshot, err := BuildClaudeSnapshot([]byte(claudeFixture), testNow, Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != SchemaVersion || snapshot.Harness != HarnessClaude {
		t.Fatalf("identity = %+v", snapshot)
	}
	if snapshot.Context.Status != StatusLive || snapshot.Context.UsedPercent != 41.5 || snapshot.Context.RemainingPercent != 58.5 {
		t.Fatalf("context = %+v", snapshot.Context)
	}
	if snapshot.Context.WindowTokens != 200000 || snapshot.Model != "Opus 4.8" {
		t.Fatalf("model/context window = %q/%d", snapshot.Model, snapshot.Context.WindowTokens)
	}
	if got := limitByKind(t, snapshot, LimitFiveHour); got.UsedPercent != 34 || got.RemainingPercent != 66 || got.Status != StatusLive {
		t.Fatalf("five hour = %+v", got)
	}
	if got := limitByKind(t, snapshot, LimitSevenDay); got.UsedPercent != 82 || got.RemainingPercent != 18 || got.ResetsAt.IsZero() {
		t.Fatalf("seven day = %+v", got)
	}
	if snapshot.Mode != ModeConserve || snapshot.RouteGuidance != GuidancePreservePrimary {
		t.Fatalf("adaptive state = mode %q guidance %q", snapshot.Mode, snapshot.RouteGuidance)
	}
	if snapshot.InferenceClass != InferenceCapacity || snapshot.RecommendedTheme != ThemePAIOps {
		t.Fatalf("inference = class %q theme %q", snapshot.InferenceClass, snapshot.RecommendedTheme)
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"BODY_MUST_NOT_SURVIVE", "session-must-not-survive", "/private/project", "message", "session_id", "current_dir"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("derived snapshot retained forbidden input %q: %s", forbidden, body)
		}
	}
}

func TestAdaptiveModeUsesHysteresis(t *testing.T) {
	metric := Metric{Status: StatusLive, UsedPercent: 70, RemainingPercent: 30}
	if got := SelectMode(metric, nil, ModeFlow); got != ModeWatch {
		t.Fatalf("70%% from flow = %q, want watch", got)
	}
	if got := SelectMode(metric, nil, ModeConserve); got != ModeConserve {
		t.Fatalf("70%% from conserve = %q, want conserve hysteresis", got)
	}
	metric.UsedPercent, metric.RemainingPercent = 67, 33
	if got := SelectMode(metric, nil, ModeConserve); got != ModeWatch {
		t.Fatalf("67%% from conserve = %q, want watch after exit threshold", got)
	}
	metric.Status = StatusUnknown
	if got := SelectMode(metric, nil, ModeCritical); got != ModeUnknown {
		t.Fatalf("unknown evidence = %q", got)
	}
}

func TestSnapshotStoreIsAtomicPrivateAndStaleAware(t *testing.T) {
	home := t.TempDir()
	snapshot, err := BuildClaudeSnapshot([]byte(claudeFixture), testNow, Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Home: home, TTL: 2 * time.Minute}
	if err := store.Write(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.FreshUntil.Equal(testNow.Add(2 * time.Minute)) {
		t.Fatalf("write did not return the persisted freshness boundary: %s", snapshot.FreshUntil)
	}
	path := SnapshotPath(home, HarnessClaude)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "BODY_MUST_NOT_SURVIVE") {
		t.Fatalf("snapshot persisted an input body: %s", body)
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("atomic write leaked temps: %v", temps)
	}
	if got, fresh, err := store.ReadFresh(HarnessClaude, testNow.Add(time.Minute)); err != nil || !fresh || got.Mode != ModeConserve {
		t.Fatalf("fresh read = %+v fresh=%v err=%v", got, fresh, err)
	}
	if got, fresh, err := store.ReadFresh(HarnessClaude, testNow.Add(3*time.Minute)); err != nil || fresh || got.Mode != ModeUnknown {
		t.Fatalf("stale read = %+v fresh=%v err=%v", got, fresh, err)
	}
}

func TestRenderThemesAndCodexThemeAreDeterministic(t *testing.T) {
	snapshot, err := BuildClaudeSnapshot([]byte(claudeFixture), testNow, Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		theme  string
		prefix string
	}{
		{ThemePAINexus, "< CLAUDE:CONSERVE >"},
		{ThemePAIFocus, "// CLAUDE:CONSERVE //"},
		{ThemePAIOps, "!! CLAUDE:CONSERVE !!"},
		{ThemePAIMono, ".. CLAUDE:CONSERVE .."},
	} {
		line, err := Render(snapshot, RenderOptions{Theme: tc.theme, Width: 120, Color: false})
		if err != nil {
			t.Fatalf("render %s: %v", tc.theme, err)
		}
		if !strings.HasPrefix(line, tc.prefix) || !strings.Contains(line, "ctx [######----] 59%") {
			t.Fatalf("render %s = %q", tc.theme, line)
		}
		if strings.ContainsAny(line, "\n\r") {
			t.Fatalf("render %s is not one line: %q", tc.theme, line)
		}
		for _, r := range line {
			if r > unicode.MaxASCII {
				t.Fatalf("render %s contains non-ASCII rune %q: %q", tc.theme, r, line)
			}
		}
	}
	mono, err := Render(snapshot, RenderOptions{Theme: ThemePAIMono, Width: 120, Color: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mono, "\x1b[") {
		t.Fatalf("mono theme contains ANSI: %q", mono)
	}
	short, err := Render(snapshot, RenderOptions{Theme: ThemePAINexus, Width: 34, Color: false})
	if err != nil {
		t.Fatal(err)
	}
	if displayWidth(short) > 34 {
		t.Fatalf("render width = %d: %q", displayWidth(short), short)
	}
	for _, r := range short {
		if r > unicode.MaxASCII {
			t.Fatalf("truncated render contains non-ASCII rune %q: %q", r, short)
		}
	}
	auto, err := Render(snapshot, RenderOptions{Theme: ThemeAuto, Width: 120, Color: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(auto, "!! CLAUDE:CONSERVE !! left:") {
		t.Fatalf("auto theme did not select capacity information profile: %q", auto)
	}

	first, err := RenderCodexTheme(ThemePAINexus)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderCodexTheme(ThemePAINexus)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(first, "<plist version=\"1.0\">") || !strings.Contains(first, "PAI Nexus") {
		t.Fatalf("codex theme is not deterministic TextMate XML: %q", first)
	}
}

func TestClaudeRenderHasNoPAIBranding(t *testing.T) {
	snapshot, err := BuildClaudeSnapshot([]byte(claudeFixture), testNow, Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		ThemePAINexus: "< CLAUDE:CONSERVE > left: ctx [######----] 59% | 5h 66% | wk 18%",
		ThemePAIFocus: "// CLAUDE:CONSERVE // left: ctx [######----] 59% | wk 18%",
		ThemePAIOps:   "!! CLAUDE:CONSERVE !! left: ctx [######----] 59% | 5h 66% | wk 18%",
		ThemePAIMono:  ".. CLAUDE:CONSERVE .. left: ctx [######----] 59% | 5h 66% | wk 18%",
	}
	for _, theme := range ThemeIDs() {
		line, err := Render(snapshot, RenderOptions{Theme: theme, Width: 120, Color: false})
		if err != nil {
			t.Fatal(err)
		}
		if line != expected[theme] {
			t.Fatalf("render %s = %q, want %q", theme, line, expected[theme])
		}
		if strings.Contains(strings.ToUpper(line), "PAI") {
			t.Fatalf("Claude render leaked retired branding: %q", line)
		}
	}
}

func TestClaudeRenderShowsRemainingContextAsTenSegmentCapacity(t *testing.T) {
	watch := Snapshot{
		Harness: HarnessClaude,
		Context: Metric{Status: StatusLive, RemainingPercent: 72},
		Limits: []LimitWindow{
			{Kind: LimitFiveHour, Status: StatusLive, RemainingPercent: 81},
			{Kind: LimitSevenDay, Status: StatusLive, RemainingPercent: 43},
		},
		Mode: ModeWatch,
	}
	line, err := Render(watch, RenderOptions{Theme: ThemePAINexus, Width: 120})
	if err != nil {
		t.Fatal(err)
	}
	if want := "< CLAUDE:WATCH > left: ctx [#######---] 72% | 5h 81% | wk 43%"; line != want {
		t.Fatalf("watch render = %q, want %q", line, want)
	}

	unknown := Snapshot{Harness: HarnessClaude, Context: Metric{Status: StatusUnknown}, Mode: ModeUnknown}
	line, err = Render(unknown, RenderOptions{Theme: ThemePAIMono, Width: 120})
	if err != nil {
		t.Fatal(err)
	}
	if want := ".. CLAUDE:UNKNOWN .. left: ctx [??????????] ? | 5h ? | wk ?"; line != want {
		t.Fatalf("unknown render = %q, want %q", line, want)
	}
}

func TestInferDisplayClassChangesThemeAndInformationProfile(t *testing.T) {
	for _, tc := range []struct {
		mode  Mode
		class InferenceClass
		theme string
	}{
		{ModeFlow, InferenceFocus, ThemePAIFocus},
		{ModeWatch, InferenceBalanced, ThemePAINexus},
		{ModeConserve, InferenceCapacity, ThemePAIOps},
		{ModeCritical, InferenceCapacity, ThemePAIOps},
		{ModeUnknown, InferenceFallback, ThemePAIMono},
	} {
		class, theme := InferDisplayClass(tc.mode)
		if class != tc.class || theme != tc.theme {
			t.Fatalf("mode %q = class %q theme %q, want %q/%q", tc.mode, class, theme, tc.class, tc.theme)
		}
	}
}

func limitByKind(t *testing.T, snapshot Snapshot, kind LimitKind) LimitWindow {
	t.Helper()
	for _, limit := range snapshot.Limits {
		if limit.Kind == kind {
			return limit
		}
	}
	t.Fatalf("missing limit %q in %+v", kind, snapshot.Limits)
	return LimitWindow{}
}
