package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"compound/internal/landlease"
)

func runLandLeaseCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runLandLease(args, &out, &errb)
	return out.String(), errb.String(), code
}

// A different holder cannot acquire over a live lease via the CLI: the second acquire exits non-zero
// with the current holder surfaced, and status reflects the original holder.
func TestLandLeaseCLIAcquireRefusesCompetingHolder(t *testing.T) {
	home := t.TempDir()

	_, errStr, code := runLandLeaseCLI(t, "acquire", "--holder", "lane-alpha", "--ttl", "300", "--home", home)
	if code != 0 {
		t.Fatalf("A acquire exit=%d stderr=%s", code, errStr)
	}

	// B is refused (non-zero) while A holds, and the current holder is printed.
	_, errStr, code = runLandLeaseCLI(t, "acquire", "--holder", "lane-beta", "--ttl", "300", "--home", home)
	if code == 0 {
		t.Fatalf("B acquire over A's live lease must exit non-zero")
	}
	if !strings.Contains(errStr, "lane-alpha") {
		t.Fatalf("refusal must name the current holder, got %q", errStr)
	}

	// status --json shows A still holding, live.
	out, errStr, code := runLandLeaseCLI(t, "status", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, errStr)
	}
	var st landlease.StatusResult
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode status: %v (%s)", err, out)
	}
	if !st.Held || st.Holder != "lane-alpha" {
		t.Fatalf("status should show lane-alpha holding: %+v", st)
	}
}

// Steal via the CLI requires a reason and records the audit trail; a reasonless steal is refused.
func TestLandLeaseCLIStealRequiresReasonAndRecords(t *testing.T) {
	home := t.TempDir()
	if _, errStr, code := runLandLeaseCLI(t, "acquire", "--holder", "lane-alpha", "--home", home); code != 0 {
		t.Fatalf("A acquire exit=%d stderr=%s", code, errStr)
	}

	// Reasonless steal is refused.
	if _, _, code := runLandLeaseCLI(t, "steal", "--holder", "lane-beta", "--home", home); code == 0 {
		t.Fatalf("steal without --reason must exit non-zero")
	}

	// Steal with a reason succeeds and records prior_holder + reason.
	out, errStr, code := runLandLeaseCLI(t, "steal", "--holder", "lane-beta", "--reason", "alpha wedged", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("steal exit=%d stderr=%s", code, errStr)
	}
	var lease landlease.Lease
	if err := json.Unmarshal([]byte(out), &lease); err != nil {
		t.Fatalf("decode steal: %v (%s)", err, out)
	}
	if lease.Holder != "lane-beta" || lease.PriorHolder != "lane-alpha" || lease.StealReason != "alpha wedged" {
		t.Fatalf("steal record wrong: %+v", lease)
	}
	if lease.StolenAt == "" {
		t.Fatalf("steal must record stolen_at")
	}
}

// Release via the CLI is holder-scoped: a non-holder release is refused; the holder frees it.
func TestLandLeaseCLIReleaseHolderScoped(t *testing.T) {
	home := t.TempDir()
	if _, errStr, code := runLandLeaseCLI(t, "acquire", "--holder", "lane-alpha", "--home", home); code != 0 {
		t.Fatalf("A acquire exit=%d stderr=%s", code, errStr)
	}
	// Non-holder release is refused.
	if _, _, code := runLandLeaseCLI(t, "release", "--holder", "lane-beta", "--home", home); code == 0 {
		t.Fatalf("non-holder release must exit non-zero")
	}
	// Holder release frees it, then status shows none held.
	if _, errStr, code := runLandLeaseCLI(t, "release", "--holder", "lane-alpha", "--home", home); code != 0 {
		t.Fatalf("holder release exit=%d stderr=%s", code, errStr)
	}
	out, _, code := runLandLeaseCLI(t, "status", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("status exit=%d", code)
	}
	var st landlease.StatusResult
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode status: %v (%s)", err, out)
	}
	if st.Held {
		t.Fatalf("lease should be free after holder release: %+v", st)
	}
}

func TestLandLeaseCLIUsage(t *testing.T) {
	_, _, code := runLandLeaseCLI(t)
	if code != 2 {
		t.Fatalf("no args should print usage and exit 2, got %d", code)
	}
	_, errStr, code := runLandLeaseCLI(t, "bogus")
	if code != 2 || !strings.Contains(errStr, "usage") {
		t.Fatalf("unknown subcommand should print usage/exit 2, got code=%d err=%s", code, errStr)
	}
}
