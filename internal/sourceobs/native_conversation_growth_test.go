package sourceobs

import (
	"compound/internal/home"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeRawBodies(t *testing.T, hp, sid string) string {
	t.Helper()
	var out []string
	for _, item := range nativeManifest(t, hp).Items {
		parsed, err := url.Parse(item.SourceRef)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Query().Get("session") == sid && parsed.Query().Get("row") != "" {
			out = append(out, nativeBody(t, hp, item))
		}
	}
	return strings.Join(out, "\n")
}

func nativeArtifactTotals(t *testing.T, hp string) (int64, int) {
	t.Helper()
	var bytes int64
	count := 0
	for _, root := range []string{filepath.Join(hp, "artifacts", "sha256"), filepath.Join(hp, "private", "artifacts", "sha256")} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				bytes += info.Size()
				count++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return bytes, count
}

func TestNativeRowCustodyAvoidsWholeSessionAmplification(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+`
INSERT INTO sessions(id,source,started_at) VALUES('growth','cli',1);
INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(1,'growth','user','Cedar archive',1);
INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(2,'growth','tool',replace(hex(zeroblob(1048576)),'00','x'),2);
`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	captureNative(t, hp, root, "hermes", now)
	before, countBefore := nativeArtifactTotals(t, hp)
	unchanged := captureNative(t, hp, root, "hermes", now)
	afterUnchanged, countUnchanged := nativeArtifactTotals(t, hp)
	if before != afterUnchanged || countBefore != countUnchanged || unchanged.NativeBodyRows != 0 {
		t.Fatalf("unchanged refresh wrote/read bodies: bytes%d->%d count%d->%d report%+v", before, afterUnchanged, countBefore, countUnchanged, unchanged)
	}
	nativeSQL(t, path, "UPDATE sessions SET runtime_revision=2;")
	stateCapture := captureNative(t, hp, root, "hermes", now)
	if stateCapture.NativeRowsCaptured != 1 || stateCapture.Captured != 0 {
		t.Fatalf("state-only capture must custody one row and reuse projection: %+v", stateCapture)
	}
	afterState, _ := nativeArtifactTotals(t, hp)
	for i := 0; i < 10; i++ {
		nativeSQL(t, path, fmt.Sprintf("UPDATE sessions SET runtime_revision=%d; INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(%d,'growth','tool','small tool result %d',%d);", i+3, i+3, i, i+3))
		captureNative(t, hp, root, "hermes", now)
	}
	after, countAfter := nativeArtifactTotals(t, hp)
	stateBytes, growthBytes := afterState-before, after-afterState
	t.Logf("MEASURED native owned artifact growth: state_only_bytes=%d ten_small_appends_bytes=%d new_artifacts=%d initial_artifact_bytes=%d unchanged_body_rows=%d", stateBytes, growthBytes, countAfter-countBefore, before, unchanged.NativeBodyRows)
	if stateBytes <= 0 || growthBytes <= 0 || stateBytes > 16<<10 || growthBytes > 128<<10 || countAfter-countBefore != 21 {
		t.Fatalf("whole-session amplification: state_only_bytes=%d ten_small_appends_bytes=%d new_artifacts=%d initial_artifact_bytes=%d", stateBytes, growthBytes, countAfter-countBefore, before)
	}
}

func TestNativeV2MigrationRetainsOriginalSnapshotAndCustodiesRows(t *testing.T) {
	hp, root := t.TempDir(), nativeRoot(t)
	path := filepath.Join(root, "state.db")
	nativeSQL(t, path, hermesNativeSchema+`
INSERT INTO sessions(id,source,started_at,title) VALUES('migration','cli',1,'Archive plan');
INSERT INTO messages(id,session_id,role,content,timestamp) VALUES(1,'migration','user','Cedar archive',1);
INSERT INTO messages(id,session_id,role,content,timestamp,tool_calls) VALUES(2,'migration','tool','Hidden raw tool payload',2,X'0001ff');
`)
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	namespace := "native:hermes:" + filepath.ToSlash(path)
	ref := (&url.URL{Scheme: "sqlite", Path: filepath.ToSlash(path), RawQuery: url.Values{"session": {"migration"}, "tables": {"sessions,messages"}}.Encode()}).String()
	legacy := `{"type":"native_session","session_id":"migration"}
{"type":"source_row","table":"messages","row":{"id":2,"role":"tool","content":"Legacy hidden raw payload"}}
{"type":"message","timestamp":"1","message":{"role":"user","content":"Cedar archive"},"source":{"table":"messages","message_id":"1"}}
`
	store, err := home.NewArtifactStore(hp)
	if err != nil {
		t.Fatal(err)
	}
	bodyRef, err := store.Put([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	key := idFor("source_", "native_fixture", namespace, "session", "migration")
	old := SourceManifestItem{ID: idFor("srcitem_", "native_fixture", key, hashBytes([]byte(legacy))), SourceKey: key, SourceNamespace: namespace,
		SourceFingerprint: fmt.Sprintf("native-session-v2:legacy:legacy:%d", now.Unix()/86400),
		SourceKind:        "hermes_transcript_v1", SourceRef: ref, FileHash: hashBytes([]byte(legacy)), SnapshotTime: now.Format(time.RFC3339Nano),
		Privacy: "private", BodyPolicy: "owned_snapshot", BodyRef: bodyRef, BodyBytes: int64(len(legacy))}
	manifest := SourceManifest{SchemaVersion: SourceManifestSchemaVersion, ID: "srcman_native_fixture", SourceFamily: "native_fixture", AuthorityRef: SourceManifestAuthorityRef("native_fixture"), Lifecycle: defaultSourceManifestLifecycle(), Items: []SourceManifestItem{old}}
	if _, err := RecordSourceManifest(hp, manifest); err != nil {
		t.Fatal(err)
	}
	migrated := captureNative(t, hp, root, "hermes", now)
	if migrated.Captured != 1 || migrated.NativeRowsCaptured != 3 {
		t.Fatalf("v2 migration: %+v", migrated)
	}
	preserved, current := false, false
	for _, item := range nativeSessionItems(t, hp, "migration") {
		if item.ID == old.ID && item.BodyRef == old.BodyRef && item.FileHash == old.FileHash && item.SourceFingerprint == old.SourceFingerprint && item.VersionState == "superseded" {
			preserved = nativeBody(t, hp, item) == legacy
		}
		if item.VersionState != "superseded" && strings.HasPrefix(item.SourceFingerprint, "native-session-v3:") {
			body := nativeBody(t, hp, item)
			current = !strings.Contains(body, "source_row") && !strings.Contains(body, "Hidden raw") && strings.Contains(body, "Archive plan")
		}
	}
	if !preserved || !current {
		t.Fatal("v2 source was relabeled or semantic projection lost context")
	}
	raw := nativeRawBodies(t, hp, "migration")
	if !strings.Contains(raw, `"base64":"AAH/"`) || !strings.Contains(raw, "Hidden raw tool payload") {
		t.Fatal("original BLOB/native columns are not recoverable through owned row refs")
	}
}
