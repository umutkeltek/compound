package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePacketMarkdownExtractsWorkFields(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "work", "goal-packet.md"))
	if err != nil {
		t.Fatal(err)
	}
	packet, err := ParsePacketMarkdown(data, "goal-packet.md")
	if err != nil {
		t.Fatal(err)
	}
	if packet.Title != "Compound Goal Packet: Capability Intake" {
		t.Fatalf("title = %q", packet.Title)
	}
	if packet.Owner != "compound-core" {
		t.Fatalf("owner = %q", packet.Owner)
	}
	if !strings.Contains(packet.Objective, "capability intake path") {
		t.Fatalf("objective = %q", packet.Objective)
	}
	if !strings.Contains(packet.SuccessPredicate, "state-delta receipt") {
		t.Fatalf("success predicate = %q", packet.SuccessPredicate)
	}
	if len(packet.NonGoals) != 3 || !strings.Contains(packet.NonGoals[0], "source packet") {
		t.Fatalf("non goals = %#v", packet.NonGoals)
	}
	if !strings.HasPrefix(packet.EvidenceRef, packetEvidencePrefix) {
		t.Fatalf("evidence ref = %q", packet.EvidenceRef)
	}
}

func TestImportPacketCreatesIdempotentProposedWork(t *testing.T) {
	home := t.TempDir()
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	report, err := store.ImportPacket(packetPath, PacketImportOptions{}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Created {
		t.Fatal("first import should create work")
	}
	if report.Work.State != "proposed" {
		t.Fatalf("state = %q, want proposed", report.Work.State)
	}
	if report.Work.Owner != "compound-core" {
		t.Fatalf("owner = %q", report.Work.Owner)
	}
	if report.Work.Authority.SourceClass != "source" ||
		!strings.HasPrefix(report.Work.Authority.SourceRevision, "sha256:") {
		t.Fatalf("authority = %+v", report.Work.Authority)
	}
	if report.Work.Uncertainty.Level != "inferred" {
		t.Fatalf("uncertainty = %+v", report.Work.Uncertainty)
	}
	if report.Work.CanonicalSurface != "packet:compound-goal-packet-capability-intake" {
		t.Fatalf("canonical surface = %q", report.Work.CanonicalSurface)
	}
	if len(report.Work.EvidenceRefs) != 1 || report.Work.EvidenceRefs[0] != report.EvidenceRef {
		t.Fatalf("evidence refs = %v report=%q", report.Work.EvidenceRefs, report.EvidenceRef)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("packet import must not create claims ledger, stat err=%v", err)
	}

	copyPath := filepath.Join(t.TempDir(), "renamed-packet.md")
	data, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	reimport, err := store.ImportPacket(copyPath, PacketImportOptions{}, fixedNow.Add(1))
	if err != nil {
		t.Fatal(err)
	}
	if reimport.Created {
		t.Fatal("same packet content should reuse existing work")
	}
	if reimport.Work.ID != report.Work.ID {
		t.Fatalf("work id changed across path move: %q vs %q", reimport.Work.ID, report.Work.ID)
	}
	_, events, err := store.Show(report.Work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("idempotent reimport should not append lifecycle events: %+v", events)
	}
}
