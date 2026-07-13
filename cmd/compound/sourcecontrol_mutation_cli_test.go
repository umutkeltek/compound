package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/authority"
	"compound/internal/envelope"
	"compound/internal/home"
)

func TestAuthorityDirtyPlanDerivesMutationBindingFromPersistedActions(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	repo := initCompoundCommandGitRepo(t)
	writeFileAt(t, filepath.Join(repo, "README.md"), "changed\n")
	writeFileAt(t, filepath.Join(repo, "SECOND.md"), "second\n")

	inputHash := "mutation-input:sha256:" + strings.Repeat("a", 64)
	first := persistedMutationAction("act_cli_dirty_first", "work_live_cli_dirty", filepath.Join(repo, "README.md"), inputHash)
	second := persistedMutationAction("act_cli_dirty_second", "work_live_cli_dirty", filepath.Join(repo, "SECOND.md"), inputHash)
	writeJSONFixture(t, filepath.Join(homePath, "events", first.ID+".json"), first)
	writeJSONFixture(t, filepath.Join(homePath, "events", second.ID+".json"), second)

	out := captureStdout(t, func() {
		cmdAuthority([]string{
			"dirty-plan", repo,
			"--work-id", "work_owner_cli_dirty",
			"--action-intent", second.ID,
			"--action-intent", first.ID,
			"--json",
		})
	})
	var result struct {
		Plan authority.SourceControlReviewPlan `json:"plan"`
	}
	readJSONBytes(t, []byte(out), &result)
	binding := result.Plan.MutationBinding
	if binding == nil {
		t.Fatalf("dirty-plan omitted persisted mutation binding: %s", out)
	}
	if binding.ActionWorkID != "work_live_cli_dirty" || binding.MutationInputHash != inputHash ||
		binding.Capability != "filesystem.write" || binding.Action != "multi_edit_file" || binding.MutationClass != "local_reversible" {
		t.Fatalf("dirty-plan trusted or lost action semantics: %+v", binding)
	}
	if len(binding.TargetPaths) != 2 || len(binding.ActionIntentIDs) != 2 ||
		binding.ActionIntentIDs[0] != first.ID || binding.ActionIntentIDs[1] != second.ID {
		t.Fatalf("dirty-plan did not bind complete persisted action set: %+v", binding)
	}
	if result.Plan.WorkID != "work_owner_cli_dirty" {
		t.Fatalf("owner work provenance = %q", result.Plan.WorkID)
	}
}

func TestDirtyPlanPersistedActionsMustShareExecutableIdentity(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	inputHash := "mutation-input:sha256:" + strings.Repeat("b", 64)
	first := persistedMutationAction("act_cli_mismatch_first", "work_live_cli_mismatch", "/repo/FIRST.md", inputHash)
	second := persistedMutationAction("act_cli_mismatch_second", "work_live_cli_mismatch", "/repo/SECOND.md", inputHash)
	second.Action = "edit_file"
	writeJSONFixture(t, filepath.Join(homePath, "events", first.ID+".json"), first)
	writeJSONFixture(t, filepath.Join(homePath, "events", second.ID+".json"), second)

	if _, err := loadSourceControlMutationBinding(homePath, []string{first.ID, second.ID}); err == nil || !strings.Contains(err.Error(), "share action") {
		t.Fatalf("mismatched persisted semantics error = %v", err)
	}
}

func persistedMutationAction(id, workID, target, inputHash string) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     cliTestNow.Format("2006-01-02T15:04:05Z07:00"),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier2",
			EvidenceRefs:  []string{inputHash},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    "filesystem.write",
		Action:        "multi_edit_file",
		Target:        envelope.ActionTarget{Kind: "file", Path: target},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "repo",
	}
}

func writeFileAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONBytes(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, data)
	}
}
