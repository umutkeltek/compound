package capgraph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const SkillSourceDoctorSchemaVersion = "compound.skill_source_doctor.v0.1"

type SkillSourceDoctorReport struct {
	SchemaVersion  string                     `json:"schema_version"`
	Root           string                     `json:"root"`
	Status         string                     `json:"status"`
	SkillFileCount int                        `json:"skill_file_count"`
	ErrorCount     int                        `json:"error_count"`
	Checks         []SkillSourceDoctorCheck   `json:"checks,omitempty"`
	Files          []SkillSourceFileReport    `json:"files,omitempty"`
	DuplicateNames []SkillSourceDuplicateName `json:"duplicate_names,omitempty"`
	// Dangling lists the entries that were reported as dangling_symlink.
	// Distinct from the Files list because dangling entries have no SKILL.md
	// and therefore no frontmatter name to dedupe on; the doctor surfaces
	// them here so the operator can see what is unreachable on disk WITHOUT
	// having to scan Files for reason="dangling_symlink" themselves.
	Dangling []string `json:"dangling,omitempty"`
}

type SkillSourceDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type SkillSourceFileReport struct {
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	// Reachable is true iff the skill's dir AND its SKILL.md both resolve on
	// disk (Lstat+Stat dance: a real dir, a non-dangling symlink, or a
	// symlink whose target resolves and is the expected kind). False means
	// the skill CANNOT fire from disk as of this report, regardless of any
	// ConnectionSurface facts the graph carries. For ComputeReachability,
	// `Source == nil || Source.Reachable` is what counts.
	Reachable bool `json:"reachable"`
	// Reason names WHICH step failed: ok | dangling_symlink | missing |
	// unreadable | no_frontmatter. ok is reserved for entries whose content
	// AND reachability are both clean. The doctor's CLI renderer AND the
	// ApplySkillManifest stamp both consume Reason -- the CLI lists Dangling
	// from Files+Reason; the import path stamps Node.Source from Reachable+Reason.
	Reason string   `json:"reason,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

type SkillSourceDuplicateName struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

func DoctorSkillSource(root string) (SkillSourceDoctorReport, error) {
	root = filepath.Clean(root)
	entries, err := skillSourceEntries(root)
	if err != nil {
		return SkillSourceDoctorReport{}, err
	}
	report := SkillSourceDoctorReport{
		SchemaVersion: SkillSourceDoctorSchemaVersion,
		Root:          root,
		Status:        "ok",
	}
	addCheck := func(name string, ok bool, detail string) {
		status := "ok"
		if !ok {
			status = "fail"
			report.Status = "fail"
		}
		report.Checks = append(report.Checks, SkillSourceDoctorCheck{Name: name, Status: status, Detail: detail})
	}
	addCheck("skill_files", len(entries) > 0, fmt.Sprintf("%d", len(entries)))
	report.SkillFileCount = len(entries)

	namePaths := map[string][]int{}
	for _, absDir := range entries {
		fileReport := inspectSkillSourceEntry(root, absDir)
		if fileReport.Status != "ok" {
			report.Status = "fail"
			report.ErrorCount += len(fileReport.Errors)
		}
		if fileReport.Reachable == false && fileReport.Reason == "dangling_symlink" {
			report.Dangling = append(report.Dangling, fileReport.Path)
		}
		if strings.TrimSpace(fileReport.Name) != "" {
			namePaths[fileReport.Name] = append(namePaths[fileReport.Name], len(report.Files))
		}
		report.Files = append(report.Files, fileReport)
	}
	sort.Strings(report.Dangling)

	for name, indexes := range namePaths {
		if len(indexes) < 2 {
			continue
		}
		dup := SkillSourceDuplicateName{Name: name}
		for _, idx := range indexes {
			path := report.Files[idx].Path
			report.Files[idx].Status = "fail"
			report.Files[idx].Errors = append(report.Files[idx].Errors, "duplicate skill name")
			dup.Paths = append(dup.Paths, path)
			report.ErrorCount++
		}
		sort.Strings(dup.Paths)
		report.DuplicateNames = append(report.DuplicateNames, dup)
		report.Status = "fail"
	}
	sort.Slice(report.DuplicateNames, func(i, j int) bool {
		return report.DuplicateNames[i].Name < report.DuplicateNames[j].Name
	})
	addCheck("duplicate_names", len(report.DuplicateNames) == 0, fmt.Sprintf("%d", len(report.DuplicateNames)))
	addCheck("dangling_entries", len(report.Dangling) == 0, fmt.Sprintf("%d", len(report.Dangling)))
	return report, nil
}

// DoctorSkillSourceEntry is the per-entry surface the import path uses
// (ApplySkillManifest stamps Node.Source from its result). It walks the
// Lstat+Stat dance on `root/relDir` and `root/relDir/SKILL.md`, and never
// treats a dangling symlink as "file absent" — a dangling link is its own
// reason (Reason="dangling_symlink"). Existing callers (DoctorSkillSource)
// iterate entries and call this; the export is for ApplySkillManifest.
func DoctorSkillSourceEntry(root, relDir string) SkillSourceFileReport {
	absDir := filepath.Join(root, relDir)
	return inspectSkillSourceEntry(root, absDir)
}

// skillSourceEntries lists every candidate skill entry under root (and
// root/skills). Unlike the old root/*/SKILL.md glob it also surfaces entries
// whose SKILL.md is gone — including dangling symlinks — so the doctor can
// report them as `dangling_symlink` instead of silently dropping them. The
// per-entry inspector handles the Lstat+Stat dance itself; this function
// only enumerates dirs (real or symlink).
func skillSourceEntries(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if filepath.Base(root) != "SKILL.md" {
			return nil, fmt.Errorf("skill source file must be named SKILL.md: %s", root)
		}
		return []string{filepath.Dir(root)}, nil
	}
	seen := map[string]bool{}
	var dirs []string
	for _, parent := range []string{root, filepath.Join(root, "skills")} {
		dh, err := os.ReadDir(parent)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if parent == root {
				return nil, err
			}
			continue
		}
		for _, de := range dh {
			abs := filepath.Join(parent, de.Name())
			if seen[abs] {
				continue
			}
			// ALL entries are candidates; the inspector below decides
			// missing vs dangling via Lstat+Stat. We intentionally do not
			// skip entries that look like non-dir entries -- a symlink to a
			// nonexistent target must NOT be silently dropped here. The
			// previous impl's `root/*/SKILL.md` glob was the source of the
			// "phantom reachability" bug F3 pins.
			seen[abs] = true
			dirs = append(dirs, abs)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// inspectSkillSourceEntry is the canonical reachability inspector. It walks
// the Lstat+Stat dance on the dir AND its SKILL.md, parses the frontmatter
// when reachable, and returns a SkillSourceFileReport whose Reason names
// exactly what failed (if anything). It is the only place reachability is
// derived for skill nodes; DoctorSkillSource, DoctorSkillSourceEntry, and
// ApplySkillManifest all share it.
func inspectSkillSourceEntry(root, absDir string) SkillSourceFileReport {
	rel := absDir
	if r, err := filepath.Rel(root, absDir); err == nil {
		rel = r
	}
	report := SkillSourceFileReport{Path: rel, Status: "fail", Reachable: false}
	absFile := filepath.Join(absDir, "SKILL.md")

	// Lstat the dir: distinguishes missing (lerr != nil) from a dangling
	// symlink (lstat succeeds, Stat fails).
	switch linfo, lerr := os.Lstat(absDir); {
	case lerr != nil:
		report.Reason = "missing"
		return report
	case linfo.Mode()&os.ModeSymlink != 0:
		sinfo, serr := os.Stat(absDir)
		if serr != nil {
			report.Reason = "dangling_symlink"
			return report
		}
		if !sinfo.IsDir() {
			report.Reason = "missing"
			return report
		}
	case !linfo.IsDir():
		report.Reason = "missing"
		return report
	}

	// Lstat the SKILL.md: the same dance applies. A dir can be present and
	// look reachable, but a SKILL.md whose link is broken means the skill
	// still cannot fire from disk -- reason=dangling_symlink, not the
	// generic "unreadable" the old path produced.
	switch flinfo, ferr := os.Lstat(absFile); {
	case ferr != nil:
		report.Reason = "missing"
		return report
	case flinfo.Mode()&os.ModeSymlink != 0:
		if _, fserr := os.Stat(absFile); fserr != nil {
			report.Reason = "dangling_symlink"
			return report
		}
	}

	data, err := os.ReadFile(absFile)
	if err != nil {
		report.Reason = "unreadable"
		return report
	}
	frontmatter, err := skillSourceFrontmatter(data)
	if err != nil {
		report.Reason = "no_frontmatter"
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil || fields == nil {
		report.Reason = "no_frontmatter"
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("YAML: %v", err))
		} else {
			report.Errors = append(report.Errors, "front matter YAML mapping")
		}
		return report
	}
	report.Name = skillSourceStringField(fields, "name")
	report.Description = skillSourceStringField(fields, "description")
	if report.Name == "" || report.Description == "" {
		report.Reason = "no_frontmatter"
		if report.Name == "" {
			report.Errors = append(report.Errors, "missing non-empty string field: name")
		}
		if report.Description == "" {
			report.Errors = append(report.Errors, "missing non-empty string field: description")
		}
		return report
	}
	report.Status = "ok"
	report.Reachable = true
	report.Reason = "ok"
	return report
}

func skillSourceFrontmatter(data []byte) (string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", fmt.Errorf("front matter must start on first line with ---")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), nil
		}
	}
	return "", fmt.Errorf("front matter must close with ---")
}

func skillSourceStringField(fields map[string]any, name string) string {
	value, ok := fields[name]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
