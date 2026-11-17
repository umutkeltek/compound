package tooltopology

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Operator model: the operator's standing preferences and decisions, written
// once as private runtime state under COMPOUND_HOME and served to every
// harness at session start beside the door map, so no harness needs its own
// partial copy. It is never tracked in a repository.
const (
	// OperatorModelPath is the file, relative to COMPOUND_HOME.
	OperatorModelPath = "config/operator-model.md"
	// MaxOperatorModelBytes is the session-start budget for the model. The
	// whole start text must stay under the 10,000 bytes a harness keeps from
	// hook output (it replaces a longer one with a 2,000-byte preview). The
	// parts: the ask-first line (about 450), this block, the door map (capped
	// at MaxServedBytes) and the compact laws (about 700). 5120 leaves the
	// rest of the budget at its caps. The budget only ever leaves out
	// background sections: every rule section is served whole, even past it,
	// and a section left out is named, never cut.
	MaxOperatorModelBytes = 5120

	operatorModelHeader  = "## Operator model (private, from " + OperatorModelPath + ")"
	maxOperatorModelRead = 1 << 20
)

// ServeOperatorModel returns the operator-model block for a session start, or
// "" when the file is absent, unreadable or blank. Unlike the door map it is
// silent when missing: most installs never write one, so absence is the
// normal state, not a gap to announce. The output is a pure function of the
// file bytes.
func ServeOperatorModel(home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(home, filepath.FromSlash(OperatorModelPath)))
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxOperatorModelRead))
	if err != nil {
		return ""
	}
	unread := info.Size() - int64(len(data))
	if unread < 0 {
		unread = 0
	}
	return renderOperatorModel(string(data), unread)
}

// operatorModelSection is one "## " section of the model, or the text before
// the first one.
type operatorModelSection struct {
	heading string
	text    string
	rule    bool
}

// backgroundHeadingWords open a section heading that describes rather than
// rules: who he is, background, notes. Every other section, including one
// with a heading this list does not know, is a rule and is always served
// whole, so a miss serves more, never less.
var backgroundHeadingWords = []string{"who", "about", "background", "context", "notes", "history", "examples", "reference"}

func isBackgroundHeading(heading string) bool {
	words := strings.FieldsFunc(strings.ToLower(heading), func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	})
	if len(words) == 0 {
		return false
	}
	for _, w := range backgroundHeadingWords {
		if words[0] == w {
			return true
		}
	}
	return false
}

func splitOperatorModel(content string) []operatorModelSection {
	var out []operatorModelSection
	current := operatorModelSection{rule: true}
	for _, line := range strings.SplitAfter(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			if current.text != "" {
				out = append(out, current)
			}
			heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			current = operatorModelSection{heading: heading, rule: !isBackgroundHeading(heading)}
		}
		current.text += line
	}
	if current.text != "" {
		out = append(out, current)
	}
	return out
}

// renderOperatorModel trims trailing whitespace and serves the model whole
// when it fits MaxOperatorModelBytes. When it does not, it keeps every rule
// section (and the text before the first section) whole, adds background
// sections in file order while they fit, and names each section it left
// out, with its size, in one closing line. It never cuts inside a section.
// unread counts file bytes past the read limit; the closing line names them
// too.
func renderOperatorModel(raw string, unread int64) string {
	content := strings.TrimRight(raw, " \t\r\n\v\f")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if len(content) <= MaxOperatorModelBytes && unread == 0 {
		return operatorModelHeader + "\n" + content
	}
	sections := splitOperatorModel(content)
	used := 0
	for _, section := range sections {
		if section.rule {
			used += len(section.text)
		}
	}
	keep := make([]bool, len(sections))
	var left []string
	for i, section := range sections {
		switch {
		case section.rule:
			keep[i] = true
		case used+len(section.text) <= MaxOperatorModelBytes:
			keep[i] = true
			used += len(section.text)
		default:
			left = append(left, strconv.Quote(section.heading)+" ("+strconv.Itoa(len(section.text))+" bytes)")
		}
	}
	var b strings.Builder
	for i, section := range sections {
		if keep[i] {
			b.WriteString(section.text)
		}
	}
	out := operatorModelHeader + "\n" + strings.TrimRight(b.String(), " \t\r\n\v\f")
	if unread > 0 {
		left = append(left, strconv.FormatInt(unread, 10)+" bytes past the "+strconv.Itoa(maxOperatorModelRead)+"-byte read limit")
	}
	if len(left) == 0 {
		return out
	}
	return out + "\n(not served here, to keep the session start within " + strconv.Itoa(MaxOperatorModelBytes) +
		" bytes: " + strings.Join(left, ", ") + "; full text in " + OperatorModelPath + ")"
}

// ServeSessionStart is the one session-start payload every harness receives:
// the operator model first, then the door map, one blank line apart when both
// exist. The operator's rules lead because a harness may keep only the start
// of a long hook output. The hook and `compound doors serve` both call it, so a
// lane that only gets `doors serve` (no session hook) still receives the
// operator model.
func ServeSessionStart(home string, now time.Time, ttl time.Duration) string {
	doorMap := ServeDoorMap(home, now, ttl)
	if model := ServeOperatorModel(home); model != "" && doorMap != "" {
		return model + "\n\n" + doorMap
	} else if model != "" {
		return model
	}
	return doorMap
}
