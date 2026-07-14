package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/work"
)

const dispatchClaimConfirmChildEnv = "COMPOUND_DISPATCH_CLAIM_CONFIRM_CHILD"

func TestWorkDispatchClaimCLIChild(t *testing.T) {
	mode := os.Getenv(dispatchClaimConfirmChildEnv)
	if mode == "" {
		t.Skip("subprocess helper")
	}
	// CLI boundary tests must not inherit the operator's live CodexBar/quota state.
	// A confirmed command still exercises the complete claim mutation below this adapter.
	work.LaneCapacityGuard = nil
	args := []string{"dispatch-claim", "--actor", "codex", "--json"}
	switch mode {
	case "argless":
	case "next":
		args = append(args, "--next")
	case "explicit":
		args = append(args, os.Getenv("COMPOUND_TEST_WORK_ID"))
	case "confirm":
		args = append(args, "--next", "--confirm")
	case "both":
		args = append(args, "--next", os.Getenv("COMPOUND_TEST_WORK_ID"))
	default:
		t.Fatalf("unknown child mode %q", mode)
	}
	cmdWork(args)
	// Successful JSON commands return normally; exit here so the go test child does not append
	// its own PASS line to the CLI bytes being asserted by the parent process.
	os.Exit(0)
}

func TestWorkDispatchClaimUnconfirmedPreviewsWithoutMutating(t *testing.T) {
	for _, target := range []string{"argless", "next", "explicit"} {
		t.Run(target, func(t *testing.T) {
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", cliTestNow); err != nil {
				t.Fatalf("init home: %v", err)
			}
			store, err := work.Open(homePath)
			if err != nil {
				t.Fatalf("open work: %v", err)
			}
			ready, err := store.Create(work.CreateInput{
				Owner:            "compound-core",
				SuccessPredicate: "unconfirmed dispatch-claim previews without mutation",
			}, cliTestNow)
			if err != nil {
				t.Fatalf("create work: %v", err)
			}
			before := snapshotDispatchClaimHome(t, homePath)

			output, runErr := runDispatchClaimChild(t, target, homePath, ready.ID)
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("unconfirmed %s dispatch-claim exit = %v, want 1\n%s", target, runErr, output)
			}
			var preview work.DispatchPlan
			if err := json.Unmarshal(output, &preview); err != nil {
				t.Fatalf("parse %s preview: %v\n%s", target, err, output)
			}
			if preview.Selected == nil || preview.Selected.WorkID != ready.ID {
				t.Fatalf("%s preview selected %+v, want %s", target, preview.Selected, ready.ID)
			}
			if preview.Actor != "codex" {
				t.Fatalf("%s preview actor = %q, want codex", target, preview.Actor)
			}

			after, err := store.Get(ready.ID)
			if err != nil {
				t.Fatalf("get work after preview: %v", err)
			}
			if after.State != "ready" {
				t.Fatalf("unconfirmed %s preview changed state to %s", target, after.State)
			}
			if afterSnapshot := snapshotDispatchClaimHome(t, homePath); !reflect.DeepEqual(before, afterSnapshot) {
				t.Fatalf("home mutated by unconfirmed %s dispatch-claim:\nbefore=%+v\nafter=%+v", target, before, afterSnapshot)
			}
		})
	}
}

func TestWorkDispatchClaimConfirmPerformsMutation(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "confirmed dispatch-claim performs the claim",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}

	output, runErr := runDispatchClaimChild(t, "confirm", homePath, ready.ID)
	if runErr != nil {
		t.Fatalf("confirmed dispatch-claim failed: %v\n%s", runErr, output)
	}
	var result work.DispatchClaimResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("parse confirmed result: %v\n%s", err, output)
	}
	if result.ClaimedWorkID != ready.ID || result.Status != "claimed" {
		t.Fatalf("confirmed result = %+v, want claimed %s", result, ready.ID)
	}
	after, err := store.Get(ready.ID)
	if err != nil {
		t.Fatalf("get work after confirmed claim: %v", err)
	}
	if after.State != "claimed" || after.Authority.Owner != "codex" {
		t.Fatalf("confirmed dispatch-claim did not mutate through claim path: %+v", after)
	}
}

func TestWorkDispatchClaimRejectsNextAndExplicitIDTogether(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "reject ambiguous target"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	output, runErr := runDispatchClaimChild(t, "both", homePath, ready.ID)
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("ambiguous target exit = %v, want 2\n%s", runErr, output)
	}
	if !strings.Contains(string(output), "either --next or <work_id>") {
		t.Fatalf("ambiguous target error is not actionable:\n%s", output)
	}
}

func runDispatchClaimChild(t *testing.T, mode, homePath, workID string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkDispatchClaimCLIChild$")
	cmd.Env = []string{
		dispatchClaimConfirmChildEnv + "=" + mode,
		"COMPOUND_HOME=" + homePath,
		"COMPOUND_TEST_WORK_ID=" + workID,
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"TZ=UTC",
	}
	return cmd.CombinedOutput()
}

func snapshotDispatchClaimHome(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot[rel] = "dir"
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		snapshot[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot home: %v", err)
	}
	return snapshot
}
