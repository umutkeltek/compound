package scope

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSession(t *testing.T, dir string, text string) string {
	t.Helper()
	path := filepath.Join(dir, "session.jsonl")
	line := `{"message":{"role":"user","content":"` + text + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}

func TestResolveUsesContentIdentifiersNotCwd(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd-that-looks-like-other-project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	session := writeSession(t, root, "Decision: this belongs to systems/compound and compound://self.")
	packet, err := Resolve(session, SeedEntities())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if packet.Status != "primary_about" {
		t.Fatalf("expected primary_about, got %s (%#v)", packet.Status, packet.Assignments)
	}
	if packet.PrimaryScope != "compound" {
		t.Fatalf("expected compound from content, got %q", packet.PrimaryScope)
	}
}

func TestResolveDoesNotInventScopeFromCwd(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "systems", "compound")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	session := writeSession(t, root, "Today we discussed a generic idea with no registered identifier.")
	packet, err := Resolve(session, SeedEntities())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if packet.Status != "unresolved" {
		t.Fatalf("cwd should not create scope; got %s (%#v)", packet.Status, packet.Assignments)
	}
	if packet.PrimaryScope != "" {
		t.Fatalf("expected empty primary scope, got %q", packet.PrimaryScope)
	}
}
