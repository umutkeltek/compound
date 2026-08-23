package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"compound/internal/policy"
)

func TestResolveReclaimTargetRequiresExplicitConsumerAndThreshold(t *testing.T) {
	if _, err := policy.ResolveReclaimTargetForTest([]string{"--required-bytes", "100"}); err == nil {
		t.Fatal("missing --consumer must fail closed")
	}
	if _, err := policy.ResolveReclaimTargetForTest([]string{"--consumer", "fixture"}); err == nil {
		t.Fatal("missing published threshold must fail closed")
	}
}

func TestResolveReclaimTargetUsesCallerPublishedValues(t *testing.T) {
	got, err := policy.ResolveReclaimTargetForTest([]string{
		"--consumer", "fixture",
		"--free-bytes", "50",
		"--capacity-bytes", "1000",
		"--required-bytes", "100",
		"--required-bps", "1500",
	})
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if got.Consumer != "fixture" || got.FreeBytes != 50 || got.CapacityBytes != 1000 ||
		got.RequiredFreeBytes != 100 || got.RequiredFreeBps != 1500 {
		t.Fatalf("target lost caller values: %+v", got)
	}
	if got.ShortfallBytes() != 100 {
		t.Fatalf("shortfall = %d, want stricter ratio-derived 100", got.ShortfallBytes())
	}
}

func TestChangedCommandsAreDiscoverable(t *testing.T) {
	if got := routeUsage(""); !containsAll(got, "lane-projection", "--harness H") {
		t.Fatalf("route usage does not expose lane projection: %s", got)
	}
	if got := policy.RetentionUsageForTest(); !containsAll(got, "classes", "scan", "plan", "never reclaims") {
		t.Fatalf("retention usage is incomplete: %s", got)
	}
}

func TestRetentionPlanJSONInsufficiencyExitsNonZero(t *testing.T) {
	out, err := runCLIFlagSeam(
		t,
		t.TempDir(),
		"retention", "plan",
		"--consumer", "fixture",
		"--root", t.TempDir(),
		"--free-bytes", "0",
		"--capacity-bytes", "0",
		"--required-bytes", "9223372036854775807",
		"--json",
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("insufficient JSON plan exit = %v, want 1; output:\n%s", err, out)
	}
	if !strings.Contains(string(out), `"sufficient": false`) {
		t.Fatalf("insufficient JSON plan lost its machine-readable verdict:\n%s", out)
	}
}

func TestRetentionRejectsUnknownFlagsAsUsage(t *testing.T) {
	out, err := runCLIFlagSeam(t, t.TempDir(), "retention", "plan", "--required-byte", "123")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("unknown retention flag exit = %v, want 2; output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "unknown flag --required-byte") {
		t.Fatalf("unknown retention flag was not named:\n%s", out)
	}
}

func containsAll(s string, wants ...string) bool {
	for _, want := range wants {
		if !strings.Contains(s, want) {
			return false
		}
	}
	return true
}