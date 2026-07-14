package arch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCeremonyOverlaySyncReplacesStaleCopiesWithIdempotentRelativeSymlinks(t *testing.T) {
	tempRoot := t.TempDir()
	ceremonies := []string{"compound-execute", "compound-land", "compound-verify", "extract-approach"}
	for _, ceremony := range ceremonies {
		mustWriteTestFile(t, filepath.Join(tempRoot, "skills", ceremony, "SKILL.md"), fmt.Sprintf("tracked owner: %s\n", ceremony))
		for _, overlay := range []string{".agents", ".claude"} {
			mustWriteTestFile(t, filepath.Join(tempRoot, overlay, "skills", ceremony, "SKILL.md"), "stale copied body\n")
		}
	}

	script := filepath.Join(repoRoot, "scripts", "sync-ceremony-overlays.sh")
	for run := 1; run <= 2; run++ {
		cmd := exec.Command("sh", script, tempRoot)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sync run %d failed: %v\n%s", run, err, out)
		}
		for _, ceremony := range ceremonies {
			for _, overlay := range []string{".agents", ".claude"} {
				path := filepath.Join(tempRoot, overlay, "skills", ceremony, "SKILL.md")
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("run %d left %s as %s, want symlink", run, path, info.Mode())
				}
				wantTarget := filepath.ToSlash(filepath.Join("..", "..", "..", "skills", ceremony, "SKILL.md"))
				gotTarget, err := os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
				if gotTarget != wantTarget {
					t.Fatalf("run %d link target for %s = %q, want %q", run, path, gotTarget, wantTarget)
				}
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				wantBody := fmt.Sprintf("tracked owner: %s\n", ceremony)
				if string(body) != wantBody {
					t.Fatalf("run %d resolved body for %s = %q, want %q", run, path, body, wantBody)
				}
			}
		}
	}
}
