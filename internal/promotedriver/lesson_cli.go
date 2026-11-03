package promotedriver

// lesson_cli.go owns `compound memory lesson`: the promote-on-write door for
// agent-written lessons (operator ruling 2026-11-03, Q5). The ledger write
// belongs to mem.WriteLesson; this file owns flag parsing, skill-file
// resolution and the append-only "## Lessons" lines in the named skill. It lives
// beside the promote driver because both are promotion doors; cmd/compound
// only dispatches to RunLessonCLI.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/mem"
)

// SkillsDirsEnv lists extra skill roots (path-list separated). The lesson door
// never hardcodes a personal skills directory; the caller names it.
const SkillsDirsEnv = "COMPOUND_SKILLS_DIRS"

// LessonUsage is the usage text for `compound memory lesson`.
const LessonUsage = "usage: compound memory lesson --why TEXT --how TEXT --by ACTOR [--skill NAME] [--trigger TOOL:REGEX] [--evidence REF]... [--slot entity::predicate] [--harness H] [--work W] [--repo DIR] [--skills-dir DIR]... [--json]\n" +
	"  Promotes on write (claim_type lesson). With --skill, also appends a dated line (and a trigger: JSON line when --trigger is set) to the skill's \"## Lessons\" section.\n" +
	"  Skill files resolve from <repo>/skills/<name>/SKILL.md (repo defaults to the enclosing checkout), then each --skills-dir, then each dir in $" + SkillsDirsEnv + "."

type lessonArgs struct {
	in        mem.LessonInput
	repo      string
	skillDirs []string
	json      bool
}

func parseLessonArgs(args []string) (lessonArgs, error) {
	var out lessonArgs
	value := func(i *int, name string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		var err error
		switch a := args[i]; a {
		case "--why":
			out.in.Why, err = value(&i, a)
		case "--how":
			out.in.How, err = value(&i, a)
		case "--by":
			out.in.Actor, err = value(&i, a)
		case "--skill":
			out.in.Skill, err = value(&i, a)
		case "--trigger":
			out.in.Trigger, err = value(&i, a)
		case "--slot":
			out.in.Slot, err = value(&i, a)
		case "--harness":
			out.in.Harness, err = value(&i, a)
		case "--work":
			out.in.WorkID, err = value(&i, a)
		case "--repo":
			out.repo, err = value(&i, a)
		case "--evidence":
			var ref string
			ref, err = value(&i, a)
			out.in.Evidence = append(out.in.Evidence, ref)
		case "--skills-dir":
			var dir string
			dir, err = value(&i, a)
			out.skillDirs = append(out.skillDirs, dir)
		case "--json":
			out.json = true
		default:
			return out, fmt.Errorf("unknown argument %q", a)
		}
		if err != nil {
			return out, err
		}
	}
	if strings.TrimSpace(out.in.Harness) == "" {
		out.in.Harness = strings.TrimSpace(os.Getenv("COMPOUND_HARNESS"))
	}
	return out, nil
}

// enclosingCheckout walks up from dir to the first directory holding .git.
func enclosingCheckout(dir string) string {
	for d := dir; d != "" && d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
	}
	return ""
}

// LessonSkillRoots returns the ordered skill roots: the repo's tracked skills
// first, then explicit --skills-dir values, then $COMPOUND_SKILLS_DIRS.
func LessonSkillRoots(repo string, flagDirs []string) []string {
	var roots []string
	if strings.TrimSpace(repo) == "" {
		if wd, err := os.Getwd(); err == nil {
			repo = enclosingCheckout(wd)
		}
	}
	if strings.TrimSpace(repo) != "" {
		roots = append(roots, filepath.Join(repo, "skills"))
	}
	roots = append(roots, flagDirs...)
	for _, dir := range filepath.SplitList(os.Getenv(SkillsDirsEnv)) {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, dir)
		}
	}
	return roots
}

// ResolveLessonSkillFile finds <root>/<skill>/SKILL.md in the first root that
// has it. It fails closed (before any ledger write) when the skill is named
// but no file resolves, so a typo never leaves a lesson without its skill line.
func ResolveLessonSkillFile(skill string, roots []string) (string, error) {
	skill = strings.TrimSpace(skill)
	if skill == "" {
		return "", nil
	}
	if strings.ContainsAny(skill, `/\`) || skill == "." || skill == ".." {
		return "", fmt.Errorf("skill %q must be a bare skill name", skill)
	}
	for _, root := range roots {
		path := filepath.Join(root, skill, "SKILL.md")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("skill %q has no SKILL.md under %v (pass --repo, --skills-dir or $%s)", skill, roots, SkillsDirsEnv)
}

// AppendLessonToSkill appends the lesson's lines to the "## Lessons" section of
// the skill file (creating the section at the end when absent). Append-only and
// idempotent per line: a line already present anywhere in the file is skipped,
// so re-running a lesson only fills in what is missing.
func AppendLessonToSkill(path string, block []string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(data)
	present := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		present[l] = true
	}
	var missing []string
	for _, l := range block {
		if !present[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	section := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## Lessons" {
			section = i
		}
	}
	if section < 0 {
		lines = append(append(lines, "", "## Lessons", ""), missing...)
	} else {
		end := len(lines)
		for i := section + 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ") {
				end = i
				break
			}
		}
		last := section
		for i := section + 1; i < end; i++ {
			if strings.TrimSpace(lines[i]) != "" {
				last = i
			}
		}
		insert := missing
		if last == section {
			insert = append([]string{""}, missing...)
		}
		if last+1 == end && end < len(lines) {
			insert = append(insert, "")
		}
		merged := make([]string, 0, len(lines)+len(insert))
		merged = append(merged, lines[:last+1]...)
		merged = append(merged, insert...)
		merged = append(merged, lines[last+1:]...)
		lines = merged
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	tmp := path + ".lesson.tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), info.Mode().Perm()); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// LessonReceipt is the JSON receipt of one `compound memory lesson` call.
type LessonReceipt struct {
	mem.LessonResult
	Slot          string   `json:"slot"`
	SkillFile     string   `json:"skill_file,omitempty"`
	SkillLines    []string `json:"skill_lines,omitempty"`
	SkillAppended bool     `json:"skill_appended"`
}

// RunLessonCLI runs `compound memory lesson` and returns the exit code.
func RunLessonCLI(store *mem.Store, args []string, stdout, stderr io.Writer) int {
	parsed, err := parseLessonArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		fmt.Fprintln(stderr, LessonUsage)
		return 2
	}
	skillFile, err := ResolveLessonSkillFile(parsed.in.Skill, LessonSkillRoots(parsed.repo, parsed.skillDirs))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	res, err := store.WriteLesson(parsed.in, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	receipt := LessonReceipt{LessonResult: res, Slot: res.Claim.Entity + "::" + res.Claim.Predicate}
	if skillFile != "" {
		receipt.SkillFile = skillFile
		receipt.SkillLines = mem.LessonSkillLines(res.Claim)
		receipt.SkillAppended, err = AppendLessonToSkill(skillFile, receipt.SkillLines)
		if err != nil {
			// The lesson is already promoted; say exactly what is missing.
			fmt.Fprintf(stderr, "error: lesson %s promoted but skill lines not written to %s: %v\n", res.Claim.ID, skillFile, err)
			return 1
		}
	}
	if parsed.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(receipt); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "lesson %s: %s\n  slot: %s\n  position: %s\n", res.Outcome, res.Claim.ID, receipt.Slot, res.PositionID)
	if res.ContestedWith != "" {
		fmt.Fprintf(stdout, "  contested_with: %s (both kept; the next consumer that records a use supersedes the other)\n", res.ContestedWith)
	}
	if skillFile != "" {
		state := "appended"
		if !receipt.SkillAppended {
			state = "already present"
		}
		fmt.Fprintf(stdout, "  skill: %s (%s)\n", skillFile, state)
	}
	return 0
}
