package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/hookplan"
	"compound/internal/policy"
)

// DoctorExpectedHarness uses the installer as the source of canonical runtime
// harness identity for a configured harness row.
func DoctorExpectedHarness(installerHarness string) string {
	command := hookplan.HookCommand("compound", installerHarness)
	_, runtime, ok := strings.Cut(command, " --harness ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(runtime)
}

// ValidateDoctorProbeArgs permits only the installed live PreToolUse argv shape.
// A probe with output-path or home overrides must refuse before process start.
func ValidateDoctorProbeArgs(args []string, enforced bool, expectedHarness string) string {
	if len(args) != 5 && len(args) != 6 {
		return "unsupported installed live hook arguments; probe refused before execution"
	}
	if args[0] != "hook" || args[1] != "pre-tool-use" || args[2] != "--live" || args[3] != "--harness" || args[4] == "" || strings.HasPrefix(args[4], "-") {
		return "unsupported installed live hook arguments; probe refused before execution"
	}
	if expectedHarness == "" || args[4] != expectedHarness {
		return "installed live hook harness does not match the configured harness row"
	}
	// The one optional trailing flag is the posture: --enforce for an enforced
	// lane, --enforce-governed for one that blocks only inside governed runs,
	// --spool for a watch-only one (its records arrive through drain).
	if len(args) == 6 && args[5] != "--enforce" && args[5] != hookplan.EnforceGovernedFlag && args[5] != "--spool" {
		return "unsupported installed live hook arguments; probe refused before execution"
	}
	if (len(args) == 6 && args[5] == "--enforce") != enforced {
		return "installed live hook enforcement posture does not match arguments"
	}
	return ""
}

// DoctorProbeEnvironment prevents inherited Compound routing from escaping the
// disposable probe home while retaining ordinary process environment like PATH.
func DoctorProbeEnvironment(homePath string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "COMPOUND_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "COMPOUND_HOME="+homePath)
}

// DoctorLiveProbeInput carries only the native fields needed to exercise the
// installed PreToolUse route and bind its action to a nonce target.
func DoctorLiveProbeInput(nonce, targetPath string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"hook_event_name":      "PreToolUse",
		"session_id":           "sess_" + nonce,
		"cwd":                  targetPath,
		"tool_name":            "apply_patch",
		"tool_schema_revision": "builtin:apply_patch",
		"tool_input":           map[string]any{"command": "doctor-unparseable-security-probe-" + nonce},
		"work_id":              "work_doctor_probe",
	})
}

func DoctorExpectedExit(posture, outcome string) (int, bool) {
	switch posture {
	case "observation_only":
		if outcome == "hold" || outcome == "deny" {
			return 0, true
		}
	case "enforced":
		if outcome == "hold" {
			return 3, true
		}
		if outcome == "deny" {
			return 4, true
		}
	}
	return 0, false
}

// InspectDoctorLiveRoute checks the installed route's native response and,
// for a --spool route, runs the same binary's `hook drain --once` in the probe
// home so the evidence check that follows sees the records the drain writes.
// A spool route that answers but never records therefore still fails the probe.
func InspectDoctorLiveRoute(ctx context.Context, out []byte, binaryPath string, args []string, dir string, env []string) string {
	if detail := InspectDoctorLiveResponse(out); detail != "" || len(args) == 0 || args[len(args)-1] != "--spool" {
		return detail
	}
	drain := exec.CommandContext(ctx, binaryPath, "hook", "drain", "--once")
	drain.Dir, drain.Env = dir, env
	if drained, err := drain.CombinedOutput(); err != nil {
		return fmt.Sprintf("the spool route answered but `hook drain --once` failed: %v: %s", err, strings.TrimSpace(string(drained)))
	}
	return ""
}

// InspectDoctorLiveResponse verifies that the installed route returned a
// native PreToolUse response before its persisted evidence is trusted.
func InspectDoctorLiveResponse(out []byte) string {
	var response struct {
		HookName           string `json:"hook_name"`
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return "the exact installed live route produced no parseable native HookEvent response"
	}
	if response.HookName != "PreToolUse" && response.HookSpecificOutput.HookEventName != "PreToolUse" {
		return "the exact installed live route did not report PreToolUse"
	}
	return ""
}

// InspectDoctorLiveEvidence binds the nonce target to a non-allowing decision
// and HookEvent in the isolated home. The target must not have been written.
func InspectDoctorLiveEvidence(homePath, targetPath string) (string, string) {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return "", "could not open isolated live-probe evidence: " + err.Error()
	}
	entries, err := store.Scan(eventstore.ScanOptions{
		Prefixes:    []string{"act_", "pol_", "hook_"},
		RegularOnly: true,
	})
	if err != nil {
		return "", "could not scan isolated live-probe evidence: " + err.Error()
	}
	var action envelope.ActionIntent
	var decisions []envelope.PolicyDecision
	var events []envelope.HookEvent
	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			return "", "could not read isolated live-probe evidence: " + err.Error()
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if json.Unmarshal(record.Bytes, &header) != nil {
			continue
		}
		switch header.SchemaVersion {
		case envelope.SVActionIntent:
			var candidate envelope.ActionIntent
			if json.Unmarshal(record.Bytes, &candidate) == nil && candidate.Target.Path == targetPath {
				action = candidate
			}
		case envelope.SVPolicyDecision:
			var decision envelope.PolicyDecision
			if json.Unmarshal(record.Bytes, &decision) == nil {
				decisions = append(decisions, decision)
			}
		case envelope.SVHookEvent:
			var event envelope.HookEvent
			if json.Unmarshal(record.Bytes, &event) == nil {
				events = append(events, event)
			}
		}
	}
	if action.ID == "" {
		return "", "the live route persisted no ActionIntent for this run's nonce target"
	}
	outcome := ""
	for _, decision := range decisions {
		if decision.ActionIntentID != action.ID {
			continue
		}
		if policy.Allowed(decision) {
			return "", fmt.Sprintf("the gate ALLOWED the live destructive probe (outcome %q)", decision.Outcome)
		}
		outcome = decision.Outcome
		break
	}
	if outcome == "" {
		return "", "the live route persisted no PolicyDecision for its probe ActionIntent"
	}
	sum := sha256.Sum256([]byte(action.ID))
	wantHash := hex.EncodeToString(sum[:])
	for _, event := range events {
		if event.HookName == "PreToolUse" && event.InputHash == wantHash {
			if _, err := os.Lstat(targetPath); !errors.Is(err, os.ErrNotExist) {
				return "", "the live probe unexpectedly created its write target"
			}
			return outcome, ""
		}
	}
	return "", "the live route persisted no HookEvent bound to this run's probe ActionIntent"
}
