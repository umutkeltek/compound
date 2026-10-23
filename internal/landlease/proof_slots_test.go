package landlease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func requireProofSlot(t *testing.T, resource *Resource, slot int) {
	t.Helper()
	identity := resource.ProofIdentity()
	if identity == nil || identity.Slot != slot {
		t.Fatalf("resource identity = %+v, want slot %d", identity, slot)
	}
	want := ProofCoordinationIdentitySchema
	if slot != 0 {
		want = ProofCoordinationSlotIdentitySchema
	}
	if identity.SchemaVersion != want {
		t.Fatalf("slot %d schema = %q, want %q", slot, identity.SchemaVersion, want)
	}
	status, err := resource.StatusReadOnly(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Held || status.ProofCoordination == nil || *status.ProofCoordination != *identity {
		t.Fatalf("slot %d lease not held with its identity: %+v", slot, status)
	}
}

func requireProofBusy(t *testing.T, ticket *ProofAdmission, reason string) *ProofAdmissionBusyError {
	t.Helper()
	called := false
	err := ticket.WithTurn(context.Background(), false, func(*Resource) error { called = true; return nil })
	var busy *ProofAdmissionBusyError
	if called || !errors.As(err, &busy) {
		t.Fatalf("admission not refused: called=%v err=%v", called, err)
	}
	if !strings.Contains(busy.Reason, reason) {
		t.Fatalf("busy reason = %q, want %q", busy.Reason, reason)
	}
	return busy
}

func releaseDomainProof(t *testing.T, resource *Resource, barrier *ExecutionBarrier, holder string) {
	t.Helper()
	if err := barrier.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Release(holder, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestProofSlotsTwoRepositoriesRunConcurrently(t *testing.T) {
	homes, _, _ := proofDomainFixture(t)
	first, _ := acquireDomainProof(t, beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "2"), "slot-first")
	second, _ := acquireDomainProof(t, beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "3"), "slot-second")
	requireProofSlot(t, first, 0)
	requireProofSlot(t, second, 1)
}

func TestProofSlotsSameRepositoryStillSerializes(t *testing.T) {
	homes, _, _ := proofDomainFixture(t)
	first, _ := acquireDomainProof(t, beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "2"), "slot-first")
	requireProofSlot(t, first, 0)
	again := beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "2")
	requireProofBusy(t, again, "same repository proof running")
}

func TestProofSlotsThirdRepositoryWaitsForAFreeSlot(t *testing.T) {
	homes, _, _ := proofDomainFixture(t)
	first, _ := acquireDomainProof(t, beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "2"), "slot-first")
	second, secondBarrier := acquireDomainProof(t, beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "3"), "slot-second")
	third := beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "4")
	busy := requireProofBusy(t, third, "capacity occupied (2/2)")
	if busy.Holder != "slot-first" {
		t.Fatalf("busy holder = %q, want slot-0 holder", busy.Holder)
	}
	releaseDomainProof(t, second, secondBarrier, "slot-second")
	admitted, _ := acquireDomainProof(t, third, "slot-third")
	requireProofSlot(t, admitted, 1)
	requireProofSlot(t, first, 0)
}

func TestProofSlotsStatusReportsEverySlot(t *testing.T) {
	homes, _, _ := proofDomainFixture(t)
	acquireDomainProof(t, beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "2"), "slot-first")
	acquireDomainProof(t, beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "3"), "slot-second")
	rows, err := ProofResourceStatuses(homes[0], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		class  ProofCapacityClass
		slot   int
		holder string
	}
	var got []row
	for _, r := range rows {
		got = append(got, row{r.Class, r.Slot, r.Status.Holder})
	}
	want := []row{{ProofCapacitySystemFull, 0, "slot-first"}, {ProofCapacitySystemFull, 1, "slot-second"}, {ProofCapacityNativeConsumer, 0, ""}}
	if len(got) != len(want) {
		t.Fatalf("status rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("status rows = %+v, want %+v", got, want)
		}
	}
}

func TestProofSlotsRecoveryStaysOnTheExactSlot(t *testing.T) {
	homes, _, _ := proofDomainFixture(t)
	first, firstBarrier := acquireDomainProof(t, beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "2"), "slot-first")
	dead, deadBarrier := acquireDomainProof(t, beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "3"), "slot-dead")
	requireProofSlot(t, dead, 1)
	if err := deadBarrier.Close(); err != nil {
		t.Fatal(err)
	}
	// Slot-1 identity drives execution observation and termination evidence.
	slotOne := ProofResourceForIdentity(homes[0], dead.ProofIdentity())
	if pending, err := slotOne.ExecutionPending("slot-dead"); err != nil || pending {
		t.Fatalf("slot-1 execution pending = %v, %v", pending, err)
	}
	if _, err := slotOne.AttestExecutionTerminated("slot-dead"); err != nil {
		t.Fatalf("slot-1 termination attestation: %v", err)
	}

	// While the dead lease is still Held, an audited steal for its exact holder
	// targets slot 1 and leaves slot 0 untouched.
	stealer := beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "4")
	var stolenSlot = -1
	err := stealer.WithTurnFor(context.Background(), true, func(holder string) bool { return holder == "slot-dead" }, func(r *Resource) error {
		stolenSlot = r.ProofIdentity().Slot
		return nil
	})
	if err != nil || stolenSlot != 1 {
		t.Fatalf("prefer steal chose slot %d: %v", stolenSlot, err)
	}
	if err := stealer.Cancel(); err != nil {
		t.Fatal(err)
	}
	requireProofSlot(t, first, 0)

	// Expire the dead lease and free slot 0: the next same-repository proof is
	// still pinned to slot 1, where its predecessor record lives.
	path, _ := statePath(proofSlotRoot(homesBacking(t, homes[0]), dead.ProofIdentity().Generation, ProofCapacitySystemFull, 1))
	lease, _, err := readLease(path)
	if err != nil {
		t.Fatal(err)
	}
	lease.AcquiredAt = baseNow.Format(time.RFC3339Nano)
	lease.ExpiresAt = baseNow.Add(time.Minute).Format(time.RFC3339Nano)
	if err := writeLeaseAtomic(path, *lease); err != nil {
		t.Fatal(err)
	}
	releaseDomainProof(t, first, firstBarrier, "slot-first")
	recovered, _ := acquireDomainProof(t, beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "3"), "slot-recovered")
	requireProofSlot(t, recovered, 1)
	if status, err := ProofResourceForIdentity(homes[0], first.ProofIdentity()).StatusReadOnly(time.Now()); err != nil || status.Holder != "" {
		t.Fatalf("slot 0 touched by pinned recovery: %+v %v", status, err)
	}
}

func homesBacking(t *testing.T, home string) string {
	t.Helper()
	_, backing, exists, err := proofDomainForHome(home)
	if err != nil || !exists {
		t.Fatalf("proof domain for home: %v exists=%v", err, exists)
	}
	return backing
}

// legacyProofCoordinationIdentity mirrors the identity an older binary decodes.
type legacyProofCoordinationIdentity struct {
	SchemaVersion         string             `json:"schema_version"`
	Generation            string             `json:"generation"`
	Class                 ProofCapacityClass `json:"class"`
	RepositoryFingerprint string             `json:"repository_fingerprint"`
	RepositoryBarrier     string             `json:"repository_barrier"`
}

func TestProofSlotsOldClientsNeitherDoubleBookNorMisread(t *testing.T) {
	identity, err := NewProofCoordinationIdentity(strings.Repeat("a", 64), ProofCapacitySystemFull, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(identity)
	legacy, _ := json.Marshal(legacyProofCoordinationIdentity{identity.SchemaVersion, identity.Generation, identity.Class, identity.RepositoryFingerprint, identity.RepositoryBarrier})
	if !bytes.Equal(current, legacy) || strings.Contains(string(current), "slot") {
		t.Fatalf("slot-0 identity bytes changed:\n%s\n%s", current, legacy)
	}
	slotOne, err := identity.WithSlot(1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(slotOne)
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var old legacyProofCoordinationIdentity
	if err := dec.Decode(&old); err == nil {
		t.Fatalf("old decoder accepted slot identity %s", body)
	}
	for _, bad := range []struct {
		schema string
		slot   int
	}{{ProofCoordinationIdentitySchema, 1}, {ProofCoordinationSlotIdentitySchema, 0}, {ProofCoordinationSlotIdentitySchema, proofSlotMax + 1}} {
		candidate := identity
		candidate.SchemaVersion, candidate.Slot = bad.schema, bad.slot
		if candidate.Validate() == nil {
			t.Fatalf("identity %q slot %d validated", bad.schema, bad.slot)
		}
	}
	if _, err := identity.WithSlot(proofSlotMax + 1); err == nil {
		t.Fatal("WithSlot accepted slot beyond schema bound")
	}

	// A repository in slot 1 still holds the repository barrier an old client takes.
	homes, backing, generation := proofDomainFixture(t)
	acquireDomainProof(t, beginDomainTicket(t, homes[0], ProofCapacitySystemFull, "3"), "slot-other")
	running, _ := acquireDomainProof(t, beginDomainTicket(t, homes[1], ProofCapacitySystemFull, "2"), "slot-running")
	requireProofSlot(t, running, 1)
	repositoryRoot := proofRepositoryRoot(backing, generation)
	holder := proofRepositoryHolder(running.ProofIdentity())
	if barrier, err := lockExecutionBarrier(repositoryRoot, holder); err == nil {
		_ = barrier.Close()
		t.Fatal("old client could take the repository barrier of a slot-1 proof")
	}
	if pending, err := executionBarrierPending(repositoryRoot, holder); err != nil || !pending {
		t.Fatalf("slot-1 repository barrier pending = %v, %v", pending, err)
	}

	// A slot-1 root lease carrying a slot-0 identity is refused.
	slotZero := *running.ProofIdentity()
	slotZero, err = slotZero.WithSlot(0)
	if err != nil {
		t.Fatal(err)
	}
	expected := running.ProofIdentity()
	binding := &leaseBinding{identity: expected, acquisition: true}
	if err := binding.validate(&Lease{Holder: "misrouted", ProofCoordination: &slotZero}); err == nil {
		t.Fatal("slot-1 binding accepted a slot-0 lease")
	}
	if _, err := proofLeaseIdentity(&Lease{Holder: "misrouted", ProofCoordination: &slotZero}, "next", expected); err == nil {
		t.Fatal("lease identity accepted a slot change")
	}
}

func TestProofSlotsQueueTicketsAreSlotless(t *testing.T) {
	homes, _, _ := proofDomainFixture(t)
	identity, err := ProofCoordinationFor(homes[0], ProofCapacitySystemFull, strings.Repeat("5", 64))
	if err != nil {
		t.Fatal(err)
	}
	slotted, err := identity.WithSlot(1)
	if err != nil {
		t.Fatal(err)
	}
	if ticket, err := BeginProofAdmission(homes[0], slotted, time.Now().Add(time.Minute)); err == nil {
		_ = ticket.Cancel()
		t.Fatal("queue accepted a slot-bearing ticket")
	}
}
