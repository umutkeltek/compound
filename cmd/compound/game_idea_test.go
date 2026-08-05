package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"compound/internal/game/source/gamesource"
	"compound/internal/sourceobs"
)

// stubLLMClient for game_idea_test.go — a deterministic LLM stub the
// production code path will never reach because cmdGameDraft fails fast
// on missing env vars before invoking it. We use it via a thin shim that
// the draft CLI uses internally.
type stubLLMClient struct {
	mu        sync.Mutex
	responses map[string][]byte
	err       error
	calls     int
}

func (s *stubLLMClient) Complete(_ context.Context, system, user string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.responses[system+"\n--\n"+user]; ok {
		return v, nil
	}
	if v, ok := s.responses["default"]; ok {
		return v, nil
	}
	return nil, errors.New("no canned response")
}

func seedIdeaObsCli(t *testing.T, home, kind, sourceKey, subject, statement string, when time.Time) {
	t.Helper()
	it := gamesource.RawObservation{
		Kind: kind, SourceKey: sourceKey, Subject: subject,
		Statement: statement, Confidence: 1.0,
		Evidence: []string{"https://store.steampowered.com/api/appdetails", "revision:test"},
	}
	rev := "idea-cli-" + time.Now().Format(time.RFC3339Nano)
	obs, err := gamesource.BuildObservation(gamesource.AdapterIDSteamWeb, gamesource.SourceSystemSteamWeb, rev, it, when)
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	if _, err := sourceobs.Apply(home, obs, when); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
}

func TestCmdGameScoutJSON(t *testing.T) {
	home := withTempHome(t)
	now := time.Now().UTC()
	seedIdeaObsCli(t, home, "genre", "action", "Action",
		`Game is in genre "Action" on appid 440`, now.Add(-1*time.Hour))
	seedIdeaObsCli(t, home, "genre", "strategy", "Strategy",
		`Game is in genre "Strategy" on appid 570`, now.Add(-1*time.Hour))

	var code int
	stdout, stderr := captureStdio(t, func() {
		code = cmdGameScout([]string{"action", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("parse: %v\nbody: %s", err, stdout)
	}
	if got["schema"] != "compound/game-scout-packet.v0.1" {
		t.Errorf("schema: %v", got["schema"])
	}
	anchors, _ := got["anchors"].([]any)
	if len(anchors) != 1 {
		t.Errorf("expected 1 anchor matching 'action', got %d", len(anchors))
	}
}

func TestCmdGameScoutRequiresQuery(t *testing.T) {
	withTempHome(t)
	var code int
	_, stderr := captureStdio(t, func() {
		code = cmdGameScout([]string{"--json"})
	})
	if code != 2 {
		t.Errorf("expected exit 2, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "query") {
		t.Errorf("expected query hint, got %q", stderr)
	}
}

func TestCmdGameScoutEmptyQueryReturnsTrending(t *testing.T) {
	home := withTempHome(t)
	now := time.Now().UTC()
	seedIdeaObsCli(t, home, "genre", "action", "Action",
		`Game is in genre "Action" on appid 440`, now.Add(-1*time.Hour))
	var code int
	stdout, _ := captureStdio(t, func() {
		code = cmdGameScout([]string{"", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	anchors, _ := got["anchors"].([]any)
	if len(anchors) != 0 {
		t.Errorf("empty query should match no anchors; got %d", len(anchors))
	}
	if warnings, _ := got["warnings"].([]any); len(warnings) == 0 {
		t.Errorf("empty query should emit warning")
	}
}

func TestCmdGameDraftRequiresEnvVars(t *testing.T) {
	// Clear env vars to ensure the failure path triggers.
	prevBase := os.Getenv("OPENAI_BASE_URL")
	prevKey := os.Getenv("OPENAI_API_KEY")
	prevModel := os.Getenv("OPENAI_MODEL")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Cleanup(func() {
		_ = os.Setenv("OPENAI_BASE_URL", prevBase)
		_ = os.Setenv("OPENAI_API_KEY", prevKey)
		_ = os.Setenv("OPENAI_MODEL", prevModel)
	})
	withTempHome(t)
	var code int
	_, stderr := captureStdio(t, func() {
		code = cmdGameDraft([]string{"co-op roguelike", "--json"})
	})
	if code != 1 {
		t.Errorf("expected exit 1, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "OPENAI_BASE_URL") {
		t.Errorf("expected OPENAI env-var message, got %q", stderr)
	}
}

func TestCmdGameDraftRequiresQuery(t *testing.T) {
	// Set env vars so we reach the query check, not the env-var check.
	t.Setenv("OPENAI_BASE_URL", "https://example.invalid")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("OPENAI_MODEL", "gpt-test")
	withTempHome(t)
	var code int
	_, stderr := captureStdio(t, func() {
		code = cmdGameDraft([]string{"--json"})
	})
	if code != 2 {
		t.Errorf("expected exit 2, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "query") {
		t.Errorf("expected query hint, got %q", stderr)
	}
}

func TestCmdGameRejectsUnknownFlagScoutDraft(t *testing.T) {
	withTempHome(t)
	cases := [][]string{
		{"scout", "--bad"},
		{"draft", "--bogus"},
	}
	for _, args := range cases {
		var code int
		_, stderr := captureStdio(t, func() {
			switch args[0] {
			case "scout":
				code = cmdGameScout(args[1:])
			case "draft":
				code = cmdGameDraft(args[1:])
			}
		})
		if code != 2 {
			t.Errorf("args=%v: expected exit 2, got %d stderr=%s", args, code, stderr)
		}
		if !strings.Contains(stderr, "unknown flag") {
			t.Errorf("args=%v: expected unknown flag, got %q", args, stderr)
		}
	}
}

func TestGameFirstPositional(t *testing.T) {
	got, err := gameFirstPositional([]string{"--json", "action", "--limit", "10"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "action" {
		t.Errorf("got %q want action", got)
	}
	// No positional at all (all args are flags): error.
	if _, err := gameFirstPositional([]string{"--json", "--home"}); err == nil {
		t.Errorf("expected error on no positional")
	}
}
