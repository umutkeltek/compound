package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
)

var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func clock() time.Time { return fixedNow }

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixtureRoot builds three synthetic skills (one with a multi-line description,
// one without a name, one nested in a grouping dir) plus entries that must
// never become skills. The path contains a space on purpose.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "skills root")
	writeFile(t, filepath.Join(root, "alpha-widget", "SKILL.md"),
		"---\nname: alpha-widget\ndescription: |\n  Fold synthetic widget telemetry\n  into a weekly report.\n---\n# Alpha\nBody text.\n")
	writeFile(t, filepath.Join(root, "beta-gadget", "SKILL.md"),
		"---\ndescription: Calibrate synthetic gadget sensors.\n---\n# Beta\n")
	writeFile(t, filepath.Join(root, "vendor-group", "gamma-relay", "SKILL.md"),
		"---\nname: gamma-relay\ndescription: Route synthetic relay messages between queues.\n---\n# Gamma\n")
	// Never skills: a file at the root, a hidden skill, a dir without SKILL.md,
	// a SKILL.md inside a skill's own body, a file and a too-deep skill in a group.
	writeFile(t, filepath.Join(root, "README.md"), "# not a skill\n")
	writeFile(t, filepath.Join(root, ".hidden-skill", "SKILL.md"), "---\nname: hidden\ndescription: Hidden.\n---\n")
	writeFile(t, filepath.Join(root, "notes", "notes.md"), "plain notes\n")
	writeFile(t, filepath.Join(root, "alpha-widget", "examples", "inner", "SKILL.md"), "---\nname: inner\ndescription: Inner body.\n---\n")
	writeFile(t, filepath.Join(root, "vendor-group", "README.md"), "# group\n")
	writeFile(t, filepath.Join(root, "vendor-group", "deeper", "more", "SKILL.md"), "---\nname: too-deep\ndescription: Too deep.\n---\n")
	return root
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, clock)
	return code, stdout.String(), stderr.String()
}

func mustGenerate(t *testing.T, root string, harnesses []string, now time.Time) capgraph.SkillManifest {
	t.Helper()
	m, _, err := generate(root, harnesses, now)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const golden = `{
  "schema_version": "compound.skill_manifest.v0.1",
  "id": "skill_manifest_root_<REV12>",
  "adapter_id": "compound.skillmanifest",
  "source_system": "skill-root",
  "observed_at": "2026-01-02T03:04:05Z",
  "source_ref": {
    "path": "<ROOT>",
    "kind": "skill_root",
    "owner": "capability-graph",
    "source_class": "evidence",
    "revision": "sha256:<REV>"
  },
  "skills": [
    {
      "id": "skill:alpha-widget",
      "kind": "skill",
      "name": "alpha-widget",
      "status": "active",
      "triggers": [
        "Fold synthetic widget telemetry\ninto a weekly report.",
        "alpha-widget"
      ],
      "compatible_harnesses": [
        "claude_code",
        "codex"
      ],
      "weight": 1,
      "skill_path": "<ROOT>/alpha-widget"
    },
    {
      "id": "skill:beta-gadget",
      "kind": "skill",
      "name": "beta-gadget",
      "status": "active",
      "triggers": [
        "Calibrate synthetic gadget sensors.",
        "beta-gadget"
      ],
      "compatible_harnesses": [
        "claude_code",
        "codex"
      ],
      "weight": 1,
      "skill_path": "<ROOT>/beta-gadget"
    },
    {
      "id": "skill:gamma-relay",
      "kind": "skill",
      "name": "gamma-relay",
      "status": "active",
      "triggers": [
        "Route synthetic relay messages between queues.",
        "gamma-relay"
      ],
      "compatible_harnesses": [
        "claude_code",
        "codex"
      ],
      "weight": 1,
      "skill_path": "<ROOT>/vendor-group/gamma-relay"
    }
  ],
  "evidence_refs": [
    "skill_root:sha256:<REV12>"
  ]
}
`

func TestGoldenManifest(t *testing.T) {
	root := fixtureRoot(t)
	// Repeated, differently spelled harnesses normalize to the graph spelling.
	code, out, errOut := runCLI(t, "--root", root, "--harness", "codex", "--harness", "Claude-Code", "--harness", "codex")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "skillmanifest: skills=3 skipped=0 ") || strings.Contains(errOut, "warning:") {
		t.Fatalf("unexpected stderr:\n%s", errOut)
	}
	var m capgraph.SkillManifest
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not a manifest: %v\n%s", err, out)
	}
	rev := strings.TrimPrefix(m.SourceRef.Revision, "sha256:")
	if len(rev) != 64 {
		t.Fatalf("revision %q is not a sha256", m.SourceRef.Revision)
	}
	got := strings.ReplaceAll(out, rev, "<REV>")
	got = strings.ReplaceAll(got, rev[:12], "<REV12>")
	got = strings.ReplaceAll(got, root, "<ROOT>")
	if got != golden {
		t.Fatalf("manifest differs from golden\n--- got\n%s\n--- want\n%s", got, golden)
	}
	// The importer reads the file through a strict YAML decoder (unknown fields
	// refused): the emitted JSON must load and validate through that path.
	p := filepath.Join(t.TempDir(), "manifest.json")
	writeFile(t, p, out)
	loaded, err := capgraph.LoadSkillManifestForPath(p, fixedNow)
	if err != nil {
		t.Fatalf("native loader refused generated manifest: %v", err)
	}
	if r := capgraph.ValidateSkillManifest(loaded); r.Status != "ok" {
		t.Fatalf("native validation: %+v", r.Checks)
	}
	if loaded.Skills[0].Triggers[0] != "Fold synthetic widget telemetry\ninto a weekly report." {
		t.Fatalf("multi-line description not kept verbatim through the loader: %q", loaded.Skills[0].Triggers[0])
	}
}

func TestDeterministicOutputAcrossRuns(t *testing.T) {
	root := fixtureRoot(t)
	c1, out1, err1 := runCLI(t, "--root", root, "--harness", "codex")
	c2, out2, err2 := runCLI(t, "--root", root, "--harness", "codex")
	if c1 != 0 || c2 != 0 {
		t.Fatalf("exit %d/%d\n%s\n%s", c1, c2, err1, err2)
	}
	if out1 != out2 || err1 != err2 {
		t.Fatal("two runs over the same tree at the same instant differ")
	}
}

func TestRevisionIgnoresLocationAndClockButTracksContent(t *testing.T) {
	a, b := fixtureRoot(t), fixtureRoot(t)
	first := mustGenerate(t, a, []string{"codex"}, fixedNow)
	second := mustGenerate(t, b, []string{"codex"}, fixedNow.Add(time.Hour))
	if first.SourceRef.Revision != second.SourceRef.Revision || first.ID != second.ID {
		t.Fatal("checkout location or clock changed the content revision")
	}
	if first.SourceRef.Path == second.SourceRef.Path || first.ObservedAt == second.ObservedAt {
		t.Fatal("source path and observation time were not resolved per run")
	}
	// A body-only edit (frontmatter unchanged) is still a content change.
	p := filepath.Join(b, "vendor-group", "gamma-relay", "SKILL.md")
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, string(body)+"\nChanged procedure.\n")
	if mustGenerate(t, b, []string{"codex"}, fixedNow).SourceRef.Revision == first.SourceRef.Revision {
		t.Fatal("SKILL.md body change did not change the revision")
	}
	if mustGenerate(t, a, []string{"codex", "claude_code"}, fixedNow).SourceRef.Revision == first.SourceRef.Revision {
		t.Fatal("harness change altered the manifest body but not its revision")
	}
}

func TestMalformedFrontmatterFailsWithoutManifest(t *testing.T) {
	cases := map[string]string{
		"no_frontmatter":         "# Title only\n",
		"leading_blank_line":     "\n---\nname: x\ndescription: y\n---\n",
		"unclosed":               "---\nname: x\ndescription: y\n",
		"invalid_yaml":           "---\nname: [unclosed\ndescription: y\n---\n",
		"not_a_mapping":          "---\njust a sentence\n---\n",
		"empty":                  "---\n---\n",
		"name_not_string":        "---\nname: 42\ndescription: y\n---\n",
		"missing_description":    "---\nname: x\n---\n",
		"blank_description":      "---\nname: x\ndescription: \"  \"\n---\n",
		"description_not_string": "---\nname: x\ndescription: [a, b]\n---\n",
		"duplicate_key":          "---\nname: x\nname: z\ndescription: y\n---\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			root := fixtureRoot(t)
			writeFile(t, filepath.Join(root, "vendor-group", "broken-skill", "SKILL.md"), content)
			code, out, errOut := runCLI(t, "--root", root)
			if code != 1 {
				t.Fatalf("exit %d, want 1\n%s", code, errOut)
			}
			if out != "" {
				t.Fatalf("partial manifest emitted:\n%s", out)
			}
			if !strings.Contains(errOut, "vendor-group/broken-skill/SKILL.md: malformed frontmatter") ||
				!strings.Contains(errOut, "no manifest emitted") ||
				!strings.Contains(errOut, "skillmanifest: skills=3 skipped=0 ignored=") ||
				!strings.Contains(errOut, " errors=1\n") {
				t.Fatalf("unclear failure report:\n%s", errOut)
			}
		})
	}
	// Every problem is reported in one pass, not only the first.
	root := fixtureRoot(t)
	writeFile(t, filepath.Join(root, "one-broken", "SKILL.md"), "# none\n")
	writeFile(t, filepath.Join(root, "two-broken", "SKILL.md"), "---\nname: x\n---\n")
	code, out, errOut := runCLI(t, "--root", root)
	if code != 1 || out != "" || !strings.Contains(errOut, "one-broken/SKILL.md") || !strings.Contains(errOut, "two-broken/SKILL.md") || !strings.Contains(errOut, " errors=2\n") {
		t.Fatalf("exit %d, stdout %q\n%s", code, out, errOut)
	}
}

func TestIdentityCollisionsFail(t *testing.T) {
	cases := map[string]func(root string){
		// Names, not directories, own identity: two spellings of one slug collide.
		"duplicate_id": func(root string) {
			writeFile(t, filepath.Join(root, "delta-one", "SKILL.md"), "---\nname: Delta Probe\ndescription: First.\n---\n")
			writeFile(t, filepath.Join(root, "vendor-group", "delta-two", "SKILL.md"), "---\nname: delta_probe\ndescription: Second.\n---\n")
		},
		"no_slug": func(root string) {
			writeFile(t, filepath.Join(root, "epsilon", "SKILL.md"), "---\nname: \"\u00e9\u00e9\"\ndescription: Only non-ASCII letters.\n---\n")
		},
	}
	for name, add := range cases {
		t.Run(name, func(t *testing.T) {
			root := fixtureRoot(t)
			add(root)
			code, out, errOut := runCLI(t, "--root", root)
			if code != 1 || out != "" {
				t.Fatalf("exit %d, stdout %q\n%s", code, out, errOut)
			}
			if !strings.Contains(errOut, "node id skill:delta-probe already taken") && !strings.Contains(errOut, "no ASCII letter or digit") {
				t.Fatalf("unclear identity failure:\n%s", errOut)
			}
		})
	}
}

func TestSymlinks(t *testing.T) {
	root := fixtureRoot(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, filepath.Join(outside, "aliased.md"), "---\nname: aliased\ndescription: Aliased body.\n---\n")
	writeFile(t, filepath.Join(outside, "group", "eta-node", "SKILL.md"), "---\nname: eta-node\ndescription: Index synthetic eta nodes.\n---\n")
	writeFile(t, filepath.Join(outside, "theta-tool", "SKILL.md"), "---\nname: theta-tool\ndescription: Sharpen synthetic theta tools.\n---\n")
	link := func(target, at string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Fatal(err)
		}
	}
	link(filepath.Join(outside, "aliased.md"), filepath.Join(root, "zeta-alias", "SKILL.md")) // symlinked SKILL.md: rejected
	link(filepath.Join(outside, "missing"), filepath.Join(root, "iota-gone"))                 // dangling entry: skipped
	link(filepath.Join(outside, "group"), filepath.Join(root, "linked-group"))                // symlinked grouping dir: followed
	link(filepath.Join(outside, "theta-tool"), filepath.Join(root, "theta-tool"))             // symlinked skill dir: followed
	rootLink := filepath.Join(t.TempDir(), "root-link")
	link(root, rootLink) // symlinked root: allowed

	code, out, errOut := runCLI(t, "--root", rootLink)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, want := range []string{
		"warning: zeta-alias/SKILL.md: symlinked SKILL.md rejected; skipped",
		"warning: iota-gone: dangling symlink; skipped",
		"skillmanifest: skills=5 skipped=2 ",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr missing %q:\n%s", want, errOut)
		}
	}
	var m capgraph.SkillManifest
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, s := range m.Skills {
		paths[s.ID] = s.SkillPath
	}
	if _, ok := paths["skill:aliased"]; ok {
		t.Fatal("symlinked SKILL.md was imported")
	}
	want := map[string]string{
		"skill:eta-node":    filepath.Join(rootLink, "linked-group", "eta-node"),
		"skill:theta-tool":  filepath.Join(rootLink, "theta-tool"),
		"skill:gamma-relay": filepath.Join(rootLink, "vendor-group", "gamma-relay"),
	}
	for id, p := range want {
		if paths[id] != p {
			t.Errorf("%s skill_path = %q, want %q (anchored at the root as given)", id, paths[id], p)
		}
	}
	if m.SourceRef.Path != rootLink || len(m.Skills) != 5 {
		t.Fatalf("source path %q, %d skills", m.SourceRef.Path, len(m.Skills))
	}
}

func TestUsageAndRootErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "x")
	empty := t.TempDir()
	writeFile(t, filepath.Join(empty, "notes", "a.md"), "x")
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"no_root", nil, 2, "usage:"},
		{"positional", []string{"--root", empty, "extra"}, 2, "usage:"},
		{"empty_harness", []string{"--root", empty, "--harness", " "}, 2, "empty harness name"},
		{"missing_root", []string{"--root", filepath.Join(empty, "absent")}, 1, "skills root:"},
		{"file_root", []string{"--root", file}, 1, "is not a directory"},
		{"no_skills", []string{"--root", empty}, 1, "no skills found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := runCLI(t, c.args...)
			if code != c.code || out != "" || !strings.Contains(errOut, c.msg) {
				t.Fatalf("exit %d (want %d), stdout %q, stderr:\n%s", code, c.code, out, errOut)
			}
		})
	}
}

// Ids must equal the ones the native generated-skill-manifest loader assigns to
// the same names; otherwise a refresh forks every existing node instead of
// updating it.
func TestIDsMatchNativeSkillIdentity(t *testing.T) {
	names := []string{"alpha-widget", "Beta Gadget", "gamma_relay.v2", "Delta--Probe 9"}
	root := filepath.Join(t.TempDir(), "root")
	type entry struct{ Name, Kind, Trigger, Home string }
	var native struct {
		Entries []entry `json:"entries"`
	}
	for i, n := range names {
		writeFile(t, filepath.Join(root, "s"+string(rune('a'+i)), "SKILL.md"), "---\nname: "+n+"\ndescription: Synthetic entry.\n---\n")
		native.Entries = append(native.Entries, entry{Name: n, Kind: "skill", Trigger: "synthetic", Home: "codex"})
	}
	b, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "native.json")
	writeFile(t, p, string(b))
	nm, err := capgraph.LoadOSSkillManifest(p, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, s := range nm.Skills {
		if s.Status != "retired" {
			want[s.ID] = true
		}
	}
	m := mustGenerate(t, root, nil, fixedNow)
	if len(m.Skills) != len(want) {
		t.Fatalf("got %d skills, native %d", len(m.Skills), len(want))
	}
	for _, s := range m.Skills {
		if !want[s.ID] {
			t.Errorf("id %s does not match native identity %v", s.ID, want)
		}
	}
}

// The real consumer is the native importer plus surfacing. Import into an
// isolated home and ask the graph for offers.
func TestNativeImportSurfacesGeneratedSkills(t *testing.T) {
	root := fixtureRoot(t)
	m := mustGenerate(t, root, []string{"codex"}, fixedNow)
	home := t.TempDir()
	rep, err := capgraph.ApplySkillManifest(home, m, fixedNow)
	if err != nil || rep.Status != "ok" {
		t.Fatalf("native import: %v %+v", err, rep.Checks)
	}
	store, err := capgraph.OpenReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]capgraph.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, id := range []string{"skill:alpha-widget", "skill:beta-gadget", "skill:gamma-relay"} {
		if n, ok := byID[id]; !ok || n.Kind != "skill" || n.Source == nil {
			t.Fatalf("graph lacks stamped skill node %s: %+v", id, n)
		}
	}
	offered := func(task, harness, id string) bool {
		t.Helper()
		offers, err := store.SurfaceForHarness(task, harness)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range offers {
			if o.Node.ID == id {
				return true
			}
		}
		return false
	}
	if !offered("route synthetic relay messages between queues", "codex", "skill:gamma-relay") {
		t.Fatal("nested skill not offered for its own description")
	}
	if !offered("fold the widget telemetry into a weekly report", "codex", "skill:alpha-widget") {
		t.Fatal("multi-line description did not trigger its skill")
	}
	if offered("route synthetic relay messages between queues", "claude_code", "skill:gamma-relay") {
		t.Fatal("skill surfaced for a harness it was not declared for")
	}
	// Known importer limit, recorded rather than hidden: the importer's source
	// doctor requires a frontmatter name, so a name-less skill imports but is
	// stamped unreachable and is not offered. If this changes, update the README.
	if beta := byID["skill:beta-gadget"]; beta.Source.Reachable || offered("calibrate synthetic gadget sensors", "codex", "skill:beta-gadget") {
		t.Fatal("name-less skill reachability changed in the importer; re-evaluate the documented limit")
	}
	if !byID["skill:alpha-widget"].Source.Reachable || !byID["skill:gamma-relay"].Source.Reachable {
		t.Fatal("importer doctor disagrees with the generator about a named skill")
	}
}
