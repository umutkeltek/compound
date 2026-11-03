package work

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// verifierChain is a linear history c[0] <- c[1] <- ... ; ancestry is index order.
func verifierChain(n int) ([]string, VerifierAncestry) {
	chain := make([]string, n)
	index := map[string]int{}
	for i := range chain {
		chain[i] = fmt.Sprintf("%040x", i+1)
		index[chain[i]] = i
	}
	return chain, func(a, b string) (bool, error) {
		ia, okA := index[a]
		ib, okB := index[b]
		if !okA || !okB {
			return false, fmt.Errorf("unknown commit %s or %s", a, b)
		}
		return ia <= ib, nil
	}
}

func verifierRow(at time.Time, sha, run, verdict, kind string) VerifierVerdictRow {
	return VerifierVerdictRow{FinishedAt: at, SHA: sha, RunID: run, Verdict: verdict, Kind: kind, Mode: "test"}
}

func TestVerifierVerdictRule(t *testing.T) {
	c, anc := verifierChain(6)
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	const runA, runB, runC = "20261103T100000Z-aaaaaa", "20261103T110000Z-bbbbbb", "20261103T120000Z-cccccc"
	tip, target := c[2], c[5]
	cases := []struct {
		name    string
		ev      VerifierVerdictEvidence
		status  string
		ref     string
		message string
	}{
		{
			name:   "green at a descendant on the target covers the tip",
			ev:     VerifierVerdictEvidence{Rows: []VerifierVerdictRow{verifierRow(t0, c[4], runA, "green", "main")}},
			status: VerifierVerdictCovered, ref: VerifierVerdictRef(runA, c[4]),
		},
		{
			name:   "green exactly at the tip covers it",
			ev:     VerifierVerdictEvidence{Rows: []VerifierVerdictRow{verifierRow(t0, c[2], runA, "green_after_retry", "main")}},
			status: VerifierVerdictCovered, ref: VerifierVerdictRef(runA, c[2]),
		},
		{
			name:    "green only before the tip does not cover it",
			ev:      VerifierVerdictEvidence{Rows: []VerifierVerdictRow{verifierRow(t0, c[1], runA, "green", "main")}},
			status:  VerifierVerdictNotVerified,
			message: "the remote verifier has not verified this tip yet",
		},
		{
			name: "green at the target tip covers it",
			ev: VerifierVerdictEvidence{Rows: []VerifierVerdictRow{
				verifierRow(t0, c[5], runA, "green", "main"),
			}},
			status: VerifierVerdictCovered, ref: VerifierVerdictRef(runA, c[5]),
		},
		{
			name: "red, killed, and non-main greens never cover",
			ev: VerifierVerdictEvidence{Rows: []VerifierVerdictRow{
				verifierRow(t0, c[4], runA, "red", "main"),
				verifierRow(t0, c[4], runB, "killed", "main"),
				verifierRow(t0, c[4], runC, "green", "target"),
				verifierRow(t0, c[4], runC, "green", "retry"),
			}},
			status:  VerifierVerdictNotVerified,
			message: "the remote verifier has not verified this tip yet",
		},
		{
			name: "red culprit range containing the tip refuses",
			ev: VerifierVerdictEvidence{
				Rows: []VerifierVerdictRow{verifierRow(t0, c[1], runA, "green", "main"), verifierRow(t0.Add(time.Hour), c[3], runB, "red", "main")},
				Red:  &VerifierRed{SHA: c[3], Since: t0.Add(2 * time.Hour), Ranges: []VerifierCulpritRange{{Package: "compound/internal/x", LoExclusive: c[1], HiInclusive: c[3]}}},
			},
			status:  VerifierVerdictRedCulprit,
			message: "red culprit range of compound/internal/x",
		},
		{
			name: "red culprit range containing the tip refuses even over an older covering green",
			ev: VerifierVerdictEvidence{
				Rows: []VerifierVerdictRow{verifierRow(t0, c[2], runA, "green", "main")},
				Red:  &VerifierRed{SHA: c[4], Since: t0.Add(time.Hour), Ranges: []VerifierCulpritRange{{Package: "compound/internal/x", HiInclusive: c[4]}}},
			},
			status: VerifierVerdictRedCulprit,
		},
		{
			name: "a later covering green supersedes the red culprit",
			ev: VerifierVerdictEvidence{
				Rows: []VerifierVerdictRow{verifierRow(t0.Add(3*time.Hour), c[5], runC, "green", "main")},
				Red:  &VerifierRed{SHA: c[3], Since: t0.Add(2 * time.Hour), Ranges: []VerifierCulpritRange{{Package: "compound/internal/x", LoExclusive: c[1], HiInclusive: c[3]}}},
			},
			status: VerifierVerdictCovered, ref: VerifierVerdictRef(runC, c[5]),
		},
		{
			name: "a red range after the tip does not refuse a covering green",
			ev: VerifierVerdictEvidence{
				Rows: []VerifierVerdictRow{verifierRow(t0, c[2], runA, "green", "main")},
				Red:  &VerifierRed{SHA: c[5], Since: t0.Add(time.Hour), Ranges: []VerifierCulpritRange{{Package: "compound/internal/x", LoExclusive: c[3], HiInclusive: c[5]}}},
			},
			status: VerifierVerdictCovered, ref: VerifierVerdictRef(runA, c[2]),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecideVerifierVerdict(tc.ev, tip, target, anc)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.status || got.Ref != tc.ref || !strings.Contains(got.Reason, tc.message) {
				t.Fatalf("decision = %+v; want status %s ref %q reason containing %q", got, tc.status, tc.ref, tc.message)
			}
		})
	}

	// The target bounds coverage: a green at c[5] does not cover when the target is c[4].
	got, err := DecideVerifierVerdict(VerifierVerdictEvidence{Rows: []VerifierVerdictRow{verifierRow(t0, c[5], runA, "green", "main")}}, tip, c[4], anc)
	if err != nil || got.Status != VerifierVerdictNotVerified {
		t.Fatalf("green beyond target: %+v %v", got, err)
	}

	// A cited older green still covering the tip validates; a cited green that
	// does not cover the tip does not, even when another row covers it.
	ev := VerifierVerdictEvidence{Rows: []VerifierVerdictRow{verifierRow(t0, c[1], runA, "green", "main"), verifierRow(t0, c[3], runB, "green", "main"), verifierRow(t0.Add(time.Hour), c[4], runC, "green", "main")}}
	if _, err := ValidateVerifierVerdictRef(ev, VerifierVerdictRef(runB, c[3]), tip, target, anc); err != nil {
		t.Fatalf("older covering green must validate: %v", err)
	}
	if _, err := ValidateVerifierVerdictRef(ev, VerifierVerdictRef(runA, c[1]), tip, target, anc); err == nil {
		t.Fatal("a cited green before the tip must not validate")
	}
	if _, err := ValidateVerifierVerdictRef(ev, "verifier-verdict:bogus:"+c[3], tip, target, anc); err == nil {
		t.Fatal("malformed ref must not validate")
	}
}

func TestLoadVerifierVerdictEvidenceReadsVerifierFiles(t *testing.T) {
	dir := t.TempDir()
	if ev, err := LoadVerifierVerdictEvidence(filepath.Join(dir, "absent")); err != nil || len(ev.Rows) != 0 || ev.Red != nil {
		t.Fatalf("absent dir must be empty evidence: %+v %v", ev, err)
	}
	sha := strings.Repeat("a", 40)
	tsv := "#finished_at\tsha\trun_id\tverdict\tkind\tmode\tduration_s\tfailed_packages\n" +
		"2026-11-02T14:54:26Z\t" + sha + "\t20261102T140424Z-8e6123\tgreen\tmain\ttest\t2826\t-\n" +
		"2026-11-02T19:44:01Z\t" + sha + "\t20261102T185636Z-1c4779\tred\tmain\ttest\t2650\tcompound/internal/arch,compound/internal/act\n"
	red := `{"packages":{"compound/internal/arch":{"commits":1,"hi_inclusive":"` + sha + `","lo_exclusive":null,"note":"x"}},"sha":"` + sha + `","since":"2026-11-02T23:11:55Z","verified_base":null}`
	writeLandingFile(t, filepath.Join(dir, VerifierVerdictsFileName), tsv)
	writeLandingFile(t, filepath.Join(dir, VerifierRedFileName), red)
	ev, err := LoadVerifierVerdictEvidence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Rows) != 2 || !ev.Rows[0].green() || ev.Rows[1].green() || len(ev.Rows[1].FailedPackages) != 2 {
		t.Fatalf("rows = %+v", ev.Rows)
	}
	if ev.Red == nil || len(ev.Red.Ranges) != 1 || ev.Red.Ranges[0].LoExclusive != "" || ev.Red.Ranges[0].HiInclusive != sha {
		t.Fatalf("red = %+v", ev.Red)
	}
	writeLandingFile(t, filepath.Join(dir, VerifierVerdictsFileName), "not-a-time\t"+sha+"\tr\tgreen\tmain\ttest\t1\n")
	if _, err := LoadVerifierVerdictEvidence(dir); err == nil {
		t.Fatal("a malformed verdict row must fail closed, not shorten history")
	}
}

// TestVerifierVerdictIsTheCanonicalCloseProof drives the real land-check and close
// path: a green verifier verdict at a descendant of the landed tip closes the work;
// a red culprit naming the tip, or no covering verdict, refuses the close.
func TestVerifierVerdictIsTheCanonicalCloseProof(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	type fixture struct {
		store              *Store
		workID             string
		repo, candidate, s string
	}
	setup := func(t *testing.T) fixture {
		t.Helper()
		repo, base, candidate := sharedLandingLandedRepo(t)
		// A later landing on main: the verifier tested main including the candidate.
		writeLandingFile(t, filepath.Join(repo, "later.txt"), "later\n")
		landingGit(t, repo, "add", "later.txt")
		landingGit(t, repo, "commit", "-m", "later landing")
		later := landingGitOutput(t, repo, "rev-parse", "HEAD")
		store, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "closes on the verifier verdict", GitDelivery: localGitDelivery(repo, base)}, now)
		if err != nil {
			t.Fatal(err)
		}
		activateLandingWork(t, store, w.ID, now)
		attachLandingCommit(t, store, w.ID, candidate, now.Add(3*time.Minute))
		return fixture{store: store, workID: w.ID, repo: repo, candidate: candidate, s: later}
	}
	writeVerdicts := func(t *testing.T, f fixture, rows ...string) {
		t.Helper()
		dir := VerifierVerdictsDir(f.store.home)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeLandingFile(t, filepath.Join(dir, VerifierVerdictsFileName), "#finished_at\tsha\trun_id\tverdict\tkind\tmode\tduration_s\tfailed_packages\n"+strings.Join(rows, ""))
	}
	attachVerifier := func(t *testing.T, f fixture) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := RunVerifierVerdictCLI(f.store, []string{f.workID, "--tip", f.candidate, "--attach", "--actor", "codex", "--json"}, &out, &errOut); code != 0 {
			t.Fatalf("verifier-verdict --attach exit %d: %s %s", code, out.String(), errOut.String())
		}
	}
	closeWork := func(f fixture, evidence string, at time.Time) error {
		_, err := f.store.Close(f.workID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "remote verifier verified", ProofRefs: []string{evidence}}, at)
		return err
	}
	const greenRun, redRun = "20261103T120000Z-a1b2c3", "20261103T130000Z-d4e5f6"

	t.Run("green at a descendant tip closes", func(t *testing.T) {
		f := setup(t)
		writeVerdicts(t, f, "2026-11-03T12:30:00Z\t"+f.s+"\t"+greenRun+"\tgreen\tmain\ttest\t2650\t-\n")
		attachVerifier(t, f)
		landed, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate, Record: true}, now.Add(5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if landed.Status != "landed" || landed.ProofVerificationRef != VerifierVerdictRef(greenRun, f.s) || landed.ProofHeadSHA != f.s {
			t.Fatalf("land-check = %+v", landed)
		}
		if err := closeWork(f, landed.EvidenceRef, now.Add(6*time.Minute)); err != nil {
			t.Fatalf("close on a green verifier verdict: %v", err)
		}
		if w, _ := f.store.Get(f.workID); w.State != "completed" {
			t.Fatalf("state = %s", w.State)
		}
	})

	t.Run("red culprit covering the tip refuses the close", func(t *testing.T) {
		f := setup(t)
		writeVerdicts(t, f, "2026-11-03T12:30:00Z\t"+f.s+"\t"+greenRun+"\tgreen\tmain\ttest\t2650\t-\n")
		attachVerifier(t, f)
		landed, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate, Record: true}, now.Add(5*time.Minute))
		if err != nil || landed.Status != "landed" {
			t.Fatalf("land-check = %+v %v", landed, err)
		}
		// After the record, the verifier names the candidate in a culprit range.
		red := fmt.Sprintf(`{"packages":{"compound/internal/x":{"hi_inclusive":%q,"lo_exclusive":null}},"sha":%q,"since":"2026-11-03T13:30:00Z"}`, f.s, f.s)
		writeLandingFile(t, filepath.Join(VerifierVerdictsDir(f.store.home), VerifierRedFileName), red)
		err = closeWork(f, landed.EvidenceRef, now.Add(6*time.Minute))
		if err == nil || !strings.Contains(err.Error(), "verifier_red_culprit") {
			t.Fatalf("close over a red culprit err = %v", err)
		}
		again, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate}, now.Add(7*time.Minute))
		if err != nil || again.Status != "verifier_red_culprit" {
			t.Fatalf("land-check over a red culprit = %+v %v", again, err)
		}
	})

	t.Run("no verdict covering the tip refuses the close", func(t *testing.T) {
		f := setup(t)
		// Before any verdict: land-check names the remote verifier path, not the laptop battery alone.
		before, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate}, now.Add(4*time.Minute))
		if err != nil || before.Status != "missing_source_bound_proof" || !strings.Contains(before.NextAction, "verifier verdict") {
			t.Fatalf("pre-verdict land-check = %+v %v", before, err)
		}
		var out, errOut bytes.Buffer
		if code := RunVerifierVerdictCLI(f.store, []string{f.workID, "--tip", f.candidate, "--attach", "--actor", "codex"}, &out, &errOut); code != 1 || !strings.Contains(out.String(), "the remote verifier has not verified this tip yet") {
			t.Fatalf("verifier-verdict without a verdict exit %d: %s %s", code, out.String(), errOut.String())
		}
		// A green verdict at the base (before the candidate) exists and attaches, but does not cover the tip.
		base := landingGitOutput(t, f.repo, "rev-parse", f.candidate+"^")
		writeVerdicts(t, f, "2026-11-03T12:30:00Z\t"+base+"\t"+greenRun+"\tgreen\tmain\ttest\t2650\t-\n")
		w, _ := f.store.Get(f.workID)
		if _, err := f.store.Advance(f.workID, AdvanceInput{To: w.State, Actor: "codex", VerificationID: VerifierVerdictRef(greenRun, base)}, now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		report, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate, Record: true}, now.Add(6*time.Minute))
		if err != nil || report.Status != "awaiting_verifier_verdict" || !strings.Contains(report.NextAction, "the remote verifier has not verified this tip yet") {
			t.Fatalf("uncovered land-check = %+v %v", report, err)
		}
		if err := closeWork(f, report.EvidenceRef, now.Add(7*time.Minute)); err == nil {
			t.Fatal("close without a covering verifier verdict must refuse")
		}
		// Attach refuses a ref that names no green run.
		if _, err := f.store.Advance(f.workID, AdvanceInput{To: w.State, Actor: "codex", VerificationID: VerifierVerdictRef(redRun, f.s)}, now.Add(8*time.Minute)); err == nil {
			t.Fatal("attaching an remote verifier ref with no green verdict row must refuse")
		}
	})
}
