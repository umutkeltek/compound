package capgraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorSkillSourceValidatesRawSkillFiles(t *testing.T) {
	root := t.TempDir()
	writeSkillSource(t, root, "pricing", `---
name: pricing
description: "Pricing workflow"
---
# pricing
`)
	writeSkillSource(t, root, "signup", `---
name: signup
description: "Signup workflow"
---
# signup
`)

	report, err := DoctorSkillSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.SkillFileCount != 2 || report.ErrorCount != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDoctorSkillSourceRejectsMissingFrontmatterAndDuplicateNames(t *testing.T) {
	root := t.TempDir()
	writeSkillSource(t, root, "first", `---
name: duplicate
description: "First"
---
# first
`)
	writeSkillSource(t, root, "second", `---
name: duplicate
description: "Second"
---
# second
`)
	writeSkillSource(t, root, "broken", `# missing frontmatter
`)

	report, err := DoctorSkillSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "fail" {
		t.Fatalf("status = %s, want fail: %+v", report.Status, report)
	}
	if len(report.DuplicateNames) != 1 || report.DuplicateNames[0].Name != "duplicate" {
		t.Fatalf("duplicate names = %+v", report.DuplicateNames)
	}
	if report.ErrorCount < 3 {
		t.Fatalf("error count = %d, want at least 3: %+v", report.ErrorCount, report)
	}
}

func writeSkillSource(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
