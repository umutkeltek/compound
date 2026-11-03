// Command skillmanifest emits the native SkillManifest for a directory of skills.
// It reads local files only and writes nothing but stdout/stderr; the native
// importer (`compound capability import-skills`) owns governance and every write.
//
// Layout (the importer's skill-source layouts, with the container generalized):
//
//	<root>/<skill>/SKILL.md          a skill
//	<root>/<group>/<skill>/SKILL.md  a skill inside a grouping directory, i.e. a
//	                                 top-level directory without its own SKILL.md
//
// A skill's own subtree is its body and is never searched for more skills.
// Hidden entries are ignored. Symlinked directories (root, group or skill) are
// followed, as the importer's reachability doctor follows them; a symlinked
// SKILL.md is skipped with a warning. Any malformed frontmatter or identity
// collision fails the whole run: no partial manifest is ever written.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/capgraph"
)

const (
	adapterID    = "compound.skillmanifest"
	sourceSystem = "skill-root"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now))
}

type harnessFlag []string

func (h *harnessFlag) String() string { return strings.Join(*h, ",") }

func (h *harnessFlag) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("empty harness name")
	}
	*h = append(*h, v)
	return nil
}

func run(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	fset := flag.NewFlagSet("skillmanifest", flag.ContinueOnError)
	fset.SetOutput(stderr)
	root := fset.String("root", "", "skills directory to read (required)")
	var harnesses harnessFlag
	fset.Var(&harnesses, "harness", "compatible harness for every emitted skill (repeatable)")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() != 0 || strings.TrimSpace(*root) == "" {
		fmt.Fprintln(stderr, "usage: skillmanifest --root <skills-dir> [--harness <name> ...]")
		return 2
	}
	m, rep, err := generate(*root, harnesses, now())
	for _, w := range rep.warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	for _, p := range rep.problems {
		fmt.Fprintln(stderr, "error:", p)
	}
	summary := func() {
		fmt.Fprintf(stderr, "skillmanifest: skills=%d skipped=%d ignored=%d errors=%d\n", rep.found, rep.skipped, rep.ignored, len(rep.problems))
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		summary()
		return 1
	}
	// Encode fully before writing: a failure never leaves a partial manifest.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if _, err := stdout.Write(buf.Bytes()); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	summary()
	return 0
}

type report struct {
	found    int // SKILL.md files parsed as skills
	warnings []string
	problems []string
	skipped  int
	ignored  int
}

type found struct {
	rel         string // slash-separated skill directory, relative to root
	dir         string // root-anchored absolute skill directory
	body        []byte // SKILL.md bytes
	name        string
	description string
}

type scanner struct {
	root   string
	rep    *report
	skills []found
}

func generate(root string, harnesses []string, now time.Time) (capgraph.SkillManifest, report, error) {
	var m capgraph.SkillManifest
	var rep report
	abs, err := filepath.Abs(root)
	if err != nil {
		return m, rep, err
	}
	info, err := os.Stat(abs) // follows a symlinked root
	if err != nil {
		return m, rep, fmt.Errorf("skills root: %w", err)
	}
	if !info.IsDir() {
		return m, rep, fmt.Errorf("skills root %s is not a directory", abs)
	}
	s := &scanner{root: abs, rep: &rep}
	s.scanDir("", 0)
	rep.found = len(s.skills)

	byID := map[string]string{}
	entries := make([]capgraph.SkillManifestEntry, 0, len(s.skills))
	hs := normalizeHarnesses(harnesses)
	for _, f := range s.skills {
		slug := faithfulSlug(f.name)
		if slug == "" {
			rep.problems = append(rep.problems, fmt.Sprintf("%s/SKILL.md: name %q has no ASCII letter or digit for a stable node id", f.rel, f.name))
			continue
		}
		id := "skill:" + slug
		if prev, dup := byID[id]; dup {
			rep.problems = append(rep.problems, fmt.Sprintf("%s/SKILL.md: node id %s already taken by %s/SKILL.md", f.rel, id, prev))
			continue
		}
		byID[id] = f.rel
		entries = append(entries, capgraph.SkillManifestEntry{
			ID:     id,
			Kind:   "skill",
			Name:   f.name,
			Status: "active",
			// The description is the trigger text, verbatim. The name is kept as
			// a second trigger so a re-imported node still matches its own name.
			Triggers:            []string{f.description, f.name},
			CompatibleHarnesses: hs,
			Weight:              1,
			SkillPath:           f.dir,
		})
	}
	if len(rep.problems) > 0 {
		return m, rep, fmt.Errorf("%d problem(s) under %s; no manifest emitted", len(rep.problems), abs)
	}
	if len(entries) == 0 {
		return m, rep, fmt.Errorf("no skills found under %s", abs)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	revision := contentRevision(s.skills, hs)
	m = capgraph.SkillManifest{
		SchemaVersion: capgraph.SkillManifestSchemaVersion,
		ID:            "skill_manifest_root_" + revision[7:19],
		AdapterID:     adapterID,
		SourceSystem:  sourceSystem,
		ObservedAt:    now.UTC().Format(time.RFC3339),
		SourceRef: capgraph.SkillSourceRef{
			Path:        abs,
			Kind:        "skill_root",
			Owner:       "capability-graph",
			SourceClass: "evidence",
			Revision:    revision,
		},
		Skills:       entries,
		EvidenceRefs: []string{"skill_root:" + revision[:19]},
	}
	if r := capgraph.ValidateSkillManifest(m); r.Status != "ok" {
		var failed []string
		for _, c := range r.Checks {
			if c.Status != "ok" {
				failed = append(failed, c.Name+"="+c.Detail)
			}
		}
		return capgraph.SkillManifest{}, rep, fmt.Errorf("native manifest validation failed: %s", strings.Join(failed, "; "))
	}
	return m, rep, nil
}

// scanDir lists one directory. depth 0 is the root; depth 1 is a grouping dir.
func (s *scanner) scanDir(rel string, depth int) {
	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		s.rep.problems = append(s.rep.problems, fmt.Sprintf("%s: %v", displayRel(rel), err))
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			s.rep.ignored++
			continue
		}
		s.inspect(path.Join(rel, e.Name()), depth)
	}
}

func (s *scanner) inspect(rel string, depth int) {
	dir := filepath.Join(s.root, filepath.FromSlash(rel))
	li, err := os.Lstat(dir)
	if err != nil {
		s.rep.problems = append(s.rep.problems, fmt.Sprintf("%s: %v", rel, err))
		return
	}
	switch {
	case li.Mode()&os.ModeSymlink != 0:
		ti, err := os.Stat(dir)
		if err != nil {
			s.skip("%s: dangling symlink; skipped", rel)
			return
		}
		if !ti.IsDir() {
			s.rep.ignored++
			return
		}
	case !li.IsDir():
		s.rep.ignored++
		return
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	fi, err := os.Lstat(skillFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if depth == 0 {
			s.scanDir(rel, 1) // grouping directory: its children may be skills
		} else {
			s.rep.ignored++
		}
		return
	case err != nil:
		s.rep.problems = append(s.rep.problems, fmt.Sprintf("%s/SKILL.md: %v", rel, err))
		return
	case fi.Mode()&os.ModeSymlink != 0:
		s.skip("%s/SKILL.md: symlinked SKILL.md rejected; skipped", rel)
		return
	case !fi.Mode().IsRegular():
		s.skip("%s/SKILL.md: not a regular file; skipped", rel)
		return
	}
	body, err := os.ReadFile(skillFile)
	if err != nil {
		s.rep.problems = append(s.rep.problems, fmt.Sprintf("%s/SKILL.md: %v", rel, err))
		return
	}
	name, description, err := parseFrontmatter(body)
	if err != nil {
		s.rep.problems = append(s.rep.problems, fmt.Sprintf("%s/SKILL.md: malformed frontmatter: %v", rel, err))
		return
	}
	if name == "" {
		name = path.Base(rel)
	}
	s.skills = append(s.skills, found{rel: rel, dir: dir, body: body, name: name, description: description})
}

func (s *scanner) skip(format string, args ...any) {
	s.rep.skipped++
	s.rep.warnings = append(s.rep.warnings, fmt.Sprintf(format, args...))
}

func displayRel(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// parseFrontmatter follows the importer's skill-source rules: the first line is
// exactly ---, the block closes with a --- line, and it holds a YAML mapping.
// name is optional (the caller falls back to the directory name); description
// is required and kept verbatim apart from surrounding whitespace.
func parseFrontmatter(body []byte) (name, description string, err error) {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	if lines[0] != "---" {
		return "", "", errors.New("must start with --- on the first line")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", "", errors.New("must close with a --- line")
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fields); err != nil {
		return "", "", fmt.Errorf("YAML: %s", strings.ReplaceAll(err.Error(), "\n", " "))
	}
	if fields == nil {
		return "", "", errors.New("empty; want a YAML mapping")
	}
	if v, ok := fields["name"]; ok && v != nil {
		text, isText := v.(string)
		if !isText {
			return "", "", fmt.Errorf("name must be a string, got %T", v)
		}
		name = strings.TrimSpace(text)
	}
	v, ok := fields["description"]
	if !ok || v == nil {
		return "", "", errors.New("missing description")
	}
	text, isText := v.(string)
	if !isText {
		return "", "", fmt.Errorf("description must be a string, got %T", v)
	}
	description = strings.TrimSpace(text)
	if description == "" {
		return "", "", errors.New("empty description")
	}
	return name, description, nil
}

// faithfulSlug matches the importer's identity slug for skill names: lowercase,
// every run of characters outside [a-z0-9] is one separator, all tokens kept.
// Matching it means a re-import updates an existing node instead of forking it.
func faithfulSlug(s string) string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	return strings.Join(parts, "-")
}

// normalizeHarnesses writes harness names in the graph's one spelling
// (lowercase, underscores, no harness: prefix), deduplicated and sorted.
func normalizeHarnesses(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range in {
		h = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h)), "harness:")
		h = strings.ReplaceAll(h, "-", "_")
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// contentRevision hashes what the manifest is made from: each emitted skill's
// root-relative directory and SKILL.md bytes, plus the harness list. It is
// independent of the root's location and of the clock.
func contentRevision(skills []found, harnesses []string) string {
	sorted := append([]found(nil), skills...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].rel < sorted[j].rel })
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "skill:%d:%s:%d:", len(f.rel), f.rel, len(f.body))
		h.Write(f.body)
	}
	for _, name := range harnesses {
		fmt.Fprintf(h, "harness:%d:%s:", len(name), name)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
