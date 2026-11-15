package promotedriver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hook"
	"compound/internal/mem"
)

// capacityLessonArgs is a large, synthetic lesson (trigger about 220 bytes,
// how about 280 bytes) so the index fills after a few hundred lessons.
func capacityLessonArgs(i int) (why, how, trigger string) {
	words := []string{"overwrite-every-remote-file", "delete-the-stale-rows", "prune-the-old-branches", "reset-the-shared-cache", "push-without-review"}
	trigger = fmt.Sprintf(`Bash:\bfill-tool-%04d\s+apply-step\b.*--(%s)`, i, strings.Join(words, "|"))
	how = fmt.Sprintf("Lesson %04d: %s", i, strings.Repeat("preview the step first and read every change it lists. ", 5))
	return fmt.Sprintf("fill tool %04d forced step lost data", i), strings.TrimSpace(how[:280]), trigger
}

func capacityCommand(i int) string {
	return fmt.Sprintf("fill-tool-%04d apply-step ./x --prune-the-old-branches", i)
}

// fillHookIndex writes lessons straight to the ledger until a rebuild leaves
// some out, and returns how many it wrote and that build.
func fillHookIndex(t *testing.T, homePath string, store *mem.Store) (int, hook.LessonIndexBuild) {
	t.Helper()
	for i := 0; i < 3000; i++ {
		why, how, trigger := capacityLessonArgs(i)
		if _, err := store.WriteLesson(mem.LessonInput{Why: why, How: how, Trigger: trigger, Actor: "agent:fill"}, servingTestNow); err != nil {
			t.Fatalf("fill lesson %d: %v", i, err)
		}
		if i%50 != 49 {
			continue
		}
		build, err := hook.RebuildLessonIndex(homePath, servingTestNow)
		if err != nil {
			t.Fatalf("a full index must still publish: %v", err)
		}
		if len(build.Unserved) > 0 {
			return i + 1, build
		}
	}
	t.Fatal("the index never filled")
	return 0, hook.LessonIndexBuild{}
}

// Past the size cap the index still publishes, the write that filled it
// exits 1 and names what is not served, and a retire still stops serving at
// once and lets the next lesson in.
func TestRetireStopsServingWhenTheIndexIsFull(t *testing.T) {
	homePath, store := servingTestHome(t)
	n, _ := fillHookIndex(t, homePath, store)

	why, how, trigger := capacityLessonArgs(n)
	code, out, errOut := runLesson(t, store, "--why", why, "--how", how, "--trigger", trigger, "--by", "agent:test", "--json")
	if code != 1 || !strings.Contains(errOut, "the hook index is full") || !strings.Contains(errOut, "retire or amend lessons") {
		t.Fatalf("the write that fills the index must exit 1 and say so: %d\n%s", code, errOut)
	}
	var receipt LessonReceipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Index.Indexed || receipt.Index.Rank != 1 || len(receipt.Index.Unserved) == 0 {
		t.Fatalf("the newest lesson ranks first; the receipt names the unserved ones: %+v", receipt.Index)
	}
	if got := served(t, "claude_code", capacityCommand(n)); !strings.Contains(got, receipt.Claim.ID) {
		t.Fatalf("newest lesson not served in a full index: %q", got)
	}
	nextIn := receipt.Index.Unserved[0]

	code, out, errOut = runLesson(t, store, "retire", receipt.Claim.ID, "--reason", "no longer holds", "--by", "agent:test", "--json")
	if code != 0 {
		t.Fatalf("retire in a full index: %d %s %s", code, out, errOut)
	}
	if got := served(t, "codex", capacityCommand(n)); got != "" {
		t.Fatalf("a retired lesson is still served in a full index: %q", got)
	}
	build, err := hook.RebuildLessonIndex(homePath, servingTestNow)
	if err != nil || !build.Serves(nextIn) || len(build.Unserved) != len(receipt.Index.Unserved)-1 {
		t.Fatalf("the room a retire frees goes to the next lesson in rank order (%s): unserved %d -> %d (%v)", nextIn, len(receipt.Index.Unserved), len(build.Unserved), err)
	}
}

// When the full rebuild fails for any reason, a retire still takes the
// lesson out of the published index; an amend takes the old version out and
// says the new one is not served yet.
func TestRetireStopsServingEvenWhenTheFullRebuildFails(t *testing.T) {
	homePath, store := servingTestHome(t)
	a := writeServedLesson(t, store, "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	code, out, errOut := runLesson(t, store, "--why", "the gadget flag was renamed", "--how", "Use gadget --apply.", "--by", "agent:test", "--trigger", `Bash:gadget\b`, "--json")
	if code != 0 {
		t.Fatalf("second lesson: %d %s %s", code, out, errOut)
	}
	var b LessonReceipt
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	sources := hook.LessonSourcesPath(homePath)
	if err := os.MkdirAll(filepath.Dir(sources), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sources, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut = runLesson(t, store, "retire", a.Claim.ID, "--reason", "no longer holds", "--by", "agent:test")
	if code != 0 || !strings.Contains(errOut, "removed from the published index") || !strings.Contains(errOut, "compound hook lessons-index") {
		t.Fatalf("retire with a failing rebuild: %d\n%s\n%s", code, out, errOut)
	}
	if got := served(t, "claude_code", "widget --force all"); got != "" {
		t.Fatalf("retired lesson still served after a failed rebuild: %q", got)
	}
	if got := served(t, "claude_code", "gadget run"); !strings.Contains(got, b.Claim.ID) {
		t.Fatalf("the other lesson must keep serving: %q", got)
	}

	code, _, errOut = runLesson(t, store, "amend", b.Claim.ID, "--how", "Use gadget --commit.", "--by", "agent:test")
	if code != 1 || !strings.Contains(errOut, "serving is stale") {
		t.Fatalf("amend with a failing rebuild must exit 1 and say serving is stale: %d %s", code, errOut)
	}
	if got := served(t, "claude_code", "gadget run"); got != "" {
		t.Fatalf("the old version of an amended lesson must stop being served: %q", got)
	}
}
