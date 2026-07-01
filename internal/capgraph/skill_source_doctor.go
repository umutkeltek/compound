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
}

type SkillSourceDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type SkillSourceFileReport struct {
	Path        string   `json:"path"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Errors      []string `json:"errors,omitempty"`
}

type SkillSourceDuplicateName struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

func DoctorSkillSource(root string) (SkillSourceDoctorReport, error) {
	root = filepath.Clean(root)
	files, err := skillSourceFiles(root)
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
	addCheck("skill_files", len(files) > 0, fmt.Sprintf("%d", len(files)))
	report.SkillFileCount = len(files)

	namePaths := map[string][]int{}
	for _, path := range files {
		fileReport := doctorSkillSourceFile(root, path)
		if fileReport.Status != "ok" {
			report.Status = "fail"
			report.ErrorCount += len(fileReport.Errors)
		}
		if strings.TrimSpace(fileReport.Name) != "" {
			namePaths[fileReport.Name] = append(namePaths[fileReport.Name], len(report.Files))
		}
		report.Files = append(report.Files, fileReport)
	}

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
	return report, nil
}

func skillSourceFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if filepath.Base(root) != "SKILL.md" {
			return nil, fmt.Errorf("skill source file must be named SKILL.md: %s", root)
		}
		return []string{root}, nil
	}
	seen := map[string]bool{}
	var files []string
	for _, pattern := range []string{
		filepath.Join(root, "*", "SKILL.md"),
		filepath.Join(root, "skills", "*", "SKILL.md"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			match = filepath.Clean(match)
			if seen[match] {
				continue
			}
			seen[match] = true
			files = append(files, match)
		}
	}
	sort.Strings(files)
	return files, nil
}

func doctorSkillSourceFile(root, path string) SkillSourceFileReport {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	report := SkillSourceFileReport{Path: rel, Status: "ok"}
	data, err := os.ReadFile(path)
	if err != nil {
		report.Status = "fail"
		report.Errors = append(report.Errors, fmt.Sprintf("read: %v", err))
		return report
	}
	frontmatter, err := skillSourceFrontmatter(data)
	if err != nil {
		report.Status = "fail"
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
		report.Status = "fail"
		report.Errors = append(report.Errors, fmt.Sprintf("YAML: %v", err))
		return report
	}
	if fields == nil {
		report.Status = "fail"
		report.Errors = append(report.Errors, "front matter YAML mapping")
		return report
	}
	report.Name = skillSourceStringField(fields, "name")
	report.Description = skillSourceStringField(fields, "description")
	if report.Name == "" {
		report.Status = "fail"
		report.Errors = append(report.Errors, "missing non-empty string field: name")
	}
	if report.Description == "" {
		report.Status = "fail"
		report.Errors = append(report.Errors, "missing non-empty string field: description")
	}
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
