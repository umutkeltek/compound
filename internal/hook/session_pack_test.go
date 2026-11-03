package hook

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/hookplan"
	"compound/internal/mem"
	"compound/internal/tooltopology"
)

// sessionPackRepo is a repository fixture with a Tier-0 constitution, so the law
// line has something to serve.
func sessionPackRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "compound") // the seeded claim is about this entity
	dir := filepath.Join(repo, "docs", "architecture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Universal principles\n\n## The Laws\n\n1. Discover reality before acting.\n2. Subtract by default.\n\n## Notes\n\n3. Not a law.\n"
	if err := os.WriteFile(filepath.Join(dir, "universal-principles.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// sessionPackHome seeds everything a full pack serves (door map, an owned-ledger
// claim, capability offers), so an absence in minimal mode is a real absence and
// not an empty home.
func sessionPackHome(t *testing.T) string {
	t.Helper()
	home := doorMapHome(t)
	modelPath := filepath.Join(home, filepath.FromSlash(tooltopology.OperatorModelPath))
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte(sessionPackOperatorModel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{{
		Entity:          "compound",
		Predicate:       "capability_authority_surface",
		Object:          "Compound owns the capability graph runtime surface",
		Confidence:      0.91,
		Derivation:      "deterministic",
		ClaimType:       "architecture",
		Qualifiers:      map[string]string{"domain": "capability_authority"},
		PromotionBasis:  candidates.BasisCuratedPage,
		SourceTraceRef:  "ev_compound",
		SnippetHash:     "sha256:compound",
		Visibility:      "contextual",
		ObservationDate: "2026-06-30T00:00:00Z",
	}}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Adjudicate(got[0].ID, "promote", "test", fixedNow); err != nil {
		t.Fatal(err)
	}
	manifest, err := capgraph.LoadSkillManifest(filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capgraph.ApplySkillManifest(home, manifest, fixedNow); err != nil {
		t.Fatal(err)
	}
	return home
}

// sessionPackOperatorModel is a synthetic operator model; both modes serve it after
// the door map, as the operator asked on 11-02.
const sessionPackOperatorModel = "- Lead with the decision.\n- Ask before any device run."

func sessionPackFixture(harnessName, repo string) Fixture {
	return Fixture{
		Hook:      "session-start",
		Harness:   harnessName,
		SessionID: "sess-pack-" + harnessName,
		Cwd:       repo,
		Task:      "resume compound pricing",
		RepoKind:  "control-plane",
		Live:      true,
	}
}

// servedSessionStart is what the harness reads: the live response joins Summary
// and ContextOutput the same way.
func servedSessionStart(res Result) string {
	return joinSessionStartParts(res.Summary, res.ContextOutput)
}

func ctxFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "ctx_") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

var minimalSessionStartForbidden = []string{
	"owned ledger",
	"capability offer",
	"Context pack: ctx_",
	"Compound positions",
	"decision ref(s)",
}

func TestSessionPackMinimalServesLawsDoorMapAndPointerOnly(t *testing.T) {
	for _, harnessName := range []string{"claude_code", "codex"} {
		t.Run(harnessName, func(t *testing.T) {
			t.Setenv(SessionPackEnv, "")
			t.Setenv("HOME", t.TempDir())
			home := sessionPackHome(t)
			repo := sessionPackRepo(t)
			deps := baseDeps()
			deps.HomePath = home
			fx := sessionPackFixture(harnessName, repo)

			res, err := Run(fx, deps, fixedNow)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			served := servedSessionStart(res)
			for _, want := range []string{
				"Tier-0 constitution: Laws 1. Discover reality before acting. · 2. Subtract by default.",
				"## Door map",
				"PASS web: search-cli",
				"Context pack is on demand: run `compound orient --harness " + harnessName + " --cwd " + repo + " --task \"<task>\"` before a non-trivial task.",
			} {
				if !strings.Contains(served, want) {
					t.Fatalf("minimal session start missing %q:\n%s", want, served)
				}
			}
			if strings.Contains(served, "Not a law") {
				t.Fatalf("law line read past the Laws section:\n%s", served)
			}
			operatorModel := tooltopology.ServeOperatorModel(home)
			if operatorModel == "" || !strings.Contains(operatorModel, sessionPackOperatorModel) {
				t.Fatalf("scratch home serves no operator model: %q", operatorModel)
			}
			order := []string{"Tier-0 constitution:", "## Door map", operatorModel, "Context pack is on demand"}
			last := -1
			for _, part := range order {
				at := strings.Index(served, part)
				if at <= last {
					t.Fatalf("minimal session start must serve law line, door map, operator model, pointer in that order; %q at %d after %d:\n%s", part, at, last, served)
				}
				last = at
			}
			for _, forbidden := range minimalSessionStartForbidden {
				if strings.Contains(served, forbidden) {
					t.Fatalf("minimal session start served %q:\n%s", forbidden, served)
				}
			}
			if len(res.Produced) != 0 || res.Injection != nil || res.Context != nil || res.Event.IDs.ContextPackID != "" || res.Event.OutputRef != "" {
				t.Fatalf("minimal session start built a pack: produced=%d injection=%v context=%v pack=%q output=%q",
					len(res.Produced), res.Injection, res.Context, res.Event.IDs.ContextPackID, res.Event.OutputRef)
			}
			if minted := ctxFilesUnder(t, home); len(minted) != 0 {
				t.Fatalf("minimal session start minted pack files: %v", minted)
			}
			// The session is still recorded: the HookEvent carries the session id and
			// harness, and the per-prompt pointer says the pack is on demand.
			stream, err := os.ReadFile(filepath.Join(home, "events", "hook-events.ndjson"))
			if err != nil {
				t.Fatalf("read hook event stream: %v", err)
			}
			if !strings.Contains(string(stream), `"hook_name":"SessionStart"`) || !strings.Contains(string(stream), `"session_id":"`+fx.SessionID+`"`) || !strings.Contains(string(stream), `"harness":"`+harnessName+`"`) {
				t.Fatalf("minimal session start lost the session record:\n%s", stream)
			}
			if note := orientationNoteForSession(home, fx.SessionID, harnessName); !strings.Contains(note, "Orientation is on demand this session") {
				t.Fatalf("per-prompt pointer after a minimal start = %q", note)
			}
		})
	}
}

// The minimal start's ContextOutput is the full start's payload (door map, then the
// operator model when the home has one) followed by the pointer, byte for byte.
func TestSessionPackMinimalKeepsTheSessionStartPayloadThenThePointer(t *testing.T) {
	t.Setenv(SessionPackEnv, "")
	fx := Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product", Live: true}
	for name, model := range map[string]string{"with operator model": syntheticOperatorModel, "without operator model": ""} {
		t.Run(name, func(t *testing.T) {
			home := doorMapHome(t)
			if model != "" {
				writeHookOperatorModel(t, home, model)
			}
			deps := baseDeps()
			deps.HomePath = home
			res, err := Run(fx, deps, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			want := tooltopology.ServeSessionStart(home, fixedNow, tooltopology.DefaultDoorMapTTL) + "\n\n" + onDemandPackPointer(fx)
			if res.ContextOutput != want {
				t.Fatalf("minimal ContextOutput:\n got %q\nwant %q", res.ContextOutput, want)
			}
			if hasModel := strings.Contains(res.ContextOutput, "## Operator model"); hasModel != (model != "") {
				t.Fatalf("operator model present = %v, want %v:\n%s", hasModel, model != "", res.ContextOutput)
			}
		})
	}
}

func TestSessionPackFullEnvRestoresTheFullPack(t *testing.T) {
	for _, harnessName := range []string{"claude_code", "codex"} {
		t.Run(harnessName, func(t *testing.T) {
			t.Setenv(SessionPackEnv, "full")
			t.Setenv("HOME", t.TempDir())
			home := sessionPackHome(t)
			repo := sessionPackRepo(t)
			deps := baseDeps()
			deps.HomePath = home
			fx := sessionPackFixture(harnessName, repo)

			res, err := Run(fx, deps, fixedNow)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			served := servedSessionStart(res)
			// The same fixture that is minimal by default serves every part the minimal
			// mode drops, so the minimal test's absences are real absences.
			for _, want := range []string{
				"Tier-0 constitution: Laws 1. Discover reality before acting.",
				"## Door map",
				"Compound memory (owned ledger): compound.capability_authority_surface",
				tooltopology.ServeOperatorModel(home),
				"capability offer",
				"Context pack: ctx_",
			} {
				if !strings.Contains(served, want) {
					t.Fatalf("full session start missing %q:\n%s", want, served)
				}
			}
			if strings.Contains(served, "Context pack is on demand") {
				t.Fatalf("full session start carried the on-demand pointer:\n%s", served)
			}
			if len(res.Produced) != 3 || res.Injection == nil || res.Event.IDs.ContextPackID == "" {
				t.Fatalf("full session start did not build the pack: produced=%d injection=%v", len(res.Produced), res.Injection)
			}
			if minted := ctxFilesUnder(t, home); len(minted) == 0 {
				t.Fatal("full session start minted no ctx_ pack file")
			}
			if note := orientationNoteForSession(home, fx.SessionID, harnessName); !strings.Contains(note, "Orientation served this session") {
				t.Fatalf("per-prompt pointer after a full start = %q", note)
			}
		})
	}
}

// Unattended executors keep the pack: their default output must be the full
// output, byte for byte, with or without the override.
func TestSessionPackEnforcedLanesKeepTheFullPack(t *testing.T) {
	run := func(t *testing.T, fx Fixture, env string) Result {
		t.Helper()
		t.Setenv(SessionPackEnv, env)
		t.Setenv("HOME", t.TempDir())
		deps := baseDeps()
		deps.HomePath = sessionPackHome(t)
		res, err := Run(fx, deps, fixedNow)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return res
	}
	repo := sessionPackRepo(t)
	cases := map[string]Fixture{}
	for _, lane := range []string{"claude_zai", "claude_minimax", "claude_qwen", "oh_my_pi", "opencode"} {
		cases[lane] = sessionPackFixture(lane, repo)
	}
	enforcedInteractive := sessionPackFixture("claude_code", repo)
	enforcedInteractive.Enforced = true
	cases["claude_code --enforce"] = enforcedInteractive
	for name, fx := range cases {
		t.Run(name, func(t *testing.T) {
			def := run(t, fx, "")
			full := run(t, fx, "full")
			if servedSessionStart(def) != servedSessionStart(full) {
				t.Fatalf("default output differs from full output:\n--- default\n%s\n--- full\n%s", servedSessionStart(def), servedSessionStart(full))
			}
			if !strings.Contains(def.Summary, "Context pack: ctx_") || len(def.Produced) != 3 {
				t.Fatalf("enforced lane lost its session pack:\n%s", servedSessionStart(def))
			}
		})
	}
}

func TestSessionPackModeTable(t *testing.T) {
	cases := []struct {
		name string
		fx   Fixture
		env  string
		want string
	}{
		{"claude_code default", Fixture{Harness: "claude_code", Live: true}, "", hookplan.SessionPackMinimal},
		{"installer label", Fixture{Harness: "claude-code", Live: true}, "", hookplan.SessionPackMinimal},
		{"codex default", Fixture{Harness: "codex", Live: true}, "", hookplan.SessionPackMinimal},
		{"env full", Fixture{Harness: "claude_code", Live: true}, "full", hookplan.SessionPackFull},
		{"env case and space", Fixture{Harness: "codex", Live: true}, " FULL ", hookplan.SessionPackFull},
		{"unknown env keeps default", Fixture{Harness: "codex", Live: true}, "yes", hookplan.SessionPackMinimal},
		{"enforced interactive", Fixture{Harness: "claude_code", Live: true, Enforced: true}, "", hookplan.SessionPackFull},
		{"executor lane", Fixture{Harness: "claude_zai", Live: true}, "", hookplan.SessionPackFull},
		{"env minimal on executor", Fixture{Harness: "opencode", Live: true}, "minimal", hookplan.SessionPackMinimal},
		{"fixture replay ignores env", Fixture{Harness: "claude_code"}, "minimal", hookplan.SessionPackFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(SessionPackEnv, tc.env)
			if got := sessionPackMode(tc.fx); got != tc.want {
				t.Fatalf("sessionPackMode(%+v, env %q) = %q, want %q", tc.fx, tc.env, got, tc.want)
			}
		})
	}
}

func TestSessionPackPointerQuotesShellWords(t *testing.T) {
	got := onDemandPackPointer(Fixture{Harness: "codex", Cwd: "/work/it's here"})
	if !strings.Contains(got, `--cwd '/work/it'\''s here'`) {
		t.Fatalf("pointer = %q, want a single-quoted cwd", got)
	}
	if got := onDemandPackPointer(Fixture{}); !strings.Contains(got, "--harness <harness> --cwd <repository>") {
		t.Fatalf("pointer without harness or cwd = %q", got)
	}
}
