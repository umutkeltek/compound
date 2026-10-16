package eventstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPostCompletionRepairSealSameTimestampRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), hookEventsStreamName)
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := postCompletionRepairSeal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := postCompletionRepairSeal(path)
	if err != nil {
		t.Fatal(err)
	}
	// Model a same-tick rewrite without altering the file's timestamps.
	after.ModTimeNano = before.ModTimeNano
	if before == after {
		t.Fatal("different compatibility stream bytes have identical seals")
	}
}

func TestLegacyStreamEqualCompletionTimestampFailsClosed(t *testing.T) {
	store := postCompletionRepairFixture(t)
	stream := filepath.Join(store.engine.eventsDir, hookEventsStreamName)
	// A hard link gives the stream exactly the completion timestamp without
	// sleeping or altering timestamps. This models the ambiguous same-tick case.
	marker := store.engine.completionPath()
	if err := os.Link(marker, stream); err != nil {
		t.Fatal(err)
	}
	if active, err := store.LegacyWriterActive(); err != nil || !active {
		t.Fatalf("new stream read as quiet: active=%v err=%v", active, err)
	}
}
