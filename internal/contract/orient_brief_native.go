package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
)

// A checkout the projection does not cover still has facts: the ones Compound
// itself recorded for it. A new home has no projection at all, so without
// them the brief stays empty however many sessions end in the checkout. A
// projection entry always wins; the two sources are never merged.

const (
	orientBriefSessionFileLimit = 3
	// The reader runs on every orient of an uncovered checkout, so it reads a
	// fixed window however old the home is: the two newest receipt segments
	// (the event store rolls a segment at 8 MiB). Older receipts are not shown.
	orientBriefReceiptSegments = 2
)

// orientBriefNativeFacts renders the facts of a checkout with no projection
// entry: its last session, and the last session that changed files when that
// is an older one. At most four lines, so inside orientBriefFactLineLimit. A
// reader that fails says so on its own line, so breakage never reads as
// "nothing recorded".
func orientBriefNativeFacts(in OrientBriefInput, head string) []string {
	var facts []string
	if root, ok := briefCheckoutRoot(in.Input.Cwd); ok && strings.TrimSpace(in.HomePath) != "" {
		facts = briefLastSessionLines(in.HomePath, root)
	}
	if len(facts) == 0 {
		return []string{head + ": " + orientBriefNoFactsSuffix}
	}
	return append([]string{head}, facts...)
}

// briefCheckoutRoot returns the checkout cwd belongs to, symlink-resolved: the
// nearest directory at or above it that holds a .git entry, or cwd itself
// outside Git.
func briefCheckoutRoot(cwd string) (string, bool) {
	start, err := canonicalWorkspacePath(cwd)
	if err != nil {
		return "", false
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		if filepath.Dir(dir) == dir {
			return start, true
		}
	}
}

// briefPathInCheckout reports whether path lies in the checkout at root (already
// canonical) and not in another checkout nested below it: a linked worktree or
// a clone inside the tree is a different checkout with its own facts. A
// relative path never matches, because it would resolve against this process.
func briefPathInCheckout(root, path string) bool {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) || !workspacePathContains(root, path) {
		return false
	}
	dir, err := canonicalWorkspacePath(path)
	if err != nil {
		return false
	}
	for dir != root && filepath.Dir(dir) != dir {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return false
		}
		dir = filepath.Dir(dir)
	}
	return dir == root
}

func briefLastSessionLines(homePath, root string) []string {
	newest, lastChanged, err := newestSessionReceipts(homePath, root)
	if err != nil {
		return []string{"- " + briefLine("Session receipts unavailable: "+err.Error())}
	}
	if newest == nil {
		return nil
	}
	lines := briefSessionLines("Last session here", *newest)
	// A session that only asked a question would otherwise hide the session
	// that did the work: the trail an agent needs is the older one.
	if lastChanged != nil && lastChanged.ID != newest.ID {
		lines = append(lines, briefSessionLines("Last session that changed files", *lastChanged)...)
	}
	return lines
}

// newestSessionReceipts returns, of the session receipts whose surface (the
// session's cwd) lies in the checkout at root, the newest one and the newest
// one that changed files; either is nil when there is none, and they are the
// same receipt when the newest session changed files. Scan returns entries in
// id order, so this takes one pass over the window
// (orientBriefReceiptSegments); a record that names no surface is skipped
// before it is decoded.
func newestSessionReceipts(homePath, root string) (newest, lastChanged *envelope.Receipt, err error) {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return nil, nil, err
	}
	entries, err := store.Scan(eventstore.ScanOptions{Prefixes: []string{"rec_"}, RegularOnly: true, RecentSegments: orientBriefReceiptSegments})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil // a home with no event store yet has no receipt
	} else if err != nil {
		return nil, nil, err
	}
	inCheckout := map[string]bool{}
	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil || !bytes.Contains(record.Bytes, []byte(`"surface"`)) {
			continue
		}
		var r envelope.Receipt
		if json.Unmarshal(record.Bytes, &r) != nil || strings.TrimSpace(r.IDs.SessionID) == "" {
			continue
		}
		changed := len(r.StateDelta.FilesChanged) > 0
		if !receiptIsNewer(r, newest) && !(changed && receiptIsNewer(r, lastChanged)) {
			continue
		}
		match, known := inCheckout[r.Scope.Surface]
		if !known {
			match = briefPathInCheckout(root, r.Scope.Surface)
			inCheckout[r.Scope.Surface] = match
		}
		if !match {
			continue
		}
		if receiptIsNewer(r, newest) {
			newest = &r
		}
		if changed && receiptIsNewer(r, lastChanged) {
			lastChanged = &r
		}
	}
	return newest, lastChanged, nil
}

// receiptIsNewer orders receipts by creation time, then id, so two receipts
// minted in the same second still have one newest.
func receiptIsNewer(r envelope.Receipt, than *envelope.Receipt) bool {
	return than == nil || r.CreatedAt > than.CreatedAt || (r.CreatedAt == than.CreatedAt && r.ID > than.ID)
}

// briefSessionLines renders one session receipt. Every part is read from the
// receipt, and a part the receipt lacks is left out, never guessed. The
// transcript path gets a line of its own: a real path is long (a harness
// encodes the whole checkout path into it), and on a shared line it was the
// part that got cut.
func briefSessionLines(label string, r envelope.Receipt) []string {
	summary := r.StateDelta.Summary
	var parts []string
	if at := briefSessionTime(summary, r.CreatedAt); at != "" {
		parts = append(parts, at)
	}
	if fields := strings.Fields(sessionSummaryField(summary, "tool_calls")); len(fields) > 0 {
		if n, err := strconv.Atoi(fields[0]); err == nil && n >= 0 {
			parts = append(parts, briefCount(n, "tool call"))
		}
	}
	if files := r.StateDelta.FilesChanged; len(files) > 0 {
		var names []string
		for i, file := range files {
			if i == orientBriefSessionFileLimit {
				names = append(names, fmt.Sprintf("+%d more", len(files)-i))
				break
			}
			names = append(names, filepath.Base(file))
		}
		parts = append(parts, briefCount(len(files), "file")+" changed ("+strings.Join(names, ", ")+")")
	}
	if len(parts) == 0 {
		parts = append(parts, "receipt "+r.ID)
	}
	lines := []string{"- " + briefLine(label+": "+strings.Join(parts, ", "))}
	if transcript := sanitizeBriefText(sessionSummaryField(summary, "transcript")); transcript != "" {
		lines = append(lines, briefTranscriptLine(transcript, r.ID))
	}
	return lines
}

// briefTranscriptLine names the session's transcript. A path cut short
// resolves to nothing, so one that does not fit the line whole is replaced by
// the command that prints the full receipt.
func briefTranscriptLine(transcript, receiptID string) string {
	const prefix = "  transcript: "
	if len(prefix)+len(transcript) > orientBriefLineMaxBytes {
		return prefix + clipBytes("path too long for the brief; compound event show "+receiptID, orientBriefLineMaxBytes-len(prefix))
	}
	return prefix + transcript
}

// briefSessionTime returns when the session last did something: the end of
// the receipt's transcript window, or the receipt's own creation time when
// the window has no readable end.
func briefSessionTime(summary, createdAt string) string {
	_, end, _ := strings.Cut(sessionSummaryField(summary, "window"), " -> ")
	for _, stamp := range []string{end, createdAt} {
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp)); err == nil {
			return at.UTC().Format(orientBriefStampLayout)
		}
	}
	return ""
}

// sessionSummaryField returns the value of one "key: value" line of a session
// receipt summary, or "" when the summary has no such line.
func sessionSummaryField(summary, key string) string {
	for _, line := range strings.Split(summary, "\n") {
		if value, ok := strings.CutPrefix(line, key+": "); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func briefCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
