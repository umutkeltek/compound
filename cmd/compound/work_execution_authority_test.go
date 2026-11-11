package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"compound/internal/work"
)

func TestWorkExecutionAuthorityIsExactAndReadOnly(t *testing.T) {
	now := time.Now().UTC()
	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(work.CreateInput{Owner: "pool-proof", SuccessPredicate: "read-only native pool output"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(created.ID, "pool-proof", now); err != nil {
		t.Fatal(err)
	}
	f := claimedExecutionFixture{homePath: homePath, store: store, workID: created.ID}
	w, err := f.store.Advance(f.workID, work.AdvanceInput{To: "running", Actor: "pool-proof"}, now)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
	action := w.ActionIntentIDs[len(w.ActionIntentIDs)-1]
	args := []string{"--work", w.ID, "--policy-decision", w.PolicyDecisionID, "--action-intent", action, "--permission-mode", "plan", "--json"}
	if workCommandNeedsWritableStore("execution-authority", args) {
		t.Fatal("authority callback opens writable store")
	}
	readonly, err := work.OpenReadOnly(f.homePath)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotHomeFileHashes(t, f.homePath)
	if err := checkWorkExecutionAuthority(readonly, args, now); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"work", []string{"--work", "work_other", "--policy-decision", w.PolicyDecisionID, "--action-intent", action, "--permission-mode", "plan"}},
		{"policy", []string{"--work", w.ID, "--policy-decision", "pol_other", "--action-intent", action, "--permission-mode", "plan"}},
		{"action", []string{"--work", w.ID, "--policy-decision", w.PolicyDecisionID, "--action-intent", "act_other", "--permission-mode", "plan"}},
		{"mutation", []string{"--work", w.ID, "--policy-decision", w.PolicyDecisionID, "--action-intent", action, "--permission-mode", "bypass"}},
		{"missing", args[:6]},
		{"extra", append(append([]string(nil), args...), "--advance")},
		{"duplicate", append(append([]string(nil), args...), "--work", w.ID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := checkWorkExecutionAuthority(readonly, test.args, now); err == nil {
				t.Fatal("invalid reservation accepted")
			}
		})
	}
	if !reflect.DeepEqual(before, snapshotHomeFileHashes(t, f.homePath)) {
		t.Fatal("read-only authority callback mutated home")
	}
	output := captureStdout(t, func() { cmdWork(append([]string{"execution-authority"}, args...)) })
	var verdict struct {
		Allow bool `json:"allow"`
	}
	if err := json.Unmarshal([]byte(output), &verdict); err != nil || !verdict.Allow {
		t.Fatalf("CLI did not expose native authority: %s", output)
	}
	if !reflect.DeepEqual(before, snapshotHomeFileHashes(t, f.homePath)) {
		t.Fatal("CLI callback mutated home")
	}
	if _, err := f.store.Advance(w.ID, work.AdvanceInput{To: "verifying", Actor: "pool-proof"}, now); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkExecutionAuthority(readonly, args, now); err == nil {
		t.Fatal("stale running reservation accepted")
	}
}
