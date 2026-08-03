package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteExperimentPlanCLI(t *testing.T) {
	root := t.TempDir()
	for _, row := range []struct{ id, route, alias string }{{"claude-gpt", "codex/gpt-5.6-sol", "codex/gpt-5.6-sol"}, {"claude-minimax", "minimax/MiniMax-M3", "MiniMax-M3[1m]"}} {
		v := map[string]any{"schema": "switchback/profile-conformance@1", "conformance_revision": "sha256:" + row.id, "profile": map[string]any{"id": row.id, "harness": "claude-code", "route": row.route, "requested_model": row.route, "requested_effort": "xhigh", "model_aliases": map[string]string{"opus": row.alias}}}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(root, row.id+".json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out, errOut bytes.Buffer
	code := runRouteExperimentPlanCommand([]string{"--profiles-root", root, "--role", "opus", "--profile", "claude-gpt", "--profile", "claude-minimax", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errOut.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["verdict"] != "plan_only_not_execution_evidence" {
		t.Fatalf("got %#v", got)
	}
}

func TestRouteExperimentPlanRequiresProfilesRoot(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runRouteExperimentPlanCommand(
		[]string{"--profile", "claude-gpt", "--profile", "claude-minimax"},
		&out,
		&errOut,
	)
	if code != 2 {
		t.Fatalf("code=%d, want 2; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "--profiles-root is required") {
		t.Fatalf("missing required-flag refusal: %s", errOut.String())
	}
}

func TestRouteExperimentPlanOneProfileMultipleModelRoles(t *testing.T) {
	root := t.TempDir()
	v := map[string]any{
		"schema":               "switchback/profile-conformance@1",
		"conformance_revision": "sha256:qwen",
		"profile": map[string]any{
			"id": "claude-qwen", "harness": "claude-code", "route": "qwen/qwen3.8-max",
			"requested_model": "qwen3.8-max", "requested_effort": "xhigh",
			"model_aliases": map[string]string{"opus": "qwen3.8-max", "sonnet": "qwen3.7-plus"},
		},
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(root, "claude-qwen.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runRouteExperimentPlanCommand([]string{
		"--profiles-root", root,
		"--profile", "claude-qwen",
		"--role", "opus",
		"--role", "sonnet",
		"--json",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errOut.String())
	}
	var got struct {
		RoleAliases []string `json:"role_aliases"`
		Lanes       []struct {
			RoleAlias      string `json:"role_alias"`
			RequestedAlias string `json:"requested_alias"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Lanes) != 2 || len(got.RoleAliases) != 2 {
		t.Fatalf("plan did not expand one profile into two real model trials: %+v", got)
	}
	if got.Lanes[0].RoleAlias != "opus" || got.Lanes[0].RequestedAlias != "qwen3.8-max" ||
		got.Lanes[1].RoleAlias != "sonnet" || got.Lanes[1].RequestedAlias != "qwen3.7-plus" {
		t.Fatalf("model aliases do not match the owner projection: %+v", got.Lanes)
	}
}

func TestRouteExperimentPlanRefusesUnpublishedEffortVariant(t *testing.T) {
	root := t.TempDir()
	v := map[string]any{
		"schema":               "switchback/profile-conformance@1",
		"conformance_revision": "sha256:qwen",
		"profile": map[string]any{
			"id": "claude-qwen", "harness": "claude-code", "route": "qwen/qwen3.8-max",
			"requested_model": "qwen3.8-max", "requested_effort": "xhigh",
			"model_aliases": map[string]string{"opus": "qwen3.8-max", "sonnet": "qwen3.7-plus"},
		},
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(root, "claude-qwen.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runRouteExperimentPlanCommand([]string{
		"--profiles-root", root,
		"--profile", "claude-qwen",
		"--role", "opus",
		"--role", "sonnet",
		"--effort", "high",
	}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code=%d, want 2; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "effort high is not materialized") {
		t.Fatalf("planner did not name the owner-published profile requirement: %s", errOut.String())
	}
}
