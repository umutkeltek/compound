package main

// memory_lesson_cli.go owns `compound memory lesson`: the promote-on-write door
// for agent-written lessons (operator ruling 2026-11-03, Q5). The ledger write
// belongs to mem.WriteLesson; this file owns flag parsing, skill-file
// resolution and the append-only "## Lessons" line in the named skill.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/mem"
)

const memoryLessonUsage = "usage: compound memory lesson --why TEXT --how TEXT --by ACTOR [--skill NAME] [--trigger TOOL:REGEX] [--evidence REF]... [--slot entity::predicate] [--harness H] [--work W] [--repo DIR] [--skills-dir DIR]... [--json]\n" +
	"  Promotes on write (claim_type lesson). With --skill, also appends one dated line to the skill's \"## Lessons\" section.\n" +
	"  Skill files resolve from <repo>/skills/<name>/SKILL.md, then each --skills-dir, then each dir in $" + skillsDirsEnv + "."

// skillsDirsEnv lists extra skill roots (path-list separated). The lesson door
// never hardcodes a personal skills directory; the caller names it.
const skillsDirsEnv = "COMPOUND_SKILLS_DIRS"

type memoryLessonArgs struct {
	in        mem.LessonInput
	repo      string
	skillDirs []string
	json      bool
}

func parseMemoryLessonArgs(args []string) (memoryLessonArgs, error) {
	var out memoryLessonArgs
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

// lessonSkillRoots returns the ordered skill roots: the repo's tracked skills
// first, then explicit --skills-dir values, then $COMPOUND_SKILLS_DIRS.
func lessonSkillRoots(repo string, flagDirs []string) []string {
	var roots []string
	if strings.TrimSpace(repo) == "" {
		if top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
			repo = strings.TrimSpace(string(top))
		}
	}
	if strings.TrimSpace(repo) != "" {
		roots = append(roots, filepath.Join(repo, "skills"))
	}
	roots = append(roots, flagDirs...)
	for _, dir := range filepath.SplitList(os.Getenv(skillsDirsEnv)) {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, dir)
		}
	}
	return roots
}

// resolveLessonSkillFile finds <root>/<skill>/SKILL.md in the first root that
// has it. It fails closed (before any ledger write) when the skill is named
// but no file resolves, so a typo never leaves a lesson without its skill line.
func resolveLessonSkillFile(skill string, roots []string) (string, error) {
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
	return "", fmt.Errorf("skill %q has no SKILL.md under %v (pass --repo, --skills-dir or $%s)", skill, roots, skillsDirsEnv)
}

// appendLessonToSkill appends line to the "## Lessons" section of the skill
// file (creating the section at the end when absent). Append-only and
// idempotent: a file that already names the lesson claim id is left unchanged.
func appendLessonToSkill(path, line, claimID string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(data)
	if claimID != "" && strings.Contains(text, claimID) {
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
		lines = append(lines, "", "## Lessons", "", line)
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
		insert := []string{line}
		if last == section {
			insert = []string{"", line}
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

type memoryLessonReceipt struct {
	mem.LessonResult
	Slot          string `json:"slot"`
	SkillFile     string `json:"skill_file,omitempty"`
	SkillLine     string `json:"skill_line,omitempty"`
	SkillAppended bool   `json:"skill_appended"`
}

func cmdMemoryLesson(store *mem.Store, args []string) {
	parsed, err := parseMemoryLessonArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, memoryLessonUsage)
		os.Exit(2)
	}
	skillFile, err := resolveLessonSkillFile(parsed.in.Skill, lessonSkillRoots(parsed.repo, parsed.skillDirs))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	res, err := store.WriteLesson(parsed.in, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	receipt := memoryLessonReceipt{LessonResult: res, Slot: res.Claim.Entity + "::" + res.Claim.Predicate}
	if skillFile != "" {
		receipt.SkillFile = skillFile
		receipt.SkillLine = mem.LessonSkillLine(res.Claim)
		receipt.SkillAppended, err = appendLessonToSkill(skillFile, receipt.SkillLine, res.Claim.ID)
		if err != nil {
			// The lesson is already promoted; say exactly what is missing.
			fmt.Fprintf(os.Stderr, "error: lesson %s promoted but skill line not written to %s: %v\n", res.Claim.ID, skillFile, err)
			os.Exit(1)
		}
	}
	if parsed.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(receipt); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("lesson %s: %s\n", res.Outcome, res.Claim.ID)
	fmt.Printf("  slot: %s\n  position: %s\n", receipt.Slot, res.PositionID)
	if res.ContestedWith != "" {
		fmt.Printf("  contested_with: %s (both kept; the next consumer that records a use supersedes the other)\n", res.ContestedWith)
	}
	if skillFile != "" {
		state := "appended"
		if !receipt.SkillAppended {
			state = "already present"
		}
		fmt.Printf("  skill: %s (%s)\n", skillFile, state)
	}
}

// resolveContestedLessonsOnUse is the consumer-supersedes step on `work close
// --used`: each used ref that names a lesson claim (claim_* or lesson:claim_*)
// resolves its contradiction in favour of the used lesson. Close has already
// succeeded, so failures are warnings, never a failed close.
func resolveContestedLessonsOnUse(homePath, workID, actor, usedCSV string) {
	var refs []string
	for _, ref := range splitCSV(usedCSV) {
		ref = strings.TrimPrefix(strings.TrimSpace(ref), "lesson:")
		if strings.HasPrefix(ref, "claim_") {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: lesson use not recorded:", err)
		return
	}
	for _, ref := range refs {
		res, err := store.ResolveContestedLessonOnUse(ref, actor, workID, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: lesson use %s not recorded: %v\n", ref, err)
			continue
		}
		if len(res.Superseded) > 0 {
			fmt.Printf("lesson %s used: superseded contested %s\n", ref, strings.Join(res.Superseded, ","))
		}
	}
}
