package panel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
)

func TestSelectLensesIncludesAndExcludesReasons(t *testing.T) {
	homePath := t.TempDir()
	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []capgraph.Node{
		{ID: "lens:included", Kind: "lens", Name: "Included", Triggers: []string{"memory_candidate"}, Weight: 1.5},
		{ID: "lens:token", Kind: "lens", Name: "Token", Triggers: []string{"memory"}, Weight: 0.8},
		{ID: "lens:retired", Kind: "lens", Name: "Retired", Triggers: []string{"memory_candidate"}, Retired: true},
		{ID: "lens:evidence-only", Kind: "lens", Name: "Evidence Only", Triggers: []string{"memory_candidate"}, EvidenceOnly: true},
		{ID: "lens:missing-prompt", Kind: "lens", Name: "Missing Prompt", Triggers: []string{"memory_candidate"}},
		{ID: "lens:mismatch", Kind: "lens", Name: "Mismatch", Triggers: []string{"external_repo"}},
		{ID: "tool:not-a-lens", Kind: "tool", Name: "Ignored Tool", Triggers: []string{"memory_candidate"}},
	}
	for _, node := range nodes {
		if err := store.Add(node); err != nil {
			t.Fatalf("add node %s: %v", node.ID, err)
		}
	}
	prompts := map[string]string{
		"lens:included":      "included prompt\n",
		"lens:token":         "token prompt\n",
		"lens:retired":       "retired prompt\n",
		"lens:evidence-only": "evidence prompt\n",
		"lens:mismatch":      "mismatch prompt\n",
	}
	for id, body := range prompts {
		writeLensPromptFixture(t, homePath, id, body)
	}

	selection, err := SelectLenses(homePath, "memory_candidate")
	if err != nil {
		t.Fatalf("select lenses: %v", err)
	}

	included := map[string]Lens{}
	for _, lens := range selection.Included {
		included[lens.NodeID] = lens
	}
	if len(included) != 2 {
		t.Fatalf("included = %+v, want 2 lenses", included)
	}
	for id, body := range map[string]string{"lens:included": prompts["lens:included"], "lens:token": prompts["lens:token"]} {
		lens := included[id]
		if lens.NodeID == "" {
			t.Fatalf("missing included lens %s in %+v", id, included)
		}
		if got, want := lens.PromptSHA256, sha256Hex([]byte(body)); got != want {
			t.Fatalf("%s prompt hash = %s, want %s", id, got, want)
		}
		if lens.PromptPath != filepath.Join(homePath, "registries", "lens-prompts", id+".md") {
			t.Fatalf("%s prompt path = %s", id, lens.PromptPath)
		}
	}
	if len(selection.IncludedRefs) != 2 {
		t.Fatalf("included refs = %+v, want 2", selection.IncludedRefs)
	}
	refByID := map[string]IncludedLens{}
	for _, ref := range selection.IncludedRefs {
		refByID[ref.Ref] = ref
	}
	if refByID["lens:included"].Reason != "trigger_match" || refByID["lens:included"].Weight != 1.5 {
		t.Fatalf("included ref not populated: %+v", refByID["lens:included"])
	}

	reasons := map[string]string{}
	for _, excluded := range selection.Excluded {
		reasons[excluded.Ref] = excluded.Reason
	}
	wantReasons := map[string]string{
		"lens:retired":        "retired",
		"lens:evidence-only":  "evidence_only",
		"lens:missing-prompt": "no_prompt_in_registry",
		"lens:mismatch":       "trigger_mismatch",
	}
	if !reflect.DeepEqual(reasons, wantReasons) {
		t.Fatalf("excluded reasons = %+v, want %+v", reasons, wantReasons)
	}
}

func TestImportLensManifestIdempotent(t *testing.T) {
	homePath := t.TempDir()
	manifest := filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml")
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	first, err := ImportLensManifest(homePath, manifest, now)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if !first.Changed || first.NodesChanged != 6 || first.PromptsChanged != 6 || !first.AuthorityAppended {
		t.Fatalf("first import result = %+v, want all content changed", first)
	}
	nodesBefore := readTestFile(t, filepath.Join(homePath, "registries", "capability-nodes.jsonl"))
	authorityBefore := readTestFile(t, filepath.Join(homePath, "registries", "lens-source-authority.jsonl"))

	second, err := ImportLensManifest(homePath, manifest, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Changed || second.NodesChanged != 0 || second.PromptsChanged != 0 || second.AuthorityAppended {
		t.Fatalf("second import result = %+v, want no changes", second)
	}
	if got := readTestFile(t, filepath.Join(homePath, "registries", "capability-nodes.jsonl")); !bytes.Equal(got, nodesBefore) {
		t.Fatalf("node log changed on idempotent import")
	}
	if got := readTestFile(t, filepath.Join(homePath, "registries", "lens-source-authority.jsonl")); !bytes.Equal(got, authorityBefore) {
		t.Fatalf("authority log changed on idempotent import")
	}

	store, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	lensCount := 0
	for _, node := range nodes {
		if node.Kind == "lens" {
			lensCount++
		}
	}
	if lensCount != 6 {
		t.Fatalf("lens node count = %d, want 6", lensCount)
	}
	selection, err := SelectLenses(homePath, "memory_candidate")
	if err != nil {
		t.Fatalf("select imported lenses: %v", err)
	}
	if len(selection.Included) != 6 || len(selection.Excluded) != 0 {
		t.Fatalf("imported memory_candidate selection = %+v, want six included and none excluded", selection)
	}
}

func writeLensPromptFixture(t *testing.T, homePath, id, body string) {
	t.Helper()
	dir := filepath.Join(homePath, "registries", "lens-prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLensManifestFixtureHasRequiredPrompts(t *testing.T) {
	data := readTestFile(t, filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml"))
	for _, id := range []string{
		"duplicate-risk",
		"evidence-strength",
		"contradiction-with-existing-memory",
		"fossilization-risk",
		"privacy-risk",
		"inversion-falsification",
	} {
		if !strings.Contains(string(data), "id: "+id) {
			t.Fatalf("manifest missing lens %s", id)
		}
	}
	if got := strings.Count(string(data), `{"confidence": <0..1>, "findings": [{"claim": "...", "severity": "low|medium|high", "evidence_refs": ["..."]}]}`); got != 6 {
		t.Fatalf("strict JSON contract count = %d, want 6", got)
	}
}
