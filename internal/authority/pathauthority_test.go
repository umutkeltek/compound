package authority

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/mem"
)

func TestImportPathAuthorityMergesMovedPathIntoSameEntity(t *testing.T) {
	existing := []mem.Entity{
		{
			Slug:        "project:sample-mobile-app",
			Kind:        "project",
			Aliases:     []string{"SampleMobileApp"},
			Identifiers: []string{"bundle:com.example.samplemobile", "/workspace/old/sample-mobile-app"},
			Visibility:  "operational",
		},
	}
	_, entity, facts, report, err := ImportPathAuthority("../../fixtures/authority/path-authority-sample-mobile-app.yaml", existing)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.EntityRef != "project:sample-mobile-app" {
		t.Fatalf("report = %+v", report)
	}
	for _, want := range []string{"https://example.invalid/sample-mobile-app.git", "git_remote:https://example.invalid/sample-mobile-app.git", "com.example.samplemobile", "/workspace/old/sample-mobile-app", "/workspace/new/sample-mobile-app", "host:macbook"} {
		if !hasString(entity.Identifiers, want) {
			t.Fatalf("identifiers missing %q: %v", want, entity.Identifiers)
		}
	}
	if len(facts) == 0 {
		t.Fatal("path authority should emit deterministic authority facts")
	}
}

func TestImportPathAuthorityRejectsBadStatus(t *testing.T) {
	pa := PathAuthority{
		SchemaVersion:       PathAuthoritySchemaVersion,
		EntityRef:           "project:x",
		EntityKind:          "project",
		AuthoritativeSource: "fixture",
		PathBindings:        []PathBinding{{Path: "/x", Status: "maybe"}},
		EvidenceRefs:        []string{"fixture"},
	}
	if err := validatePathAuthority(pa); err == nil {
		t.Fatal("bad path status should be rejected")
	}
}

func TestImportPathAuthorityRejectsRelativeActiveBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "path-authority.yaml")
	body := strings.Join([]string{
		"schema_version: " + PathAuthoritySchemaVersion,
		"entity_ref: source:umut-os",
		"entity_kind: repo",
		"authoritative_source: direct-path-authority-test",
		"path_bindings:",
		"  - path: relative/umut-os",
		"    status: active",
		"evidence_refs:",
		"  - test:relative-active",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, _, err := ImportPathAuthority(path, nil)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("direct path-authority import must reject relative active current_path, got %v", err)
	}
}

func TestImportOSPathRegistryNormalizesActiveAndStaleRoots(t *testing.T) {
	_, entities, facts, report, err := importOSPathRegistryWithAccountHome(
		"../../fixtures/authority/os-path-authority.yaml",
		nil,
		func() (string, error) { return "/home/tester", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.EntityCount != 5 {
		t.Fatalf("report = %+v", report)
	}
	if len(entities) != 5 {
		t.Fatalf("entities = %d, want 5", len(entities))
	}
	control := findEntity(entities, "source:control-spine")
	if control.Slug == "" {
		t.Fatal("missing control spine entity")
	}
	if !hasString(control.Identifiers, "/home/tester/Projects/control-spine/root") {
		t.Fatalf("control identifiers = %v", control.Identifiers)
	}
	if !hasCandidate(facts, "source:control-spine", "current_path", "/home/tester/Projects/control-spine/root") {
		t.Fatalf("active root did not create current_path claim: %+v", facts)
	}
	if hasCandidate(facts, "source:old-control-spine", "current_path", "/home/tester/Projects/old-control-spine") {
		t.Fatal("retired compatibility root must not create current_path")
	}
	if !hasCandidate(facts, "source:old-control-spine", "known_path_retired", "/home/tester/Projects/old-control-spine") {
		t.Fatalf("retired root did not create known_path_retired claim: %+v", facts)
	}
	if !hasCandidate(facts, "source:old-decisions", "known_path_stale", "/home/tester/Desktop/decisions") {
		t.Fatalf("missing_stale root did not create known_path_stale claim: %+v", facts)
	}
}

func TestImportOSPathRegistryUsesAccountHomeWhenProcessHOMEIsSpoofed(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Fatalf("current account: %v", err)
	}
	want, err := filepath.EvalSymlinks(account.HomeDir)
	if err != nil {
		t.Fatalf("canonical account home: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "path-authority.yaml")
	body := "version: 1\ncanonical_roots:\n  account_home_probe:\n    path: \"~\"\n    status: active\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, facts, _, err := ImportOSPathRegistry(path, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !hasCandidate(facts, "source:account-home-probe", "current_path", want) {
		t.Fatalf("HOME spoof changed imported account path; want canonical account home %q, facts=%+v", want, facts)
	}
}

func TestImportOSPathRegistryRejectsRelativeActiveRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "path-authority.yaml")
	body := "version: 1\ncanonical_roots:\n  relative_root:\n    path: relative/root\n    status: active\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, _, err := importOSPathRegistryWithAccountHome(path, nil, func() (string, error) {
		return t.TempDir(), nil
	})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative active root must fail closed, got %v", err)
	}
}

func TestImportOSPathRegistryCanonicalizesExistingSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real-root")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(root, "linked-root")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "path-authority.yaml")
	body := "version: 1\ncanonical_roots:\n  linked_root:\n    path: " + linkRoot + "\n    status: active\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, facts, _, err := importOSPathRegistryWithAccountHome(path, nil, func() (string, error) {
		return root, nil
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !hasCandidate(facts, "source:linked-root", "current_path", wantRoot) {
		t.Fatalf("existing symlink root was not canonicalized to %q: %+v", wantRoot, facts)
	}
}

func TestImportOSPathRegistryRejectsMissingInstalledHarnessHome(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "path-authority.yaml")
	missing := filepath.Join(root, "missing-harness-home")
	body := "version: 1\ncanonical_roots:\n  installed_harness_home:\n    path: " + missing + "\n    status: active\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, _, err := importOSPathRegistryWithAccountHome(path, nil, func() (string, error) {
		return root, nil
	})
	if err == nil || !strings.Contains(err.Error(), "installed_harness_home") || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("missing installed_harness_home must fail closed, got %v", err)
	}
}

func TestImportOSPathRegistryRejectsUnknownStatus(t *testing.T) {
	path := t.TempDir() + "/path-authority.yaml"
	data := []byte("version: 1\ncanonical_roots:\n  x:\n    path: ~/x\n    status: mystery\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ImportOSPathRegistry(path, nil); err == nil {
		t.Fatal("unknown OS path status should be rejected")
	}
}

func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func findEntity(values []mem.Entity, slug string) mem.Entity {
	for _, v := range values {
		if v.Slug == slug {
			return v
		}
	}
	return mem.Entity{}
}

func hasCandidate(values []mem.Candidate, entity, predicate, object string) bool {
	for _, v := range values {
		if v.Entity == entity && v.Predicate == predicate && v.Object == object {
			return true
		}
	}
	return false
}
