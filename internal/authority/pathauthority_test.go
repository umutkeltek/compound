package authority

import (
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

func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
