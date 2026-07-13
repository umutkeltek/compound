package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/mem"
)

// TestLargeToolOutputExternalizedAsObservedNotAmbient drives a PostToolUse hook with a ~340KB
// tool body and proves C3's contract at the real seam: the body is externalized to a
// content-addressed rebuildable blob + ONE observed-class candidate carrying a compact pointer;
// it never floods the summary, never touches the ledger, never enters ambient recall, and is
// retrievable only via ReadToolOutput.
func TestLargeToolOutputExternalizedAsObservedNotAmbient(t *testing.T) {
	dir := t.TempDir()
	if err := home.Init(dir, "test", fixedNow); err != nil {
		t.Fatalf("home init: %v", err)
	}
	deps := baseDeps()
	deps.HomePath = dir

	store, err := mem.Open(dir)
	if err != nil {
		t.Fatalf("mem open: %v", err)
	}
	before, err := store.LedgerDigest()
	if err != nil {
		t.Fatalf("ledger digest before: %v", err)
	}

	body := strings.Repeat("SECRETLARGEOUTPUT", 20000) // ~340 KB
	cid := mem.ToolOutputCID([]byte(body))

	res, err := Run(Fixture{
		Hook:       "post-tool-use",
		Harness:    "claude_code",
		Cwd:        "/repo/x",
		Tool:       "Bash",
		WorkID:     "work_1177cb550af3",
		ToolOutput: body,
	}, deps, fixedNow)
	if err != nil {
		t.Fatalf("post-tool-use run: %v", err)
	}

	// (a) The emitted summary is bounded and does NOT carry the body verbatim.
	if strings.Contains(res.Summary, body[:128]) {
		t.Fatal("summary leaked the tool-output body into context")
	}
	if len(res.Summary) > 400 {
		t.Fatalf("summary must stay bounded, got %d chars: %q", len(res.Summary), res.Summary)
	}
	if !strings.Contains(res.Summary, cid) {
		t.Fatalf("summary must carry the pointer cid, got %q", res.Summary)
	}

	// (b) Exactly one observed candidate with the tool-output source-trace and no promotion basis.
	candDir := filepath.Join(dir, "inbox", "memory-candidates")
	entries, err := os.ReadDir(candDir)
	if err != nil {
		t.Fatalf("read candidate inbox: %v", err)
	}
	var cand mem.Candidate
	found := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var c mem.Candidate
		if err := candidates.ReadCandidate(dir, candidates.Memory, strings.TrimSuffix(e.Name(), ".json"), &c); err != nil {
			t.Fatalf("read candidate %s: %v", e.Name(), err)
		}
		if c.SourceTraceRef == "tool-output:"+cid {
			cand = c
			found++
		}
	}
	if found != 1 {
		t.Fatalf("want exactly 1 observed tool-output candidate, got %d", found)
	}
	if cand.Derivation != "observed" || cand.State != "proposed" || cand.PromotionBasis != "" {
		t.Fatalf("candidate not observed/proposed/no-basis: derivation=%q state=%q basis=%q", cand.Derivation, cand.State, cand.PromotionBasis)
	}

	// (c) Ledger byte-identical: capture added zero ledger bytes.
	after, err := store.LedgerDigest()
	if err != nil {
		t.Fatalf("ledger digest after: %v", err)
	}
	if before != after {
		t.Fatalf("ledger digest changed by tool-output capture: %s -> %s", before, after)
	}

	// (d) A fresh ContextPack (session-start) leaks the body/cid into NEITHER Included NOR ActiveMemory.
	ss, err := Run(Fixture{Hook: "session-start", Harness: "claude_code", Cwd: "/repo/x", Task: "audit", RepoKind: "product"}, deps, fixedNow)
	if err != nil {
		t.Fatalf("session-start run: %v", err)
	}
	var pack *envelope.ContextPack
	for _, e := range ss.Produced {
		if p, ok := e.(*envelope.ContextPack); ok {
			pack = p
		}
	}
	if pack == nil {
		t.Fatal("session-start produced no context pack")
	}
	for _, inc := range pack.Included {
		if strings.Contains(inc.Ref, cid) || strings.Contains(inc.Ref, body[:64]) {
			t.Fatalf("context pack Included leaked externalized tool output: %q", inc.Ref)
		}
	}
	for _, am := range pack.ActiveMemory {
		if strings.Contains(am, cid) || strings.Contains(am, body[:64]) {
			t.Fatalf("context pack ActiveMemory leaked externalized tool output: %q", am)
		}
	}

	// (e) ReadToolOutput returns the exact original bytes.
	got, err := store.ReadToolOutput(cid)
	if err != nil {
		t.Fatalf("read tool output: %v", err)
	}
	if string(got) != body {
		t.Fatalf("read round-trip mismatch: %d vs %d bytes", len(got), len(body))
	}

	// (f) Re-running the identical PostToolUse is idempotent: still exactly one candidate.
	if _, err := Run(Fixture{
		Hook: "post-tool-use", Harness: "claude_code", Cwd: "/repo/x", Tool: "Bash",
		WorkID: "work_1177cb550af3", ToolOutput: body,
	}, deps, fixedNow); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	entries2, _ := os.ReadDir(candDir)
	n := 0
	for _, e := range entries2 {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("re-run must be idempotent; want 1 candidate, got %d", n)
	}
}
