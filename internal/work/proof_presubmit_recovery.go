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
// A started group without durable closure (a producer killed mid-gate) is
// admitted only after its exact holder lease expired with no renewal and a
// kernel query finds no member of that group; the group is never signalled.
func (s *Store) RecoverPresubmitProducer(workID, actor, attemptID, reason string) (out PresubmitExecutionResult, err error) {
	if !runattempt.Valid(attemptID) || strings.TrimSpace(reason) == "" {
		return out, fmt.Errorf("exact native attempt and recovery reason required")
	}
	var resource *landlease.Resource
	var status landlease.StatusResult
	var killedGroups []int
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
		if killedGroups, e = validatePresubmitRecoveryGroups(attempt, out); e != nil {
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
		status, e = resource.Status(s.presubmitRecoveryNow())
		if e != nil {
			return e
		}
		if status.Holder != out.Holder {
			return fmt.Errorf("presubmit exact holder changed; refusing recovery")
		}
		if e = presubmitKilledGroupsGone(attempt, status, killedGroups, s.presubmitRecoveryNow()); e != nil {
			return e
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
		out.RecoveredEmptyGroups = killedGroups
		admitted = true
		return nil
	})
	if err != nil {
		return out, err
	}
	out.Status = "failed"
	out.FailureReason = "native producer was lost; " + reason + "; canonical proof remains required"
	if len(out.RecoveredEmptyGroups) > 0 {
		out.FailureReason += fmt.Sprintf("; started groups %v had no kernel members after holder lease expiry", out.RecoveredEmptyGroups)
	}
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
		if _, err = resource.RearmExpiredExact(status, s.presubmitRecoveryNow()); err != nil {
			return out, err
		}
	}
	_, err = resource.ReleaseAfter(out.Holder, s.presubmitRecoveryNow(), func() error { return s.persistPresubmitRecovery(&out) })
	if err == nil {
		out.Released = true
	}
	return out, err
}

func (s *Store) presubmitRecoveryNow() time.Time {
	if s.presubmitRecoveryClock != nil {
		return s.presubmitRecoveryClock()
	}
	return time.Now()
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

// validatePresubmitRecoveryGroups checks the durable group journal and returns
// the started groups that lack durable closure. Those stay refused until
// presubmitKilledGroupsGone proves them empty. An unregistered or ambiguous
// launch has no group number to inspect, so it still refuses here.
func validatePresubmitRecoveryGroups(attempt runattempt.RunAttempt, out PresubmitExecutionResult) ([]int, error) {
	latest := map[int]PresubmitCheckpoint{}
	registered := false
	for _, checkpoint := range out.Checkpoints {
		if checkpoint.CommandSequence <= 0 {
			continue
		}
		if checkpoint.GroupStarted && checkpoint.ProcessGroupID <= 0 {
			return nil, fmt.Errorf("native group journal has an invalid started identity")
		}
		prior, exists := latest[checkpoint.CommandSequence]
		if exists && prior.GroupStarted && checkpoint.GroupStarted && prior.ProcessGroupID != checkpoint.ProcessGroupID {
			return nil, fmt.Errorf("native group journal changed command identity")
		}
		if exists && prior.GroupClosed && !checkpoint.GroupClosed {
			return nil, fmt.Errorf("native group journal reopened a closed command")
		}
		latest[checkpoint.CommandSequence] = checkpoint
	}
	launch := attempt.Launch
	exactStartError := launch != nil && launch.PreparedAt != nil && launch.StartErrorAt != nil && launch.CommittedAt == nil && launch.PreparedAt.Equal(*launch.StartErrorAt)
	mayHaveLaunched := launch != nil && (launch.PreparedAt != nil || launch.CommittedAt != nil) && !exactStartError
	var unclosed []int
	for _, checkpoint := range latest {
		registered = registered || checkpoint.GroupStarted
		if checkpoint.GroupClosed {
			continue
		}
		if checkpoint.GroupStarted {
			unclosed = append(unclosed, checkpoint.ProcessGroupID)
			continue
		}
		if !mayHaveLaunched && len(latest) == 1 {
			continue
		}
		return nil, fmt.Errorf("native unregistered or ambiguous command launch lacks group evidence; exact external/manual recovery required, barrier alone is insufficient")
	}
	if mayHaveLaunched && !registered {
		return nil, fmt.Errorf("native prepared/committed launch lacks registered group evidence; exact external/manual recovery required")
	}
	slices.Sort(unclosed)
	return slices.Compact(unclosed), nil
}

// presubmitKilledGroupsGone admits started groups without durable closure only
// when the exact holder lease expired with no renewal and the producer's last
// activity is older than that lease TTL, and a kernel membership query (never
// a signal) finds no member. Any member refuses: it may be the run itself or
// an unrelated group that reused the number.
func presubmitKilledGroupsGone(attempt runattempt.RunAttempt, status landlease.StatusResult, groups []int, now time.Time) error {
	if len(groups) == 0 {
		return nil
	}
	ttl := time.Duration(status.TTLSeconds) * time.Second
	if status.Held || !status.IsExpired || ttl <= 0 {
		return fmt.Errorf("native started groups %v lack durable closed evidence and the exact holder lease has not expired", groups)
	}
	lastActivity := attempt.CreatedAt
	if attempt.LastHeartbeatAt != nil && attempt.LastHeartbeatAt.After(lastActivity) {
		lastActivity = *attempt.LastHeartbeatAt
	}
	if lastActivity.IsZero() || now.Sub(lastActivity) <= ttl {
		return fmt.Errorf("native started groups %v lack durable closed evidence and the producer heartbeat is newer than its lease TTL", groups)
	}
	for _, pgid := range groups {
		members, err := presubmitRecordedGroupMembers(pgid)
		if err != nil {
			return fmt.Errorf("native started group %d lacks durable closed evidence and its kernel membership query failed: %w", pgid, err)
		}
		if members != 0 {
			return fmt.Errorf("native started group %d still has %d live member(s); it may be the run or a reused group number, so recovery refuses", pgid, members)
		}
	}
	return nil
}
