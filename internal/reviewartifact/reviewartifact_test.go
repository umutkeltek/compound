package reviewartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin the exact behaviour every migrated call site relied on. They
// are written to FAIL if the shared mechanism ever drifts from the per-package
// helpers it replaced — that drift is the only way collapsing 17 writers into
// one module could change a persisted artifact.

func TestDigest12AndID(t *testing.T) {
	want := hex.EncodeToString(func() []byte { s := sha256.Sum256([]byte("abc")); return s[:] }())[:12]
	if got := Digest12("abc"); got != want {
		t.Fatalf("Digest12 = %q, want %q", got, want)
	}
	if got := Digest12("abc"); len(got) != 12 {
		t.Fatalf("Digest12 length = %d, want 12", len(got))
	}
	// ID joins parts with "|" then prefixes — matches the old idFor/idSkill/idImprovement.
	if got, want := ID("evp_", "cand-1", "/corpus"), "evp_"+Digest12("cand-1|/corpus"); got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := ID("p_", "x"), "p_"+Digest12("x"); got != want {
		t.Fatalf("ID single part = %q, want %q", got, want)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"Hello, World!", 0, "hello-world"},
		{"  spaced  out  ", 0, "spaced-out"},
		{"___leading_and_trailing___", 0, "leading-and-trailing"},
		{"UPPER_Snake.Case", 0, "upper-snake-case"},
		{"", 0, ""},
		{"!!!", 0, ""},
		{"a1b2c3", 0, "a1b2c3"},
	}
	for _, c := range cases {
		if got := Slugify(c.in, c.maxLen); got != c.want {
			t.Errorf("Slugify(%q, %d) = %q, want %q", c.in, c.maxLen, got, c.want)
		}
	}
	// maxLen truncates and re-trims so the result never ends in a dash.
	long := "this-is-a-very-long-subject-line-that-exceeds-the-limit-by-a-lot"
	got := Slugify(long, 48)
	if len(got) > 48 {
		t.Fatalf("Slugify maxLen not applied: len=%d", len(got))
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("Slugify truncation left a trailing dash: %q", got)
	}
	// A value whose 48th char lands on a dash boundary must be trimmed.
	if g := Slugify("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbb", 48); strings.HasSuffix(g, "-") {
		t.Fatalf("trailing dash after truncation: %q", g)
	}
}

func TestUniqueSortedAndUnique(t *testing.T) {
	in := []string{" b ", "a", "b", "", "c", "a"}
	if got := UniqueSorted(in); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("UniqueSorted = %v, want [a b c]", got)
	}
	// Unique preserves first-seen order; UniqueSorted does not.
	if got := Unique(in); strings.Join(got, ",") != "b,a,c" {
		t.Fatalf("Unique = %v, want [b a c]", got)
	}
	if got := Unique(nil); got != nil {
		t.Fatalf("Unique(nil) = %v, want nil", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", "x ", "y"); got != "x" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "x")
	}
	if got := FirstNonEmpty("", "   "); got != "" {
		t.Fatalf("FirstNonEmpty all-empty = %q, want \"\"", got)
	}
}

func TestHashTarget(t *testing.T) {
	if exists, hash, err := HashTarget(""); err != nil || exists || hash != "" {
		t.Fatalf("empty path: (%v,%q,%v), want (false,\"\",nil)", exists, hash, err)
	}
	if exists, hash, err := HashTarget(filepath.Join(t.TempDir(), "nope")); err != nil || exists || hash != "" {
		t.Fatalf("missing path: (%v,%q,%v), want (false,\"\",nil)", exists, hash, err)
	}

	// File branch: identical to the file-only hashers it replaces.
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	exists, hash, err := HashTarget(fp)
	wantSum := sha256.Sum256([]byte("payload"))
	if err != nil || !exists || hash != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("file hash = (%v,%q,%v), want (true,%q,nil)", exists, hash, err, hex.EncodeToString(wantSum[:]))
	}

	// Directory branch: stable regardless of file creation order.
	dirA := t.TempDir()
	_ = os.WriteFile(filepath.Join(dirA, "a"), []byte("1"), 0o644)
	_ = os.WriteFile(filepath.Join(dirA, "b"), []byte("2"), 0o644)
	dirB := t.TempDir()
	_ = os.WriteFile(filepath.Join(dirB, "b"), []byte("2"), 0o644)
	_ = os.WriteFile(filepath.Join(dirB, "a"), []byte("1"), 0o644)
	_, hashA, _ := HashTarget(dirA)
	_, hashB, _ := HashTarget(dirB)
	if hashA == "" || hashA != hashB {
		t.Fatalf("dir hash not order-independent: %q vs %q", hashA, hashB)
	}
}

func TestWrite(t *testing.T) {
	home := t.TempDir()
	type payload struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	p := payload{ID: "evp_x", Name: "demo"}
	paths, err := Write(home, "distiller-eval", "evp_x", p, " ", "# Title\n")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(home, "reviews", "distiller-eval")
	if paths.PlanPath != filepath.Join(wantDir, "evp_x.json") {
		t.Fatalf("PlanPath = %q", paths.PlanPath)
	}
	if paths.ReviewPath != filepath.Join(wantDir, "evp_x.md") {
		t.Fatalf("ReviewPath = %q", paths.ReviewPath)
	}

	jsonBytes, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	// JSON is indented with the caller's indent and ends in exactly one newline.
	if !strings.HasSuffix(string(jsonBytes), "}\n") || strings.HasSuffix(string(jsonBytes), "}\n\n") {
		t.Fatalf("json trailing newline wrong: %q", string(jsonBytes))
	}
	if !strings.Contains(string(jsonBytes), "\n \"id\":") {
		t.Fatalf("json indent not preserved (want single space): %q", string(jsonBytes))
	}
	var got payload
	if err := json.Unmarshal(jsonBytes, &got); err != nil || got != p {
		t.Fatalf("round-trip = %+v err=%v", got, err)
	}

	md, _ := os.ReadFile(paths.ReviewPath)
	if string(md) != "# Title\n" {
		t.Fatalf("markdown written non-verbatim: %q", string(md))
	}

	// Two-space indent is preserved too (the other half of the call sites).
	paths2, err := Write(home, "capability-skills", "skp_y", p, "  ", "x")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(paths2.PlanPath)
	if !strings.Contains(string(b2), "\n  \"id\":") {
		t.Fatalf("two-space indent not preserved: %q", string(b2))
	}

	// Empty id is rejected; empty home is rejected.
	if _, err := Write(home, "x", "  ", p, " ", "m"); err == nil {
		t.Fatal("expected error for empty id")
	}
	if _, err := Write("  ", "x", "id", p, " ", "m"); err == nil {
		t.Fatal("expected error for empty home")
	}
}

func TestTimestamp(t *testing.T) {
	at := time.Date(2026, 6, 30, 12, 0, 0, 0, time.FixedZone("x", 3*3600))
	if got := Timestamp(at); got != "2026-06-30T09:00:00Z" {
		t.Fatalf("Timestamp = %q, want UTC RFC3339", got)
	}
}
