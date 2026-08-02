package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"compound/internal/persist"
)

// testDSN returns the test database DSN or skips the test.
// Set COMPOUND_TEST_DSN to run integration tests:
//
//	COMPOUND_TEST_DSN="postgres://compound:test@localhost:5432/compound_test?sslmode=disable" go test ./internal/persist/pg/ -v
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("COMPOUND_TEST_DSN")
	if dsn == "" {
		t.Skip("COMPOUND_TEST_DSN not set; skipping integration test")
	}
	return dsn
}

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := testDSN(t)
	ctx := context.Background()
	cfg := persist.Config{
		Backend:          persist.BackendPostgres,
		DSN:              dsn,
		VectorDimensions: 8, // small dims for fast tests
	}
	store, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		store.Close()
		t.Fatalf("EnsureSchema: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ─── Connection + Schema ─────────────────────────────────────────────────────────

func TestOpenAndEnsureSchema(t *testing.T) {
	store := testStore(t)
	// EnsureSchema is idempotent — calling twice should not error
	ctx := context.Background()
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema (2nd call): %v", err)
	}
}

func TestTableCounts(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	counts := store.TableCounts(ctx)
	// All expected tables should be present
	expected := []string{"claims", "edges", "candidates", "events", "work_log", "work_envelopes", "vectors"}
	for _, name := range expected {
		if _, ok := counts[name]; !ok {
			t.Errorf("TableCounts missing table %q", name)
		}
	}
}

// ─── Claims ──────────────────────────────────────────────────────────────────────

func TestAppendClaimAndFold(t *testing.T) {
	store := testStore(t)
	now := time.Now()

	claim := persist.Claim{
		ID:         fmt.Sprintf("test-claim-%d", now.UnixNano()),
		Entity:     "test::entity",
		Predicate:  "color",
		Object:     "blue",
		Confidence: 0.95,
		Derivation: "extracted",
		Visibility: "operational",
		State:      "promoted",
	}

	seq, err := store.AppendClaim(claim, now)
	if err != nil {
		t.Fatalf("AppendClaim: %v", err)
	}
	if seq <= 0 {
		t.Fatalf("AppendClaim returned non-positive seq: %d", seq)
	}

	// Fold back
	claims, err := store.FoldClaims(persist.ClaimFilter{
		Entity: "test::entity",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("FoldClaims: %v", err)
	}
	if len(claims) == 0 {
		t.Fatal("FoldClaims returned 0 claims")
	}

	found := false
	for _, c := range claims {
		if c.ID == claim.ID && c.Object == "blue" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FoldClaims did not return the appended claim (id=%s)", claim.ID)
	}
}

func TestFoldClaimsFilters(t *testing.T) {
	store := testStore(t)
	now := time.Now()

	// Append two claims with different topics (Topic maps to predicate in fold)
	c1 := persist.Claim{
		ID: fmt.Sprintf("filter-a-%d", now.UnixNano()), Entity: "test::filters",
		Predicate: "size", Object: "large", Confidence: 0.8,
		Derivation: "extracted", Visibility: "operational", State: "promoted",
	}
	c2 := persist.Claim{
		ID: fmt.Sprintf("filter-b-%d", now.UnixNano()), Entity: "test::filters",
		Predicate: "weight", Object: "heavy", Confidence: 0.7,
		Derivation: "inferred", Visibility: "operational", State: "promoted",
	}
	if _, err := store.AppendClaim(c1, now); err != nil {
		t.Fatalf("AppendClaim c1: %v", err)
	}
	if _, err := store.AppendClaim(c2, now); err != nil {
		t.Fatalf("AppendClaim c2: %v", err)
	}

	// Filter by topic (predicate)
	claims, err := store.FoldClaims(persist.ClaimFilter{
		Entity: "test::filters",
		Topic:  "size",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("FoldClaims with topic filter: %v", err)
	}
	for _, c := range claims {
		if c.Predicate != "size" {
			t.Errorf("expected predicate 'size', got %q", c.Predicate)
		}
	}
}

func TestNextSeq(t *testing.T) {
	store := testStore(t)
	seq1, err := store.NextSeq()
	if err != nil {
		t.Fatalf("NextSeq (1st): %v", err)
	}
	seq2, err := store.NextSeq()
	if err != nil {
		t.Fatalf("NextSeq (2nd): %v", err)
	}
	if seq2 <= seq1 {
		t.Errorf("NextSeq not monotonic: %d then %d", seq1, seq2)
	}
}

// ─── Edges ───────────────────────────────────────────────────────────────────────

func TestAppendEdge(t *testing.T) {
	store := testStore(t)
	now := time.Now()

	edge := persist.Edge{
		From:    "claim-a",
		To:      "claim-b",
		Kind:    "supersedes",
		ClaimID: "claim-a",
	}
	if err := store.AppendEdge(edge, now); err != nil {
		t.Fatalf("AppendEdge: %v", err)
	}
}

// ─── Candidates ──────────────────────────────────────────────────────────────────

func TestCandidateLifecycle(t *testing.T) {
	store := testStore(t)
	now := time.Now()

	cand := persist.Candidate{
		ID:         fmt.Sprintf("cand-%d", now.UnixNano()),
		Entity:     "test::candidates",
		Predicate:  "hypothesis",
		Object:     "postgres-is-fast",
		Confidence: 0.7,
		Derivation: "extracted",
		Visibility: "candidate",
		State:      "proposed",
	}

	// Append
	if err := store.AppendCandidate(cand, now); err != nil {
		t.Fatalf("AppendCandidate: %v", err)
	}

	// Get
	got, err := store.GetCandidate(cand.ID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if got.State != "proposed" {
		t.Errorf("expected state 'proposed', got %q", got.State)
	}
	if got.Object != "postgres-is-fast" {
		t.Errorf("expected object 'postgres-is-fast', got %q", got.Object)
	}

	// Update (adjudication: proposed → parked)
	err = store.UpdateCandidate(cand.ID, func(c *persist.Candidate) error {
		c.State = "parked"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateCandidate: %v", err)
	}

	// Verify update
	updated, err := store.GetCandidate(cand.ID)
	if err != nil {
		t.Fatalf("GetCandidate after update: %v", err)
	}
	if updated.State != "parked" {
		t.Errorf("expected state 'parked' after update, got %q", updated.State)
	}

	// List by state
	list, err := store.ListCandidates(persist.CandidateFilter{
		Entity: "test::candidates",
		State:  "parked",
	})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	found := false
	for _, c := range list {
		if c.ID == cand.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListCandidates did not find the parked candidate")
	}
}

// ─── Events ──────────────────────────────────────────────────────────────────────

func TestEventPutAndRead(t *testing.T) {
	store := testStore(t)

	id := fmt.Sprintf("event-%d", time.Now().UnixNano())
	payload := map[string]any{
		"type":  "test_event",
		"count": 42,
		"tags":  []string{"a", "b"},
	}

	if err := store.PutJSON(id, payload); err != nil {
		t.Fatalf("PutJSON: %v", err)
	}

	rec, err := store.Read(id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rec == nil {
		t.Fatal("Read returned nil")
	}
	if rec.ID != id {
		t.Errorf("expected ID %q, got %q", id, rec.ID)
	}

	// Verify payload round-trips via Bytes
	var m map[string]any
	if err := json.Unmarshal(rec.Bytes, &m); err != nil {
		t.Fatalf("Unmarshal rec.Bytes: %v", err)
	}
	if m["type"] != "test_event" {
		t.Errorf("expected type 'test_event', got %v", m["type"])
	}
}

func TestEventPutJSONIfAbsent(t *testing.T) {
	store := testStore(t)

	id := fmt.Sprintf("absent-%d", time.Now().UnixNano())

	// First write should create
	created, err := store.PutJSONIfAbsent(id, map[string]string{"v": "1"})
	if err != nil {
		t.Fatalf("PutJSONIfAbsent (1st): %v", err)
	}
	if !created {
		t.Error("PutJSONIfAbsent should return created=true on first write")
	}

	// Second write should NOT overwrite
	created, err = store.PutJSONIfAbsent(id, map[string]string{"v": "2"})
	if err != nil {
		t.Fatalf("PutJSONIfAbsent (2nd): %v", err)
	}
	if created {
		t.Error("PutJSONIfAbsent should return created=false on duplicate")
	}

	// Verify original value preserved
	rec, err := store.Read(id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var m map[string]string
	json.Unmarshal(rec.Bytes, &m)
	if m["v"] != "1" {
		t.Errorf("expected original value '1', got %q", m["v"])
	}
}

func TestEventScan(t *testing.T) {
	store := testStore(t)

	id := fmt.Sprintf("scan-%d", time.Now().UnixNano())
	if err := store.PutJSON(id, map[string]string{"scan": "test"}); err != nil {
		t.Fatalf("PutJSON: %v", err)
	}

	events, err := store.Scan(persist.EventScanOptions{Limit: 100})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	found := false
	for _, e := range events {
		if e.ID == id {
			found = true
		}
	}
	if !found {
		t.Error("Scan did not return the stored event")
	}
}

// ─── Vectors ─────────────────────────────────────────────────────────────────────

func TestVectorUpsertSearchDelete(t *testing.T) {
	store := testStore(t)

	ref := fmt.Sprintf("vec-%d", time.Now().UnixNano())
	embedding := []float32{1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0}
	meta := map[string]string{"topic": "testing", "source": "unit-test"}

	// Upsert
	if err := store.Upsert(ref, embedding, meta); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search with similar vector — should return high similarity
	query := []float32{0.9, 0.1, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0}
	hits, err := store.Search(query, 5, map[string]string{"topic": "testing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search returned 0 hits")
	}
	if hits[0].Ref != ref {
		t.Errorf("expected first hit ref %q, got %q", ref, hits[0].Ref)
	}
	if hits[0].Score < 0.9 {
		t.Errorf("expected high similarity score, got %f", hits[0].Score)
	}

	// Count
	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count < 1 {
		t.Error("Count should be >= 1 after upsert")
	}

	// Delete
	if err := store.Delete(ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Search after delete — should not find it
	hits2, err := store.Search(query, 5, map[string]string{"topic": "testing"})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, h := range hits2 {
		if h.Ref == ref {
			t.Error("Search returned deleted vector")
		}
	}
}

func TestVectorUpsertOverwrites(t *testing.T) {
	store := testStore(t)

	ref := fmt.Sprintf("vec-ow-%d", time.Now().UnixNano())
	v1 := []float32{1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0}
	v2 := []float32{0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0}

	if err := store.Upsert(ref, v1, nil); err != nil {
		t.Fatalf("Upsert v1: %v", err)
	}
	if err := store.Upsert(ref, v2, nil); err != nil {
		t.Fatalf("Upsert v2 (overwrite): %v", err)
	}

	// Search with v2 direction — should find the updated vector
	hits, err := store.Search(v2, 1, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 || hits[0].Ref != ref {
		t.Error("Upsert did not overwrite — search with v2 did not find ref")
	}
}

// ─── Interface compliance ────────────────────────────────────────────────────────

func TestInterfaceCompliance(t *testing.T) {
	// Compile-time check that *Store satisfies all interfaces
	var _ persist.LedgerStore = (*Store)(nil)
	var _ persist.EventStore = (*Store)(nil)
	var _ persist.VectorStore = (*Store)(nil)
}
