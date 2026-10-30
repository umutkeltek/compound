package corpus

import (
	"context"
	"strings"
	"testing"

	"compound/internal/mem"
	"compound/internal/sourceobs"
)

func TestRecallReadsBodiesOnlyAfterGlobalSelection(t *testing.T) {
	for _, radius := range []int{0, 1} {
		t.Run(string(rune('0'+radius)), func(t *testing.T) {
			f := seedCorpusTextFixture(t, "public", strings.Repeat("A retained archive decision has source evidence. ", 80))
			if len(f.chunks) < 4 {
				t.Fatal("fixture needs several ranked chunks")
			}
			previous := readSourceInPlaceBody
			reads := 0
			readSourceInPlaceBody = func(path string) ([]byte, error) { reads++; return previous(path) }
			t.Cleanup(func() { readSourceInPlaceBody = previous })
			r := Search(context.Background(), f.homePath, []LaneConfig{f.lane}, func(LaneConfig) (Embedder, error) { return f.embedder, nil }, "archive", 1, QueryOptions{IncludeText: true, NeighborRadius: radius})
			if len(r.Evidence) != 1 || r.Evidence[0].Text == "" {
				t.Fatalf("selected evidence missing: %+v", r)
			}
			if radius > 0 {
				if len(r.Evidence[0].Neighbors) == 0 {
					t.Fatal("selected evidence lost its adjacent context")
				}
				for _, neighbor := range r.Evidence[0].Neighbors {
					if neighbor.Text == "" || neighbor.TextStatus != string(ChunkTextOK) {
						t.Fatalf("neighbor text missing: %+v", neighbor)
					}
				}
			}
			if reads > 1+2*radius {
				t.Fatalf("read %d source bodies for one selected hit with radius %d; unselected candidates must not be hydrated", reads, radius)
			}
		})
	}
}

func TestRecallRechecksPrivacyBeforeMaterializingSelectedEvidence(t *testing.T) {
	f := seedCorpusTextFixtureWith(t, corpusFixtureOpts{privacy: "public", fileName: "sensitive-source-name.md", body: "SOURCE_BECAME_PRIVATE_AFTER_RANKING"})
	f.lane.Privacy = "private"
	f.lane.SourceInPlacePrivacyGate = true
	previous := runCorpusEngineQuery
	runCorpusEngineQuery = func(engine *mem.ProtocolEngine, text string, k int) ([]mem.Hit, error) {
		hits, err := previous(engine, text, k)
		if err != nil {
			return nil, err
		}
		current, e := sourceobs.LoadSourceManifest(f.homePath, f.lane.SourceFamily)
		if e != nil {
			t.Fatal(e)
		}
		current.Items[0].Privacy = "private"
		if _, e = sourceobs.RecordSourceManifest(f.homePath, current); e != nil {
			t.Fatal(e)
		}
		return hits, nil
	}
	t.Cleanup(func() { runCorpusEngineQuery = previous })
	r := Search(context.Background(), f.homePath, []LaneConfig{f.lane}, func(LaneConfig) (Embedder, error) { return f.embedder, nil }, "archive", 1, QueryOptions{})
	if len(r.Evidence) != 1 {
		t.Fatalf("expected suppressed evidence: %+v", r)
	}
	hit := r.Evidence[0]
	if hit.Text != "" || hit.TextStatus != string(ChunkTextSuppressedPrivate) || hit.Title != "(private source)" || len(hit.Neighbors) != 0 {
		t.Fatalf("changed privacy leaked selected evidence: %+v", hit)
	}
}
