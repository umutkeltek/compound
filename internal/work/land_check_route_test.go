package work

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The remote verifier runs Compound's suite on Compound's target branch. An
// app repository that already landed its candidate was told to wait for that
// verifier, which never verifies it, so the work could never close. These
// tests pin the route by repository profile and keep every close gate.

const (
	routeCompoundGoMod = "module compound\n\ngo 1.24\n"
	// A synthetic Xcode declaration of the kind an app repository commits.
	routeXcodeProofContract = `{"schema_version":"compound.proof_consumers.v1","profile":"xcode_consumers_v1","consumers":[{"id":"ios-app","command":["bash","Tools/compound_proof.sh"],"dir":".","timeout_seconds":1800,"required_tools":["bash","xcodebuild"],"report_kind":"xcresult","required_tests":["ExampleTests/testLaunch()"]}]}`
)

// landedRepoWithProfile returns a clean repository whose checked-out main
// already contains the candidate. files are committed in the base, so the
// candidate carries them exactly as an enrolled repository would.
func landedRepoWithProfile(t *testing.T, files map[string]string) (string, string, string) {
	t.Helper()
	repo, _ := sharedLandingBaseRepo(t)
	if len(files) > 0 {
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			writeLandingFile(t, filepath.Join(repo, name), files[name])
			landingGit(t, repo, "add", name)
		}
		landingGit(t, repo, "commit", "-m", "enroll proof profile")
	}
	base := landingGitOutput(t, repo, "rev-parse", "HEAD")
	writeLandingFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
	landingGit(t, repo, "add", "feature.txt")
	landingGit(t, repo, "commit", "-m", "landed candidate")
	candidate := landingGitOutput(t, repo, "rev-parse", "HEAD")
	return repo, base, candidate
}

func routeLandedWork(t *testing.T, files map[string]string, now time.Time) (*Store, string, string, string, string) {
	t.Helper()
	repo, base, candidate := landedRepoWithProfile(t, files)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := landingDeliveryWork(t, store, localGitDelivery(repo, base), "landed candidate closes on its own proof", candidate, now)
	return store, w.ID, repo, base, candidate
}

func TestLandCheckRoutesLandedCandidateProofByRepositoryProfile(t *testing.T) {
	now := time.Date(2026, 11, 17, 22, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		files      map[string]string
		wantStatus string
		want       []string
		notWant    []string
	}{
		{
			name:       "compound profile waits for the remote verifier",
			files:      map[string]string{"go.mod": routeCompoundGoMod},
			wantStatus: "missing_source_bound_proof",
			want:       []string{"wait for a green verifier verdict", "compound work verifier-verdict"},
		},
		{
			name:       "declared native profile runs exact candidate proof",
			files:      map[string]string{CanonicalConsumerContractPath: routeXcodeProofContract},
			wantStatus: "awaiting_native_proof",
			want:       []string{"native exact-candidate proof", "xcode_consumers_v1", "compound proof-check --json --repo", "remote verifier covers only Compound"},
			notWant:    []string{"wait for a green verifier verdict", "verifier-verdict "},
		},
		{
			name:       "no profile is an explicit verification gap",
			wantStatus: "unsupported_proof_profile",
			want:       []string{"verification gap", "remote verifier covers only Compound", "cannot close it"},
			notWant:    []string{"wait for a green verifier verdict", "proof-check --json"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, workID, _, base, candidate := routeLandedWork(t, tc.files, now)
			report, err := store.LandCheck(workID, LandCheckOptions{CandidateTipSHA: candidate}, now.Add(5*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if !report.CandidateContainedInTarget || !report.CleanTarget {
				t.Fatalf("fixture must be a clean landed candidate: %+v", report)
			}
			if report.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q: %s", report.Status, tc.wantStatus, report.NextAction)
			}
			want := append([]string{"commit:" + candidate}, tc.want...)
			if tc.wantStatus == "awaiting_native_proof" {
				want = append(want, "--base "+base, "--work "+workID, "HEAD is exactly "+candidate)
			}
			for _, fragment := range want {
				if !strings.Contains(report.NextAction, fragment) {
					t.Fatalf("next action lacks %q: %s", fragment, report.NextAction)
				}
			}
			for _, fragment := range tc.notWant {
				if strings.Contains(report.NextAction, fragment) {
					t.Fatalf("next action routes to %q: %s", fragment, report.NextAction)
				}
			}

			// The dashboard shows the same route and the close stays refused.
			board := landingProjection(t, store, now.Add(6*time.Minute))
			item := findWorkbenchItem(t, board.Open, workID)
			if item.LandingStatus != "awaiting_proof" || item.NextAction != report.NextAction {
				t.Fatalf("workbench row = %q %q, want awaiting_proof with the land-check route", item.LandingStatus, item.NextAction)
			}
			if plan := dispatchClosePlanCandidate(item, "codex", "completed", "outward_delta"); plan.Action != "await_proof" || plan.Command != nil {
				t.Fatalf("unproved landed candidate must not plan a close: %+v", plan)
			}
		})
	}
}

// A green verdict row that names the app repository's own target commit is
// the most permissive wrong verifier: by ancestry alone it covers the tip.
// It must never certify a repository the verifier does not run.
func TestLandCheckRefusesRemoteVerifierForDeclaredNativeProfile(t *testing.T) {
	now := time.Date(2026, 11, 17, 22, 30, 0, 0, time.UTC)
	store, workID, repo, base, candidate := routeLandedWork(t, map[string]string{CanonicalConsumerContractPath: routeXcodeProofContract}, now)
	target := landingGitOutput(t, repo, "rev-parse", "main")
	const runID = "20261117T220000Z-abc123"
	writeLandingFile(t, filepath.Join(mustVerifierDir(t, store), VerifierVerdictsFileName),
		"#finished_at\tsha\trun_id\tverdict\tkind\tmode\tduration_s\tfailed_packages\n"+
			"2026-11-17T22:10:00Z\t"+target+"\t"+runID+"\tgreen\tmain\ttest\t2650\t-\n")
	w, err := store.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, AdvanceInput{To: w.State, Actor: "codex", VerificationID: VerifierVerdictRef(runID, target)}, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}

	report, err := store.LandCheck(workID, LandCheckOptions{CandidateTipSHA: candidate, Actor: "codex", Record: true}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "verifier_not_applicable" {
		t.Fatalf("wrong-verifier land-check status = %q, want verifier_not_applicable: %+v", report.Status, report)
	}
	for _, fragment := range []string{VerifierVerdictRef(runID, target), "cannot certify this repository", "native exact-candidate proof", "--base " + base, "--work " + workID} {
		if !strings.Contains(report.NextAction, fragment) {
			t.Fatalf("next action lacks %q: %s", fragment, report.NextAction)
		}
	}
	_, err = store.Close(workID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "wrong verifier", ProofRefs: []string{report.EvidenceRef}}, now.Add(6*time.Minute))
	if err == nil {
		t.Fatal("a remote verifier verdict must not close a native-profile repository")
	}
	if current, _ := store.Get(workID); current.State == "completed" {
		t.Fatalf("work closed on the wrong verifier: %s", current.State)
	}
}

func mustVerifierDir(t *testing.T, store *Store) string {
	t.Helper()
	dir := VerifierVerdictsDir(store.home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
