package authority

import (
	"os"
	"testing"

	"compound/internal/mem"
)

func TestImportPathAuthorityMergesMovedPathIntoSameEntity(t *testing.T) {
	existing := []mem.Entity{
		{
			Slug:        "project:example-app",
			Kind:        "project",
			Aliases:     []string{"ExampleApp"},
			Identifiers: []string{"bundle:com.example.exampleapp", "/workspace/old/example-app"},
			Visibility:  "operational",
		},
	}
	_, entity, facts, report, err := ImportPathAuthority("../../fixtures/authority/path-authority-example-app.yaml", existing)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.EntityRef != "project:example-app" {
		t.Fatalf("report = %+v", report)
	}
	for _, want := range []string{"https://example.invalid/example-app.git", "git_remote:https://example.invalid/example-app.git", "com.example.exampleapp", "/workspace/old/example-app", "/workspace/new/example-app", "host:macbook"} {
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

func TestImportOSPathRegistryNormalizesActiveAndStaleRoots(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	_, entities, facts, report, err := ImportOSPathRegistry("../../fixtures/authority/os-path-authority.yaml", nil)
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
