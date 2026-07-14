package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"compound/internal/home"
	"compound/internal/work"
)

func TestWorkShowEmitsWorkbenchPacketPathWithDefaultResolver(t *testing.T) {
	repoRoot := t.TempDir()
	packetDir := filepath.Join(repoRoot, "docs_private", "plan", "packets")
	if err := os.MkdirAll(packetDir, 0o755); err != nil {
		t.Fatalf("create packet dir: %v", err)
	}
	canonicalPacketPath := filepath.Join(packetDir, "default-resolver.md")
	worktreePacketDir := filepath.Join(t.TempDir(), "executor-worktree", "docs_private", "plan", "packets")
	if err := os.MkdirAll(worktreePacketDir, 0o755); err != nil {
		t.Fatalf("create worktree packet dir: %v", err)
	}
	importedPacketPath := filepath.Join(worktreePacketDir, "default-resolver.md")
	packetBody := `# Default resolver packet
Date: 2026-07-14
Status: ready
Owner: codex
Proof-medium: cli
Success: work show uses the same default packet directory as workbench.

## Objective
Prove packet path resolution without COMPOUND_PACKET_DIR.
`
	if err := os.WriteFile(importedPacketPath, []byte(packetBody), 0o644); err != nil {
		t.Fatalf("write imported packet: %v", err)
	}

	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("COMPOUND_PACKET_DIR", "")
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	imported, err := store.ImportPacket(importedPacketPath, work.PacketImportOptions{Owner: "codex"}, cliTestNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
	if err := os.Remove(importedPacketPath); err != nil {
		t.Fatalf("remove stale worktree packet: %v", err)
	}
	if err := os.WriteFile(canonicalPacketPath, []byte(packetBody), 0o644); err != nil {
		t.Fatalf("write canonical packet: %v", err)
	}

	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("change cwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalCWD) })
	offered, err := work.OfferedPackets(defaultWorkbenchPacketDir())
	if err != nil || len(offered.Items) != 1 {
		t.Fatalf("workbench packet resolution: items=%+v err=%v", offered.Items, err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"show", imported.Work.ID, "--json"})
	})
	var shown struct {
		PacketPath string `json:"packet_path"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("decode work show: %v\n%s", err, out)
	}
	offeredPath, err := filepath.Abs(offered.Items[0].Path)
	if err != nil {
		t.Fatalf("resolve offered packet path: %v", err)
	}
	if shown.PacketPath != offeredPath {
		t.Fatalf("packet_path = %q, want workbench path %q", shown.PacketPath, offeredPath)
	}
	if shown.PacketPath == importedPacketPath {
		t.Fatalf("packet_path reused deleted import path %q", importedPacketPath)
	}
	if shown.PacketPath != canonicalPacketPath {
		t.Fatalf("packet_path = %q, want canonical current path %q", shown.PacketPath, canonicalPacketPath)
	}
	if _, err := os.Stat(shown.PacketPath); err != nil {
		t.Fatalf("packet_path does not exist: %v", err)
	}
}

func TestWorkShowOmitsPacketPathForNonPacketWork(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	created, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "non-packet work omits packet path"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"show", created.ID, "--json"})
	})
	var shown map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("decode work show: %v\n%s", err, out)
	}
	if _, ok := shown["packet_path"]; ok {
		t.Fatalf("non-packet work emitted packet_path:\n%s", out)
	}
}
