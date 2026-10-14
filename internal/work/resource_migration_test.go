package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/landlease"
	"compound/internal/runattempt"
)

func TestResourceMigrationRefusesUnresolvedAuthority(t *testing.T) {
	for _, mode := range []string{"missing-pause", "expired", "foreign-actor", "active-attempt", "pending-completion", "pending-recovery", "expired-holder"} {
		t.Run(mode, func(t *testing.T) {
			s := newStore(t)
			w := claimedProofWork(t, s, "resource migration", proofActorAlpha, fixedNow)
			now := fixedNow.Add(time.Minute)
			in := ResourceMigrationInput{Actor: proofActorAlpha, Operator: "operator:fixture", EvidenceRef: "test://writer-pause", WritersPaused: true, ExpiresAt: now.Add(time.Minute)}
			switch mode {
			case "missing-pause":
				in.WritersPaused = false
			case "expired":
				in.ExpiresAt = now
			case "foreign-actor":
				in.Actor = proofActorBeta
			case "active-attempt":
				if _, err := s.runAttempts().Mint(w.ID, proofActorAlpha, proofResourceHarness, now); err != nil {
					t.Fatal(err)
				}
			case "pending-completion", "pending-recovery":
				area := "proof-resource-completions"
				if mode == "pending-recovery" {
					area = "proof-resource-legacy-recoveries"
				}
				dir := filepath.Join(s.home, "work", area)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "expired-holder":
				if _, err := landlease.Acquire(s.home, "legacy:holder", time.Second, now.Add(-time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.MigrateCoordinationResources(w.ID, in, now); err == nil {
				t.Fatal("unsafe migration succeeded")
			}
			layout, err := landlease.LayoutStatus(s.home)
			if err != nil || layout.Mode != "legacy" {
				t.Fatalf("refusal changed layout: %+v %v", layout, err)
			}
		})
	}
}

func TestResourceMigrationAndProofConsumerUseScopedLease(t *testing.T) {
	s := newStore(t)
	w := claimedProofWork(t, s, "scoped consumer", proofActorAlpha, fixedNow)
	now := fixedNow.Add(time.Minute)
	in := ResourceMigrationInput{Actor: proofActorAlpha, Operator: "operator:fixture", EvidenceRef: "test://writer-pause", WritersPaused: true, ExpiresAt: now.Add(time.Minute)}
	layout, err := s.MigrateCoordinationResources(w.ID, in, now)
	if err != nil || layout.Mode != "scoped" {
		t.Fatalf("migration: %+v %v", layout, err)
	}
	key, err := landlease.LandingResourceKey(strings.Repeat("a", 64), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	landing, err := landlease.OpenResource(s.home, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := landing.Acquire("synthetic:"+w.ID, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	acquired, err := reserveProofResource(t, s, w.ID, ProofResourceReserveInput{
		Actor: proofActorAlpha, TTL: time.Minute, Workspace: "/synthetic/repo",
		Command: "compound proof-check --out /synthetic/report.json",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	probe := &DefaultSweepStaleProbe{Home: s.home, Now: func() time.Time { return now.Add(2 * time.Second) }}
	if held, err := probe.LeaseHeld(w.ID); err != nil || !held {
		t.Fatalf("proof work liveness lost: %v %v", held, err)
	}
	if err := acquired.ExecutionBarrier.Close(); err != nil {
		t.Fatal(err)
	}
	completed, err := s.CompleteProofResource(w.ID, ProofResourceCompleteInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, FailureReason: "synthetic gate failure",
	}, now.Add(3*time.Second))
	if err != nil || completed.Result.RunAttemptStatus != runattempt.StatusFailed {
		t.Fatalf("completion: %+v %v", completed, err)
	}
	if status, err := landlease.ProofResource(s.home).StatusReadOnly(now); err != nil || status.Held {
		t.Fatalf("proof not released: %+v %v", status, err)
	}
	if status, err := landing.StatusReadOnly(now); err != nil || !status.Held {
		t.Fatalf("landing disturbed: %+v %v", status, err)
	}
}

func TestLandingResourceAuthorityRejectsForeignWorkAndIdentityDrift(t *testing.T) {
	repo, base, _ := sharedLandingReadyRepo(t)
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "codex", Actor: proofActorAlpha, SuccessPredicate: "bound landing", GitDelivery: localGitDelivery(repo, base)}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.Claim(w.ID, proofActorAlpha, fixedNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	good, err := s.LandingResourceKey(w.ID)
	if err != nil || good.RepositoryFingerprint != w.GitDelivery.RepositoryFingerprint {
		t.Fatalf("key: %+v %v", good, err)
	}
	_, err = s.MutateLandingResource(w.ID, LandingResourceMutation{Actor: proofActorBeta, Holder: "codex:" + w.ID, Operation: "acquire", TTL: time.Minute}, fixedNow.Add(2*time.Second))
	if err == nil {
		t.Fatal("foreign actor acquired")
	}
	w.GitDelivery.RepositoryFingerprint = strings.Repeat("b", 64)
	if err := s.writeJSON(s.workPath(w.ID), w); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LandingResourceKey(w.ID); err == nil {
		t.Fatal("drifted fingerprint accepted")
	}
}

func TestSweepProofHolderUsesFourthComponent(t *testing.T) {
	s := newStore(t)
	holder := proofResourceHolder("pfr_fixture", "ratt_fixture", "work_fixture", "codex:actor")
	// Real minted attempt id keeps the production holder parser strict.
	attempt, err := s.runAttempts().Mint("work_fixture", "codex:actor", proofResourceHarness, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	holder = proofResourceHolder("pfr_fixture", attempt.ID, "work_fixture", "codex:actor")
	if _, err := landlease.Acquire(s.home, holder, time.Minute, fixedNow); err != nil {
		t.Fatal(err)
	}
	p := &DefaultSweepStaleProbe{Home: s.home, Now: func() time.Time { return fixedNow.Add(time.Second) }}
	if held, err := p.LeaseHeld("work_fixture"); err != nil || !held {
		t.Fatalf("liveness: %v %v", held, err)
	}
	if held, err := p.LeaseHeld("work_other"); err != nil || held {
		t.Fatalf("foreign liveness: %v %v", held, err)
	}
}
