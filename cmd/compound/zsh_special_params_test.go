package main

// zsh special-parameter guard. In zsh a handful of lowercase parameter names are not
// ordinary variables: they are TIED to shell state. Assigning one as a scalar does not
// shadow it, it corrupts the thing it is tied to -- and the corruption is silent at the
// assignment site, surfacing later as an unrelated failure.
//
// Measured 2026-07-27 (probe over all 18 candidate specials, `local <p>=/tmp/scratch`
// followed by an external command): exactly TWO break:
//
//	path    -> tied to $PATH. `local path=...` empties command lookup for the rest of the
//	           function: `command not found: head`, thrown from a line that has nothing to
//	           do with the assignment. This is the one that bites, because `path` is the
//	           natural name for a variable holding a file path. Lived: a citation resolver
//	           used `local path="${cite%%:*}"` and died seven lines later on a missing
//	           `head`.
//	status  -> read-only. `local status=...` aborts the function with
//	           `read-only variable: status`.
//
// The other tied parameters (cdpath, fpath, manpath, module_path, fignore, mailpath,
// psvar, watch, prompt, argv, options, commands, functions, aliases, parameters,
// dirstack) do NOT break command execution when assigned locally, so they are
// deliberately NOT flagged -- a guard that cries wolf on a harmless `prompt=` teaches
// people to ignore it.
//
// Re-probing warning: probe a tied parameter with an EXTERNAL command, never a builtin.
// The first probe run for this guard reported a false "ok" for `path`, because `echo`
// survives an empty $PATH.
//
// This lives as a Go test because `go test ./...` is what actually runs in this repo:
// there is no CI workflow and the Makefile has two targets (install, release-artifacts),
// so a standalone shell linter would be a surface nobody invokes. The rule needs to run
// itself, not wait to be remembered.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// zshSpecialAssignmentRe matches an assignment to a fatal zsh special parameter.
//
// The leading (^|[;&|(){}[:space:]]) is load-bearing: without it this fires on every
// ordinary name that merely ENDS in one of the words -- `cite_path=`, `repo_path=`,
// `exit_status=` -- which is most of them, and a guard with that false-positive rate gets
// deleted rather than obeyed. Pinned by TestZshSpecialAssignmentDetector.
var zshSpecialAssignmentRe = regexp.MustCompile(
	`(^|[;&|(){}[:space:]])(local[[:space:]]+|typeset[[:space:]]+|declare[[:space:]]+|export[[:space:]]+)?(path|status)=`,
)

// isZshScript reports whether a file is interpreted by zsh: a `.zsh` extension, or a
// shebang naming zsh. Bash treats `path` as an ordinary name, so a bash script assigning
// it is not a defect and must not be reported.
func isZshScript(name string, firstLine string) bool {
	if strings.HasSuffix(name, ".zsh") {
		return true
	}
	return strings.HasPrefix(firstLine, "#!") && strings.Contains(firstLine, "zsh")
}

// TestZshScriptsDoNotAssignSpecialParameters walks every zsh script under the repo and
// fails on an assignment to a fatal special parameter, naming file:line and the fix.
//
// Falsifier: add `local path=/tmp/x` to any zsh script in the repo and this goes RED
// naming that file:line; remove it and this goes GREEN. Deliberately phrased against "any
// zsh script" rather than a specific one, so the falsifier is reproducible in a fresh
// clone instead of depending on a file that may not be present.
func TestZshScriptsDoNotAssignSpecialParameters(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	type hit struct {
		file string
		line int
		text string
	}
	var hits []hit

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not this guard's business; skip rather than fail.
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "worktrees":
				// `worktrees` holds full copies of this repo -- scanning them would
				// report every hit a second time under a path that is not the source.
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".sh" && ext != ".zsh" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		lines := strings.Split(string(body), "\n")
		if len(lines) == 0 || !isZshScript(path, lines[0]) {
			return nil
		}
		for i, line := range lines {
			code := line
			if idx := strings.Index(code, "#"); idx >= 0 {
				code = code[:idx] // a comment naming the trap is not the trap
			}
			if zshSpecialAssignmentRe.MatchString(code) {
				rel, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					rel = path
				}
				hits = append(hits, hit{file: rel, line: i + 1, text: strings.TrimSpace(line)})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}

	for _, h := range hits {
		t.Errorf(
			"%s:%d assigns a tied zsh special parameter -- rename it (e.g. cite_path, exit_status):\n    %s\n"+
				"    `path` is tied to $PATH: assigning it empties command lookup for the rest of the\n"+
				"    function, and the failure surfaces later as `command not found` on an unrelated line.\n"+
				"    `status` is read-only: assigning it aborts the function.",
			h.file, h.line, h.text,
		)
	}
}

// TestZshSpecialAssignmentDetector pins the detector itself, because a guard that
// over-reports is a guard that gets switched off. The negative cases are the ones that
// matter: every one of them appeared in real scripts in this repo.
func TestZshSpecialAssignmentDetector(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		// Positives -- the defect in each form it can take.
		{"bare", `path=/tmp/x`, true},
		{"local", `  local path="${cite%%:*}"`, true},
		{"typeset", `typeset path=/tmp/x`, true},
		{"export", `export path=/tmp/x`, true},
		{"status", `local status=done`, true},
		{"after semicolon", `cd /tmp; path=/x`, true},
		{"inside braces", `f() { path=/x; }`, true},

		// Negatives -- ordinary names that merely END in a special word. Without the
		// leading boundary these all false-positive, which is most real code.
		{"suffix cite_path", `local cite_path="${cite%%:*}"`, false},
		{"suffix repo_path", `repo_path=/tmp/x`, false},
		{"suffix exit_status", `exit_status=1`, false},
		{"uppercase PATH", `PATH=/usr/bin:$PATH`, false},
		{"other tied param", `fpath=(/usr/share/zsh $fpath)`, false},
		{"reference not assignment", `echo "$path"`, false},
		{"comparison", `[[ "$status" == ok ]]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := zshSpecialAssignmentRe.MatchString(tc.line); got != tc.want {
				t.Errorf("MatchString(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}
