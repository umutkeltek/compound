package eventstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These tests prove the frozen-stream doctor's byte-size baseline (see
// stream-baseline.json / legacyStreamRegrew in migrate.go): a stream's mtime
// alone must no longer flip the doctor degraded once a baseline is recorded,
// growth/shrink of the recorded size must, a governed re-baseline accepts a
// legitimate size change as the new trusted baseline, and a home that
// completed migration before this change (no sidecar) keeps behaving exactly
// as it did before (mtime boundary).

// FALSIFIER F1 (size-robust): a completed home with a sidecar recording a
// nonzero stream size must NOT flag when that stream's mtime is bumped after
// the completion boundary but its byte size is unchanged — a mtime-only
// regression (e.g. a backup restore, a filesystem migration touching mtimes)
// no longer false-reds the doctor.
func TestLegacyStreamRegrewSizeRobustAgainstMTimeBump(t *testing.T) {
	for _, streamName := range []string{hookEventsStreamName, tracesStreamName} {
		t.Run(streamName, func(t *testing.T) {
			store := openMigrateStore(t)
			seedLoose(t, store, "rec_f1", "body-f1")

			streamPath := filepath.Join(store.home, "events", streamName)
			if err := os.WriteFile(streamPath, []byte(`{"id":"pre-existing"}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			r, err := store.Migrate(MigrateOptions{Mode: ModeApply})
			if err != nil || !r.Completion.Complete {
				t.Fatalf("apply: %v %v", err, r.Completion.Reasons)
			}

			baseline, ok, err := readStreamBaseline(store.engine.streamBaselinePath())
			if err != nil {
				t.Fatalf("read sidecar: %v", err)
			}
			if !ok {
				t.Fatal("expected writeCompletion to have written the initial sidecar")
			}
			if baseline.Reason != streamBaselineReasonCompletion {
				t.Fatalf("expected reason=%q, got %q", streamBaselineReasonCompletion, baseline.Reason)
			}
			size, ok := baseline.Streams[streamName]
			if !ok || size == 0 {
				t.Fatalf("expected a nonzero recorded size for %s, got streams=%v", streamName, baseline.Streams)
			}

			completionInfo, err := os.Stat(store.engine.completionPath())
			if err != nil {
				t.Fatal(err)
			}
			future := completionInfo.ModTime().Add(time.Hour)
			if err := os.Chtimes(streamPath, future, future); err != nil {
				t.Fatal(err)
			}

			active, err := store.LegacyWriterActive()
			if err != nil {
				t.Fatal(err)
			}
			if active {
				t.Fatalf("%s: mtime bump alone (size unchanged) must not flag once a sidecar records its size", streamName)
			}
			vr, err := store.Migrate(MigrateOptions{Mode: ModeVerify})
			if err != nil {
				t.Fatal(err)
			}
			if !vr.Completion.Complete || vr.Completion.LegacyWriterActive {
				t.Fatalf("verify should report ok despite the mtime bump: complete=%v legacy_writer_active=%v reasons=%v",
					vr.Completion.Complete, vr.Completion.LegacyWriterActive, vr.Completion.Reasons)
			}
		})
	}
}

// FALSIFIER F5 (explicit fallback): a home whose completion gate closed
// BEFORE this change has no stream-baseline.json sidecar at all. It must
// behave exactly as it did before this change: mtime-boundary comparison,
// nothing more.
func TestLegacyStreamRegrewFallsBackToMtimeWhenSidecarAbsent(t *testing.T) {
	store := openMigrateStore(t)
	seedLoose(t, store, "rec_f5", "body-f5")
	if r, err := store.Migrate(MigrateOptions{Mode: ModeApply}); err != nil || !r.Completion.Complete {
		t.Fatalf("apply: %v %v", err, r.Completion.Reasons)
	}

	// Simulate a home that completed before the sidecar existed at all.
	if err := os.Remove(store.engine.streamBaselinePath()); err != nil {
		t.Fatalf("remove sidecar to simulate a pre-change home: %v", err)
	}
	if _, ok, err := readStreamBaseline(store.engine.streamBaselinePath()); err != nil || ok {
		t.Fatalf("sidecar should be absent now: ok=%v err=%v", ok, err)
	}

	completionInfo, err := os.Stat(store.engine.completionPath())
	if err != nil {
		t.Fatal(err)
	}
	streamPath := filepath.Join(store.home, "events", hookEventsStreamName)
	if err := os.WriteFile(streamPath, []byte(`{"id":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	frozen := completionInfo.ModTime().Add(-time.Second)
	if err := os.Chtimes(streamPath, frozen, frozen); err != nil {
		t.Fatal(err)
	}
	if active, err := store.LegacyWriterActive(); err != nil || active {
		t.Fatalf("sidecar-absent + frozen-before-boundary must not flag: active=%v err=%v", active, err)
	}

	future := completionInfo.ModTime().Add(time.Hour)
	if err := os.Chtimes(streamPath, future, future); err != nil {
		t.Fatal(err)
	}
	active, err := store.LegacyWriterActive()
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("sidecar-absent + written-after-boundary must flag (unchanged mtime behavior)")
	}
}

// FALSIFIERS F2 (re-baseline path) + F3 (teeth after re-baseline), exercised
// directly against the eventstore primitive (Store.RebaselineStreams): a
// completed home whose stream legitimately grew reports degraded; re-baselining
// records the grown size as the new trusted baseline and verify goes back to
// ok; a further append after THAT flips degraded again — re-baselining is not
// a one-time escape hatch, it re-arms the same doctor at the new size.
func TestRebaselineStreamsAcceptsGrowthThenDetectsFurtherGrowth(t *testing.T) {
	store := openMigrateStore(t)
	seedLoose(t, store, "rec_f2", "body-f2")

	streamPath := filepath.Join(store.home, "events", hookEventsStreamName)
	if err := os.WriteFile(streamPath, []byte(`{"id":"initial"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := store.Migrate(MigrateOptions{Mode: ModeApply}); err != nil || !r.Completion.Complete {
		t.Fatalf("apply: %v %v", err, r.Completion.Reasons)
	}

	// Legitimate growth between completion and "now" (e.g. a slow operator
	// catching up a backlog before the freeze truly lands).
	f := openForAppend(t, streamPath)
	if _, err := f.WriteString(`{"id":"grew"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	active, err := store.LegacyWriterActive()
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("grown stream must flag degraded BEFORE re-baselining")
	}

	if _, err := store.RebaselineStreams(migrateNow.Add(time.Hour)); err != nil {
		t.Fatalf("rebaseline: %v", err)
	}
	baseline, ok, err := readStreamBaseline(store.engine.streamBaselinePath())
	if err != nil || !ok {
		t.Fatalf("expected a rebaselined sidecar: ok=%v err=%v", ok, err)
	}
	if baseline.Reason != streamBaselineReasonRebaseline {
		t.Fatalf("expected reason=%q after rebaseline, got %q", streamBaselineReasonRebaseline, baseline.Reason)
	}
	info, err := os.Stat(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Streams[hookEventsStreamName] != info.Size() {
		t.Fatalf("rebaselined size %d does not match on-disk size %d", baseline.Streams[hookEventsStreamName], info.Size())
	}

	active, err = store.LegacyWriterActive()
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("verify must report ok immediately after accepting the grown size as the new baseline")
	}

	// F3 teeth: a genuine post-rebaseline append flips degraded again — the
	// governed accept is not a permanent silence switch.
	f = openForAppend(t, streamPath)
	if _, err := f.WriteString(`{"id":"post-rebaseline"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	active, err = store.LegacyWriterActive()
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("an append AFTER the re-baseline must flag degraded again")
	}
}

// RebaselineStreams refuses (and touches nothing) when migration is not complete.
func TestRebaselineStreamsRefusesWhenMigrationIncomplete(t *testing.T) {
	store := openMigrateStore(t)
	if _, err := store.RebaselineStreams(migrateNow); err == nil {
		t.Fatal("expected an error re-baselining a home with no completed migration")
	}
	if _, err := os.Stat(store.engine.streamBaselinePath()); !os.IsNotExist(err) {
		t.Fatalf("no sidecar should have been written: stat err=%v", err)
	}
}

func openForAppend(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
