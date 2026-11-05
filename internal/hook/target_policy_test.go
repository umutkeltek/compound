package hook

import (
	"path/filepath"
	"testing"
)

func TestLocalTargetPathSeparatesFilesystemAndURINamespaces(t *testing.T) {
	t.Parallel()
	cwd := "/synthetic/project"
	tests := []struct {
		name  string
		raw   string
		path  string
		local bool
	}{
		{name: "relative path", raw: "src/file.go", path: filepath.Join(cwd, "src/file.go"), local: true},
		{name: "absolute path", raw: "/synthetic/other/file.go", path: "/synthetic/other/file.go", local: true},
		{name: "local file URI", raw: "file:///synthetic/project/src/file.go", path: "/synthetic/project/src/file.go", local: true},
		{name: "localhost file URI", raw: "file://localhost/synthetic/project/src/file.go", path: "/synthetic/project/src/file.go", local: true},
		{name: "remote file URI", raw: "file://files.example.invalid/synthetic/project/src/file.go"},
		{name: "file URI query", raw: "file:///synthetic/project/src/file.go?version=1"},
		{name: "ssh URI", raw: "ssh://example.invalid/srv/stacks/search"},
		{name: "https URI", raw: "https://example.invalid/file"},
		{name: "unknown URI", raw: "future://authority/object"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, local := LocalTargetPath(tc.raw, cwd)
			if local != tc.local || got != tc.path {
				t.Fatalf("LocalTargetPath(%q)=(%q,%v), want (%q,%v)", tc.raw, got, local, tc.path, tc.local)
			}
		})
	}
}
