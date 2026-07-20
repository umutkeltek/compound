// Package harness: the Adapter interface is the executor contract for a
// real harness. Today the package exposes only a registry of capabilities
// (Harness, Capabilities, Registry); nothing drives a side effect. S1+S2
// shipped the RunAttempt spine (internal/runattempt) and panel.Apply is
// the only caller that mints one. Slice C introduces the Adapter
// interface so future harness-specific executors (Claude, Codex,
// OpenCode, the OS-harness-entrypoints shell) can drive the same spine.
//
// The Adapter contract:
//   - Mint a RunAttempt BEFORE any side effect
//   - Populate the S2 fields (workspace / command / transcript / heartbeats
//     / diff / test / proof_refs) AS DATA EMERGES -- not all-at-once
//   - Complete the RunAttempt (succeeded|failed) on terminal status
//   - Return the terminal RunAttempt plus a sentinel error ONLY for
//     infrastructure failure (couldn't mint, couldn't spawn, etc.);
//     subprocess exit codes are NOT errors -- they map to Status
//
// Compound is the orchestrator; the Adapter is the driver. The S1+S2
// spine guarantees that whatever the adapter does is auditable through a
// single ratt_* id. The adapter is responsible for populating the S2
// fields with the harness-specific evidence.
package harness

import (
	"context"
	"io"
	"time"

	"compound/internal/runattempt"
)

// Adapter is the executor contract. Concrete adapters live in
// internal/harness (os_entrypoints_adapter.go today; future claude / codex /
// opencode adapters in their own files). The Registry today maps
// harness name -> Capabilities; the future slice keys adapters to
// registry rows via Name().
type Adapter interface {
	// Name returns the adapter id. Matches Harness.Name in the registry and
	// the AdapterID in OSEntrypointsFile. Pinned by
	// TestOSEntrypointsAdapterName.
	Name() string
	// Run drives one execution end-to-end. MUST mint a RunAttempt before
	// any side effect. MUST terminalize the RunAttempt on every return
	// path (succeeded|failed). The returned RunOutput.RunAttempt is the
	// terminal artifact; out.Err is non-nil only for infrastructure
	// failure (could not mint, could not spawn, could not write transcript).
	Run(ctx context.Context, in RunInput) (RunOutput, error)
}

// RunInput is the per-Run caller contract. All fields are required unless
// marked optional. A future slice (harness-adapter-feature) adds
// PermissionDecision and CapabilityRef once the policy seam is in place.
type RunInput struct {
	// WorkID is the work envelope this run belongs to. The adapter does
	// not validate the id exists -- the caller is responsible for
	// admitting the work first. The adapter records the id on the
	// minted RunAttempt for proof-chain linkage.
	WorkID string
	// Harness is the registry name of the harness driving this run.
	// Today only "os-harness-entrypoints" has a real adapter. Pinned
	// onto the RunAttempt.Harness field.
	Harness string
	// Workspace is the filesystem root the subprocess runs inside. Pinned
	// onto RunAttempt.Workspace.
	Workspace string
	// Command is the program path. Pinned onto RunAttempt.Command.
	Command string
	// Args is the argv (after the program path). May be empty.
	Args []string
	// Env is the additional env vars to set on the subprocess. Optional.
	Env []string
	// Stdin is the optional stdin. Optional.
	Stdin io.Reader
	// Timeout caps the subprocess lifetime. 0 = no cap. Optional.
	Timeout time.Duration
	// Now is the wall-clock anchor for the RunAttempt's CreatedAt and the
	// first heartbeat. The adapter does not call time.Now() itself; the
	// caller supplies Now for testability. Required.
	Now time.Time
	// ActionIntentID optionally binds the RunAttempt to the authorizing
	// ActionIntent (closes the attempt->intent->policy-decision audit
	// chain). Optional today; future slice requires it.
	ActionIntentID string
}

// RunOutput is the per-Run return contract. Always non-nil RunAttempt;
// Err is non-nil only on infrastructure failure.
type RunOutput struct {
	// RunAttempt is the terminalized RunAttempt. Re-Get from store if the
	// caller needs the most-recent state (heartbeats land after this is
	// returned; for long runs, see RunAttempt.LastHeartbeatAt).
	RunAttempt runattempt.RunAttempt
	// TranscriptPath is the path to the captured transcript under
	// <home>/run_attempts/<id>.transcript. Set on every successful
	// subprocess exit AND on every timeout -- partial capture is the
	// audit trail even when the process did not finish.
	TranscriptPath string
	// ExitCode is the subprocess exit code. 0 for success. May be -1
	// when the process was killed by a signal (timeout).
	ExitCode int
	// TimedOut is true when the subprocess exceeded RunInput.Timeout and
	// was killed. The RunAttempt is then Status=Failed and ExitCode=-1.
	TimedOut bool
	// Duration is the wall time the subprocess ran for.
	Duration time.Duration
}
