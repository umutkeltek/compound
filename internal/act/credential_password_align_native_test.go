package act

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

// This explicit fixture door is test-only. No production request, CLI option or
// transport can select it. It launches the exact embedded programs through the
// same native owner admission callback, with real Linux PG17/libpq and Docker.
type alignmentLinuxFixture struct{ container string }

type alignmentLinuxMetadata struct {
	File          policy.CredentialVerifyFile       `json:"file"`
	Host          policy.CredentialVerifyHost       `json:"host"`
	Interpreter   string                            `json:"interpreter_sha256"`
	Client        string                            `json:"client_sha256"`
	ClientVersion string                            `json:"client_version"`
	Sudo          string                            `json:"sudo_sha256"`
	Docker        string                            `json:"docker_sha256"`
	Root          policy.RuntimeConfigInstallRoot   `json:"root"`
	Marker        policy.RuntimeConfigInstallAnchor `json:"marker"`
	Lock          policy.RuntimeConfigInstallAnchor `json:"lock"`
	OriginalStat  policy.RuntimeConfigInstallStat   `json:"original_stat"`
}

func alignmentLinux(t *testing.T) alignmentLinuxFixture {
	t.Helper()
	name := os.Getenv("COMPOUND_ALIGNMENT_NATIVE_CONTAINER")
	if name == "" {
		t.Skip("requires explicitly selected disposable Linux/PG17 Docker fixture")
	}
	if os.Getenv("COMPOUND_ALIGNMENT_ALLOW_FIXTURE") != "1" {
		t.Fatal("disposable fixture acknowledgement required")
	}
	meta, e := exec.Command("docker", "inspect", "--format", `{{.State.Running}} {{index .Config.Labels "compound.work"}} {{len .HostConfig.PortBindings}}`, name).Output()
	fields := strings.Fields(string(meta))
	if e != nil || len(fields) != 3 || fields[0] != "true" || !strings.HasPrefix(fields[1], "work_") || fields[2] != "0" {
		t.Fatal("fixture identity refused")
	}
	dir := "/root/compound-alignment-tests"
	if e = exec.Command("docker", "exec", name, "mkdir", "-p", dir).Run(); e != nil {
		t.Fatal("fixture directory unavailable")
	}
	for _, file := range []string{"credential_verify_program.py", "credential_password_align_program.py", "credential_password_align_program_test.py", "credential_password_align_native_fixture.py", "runtime_config_install_program.py", "runtime_config_install_program_test.py", "runtime_config_install_alignment_fence_test.py"} {
		path, _ := filepath.Abs(file)
		if e = exec.Command("docker", "cp", path, name+":"+dir+"/"+file).Run(); e != nil {
			t.Fatal("fixture source copy refused")
		}
	}
	return alignmentLinuxFixture{name}
}

func (f alignmentLinuxFixture) start(ctx context.Context, program, sha string, wire any, guard credentialVerifyAdmissionGuard) (credentialPasswordAlignChild, error) {
	payload, e := json.Marshal(wire)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "--interactive", f.container, "/usr/bin/python3", "-I", "-B", "-c", program, sha)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	child := &credentialVerifyNativeChild{cmd: cmd, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = &child.stdout, &child.stderr
	started := false
	e = guard(func() error {
		if e := cmd.Start(); e != nil {
			return e
		}
		started = true
		go func() { child.failed = cmd.Wait() != nil; close(child.done) }()
		return nil
	})
	if e != nil && !started {
		return nil, e
	}
	return child, e
}

type alignmentLinuxVerifyTransport struct{ alignmentLinuxFixture }

func (f alignmentLinuxVerifyTransport) Start(ctx context.Context, r policy.CredentialVerificationRequest, deadline time.Time, guard credentialVerifyAdmissionGuard) (credentialVerifyChild, error) {
	return f.start(ctx, credentialVerificationProgram, CredentialVerificationProgramSHA256(), map[string]any{"request": r, "issued_ns": strconv.FormatInt(time.Now().UnixNano(), 10), "deadline_ns": strconv.FormatInt(deadline.UnixNano(), 10)}, guard)
}

type alignmentLinuxAlignTransport struct {
	alignmentLinuxFixture
	crashCommit bool
}

type alignmentLinuxInstallerTransport struct {
	alignmentLinuxFixture
	owner *RuntimeConfigInstaller
}

func (f alignmentLinuxInstallerTransport) Start(ctx context.Context, r policy.RuntimeConfigInstallRequest, selected map[string]string, operation string, deadline time.Time, guard runtimeConfigInstallAdmissionGuard) (runtimeConfigInstallChild, error) {
	acceptances, e := f.owner.alignmentAcceptances(r)
	if e != nil {
		return nil, e
	}
	if acceptances == nil {
		acceptances = []credentialPasswordAlignmentAcceptance{}
	}
	return f.start(ctx, runtimeConfigInstallProgram(), RuntimeConfigInstallProgramSHA256(), map[string]any{"request": r, "selected": selected, "operation": operation, "issued_ns": strconv.FormatInt(time.Now().UnixNano(), 10), "deadline_ns": strconv.FormatInt(deadline.UnixNano(), 10), "alignment_acceptances": acceptances}, credentialVerifyAdmissionGuard(guard))
}

func (f alignmentLinuxAlignTransport) Start(ctx context.Context, r policy.CredentialPasswordAlignmentRequest, action string, deadline time.Time, guard credentialPasswordAlignAdmissionGuard) (credentialPasswordAlignChild, error) {
	program := credentialPasswordAlignmentProgram()
	if f.crashCommit {
		// Test-only process death after writing COMMIT but before consuming an
		// acknowledgement. No production program/CLI/request has a crash knob.
		at := strings.LastIndex(program, "if __name__ == \"__main__\":")
		program = program[:at] + "\ndef fixture_commit_without_ack(self):\n    self.child.stdin.write(b'COMMIT;\\n')\n    self.child.stdin.flush()\n    os._exit(99)\nAlignmentSession.commit=fixture_commit_without_ack\nsys.exit(alignment_main())\n"
	}
	return f.start(ctx, program, CredentialPasswordAlignmentProgramSHA256(), map[string]any{"request": r, "action_intent_id": action, "original_action_intent_id": credentialPasswordAlignOriginalAction(r, action), "issued_ns": strconv.FormatInt(time.Now().UnixNano(), 10), "deadline_ns": strconv.FormatInt(deadline.UnixNano(), 10)}, guard)
}

func nativeAlignmentFixture(t *testing.T, linux alignmentLinuxFixture) *alignmentFixture {
	t.Helper()
	raw, e := exec.Command("docker", "exec", linux.container, "/usr/bin/python3", "-I", "-B", "/root/compound-alignment-tests/credential_password_align_native_fixture.py", "prepare").Output()
	if e != nil {
		t.Fatal("native fixture preparation refused")
	}
	var metadata alignmentLinuxMetadata
	if json.Unmarshal(raw, &metadata) != nil {
		t.Fatal("native fixture metadata refused")
	}
	f := newConfiguredAlignmentFixture(t, func(c *credentialFixture) {
		c.request.File, c.request.Host = metadata.File, metadata.Host
		c.request.SSH.InterpreterSHA256, c.request.SSH.ClientSHA256, c.request.SSH.ClientVersion, c.request.SSH.SudoSHA256 = metadata.Interpreter, metadata.Client, metadata.ClientVersion, metadata.Sudo
		c.verifier.transport = alignmentLinuxVerifyTransport{linux}
	})
	f.r.Catalog.DockerSHA256 = metadata.Docker
	f.linuxMetadata = &metadata
	f.owner.transport = alignmentLinuxAlignTransport{alignmentLinuxFixture: linux}
	return f
}

func TestCredentialPasswordAlignmentActualLinuxOwnerThenUnchangedVerifier(t *testing.T) {
	linux := alignmentLinux(t)
	f := nativeAlignmentFixture(t, linux)
	f.inspect(t)
	p, e := f.owner.Plan(f.r, f.workID, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.approve(t, p)
	result, e := f.owner.Execute(context.Background(), p.PlanRef, "fixture")
	if e != nil || result.State != "completed" || result.ConfigurationAccepted {
		t.Fatal("actual native alignment failed", e)
	}
	if _, e = credentialPasswordAlignmentInstallerFence(f.owner.home, f.r.File.Path, policy.CredentialVerificationDestination(f.r.CredentialVerificationRequest), f.now.Add(-time.Second)); e == nil {
		t.Fatal("old verifier cleared actual alignment")
	}
	c := f.installer.credential
	c.now = time.Now().UTC()
	c.workID = alignmentTestWork(t, c.home, c.now)
	c.request.AttemptID = c.now.Format("20060102t150405z") + "-1111111111111111"
	qualifiedPlan := c.plan(t)
	c.approve(t, qualifiedPlan)
	qualified, e := c.verifier.Execute(context.Background(), qualifiedPlan.PlanRef, "fixture")
	if e != nil || qualified.State != "completed" || qualified.ConfigurationAccepted {
		t.Fatal("unchanged verifier did not genuinely authenticate repaired credentials", e)
	}
	var receipt envelope.Receipt
	if f.owner.event(qualified.ReceiptID, &receipt) != nil {
		t.Fatal("missing actual verifier receipt")
	}
	created, _ := time.Parse(time.RFC3339Nano, receipt.CreatedAt)
	accepted, e := credentialPasswordAlignmentInstallerFence(c.home, f.r.File.Path, policy.CredentialVerificationDestination(c.request), created)
	if e != nil || len(accepted) != 1 || accepted[0].ActionID != p.Plan.ActionIntentID {
		t.Fatal("genuine unchanged verification failed to consume completed repair proof", e)
	}
	// Consume the same real repair/qualification proof through the actual native
	// four-file installer, with the source capture fixture owned by this test.
	i := f.installer
	convert := func(from, to any) {
		raw, _ := json.Marshal(from)
		if e := json.Unmarshal(raw, to); e != nil {
			t.Fatal(e)
		}
	}
	convert(c.request.Resource, &i.request.Resource)
	convert(c.request.SSH, &i.request.SSH)
	convert(c.request.Host, &i.request.Host)
	convert(c.request.Roles, &i.request.Roles)
	i.request.Root, i.request.Marker, i.request.Lock = f.linuxMetadata.Root, f.linuxMetadata.Marker, f.linuxMetadata.Lock
	for index := range i.request.Files {
		i.request.Files[index].Path = filepath.Join(i.request.Root.Path, i.request.Files[index].Basename)
	}
	i.request.Files[0].Stat = &f.linuxMetadata.OriginalStat
	i.request.Witness = policy.RuntimeConfigInstallWitness{Ref: qualified.ReceiptID, SHA256: installerProof(t, c.home, qualified.ReceiptID, false).SHA256, ObservedAt: receipt.CreatedAt}
	a, e := runattempt.Open(c.home).Get(qualified.RunAttemptID)
	if e != nil {
		t.Fatal(e)
	}
	i.request.Qualification = policy.RuntimeConfigInstallQualification{Plan: installerProof(t, c.home, qualifiedPlan.PlanRef, true), Receipt: installerProof(t, c.home, qualified.ReceiptID, false), Transition: installerProof(t, c.home, qualified.TransitionID, false), Attempt: policy.RuntimeConfigInstallProofRef{Ref: a.ID, SHA256: runtimeConfigInstallHash(a)}, Observation: installerProof(t, c.home, qualified.ObservationRef, true)}
	c.now = time.Now().UTC()
	i.request.AttemptID = c.now.Format("20060102t150405z") + "-9999999999999999"
	i.owner.transport = alignmentLinuxInstallerTransport{alignmentLinuxFixture: linux, owner: i.owner}
	installation := i.plan(t)
	i.approve(t, installation)
	installed, e := i.owner.Execute(context.Background(), installation.PlanRef, "fixture")
	if e != nil || !installed.ConfigurationAccepted || installed.ApplicationActivated {
		t.Fatal("real installer did not consume repaired credential proof", e)
	}
	logs, e := exec.Command("docker", "logs", linux.container).CombinedOutput()
	if e != nil || bytes.Contains(logs, []byte("synthetic-canary-")) || regexp.MustCompile(`SCRAM-SHA-256\$[1-9][0-9]*:[A-Za-z0-9+/]+=*\$[A-Za-z0-9+/]+=*:[A-Za-z0-9+/]+=*`).Match(logs) {
		t.Fatal("fixture logs contain credential material or are unavailable")
	}
}

func TestCredentialPasswordAlignmentProgramActualLinuxPrimitiveConsumer(t *testing.T) {
	linux := alignmentLinux(t)
	for _, file := range []string{"credential_password_align_program_test.py", "runtime_config_install_alignment_fence_test.py"} {
		cmd := exec.Command("docker", "exec", "--env", "COMPOUND_ALIGNMENT_LINUX_PG17=1", linux.container, "/usr/bin/python3", "-I", "-B", "/root/compound-alignment-tests/"+file)
		if raw, e := cmd.CombinedOutput(); e != nil || bytes.Contains(raw, []byte("skipped=")) {
			t.Fatal("actual Linux acceptance suite failed or skipped", string(raw))
		}
	}
}

func TestCredentialPasswordAlignmentPurePythonConsumers(t *testing.T) {
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("Python 3 unavailable")
	}
	for _, file := range []string{"credential_password_align_program_test.py", "runtime_config_install_alignment_fence_test.py"} {
		t.Run(file, func(t *testing.T) {
			cmd := exec.Command(python, "-I", "-B", file)
			if raw, e := cmd.CombinedOutput(); e != nil {
				t.Fatal("synthetic Python acceptance failed", string(raw))
			}
		})
	}
}

func TestCredentialPasswordAlignmentActualCommitAckLossAndRestoreAckLoss(t *testing.T) {
	linux := alignmentLinux(t)
	f := nativeAlignmentFixture(t, linux)
	f.inspect(t)
	p, e := f.owner.Plan(f.r, f.workID, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.approve(t, p)
	f.owner.transport = alignmentLinuxAlignTransport{alignmentLinuxFixture: linux, crashCommit: true}
	original, e := f.owner.Execute(context.Background(), p.PlanRef, "fixture")
	if e == nil || original.State != "recovery_required" {
		t.Fatal("commit acknowledgement loss was accepted", e)
	}
	f.owner.transport = alignmentLinuxAlignTransport{alignmentLinuxFixture: linux}
	f.freshRecovery(t, p, original, "reconcile")
	reconcile, e := f.owner.Plan(f.r, f.workID, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.approve(t, reconcile)
	resolved, e := f.owner.Execute(context.Background(), reconcile.PlanRef, "fixture")
	if e != nil || resolved.State != "completed" {
		t.Fatal("real committed postimage did not reconcile", e)
	}
	store, _ := NewArtifactStore(f.owner.home)
	raw, _ := store.Get(resolved.ObservationRef)
	o, e := decodeCredentialPasswordAlignmentObservation(raw, reconcile.Plan.Request)
	if e != nil || o.Effect != "reconciled_new_new" {
		t.Fatal("lost COMMIT acknowledgement did not preserve new/new classification", e)
	}
	f.freshRecovery(t, p, original, "restore")
	restore, e := f.owner.Plan(f.r, f.workID, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.approve(t, restore)
	f.owner.transport = alignmentLinuxAlignTransport{alignmentLinuxFixture: linux, crashCommit: true}
	failedRestore, e := f.owner.Execute(context.Background(), restore.PlanRef, "fixture")
	if e == nil || failedRestore.State != "recovery_required" {
		t.Fatal("restore acknowledgement loss was accepted", e)
	}
	f.owner.transport = alignmentLinuxAlignTransport{alignmentLinuxFixture: linux}
	f.freshRecovery(t, p, original, "restore")
	if _, e = f.owner.Plan(f.r, f.workID, "fixture"); e == nil {
		t.Fatal("blind restore retry reached admission")
	}
	f.r.Operation = "reconcile"
	recovery, e := f.owner.Plan(f.r, f.workID, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.approve(t, recovery)
	completed, e := f.owner.Execute(context.Background(), recovery.PlanRef, "fixture")
	if e != nil {
		t.Fatal("journaled restore postimage with new row revisions was not reconciled", e)
	}
	raw, _ = store.Get(completed.ObservationRef)
	o, e = decodeCredentialPasswordAlignmentObservation(raw, recovery.Plan.Request)
	if e != nil || o.Effect != "reconciled_old_old" || len(o.ResolvedRecoveryActions) != 2 {
		t.Fatal("restored catalog was confused with original old row revisions", e)
	}
	for _, id := range []string{original.RunAttemptID, failedRestore.RunAttemptID} {
		a, e := runattempt.Open(f.owner.home).Get(id)
		if e != nil || a.Status != runattempt.StatusFailed {
			t.Fatal("failed acknowledgement-loss history was rewritten")
		}
	}
}
