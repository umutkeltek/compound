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

func TestLiveSessionStartServesOperatorModelAfterDoorMap(t *testing.T) {
	home := doorMapHome(t)
	writeHookOperatorModel(t, home, syntheticOperatorModel)
	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	doorMap := tooltopology.ServeDoorMap(home, fixedNow, tooltopology.DefaultDoorMapTTL)
	want := doorMap + "\n\n" + "## Operator model (private, from config/operator-model.md)\n" + strings.TrimRight(syntheticOperatorModel, "\n")
	if res.ContextOutput != want {
		t.Fatalf("live session-start must serve the door map, one blank line, then the operator model:\n got %q\nwant %q", res.ContextOutput, want)
	}
}

func TestLiveSessionStartWithoutOperatorModelServesDoorMapOnly(t *testing.T) {
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
// session-start payload: a full door-map block plus an operator model past its
// cap. Each block keeps its own cap; the log line records the totals.
func TestLiveSessionStartOversizeOperatorModelStaysBounded(t *testing.T) {
	home := doorMapHome(t)
	writeHookOperatorModel(t, home, strings.Repeat("- a standing preference line that repeats\n", 200))
	deps := baseDeps()
	deps.HomePath = home
	res, err := Run(Fixture{Hook: "session-start", Harness: "codex", Cwd: "/repo/productX", RepoKind: "product", Live: true}, deps, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(res.ContextOutput, "\n\n## Operator model")
	if idx < 0 {
		t.Fatalf("operator model block missing from %q", res.ContextOutput)
	}
	doorPart, modelPart := res.ContextOutput[:idx], res.ContextOutput[idx+2:]
	if len(doorPart) > tooltopology.MaxServedBytes {
		t.Fatalf("door map part is %d bytes, cap %d", len(doorPart), tooltopology.MaxServedBytes)
	}
	header := "## Operator model (private, from config/operator-model.md)\n"
	lastNL := strings.LastIndex(modelPart, "\n")
	if !strings.HasPrefix(modelPart[lastNL+1:], "(truncated: ") {
		t.Fatalf("oversize operator model must end with the truncation line, got %q", modelPart[lastNL+1:])
	}
	if content := modelPart[len(header):lastNL]; len(content) > tooltopology.MaxOperatorModelBytes {
		t.Fatalf("operator model content is %d bytes, cap %d", len(content), tooltopology.MaxOperatorModelBytes)
	}
	t.Logf("session-start ContextOutput=%d bytes (door map %d + separator 2 + operator model %d); pack summary=%d bytes",
		len(res.ContextOutput), len(doorPart), len(modelPart), len(res.Summary))
}
