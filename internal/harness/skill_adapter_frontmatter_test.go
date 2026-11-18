package harness

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type skillFrontmatterFields struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// strictFrontmatter parses the block between the opening and closing "---" lines the way a
// strict skill loader does: a YAML error or an unknown key fails the parse.
func strictFrontmatter(t *testing.T, rendered string) (skillFrontmatterFields, error) {
	t.Helper()
	if !strings.HasPrefix(rendered, "---\n") {
		t.Fatalf("rendered skill does not open with a frontmatter line: %q", firstLine(rendered))
	}
	rest := rendered[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatalf("rendered skill has no closing frontmatter line")
	}
	decoder := yaml.NewDecoder(strings.NewReader(rest[:end+1]))
	decoder.KnownFields(true)
	var fields skillFrontmatterFields
	err := decoder.Decode(&fields)
	return fields, err
}

func TestSkillFrontmatterSurvivesAStrictYAMLParser(t *testing.T) {
	tricky := "One door: the `agent-browser` CLI; say \"hello\" (open: read) back\\slash\ttab\x07bell # not a comment"
	fields, err := strictFrontmatter(t, skillFrontmatter("Browser", tricky))
	if err != nil {
		t.Fatalf("quoted frontmatter must parse strictly: %v", err)
	}
	if fields.Name != "Browser" || fields.Description != tricky {
		t.Fatalf("frontmatter did not round-trip:\n got name=%q description=%q\nwant description=%q", fields.Name, fields.Description, tricky)
	}

	// Falsifier: the pre-fix rendering wrote the description as a plain scalar, which a
	// strict parser refuses. If this ever parses, the test above proves nothing.
	printable := "One door: the `agent-browser` CLI; say \"hello\" (open: read)"
	if fields, err := strictFrontmatter(t, skillFrontmatter("Browser", printable)); err != nil || fields.Description != printable {
		t.Fatalf("quoted printable description = %q, %v", fields.Description, err)
	}
	plain := "---\nname: Browser\ndescription: " + printable + "\n---\n"
	if _, err := strictFrontmatter(t, plain); err == nil || !strings.Contains(err.Error(), "mapping values are not allowed") {
		t.Fatalf("plain-scalar description must fail a strict parse with the mapping-values error, got %v", err)
	}
}

func TestRenderedSkillAdaptersHaveStrictYAMLFrontmatter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	adapters := DefaultSkillAdapters()
	if len(adapters) != 3 {
		t.Fatalf("expected the three default adapters, got %d", len(adapters))
	}
	for _, adapter := range adapters {
		rendered, err := RenderSkillAdapter(fixtureBody(t), adapter, time.Unix(0, 0))
		if err != nil {
			t.Fatalf("%s: render: %v", adapter.ID, err)
		}
		fields, err := strictFrontmatter(t, string(rendered))
		if err != nil {
			t.Fatalf("%s: rendered frontmatter fails a strict YAML parse: %v", adapter.ID, err)
		}
		if fields.Name != "Browser" {
			t.Fatalf("%s: name = %q, want Browser", adapter.ID, fields.Name)
		}
		if !strings.Contains(fields.Description, ": ") || !strings.Contains(fields.Description, "`agent-browser`") {
			t.Fatalf("%s: description lost its text: %q", adapter.ID, fields.Description)
		}

		// The same rendering with the description line written the old way must fail, so
		// this test would have caught the break.
		quotedLine := "description: " + yamlDoubleQuoted(fields.Description) + "\n"
		if !strings.Contains(string(rendered), quotedLine) {
			t.Fatalf("%s: rendered description line is not the double-quoted form", adapter.ID)
		}
		old := strings.Replace(string(rendered), quotedLine, "description: "+fields.Description+"\n", 1)
		if _, err := strictFrontmatter(t, old); err == nil {
			t.Fatalf("%s: the old plain-scalar description parsed; the falsifier is broken", adapter.ID)
		}
	}
}
