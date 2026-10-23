package act

import (
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/policy"
	"compound/internal/runattempt"
	"compound/internal/work"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sourceMarkerTransportFixture struct {
	starts int
	fail   bool
}

func sourceMarkerObservationFixture(r policy.SourceMarkerRetirementRequest, operation string) SourceMarkerRetirementObservation {
	o := SourceMarkerRetirementObservation{Operation: operation, Changed: operation == "retire", SchemaVersion: sourceMarkerRetirementObservationSchema, RequestSHA256: policy.SourceMarkerRetirementDigest(r), ProgramSHA256: SourceMarkerRetirementProgramSHA256(), Transaction: ".smr-" + r.AttemptID, State: "source_marker_retired", HostBefore: r.Host, HostAfter: r.Host, JournalRetained: true, SourceMarkerRetired: true, MarkerInodePreserved: true, DatabaseClosed: true, ApplicationContainersAbsent: true}
	for i, f := range r.Files {
		o.RuntimeBefore[i] = sourceMarkerWithoutHash(*f.Stat)
		o.RuntimeAfter[i] = o.RuntimeBefore[i]
	}
	if o.Changed {
		s := sourceMarkerWithoutHash(*r.Marker.Stat)
		o.MarkerBefore = &s
	}
	o.MarkerAfter = sourceMarkerWithoutHash(*r.Marker.Stat)
	o.InactiveBefore = sourceMarkerWithoutHash(*r.Inactive.Stat)
	o.InactiveAfter = o.InactiveBefore
	return o
}
func (m *sourceMarkerTransportFixture) Start(ctx context.Context, r policy.SourceMarkerRetirementRequest, _ map[string]string, operation string, _ time.Time, guard sourceMarkerRetirementAdmissionGuard) (sourceMarkerRetirementChild, error) {
	if e := guard(func() error { m.starts++; return nil }); e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(sourceMarkerObservationFixture(r, operation))
	child := &credentialMockChild{raw: raw}
	if m.fail {
		child.waitErr = errors.New("synthetic response loss")
	}
	return child, nil
}
func sourceMarkerFixture(t *testing.T) (*SourceMarkerRetirer, policy.SourceMarkerRetirementRequest, *sourceMarkerTransportFixture, string, time.Time) {
	t.Helper()
	f := newInstallerNativeFixture(t)
	f.request.Marker.Basename = ".runtime-staging-inactive"
	p := f.plan(t)
	f.approve(t, p)
	accepted, e := f.owner.Execute(context.Background(), p.PlanRef, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	artifacts, _ := NewArtifactStore(f.credential.home)
	raw, e := artifacts.Get(accepted.ObservationRef)
	if e != nil {
		t.Fatal(e)
	}
	observation, e := decodeRuntimeConfigInstallObservation(raw, f.request)
	if e != nil {
		t.Fatal(e)
	}
	now := f.credential.now
	sha := strings.Repeat("a", 64)
	r := policy.SourceMarkerRetirementRequest{SchemaVersion: policy.SourceMarkerRetirementSchema, ProfileID: "fixture-retirement", AttemptID: now.Format("20060102t150405z") + "-bbbbbbbbbbbbbbbb", ProgramSHA256: SourceMarkerRetirementProgramSHA256(), EffectSet: policy.SourceMarkerRetirementEffects, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano), Installation: policy.SourceMarkerInstallation{PlanRef: p.PlanRef, WorkID: f.workID, ActionID: p.Plan.ActionIntentID, AttemptID: accepted.RunAttemptID, ReceiptID: accepted.ReceiptID, TransitionID: accepted.TransitionID, ObservationRef: accepted.ObservationRef, ObservationSHA256: runtimeConfigBytesHash(raw), RequestSHA256: p.Plan.RequestSHA256}, Resource: f.request.Resource, Host: f.request.Host, Root: policy.BackupCredentialStageRoot{Path: f.request.Root.Path}, RecoveryBasename: "fixture-source-marker-recovery", ContainerPrefix: "fixture", Database: policy.SourceMarkerRetirementDatabase{Name: f.request.Database.Name, OID: "12345", AllowConnections: f.request.Database.AllowConnections}, Executables: policy.SourceMarkerRetirementExecutables{DockerSHA256: sha, RunuserSHA256: sha, ClientPath: f.request.SSH.ClientPath, ClientSHA256: f.request.SSH.ClientSHA256, ClientVersion: f.request.SSH.ClientVersion}}
	ssh := f.request.SSH
	r.SSH = policy.BackupCredentialStageSSH{Alias: ssh.Alias, User: ssh.User, Port: ssh.Port, IdentityFile: ssh.IdentityFile, KnownHostsFile: ssh.KnownHostsFile, KnownHostsSHA256: ssh.KnownHostsSHA256, HostKeySHA256: ssh.HostKeySHA256, SSHSHA256: sha, InterpreterSHA256: ssh.InterpreterSHA256, SudoSHA256: ssh.SudoSHA256}
	for i, file := range f.request.Files {
		s := *observation.Postimages[i]
		r.Files[i] = policy.SourceMarkerRetirementFile{Basename: file.Basename, Path: file.Path, Stat: &s}
	}
	marker := sourceMarkerWithoutHash(*r.Files[0].Stat)
	marker.Inode = 700
	marker.Bytes = 20
	r.Marker = policy.SourceMarkerRetirementFile{Basename: r.Files[0].Basename + ".SOURCE_COMPARISON_ONLY", Path: filepath.Join(r.Root.Path, r.Files[0].Basename+".SOURCE_COMPARISON_ONLY"), Stat: &marker}
	inactive := sourceMarkerWithoutHash(f.request.Marker.Stat)
	r.Inactive = policy.SourceMarkerRetirementFile{Basename: f.request.Marker.Basename, Path: filepath.Join(r.Root.Path, f.request.Marker.Basename), Stat: &inactive}
	lock := marker
	lock.Inode = 701
	lock.Bytes = 0
	lock.Mode = "0644"
	r.MaintenanceLock = policy.SourceMarkerRetirementFile{Basename: "fixture-runtime-maintenance.lock", Path: "/run/lock/fixture-runtime-maintenance.lock", Stat: &lock}
	identity := marker
	identity.Bytes = 100
	r.SSH.IdentityStat = identity
	known := identity
	known.Inode = 702
	known.SHA256 = sha
	r.SSH.KnownHostsStat = known
	v, e := NewSourceMarkerRetirer(f.credential.home)
	if e != nil {
		t.Fatal(e)
	}
	v.now = func() time.Time { return now }
	m := &sourceMarkerTransportFixture{}
	v.transport = m
	w := nativeInstallerWork(t, v.home, "retire synthetic source marker after genuine native acceptance", "work_fixture_marker_retirement", now)
	return v, r, m, w.ID, now
}
func sourceMarkerApprove(t *testing.T, v *SourceMarkerRetirer, p SourceMarkerRetirementPlanResult, now time.Time) {
	t.Helper()
	cfg, _, e := policy.LoadConfig(v.home)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = policy.NewExactActionApprovalStore(v.home).Approve(p.Plan.ApprovalPlanID, "operator:fixture", cfg, now); e != nil {
		t.Fatal(e)
	}
}
func TestSourceMarkerRetirementRequiresGenuineNativeAcceptance(t *testing.T) {
	v, r, m, w, _ := sourceMarkerFixture(t)
	r.Installation.AttemptID = "ratt_20261023T010203Z_aaaaaaaa"
	if _, e := v.Plan(r, w, "fixture"); e == nil || m.starts != 0 {
		t.Fatal("accepted nonexistent installation attempt")
	}
}
func TestSourceMarkerRetirementNativeProofAndIndependentEffect(t *testing.T) {
	v, r, m, w, now := sourceMarkerFixture(t)
	p, e := v.Plan(r, w, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.Execute(context.Background(), p.PlanRef, "fixture"); e == nil || m.starts != 0 {
		t.Fatal("unapproved marker effect launched")
	}
	sourceMarkerApprove(t, v, p, now)
	out, e := v.Execute(context.Background(), p.PlanRef, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	a, e := runattempt.Open(v.home).Get(out.RunAttemptID)
	if e != nil || a.Harness != "compound" || a.Actor != "fixture" || a.Status != runattempt.StatusSucceeded || a.ActionIntentID != p.Plan.ActionIntentID || a.TerminalizationOwner != sourceMarkerRetirementOwner || a.Launch == nil || a.Launch.CommittedAt == nil || !out.SourceMarkerRetired || out.ApplicationActivated || out.ServiceActivated {
		t.Fatal("missing native marker proof")
	}
	events, e := eventstore.Open(v.home)
	if e != nil {
		t.Fatal(e)
	}
	record, e := events.Read(out.ReceiptID)
	if e != nil {
		t.Fatal(e)
	}
	var receipt envelope.Receipt
	if e = json.Unmarshal(record.Bytes, &receipt); e != nil {
		t.Fatal(e)
	}
	changed := receipt.StateDelta.FilesChanged
	if len(changed) != 2 || changed[0] != r.Marker.Path || changed[1] != filepath.Join(r.Root.Path, r.RecoveryBasename, r.Marker.Basename+".preimage") {
		t.Fatal("marker receipt attributed changes outside the exact marker rename")
	}
	s, _ := work.Open(v.home)
	_, _, present, e := s.ReadReceiptedActionReservationInput(w, p.Plan.ActionIntentID)
	if e != nil || present {
		t.Fatal("native marker completion did not converge")
	}
}
func TestSourceMarkerRetirementRecoveryIsSeparate(t *testing.T) {
	v, r, m, w, now := sourceMarkerFixture(t)
	p, e := v.Plan(r, w, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	sourceMarkerApprove(t, v, p, now)
	m.fail = true
	failed, e := v.Execute(context.Background(), p.PlanRef, "fixture")
	if e == nil || failed.State != "recovery_required" {
		t.Fatal("lost response did not retain recovery obligation")
	}
	nativeInstallerWork(t, v.home, "reconcile synthetic marker", failed.RecoveryWorkID, now)
	recovery, e := v.PlanRecovery(p.PlanRef, "reconcile", failed.RecoveryWorkID, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	sourceMarkerApprove(t, v, recovery, now)
	m.fail = false
	out, e := v.Execute(context.Background(), recovery.PlanRef, "fixture")
	if e != nil || !out.SourceMarkerRetired {
		t.Fatal("native marker recovery failed", e)
	}
}
func TestSourceMarkerRetirementDoesNotWeakenGenericProtectedPaths(t *testing.T) {
	v, r, _, w, now := sourceMarkerFixture(t)
	ai := sourceMarkerRetirementIntent(r, w, "fixture", strings.Repeat("b", 64), now)
	cfg, _, _ := policy.LoadConfig(v.home)
	if policy.Evaluate(ai, cfg, now).Outcome != "deny" {
		t.Fatal("generic env protected path was weakened")
	}
	if pd, ok := policy.EvaluateSourceMarkerRetirement(ai, r, SourceMarkerRetirementProgramSHA256(), cfg, policy.EvaluationContext{}, now); !ok || pd.Outcome != "hold" {
		t.Fatal("typed marker requires one-use approval")
	}
	cfg.ForbiddenPaths = append(cfg.ForbiddenPaths, r.Marker.Path)
	if pd, ok := policy.EvaluateSourceMarkerRetirement(ai, r, SourceMarkerRetirementProgramSHA256(), cfg, policy.EvaluationContext{}, now); ok || pd.Outcome != "deny" {
		t.Fatal("custom marker denial bypassed")
	}
}
func TestSourceMarkerRetirementObservationRejectsChanges(t *testing.T) {
	_, r, _, _, _ := sourceMarkerFixture(t)
	valid := sourceMarkerObservationFixture(r, "retire")
	for _, mutate := range []func(*SourceMarkerRetirementObservation){func(o *SourceMarkerRetirementObservation) { o.RuntimeAfter[0].Inode++ }, func(o *SourceMarkerRetirementObservation) { o.RuntimeAfter[1].CtimeNS = "1" }, func(o *SourceMarkerRetirementObservation) { o.MarkerAfter.Inode++ }, func(o *SourceMarkerRetirementObservation) { o.InactiveAfter.Mode = "0644" }, func(o *SourceMarkerRetirementObservation) { o.CredentialBodiesOpened = true }, func(o *SourceMarkerRetirementObservation) { o.DatabaseClosed = false }, func(o *SourceMarkerRetirementObservation) { o.ApplicationContainersAbsent = false }, func(o *SourceMarkerRetirementObservation) { o.ServiceActivated = true }} {
		o := valid
		mutate(&o)
		raw, _ := json.Marshal(o)
		if _, e := decodeSourceMarkerRetirementObservation(raw, r); e == nil {
			t.Fatal("accepted changed marker postcondition")
		}
	}
}
func TestSourceMarkerRetirementProgramSyntheticMain(t *testing.T) {
	command := exec.Command("python3", "-I", "-B", "source_marker_retirement_program_test.py")
	if raw, e := command.CombinedOutput(); e != nil {
		t.Fatalf("synthetic marker main failed: %v %s", e, raw)
	}
}

func TestSourceMarkerRetirementRejectsChangedWorkGeneration(t *testing.T) {
	v, r, m, w, now := sourceMarkerFixture(t)
	p, e := v.Plan(r, w, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	sourceMarkerApprove(t, v, p, now)
	s, e := work.Open(v.home)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Advance(w, work.AdvanceInput{To: "verifying", Actor: "fixture"}, now); e != nil {
		t.Fatal(e)
	}
	if _, e = v.Execute(context.Background(), p.PlanRef, "fixture"); e == nil || m.starts != 0 {
		t.Fatal("stale Work generation launched marker rename")
	}
}
