package policy

import "strings"

// The routine-read lane for shell commands.
//
// classifyShellAction answers one question: does every executable segment of a
// command — including the bodies of its command substitutions — name a
// read-only local verb? An allowlist answers it. Anything else leaves the
// routine lane and is classified by its worst consequence, so the boundary
// still fails closed on what it does not recognize.
//
// Why the verb and not the command text: substring matching cannot tell
// `grep "^func Test"` (which contains "nc ") from netcat, and cannot tell
// `echo "curl x"` from egress. It also cannot tell `ls a; ls b` — a pair of
// reads — from a composed attack, which is why the pre-existing rule of
// "contains a shell metacharacter, therefore unknown, therefore hold" turned
// every ordinary read into an operator prompt. docs/security/prompt-injection.md
// promises that routine reads stay automatic; holding them trains the operator
// to click through holds, which defeats the boundary it is protecting.
//
// Decomposition is deliberately quote-blind. A `;` or `|` inside a quoted
// string splits a segment the shell would have kept whole, producing MORE
// segments that each must pass the allowlist. Over-splitting can only
// over-restrict, never admit something the shell would run differently.
//
// Adding a verb to any read table requires that the verb cannot write, execute,
// or egress — including through its own flags (`sed -i`, `find -exec`).

const (
	shellClassRead        = "read"
	shellClassGoverned    = "governed"
	shellClassInstall     = "install"
	shellClassUnknown     = "unknown"
	shellClassNetwork     = "network"
	shellClassDestructive = "destructive"
)

// shellClassSeverity orders escalation when one command mixes classes: the
// worst segment decides the whole command.
var shellClassSeverity = map[string]int{
	shellClassRead:        0,
	shellClassGoverned:    1,
	shellClassInstall:     2,
	shellClassUnknown:     3,
	shellClassNetwork:     4,
	shellClassDestructive: 5,
}

// shellSubstitutionPlaceholder stands in for a command substitution body in the
// outer segment. It is not a verb in any table, so a substitution used AS the
// verb (`$(echo ls) x`) leaves the routine lane.
const shellSubstitutionPlaceholder = "compound_substitution_value"

var shellReadVerbs = map[string]bool{
	// Location and metadata.
	"cd": true, "pwd": true, "ls": true, "eza": true, "tree": true, "stat": true,
	"file": true, "basename": true, "dirname": true, "realpath": true, "readlink": true,
	"du": true, "df": true, "uptime": true, "date": true, "whoami": true, "hostname": true,
	"uname": true, "arch": true, "printenv": true, "which": true, "id": true, "groups": true,
	"sw_vers": true, "vm_stat": true, "ps": true, "lsof": true, "nproc": true,
	// Emit and format.
	"echo": true, "printf": true, "true": true, "false": true,
	// Read and transform text.
	"cat": true, "bat": true, "head": true, "tail": true, "wc": true, "nl": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "column": true, "rev": true,
	"fold": true, "paste": true, "join": true, "comm": true, "expand": true, "unexpand": true,
	"diff": true, "cmp": true, "diff3": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true,
	"jq": true, "yq": true,
	// Digest and inspect bytes.
	"md5": true, "md5sum": true, "shasum": true, "sha1sum": true, "sha256sum": true,
	"base64": true, "xxd": true, "od": true, "strings": true,
	// First-party read-only scanner.
	"redact": true,
}

var shellDestructiveVerbs = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "shred": true, "srm": true,
	"dd": true, "diskutil": true, "fdisk": true, "newfs": true, "truncate": true,
}

var shellNetworkVerbs = map[string]bool{
	"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true, "socat": true,
	"telnet": true, "ssh": true, "scp": true, "sftp": true, "rsync": true, "ftp": true,
	"http": true, "https": true, "httpie": true,
	"gh": true, "glab": true, "aws": true, "gcloud": true, "az": true,
	"wrangler": true, "vercel": true, "netlify": true,
}

// shellNestedReadVerbs holds first-party CLIs whose read-only subcommands are
// known. A nil value list means every invocation of that subcommand is a read.
// A verb absent here leaves the routine lane, which is why governed execution
// (`compound work advance --execute`) still reaches the operator.
var shellNestedReadVerbs = map[string]map[string][]string{
	"mcporter": {
		"list": nil,
	},
}

// compoundReadSubcommands and compoundGovernedSubcommands classify the
// first-party control CLI without treating every `compound` invocation as
// trusted. The outer monitor recognizes only these exact command shapes; each
// governed verb still performs its own ActionIntent/PolicyDecision checks.
// Anything absent from both maps remains unknown and hits the security floor.
var compoundReadSubcommands = map[string][]string{
	"version":      nil,
	"orient":       nil,
	"status":       nil,
	"executors":    nil,
	"route":        nil,
	"coordination": nil,
	"workbench":    nil,
	"attention":    nil,
	"decisions":    {"show", "list"},
	"work":         {"list", "show"},
	"capability":   {"authority", "show", "list"},
	"land-lease":   {"status"},
	"authority":    {"git"},
}

var compoundGovernedSubcommands = map[string][]string{
	"proof-check": nil,
	"work": {
		"create",
		"claim",
		"advance",
		"close-plan",
		"land-check",
		"close",
	},
	"land-lease": {"acquire", "release"},
	"authority":  {"dirty-plan", "dirty-review"},
}

var gitReadSubcommands = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "rev-parse": true,
	"rev-list": true, "ls-files": true, "ls-tree": true, "merge-base": true,
	"describe": true, "blame": true, "annotate": true, "cat-file": true,
	"for-each-ref": true, "shortlog": true, "reflog": true, "count-objects": true,
	"name-rev": true, "check-ignore": true, "check-attr": true, "diff-tree": true,
	"diff-index": true, "grep": true, "show-ref": true, "symbolic-ref": true,
	"var": true, "version": true, "help": true, "whatchanged": true, "cherry": true,
	"verify-commit": true, "verify-tag": true,
}

var gitNetworkSubcommands = map[string]bool{
	"push": true, "pull": true, "fetch": true, "clone": true, "ls-remote": true,
	"submodule": true, "request-pull": true, "send-email": true, "svn": true,
}

// gitGlobalFlagsWithValue take a separate argument, so the value must not be
// mistaken for the subcommand (`git -C /path status`).
var gitGlobalFlagsWithValue = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--exec-path": true,
}

// gitRefQueryFlagsWithValue take a separate argument on the ref-listing
// subcommands, where the presence of a bare operand is what distinguishes
// listing from creating. Without these, `git branch -a --contains <sha>` reads
// the SHA as a new branch name and a plain listing gets held.
var gitRefQueryFlagsWithValue = map[string]bool{
	"--contains": true, "--no-contains": true,
	"--merged": true, "--no-merged": true,
	"--points-at": true, "--sort": true, "--format": true,
	"--color": true, "--abbrev": true, "-n": true,
}

var goReadSubcommands = map[string]bool{
	"test": true, "vet": true, "build": true, "list": true, "env": true,
	"doc": true, "version": true,
}

func classifyShellAction(classification ActionClassification, command string) ActionClassification {
	if strings.TrimSpace(command) == "" {
		classification.Known = false
		classification.Operation = "unknown"
		classification.MutationClass = "security"
		classification.Reversibility = "hard"
		classification.BlastRadius = "host"
		return classification
	}
	segments, ok := shellLex(command)
	if !ok || len(segments) == 0 {
		return shellClassificationFor(classification, shellClassUnknown)
	}
	worst := shellClassRead
	for _, segment := range segments {
		class := shellSegmentClass(segment)
		if shellClassSeverity[class] > shellClassSeverity[worst] {
			worst = class
		}
	}
	return shellClassificationFor(classification, worst)
}

func shellClassificationFor(classification ActionClassification, class string) ActionClassification {
	switch class {
	case shellClassRead:
		classification.Operation = "local_command"
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "easy"
		classification.BlastRadius = "local"
	case shellClassGoverned:
		classification.Operation = "governed_command"
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "easy"
		classification.BlastRadius = "local"
	case shellClassNetwork:
		classification.Operation = "network_command"
		classification.RequiredSink = "network"
		classification.MutationClass = "external_side_effect"
		classification.Reversibility = "hard"
		classification.BlastRadius = "internet"
	case shellClassDestructive:
		classification.Operation = "destructive_command"
		classification.MutationClass = "destructive"
		classification.Reversibility = "irreversible"
		classification.BlastRadius = "host"
	case shellClassInstall:
		classification.Operation = "install_command"
		classification.RequiredSink = "install"
		classification.MutationClass = "security"
		classification.Reversibility = "manual"
		classification.BlastRadius = "host"
	default:
		classification.Known = false
		classification.Operation = "unknown_command"
		classification.MutationClass = "security"
		classification.Reversibility = "hard"
		classification.BlastRadius = "host"
	}
	return classification
}

// shellSegment is one executable segment of a command: the words it runs,
// whether it redirects output somewhere that keeps the data, and whether a word
// was spliced together across a quote boundary.
type shellSegment struct {
	words      []string
	writes     bool
	obfuscated bool
}

// shellLex decomposes a command into its executable segments in one pass,
// honoring quoting and backslash escapes the way the shell does, and collecting
// each command-substitution body as a segment of its own.
//
// Quote fidelity is a correctness requirement in both directions: `go test -run
// 'TestA|TestB'` must stay one segment (the pipe is data), and `echo "; curl x"`
// must stay one segment for the same reason — the shell runs no curl there.
//
// It reports false when the command cannot be lexed — a NUL byte, an
// unterminated quote or substitution, backgrounding, or process substitution —
// so anything unanalyzable fails closed instead of being read as a bag of words.
func shellLex(command string) ([]shellSegment, bool) {
	if strings.ContainsRune(command, 0) {
		return nil, false
	}
	segments := []shellSegment{}
	current := shellSegment{}
	word := strings.Builder{}
	trailing := []shellSegment{}

	endWord := func() {
		if word.Len() > 0 {
			current.words = append(current.words, word.String())
			word.Reset()
		}
	}
	endSegment := func() {
		endWord()
		if len(current.words) > 0 || current.writes {
			segments = append(segments, current)
		}
		current = shellSegment{}
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '\\':
			if i+1 < len(runes) {
				i++
				word.WriteRune(runes[i])
				continue
			}
			word.WriteRune(r)
		case '\'':
			body, end, ok := shellReadQuoted(runes, i)
			if !ok {
				return nil, false
			}
			// An empty quote pair, or a word that continues right after the
			// closing quote, splices a verb out of fragments. Nothing routine
			// writes a command that way, so the splice itself is the signal:
			// the segment leaves the routine lane even though the assembled
			// word resolves to a real program.
			if body == "" || shellWordContinues(runes, end) {
				current.obfuscated = true
			}
			word.WriteString(body)
			i = end
		case '"':
			// Double quotes do not suppress command substitution, so the body
			// is scanned rather than taken literally.
			body, nested, end, ok := shellReadDoubleQuoted(runes, i)
			if !ok {
				return nil, false
			}
			trailing = append(trailing, nested...)
			if body == "" || shellWordContinues(runes, end) {
				current.obfuscated = true
			}
			word.WriteString(body)
			i = end
		case '`':
			body, end, ok := shellReadBacktick(runes, i)
			if !ok {
				return nil, false
			}
			nested, ok := shellLex(body)
			if !ok {
				return nil, false
			}
			trailing = append(trailing, nested...)
			word.WriteString(shellSubstitutionPlaceholder)
			i = end
		case '$':
			if i+1 < len(runes) && runes[i+1] == '(' {
				body, end, ok := shellReadSubstitution(runes, i+1)
				if !ok {
					return nil, false
				}
				nested, ok := shellLex(body)
				if !ok {
					return nil, false
				}
				trailing = append(trailing, nested...)
				word.WriteString(shellSubstitutionPlaceholder)
				i = end
				continue
			}
			word.WriteRune(r)
		case '<', '>':
			if i+1 < len(runes) && runes[i+1] == '(' {
				return nil, false // process substitution runs a command
			}
			end, writes, ok := shellReadRedirection(runes, i, &word, &current)
			if !ok {
				return nil, false
			}
			if writes {
				current.writes = true
			}
			i = end
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				endSegment()
				i++
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '>' {
				end, writes, ok := shellReadRedirection(runes, i+1, &word, &current)
				if !ok {
					return nil, false
				}
				if writes {
					current.writes = true
				}
				i = end
				continue
			}
			return nil, false // backgrounding detaches the command
		case '|':
			endSegment()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		case ';', '\n', '\r':
			endSegment()
		case ' ', '\t':
			endWord()
		default:
			word.WriteRune(r)
		}
	}
	endSegment()
	return append(segments, trailing...), true
}

// shellReadQuoted returns the body of the quoted span starting at open, with
// backslash escapes resolved inside double quotes only, as the shell does.
func shellReadQuoted(runes []rune, open int) (string, int, bool) {
	quote := runes[open]
	body := strings.Builder{}
	for i := open + 1; i < len(runes); i++ {
		if quote == '"' && runes[i] == '\\' && i+1 < len(runes) {
			i++
			body.WriteRune(runes[i])
			continue
		}
		if runes[i] == quote {
			return body.String(), i, true
		}
		body.WriteRune(runes[i])
	}
	return "", 0, false
}

// shellReadDoubleQuoted returns the literal text of a double-quoted span with
// every command substitution replaced by a placeholder, plus the segments those
// substitutions run. `"$(cmd)"` executes cmd, so the quotes hide nothing and the
// inner command must be classified on its own.
func shellReadDoubleQuoted(runes []rune, open int) (string, []shellSegment, int, bool) {
	text := strings.Builder{}
	nested := []shellSegment{}
	for i := open + 1; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			if i+1 < len(runes) {
				i++
				text.WriteRune(runes[i])
			}
		case '"':
			return text.String(), nested, i, true
		case '`':
			body, end, ok := shellReadBacktick(runes, i)
			if !ok {
				return "", nil, 0, false
			}
			segments, ok := shellLex(body)
			if !ok {
				return "", nil, 0, false
			}
			nested = append(nested, segments...)
			text.WriteString(shellSubstitutionPlaceholder)
			i = end
		case '$':
			if i+1 < len(runes) && runes[i+1] == '(' {
				body, end, ok := shellReadSubstitution(runes, i+1)
				if !ok {
					return "", nil, 0, false
				}
				segments, ok := shellLex(body)
				if !ok {
					return "", nil, 0, false
				}
				nested = append(nested, segments...)
				text.WriteString(shellSubstitutionPlaceholder)
				i = end
				continue
			}
			text.WriteRune(runes[i])
		default:
			text.WriteRune(runes[i])
		}
	}
	return "", nil, 0, false
}

func shellReadBacktick(runes []rune, open int) (string, int, bool) {
	for i := open + 1; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			i++
			continue
		}
		if runes[i] == '`' {
			return string(runes[open+1 : i]), i, true
		}
	}
	return "", 0, false
}

// shellReadSubstitution returns the body of a $( … ) span, tracking nesting and
// skipping quoted spans so a `)` inside a string does not close it early.
func shellReadSubstitution(runes []rune, open int) (string, int, bool) {
	depth := 0
	for i := open; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++
		case '\'', '"':
			_, end, ok := shellReadQuoted(runes, i)
			if !ok {
				return "", 0, false
			}
			i = end
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return string(runes[open+1 : i]), i, true
			}
		}
	}
	return "", 0, false
}

// shellReadRedirection consumes a redirection operator and its target. A
// discard to /dev/null and a descriptor duplication keep no data, so they are
// not writes; anything else is. The leading file-descriptor digit belongs to
// the operator, not to the preceding word.
func shellReadRedirection(runes []rune, start int, word *strings.Builder, current *shellSegment) (int, bool, bool) {
	if digits := word.String(); digits != "" && shellAllDigits(digits) {
		word.Reset()
	}
	i := start
	input := runes[i] == '<'
	for i+1 < len(runes) && (runes[i+1] == '>' || runes[i+1] == '<') {
		i++
	}
	if i+1 < len(runes) && runes[i+1] == '&' {
		i++
		for i+1 < len(runes) && (runes[i+1] == '-' || (runes[i+1] >= '0' && runes[i+1] <= '9')) {
			i++
		}
		return i, false, true
	}
	for i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\t') {
		i++
	}
	target := strings.Builder{}
	for i+1 < len(runes) {
		next := runes[i+1]
		if next == ' ' || next == '\t' || next == ';' || next == '|' || next == '\n' || next == '&' {
			break
		}
		if next == '\'' || next == '"' {
			body, end, ok := shellReadQuoted(runes, i+1)
			if !ok {
				return 0, false, false
			}
			target.WriteString(body)
			i = end
			continue
		}
		i++
		target.WriteRune(next)
	}
	if target.Len() == 0 {
		return i, false, false
	}
	if input {
		return i, false, true
	}
	return i, target.String() != "/dev/null", true
}

// shellWordContinues reports whether the character after a closing quote keeps
// building the same word, rather than ending it.
func shellWordContinues(runes []rune, closing int) bool {
	if closing+1 >= len(runes) {
		return false
	}
	switch runes[closing+1] {
	case ' ', '\t', '\n', '\r', ';', '|', '&', '<', '>', ')':
		return false
	}
	return true
}

func shellAllDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func shellSegmentClass(segment shellSegment) string {
	if segment.writes || segment.obfuscated {
		return shellClassUnknown
	}
	fields := shellStripCommandPrefixes(segment.words)
	if len(fields) == 0 {
		return shellClassRead
	}
	return shellVerbClass(shellVerbName(fields[0]), fields[1:])
}

// shellStripCommandPrefixes drops environment assignments and the wrappers that
// merely run another command, so the classified verb is the one that acts.
// `sudo` is deliberately absent: it changes privilege, so it is not routine.
func shellStripCommandPrefixes(fields []string) []string {
	for len(fields) > 0 {
		head := fields[0]
		if shellLooksLikeAssignment(head) {
			fields = fields[1:]
			continue
		}
		switch shellVerbName(head) {
		case "env", "nice", "stdbuf", "time", "timeout", "gtimeout", "command", "builtin", "exec":
			fields = fields[1:]
			for len(fields) > 0 && (strings.HasPrefix(fields[0], "-") || shellLooksLikeDuration(fields[0])) {
				fields = fields[1:]
			}
			continue
		}
		break
	}
	return fields
}

func shellLooksLikeAssignment(token string) bool {
	index := strings.Index(token, "=")
	if index <= 0 || strings.HasPrefix(token, "-") {
		return false
	}
	for _, r := range token[:index] {
		if r != '_' && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func shellLooksLikeDuration(token string) bool {
	if token == "" {
		return false
	}
	digits := 0
	for i, r := range token {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case (r == 's' || r == 'm' || r == 'h' || r == 'd') && i == len(token)-1:
		case r == '.':
		default:
			return false
		}
	}
	return digits > 0
}

// shellVerbName reduces a command word to the program it names, so a path or a
// wrapper cannot hide the verb (`/usr/bin/curl` is curl). Quotes are trimmed
// only at the edges; an interior quote pair is left intact, so a quote-split
// verb never resolves to an allowlisted one.
func shellVerbName(token string) string {
	token = strings.TrimSpace(token)
	token = strings.TrimLeft(token, "({!")
	token = strings.Trim(token, `"'`)
	if index := strings.LastIndex(token, "/"); index >= 0 && index+1 < len(token) {
		token = token[index+1:]
	}
	return strings.ToLower(token)
}

func shellVerbClass(verb string, args []string) string {
	switch verb {
	case "":
		return shellClassUnknown
	case "compound":
		return shellCompoundClass(args)
	case "git":
		return shellGitClass(args)
	case "go":
		return shellGoClass(args)
	case "gofmt":
		// gofmt writes formatted source when -w is present. Its stdout/diff/list
		// modes remain routine reads, but source mutation must leave this lane.
		if shellFlagAbsent(args, "-w") {
			return shellClassRead
		}
		return shellClassUnknown
	case "install.sh":
		return shellClassInstall
	case "make":
		sub, _ := shellFirstOperand(args, nil)
		if sub == "install" {
			return shellClassInstall
		}
		return shellClassUnknown
	case "sed":
		if shellSedIsReadOnly(args) {
			return shellClassRead
		}
		return shellClassUnknown
	case "find":
		return shellFindClass(args)
	case "fd":
		if shellFlagAbsent(args, "-x", "--exec", "-X", "--exec-batch") {
			return shellClassRead
		}
		return shellClassUnknown
	case "awk", "gawk", "nawk":
		for _, arg := range args {
			// AWK's `print > file` and `printf >> file` write from inside the
			// program, beyond shell-level redirection detection. Without an AWK
			// parser, any `>` in the program fails closed; comparisons may be
			// held, but a file write must never enter the routine-read lane.
			if strings.Contains(arg, "system(") || strings.Contains(arg, "close(") || strings.Contains(arg, ">") {
				return shellClassUnknown
			}
		}
		return shellClassRead
	}
	if strings.HasPrefix(verb, "mkfs") {
		return shellClassDestructive
	}
	if shellDestructiveVerbs[verb] {
		return shellClassDestructive
	}
	if shellNetworkVerbs[verb] {
		return shellClassNetwork
	}
	if class, ok := shellPackageManagerClass(verb, args); ok {
		return class
	}
	if subcommands, ok := shellNestedReadVerbs[verb]; ok {
		return shellNestedClass(subcommands, args)
	}
	if shellReadVerbs[verb] {
		return shellClassRead
	}
	return shellClassUnknown
}

func shellCompoundClass(args []string) string {
	sub, rest := shellFirstOperand(args, nil)
	if shellNestedCommandAllowed(compoundReadSubcommands, sub, rest) {
		return shellClassRead
	}
	if shellNestedCommandAllowed(compoundGovernedSubcommands, sub, rest) {
		return shellClassGoverned
	}
	return shellClassUnknown
}

func shellNestedCommandAllowed(commands map[string][]string, sub string, rest []string) bool {
	allowed, ok := commands[sub]
	if !ok {
		return false
	}
	if allowed == nil {
		return true
	}
	next, _ := shellFirstOperand(rest, nil)
	for _, candidate := range allowed {
		if next == candidate {
			return true
		}
	}
	return false
}

func shellNestedClass(subcommands map[string][]string, args []string) string {
	sub, rest := shellFirstOperand(args, nil)
	if sub == "" {
		return shellClassRead
	}
	allowed, ok := subcommands[sub]
	if !ok {
		return shellClassUnknown
	}
	if allowed == nil {
		return shellClassRead
	}
	next, _ := shellFirstOperand(rest, nil)
	for _, candidate := range allowed {
		if next == candidate {
			return shellClassRead
		}
	}
	return shellClassUnknown
}

func shellGitClass(args []string) string {
	sub, rest := shellFirstOperand(args, gitGlobalFlagsWithValue)
	switch {
	case sub == "":
		return shellClassUnknown
	case sub == "clean", sub == "rm", sub == "filter-branch", sub == "filter-repo":
		return shellClassDestructive
	case sub == "reset" && !shellFlagAbsent(rest, "--hard"):
		return shellClassDestructive
	case sub == "update-ref" && !shellFlagAbsent(rest, "-d", "--delete"):
		return shellClassDestructive
	case sub == "branch" && !shellFlagAbsent(rest, "-d", "-D", "--delete"):
		return shellClassDestructive
	case sub == "add":
		return shellClassGoverned
	case sub == "commit":
		if !shellFlagAbsent(rest, "--amend") {
			return shellClassUnknown
		}
		return shellClassGoverned
	case sub == "rebase":
		if !shellFlagAbsent(rest, "-x", "--exec") {
			return shellClassUnknown
		}
		return shellClassGoverned
	case sub == "merge":
		if !shellFlagAbsent(rest, "--ff-only") {
			return shellClassGoverned
		}
		return shellClassUnknown
	case gitNetworkSubcommands[sub]:
		return shellClassNetwork
	case gitReadSubcommands[sub]:
		return shellClassRead
	case sub == "branch":
		// Listing is a read; naming an operand creates or renames a ref. Query
		// flags take a value, and that value is not an operand.
		if operand, _ := shellFirstOperand(rest, gitRefQueryFlagsWithValue); operand != "" {
			return shellClassUnknown
		}
		if shellFlagAbsent(rest, "-m", "-M", "-c", "-C", "--move", "--copy",
			"--set-upstream-to", "-u", "--unset-upstream", "--edit-description") {
			return shellClassRead
		}
		return shellClassUnknown
	case sub == "tag":
		if operand, _ := shellFirstOperand(rest, gitRefQueryFlagsWithValue); operand != "" {
			return shellClassUnknown
		}
		return shellClassRead
	case sub == "worktree", sub == "stash":
		next, _ := shellFirstOperand(rest, nil)
		switch next {
		case "list", "show":
			return shellClassRead
		case "add":
			if sub == "worktree" {
				return shellClassGoverned
			}
		case "remove", "drop", "clear", "prune":
			return shellClassDestructive
		}
		return shellClassUnknown
	case sub == "remote":
		next, _ := shellFirstOperand(rest, nil)
		if next == "" || next == "show" || next == "get-url" {
			return shellClassRead
		}
		return shellClassUnknown
	case sub == "config":
		if !shellFlagAbsent(rest, "--get", "--get-all", "--get-regexp", "--list", "-l") {
			return shellClassRead
		}
		return shellClassUnknown
	case sub == "reflog":
		if !shellFlagAbsent(rest, "expire", "delete") {
			return shellClassDestructive
		}
		return shellClassRead
	}
	return shellClassUnknown
}

func shellGoClass(args []string) string {
	sub, rest := shellFirstOperand(args, nil)
	switch {
	case sub == "":
		return shellClassUnknown
	case sub == "install":
		return shellClassInstall
	case sub == "mod":
		next, _ := shellFirstOperand(rest, nil)
		if next == "graph" || next == "why" || next == "verify" {
			return shellClassRead
		}
		return shellClassUnknown
	case goReadSubcommands[sub]:
		return shellClassRead
	}
	return shellClassUnknown
}

func shellPackageManagerClass(verb string, args []string) (string, bool) {
	sub, rest := shellFirstOperand(args, nil)
	switch verb {
	case "npm", "pnpm", "yarn", "bun", "bunx":
		switch sub {
		case "add", "install", "i", "ci", "create", "dlx", "x":
			return shellClassInstall, true
		case "test":
			return shellClassRead, true
		case "run":
			next, _ := shellFirstOperand(rest, nil)
			switch next {
			case "test", "lint", "build", "typecheck", "check":
				return shellClassRead, true
			}
			return shellClassUnknown, true
		}
		return shellClassUnknown, true
	case "brew":
		switch sub {
		case "install", "upgrade", "reinstall", "tap", "uninstall":
			return shellClassInstall, true
		case "list", "info", "--version", "config", "outdated":
			return shellClassRead, true
		}
		return shellClassUnknown, true
	case "pip", "pip3", "gem", "cargo", "apt", "apt-get", "dnf", "yum":
		switch sub {
		case "install", "upgrade", "uninstall", "remove":
			return shellClassInstall, true
		case "list", "show", "freeze", "search":
			return shellClassRead, true
		case "test", "build", "check", "clippy":
			if verb == "cargo" {
				return shellClassRead, true
			}
		}
		return shellClassUnknown, true
	}
	return "", false
}

func shellFindClass(args []string) string {
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "-delete":
			return shellClassDestructive
		case "-exec", "-execdir", "-ok", "-okdir", "-fls", "-fprint", "-fprint0", "-fprintf":
			return shellClassUnknown
		}
	}
	return shellClassRead
}

// shellSedIsReadOnly rejects in-place editing and the script commands that
// write a file (w/W), execute (e), or read one into the stream (r).
func shellSedIsReadOnly(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			if strings.HasPrefix(arg, "--in-place") {
				return false
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if strings.ContainsRune(arg, 'i') {
				return false
			}
			continue
		}
		if shellSedScriptWrites(arg) {
			return false
		}
	}
	return true
}

func shellSedScriptWrites(script string) bool {
	for i := 0; i < len(script); i++ {
		switch script[i] {
		case 'w', 'W', 'e', 'r':
		default:
			continue
		}
		if i == 0 {
			return true
		}
		switch script[i-1] {
		case ' ', '\t', ';', '{', '}', '/':
			return true
		}
	}
	return false
}

// shellFirstOperand returns the first non-flag argument, skipping flags and the
// values of flags known to take one.
func shellFirstOperand(args []string, flagsWithValue map[string]bool) (string, []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return strings.ToLower(strings.Trim(arg, `"'`)), args[i+1:]
		}
		if flagsWithValue[arg] && !strings.Contains(arg, "=") {
			i++
		}
	}
	return "", nil
}

func shellFlagAbsent(args []string, flags ...string) bool {
	for _, arg := range args {
		name := arg
		if index := strings.Index(arg, "="); index > 0 {
			name = arg[:index]
		}
		for _, flag := range flags {
			if name == flag {
				return false
			}
		}
	}
	return true
}
