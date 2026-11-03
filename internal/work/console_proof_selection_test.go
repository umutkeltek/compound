package work

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// consoleSelectionRepo builds a four-commit history:
// base (README + console/old.ts) -> docs (README only) -> console (console/app.ts)
// -> rename (console/old.ts moved to lib/old.ts).
func consoleSelectionRepo(t *testing.T) (repo string, commits map[string]string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(name string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "-m", name)
		commits[name] = git("rev-parse", "HEAD")
	}
	commits = map[string]string{}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "Compound Test")
	write("README.md", "one\n")
	write("console/old.ts", "export const old = 1\n")
	commit("base")
	write("README.md", "two\n")
	commit("docs")
	write("console/app.ts", "export const app = 1\n")
	commit("console")
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("mv", "console/old.ts", "lib/old.ts")
	commit("rename")
	return repo, commits
}

func TestConsoleProofSelectionFollowsTheProvedRange(t *testing.T) {
	t.Parallel()
	repo, c := consoleSelectionRepo(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, base, head string
		requested        bool
		include          bool
		reason           string
	}{
		{"range outside console", c["base"], c["docs"], false, false, ConsoleProofReasonUnchanged},
		{"range touching console", c["docs"], c["console"], false, true, ConsoleProofReasonChanged},
		{"rename out of console", c["console"], c["rename"], false, true, ConsoleProofReasonChanged},
		{"empty range", c["rename"], c["rename"], false, false, ConsoleProofReasonUnchanged},
		{"no base", "", c["rename"], false, false, ConsoleProofReasonNoRange},
		{"explicit request", c["base"], c["docs"], true, true, ConsoleProofReasonRequested},
		{"head defaults to HEAD", c["console"], "", false, true, ConsoleProofReasonChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveConsoleProofSelection(ctx, repo, tc.base, tc.head, tc.requested)
			if err != nil {
				t.Fatal(err)
			}
			if got.Include != tc.include || got.Reason != tc.reason {
				t.Fatalf("selection = %+v, want include=%t reason=%s", got, tc.include, tc.reason)
			}
		})
	}
	if _, err := ResolveConsoleProofSelection(ctx, repo, strings.Repeat("0", 40), c["docs"], false); err == nil {
		t.Fatal("unknown base resolved a selection instead of failing")
	}
}

func consoleSelectionInventory() CanonicalProofInventory {
	return func(string, ...string) ([]ProofCheckStep, error) {
		return []ProofCheckStep{
			{Name: "go-test", ProofRef: "go-test"},
			{Name: "console-tsc", Dir: "console", ProofRef: "console-tsc-noemit"},
			{Name: "bun-tools-test", SkipIfMissing: "bun", ProofRef: "bun-tools-test"},
			{Name: "console-smoke", Dir: "console", ProofRef: "console-smoke"},
			{Name: "redact-public", ProofRef: "redact-public-clean"},
		}, nil
	}
}

func consoleSelectionNames(steps []ProofCheckStep) []string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.Name)
	}
	return names
}

func TestConsoleSelectedProofInventoryDropsOnlyConsoleGates(t *testing.T) {
	t.Parallel()
	full := consoleSelectionInventory()
	excluded, err := ConsoleSelectedProofInventory(full, ConsoleProofSelection{Reason: ConsoleProofReasonUnchanged})("base", "head")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := consoleSelectionNames(excluded), []string{"go-test", "bun-tools-test", "redact-public"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default inventory = %v, want %v (non-console gates kept in order)", got, want)
	}
	if slices.ContainsFunc(excluded, IsConsoleProofGate) {
		t.Fatalf("default inventory still carries a console gate: %v", consoleSelectionNames(excluded))
	}
	included, err := ConsoleSelectedProofInventory(full, ConsoleProofSelection{Include: true, Reason: ConsoleProofReasonRequested})("base", "head")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := full("base", "head")
	if !reflect.DeepEqual(included, all) || !slices.ContainsFunc(included, IsConsoleProofGate) {
		t.Fatalf("selected inventory = %v, want the full inventory %v", consoleSelectionNames(included), consoleSelectionNames(all))
	}
}

// The certifying path seals the selected inventory into the profile and later
// re-derives it to decide runtime-artifact publication. Both calls must agree,
// and the range-resolved inventory must equal the explicitly selected one.
func TestConsoleAwareProofInventoryIsStableForProfileRederivation(t *testing.T) {
	t.Parallel()
	repo, c := consoleSelectionRepo(t)
	ctx := context.Background()
	full := consoleSelectionInventory()
	for _, rng := range [][2]string{{c["base"], c["docs"]}, {c["docs"], c["console"]}} {
		aware := ConsoleAwareProofInventory(ctx, repo, false, full)
		first, err := aware(rng[0], rng[1])
		if err != nil {
			t.Fatal(err)
		}
		second, err := aware(rng[0], rng[1])
		if err != nil {
			t.Fatal(err)
		}
		selection, err := ResolveConsoleProofSelection(ctx, repo, rng[0], rng[1], false)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := ConsoleSelectedProofInventory(full, selection)(rng[0], rng[1])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(first, sealed) {
			t.Fatalf("range %s...%s: inventories disagree: %v / %v / %v", rng[0][:8], rng[1][:8],
				consoleSelectionNames(first), consoleSelectionNames(second), consoleSelectionNames(sealed))
		}
		if slices.ContainsFunc(first, IsConsoleProofGate) != selection.Include {
			t.Fatalf("range %s...%s: console gates present=%t, selection %+v", rng[0][:8], rng[1][:8], slices.ContainsFunc(first, IsConsoleProofGate), selection)
		}
	}
}

func TestConsoleProofRequestTravelsByContext(t *testing.T) {
	t.Parallel()
	if ConsoleProofRequested(context.Background()) {
		t.Fatal("a bare context requested console gates")
	}
	if ConsoleProofRequested(ContextWithConsoleProofRequest(context.Background(), false)) {
		t.Fatal("an explicit false request turned into a console request")
	}
	if !ConsoleProofRequested(ContextWithConsoleProofRequest(context.Background(), true)) {
		t.Fatal("--with-console did not reach the runner context")
	}
}
