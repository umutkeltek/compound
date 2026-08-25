package hookplan

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// OmpDeriveSession is the Go-side mirror of the TS shim's
// `deriveOmpSession()` (internal/hookplan/omp_hook_shim.ts). It MUST stay
// in lockstep — the TS runs at hook time; this Go helper is the unit-testable
// companion that lets the F6 falsifier pin both sides to the same algorithm
// without booting bun/node. The contract:
//
//   - homedir is the operator home (TS: `homedir()`, Go: caller-resolved).
//   - cwd is the process's current working directory (process.cwd() / os.Getwd).
//   - Returned TranscriptPath is the absolute path to the newest *.jsonl
//     file under <homedir>/.omp/agent/sessions/<cwd-slug>/ whose header
//     `{"type":"session",...,"cwd":<abs>}` line carries the same cwd. Empty
//     string when no match (callers MUST treat that as "do not pass
//     transcript_path to scanSessionTranscript", never as a probing fallback).
//   - Returned SessionID is the omp-embedded uuid when found; otherwise a
//     synthetic per-process id shaped like the pre-slice-3 fallback so
//     SessionStart and SessionEnd share one correlation key.
//
// Slug derivation: strip a leading `/`, replace every `/` with `-`, replace
// any non-[A-Za-z0-9._-] with `-`, and wrap in `--`. Examples:
//
//	/private/home/operator/projects/sample -> --private-home-operator-projects-sample--
//	/tmp/x                                -> --tmp-x--
func OmpDeriveSession(homedir, cwd string) (transcriptPath, sessionID string, ok bool) {
	slug := ompSlugFromCwd(cwd)
	dir := filepath.Join(homedir, ".omp", "agent", "sessions", slug)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ompFallbackID(), false
	}
	var newestPath string
	var newestMtime int64
	var newestID string
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		full := filepath.Join(dir, name)
		// Header peek: only the first ~2 KB; we just need the session line.
		f, err := os.Open(full)
		if err != nil {
			continue
		}
		var headBytes [2048]byte
		n, _ := f.Read(headBytes[:])
		_ = f.Close()
		header := extractSessionHeader(headBytes[:n])
		if header == nil || header.Cwd != cwd {
			continue
		}
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() <= newestMtime {
			continue
		}
		newestMtime = info.ModTime().UnixNano()
		newestPath = full
		newestID = header.ID
	}
	if newestPath == "" {
		return "", ompFallbackID(), false
	}
	id := newestID
	if id == "" {
		id = ompFallbackID()
	}
	return newestPath, id, true
}

// ompFallbackID returns the synthetic id the TS shim uses when discovery
// fails. Cross-process uniqueness is enough for SessionStart/SessionEnd
// correlation; the parser never keys off this value (it keys off the
// transcript_path if present, otherwise no skill evidence is collected).
func ompFallbackID() string {
	// os.Getpid() is not in the signature; callers may pass through their
	// own pid if they need a true per-process id. For the test helper we
	// return a stable shape that matches the shim's synthetic id.
	return "omp-fallback-no-match"
}

// ompSlugFromCwd reproduces the TS slug derivation (see file header).
func ompSlugFromCwd(cwd string) string {
	s := strings.TrimLeft(cwd, "/")
	s = strings.ReplaceAll(s, "/", "-")
	// Replace any non-[A-Za-z0-9._-] with "-"
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return "--" + b.String() + "--"
}

type ompHeaderProbe struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
}

func extractSessionHeader(buf []byte) *ompHeaderProbe {
	// omp's session line is on line 2 (line 1 is usually a "title" or other
	// benign entry). Be liberal: scan the first few non-empty lines for a
	// JSON object whose `type` is "session".
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	seen := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		seen++
		if seen > 8 {
			return nil
		}
		if line[0] != '{' {
			continue
		}
		var probe ompHeaderProbe
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Type != "session" {
			continue
		}
		if probe.ID == "" || probe.Cwd == "" {
			continue
		}
		return &probe
	}
	return nil
}
