package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

const (
	hardenTestDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hardenTestDigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hardenTestCommit  = "0123456789abcdef0123456789abcdef01234567"
	hardenTestLater   = "fedcba9876543210fedcba9876543210fedcba98"
	hardenTestBase    = "1111111111111111111111111111111111111111"
)

func hardenTestProof() BenchCompareProof {
	return BenchCompareProof{
		Suite:              "capability-suite",
		InputDigest:        "input-digest",
		BeforeReportSHA256: hardenTestDigestA,
		AfterReportSHA256:  hardenTestDigestB,
		BeforeBuildSHA:     "before-build",
		AfterBuildSHA:      hardenTestCommit[:9],
		BeforeGraphHash:    "graph-before",
		AfterGraphHash:     "graph-after",
		Accept:             true,
	}
}

// writeHardenTestProofBytes stores a record directly, for shapes the recorder refuses to write.
func writeHardenTestProofBytes(t *testing.T, s *Store, proof BenchCompareProof) string {
	t.Helper()
	proof.Schema = BenchCompareProofSchema
	body, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	path := BenchCompareProofPath(s.home, digest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return benchCompareAcceptRefPrefix + digest
}

// runToCloseable creates a work (optionally carrying a target surface) and walks
// it ready -> claimed -> running so it can be closed to a terminal outcome.
func runToCloseable(t *testing.T, s *Store, targetSurface, predicate string) string {
	t.Helper()
	w, err := s.Create(CreateInput{
		Owner:            "compound-harness",
		Actor:            "operator-test",
		SuccessPredicate: predicate,
		TargetSurface:    targetSurface,
	}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.TargetSurface != targetSurface {
		t.Fatalf("create should preserve target_surface %q, got %q", targetSurface, w.TargetSurface)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	return w.ID
}

// TestShouldHardenStampedOnlyOnAcceptedHarnessEditClose is falsifier 6: an
// ACCEPTED (completed) close of an explicit harness-edit envelope (one with a
// TargetSurface) stamps SecondOrder.ShouldHarden=true ONLY when it cites a recorded
// accepted comparison — "completed" cannot self-certify a harness edit;
// a rejected/failed close of the same envelope stamps false but STILL emits Receipt
// + Transition; and an ordinary work with no TargetSurface never hardens on close.
func TestShouldHardenStampedOnlyOnAcceptedHarnessEditClose(t *testing.T) {
	t.Parallel()
	t.Run("accepted harness-edit close hardens", func(t *testing.T) {
		s := newStore(t)
		id := runToCloseable(t, s, "capability_edges", "apply the accepted harness edit")
		_, ref, err := s.RecordBenchCompare(id, hardenTestProof())
		if err != nil {
			t.Fatalf("record comparison: %v", err)
		}
		res, err := s.Close(id, CloseInput{
			Outcome:   "completed",
			MoveType:  "control_plane_repair",
			Summary:   "harness edit accepted and applied",
			Changed:   true,
			ProofRefs: []string{"gate:harness-edit-accepted", ref},
		}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if !res.Transition.SecondOrder.ShouldHarden {
			t.Fatalf("accepted harness-edit close must stamp should_harden=true: %+v", res.Transition.SecondOrder)
		}
	})

	t.Run("rejected harness-edit close does not harden but still records", func(t *testing.T) {
		s := newStore(t)
		id := runToCloseable(t, s, "capability_edges", "reject the harness edit")
		res, err := s.Close(id, CloseInput{
			Outcome:   "failed",
			MoveType:  "control_plane_repair",
			Summary:   "harness edit rejected on review",
			ProofRefs: []string{"gate:harness-edit-rejected"},
		}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if res.Transition.SecondOrder.ShouldHarden {
			t.Fatalf("rejected harness-edit close must NOT harden: %+v", res.Transition.SecondOrder)
		}
		// A rejection is still logged: Receipt + Transition are emitted.
		if res.Receipt.ID == "" || res.Transition.ID == "" {
			t.Fatalf("rejected harness-edit close must still emit Receipt+Transition: %+v", res)
		}
	})

	// The outcome gate on its own: a real accepted comparison must not harden a close
	// that reports the edit failed.
	t.Run("failed close citing an accepted comparison does not harden", func(t *testing.T) {
		s := newStore(t)
		id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
		_, ref, err := s.RecordBenchCompare(id, hardenTestProof())
		if err != nil {
			t.Fatalf("record comparison: %v", err)
		}
		res, err := s.Close(id, CloseInput{
			Outcome:   "failed",
			MoveType:  "control_plane_repair",
			Summary:   "harness edit failed after the comparison",
			ProofRefs: []string{"gate:harness-edit-rejected", ref},
		}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if res.Transition.SecondOrder.ShouldHarden {
			t.Fatal("a failed close must not harden, whatever it cites")
		}
	})

	t.Run("ordinary work never hardens on close", func(t *testing.T) {
		s := newStore(t)
		id := runToCloseable(t, s, "", "ordinary work with no target surface")
		res, err := s.Close(id, CloseInput{
			Outcome:   "completed",
			MoveType:  "control_plane_repair",
			Summary:   "ordinary control-plane repair",
			Changed:   true,
			ProofRefs: []string{"gate:ordinary"},
		}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if res.Transition.SecondOrder.ShouldHarden {
			t.Fatalf("ordinary (no target_surface) close must not harden: %+v", res.Transition.SecondOrder)
		}
	})

	// "completed" alone must NOT harden: a bench-report-exists ref is not a bench-compare ACCEPT.
	// This is the gate that stops a self-certified close from marking the harness as hardened.
	t.Run("completed harness-edit close without bench-compare accept proof does not harden", func(t *testing.T) {
		s := newStore(t)
		id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
		res, err := s.Close(id, CloseInput{
			Outcome:   "completed",
			MoveType:  "control_plane_repair",
			Summary:   "harness edit applied",
			Changed:   true,
			ProofRefs: []string{"gate:harness-edit-accepted", "bench_report:capability-suite"},
		}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if res.Transition.SecondOrder.ShouldHarden {
			t.Fatalf("completed harness-edit close without bench_compare accept proof must NOT harden: %+v", res.Transition.SecondOrder)
		}
	})
}

// TestCloseRefusesCitedComparisonThatDoesNotResolve closes a real harness-edit work
// citing each reference a text test would have accepted. Each close must refuse with
// its reason and change nothing, because a terminal Transition cannot be corrected.
// The closer can then close unhardened on purpose by dropping the citation.
func TestCloseRefusesCitedComparisonThatDoesNotResolve(t *testing.T) {
	t.Parallel()
	storedAccept := func(t *testing.T, s *Store, change func(*BenchCompareProof)) string {
		t.Helper()
		proof := hardenTestProof()
		change(&proof)
		return writeHardenTestProofBytes(t, s, proof)
	}
	recordedRejection := func(t *testing.T, s *Store, id string) string {
		t.Helper()
		proof := hardenTestProof()
		proof.Accept = false
		path, ref, err := s.RecordBenchCompare(id, proof)
		if err != nil {
			t.Fatal(err)
		}
		if ref != "" {
			t.Fatalf("a rejected comparison must emit no proof ref, got %q", ref)
		}
		return benchCompareAcceptRefPrefix + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "sha256-"), ".json")
	}
	tests := map[string]struct {
		refs func(t *testing.T, s *Store, id string) []string
		want string
	}{
		"fabricated digest": {
			refs: func(*testing.T, *Store, string) []string {
				return []string{benchCompareAcceptRefPrefix + hardenTestDigestA}
			},
			want: "no recorded comparison",
		},
		"suite label from an unrecorded compare": {
			refs: func(*testing.T, *Store, string) []string { return []string{"bench_compare_accept:capability-suite"} },
			want: "not a recorded comparison",
		},
		"rejected comparison": {
			refs: func(t *testing.T, s *Store, id string) []string { return []string{recordedRejection(t, s, id)} },
			want: "rejected",
		},
		"accepted beside a rejected one": {
			refs: func(t *testing.T, s *Store, id string) []string {
				_, accepted, err := s.RecordBenchCompare(id, hardenTestProof())
				if err != nil {
					t.Fatal(err)
				}
				return []string{accepted, recordedRejection(t, s, id)}
			},
			want: "rejected",
		},
		"comparison of another work": {
			refs: func(t *testing.T, s *Store, _ string) []string {
				other := runToCloseable(t, s, "capability_edges", "a different harness edit")
				_, ref, err := s.RecordBenchCompare(other, hardenTestProof())
				if err != nil {
					t.Fatal(err)
				}
				return []string{ref}
			},
			want: "judged work",
		},
		"comparison of another surface": {
			refs: func(t *testing.T, s *Store, id string) []string {
				return []string{storedAccept(t, s, func(p *BenchCompareProof) { p.WorkID, p.TargetSurface = id, "another_surface" })}
			},
			want: "judged surface",
		},
		"stored comparison of one report with itself": {
			refs: func(t *testing.T, s *Store, id string) []string {
				return []string{storedAccept(t, s, func(p *BenchCompareProof) {
					p.WorkID, p.TargetSurface = id, "capability_edges"
					p.AfterReportSHA256 = p.BeforeReportSHA256
				})}
			},
			want: "measured no edit",
		},
		"tampered bytes": {
			refs: func(t *testing.T, s *Store, id string) []string {
				ref := recordedRejection(t, s, id)
				path := BenchCompareProofPath(s.home, strings.TrimPrefix(ref, benchCompareAcceptRefPrefix))
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				flipped := strings.Replace(string(data), `"accept": false`, `"accept": true`, 1)
				if flipped == string(data) {
					t.Fatal("fixture did not flip the verdict")
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(flipped), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{ref}
			},
			want: "do not match the digest",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
			refs := append([]string{"gate:harness-edit-accepted"}, tc.refs(t, s, id)...)
			closing := CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Summary: "harness edit applied", Changed: true, ProofRefs: refs}
			if _, err := s.Close(id, closing, fixedNow); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("close error = %v, want it to contain %q", err, tc.want)
			}
			w, err := s.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if w.State != "running" {
				t.Fatalf("a refused close must change nothing, state = %s", w.State)
			}
			closing.ProofRefs = []string{"gate:harness-edit-accepted"}
			res, err := s.Close(id, closing, fixedNow)
			if err != nil {
				t.Fatalf("dropping the citation must allow an unhardened close: %v", err)
			}
			if res.Transition.SecondOrder.ShouldHarden {
				t.Fatal("a close with no comparison must not harden")
			}
		})
	}
}

// A ref that only contains the right words is not a citation at all: the close
// completes, and nothing hardens.
func TestLabelsWithTheRightWordsAreNotCitations(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"bench_compare:not_accepted:fixture", "check:bench_compare_smoke", "bench_compare:accept:capability-suite", "BENCH_COMPARE_ACCEPT:capability-suite"} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
			res, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted", label), fixedNow)
			if err != nil {
				t.Fatalf("a label must not refuse the close: %v", err)
			}
			if res.Transition.SecondOrder.ShouldHarden {
				t.Fatal("a label must not harden")
			}
		})
	}
}

// hardenTestAttached leaves a work verifying with ref attached as a verification.
func hardenTestAttached(t *testing.T, s *Store, id, ref string) {
	t.Helper()
	if _, err := s.Advance(id, AdvanceInput{To: "verifying", Actor: "codex", VerificationID: ref}, fixedNow); err != nil {
		t.Fatalf("attach %s: %v", ref, err)
	}
}

func hardenTestClose(refs ...string) CloseInput {
	return CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Summary: "harness edit applied", Changed: true, Actor: "codex", ProofRefs: refs}
}

// Both close doors judge the receipt's refs, so they must give one answer.
func TestCloseDoorsAgreeOnAnAttachedComparison(t *testing.T) {
	t.Parallel()
	for door, closeWork := range map[string]func(*Store, string) (CloseResult, error){
		"work close": func(s *Store, id string) (CloseResult, error) {
			return s.Close(id, hardenTestClose("gate:harness-edit-accepted"), fixedNow)
		},
		"dispatch close": func(s *Store, id string) (CloseResult, error) {
			closed, err := s.DispatchClose(fixedNow, DispatchCloseOptions{Actor: "codex", WorkID: id})
			if err != nil {
				return CloseResult{}, err
			}
			if closed.CloseResult == nil {
				return CloseResult{}, errors.New("dispatch closed nothing: " + closed.NoAction)
			}
			return *closed.CloseResult, nil
		},
	} {
		t.Run(door, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
			_, ref, err := s.RecordBenchCompare(id, hardenTestProof())
			if err != nil {
				t.Fatal(err)
			}
			hardenTestAttached(t, s, id, ref)
			res, err := closeWork(s, id)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Transition.SecondOrder.ShouldHarden {
				t.Fatal("a valid attached comparison must harden through every door")
			}
			if !slices.Contains(res.Receipt.ProofRefs, ref) {
				t.Fatal("the receipt must list the comparison that hardened it")
			}
		})
	}
}

// An accepted comparison cannot outvote a rejection attached to the same work, and
// the closer can still finish the work, unhardened, by citing nothing.
func TestAttachedRejectionCannotBeOutvoted(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	rejected := hardenTestProof()
	rejected.Accept = false
	path, _, err := s.RecordBenchCompare(id, rejected)
	if err != nil {
		t.Fatal(err)
	}
	rejectedRef := benchCompareAcceptRefPrefix + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "sha256-"), ".json")
	_, acceptedRef, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatal(err)
	}
	hardenTestAttached(t, s, id, rejectedRef)
	if _, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted", acceptedRef), fixedNow); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("close error = %v, want the rejection named", err)
	}
	// Attached side by side, the accepted one still does not outvote the rejection.
	later := fixedNow.Add(time.Minute)
	if _, err := s.Advance(id, AdvanceInput{To: "verifying", Actor: "codex", VerificationID: acceptedRef}, later); err != nil {
		t.Fatalf("attach %s: %v", acceptedRef, err)
	}
	res, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted"), later.Add(time.Minute))
	if err != nil {
		t.Fatalf("citing nothing must let the closer finish: %v", err)
	}
	if res.Transition.SecondOrder.ShouldHarden {
		t.Fatal("a work with a rejection attached must not harden")
	}
	if !slices.Contains(res.Receipt.ProofRefs, rejectedRef) {
		t.Fatal("the receipt must still list the rejection")
	}
}

// A plan for a work already closed the same way replays the close, which never reads
// the record again, so a record that has since gone must not block it.
func TestClosePlanForAClosedWorkDoesNotReadTheRecord(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	path, ref, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatal(err)
	}
	hardenTestAttached(t, s, id, ref)
	closed, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted", ref), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The record is gone, and the replay still returns the close it made.
	replay, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted", ref), fixedNow)
	if err != nil {
		t.Fatalf("a replay must not read the record again: %v", err)
	}
	if replay.Transition.ID != closed.Transition.ID || !replay.Transition.SecondOrder.ShouldHarden {
		t.Fatalf("a replay must return the same hardened transition: %+v", replay.Transition.SecondOrder)
	}
	plan, err := s.ClosePlan(id, fixedNow, ClosePlanOptions{Actor: "codex", Outcome: "completed", MoveType: "control_plane_repair"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.NoAction, "comparison") {
		t.Fatalf("a closed work must not be judged again: %q", plan.NoAction)
	}
}

// One bad attached ref must not stop dispatch. The plan shows the refusal first, so
// dispatch passes over the work and closes what is ready behind it.
func TestDispatchPassesOverAWorkItCannotClose(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	blocked := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	hardenTestAttached(t, s, blocked, "bench_compare_accept:capability-suite")
	ready := runToCloseable(t, s, "", "ordinary ready work")
	hardenTestAttached(t, s, ready, "check:unrelated-ready-work")

	plan, err := s.ClosePlan(blocked, fixedNow, ClosePlanOptions{Actor: "codex", Outcome: "completed", MoveType: "control_plane_repair"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.NoAction, "not a recorded comparison") || !strings.Contains(plan.NoAction, "unhardened") || plan.Status == "close_policy_allowed" || len(plan.CloseCommand) != 0 {
		t.Fatalf("the plan must show the refusal and the way past it: status=%s no_action=%q command=%v", plan.Status, plan.NoAction, plan.CloseCommand)
	}
	closed, err := s.DispatchClose(fixedNow, DispatchCloseOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("a bad attached ref must not stop dispatch: %v", err)
	}
	if closed.CloseResult == nil || closed.CloseResult.Work.ID != ready {
		t.Fatalf("dispatch must close the ready work behind it: %+v", closed)
	}
	if w, err := s.Get(blocked); err != nil || w.State != "verifying" {
		t.Fatalf("the blocked work must stay open: %+v %v", w.State, err)
	}
	res, err := s.Close(blocked, hardenTestClose("gate:harness-edit-accepted"), fixedNow)
	if err != nil {
		t.Fatalf("citing nothing must let the closer finish: %v", err)
	}
	if res.Transition.SecondOrder.ShouldHarden {
		t.Fatal("a bad attached ref must not harden")
	}
}

// A record that cannot be read says nothing about the edit, so even a close that
// cites nothing must wait for it rather than settle for a permanent false.
func TestCloseRefusesWhenAnAttachedRecordCannotBeRead(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the file mode this test relies on")
	}
	s := newStore(t)
	id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	path, ref, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatal(err)
	}
	hardenTestAttached(t, s, id, ref)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted"), fixedNow); err == nil || !strings.Contains(err.Error(), "read recorded comparison") {
		t.Fatalf("close error = %v, want a read refusal", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.Close(id, hardenTestClose("gate:harness-edit-accepted"), fixedNow)
	if err != nil {
		t.Fatalf("close after the read recovers: %v", err)
	}
	if !res.Transition.SecondOrder.ShouldHarden {
		t.Fatal("the close must harden once the record can be read")
	}
}

// A failed read must not become a permanent false: the close refuses, and the same
// close hardens once the record can be read.
func TestCloseSurvivesAFailedComparisonRead(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the file mode this test relies on")
	}
	s := newStore(t)
	id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	path, ref, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	closing := CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Summary: "harness edit applied", Changed: true, ProofRefs: []string{"gate:harness-edit-accepted", ref}}
	if _, err := s.Close(id, closing, fixedNow); err == nil || !strings.Contains(err.Error(), "read recorded comparison") {
		t.Fatalf("close error = %v, want a read refusal", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.Close(id, closing, fixedNow)
	if err != nil {
		t.Fatalf("close after the read recovers: %v", err)
	}
	if !res.Transition.SecondOrder.ShouldHarden {
		t.Fatal("the same close must harden once the record can be read")
	}
}

// TestBenchCompareBindsTheEdit pins what a comparison must measure: the two sides of
// this edit. A source-delivered work binds them to its base and its current candidate.
func TestBenchCompareBindsTheEdit(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	_, ref, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatal(err)
	}
	envelopeWith := func(delivery *envelope.GitDeliveryContract, artifacts ...string) envelope.WorkEnvelope {
		return envelope.WorkEnvelope{ID: id, TargetSurface: "capability_edges", GitDelivery: delivery, ArtifactIDs: artifacts}
	}
	baseBefore := func(p *BenchCompareProof) { p.BeforeBuildSHA = hardenTestBase[:9] }
	delivery := &envelope.GitDeliveryContract{ExpectedBaseSHA: hardenTestBase}
	for name, tc := range map[string]struct {
		w       envelope.WorkEnvelope
		change  func(*BenchCompareProof)
		wantErr string
	}{
		"measured candidate, no delivery":      {w: envelopeWith(nil, "commit:"+hardenTestCommit)},
		"no candidate and no delivery":         {w: envelopeWith(nil)},
		"delivery base and candidate":          {w: envelopeWith(delivery, "commit:"+hardenTestCommit), change: baseBefore},
		"full build ids":                       {w: envelopeWith(delivery, "commit:"+hardenTestCommit), change: func(p *BenchCompareProof) { p.BeforeBuildSHA, p.AfterBuildSHA = hardenTestBase, hardenTestCommit }},
		"candidate moved":                      {w: envelopeWith(nil, "commit:"+hardenTestCommit, "commit:"+hardenTestLater), wantErr: "stale comparison"},
		"malformed candidate":                  {w: envelopeWith(nil, "commit:not-a-sha"), wantErr: "malformed"},
		"delivery with no candidate":           {w: envelopeWith(delivery), change: baseBefore, wantErr: "no candidate commit"},
		"old report as the before side":        {w: envelopeWith(delivery, "commit:"+hardenTestCommit), wantErr: "delivery base"},
		"dev build cannot name a candidate":    {w: envelopeWith(nil, "commit:"+hardenTestCommit), change: func(p *BenchCompareProof) { p.AfterBuildSHA = "dev" }, wantErr: "stale comparison"},
		"short build cannot name a candidate":  {w: envelopeWith(nil, "commit:"+hardenTestCommit), change: func(p *BenchCompareProof) { p.AfterBuildSHA = hardenTestCommit[:3] }, wantErr: "stale comparison"},
		"same build and graph measure no edit": {w: envelopeWith(nil), change: func(p *BenchCompareProof) { p.AfterBuildSHA, p.AfterGraphHash = p.BeforeBuildSHA, p.BeforeGraphHash }, wantErr: "measured no edit"},
		"one commit spelled two ways is no edit": {w: envelopeWith(nil), change: func(p *BenchCompareProof) {
			p.BeforeBuildSHA, p.AfterBuildSHA, p.AfterGraphHash = hardenTestCommit[:9], hardenTestCommit, p.BeforeGraphHash
		}, wantErr: "measured no edit"},
		"two different commits are an edit": {w: envelopeWith(nil), change: func(p *BenchCompareProof) {
			p.BeforeBuildSHA, p.AfterBuildSHA, p.AfterGraphHash = hardenTestBase[:9], hardenTestCommit, p.BeforeGraphHash
		}},
		"same build, new graph is a real edit": {w: envelopeWith(nil), change: func(p *BenchCompareProof) { p.AfterBuildSHA = p.BeforeBuildSHA }},
	} {
		t.Run(name, func(t *testing.T) {
			proof := hardenTestProof()
			proof.WorkID, proof.TargetSurface, proof.Schema = id, "capability_edges", BenchCompareProofSchema
			if tc.change != nil {
				tc.change(&proof)
			}
			err := proof.bindingError(tc.w)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("must bind: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
	if err := s.resolveBenchCompareAccept(ref, envelopeWith(nil, "commit:"+hardenTestLater)); err == nil {
		t.Fatal("resolving must apply the binding, not only recording")
	}
}

func TestRecordBenchCompareRefusesWhatItCannotProve(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ordinary := runToCloseable(t, s, "", "ordinary work")
	if _, _, err := s.RecordBenchCompare(ordinary, hardenTestProof()); err == nil {
		t.Fatal("a work with no target surface must refuse a comparison")
	}
	edit := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	incomplete := hardenTestProof()
	incomplete.InputDigest = ""
	if _, _, err := s.RecordBenchCompare(edit, incomplete); err == nil {
		t.Fatal("a comparison with no evaluation identity must refuse")
	}
	// An accepted comparison no close could honor gets no ref, and leaves no record.
	selfCompare := hardenTestProof()
	selfCompare.AfterReportSHA256 = selfCompare.BeforeReportSHA256
	if _, ref, err := s.RecordBenchCompare(edit, selfCompare); err == nil || !strings.Contains(err.Error(), "measured no edit") || ref != "" {
		t.Fatalf("a self-comparison must refuse: ref=%q err=%v", ref, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(BenchCompareProofPath(s.home, hardenTestDigestA))); err == nil && len(entries) != 0 {
		t.Fatalf("refused comparisons must leave no record: %d", len(entries))
	}
	if _, err := s.Close(edit, CloseInput{Outcome: "failed", MoveType: "control_plane_repair", Summary: "rejected", ProofRefs: []string{"gate:harness-edit-rejected"}}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordBenchCompare(edit, hardenTestProof()); err == nil {
		t.Fatal("a closed work must refuse a comparison")
	}
}

func TestRecordBenchCompareReplaysIdenticalEvidence(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	id := runToCloseable(t, s, "capability_edges", "apply the harness edit")
	firstPath, firstRef, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatal(err)
	}
	secondPath, secondRef, err := s.RecordBenchCompare(id, hardenTestProof())
	if err != nil {
		t.Fatalf("identical evidence must replay: %v", err)
	}
	if firstPath != secondPath || firstRef != secondRef {
		t.Fatalf("replay moved evidence: %s/%s vs %s/%s", firstPath, firstRef, secondPath, secondRef)
	}
	changed := hardenTestProof()
	changed.AfterGraphHash = "graph-revised"
	thirdPath, _, err := s.RecordBenchCompare(id, changed)
	if err != nil {
		t.Fatal(err)
	}
	if thirdPath == firstPath {
		t.Fatal("different evidence must keep its own record")
	}
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("earlier evidence must survive: %v", err)
	}
}
