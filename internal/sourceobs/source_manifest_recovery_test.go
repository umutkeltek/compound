package sourceobs

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"compound/internal/home"
)

func TestSourceManifestConcurrentMaterializersShareOneExactTarget(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", time.Date(2026, 7, 14, 15, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "large-source.bin")
	body := bytes.Repeat([]byte("compound-custody\n"), 512*1024)
	if err := os.WriteFile(sourcePath, body, 0o640); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildOwnedSourceManifest(homePath, "concurrent_materialize", []string{sourcePath}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordSourceManifest(homePath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(t.TempDir(), "restored", "large-source.bin")
	const callers = 12
	start := make(chan struct{})
	reports := make(chan SourceManifestMaterializeReport, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			report, err := MaterializeSourceManifestItem(homePath, manifest.SourceFamily, manifest.Items[0].ID, targetPath)
			reports <- report
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(reports)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent materializer failed: %v", err)
		}
	}
	created := 0
	for report := range reports {
		if !report.AlreadyExists {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("materializers reporting target creation = %d, want exactly 1", created)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("materialized target differs: got=%d bytes want=%d", len(got), len(body))
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("materialized mode = %04o, want 0640", info.Mode().Perm())
	}
}
