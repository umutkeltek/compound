package work

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/envelope"
	"compound/internal/landlease"
	"compound/internal/runattempt"
)

var presubmitStampOrigin = func(store *runattempt.Store, id string) (runattempt.RunAttempt, error) {
	return store.SetOrigin(id, runattempt.OriginHarnessExecution)
}

const presubmitExecutionKind = "presubmit-execution"
const presubmitExecutionRefPrefix = "presubmit-execution:"

type PresubmitExecutionResult struct {
	ContainmentUncertain     bool                                 `json:"containment_uncertain,omitempty"`
	Kind                     string                               `json:"kind"`
	WorkID                   string                               `json:"work_id"`
	Actor                    string                               `json:"actor"`
	RunAttemptID             string                               `json:"run_attempt_id"`
	ActionIntentID           string                               `json:"action_intent_id"`
	PolicyDecisionID         string                               `json:"policy_decision_id"`
	Request                  PresubmitExecutionRequest            `json:"request"`
	ExpectedHead             string                               `json:"expected_head"`
	RepositoryFingerprint    string                               `json:"repository_fingerprint"`
	ExecutableSHA256         string                               `json:"executable_sha256"`
	Status                   string                               `json:"status"`
	FailureReason            string                               `json:"failure_reason,omitempty"`
	StartedAt                time.Time                            `json:"started_at"`
	CompletedAt              time.Time                            `json:"completed_at,omitempty"`
	Report                   *ProofCheckReport                    `json:"report,omitempty"`
	RecoveryActionIntentID   string                               `json:"recovery_action_intent_id,omitempty"`
	RecoveryPolicyDecisionID string                               `json:"recovery_policy_decision_id,omitempty"`
	RecoveredEmptyGroups     []int                                `json:"recovered_empty_groups,omitempty"`
	Holder                   string                               `json:"holder,omitempty"`
	ProofCoordination        *landlease.ProofCoordinationIdentity `json:"proof_coordination,omitempty"`
	LeasePrepared            bool                                 `json:"lease_prepared"`
	Quiescent                bool                                 `json:"quiescent"`
	Released                 bool                                 `json:"released"`
	SourceSnapshot           *gitrepository.Snapshot              `json:"source_snapshot,omitempty"`
	GateInventory            []ProofCheckStep                     `json:"gate_inventory,omitempty"`
	PresubmitPlan            *ProofCheckPresubmit                 `json:"presubmit_plan,omitempty"`
	InventoryDigest          string                               `json:"inventory_digest,omitempty"`
	Checkpoints              []PresubmitCheckpoint                `json:"checkpoints,omitempty"`
	ArtifactRef              string                               `json:"-"`
}

type presubmitExecutionContextKey struct{}

// PresubmitExecutionAuthority is detached metadata, not a mutable runtime view.
type PresubmitExecutionAuthority struct {
	RunAttemptID     string
	ActionIntentID   string
	PolicyDecisionID string
	SourceSnapshot   *gitrepository.Snapshot
}

func PresubmitExecutionFromContext(ctx context.Context) (PresubmitExecutionAuthority, bool) {
	out, ok := ctx.Value(presubmitExecutionContextKey{}).(*PresubmitExecutionResult)
	if !ok || out == nil {
		return PresubmitExecutionAuthority{}, false
	}
	view := PresubmitExecutionAuthority{RunAttemptID: out.RunAttemptID, ActionIntentID: out.ActionIntentID, PolicyDecisionID: out.PolicyDecisionID}
	if out.SourceSnapshot != nil {
		snapshot := *out.SourceSnapshot
		view.SourceSnapshot = &snapshot
	}
	return view, true
}

// RunPresubmitProducer is a fixed-purpose native admission owner. Its result is
// execution evidence, never a canonical reservation or verification.
func (s *Store) RunPresubmitProducer(ctx context.Context, request PresubmitExecutionRequest, produce func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error)) (out PresubmitExecutionResult, err error) {
	if ctx == nil || produce == nil {
		return out, fmt.Errorf("presubmit requires context and producer")
	}
	if request.Budget <= 0 || request.Budget > MaxPresubmitExecutionBudget || !presubmitFullSHA.MatchString(request.BaseRef) || request.Actor == "" || request.OutPath == "" {
		return out, fmt.Errorf("presubmit requires exact Work, actor, full base, output and positive bounded active budget")
	}
	if supportErr := presubmitGroupContainmentSupported(); supportErr != nil {
		return PresubmitExecutionResult{Kind: presubmitExecutionKind, WorkID: request.WorkID, Actor: request.Actor, Request: request, Status: "refused", FailureReason: supportErr.Error()}, supportErr
	}
	request.Args = append([]string(nil), request.Args...)
	request.Repo, err = filepath.Abs(request.Repo)
	if err != nil {
		return out, err
	}
	request.OutPath, err = filepath.Abs(request.OutPath)
	if err != nil {
		return out, err
	}
	executable, err := os.Executable()
	if err != nil {
		return out, err
	}
	body, err := os.ReadFile(executable)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(body)
	out = PresubmitExecutionResult{Kind: presubmitExecutionKind, WorkID: request.WorkID, Actor: request.Actor, Request: request, ExecutableSHA256: hex.EncodeToString(digest[:]), Status: "running", StartedAt: time.Now().UTC()}
	ctx, stopDeadline := context.WithTimeout(ctx, request.Budget)
	defer stopDeadline()
	ctx, stopRuntime := context.WithCancelCause(ctx)
	defer stopRuntime(nil)
	var runtime *presubmitRuntime
	producerReturned := false
	// Install terminalization before admission: every post-mint return, including
	// the first metadata write, reaches the same failure/result finalizer.
	defer func() {
		panicValue := recover()

		if panicValue != nil {
			err = errors.Join(err, fmt.Errorf("presubmit producer panic: %v", panicValue))
		}
		if out.RunAttemptID == "" {
			if err != nil {
				out.Status = "refused"
				out.FailureReason = err.Error()
				out.CompletedAt = time.Now().UTC()
			}
			if panicValue != nil {
				panic(panicValue)
			}
			return
		}
		if !producerReturned && err == nil {
			err = fmt.Errorf("presubmit producer did not complete")
		}
		if runtime != nil {
			err = errors.Join(err, runtime.finish())
		}
		err = errors.Join(err, context.Cause(ctx))
		out.CompletedAt = time.Now().UTC()
		out.Status = "succeeded"
		if err != nil {
			out.Status = "failed"
			out.FailureReason = err.Error()
		}
		if err != nil && out.Report != nil {
			out.Report.Status = "failed"
			out.Report.Warnings = append(out.Report.Warnings, err.Error())
		}
		artifactErr := s.persistPresubmitExecution(&out)
		if artifactErr != nil {
			err = errors.Join(err, artifactErr)
			out.Status = "failed"
			out.FailureReason = err.Error()
			err = errors.Join(err, s.checkpointPresubmitExecution(&out))
		}
		terminal := runattempt.StatusSucceeded
		if err != nil {
			terminal = runattempt.StatusFailed
		}
		terminalErr := s.completeProofAttempt(out.RunAttemptID, terminal, out.FailureReason, time.Now())
		if terminalErr != nil {
			err = errors.Join(err, terminalErr)
			out.Status = "failed"
			out.FailureReason = err.Error()
			err = errors.Join(err, s.checkpointPresubmitExecution(&out))
		}
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	err = s.withLifecycleMutation(func() error {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		w, getErr := s.Get(request.WorkID)
		if getErr != nil {
			return getErr
		}
		if w.State != "verifying" {
			return fmt.Errorf("native presubmit requires verifying Work, got %s", w.State)
		}
		if leaseErr := s.requireProofWorkLeaseOwner(w.ID, request.Actor, "native presubmit"); leaseErr != nil {
			return leaseErr
		}
		if w.GitDelivery == nil || w.GitDelivery.ExpectedBaseSHA != request.BaseRef {
			return fmt.Errorf("presubmit base must equal immutable Work delivery base")
		}
		_, head, found, valid := latestCommitArtifact(w.ArtifactIDs)
		if !found || !valid {
			return fmt.Errorf("presubmit requires latest full commit artifact")
		}
		out.ExpectedHead = head
		out.RepositoryFingerprint = w.GitDelivery.RepositoryFingerprint
		history, historyErr := s.runAttempts().ForWork(w.ID)
		if historyErr != nil {
			return historyErr
		}
		sort.Slice(history, func(i, j int) bool { return history[i].CreatedAt.Before(history[j].CreatedAt) })
		predecessor := ""
		for _, prior := range history {
			if prior.Harness != "proof-check-presubmit" {
				continue
			}
			if !runattempt.IsTerminal(prior.Status) {
				return fmt.Errorf("native presubmit already active for this Work: %s (%s)", prior.ID, prior.Status)
			}
			predecessor = prior.ID
		}
		binding, marshalErr := presubmitBindingDigest(out)
		if marshalErr != nil {
			return marshalErr
		}
		action := s.proofResourceAction(w, "presubmit:"+binding, request.Actor, "reserve", "", "", "", "", time.Now())
		action.Action = "execute_noncertifying_presubmit"
		action.ArgumentsHash = binding
		action.Preview = "run bounded native noncertifying presubmit; preserve canonical proof authority"
		action.RollbackPlan = "cancel and join owned subprocess groups; retain execution-only partial evidence"
		decision, evalErr := s.evaluateProofResourceAction(action, time.Now())
		if evalErr != nil {
			return evalErr
		}
		w, evalErr = s.recordActionLocked(w.ID, action, decision, time.Now())
		if evalErr != nil {
			return evalErr
		}
		out.ActionIntentID, out.PolicyDecisionID = action.ID, decision.ID
		attempt, mintErr := s.runAttempts().MintWithActionIntent(w.ID, request.Actor, "proof-check-presubmit", action.ID, time.Now())
		if mintErr != nil {
			return mintErr
		}
		out.RunAttemptID = attempt.ID
		if predecessor != "" {
			if _, e := s.runAttempts().SetPredecessor(attempt.ID, predecessor); e != nil {
				return e
			}
		}
		for _, prior := range history {
			if prior.Harness == "proof-check-presubmit" && !slices.Contains(w.RunAttemptIDs, prior.ID) {
				w.RunAttemptIDs = append(w.RunAttemptIDs, prior.ID)
			}
		}
		w.RunAttemptIDs = append(w.RunAttemptIDs, attempt.ID)
		if writeErr := s.writeJSON(s.workPath(w.ID), &w); writeErr != nil {
			return writeErr
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if _, err = presubmitStampOrigin(s.runAttempts(), out.RunAttemptID); err != nil {
		return out, err
	}
	if _, err = s.runAttempts().SetWorkspace(out.RunAttemptID, request.Repo); err != nil {
		return out, err
	}
	if _, err = s.runAttempts().SetCommand(out.RunAttemptID, request.command()); err != nil {
		return out, err
	}
	runtime, err = s.preparePresubmitRuntime(ctx, &out, stopRuntime)
	if err != nil {
		return out, err
	}
	ctx = context.WithValue(ctx, presubmitExecutionContextKey{}, &out)
	ctx = bindPresubmitCommandRunner(ctx, runtime)
	callbackRequest := request
	callbackRequest.Args = append([]string(nil), request.Args...)
	report, produceErr := produce(ctx, callbackRequest)
	producerReturned = true
	out.Report = &report
	err = errors.Join(produceErr, context.Cause(ctx))
	if err == nil {
		err = runtime.check(ctx)
	}
	if err == nil && report.Status != "ok" {
		err = fmt.Errorf("noncertifying presubmit report status %q", report.Status)
	}
	if err == nil {
		err = runtime.validateReport(report)
	}
	return out, err
}

func presubmitSameGeneration(w envelope.WorkEnvelope, out PresubmitExecutionResult) bool {
	return w.State == "verifying" && w.PolicyDecisionID == out.PolicyDecisionID && selectedLastAction(w) == out.ActionIntentID
}

func (s *Store) persistPresubmitExecution(out *PresubmitExecutionResult) error {
	return s.withLifecycleMutation(func() error {
		w, err := s.Get(out.WorkID)
		if err != nil {
			return err
		}
		same := presubmitSameGeneration(w, *out)
		var drift error
		if !same {
			drift = fmt.Errorf("presubmit authority changed; retained reverse-linked execution without modifying newer Work")
			out.Status = "failed"
			out.FailureReason = errors.Join(errors.New(out.FailureReason), drift).Error()
		}
		if err = s.checkpointPresubmitExecution(out); err != nil {
			return errors.Join(drift, err)
		}
		if !same {
			return drift
		}
		w.ArtifactIDs = append(w.ArtifactIDs, out.ArtifactRef)
		return s.writeJSON(s.workPath(w.ID), &w)
	})
}
