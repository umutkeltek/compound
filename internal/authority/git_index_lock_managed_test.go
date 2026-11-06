package authority

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentManagedGitIndexLockRecoveryPreservesWIPWithoutPauseClaim(t *testing.T) {
	home, plan, now := newIndexRecoveryFixture(t, "codex:test")
	at := now.Add(time.Second)
	result, err := RecoverStaleGitIndexLock(context.Background(), home, plan.State.Snapshot.Repo, plan.WorkID, plan.Actor, indexRecoveryIdle, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if result.Coordination != GitIndexLockAgentManaged || result.Result.Outcome != "completed" || result.Result.ReceiptID == "" || result.Result.TransitionID == "" {
		t.Fatalf("missing governed recovery: %+v", result)
	}
	var review GitIndexLockReview
	if err := readIndexRecoveryArtifact(home, result.ReviewID+".review.json", &review); err != nil {
		t.Fatal(err)
	}
	if review.Window.Coordination != GitIndexLockAgentManaged || review.Window.WritersPaused || review.Window.ReviewedBy != "codex:test" {
		t.Fatalf("machine recovery fabricated a pause or operator identity: %+v", review)
	}
	repo := plan.State.Snapshot.Repo
	index, err := os.ReadFile(filepath.Join(repo, ".git/index"))
	if err != nil || !bytes.Equal(index, plan.State.Index) {
		t.Fatal("index changed")
	}
	body, err := os.ReadFile(filepath.Join(repo, "seed.txt"))
	if err != nil || string(body) != "foreign WIP\n" {
		t.Fatal("working bytes changed")
	}
	lock := filepath.Join(repo, ".git/index.lock")
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("old lock remains: %v", err)
	}
	if err := os.WriteFile(lock, []byte("new writer"), 0600); err != nil {
		t.Fatal(err)
	}
	replay, err := ApplyGitIndexLockRecovery(context.Background(), home, result.PlanID, result.ReviewID, plan.Actor, indexRecoveryIdle, func() time.Time { return at.Add(10 * time.Minute) })
	if err != nil || !replay.Replayed || replay.ReceiptID != result.Result.ReceiptID {
		t.Fatalf("replay failed: %+v %v", replay, err)
	}
	body, err = os.ReadFile(lock)
	if err != nil || string(body) != "new writer" {
		t.Fatal("historical recovery touched replacement")
	}
}

func TestAgentManagedGitIndexLockReviewCannotForgePauseOrIdentity(t *testing.T) {
	_, plan, now := newIndexRecoveryFixture(t, "codex:test")
	base := GitIndexLockReview{PlanID: plan.ID, PlanDigest: plan.Digest, IssuedAt: now, Window: GitIndexLockWindow{
		Coordination: GitIndexLockAgentManaged, ReviewedBy: plan.Actor, EvidenceRef: agentIndexLockEvidence(plan), ExpiresAt: now.Add(time.Minute),
	}}
	for _, mode := range []string{"valid", "pause", "foreign actor", "foreign evidence", "unknown mode", "expired"} {
		t.Run(mode, func(t *testing.T) {
			review := base
			switch mode {
			case "pause":
				review.Window.WritersPaused = true
			case "foreign actor":
				review.Window.ReviewedBy = "codex:other"
			case "foreign evidence":
				review.Window.EvidenceRef = "caller:assertion"
			case "unknown mode":
				review.Window.Coordination = "unchecked"
			case "expired":
				review.Window.ExpiresAt = now
			}
			review.ID = "gilr_" + indexRecoveryDigest(review)[:24]
			err := validateGitIndexLockReview(plan, review, now)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
		})
	}
	manual := base
	manual.Window.Coordination = ""
	manual.ID = "gilr_" + indexRecoveryDigest(manual)[:24]
	if err := validateGitIndexLockReview(plan, manual, now); err == nil {
		t.Fatal("legacy manual mode accepted a fake operator pause")
	}
}

func TestAgentManagedGitIndexLockRecoveryRefusesUncertainAndChangedState(t *testing.T) {
	for _, mode := range []string{"unknown holder", "fresh", "nonempty", "changed after plan"} {
		t.Run(mode, func(t *testing.T) {
			home, plan, now := newIndexRecoveryFixture(t, "codex:test")
			lock := filepath.Join(plan.State.Snapshot.Repo, ".git/index.lock")
			calls := 0
			probe := func(context.Context, ...string) error {
				calls++
				if mode == "unknown holder" {
					return fmt.Errorf("writer state unknown")
				}
				if mode == "changed after plan" && calls == 2 {
					return os.WriteFile(lock, []byte("replacement"), 0600)
				}
				return nil
			}
			switch mode {
			case "fresh":
				if err := os.Chtimes(lock, now, now); err != nil {
					t.Fatal(err)
				}
			case "nonempty":
				if err := os.WriteFile(lock, []byte("writer"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := RecoverStaleGitIndexLock(context.Background(), home, plan.State.Snapshot.Repo, plan.WorkID, plan.Actor, probe, func() time.Time { return now.Add(time.Second) })
			if err == nil || result.Result.Recovery.Changed {
				t.Fatalf("unsafe recovery accepted: %+v %v", result, err)
			}
			if _, err := os.Stat(lock); err != nil {
				t.Fatal("refusal removed lock")
			}
		})
	}
}
