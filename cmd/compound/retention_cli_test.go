package main

import (
	"strings"
	"testing"
)

func TestResolveReclaimTargetRequiresExplicitConsumerAndThreshold(t *testing.T) {
	if _, err := resolveReclaimTarget([]string{"--required-bytes", "100"}); err == nil {
		t.Fatal("missing --consumer must fail closed")
	}
	if _, err := resolveReclaimTarget([]string{"--consumer", "fixture"}); err == nil {
		t.Fatal("missing published threshold must fail closed")
	}
}

func TestResolveReclaimTargetUsesCallerPublishedValues(t *testing.T) {
	got, err := resolveReclaimTarget([]string{
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
	if got := retentionUsage(); !containsAll(got, "classes", "scan", "plan", "never reclaims") {
		t.Fatalf("retention usage is incomplete: %s", got)
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
