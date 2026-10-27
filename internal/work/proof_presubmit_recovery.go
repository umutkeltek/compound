package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"compound/internal/envelope"
	homepkg "compound/internal/home"
	"compound/internal/landlease"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

const PresubmitRecoveryFlag = "--presubmit-recover-execution"
const PresubmitRecoveryHelp = " --presubmit-recover-execution RATT_ID --work WORK --actor ACTOR --reason TEXT: recover only an authentic noncertifying native holder after barrier quiescence; never certifies source"

func (s *Store) readPresubmitExecution(attemptID string) (PresubmitExecutionResult, error) {
	attempt, err := s.runAttempts().Get(attemptID)
	if err != nil {
		return PresubmitExecutionResult{}, err
	}
	reader, err := homepkg.OpenArtifactReader(s.home)
	if err != nil {
		return PresubmitExecutionResult{}, err
	}
	for i := len(attempt.ProofRefs) - 1; i >= 0; i-- {
		ref := attempt.ProofRefs[i]
		if !strings.HasPrefix(ref, presubmitExecutionRefPrefix) {
			continue
		}
		body, err := reader.GetPrivateBounded(strings.TrimPrefix(ref, presubmitExecutionRefPrefix), 16<<20)
		if err != nil {
			return PresubmitExecutionResult{}, err
		}
		var result PresubmitExecutionResult
		if err = json.Unmarshal(body, &result); err != nil {
			return result, err
		}
		if result.Kind != presubmitExecutionKind || result.RunAttemptID != attempt.ID || result.WorkID != attempt.WorkID || result.Actor != attempt.Actor || result.ActionIntentID != attempt.ActionIntentID {
			return result, fmt.Errorf("presubmit journal does not bind exact native attempt")
		}
		result.ArtifactRef = ref
		return result, nil
	}
	return PresubmitExecutionResult{}, fmt.Errorf("native presubmit attempt lacks authentic retained journal")
}

// RecoverPresubmitProducer never kills stored PIDs or accepts canonical proof.
// The existing barrier is the only release authority for a lost native owner.
func (s *Store) RecoverPresubmitProducer(workID, actor, attemptID, reason string) (out PresubmitExecutionResult, err error) {
	if !runattempt.Valid(attemptID) || strings.TrimSpace(reason) == "" {
		return out, fmt.Errorf("exact native attempt and recovery reason required")
	}
	var resource *landlease.Resource
	var status landlease.StatusResult
	admitted := false
	err = s.withLifecycleMutation(func() error {
		w, e := s.Get(workID)
		if e != nil {
			return e
		}
		if w.State != "verifying" {
			return fmt.Errorf("presubmit recovery requires verifying Work")
		}
		if e = s.requireProofWorkLeaseOwner(workID, actor, "presubmit recovery"); e != nil {
			return e
		}
		attempt, e := s.runAttempts().Get(attemptID)
		if e != nil {
			return e
		}
		if attempt.WorkID != workID || attempt.Actor != actor || attempt.Harness != "proof-check-presubmit" || attempt.Status == runattempt.StatusSucceeded {
			return fmt.Errorf("recovery requires exact same-work native presubmit owner; successful execution is not a recovery target")
		}
		out, e = s.readPresubmitExecution(attemptID)
		if e != nil {
			return e
		}
		if out.ContainmentUncertain {
			return fmt.Errorf("native containment uncertainty requires exact external recovery; barrier alone is insufficient")
		}
		if e = validatePresubmitRecoveryGroups(attempt, out); e != nil {
			return e
		}
		if out.Holder != "proof:presubmit:"+attemptID+":"+workID+":"+actor || !out.LeasePrepared || out.Released || !slices.Contains(w.ActionIntentIDs, out.ActionIntentID) {
			return fmt.Errorf("presubmit recovery journal lacks exact retained native holder/action")
		}
		var original envelope.ActionIntent
		var decision envelope.PolicyDecision
		if e = s.readEventJSON(out.ActionIntentID, &original); e != nil {
			return e
		}
		if e = s.readEventJSON(out.PolicyDecisionID, &decision); e != nil {
			return e
		}
		binding, e := presubmitBindingDigest(out)
		if e != nil {
			return e
		}
		if original.Action != "execute_noncertifying_presubmit" || original.Capability != "proof.resource" || original.Actor != actor || original.IDs.WorkID != workID || original.ArgumentsHash != binding || decision.ActionIntentID != original.ID || !policy.Allowed(decision) {
			return fmt.Errorf("presubmit recovery original intent/policy/binding invalid")
		}
		resource = landlease.ProofResourceForIdentity(s.home, out.ProofCoordination)
		status, e = resource.Status(time.Now())
		if e != nil {
			return e
		}
		if status.Holder != out.Holder {
			return fmt.Errorf("presubmit exact holder changed; refusing recovery")
		}
		pending, e := resource.ExecutionPending(out.Holder)
		if e != nil {
			return e
		}
		if pending {
			return fmt.Errorf("presubmit owned execution barrier remains live; exact cleanup required, no stored-PGID signalling")
		}
		action := s.proofResourceAction(w, "presubmit-recovery:"+attemptID, actor, "reserve", "", "", "", "", time.Now())
		action.Action = "recover_noncertifying_presubmit_execution"
		recoveryHash := sha256.Sum256([]byte(attemptID + "\n" + out.ArtifactRef + "\n" + reason))
		action.ArgumentsHash = hex.EncodeToString(recoveryHash[:])
		action.Preview = "release only the exact quiescent lost native presubmit holder; record failed execution-only evidence"
		action.RollbackPlan = "retain native journal and exclusive holder on uncertainty; canonical proof remains required"
		fresh, e := s.evaluateProofResourceAction(action, time.Now())
		if e != nil {
			return e
		}
		if _, e = s.recordActionLocked(workID, action, fresh, time.Now()); e != nil {
			return e
		}
		out.RecoveryActionIntentID, out.RecoveryPolicyDecisionID = action.ID, fresh.ID
		admitted = true
		return nil
	})
	if err != nil {
		return out, err
	}
	out.Status = "failed"
	out.FailureReason = "native producer was lost; " + reason + "; canonical proof remains required"
	out.CompletedAt = time.Now().UTC()
	if out.Report != nil {
		out.Report.Status = "failed"
		out.Report.Warnings = append(out.Report.Warnings, out.FailureReason)
	}
	defer func() {
		if !admitted {
			return
		}
		if err != nil {
			out.FailureReason = errors.Join(errors.New(out.FailureReason), err).Error()
		}
		err = errors.Join(err, s.persistPresubmitRecovery(&out))
		if out.Quiescent {
			attempt, e := s.runAttempts().Get(attemptID)
			if e == nil && !runattempt.IsTerminal(attempt.Status) {
				e = s.completeProofAttempt(attemptID, runattempt.StatusAbandoned, out.FailureReason, time.Now())
			}
			err = errors.Join(err, e)
		}
	}()
	if _, err = resource.AttestExecutionTerminated(out.Holder); err != nil {
		return out, err
	}
	out.Quiescent = true
	if status.IsExpired {
		if _, err = resource.RearmExpiredExact(status, time.Now()); err != nil {
			return out, err
		}
	}
	_, err = resource.ReleaseAfter(out.Holder, time.Now(), func() error { return s.persistPresubmitRecovery(&out) })
	if err == nil {
		out.Released = true
	}
	return out, err
}

func (s *Store) persistPresubmitRecovery(out *PresubmitExecutionResult) error {
	return s.withLifecycleMutation(func() error {
		w, err := s.Get(out.WorkID)
		if err != nil {
			return err
		}
		same := w.State == "verifying" && w.PolicyDecisionID == out.RecoveryPolicyDecisionID && selectedLastAction(w) == out.RecoveryActionIntentID
		if !same {
			out.FailureReason += "; recovery Work generation changed, newer Work preserved"
		}
		artifacts, err := homepkg.NewArtifactStore(s.home)
		if err != nil {
			return err
		}
		body, err := json.Marshal(out)
		if err != nil {
			return err
		}
		ref, err := artifacts.PutPrivate(body)
		if err != nil {
			return err
		}
		out.ArtifactRef = presubmitExecutionRefPrefix + ref
		attempt, err := s.runAttempts().Get(out.RunAttemptID)
		if err != nil {
			return err
		}
		if !runattempt.IsTerminal(attempt.Status) {
			if _, err = s.runAttempts().SetProofRefs(attempt.ID, append(attempt.ProofRefs, out.ArtifactRef)); err != nil {
				return err
			}
		}
		if !same {
			return fmt.Errorf("recovery authority changed; retained reverse execution linkage without modifying newer Work")
		}
		w.ArtifactIDs = append(w.ArtifactIDs, out.ArtifactRef)
		return s.writeJSON(s.workPath(w.ID), &w)
	})
}

func RunPresubmitRecoveryCLI(args []string, stdout, stderr io.Writer) int {
	values := map[string]string{}
	jsonOut := false
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--json" {
			if jsonOut {
				fmt.Fprintln(stderr, "duplicate --json")
				return 2
			}
			jsonOut = true
			continue
		}
		switch flag {
		case PresubmitRecoveryFlag, "--work", "--actor", "--reason":
		default:
			fmt.Fprintln(stderr, "unsupported presubmit recovery argument", flag)
			return 2
		}
		if values[flag] != "" || i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") || strings.TrimSpace(args[i+1]) == "" {
			fmt.Fprintln(stderr, "duplicate or missing presubmit recovery value", flag)
			return 2
		}
		i++
		values[flag] = args[i]
	}
	if values["--work"] == "" || values["--actor"] == "" || values["--reason"] == "" || !runattempt.Valid(values[PresubmitRecoveryFlag]) {
		fmt.Fprintln(stderr, PresubmitRecoveryHelp)
		return 2
	}
	homePath, err := homepkg.Resolve("")
	if err == nil {
		homePath, err = filepath.EvalSymlinks(homePath)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	store, err := Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	result, err := store.RecoverPresubmitProducer(values["--work"], values["--actor"], values[PresubmitRecoveryFlag], values["--reason"])
	if jsonOut {
		body, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(body))
	}
	if err != nil {
		fmt.Fprintln(stderr, "presubmit recovery:", err)
		return 1
	}
	fmt.Fprintf(stdout, "presubmit recovered: %s execution=failed quiescent=%v released=%v result=%s; canonical proof remains required\n", result.RunAttemptID, result.Quiescent, result.Released, result.ArtifactRef)
	return 0
}

func validatePresubmitRecoveryGroups(attempt runattempt.RunAttempt, out PresubmitExecutionResult) error {
	latest := map[int]PresubmitCheckpoint{}
	registered := false
	for _, checkpoint := range out.Checkpoints {
		if checkpoint.CommandSequence <= 0 {
			continue
		}
		if checkpoint.GroupStarted && checkpoint.ProcessGroupID <= 0 {
			return fmt.Errorf("native group journal has an invalid started identity")
		}
		prior, exists := latest[checkpoint.CommandSequence]
		if exists && prior.GroupStarted && checkpoint.GroupStarted && prior.ProcessGroupID != checkpoint.ProcessGroupID {
			return fmt.Errorf("native group journal changed command identity")
		}
		if exists && prior.GroupClosed && !checkpoint.GroupClosed {
			return fmt.Errorf("native group journal reopened a closed command")
		}
		latest[checkpoint.CommandSequence] = checkpoint
	}
	launch := attempt.Launch
	exactStartError := launch != nil && launch.PreparedAt != nil && launch.StartErrorAt != nil && launch.CommittedAt == nil && launch.PreparedAt.Equal(*launch.StartErrorAt)
	mayHaveLaunched := launch != nil && (launch.PreparedAt != nil || launch.CommittedAt != nil) && !exactStartError
	for _, checkpoint := range latest {
		registered = registered || checkpoint.GroupStarted
		if !checkpoint.GroupClosed {
			if !checkpoint.GroupStarted && !mayHaveLaunched && len(latest) == 1 {
				continue
			}
			return fmt.Errorf("native started or ambiguous process group lacks durable closed evidence; mid-gate SIGKILL requires exact external/manual recovery, barrier alone is insufficient")
		}
	}
	if mayHaveLaunched && !registered {
		return fmt.Errorf("native prepared/committed launch lacks registered group evidence; exact external/manual recovery required")
	}
	return nil
}
