package harness

// skill_adapter_test.go — falsifiers F1, F2, F3.
//
// F1: the canonical body is harness-neutral — no Claude-specific strings,
//     contains the profile registry + mcporter references.
// F2: render is deterministic per harness — same body+adapter+time ⇒
//     byte-identical; footer carries body sha256.
// F3: install detects drift; replaces symlinked parent with a real dir and
//     reports it.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureBody(t *testing.T) []byte {
	t.Helper()
	// A harness-neutral body. Mirrors the canonical rewrite: no PAI path,
	// no voice-notify curl, no execution.jsonl, no Claude built-ins, no
	// mcp__ tool names. References browser-profiles.json + mcporter call.
	return []byte(`---
name: Browser
description: harness-neutral body (test fixture)
---

# Browser (test fixture body)

Shared backend: ` + "`mcporter call browser.<tool>`" + `.

Profile registry: ` + "`~/.compound/registries/browser-profiles.json`" + `.

Backend vocabulary: navigate, snapshot, click, type, screenshot, extract,
login-check, close.

Safety rules:
- Never log in with an unenrolled profile.
- Never persist credentials in transcripts.

Recipes / Stories / Workflows continue to ship with the body.
`)
}

func fixedNow() time.Time {
	return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
}

// F1: body is harness-neutral.
func TestBrowserSkillBodyIsHarnessNeutral(t *testing.T) {
	home := os.Getenv("HOME")
	if strings.TrimSpace(home) == "" {
		t.Skip("HOME not set — no canonical body available in this test environment")
	}
	path := filepath.Join(home, ".agents", "skills", "Browser", "SKILL.md")
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("canonical body absent at %s — public clone, skipping (the test is for the live body)", path)
		}
		t.Fatalf("read canonical body: %v", err)
	}
	text := string(body)
	banned := []string{
		"~/.claude/",
		"PAI/",
		"localhost:31337",
		"mcp__",
		"Claude Code built-in",
	}
	for _, needle := range banned {
		if strings.Contains(text, needle) {
			t.Errorf("F1 FAIL: canonical body still contains Claude-coupled string %q — move it to the claude_code adapter", needle)
		}
	}
	required := []string{
		"browser-profiles.json",
		"mcporter call browser",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Errorf("F1 FAIL: canonical body missing required neutral reference %q", needle)
		}
	}
}

// F1 test on a fixture body — proves the renderer-side assertion shape so
// F1 is reproducible in any environment.
func TestFixtureBodyIsHarnessNeutral(t *testing.T) {
	body := string(fixtureBody(t))
	banned := []string{"~/.claude/", "PAI/", "localhost:31337", "mcp__", "Claude Code built-in"}
	for _, needle := range banned {
		if strings.Contains(body, needle) {
			t.Fatalf("fixture body contains %q", needle)
		}
	}
}

// F2: render is deterministic per harness, footer carries body sha256.
func TestRenderSkillAdapterPerHarnessIsDeterministic(t *testing.T) {
	body := fixtureBody(t)
	now := fixedNow()
	all := DefaultSkillAdapters()
	if len(all) < 3 {
		t.Fatalf("default adapters want ≥3 (claude_code, codex, hermes); got %d", len(all))
	}

	for _, h := range all {
		t.Run(h.ID, func(t *testing.T) {
			a, err := RenderSkillAdapter(body, h, now)
			if err != nil {
				t.Fatalf("render %s: %v", h.ID, err)
			}
			b, err := RenderSkillAdapter(body, h, now)
			if err != nil {
				t.Fatalf("re-render %s: %v", h.ID, err)
			}
			if string(a) != string(b) {
				t.Errorf("F2 FAIL: two renders of %s are NOT byte-identical (len=%d vs %d)", h.ID, len(a), len(b))
			}
			// Body must be present verbatim.
			if !strings.Contains(string(a), string(body)) {
				t.Errorf("F2 FAIL: %s adapter does not contain the body verbatim", h.ID)
			}
			// Footer must carry the body sha256.
			want := sha256.Sum256(body)
			wantHex := hex.EncodeToString(want[:])
			if !strings.Contains(string(a), "body_sha256="+wantHex) {
				t.Errorf("F2 FAIL: %s adapter footer missing body sha256 %s", h.ID, wantHex)
			}
			// Each adapter must carry a harness-specific header.
			if !strings.Contains(string(a), h.Header) {
				t.Errorf("F2 FAIL: %s adapter header missing", h.ID)
			}
		})
	}

	// Distinct harnesses produce distinct bytes (the header differs).
	a, _ := RenderSkillAdapter(body, all[0], now)
	c, _ := RenderSkillAdapter(body, all[1], now)
	if string(a) == string(c) {
		t.Errorf("F2 FAIL: claude_code and codex adapters are byte-identical (header should differ)")
	}
}

// F2 edge: empty body is a refusal, not a panic.
func TestRenderSkillAdapterRefusesEmptyBody(t *testing.T) {
	if _, err := RenderSkillAdapter(nil, DefaultSkillAdapters()[0], fixedNow()); err == nil {
		t.Fatal("expected refusal on empty body")
	}
}

// F3: install (apply) writes a real file; second run reports unchanged;
// hand-edit makes the next dry-run report drift; a symlinked parent gets
// replaced with a real dir and reported.
func TestInstallSkillAdaptersDetectsDrift(t *testing.T) {
	home := t.TempDir()
	// Each adapter reads $HOME; set HOME per-test.
	t.Setenv("HOME", home)

	body := fixtureBody(t)
	now := fixedNow()

	// Build a synthetic claude_code adapter whose SkillDir/SkillPath root at
	// the temp HOME — exercises the same code path as production but with
	// isolated dirs.
	claude := HarnessAdapter{
		ID: "claude_code",
		SkillDir: func(s string) string {
			return filepath.Join(home, ".claude", "skills", s)
		},
		SkillPath: func(s string) string {
			return filepath.Join(home, ".claude", "skills", s, "SKILL.md")
		},
		Header: "CLAUDE_HEADER\n",
	}
	codex := HarnessAdapter{
		ID: "codex",
		SkillDir: func(s string) string {
			return filepath.Join(home, ".codex", "skills", s)
		},
		SkillPath: func(s string) string {
			return filepath.Join(home, ".codex", "skills", s, "SKILL.md")
		},
		Header: "CODEX_HEADER\n",
	}
	hermes := HarnessAdapter{
		ID: "hermes",
		SkillDir: func(s string) string {
			return filepath.Join(home, ".hermes", "skills", s)
		},
		SkillPath: func(s string) string {
			return filepath.Join(home, ".hermes", "skills", s, "SKILL.md")
		},
		Header: "HERMES_HEADER\n",
	}
	adapters := []HarnessAdapter{claude, codex, hermes}

	// 1. dry-run: nothing exists yet → all three plan to write.
	dry, err := InstallSkillAdapters("Browser", adapters, false, now, body)
	if err != nil {
		t.Fatalf("dry-run install: %v", err)
	}
	if len(dry.Adapters) != 3 {
		t.Fatalf("dry report wants 3 adapters, got %d", len(dry.Adapters))
	}
	for _, a := range dry.Adapters {
		if a.Action != ActionWrite {
			t.Errorf("F3 dry: %s action=%s want write", a.Harness, a.Action)
		}
	}

	// 2. apply: writes the files; second run is clean.
	applied, err := InstallSkillAdapters("Browser", adapters, true, now, body)
	if err != nil {
		t.Fatalf("apply install: %v", err)
	}
	for _, a := range applied.Adapters {
		if a.Action != ActionWrite {
			t.Errorf("F3 apply: %s action=%s want write", a.Harness, a.Action)
		}
		got, err := os.ReadFile(a.Path)
		if err != nil {
			t.Fatalf("read installed %s: %v", a.Harness, err)
		}
		// Each installed file is a real file in a real dir (not a symlink).
		fi, err := os.Lstat(a.Path)
		if err != nil {
			t.Fatalf("lstat %s: %v", a.Harness, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("F3: %s is a symlink — should be a real file", a.Harness)
		}
		// Footer sha256 matches.
		want := sha256.Sum256(body)
		wantHex := hex.EncodeToString(want[:])
		if !strings.Contains(string(got), "body_sha256="+wantHex) {
			t.Errorf("F3: %s missing body sha256 footer", a.Harness)
		}
	}

	clean, err := InstallSkillAdapters("Browser", adapters, true, now, body)
	if err != nil {
		t.Fatalf("clean re-run: %v", err)
	}
	for _, a := range clean.Adapters {
		if a.Action != ActionUnchanged {
			t.Errorf("F3 second run: %s action=%s want unchanged", a.Harness, a.Action)
		}
	}

	// 3. drift: hand-edit one file, dry-run reports drift for that file.
	claudePath := claude.SkillPath("Browser")
	if err := os.WriteFile(claudePath, []byte("HAND EDIT\n"), 0o644); err != nil {
		t.Fatalf("hand edit: %v", err)
	}
	drift, err := InstallSkillAdapters("Browser", adapters, false, now, body)
	if err != nil {
		t.Fatalf("drift dry-run: %v", err)
	}
	var sawClaudeDrift bool
	for _, a := range drift.Adapters {
		if a.Harness != "claude_code" {
			if a.Action != ActionUnchanged {
				t.Errorf("F3 drift: non-edited %s action=%s want unchanged", a.Harness, a.Action)
			}
			continue
		}
		if a.Action != ActionUpdate {
			t.Errorf("F3 drift: edited %s action=%s want update", a.Harness, a.Action)
		}
		sawClaudeDrift = true
	}
	if !sawClaudeDrift {
		t.Errorf("F3 FAIL: drift dry-run did not report claude_code as drifted")
	}

	// 4. symlink-into-shared-canon is materialized. Stage a symlink at
	// $HOME/.hermes/skills/Browser that points outside the test home, then
	// re-run apply — the door should remove the symlink, mkdir a real dir,
	// and write the adapter inside. First remove the real dir the prior
	// apply created, so the symlink is the only state.
	if err := os.RemoveAll(filepath.Dir(hermes.SkillPath("Browser"))); err != nil {
		t.Fatalf("remove prior hermes skills dir: %v", err)
	}
	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "skills")
	if err := os.Symlink(shimPath, filepath.Dir(hermes.SkillPath("Browser"))); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}
	matReport, err := InstallSkillAdapters("Browser", adapters, true, now, body)
	if err != nil {
		t.Fatalf("materialize run: %v", err)
	}
	for _, a := range matReport.Adapters {
		if a.Harness != "hermes" {
			continue
		}
		if !strings.Contains(a.Detail, "replaced symlink") {
			t.Errorf("F3 symlink: expected 'replaced symlink' detail on hermes; got %q", a.Detail)
		}
	}
	// The path that was a symlink should now be a real dir.
	fi, err := os.Lstat(filepath.Dir(hermes.SkillPath("Browser")))
	if err != nil {
		t.Fatalf("lstat post-materialize: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("F3 FAIL: hermes parent is still a symlink after materialization")
	}
}
