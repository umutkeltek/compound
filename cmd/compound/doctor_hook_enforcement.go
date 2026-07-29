package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// doctor_hook_enforcement.go answers the question that made a real fail-open last 48 minutes with
// no signal: the hooks are installed and their configured command exists — but does that binary
// actually ENFORCE?
//
// Every other hook signal doctor reports is a comparison of CONFIG bytes: hook install/drift/absent
// compares the command string in the harness config against the one Compound would write. All of it
// stays green when the path is right and the file at that path is a stub, because a stub that exits
// 0 is indistinguishable from an allow. Silence and success are the same observation.
//
// So this check probes BEHAVIOUR, and picks a probe whose correct answer cannot exist in advance:
// it synthesizes a mutation whose ActionIntent id is a nonce minted for this run, feeds it to the
// configured binary as a fixture, and requires two things back — the hook event's input hash (which
// is derived from that id, so only a binary that really processed the input can produce it) and a
// real PolicyDecision that does NOT allow the mutation. A canned response fails the first; an
// exit-0 no-op fails both.
//
// Read-only like the rest of doctor: the probe runs with COMPOUND_HOME pointed at a throwaway
// directory and its cwd inside it, so the hook records its events there and nothing touches
// operator state.

// hookEnforcementProbeTimeout bounds each probe. Doctor is a routine command; a hook binary that
// cannot answer a single fixture within this budget is reported, not waited for.
const hookEnforcementProbeTimeout = 5 * time.Second

// doctorHookEnforcement is one binary the installed hooks would invoke, and whether it enforces.
type doctorHookEnforcement struct {
	// Harnesses names every configured harness whose hook command resolves to this binary. One
	// row per BINARY, not per harness: the probe costs a subprocess, and three configs almost
	// always point at the same file.
	Harnesses []string `json:"harnesses"`
	Binary    string   `json:"binary"`
	Status    string   `json:"status"` // enforcing | fail_open
	Detail    string   `json:"detail,omitempty"`
}

// buildHookEnforcementRows probes each distinct binary the installed hook configs would invoke.
// It reads the harness configs doctor already located rather than re-deriving paths, so the rows
// describe the same install the hook section describes.
func buildHookEnforcementRows(hooks []doctorHook) ([]doctorHookEnforcement, int) {
	byBinary := map[string][]string{}
	for _, entry := range hooks {
		binaryPath := installedHookBinary(entry.ConfigPath)
		if binaryPath == "" {
			continue
		}
		byBinary[binaryPath] = append(byBinary[binaryPath], entry.Harness)
	}
	binaries := make([]string, 0, len(byBinary))
	for binaryPath := range byBinary {
		binaries = append(binaries, binaryPath)
	}
	sort.Strings(binaries)

	rows := make([]doctorHookEnforcement, 0, len(binaries))
	failing := 0
	for _, binaryPath := range binaries {
		harnesses := byBinary[binaryPath]
		sort.Strings(harnesses)
		detail := probeHookEnforcement(binaryPath)
		status := "enforcing"
		if detail != "" {
			status = "fail_open"
			failing++
		}
		rows = append(rows, doctorHookEnforcement{
			Harnesses: harnesses, Binary: binaryPath, Status: status, Detail: detail,
		})
	}
	return rows, failing
}

// installedHookBinary reads a harness config and returns the binary path Compound's own PreToolUse
// binding would execute, or "" when the config holds no Compound binding. It reads the config that
// is actually on disk — asking the running process for its own path would prove nothing about the
// file the harness will exec.
func installedHookBinary(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &root) != nil {
		return ""
	}
	for _, group := range root.Hooks["PreToolUse"] {
		for _, entry := range group.Hooks {
			if !strings.Contains(entry.Command, "hook pre-tool-use --live") {
				continue
			}
			if binaryPath := hookCommandBinary(entry.Command); binaryPath != "" {
				return binaryPath
			}
		}
	}
	return ""
}

// hookCommandBinary extracts the executable from a hook command line. The installer POSIX-quotes
// the path so a directory containing spaces still execs as one token, so the unquoting has to
// mirror that — including the `'\”` escape — or a quoted path would be probed as a literal
// filename that does not exist and every install would look broken.
func hookCommandBinary(command string) string {
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, "'") {
		if idx := strings.IndexByte(command, ' '); idx >= 0 {
			return command[:idx]
		}
		return command
	}
	var out strings.Builder
	for i := 1; i < len(command); {
		if command[i] != '\'' {
			out.WriteByte(command[i])
			i++
			continue
		}
		if strings.HasPrefix(command[i:], `'\''`) {
			out.WriteByte('\'')
			i += 4
			continue
		}
		return out.String()
	}
	return "" // unterminated quote: not a command we can reason about
}

// probeHookEnforcement returns "" when the binary really enforces, or a one-line reason why it
// does not. The reason is the operator-facing evidence, so it names what came back rather than
// asserting a verdict.
func probeHookEnforcement(binaryPath string) string {
	nonce, err := mintHookProbeNonce()
	if err != nil {
		return "could not mint a probe nonce: " + err.Error()
	}
	sandbox, err := os.MkdirTemp("", "compound-hook-probe-")
	if err != nil {
		return "could not create a probe sandbox: " + err.Error()
	}
	defer os.RemoveAll(sandbox)

	fixturePath := filepath.Join(sandbox, "pre-tool-use.json")
	if err := os.WriteFile(fixturePath, hookEnforcementProbeFixture(nonce), 0o644); err != nil {
		return "could not write the probe fixture: " + err.Error()
	}

	ctx, cancel := context.WithTimeout(context.Background(), hookEnforcementProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "hook", "pre-tool-use", fixturePath)
	cmd.Dir = sandbox
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+filepath.Join(sandbox, "home"))
	out, runErr := cmd.Output()
	if runErr != nil && len(out) == 0 {
		return fmt.Sprintf("running `%s hook pre-tool-use` produced no result: %v", binaryPath, runErr)
	}
	return checkHookEnforcementResponse(out, nonce)
}

// checkHookEnforcementResponse is the pure half: given what the binary printed and the nonce it was
// asked about, decide whether only a real policy gate could have answered.
func checkHookEnforcementResponse(out []byte, nonce string) string {
	var res struct {
		Event struct {
			HookName  string `json:"hook_name"`
			Status    string `json:"status"`
			InputHash string `json:"input_hash"`
		} `json:"event"`
		Produced []json.RawMessage `json:"produced"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return "the hook produced no parseable HookEvent (an exit code alone is not a decision)"
	}
	if res.Event.HookName != "PreToolUse" {
		return fmt.Sprintf("the hook reported hook_name %q, want PreToolUse", res.Event.HookName)
	}
	wantHash := hookProbeDigest(nonce)
	if res.Event.InputHash != wantHash {
		return fmt.Sprintf("the hook's input_hash is %q, but this run's probe hashes to %q — the answer was not derived from the input",
			res.Event.InputHash, wantHash)
	}
	for _, raw := range res.Produced {
		var decision envelope.PolicyDecision
		if json.Unmarshal(raw, &decision) != nil || decision.SchemaVersion != envelope.SVPolicyDecision {
			continue
		}
		if policy.Allowed(decision) {
			return fmt.Sprintf("the gate ALLOWED a destructive probe mutation (outcome %q)", decision.Outcome)
		}
		return ""
	}
	return "the hook emitted no PolicyDecision for a destructive mutation, so nothing was gated"
}

// hookEnforcementProbeFixture builds the mutation the gate must refuse. The ActionIntent id is the
// run's nonce — that is what makes the expected answer impossible to precompute, because the hook
// event's input hash is derived from it. The harness is fixed rather than taken from the config
// entry: the probe is about the BINARY, and pinning one known harness keeps a lane-specific harness
// name from turning a real fail-open into an unknown.
func hookEnforcementProbeFixture(nonce string) []byte {
	fixture := map[string]any{
		"hook":    "pre-tool-use",
		"harness": "claude_code",
		"work_id": "work_doctor_probe",
		"action": map[string]any{
			"schema_version": envelope.SVActionIntent,
			"id":             nonce,
			"created_at":     "2026-01-01T00:00:00Z",
			"producer":       map[string]any{"kind": "hook", "name": "compound"},
			"ids":            map[string]any{"work_id": "work_doctor_probe"},
			"scope":          map[string]any{},
			"authority":      map[string]any{"source_class": "runtime"},
			"privacy":        "private",
			"risk":           "tier2",
			"evidence_refs":  []string{},
			"uncertainty":    map[string]any{"level": "known"},
			"actor":          "claude_code",
			"capability":     "filesystem.delete",
			"action":         "delete_tree",
			"target":         map[string]any{"kind": "path", "path": "build/cache"},
			"mutation_class": "destructive",
			"reversibility":  "hard",
			"blast_radius":   "repo",
		},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func mintHookProbeNonce() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "act_doctorprobe" + hex.EncodeToString(raw), nil
}

func hookProbeDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// printHookEnforcement renders the rows. A passing row stays to one line — a health surface that
// prints a wall of green trains the operator to skip it, and this is the section that must be read
// on the day it is not green.
func printHookEnforcement(stdout io.Writer, rep doctorReport) {
	fmt.Fprintln(stdout, "\n## hook enforcement (does the hook binary actually gate?)")
	if len(rep.HookEnforcement) == 0 {
		fmt.Fprintln(stdout, "- (no installed hook command to probe)")
		return
	}
	for _, row := range rep.HookEnforcement {
		harnesses := strings.Join(row.Harnesses, ",")
		if row.Status == "enforcing" {
			fmt.Fprintf(stdout, "- ok   %-24s %s (refused a probe mutation it had never seen)\n", harnesses, row.Binary)
			continue
		}
		fmt.Fprintf(stdout, "- FAIL %-24s %s\n", harnesses, row.Binary)
		fmt.Fprintf(stdout, "    %s\n", row.Detail)
	}
	if rep.HookEnforcementFailing > 0 {
		fmt.Fprintln(stdout, "  -> the installed binary answers but does not enforce; reinstall it (`make install`) and re-run this check")
	}
}
