package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
