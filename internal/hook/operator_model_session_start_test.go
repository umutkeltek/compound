package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/tooltopology"
)

const syntheticOperatorModel = "- Lead with the decision, then the evidence.\n- Plain language; no jargon without a gloss.\n"

func writeHookOperatorModel(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, "config", "operator-model.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLiveSessionStartServesOperatorModelBeforeDoorMap(t *testing.T) {
	t.Setenv(SessionPackEnv, "full") // exact full-start payload; session_pack_test pins the minimal one
	home := doorMapHome(t)
	writeHookOperatorModel(t, home, syntheticOperatorModel)
	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	doorMap := tooltopology.ServeDoorMap(home, fixedNow, tooltopology.DefaultDoorMapTTL)
	want := "## Operator model (private, from config/operator-model.md)\n" + strings.TrimRight(syntheticOperatorModel, "\n") + "\n\n" + doorMap
	if res.ContextOutput != want {
		t.Fatalf("live session-start must serve the operator model, one blank line, then the door map:\n got %q\nwant %q", res.ContextOutput, want)
	}
}

func TestLiveSessionStartWithoutOperatorModelServesDoorMapOnly(t *testing.T) {
	t.Setenv(SessionPackEnv, "full") // exact full-start payload; session_pack_test pins the minimal one
	home := doorMapHome(t)
	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if want := tooltopology.ServeDoorMap(home, fixedNow, tooltopology.DefaultDoorMapTTL); res.ContextOutput != want {
		t.Fatalf("without an operator model the payload must be the door map byte for byte:\n got %q\nwant %q", res.ContextOutput, want)
	}
	if strings.Contains(res.ContextOutput, "Operator model") {
		t.Fatalf("an absent operator model must be silent, got %q", res.ContextOutput)
	}
}

func TestFixtureReplaySessionStartIgnoresOperatorModel(t *testing.T) {
	home := doorMapHome(t)
	writeHookOperatorModel(t, home, syntheticOperatorModel)
	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product"}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.ContextOutput != "" {
		t.Fatalf("fixture replay must not depend on machine state, got %q", res.ContextOutput)
	}
}

// TestLiveSessionStartOversizeOperatorModelStaysBounded pins the worst-case
// session-start payload: the full door-map block plus an operator model past
// its budget. Every rule section arrives whole, even one that sits past the
// old byte cap; only a background section is left out, and it is named. With
// the rules inside the budget the whole payload stays under the 10,000
// characters a harness may keep from hook output.
func TestLiveSessionStartOversizeOperatorModelStaysBounded(t *testing.T) {
	t.Setenv(SessionPackEnv, "full") // exact full-start payload; session_pack_test pins the minimal one
	home := doorMapHome(t)
	decisions := "## Standing decisions\n" + strings.Repeat("- a standing decision line that the operator wrote\n", 30)
	model := "# Operator model\n\n## Standing constraints\n" + strings.Repeat("- a standing constraint line that repeats\n", 60) +
		"\n## Background\n" + strings.Repeat("- a background line that repeats\n", 200) + "\n" + decisions
	writeHookOperatorModel(t, home, model)
	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(res.ContextOutput, "\n\n## Door map")
	if idx < 0 || !strings.HasPrefix(res.ContextOutput, "## Operator model") {
		t.Fatalf("operator model block must lead, then the door map: %q", res.ContextOutput)
	}
	modelPart, doorPart := res.ContextOutput[:idx], res.ContextOutput[idx+2:]
	if len(doorPart) > tooltopology.MaxServedBytes+200 {
		t.Fatalf("door map part is %d bytes, cap %d", len(doorPart), tooltopology.MaxServedBytes)
	}
	if !strings.Contains(modelPart, strings.TrimRight(decisions, "\n")) {
		t.Fatalf("standing decisions past the old cap did not reach the session start whole")
	}
	if strings.Contains(modelPart, "a background line") || !strings.Contains(modelPart, `"Background" (`) {
		t.Fatalf("background section past the budget must be left out and named:\n%s", modelPart[len(modelPart)-300:])
	}
	if len(res.ContextOutput) >= 10000 {
		t.Fatalf("session-start payload is %d characters, want under 10000", len(res.ContextOutput))
	}
	t.Logf("session-start ContextOutput=%d bytes (door map %d + separator 2 + operator model %d); pack summary=%d bytes",
		len(res.ContextOutput), len(doorPart), len(modelPart), len(res.Summary))
}
