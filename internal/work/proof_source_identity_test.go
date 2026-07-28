package work

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/landlease"
)

func TestReserveProofResourceRejectsWorkRepositoryFingerprintMismatchBeforeMutation(t *testing.T) {
	store := newStore(t)
	repoA := initProofSourceRepo(t, "https://example.invalid/compound/repo-a.git")
	repoB := initProofSourceRepo(t, "https://example.invalid/compound/repo-b.git")
	sourceA, err := gitrepository.CaptureCleanSnapshot(repoA, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sourceB, err := gitrepository.CaptureCleanSnapshot(repoB, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 4, 30, 0, 0, time.UTC)
	item, err := store.Create(CreateInput{
		Owner:            "proof-source-test",
		Actor:            proofActorAlpha,
		SuccessPredicate: "proof belongs to the WorkEnvelope repository",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot:    repoA,
			RepositoryFingerprint: sourceA.RepositoryFingerprint,
			TargetLocalRef:        "refs/heads/main",
			ExpectedBaseSHA:       sourceA.BaseSHA,
			PublicationMode:       "local_only",
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Claim(item.ID, proofActorAlpha, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := NewProofResourceProfileWithSource(
		ProofResourceProfileCanonicalFull,
		repoB,
		sourceB,
		[]ProofResourceGateProfile{{
			Name:     "synthetic-gate",
			Command:  []string{"go", "test", "./internal/synthetic"},
			ProofRef: "synthetic-proof",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.ReserveProofResource(item.ID, ProofResourceReserveInput{
		Actor:     proofActorAlpha,
		TTL:       time.Minute,
		Workspace: repoB,
		Command:   "compound proof-check --base refs/heads/main --out /synthetic/proof.json",
		Profile:   profile,
	}, now.Add(2*time.Second))
	if err == nil || !strings.Contains(err.Error(), "repository fingerprint") {
		t.Fatalf("mismatched repository reserve error = %v", err)
	}
	current, getErr := store.Get(item.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(current.RunAttemptIDs) != len(item.RunAttemptIDs) ||
		len(current.ActionIntentIDs) != len(item.ActionIntentIDs) {
		t.Fatalf("fingerprint mismatch mutated work: before=%+v after=%+v", item, current)
	}
	status, statusErr := landlease.Status(store.home, now.Add(2*time.Second))
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Held {
		t.Fatalf("fingerprint mismatch acquired proof lease: %+v", status)
	}
}

func TestCompleteProofResourceStoresSealedSourceSnapshot(t *testing.T) {
	store := newStore(t)
	repo := initProofSourceRepo(t, "https://example.invalid/compound/repo-proof.git")
	source, err := gitrepository.CaptureCleanSnapshot(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 5, 0, 0, 0, time.UTC)
	item, err := store.Create(CreateInput{
		Owner:            "proof-source-test",
		Actor:            proofActorAlpha,
		SuccessPredicate: "sealed proof source remains reachable",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot:    repo,
			RepositoryFingerprint: source.RepositoryFingerprint,
			TargetLocalRef:        "refs/heads/main",
			ExpectedBaseSHA:       source.BaseSHA,
			PublicationMode:       "local_only",
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Claim(item.ID, proofActorAlpha, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	gates := []ProofResourceGateProfile{{
		Name:     "synthetic-gate",
		Command:  []string{"go", "test", "./internal/synthetic"},
		ProofRef: "synthetic-proof",
	}}
	profile, err := NewProofResourceProfileWithSource(
		ProofResourceProfileCanonicalFull,
		repo,
		source,
		gates,
	)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReserveProofResource(item.ID, ProofResourceReserveInput{
		Actor:     proofActorAlpha,
		TTL:       time.Minute,
		Workspace: repo,
		Command:   "compound proof-check --base refs/heads/main --out /synthetic/proof.json",
		Profile:   profile,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := reserved.ExecutionBarrier.Close(); err != nil {
		t.Fatal(err)
	}
	reportBody, err := json.Marshal(map[string]any{
		"schema_version":  "compound.proof_check_report.v0.3",
		"profile_kind":    ProofResourceProfileCanonicalFull,
		"status":          "ok",
		"generated_at":    now.Add(3 * time.Second).Format(time.RFC3339),
		"repo":            repo,
		"base_ref":        source.BaseSHA,
		"source_snapshot": source,
		"dry_run":         false,
		"proof_refs":      []string{"synthetic-proof"},
		"steps": []map[string]any{{
			"name":      "synthetic-gate",
			"command":   []string{"go", "test", "./internal/synthetic"},
			"proof_ref": "synthetic-proof",
			"status":    "ok",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(store.home)
	if err != nil {
		t.Fatal(err)
	}
	reportRef, err := artifacts.Put(reportBody)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteProofResource(item.ID, ProofResourceCompleteInput{
		Actor:             proofActorAlpha,
		ReservationID:     reserved.Reservation.ID,
		ReportArtifactRef: reportRef,
		ProducerExitKnown: true,
		ProducerExitCode:  0,
	}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Result.SourceSnapshot == nil ||
		*completed.Result.SourceSnapshot != source {
		t.Fatalf("completed proof source = %+v, want %+v", completed.Result.SourceSnapshot, source)
	}
	item, err = store.Advance(item.ID, AdvanceInput{
		To:         item.State,
		Actor:      proofActorAlpha,
		ArtifactID: "commit:" + source.HeadSHA,
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	matching, err := store.LandCheck(item.ID, LandCheckOptions{
		CandidateTipSHA: source.HeadSHA,
		Actor:           proofActorAlpha,
	}, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if matching.Status != "landed" ||
		matching.ProofVerificationRef != completed.VerificationRef ||
		matching.ProofHeadSHA != source.HeadSHA {
		t.Fatalf("matching proof land-check = %+v", matching)
	}

	if err := os.WriteFile(filepath.Join(repo, "replayed.txt"), []byte("replayed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runProofSourceGit(t, repo, "add", "replayed.txt")
	runProofSourceGit(t, repo, "commit", "-m", "replayed candidate")
	replayedTip := proofSourceGitOutput(t, repo, "rev-parse", "HEAD")
	item, err = store.Advance(item.ID, AdvanceInput{
		To:         item.State,
		Actor:      proofActorAlpha,
		ArtifactID: "commit:" + replayedTip,
	}, now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	mismatched, err := store.LandCheck(item.ID, LandCheckOptions{
		CandidateTipSHA: replayedTip,
		Actor:           proofActorAlpha,
	}, now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if mismatched.Status != "proof_candidate_tip_mismatch" ||
		mismatched.ProofHeadSHA != source.HeadSHA {
		t.Fatalf("replayed candidate reused stale proof: %+v", mismatched)
	}
}

func TestRecordedLandCheckBindsCloseToStoredProofTip(t *testing.T) {
	for _, tc := range []struct {
		name          string
		proofHead     func(f landingProofFixture) string
		wantCloseFail bool
	}{
		{
			name:      "matching",
			proofHead: func(f landingProofFixture) string { return f.candidate },
		},
		{
			name: "mismatch",
			proofHead: func(f landingProofFixture) string {
				head := strings.Repeat("f", len(f.candidate))
				if head == f.candidate {
					head = strings.Repeat("e", len(f.candidate))
				}
				return head
			},
			wantCloseFail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 7, 28, 5, 30, 0, 0, time.UTC)
			fixture := newLandingProofFixture(t, now)
			item, err := fixture.store.Get(fixture.workID)
			if err != nil {
				t.Fatal(err)
			}
			head := tc.proofHead(fixture)
			resultID := idFor("pfrs_", fixture.workID, "stored-proof", head)
			verificationRef := "proof-check:" + resultID
			sourceIdentity := gitrepository.Snapshot{
				RepositoryFingerprint: item.GitDelivery.RepositoryFingerprint,
				BaseSHA:               fixture.base,
				HeadSHA:               head,
				TreeSHA:               head,
				Clean:                 true,
			}
			reportBody, err := json.Marshal(map[string]any{
				"schema_version":  "compound.proof_check_report.v0.3",
				"profile_kind":    ProofResourceProfileCanonicalFull,
				"status":          "ok",
				"generated_at":    now.Add(5 * time.Minute).Format(time.RFC3339),
				"repo":            fixture.repo,
				"base_ref":        fixture.base,
				"source_snapshot": sourceIdentity,
				"dry_run":         false,
				"proof_refs":      []string{"synthetic-proof"},
				"steps": []map[string]any{{
					"name":      "synthetic-gate",
					"command":   []string{"go", "test", "./internal/synthetic"},
					"proof_ref": "synthetic-proof",
					"status":    "ok",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			artifacts, err := home.NewArtifactStore(fixture.store.home)
			if err != nil {
				t.Fatal(err)
			}
			reportRef, err := artifacts.Put(reportBody)
			if err != nil {
				t.Fatal(err)
			}
			result := ProofResourceResult{
				SchemaVersion:     ProofResourceResultSchemaVersion,
				ID:                resultID,
				Kind:              "work.proof_resource.result",
				Status:            ProofResourceVerified,
				WorkID:            fixture.workID,
				OwnedReport:       true,
				ReportArtifactRef: reportRef,
				VerificationRef:   verificationRef,
				SourceSnapshot:    &sourceIdentity,
			}
			if err := fixture.store.writeEventJSON(resultID, &result); err != nil {
				t.Fatal(err)
			}
			item, err = fixture.store.Advance(fixture.workID, AdvanceInput{
				To:             item.State,
				Actor:          "codex",
				VerificationID: verificationRef,
			}, now.Add(5*time.Minute))
			if err != nil {
				t.Fatal(err)
			}

			proof := fixture.proof
			if !tc.wantCloseFail {
				proof, err = fixture.store.LandCheck(fixture.workID, LandCheckOptions{
					RepoPath:        fixture.repo,
					CandidateTipSHA: fixture.candidate,
					TargetRef:       "main",
					Record:          true,
				}, now.Add(6*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				if proof.Status != "landed" {
					t.Fatalf("matching stored proof did not produce landed proof: %+v", proof)
				}
			}
			_, err = fixture.store.recordedLandedProofForClose(item, []string{proof.EvidenceRef})
			if tc.wantCloseFail {
				if err == nil || !strings.Contains(err.Error(), "proof tip") {
					t.Fatalf("mismatched stored proof close error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("matching stored proof rejected at close: %v", err)
			}
		})
	}
}

func initProofSourceRepo(t *testing.T, origin string) string {
	t.Helper()
	repo := t.TempDir()
	runProofSourceGit(t, repo, "init", "-b", "main")
	runProofSourceGit(t, repo, "config", "user.email", "test@example.invalid")
	runProofSourceGit(t, repo, "config", "user.name", "Compound Test")
	runProofSourceGit(t, repo, "remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte(origin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runProofSourceGit(t, repo, "add", "seed.txt")
	runProofSourceGit(t, repo, "commit", "-m", "seed")
	return repo
}

func runProofSourceGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func proofSourceGitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
