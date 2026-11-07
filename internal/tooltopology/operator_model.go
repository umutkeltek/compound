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
	// MaxOperatorModelBytes caps the served content. Content past the cap is
	// cut at the last newline that fits and named by a truncation line.
	MaxOperatorModelBytes = 3072

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

// renderOperatorModel trims trailing whitespace, keeps at most
// MaxOperatorModelBytes of content, and states how many bytes it left out.
// unread counts file bytes past the read limit; they are omitted too.
func renderOperatorModel(raw string, unread int64) string {
	content := strings.TrimRight(raw, " \t\r\n\v\f")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if len(content) <= MaxOperatorModelBytes && unread == 0 {
		return operatorModelHeader + "\n" + content
	}
	kept := content
	if len(kept) > MaxOperatorModelBytes {
		// The last newline at or before the cap ends the kept text; one line
		// longer than the cap falls back to a byte cut on a rune boundary.
		if nl := strings.LastIndexByte(content[:MaxOperatorModelBytes+1], '\n'); nl > 0 {
			kept = content[:nl]
		} else {
			cut := MaxOperatorModelBytes
			for cut > 0 && !isRuneStart(content[cut]) {
				cut--
			}
			kept = content[:cut]
		}
		kept = strings.TrimRight(kept, " \t\r\n\v\f")
	}
	omitted := int64(len(content)-len(kept)) + unread
	return operatorModelHeader + "\n" + kept + "\n(truncated: " + strconv.FormatInt(omitted, 10) +
		" bytes omitted; full text in " + OperatorModelPath + ")"
}

// ServeSessionStart is the one session-start payload every harness receives:
// the door map, then the operator model one blank line apart when a model
// exists. The hook and `compound doors serve` both call it, so a lane that
// only gets `doors serve` (no session hook) still receives the operator model.
func ServeSessionStart(home string, now time.Time, ttl time.Duration) string {
	doorMap := ServeDoorMap(home, now, ttl)
	if model := ServeOperatorModel(home); model != "" && doorMap != "" {
		return doorMap + "\n\n" + model
	} else if model != "" {
		return model
	}
	return doorMap
}
