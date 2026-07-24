package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/envelope"
)

func instructionContractForTest(harness, task string) envelope.EffectiveContract {
	in := Input{
		Harness:     harness,
		Cwd:         "/repo/example",
		Task:        task,
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	return Compile(in, DefaultPolicy(), fixedNow)
}

func TestRenderInstructionBlockCarriesStableAuthorityAndProjectRules(t *testing.T) {
	for _, tc := range []struct {
		harness string
		target  string
	}{
		{harness: "codex", target: "AGENTS.md"},
		{harness: "claude_code", target: "CLAUDE.md"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.target), []byte("# existing\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			render, err := RenderInstructionBlock(instructionContractForTest(tc.harness, "first task"), root)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(render.TargetPath) != tc.target || !render.Drifted {
				t.Fatalf("render target/drift = %s/%t, want %s/true", render.TargetPath, render.Drifted, tc.target)
			}
			for _, want := range []string{
				"Compound is the authority for durable meaning",
				"Retrieval engines and legacy vector/fact stores are bridges/indexes",
				"rented executor",
				"cheapest sufficient lane",
				"AGENTS.md",
				"skills/README.md",
				"Task-specific intent, memory, capabilities, and checks stay in `compound orient` and hook-served ContextPacks",
			} {
				if !strings.Contains(render.Content, want) {
					t.Fatalf("render missing %q:\n%s", want, render.Content)
				}
			}
		})
	}
}

func TestInstructionBlockIgnoresTaskTextAndDetectsOnlyManagedBlockDrift(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "AGENTS.md")
	first, err := RenderInstructionBlock(instructionContractForTest("codex", "private task alpha"), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderInstructionBlock(instructionContractForTest("codex", "unrelated task beta"), root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != second.Content || first.ContentSHA256 != second.ContentSHA256 {
		t.Fatalf("task text leaked into static block:\nfirst=%s\nsecond=%s", first.Content, second.Content)
	}
	if strings.Contains(first.Content, "private task alpha") || strings.Contains(first.Content, "unrelated task beta") {
		t.Fatalf("task-scoped text appeared in static block: %s", first.Content)
	}

	if err := os.WriteFile(target, []byte("unmanaged before\n\n"+first.Content+"\nunmanaged after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err := RenderInstructionBlock(instructionContractForTest("codex", "third task"), root)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Drifted {
		t.Fatalf("unmanaged text caused drift: %+v", clean)
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	driftedBody := strings.Replace(string(body), "single authority", "one authority", 1)
	if err := os.WriteFile(target, []byte(driftedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, err := RenderInstructionBlock(instructionContractForTest("codex", "third task"), root)
	if err != nil {
		t.Fatal(err)
	}
	if !drifted.Drifted || drifted.ObservedSHA256 == drifted.ContentSHA256 {
		t.Fatalf("managed-block mutation did not drift: %+v", drifted)
	}
}

func TestTrackedHarnessInstructionBlocksMatchCompiler(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, harness := range []string{"codex", "claude_code"} {
		render, err := RenderInstructionBlock(instructionContractForTest(harness, "static parity proof"), repoRoot)
		if err != nil {
			t.Fatal(err)
		}
		if render.Drifted {
			t.Fatalf("%s tracked instruction block drifted: %+v", harness, render)
		}
	}
}
