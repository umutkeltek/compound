package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/mem"
)

var ledgerTestNow = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func ledgerTestHome(t *testing.T) (string, *mem.Store) {
	t.Helper()
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", ledgerTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	return homePath, store
}

func writeLedgerLesson(t *testing.T, store *mem.Store, why, how, trigger string) mem.Claim {
	t.Helper()
	res, err := store.WriteLesson(mem.LessonInput{Why: why, How: how, Trigger: trigger, Actor: "agent:test"}, ledgerTestNow)
	if err != nil {
		t.Fatalf("write lesson: %v", err)
	}
	return res.Claim
}

func bashInput(command string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{"command": command})
	return encoded
}

// Every current lesson claim with a trigger is an index entry, with no skill
// file and no named source; a lesson without a trigger stays recall-only.
func TestLedgerLessonIsIndexedWithoutAnySkillFile(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	served := writeLedgerLesson(t, store, "a forced widget run wiped the cache", "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	recallOnly := writeLedgerLesson(t, store, "widget docs live in the wiki", "Read the widget page first.", "")

	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil || len(build.Problems) != 0 {
		t.Fatalf("rebuild: %v %v", err, build.Problems)
	}
	if !build.Serves(served.ID) || build.Serves(recallOnly.ID) || len(build.Index.Lessons) != 1 {
		t.Fatalf("index = %+v", build.Index.Lessons)
	}
	if build.Index.SourceLedgerSeq == 0 || build.Index.SourceMemoryRevision == "" {
		t.Fatalf("index must be bound to the ledger snapshot: %+v", build.Index)
	}
	got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("widget --force all"))
	if len(got) != 1 || got[0].ID != served.ID || got[0].Apply != served.Object {
		t.Fatalf("matching call = %+v", got)
	}
	if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("widget --dry-run")); len(got) != 0 {
		t.Fatalf("non-matching call served %+v", got)
	}
}

// For a lesson-backed skill line the ledger wins: a current lesson is served
// once with its ledger text (a stale hand edit of its line is ignored), a
// superseded one is excluded even though its line is still in the skill
// file, and an unbacked skill line is served as written. The repair door with
// no named sources reads the recorded ones and gives the same index.
func TestLedgerWinsOverSkillLinesForLessonBackedEntries(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	current := writeLedgerLesson(t, store, "a forced widget run wiped the cache", "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	loser, err := store.WriteLesson(mem.LessonInput{Why: "the gadget flag was renamed", How: "Use gadget --apply.", Trigger: `Bash:gadget\b`, Slot: "tool:gadget::lesson.flag", Actor: "agent:test"}, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	winner, err := store.WriteLesson(mem.LessonInput{Why: "the gadget flag was renamed", How: "Use gadget --commit.", Trigger: `Bash:gadget\b`, Slot: "tool:gadget::lesson.flag", Actor: "agent:test"}, ledgerTestNow)
	if err != nil || winner.Outcome != mem.LessonOutcomeContested {
		t.Fatalf("setup contest: %+v %v", winner, err)
	}
	if _, err := store.ResolveContestedLessonOnUse(winner.Claim.ID, "agent:test", "work_example", ledgerTestNow); err != nil {
		t.Fatal(err)
	}
	skills := writeSkillLines(t, current, loser.Claim)
	if _, err := WriteLessonSources(homePath, LessonSources{Skills: []string{skills}}); err != nil {
		t.Fatal(err)
	}

	build, err := RebuildLessonIndex(homePath, ledgerTestNow)
	if err != nil || len(build.Problems) != 0 {
		t.Fatalf("rebuild: %v %v", err, build.Problems)
	}
	ids := map[string]int{}
	for _, lesson := range build.Index.Lessons {
		ids[lesson.ID]++
		if lesson.ID == current.ID && lesson.Apply != current.Object {
			t.Fatalf("skill text overrode the ledger: %+v", lesson)
		}
	}
	if ids[current.ID] != 1 || ids[winner.Claim.ID] != 1 || ids["skill_port"] != 1 || ids[loser.Claim.ID] != 0 || len(build.Index.Lessons) != 3 {
		t.Fatalf("index ids = %v", ids)
	}
	if len(build.Index.ExcludedLessonIDs) != 1 || build.Index.ExcludedLessonIDs[0] != loser.Claim.ID {
		t.Fatalf("superseded lesson must be listed as excluded: %v", build.Index.ExcludedLessonIDs)
	}
	var stdout, stderr bytes.Buffer
	if code := RunLessonsIndexCommand(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("lessons-index: %d %s %s", code, &stdout, &stderr)
	}
	got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("gadget run"))
	if len(got) != 1 || got[0].ID != winner.Claim.ID {
		t.Fatalf("only the winner may be served: %+v", got)
	}
	if got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput("fixture-runner test flows/")); len(got) != 1 || got[0].ID != "skill_port" {
		t.Fatalf("unbacked skill line lost: %+v", got)
	}
}

// writeSkillLines writes one skill holding the lesson door's lines for each
// claim (the first one hand-edited to stale text) plus one unbacked trigger
// line, and returns the skills folder.
func writeSkillLines(t *testing.T, claims ...mem.Claim) string {
	t.Helper()
	skills := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(skills, "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{"# Example", "## Lessons"}
	for i, claim := range claims {
		for _, line := range mem.LessonSkillLines(claim) {
			if i == 0 {
				line = strings.ReplaceAll(line, claim.Object, "stale skill text")
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, `trigger: {"id":"skill_port","tools":["Bash"],"pattern":"\\bfixture-runner\\s+test\\b","apply":"Use the wrapper; a bare run hangs on an occupied port."}`)
	if err := os.WriteFile(filepath.Join(skills, "example", "SKILL.md"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skills
}

// A missing, half-written, foreign or oversized index (or something that is
// not a file at all) means no lessons: the hook answers at once, exits 0,
// and records why on its spool line; a broken index also says so on stderr.
func TestBrokenLessonIndexServesNothingAndSaysSo(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	writeLedgerLesson(t, store, "a forced widget run wiped the cache", "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	if _, err := RebuildLessonIndex(homePath, ledgerTestNow); err != nil {
		t.Fatal(err)
	}
	indexPath := LessonIndexPath(homePath)
	good, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		setup func(t *testing.T)
		state string
	}{
		{"missing", func(t *testing.T) { os.Remove(indexPath) }, LessonIndexMissing},
		{"half written", func(t *testing.T) { os.WriteFile(indexPath, good[:len(good)/2], 0o644) }, LessonIndexCorrupt},
		{"empty", func(t *testing.T) { os.WriteFile(indexPath, nil, 0o644) }, LessonIndexCorrupt},
		{"foreign schema", func(t *testing.T) {
			os.WriteFile(indexPath, bytes.Replace(good, []byte(LessonIndexSchemaVersion), []byte("compound.hook_lesson_index.v9"), 1), 0o644)
		}, LessonIndexUnknownSchema},
		{"oversized", func(t *testing.T) { os.WriteFile(indexPath, bytes.Repeat([]byte(" "), LessonIndexMaxBytes+1), 0o644) }, LessonIndexOversized},
		{"directory", func(t *testing.T) { os.MkdirAll(indexPath, 0o755) }, LessonIndexUnreadable},
		{"fifo", func(t *testing.T) {
			if err := syscall.Mkfifo(indexPath, 0o644); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
		}, LessonIndexUnreadable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.RemoveAll(indexPath)
			tc.setup(t)
			defer os.RemoveAll(indexPath)
			type result struct {
				code           int
				stdout, stderr string
			}
			done := make(chan result, 1)
			go func() {
				var stdout, stderr bytes.Buffer
				code := RunSpoolCommand("claude_code", "pre-tool-use", bytes.NewReader(spoolInput("widget --force all")), &stdout, &stderr, ledgerTestNow,
					func(lessons string) any { return map[string]string{"additionalContext": lessons} })
				done <- result{code, stdout.String(), stderr.String()}
			}()
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("hook hung on the index")
			}
			if got.code != 0 || strings.TrimSpace(got.stdout) != `{"additionalContext":""}` {
				t.Fatalf("hook must answer with no lessons: code=%d stdout=%s stderr=%s", got.code, got.stdout, got.stderr)
			}
			wantLog := tc.state != LessonIndexMissing
			if strings.Contains(got.stderr, "lesson index "+tc.state) != wantLog {
				t.Fatalf("stderr = %q (want log %v)", got.stderr, wantLog)
			}
			if _, state := MatchLessonsFileWithState(indexPath, "Bash", bashInput("widget --force all")); state != tc.state {
				t.Fatalf("state = %q, want %q", state, tc.state)
			}
		})
	}
	var states []string
	paths, _ := filepath.Glob(filepath.Join(SpoolDir(homePath), "*.ndjson"))
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		for _, raw := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var line SpoolLine
			if json.Unmarshal(raw, &line) == nil {
				states = append(states, line.LessonIndex)
			}
		}
	}
	if strings.Join(states, ",") != "missing,corrupt,corrupt,unknown_schema,oversized,unreadable,unreadable" {
		t.Fatalf("spool lines must record each state: %v", states)
	}
}

// A build waits for the home's build lock and reads the ledger only once it
// holds it, so a lesson written while another build runs is in the index the
// next build publishes. Without this, two writers could publish out of order
// and the last rename would drop the other's lesson.
func TestLessonIndexBuildReadsTheLedgerInsideTheBuildLock(t *testing.T) {
	homePath, store := ledgerTestHome(t)
	first := writeLedgerLesson(t, store, "a forced widget run wiped the cache", "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	lock, err := os.OpenFile(filepath.Join(homePath, filepath.FromSlash(lessonIndexLockRelPath)), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	type result struct {
		build LessonIndexBuild
		err   error
	}
	done := make(chan result, 1)
	go func() {
		build, err := RebuildLessonIndex(homePath, ledgerTestNow)
		done <- result{build, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("build published while another build held the lock: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
	second := writeLedgerLesson(t, store, "the gadget flag was renamed", "Use gadget --apply.", `Bash:gadget\b`)
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !got.build.Serves(first.ID) || !got.build.Serves(second.ID) {
			t.Fatalf("build after the lock must include the lesson written meanwhile: %+v %v", got.build.Index.Lessons, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("build never finished")
	}
}

// Several agents write lessons at the same moment: every lesson ends up
// served, and no reader ever sees a half-written index.
func TestConcurrentLessonWritersAreAllServed(t *testing.T) {
	homePath, _ := ledgerTestHome(t)
	const writers = 8
	stop := make(chan struct{})
	var readerErr error
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, state := MatchLessonsFileWithState(LessonIndexPath(homePath), "Bash", bashInput("tool-3 --force")); state != "" && state != LessonIndexMissing {
				readerErr = fmt.Errorf("reader saw index state %q", state)
				return
			}
		}
	}()
	ids := make([]string, writers)
	errs := make([]error, writers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store, err := mem.Open(homePath)
			if err != nil {
				errs[i] = err
				return
			}
			<-start
			res, err := store.WriteLesson(mem.LessonInput{
				Why: fmt.Sprintf("tool %d forced run lost data", i), How: fmt.Sprintf("Dry-run tool-%d first.", i),
				Trigger: fmt.Sprintf(`Bash:tool-%d\s+--force`, i), Actor: fmt.Sprintf("agent:writer-%d", i),
			}, ledgerTestNow)
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = res.Claim.ID
			_, errs[i] = RebuildLessonIndex(homePath, ledgerTestNow)
		}(i)
	}
	close(start)
	wg.Wait()
	close(stop)
	readerWG.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	if readerErr != nil {
		t.Fatal(readerErr)
	}
	data, err := os.ReadFile(LessonIndexPath(homePath))
	if err != nil {
		t.Fatal(err)
	}
	var index LessonIndex
	if err := json.Unmarshal(data, &index); err != nil || len(index.Lessons) != writers {
		t.Fatalf("final index has %d lessons, want %d (%v)", len(index.Lessons), writers, err)
	}
	for i, id := range ids {
		got := MatchLessonsFile(LessonIndexPath(homePath), "Bash", bashInput(fmt.Sprintf("tool-%d --force", i)))
		if len(got) != 1 || got[0].ID != id {
			t.Fatalf("writer %d lesson %s not served: %+v", i, id, got)
		}
	}
}
