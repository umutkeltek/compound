package hook

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/contract"
	"compound/internal/hookplan"
)

// SessionPackEnv overrides the session-start pack mode for one hook process:
// "full" builds and injects the ContextPack exactly as before, "minimal" serves
// only the law line, the door map and the pointer. Any other value (or none)
// leaves the harness default from hookplan.DefaultSessionPack. The hook process
// inherits the harness environment, so an A/B run sets it per arm.
const SessionPackEnv = "COMPOUND_SESSION_PACK"

// sessionOrientationOnDemand marks a session whose start served no ContextPack.
const sessionOrientationOnDemand = "on_demand"

// sessionPackMode decides whether this session start builds the ContextPack.
//
// Only a live session start can be minimal: a fixture replay keeps the full pack,
// so recorded fixtures do not depend on the environment of the test process.
// Without an override, an enforced hook keeps full whatever its harness, because
// the enforced posture is the unattended one.
func sessionPackMode(fx Fixture) string {
	if !fx.Live {
		return hookplan.SessionPackFull
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SessionPackEnv))) {
	case hookplan.SessionPackFull:
		return hookplan.SessionPackFull
	case hookplan.SessionPackMinimal:
		return hookplan.SessionPackMinimal
	}
	if fx.Enforced {
		return hookplan.SessionPackFull
	}
	return hookplan.DefaultSessionPack(fx.Harness)
}

// sessionStartOnDemand is the minimal session start. The session is recorded as
// before (HookEvent with session id, harness and input hash; the session's
// orientation marker), but no ContextRequest, EffectiveContract or ContextPack is
// built, minted or injected. Measured use of the served pack over 76 sessions was
// almost all door lines, and the blind with-and-without run showed no quality
// gain, so the agent asks for the pack with `compound orient` when a task needs it.
func sessionStartOnDemand(name string, fx Fixture, deps Deps, now time.Time) (Result, error) {
	mode := hookRequestedMode(name, fx)
	ev := newCapabilityEvent(name, fx, fx.Harness, deps, contextHookInputHash(name, fx, mode), fx.AdapterSideEffects, now)
	if strings.TrimSpace(deps.HomePath) != "" {
		// NON-FATAL like the full path's marker: losing it only degrades the
		// per-prompt pointer to its "cannot tell" wording.
		_ = recordSessionOrientation(deps.HomePath, fx.SessionID, ev.Header.ID, sessionOrientationOnDemand, now)
	}
	return Result{
		Event:         ev,
		Summary:       contract.TierZeroLawLine(fx.Cwd),
		ContextOutput: joinSessionStartParts(sessionStartDoorMap(fx, deps, now), onDemandPackPointer(fx)),
	}, nil
}

// onDemandPackPointer is the one line that replaces the pack body: the exact
// command that serves it for this harness and repository.
func onDemandPackPointer(fx Fixture) string {
	harnessName := safeMD(fx.Harness)
	if harnessName == "" {
		harnessName = "<harness>"
	}
	cwd := pointerShellWord(safeMD(fx.Cwd))
	if cwd == "" {
		cwd = "<repository>"
	}
	return fmt.Sprintf("Context pack is on demand: run `compound orient --harness %s --cwd %s --task \"<task>\"` before a non-trivial task.", harnessName, cwd)
}

// pointerShellWord single-quotes a path the agent will paste into a shell when it
// holds a character the shell would split or expand.
func pointerShellWord(s string) string {
	if s == "" || !strings.ContainsAny(s, " \t'\"$&;|<>()*?![]{}~#\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func joinSessionStartParts(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n\n")
}
