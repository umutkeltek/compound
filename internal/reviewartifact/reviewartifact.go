// Package reviewartifact is the one deep module behind every owner-review
// artifact compound writes under COMPOUND_HOME/reviews/<area>/.
//
// Across the tree, ~17 commands turn a candidate (or a doctor/snapshot finding)
// into a reviewed JSON+Markdown artifact that mutates no owner file and promotes
// no claim. Before this module each of those sites re-implemented the identical
// mechanism — target hashing, id derivation, slugging, ref de-duplication, and
// the MkdirAll+marshal+write pair — under renamed private helpers. That is the
// shallow-family shape: the interface (a Record* call) was nearly as wide as the
// implementation, and a change to the artifact format meant editing up to 17
// files.
//
// reviewartifact gives that mechanism a single home so each call site keeps only
// what genuinely varies: the area, the domain payload, the markdown body, and any
// extra draft files (.review.yaml, manifests) it writes alongside the core pair.
// It mirrors the already-deep internal/sourceobs + internal/writeback shape:
// many thin callers, one shared core.
//
// Scope discipline: this module owns only the artifact mechanism. It performs no
// gating, candidate adjudication, or promotion — those remain with internal/policy,
// internal/writeback, and the domain packages.
package reviewartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Paths is the core json+md pair a review artifact always writes. Callers that
// emit extra drafts (a .review.yaml, a manifest) write those themselves and
// carry their own wider Paths type.
type Paths struct {
	PlanPath   string
	ReviewPath string
}

// Dir resolves and creates COMPOUND_HOME/reviews/<area>/.
func Dir(homePath, area string) (string, error) {
	if strings.TrimSpace(homePath) == "" {
		return "", fmt.Errorf("COMPOUND_HOME required")
	}
	dir := filepath.Join(homePath, "reviews", area)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Write persists the json+md core pair of an owner-review artifact:
// reviews/<area>/<id>.json (payload marshaled with the given indent plus a
// trailing newline) and reviews/<area>/<id>.md (markdown verbatim). The indent
// is a caller parameter on purpose — different artifact families historically
// serialize with " " or "  ", and Write preserves each one exactly.
func Write(homePath, area, id string, payload any, indent, markdown string) (Paths, error) {
	if strings.TrimSpace(id) == "" {
		return Paths{}, fmt.Errorf("review artifact id required")
	}
	dir, err := Dir(homePath, area)
	if err != nil {
		return Paths{}, err
	}
	paths := Paths{
		PlanPath:   filepath.Join(dir, id+".json"),
		ReviewPath: filepath.Join(dir, id+".md"),
	}
	data, err := json.MarshalIndent(payload, "", indent)
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.PlanPath, append(data, '\n'), 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ReviewPath, []byte(markdown), 0o644); err != nil {
		return paths, err
	}
	return paths, nil
}

// HashTarget reports whether an owner target exists and its content hash, so a
// review artifact can record what it reviewed against. A missing path (or empty
// path) is the no-op (false, "", nil) — not an error. A file hashes its bytes; a
// directory hashes the sorted set of relative-path:content-hash entries, so the
// result is stable regardless of walk order. The file branch is identical to the
// file-only hashers it replaces, so directory-unaware callers are unaffected.
func HashTarget(path string) (bool, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, "", err
		}
		sum := sha256.Sum256(data)
		return true, hex.EncodeToString(sum[:]), nil
	}
	var entries []string
	if err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, filepath.ToSlash(rel)+":"+hex.EncodeToString(sum[:]))
		return nil
	}); err != nil {
		return false, "", err
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return true, hex.EncodeToString(sum[:]), nil
}

// Digest12 is the stable short digest used in artifact ids: the first 12 hex
// chars of sha256(value).
func Digest12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

// ID derives a deterministic artifact id: prefix + Digest12 of the parts joined
// by "|". Replaying the same inputs yields the same id.
func ID(prefix string, parts ...string) string {
	return prefix + Digest12(strings.Join(parts, "|"))
}

// Slugify lowercases value and collapses every run of non-[a-z0-9] into a single
// dash, trimming leading/trailing dashes. A maxLen <= 0 means no limit; otherwise
// the result is truncated to maxLen and re-trimmed so it never ends in a dash.
func Slugify(value string, maxLen int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if maxLen > 0 && len(out) > maxLen {
		out = strings.Trim(out[:maxLen], "-")
	}
	return out
}

// Timestamp formats t as a UTC RFC3339 string — the created_at value on every
// artifact.
func Timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// UniqueSorted trims, de-duplicates, and sorts a list of refs. Use it where the
// artifact records refs in a canonical (order-independent) order.
func UniqueSorted(values []string) []string {
	out := dedupe(values)
	sort.Strings(out)
	return out
}

// Unique trims and de-duplicates while preserving first-seen order. Use it where
// the artifact's ref order is meaningful.
func Unique(values []string) []string {
	return dedupe(values)
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// FirstNonEmpty returns the first trimmed non-empty value, or "".
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if v := strings.TrimSpace(value); v != "" {
			return v
		}
	}
	return ""
}
