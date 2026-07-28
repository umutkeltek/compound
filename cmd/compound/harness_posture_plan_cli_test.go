package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"compound/internal/hookplan"
)

func TestHarnessPosturePlanCompilesAdapterNativeConfigurationWithoutVendorFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		harness    string
		wantFamily string
		wantReady  bool
		wantGuard  hookplan.Disposition
	}{
		{harness: "claude-zai", wantFamily: "claude", wantReady: true, wantGuard: hookplan.DispositionNative},
		{harness: "codex", wantFamily: "codex", wantReady: true, wantGuard: hookplan.DispositionNative},
		{harness: "opencode", wantFamily: "opencode", wantReady: false, wantGuard: hookplan.DispositionUnsupported},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := runHarnessPosturePlan(
				[]string{"--harness", tc.harness, "--json"},
				&stdout,
				&stderr,
			)
			if code != 0 {
				t.Fatalf("posture-plan exit=%d stderr=%s", code, stderr.String())
			}
			var got hookplan.CompiledPosture
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("decode posture plan: %v\n%s", err, stdout.String())
			}
			if got.CompiledFrom.HarnessFamily != tc.wantFamily || got.MutationReady != tc.wantReady {
				t.Fatalf("compiled posture = %+v", got)
			}
			guard := compiledIntentionForTest(t, got, hookplan.IntentionGuardMutation)
			if guard.Disposition != tc.wantGuard {
				t.Fatalf("guard disposition = %q, want %q", guard.Disposition, tc.wantGuard)
			}
		})
	}
}

func compiledIntentionForTest(
	t *testing.T,
	compiled hookplan.CompiledPosture,
	want hookplan.HarnessIntention,
) hookplan.CompiledIntention {
	t.Helper()
	for _, intention := range compiled.Intentions {
		if intention.Intention == want {
			return intention
		}
	}
	t.Fatalf("compiled posture missing %q", want)
	return hookplan.CompiledIntention{}
}
