package breadth

import (
	"os"
	"path/filepath"
	"testing"

	"compound/internal/mem"
)

func TestReadOnlyMemoryReaderUsesSemanticCacheWithoutWriting(t *testing.T) {
	home := t.TempDir()
	claims := []mem.Claim{
		{ID: "winner", Entity: "fixture", Predicate: "decision", Object: "use a relay between steps"},
		{ID: "decoy", Entity: "fixture", Predicate: "decision", Object: "the immune system protects cells"},
	}
	docs := []mem.ClaimText{}
	embedder := newFakeEmbedder(2)
	embedder.vectors = map[string][]float32{"system": {1, 0}}
	for i, c := range claims {
		docs = append(docs, mem.ClaimText{ClaimID: c.ID, Text: mem.ClaimIndexText(c)})
		v := []float32{1, 0}
		if i == 1 {
			v = []float32{0, 1}
		}
		embedder.vectors[mem.ClaimIndexText(c)] = v
	}
	seed, err := NewEngine(EngineConfig{Embedder: embedder, CompoundHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seed.IndexWithError(docs, 1); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(vectorCacheDir(t, home, embedder), "manifest.json"), vectorsFilePath(t, home, embedder)}
	before := map[string]string{}
	mtimes := map[string]int64{}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		before[p] = string(b)
		st, _ := os.Stat(p)
		mtimes[p] = st.ModTime().UnixNano()
	}
	embedder.calls = 0
	engine, err := NewEngine(EngineConfig{Embedder: embedder, CompoundHome: home, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	reader := &memoryReader{engine: engine}
	if err = reader.Index(claims, 2); err != nil {
		t.Fatal(err)
	}
	if embedder.calls != 0 {
		t.Fatal("read embedded documents")
	}
	hits, degraded, err := reader.QueryWithDiagnostics("system", mem.GateList, []string{"winner", "decoy"}, 1)
	if err != nil || len(degraded) != 0 || len(hits) != 1 || hits[0].ClaimID != "winner" {
		t.Fatalf("semantic recall: %v %v %v", hits, degraded, err)
	}
	hits, err = reader.Query("system", mem.GateList, []string{"decoy"}, 2)
	for _, h := range hits {
		if h.ClaimID == "winner" {
			t.Fatal("masked claim leaked")
		}
	}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		st, _ := os.Stat(p)
		if string(b) != before[p] || st.ModTime().UnixNano() != mtimes[p] {
			t.Fatal("read changed cache")
		}
	}
}
func TestReadOnlyMemoryReaderReportsMissingCacheWithoutEmbeddingOrWriting(t *testing.T) {
	home := t.TempDir()
	embedder := newFakeEmbedder(2)
	engine, err := NewEngine(EngineConfig{Embedder: embedder, CompoundHome: home, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	reader := &memoryReader{engine: engine}
	if err = reader.Index([]mem.Claim{{ID: "c1", Entity: "archive", Predicate: "decision", Object: "keep history"}}, 1); err != nil {
		t.Fatal(err)
	}
	hits, degraded, err := reader.QueryWithDiagnostics("archive", mem.GateList, []string{"c1"}, 1)
	if err != nil || len(hits) != 1 || len(degraded) != 1 || degraded[0] != "vector" {
		t.Fatalf("%v %v %v", hits, degraded, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 || embedder.calls != 0 {
		t.Fatalf("read produced side effects: files=%d embeds=%d error=%v", len(entries), embedder.calls, err)
	}
}
