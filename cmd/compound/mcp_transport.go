// mcp_transport.go owns the local stdio JSON-RPC MCP transport for compound.
// It is a THIN framing layer: it routes JSON-RPC 2.0 envelopes over stdio and
// dispatches at most two prefixed tools back onto the same governed libraries
// the CLI already uses.
//
// Boundary:
//   - No second memory authority: the transport never loads a memory store or
//     adjudicates a candidate. memory-derived state is read solely through the
//     orient runtime materializer (the existing `compound orient` path), so a
//     single memory owner stays the rule.
//   - No shell injection: arguments are decoded into typed Go structs with
//     `DisallowUnknownFields`; values never cross an exec boundary.
//   - No auto-promotion / auto-adjudication: orient_recall returns ambient
//     recall refs the materializer already adjudicated; it never promotes.
//   - orient_recall is NOT read-only/idempotent by design: it persists sealed
//     runtime artifacts under COMPOUND_HOME (one EffectiveContract + one
//     ContextPack pair), exactly like `compound orient`. The ack surfaces the
//     deterministic paths the materializer wrote so the caller can audit
//     persistence without re-reading the home.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/finding"
	"compound/internal/home"
)

const (
	mcpFindingAckSchema = "compound.finding.record-ack.v1"
	mcpOrientAckSchema  = "compound.mcp.orient-recall-ack.v1"
	mcpServerName       = "compound-mcp-transport"
	mcpServerVersion    = "0.1.0"
	mcpProtocolVersion  = "2024-11-05"
	mcpFindingToolName          = "compound_finding_record"
	mcpOrientToolName           = "compound_orient_recall"
	mcpMaxLineBytes             = 1 << 20
	mcpMaxBodyBytes             = 64 << 10
	mcpMaxRecallRefs            = 256
	mcpMaxEvidenceRefs          = 32
	mcpMaxOrientTaskBytes       = 4096
	mcpMaxOrientHarnessBytes    = 64
	mcpMaxOrientTopicBytes      = 256
	mcpMaxOrientRepoKindBytes   = 64
	mcpMaxOrientModeBytes       = 32
	mcpMaxOrientCwdBytes        = 4096
)

// toolSet is the closed alphabet of MCP-prefixed tool names this transport
// exposes. Unknown names return -32001 (toolUnavailable) so a misconfigured
// host fails closed rather than racing against a wildcard.
var toolSet = map[string]struct{}{
	mcpFindingToolName: {},
	mcpOrientToolName:  {},
}

// ioIn/ioOut are test seams for newline-delimited framing. Production wires
// os.Stdin/Stdout.
var (
	ioIn  io.Reader = os.Stdin
	ioOut io.Writer = os.Stdout
)

// jsonRPCRequest is one JSON-RPC 2.0 envelope. Notifications omit ID; the
// dispatcher treats a missing / null ID as fire-and-forget.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCError is one JSON-RPC 2.0 error object. Message stays bounded (no
// body bytes, no private paths, no internal stack frames) so the transport
// cannot accidentally become a leak channel.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpInitializeResult is the result of an MCP `initialize` request.
type mcpInitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      mcpServerInfo          `json:"serverInfo"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// mcpToolDescriptor is one entry in `tools/list`. The JSON Schema field is
// typed structurally; the runtime validator is the same Go function the CLI
// uses (strictDecodeArgs) and refuses unknown top-level keys + non-string
// typed fields.
type mcpToolDescriptor struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// mcpToolCallParams is the typed shape of `tools/call` params.
type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// mcpToolCallResult is the typed shape of a successful `tools/call` result.
// structural content carries the typed ack (id/path/refs/...), content[0].text
// mirrors it as a single text block so older MCP hosts that pre-date
// structuredContent still get the body-free ack.
type mcpToolCallResult struct {
	Content []mcpToolContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
	Struct  interface{}      `json:"structuredContent,omitempty"`
}

type mcpToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// mcpFindingRecordInput is the typed input for compound_finding_record. All
// fields are required EXCEPT the optional provenance bag. The body byte cap
// prevents an unbounded caller from saturating stdio.
type mcpFindingRecordInput struct {
	Topic        string   `json:"topic"`
	Body         string   `json:"body"`
	WorkID       string   `json:"work_id,omitempty"`
	ProducedBy   string   `json:"produced_by,omitempty"`
	Supersedes   string   `json:"supersedes,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// mcpOrientRecallInput is the typed input for compound_orient_recall. task is
// the recall key; harness defaults to "codex" if absent (same default the CLI
// applies via firstNonEmpty). body_budget<=0 keeps the pack body-free — the
// same semantics as `compound orient` without --body-budget.
type mcpOrientRecallInput struct {
	Task       string `json:"task"`
	Harness    string `json:"harness,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	RepoKind   string `json:"repo_kind,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Topic      string `json:"topic,omitempty"`
	BodyBudget int    `json:"body_budget,omitempty"`
}

// mcpFindingRecordAck mirrors the body-free exact acknowledgement the
// `compound finding record --json` CLI emits. The transport must NOT add the
// body or provenance fields — the dedicated `show` surface is the body-bearing
// retrieval, and that boundary stays here.
type mcpFindingRecordAck struct {
	Schema     string `json:"schema"`
	Status     string `json:"status"`
	ID         string `json:"id"`
	Path       string `json:"path"`
	Privacy    string `json:"privacy"`
	Visibility string `json:"visibility"`
}

// mcpOrientRecallAck is the bounded task-recall result. active_decisions and
// active_memory are the ambient recall refs from the persisted pack, bounded by
// mcpMaxRecallRefs. body_pack_present mirrors whether the materializer emitted
// a body_pack section (bounded by body_budget). persisted_artifacts records the
// deterministic paths the runtime materializer wrote under COMPOUND_HOME — this
// is the bounded receipt the caller audits because orient_recall is
// intentionally non-idempotent.
type mcpOrientRecallAck struct {
	Schema             string                      `json:"schema"`
	PackID             string                      `json:"pack_id"`
	ContractID         string                      `json:"contract_id"`
	SummaryMD          string                      `json:"summary_md,omitempty"`
	ActiveDecisions    []string                    `json:"active_decisions"`
	ActiveMemory       []string                    `json:"active_memory"`
	BodyPackPresent    bool                        `json:"body_pack_present"`
	PersistedArtifacts mcpOrientPersistedArtifacts `json:"persisted_artifacts"`
	Warnings           []string                    `json:"warnings,omitempty"`
}

type mcpOrientPersistedArtifacts struct {
	EffectiveContractPath string `json:"effective_contract_path"`
	LatestContractPath    string `json:"latest_contract_path"`
	ContextPackPath       string `json:"context_pack_path"`
}

// JSON-RPC 2.0 standard error codes (spec §5.1) plus MCP-specific extension
// codes for bounded domain errors.
const (
	rpcParseError      = -32700
	rpcInvalidRequest  = -32600
	rpcMethodNotFound  = -32601
	rpcInvalidParams   = -32602
	rpcInternalError   = -32603
	rpcToolUnavailable = -32001
)

// runMcpTransportLoop reads newline-delimited JSON-RPC 2.0 envelopes from
// ioIn and writes responses to ioOut. A read error closes the loop without
// spamming the host; a write error is fatal (the host went away).
func runMcpTransportLoop() error {
	reader := bufio.NewReaderSize(ioIn, mcpMaxLineBytes)
	writer := bufio.NewWriter(ioOut)
	for {
		line, err := readBoundedJSONLine(reader, mcpMaxLineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return writer.Flush()
			}
			// Non-EOF read error closes the loop quietly. Logging here would
			// smear the JSON-RPC channel — the MCP host owns the transport.
			return writer.Flush()
		}
		if len(bytesTrim(line)) == 0 {
			continue
		}
		resp := dispatchJSONRPC(line)
		if resp == nil {
			continue
		}
		if err := writeJSONLine(writer, resp); err != nil {
			return err
		}
	}
}

// readBoundedJSONLine reads ONE JSON-RPC line up to max+1 bytes; over-cap
// returns an error so an ill-behaved host cannot OOM the transport. A missing
// newline is fatal for this transport (no framing ambiguity allowed).
//
// Implementation note: bufio.Reader.ReadSlice returns ErrBufferFull when the
// internal buffer fills before a newline. That is a SIGNAL to keep reading,
// NOT the transport-level "too large" error — we keep appending until we hit
// the newline OR exceed `max`.
func readBoundedJSONLine(r *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		seg, err := r.ReadSlice('\n')
		// ReadSlice always returns the partial slice when err != nil.
		out = append(out, seg...)
		if err == nil {
			if len(out) > max {
				return nil, fmt.Errorf("json-rpc line exceeds %d bytes", max)
			}
			return out, nil
		}
		// Error path: only bubble up the transport-level cap. ErrBufferFull
		// means "the bufio slab filled before we found '\n'" — keep reading.
		if !errors.Is(err, bufio.ErrBufferFull) {
			if len(out) > max {
				return nil, fmt.Errorf("json-rpc line exceeds %d bytes", max)
			}
			return out, err
		}
		if len(out) > max {
			return nil, fmt.Errorf("json-rpc line exceeds %d bytes", max)
		}
	}
}

// dispatchJSONRPC returns nil for notifications (no response required) or a
// typed response envelope for requests. Unknown methods return -32601 per
// JSON-RPC spec; unknown tools return -32001 per MCP convention.
func dispatchJSONRPC(raw []byte) []byte {
	var req jsonRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return encodeResponse(nil, &jsonRPCError{Code: rpcParseError, Message: "parse error"})
	}
	if strings.TrimSpace(req.JSONRPC) != "2.0" {
		return respondToID(req.ID, &jsonRPCError{Code: rpcInvalidRequest, Message: "jsonrpc must be exactly \"2.0\""})
	}
	if strings.TrimSpace(req.Method) == "" {
		return respondToID(req.ID, &jsonRPCError{Code: rpcInvalidRequest, Message: "method required"})
	}
	// Notifications carry no id and require no response.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		switch req.Method {
		case "notifications/initialized", "notifications/cancelled":
			return nil
		}
		// Unknown notification: silently ignore.
		return nil
	}
	switch req.Method {
	case "initialize":
		return respondToID(req.ID, mcpHandleInitialize(req))
	case "ping":
		return respondToID(req.ID, map[string]interface{}{})
	case "tools/list":
		return respondToID(req.ID, mcpHandleToolsList())
	case "tools/call":
		return respondToID(req.ID, mcpHandleToolsCall(req.Params))
	default:
		return respondToID(req.ID, &jsonRPCError{Code: rpcMethodNotFound, Message: "method not found: " + req.Method})
	}
}

func mcpHandleInitialize(req jsonRPCRequest) interface{} {
	// The protocolVersion is accepted but not consulted; this transport has
	// fixed capabilities and the protocol-version negotiation is advisory only
	// (the closed alphabet of two tools is the contract).
	_ = req
	return mcpInitializeResult{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities: map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		ServerInfo: mcpServerInfo{Name: mcpServerName, Version: mcpServerVersion},
	}
}

func mcpHandleToolsList() interface{} {
	return map[string]interface{}{
		"tools": []mcpToolDescriptor{
			mcpFindingRecordDescriptor(),
			mcpOrientRecallDescriptor(),
		},
	}
}

func mcpHandleToolsCall(rawParams json.RawMessage) interface{} {
	if len(rawParams) == 0 {
		return &jsonRPCError{Code: rpcInvalidParams, Message: "tools/call params required"}
	}
	var params mcpToolCallParams
	if err := strictDecodeArgs(rawParams, &params); err != nil {
		return &jsonRPCError{Code: rpcInvalidParams, Message: "tools/call params invalid: " + err.Error()}
	}
	if _, ok := toolSet[params.Name]; !ok {
		return &jsonRPCError{Code: rpcToolUnavailable, Message: "unknown tool: " + params.Name}
	}
	switch params.Name {
	case mcpFindingToolName:
		return mcpCallFindingRecord(params.Arguments)
	case mcpOrientToolName:
		return mcpCallOrientRecall(params.Arguments)
	default:
		return &jsonRPCError{Code: rpcToolUnavailable, Message: "tool dispatch unreachable"}
	}
}

// ---------- Tool handlers ----------

func mcpCallFindingRecord(rawArgs json.RawMessage) interface{} {
	if len(rawArgs) == 0 {
		return toolErrorResult("missing arguments", map[string]any{"reason": "missing_arguments"})
	}
	var in mcpFindingRecordInput
	if err := strictDecodeArgs(rawArgs, &in); err != nil {
		return toolErrorResult(err.Error(), map[string]any{"reason": "invalid_arguments"})
	}
	if strings.TrimSpace(in.Topic) == "" {
		return toolErrorResult("topic required", map[string]any{"reason": "topic_empty"})
	}
	if len(in.Body) == 0 {
		return toolErrorResult("body required", map[string]any{"reason": "body_empty"})
	}
	if len(in.Body) > mcpMaxBodyBytes {
		return toolErrorResult(fmt.Sprintf("body exceeds %d bytes", mcpMaxBodyBytes),
			map[string]any{"reason": "body_too_large", "limit_bytes": mcpMaxBodyBytes})
	}
	if len(in.EvidenceRefs) > mcpMaxEvidenceRefs {
		return toolErrorResult(fmt.Sprintf("evidence_refs exceeds %d", mcpMaxEvidenceRefs),
			map[string]any{"reason": "evidence_refs_too_many", "limit": mcpMaxEvidenceRefs})
	}
	for i, ref := range in.EvidenceRefs {
		if strings.TrimSpace(ref) == "" {
			return toolErrorResult("evidence_refs contains empty entry",
				map[string]any{"reason": "evidence_ref_blank", "index": i})
		}
	}
	homePath, herr := home.Resolve("")
	if herr != nil {
		return toolErrorResult("compound home unresolved",
			map[string]any{"reason": "home_uninitialized"})
	}
	res, err := finding.RecordWithStatus(homePath, finding.Finding{
		Topic:        strings.TrimSpace(in.Topic),
		Body:         in.Body,
		WorkID:       strings.TrimSpace(in.WorkID),
		ProducedBy:   strings.TrimSpace(in.ProducedBy),
		Supersedes:   strings.TrimSpace(in.Supersedes),
		EvidenceRefs: append([]string(nil), in.EvidenceRefs...),
	}, time.Now().UTC())
	if err != nil {
		// Surface a bounded error: the underlying message is internal (governed
		// storage refused the record) but never echoes the body, the body cid,
		// or any private path inside the message.
		return toolErrorResult(err.Error(),
			map[string]any{"reason": "record_failed"})
	}
	ack := mcpFindingRecordAck{
		Schema:     mcpFindingAckSchema,
		Status:     res.Status,
		ID:         res.Finding.ID,
		Path:       filepath.ToSlash(filepath.Join(finding.Subdir, res.Finding.ID+".json")),
		Privacy:    firstNonEmpty(res.Finding.Privacy, finding.PrivacyPrivate),
		Visibility: firstNonEmpty(res.Finding.Visibility, finding.VisibilityOperational),
	}
	text, _ := json.Marshal(ack)
	return mcpToolCallResult{
		Content: []mcpToolContent{{Type: "text", Text: string(text)}},
		Struct:  ack,
	}
}

func mcpCallOrientRecall(rawArgs json.RawMessage) interface{} {
	if len(rawArgs) == 0 {
		return toolErrorResult("missing arguments", map[string]any{"reason": "missing_arguments"})
	}
	var in mcpOrientRecallInput
	if err := strictDecodeArgs(rawArgs, &in); err != nil {
		return toolErrorResult(err.Error(), map[string]any{"reason": "invalid_arguments"})
	}
	if strings.TrimSpace(in.Task) == "" {
		return toolErrorResult("task required", map[string]any{"reason": "task_empty"})
	}
	if len(in.Task) > mcpMaxOrientTaskBytes {
		return toolErrorResult(fmt.Sprintf("task exceeds %d bytes", mcpMaxOrientTaskBytes),
			map[string]any{"reason": "task_too_large", "limit_bytes": mcpMaxOrientTaskBytes})
	}
	if len(in.Harness) > mcpMaxOrientHarnessBytes {
		return toolErrorResult(fmt.Sprintf("harness exceeds %d bytes", mcpMaxOrientHarnessBytes),
			map[string]any{"reason": "harness_too_large", "limit_bytes": mcpMaxOrientHarnessBytes})
	}
	if len(in.Cwd) > mcpMaxOrientCwdBytes {
		return toolErrorResult(fmt.Sprintf("cwd exceeds %d bytes", mcpMaxOrientCwdBytes),
			map[string]any{"reason": "cwd_too_large", "limit_bytes": mcpMaxOrientCwdBytes})
	}
	if len(in.RepoKind) > mcpMaxOrientRepoKindBytes {
		return toolErrorResult(fmt.Sprintf("repo_kind exceeds %d bytes", mcpMaxOrientRepoKindBytes),
			map[string]any{"reason": "repo_kind_too_large", "limit_bytes": mcpMaxOrientRepoKindBytes})
	}
	if len(in.Mode) > mcpMaxOrientModeBytes {
		return toolErrorResult(fmt.Sprintf("mode exceeds %d bytes", mcpMaxOrientModeBytes),
			map[string]any{"reason": "mode_too_large", "limit_bytes": mcpMaxOrientModeBytes})
	}
	if len(in.Topic) > mcpMaxOrientTopicBytes {
		return toolErrorResult(fmt.Sprintf("topic exceeds %d bytes", mcpMaxOrientTopicBytes),
			map[string]any{"reason": "topic_too_large", "limit_bytes": mcpMaxOrientTopicBytes})
	}
	if in.BodyBudget < 0 {
		return toolErrorResult("body_budget must be >= 0", map[string]any{"reason": "body_budget_negative"})
	}
	if in.BodyBudget > mcpMaxBodyBytes {
		return toolErrorResult(fmt.Sprintf("body_budget exceeds %d", mcpMaxBodyBytes),
			map[string]any{"reason": "body_budget_too_large", "limit": mcpMaxBodyBytes})
	}
	homePath, herr := home.Resolve("")
	if herr != nil {
		return toolErrorResult("compound home unresolved",
			map[string]any{"reason": "home_uninitialized"})
	}
	restoreEngine, err := configureMemoryEngineFromHome(homePath, "")
	if err != nil {
		return toolErrorResult("configure memory engine: "+err.Error(),
			map[string]any{"reason": "engine_configure_failed"})
	}
	defer restoreEngine()
	cwd := strings.TrimSpace(in.Cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	kind, kindVia, kerr := contract.ResolveRepoKindWithHome(homePath, in.RepoKind, cwd)
	if kerr != nil {
		return toolErrorResult(kerr.Error(), map[string]any{"reason": "repo_kind_unresolved"})
	}
	orient := contract.Input{
		Harness:     firstNonEmpty(strings.TrimSpace(in.Harness), "codex"),
		Cwd:         cwd,
		Task:        in.Task,
		RepoKind:    kind,
		RepoKindVia: kindVia,
		Mode:        firstNonEmpty(strings.TrimSpace(in.Mode), "execute"),
		Topic:       strings.TrimSpace(in.Topic),
	}
	now := time.Now()
	pack, err := materializeOrientContextPack(homePath, orient, in.BodyBudget, now)
	if err != nil {
		return toolErrorResult(err.Error(), map[string]any{"reason": "materialize_failed"})
	}
	artifacts := computePersistedOrientArtifacts(homePath, pack)
	ack := mcpOrientRecallAck{
		Schema:             mcpOrientAckSchema,
		PackID:             pack.ID,
		ContractID:         pack.IDs.ContractID,
		SummaryMD:          pack.SummaryMD,
		ActiveDecisions:    boundedRecallRefs(pack.ActiveDecisions, mcpMaxRecallRefs),
		ActiveMemory:       boundedRecallRefs(pack.ActiveMemory, mcpMaxRecallRefs),
		BodyPackPresent:    pack.BodyPack != nil,
		PersistedArtifacts: artifacts,
	}
	text, _ := json.Marshal(ack)
	return mcpToolCallResult{
		Content: []mcpToolContent{{Type: "text", Text: string(text)}},
		Struct:  ack,
	}
}

// computePersistedOrientArtifacts reconstructs the deterministic artifact
// paths the runtime materializer wrote. The materializer computes these from
// (homePath, contractID, packID, contractCreatedAt) by the same convention as
// contract.RecordRuntimeMaterialization; we mirror the convention here so the
// path receipt is byte-identical to what `compound orient` produced.
func computePersistedOrientArtifacts(homePath string, pack envelope.ContextPack) mcpOrientPersistedArtifacts {
	if strings.TrimSpace(homePath) == "" || strings.TrimSpace(pack.ID) == "" || strings.TrimSpace(pack.IDs.ContractID) == "" {
		return mcpOrientPersistedArtifacts{}
	}
	dateDir := ""
	if t, err := time.Parse(time.RFC3339, pack.CreatedAt); err == nil {
		dateDir = t.UTC().Format("2006-01-02")
	}
	if dateDir == "" {
		// Fallback: today's UTC date keeps the path deterministic even when
		// the header timestamp is unparseable. The materializer would have
		// refused such a pack upstream, so this branch is a defensive last
		// resort — it never widens the path to leak home internals.
		dateDir = time.Now().UTC().Format("2006-01-02")
	}
	return mcpOrientPersistedArtifacts{
		EffectiveContractPath: filepath.ToSlash(filepath.Join(homePath, "contracts", "effective", dateDir, pack.IDs.ContractID+".json")),
		LatestContractPath:    filepath.ToSlash(filepath.Join(homePath, "contracts", "effective", "latest.json")),
		ContextPackPath:       filepath.ToSlash(filepath.Join(homePath, "context", "packs", pack.ID+".json")),
	}
}

// ---------- Helpers: encoding, validation, IO ----------

// toolErrorResult wraps an error message as an MCP tool result with
// isError=true so the host sees the failure without leaking it as a JSON-RPC
// error envelope (which would shift blame to the transport, not the tool).
func toolErrorResult(message string, extra map[string]any) mcpToolCallResult {
	payload := map[string]any{"error": message}
	for k, v := range extra {
		payload[k] = v
	}
	text, _ := json.Marshal(payload)
	return mcpToolCallResult{
		IsError: true,
		Content: []mcpToolContent{{Type: "text", Text: string(text)}},
		Struct:  payload,
	}
}

// boundedRecallRefs caps any unbounded recall refs slice at the transport's
// max, preserving order so callers observe exactly the same slices the
// materializer emitted up to the cap.
func boundedRecallRefs(refs []string, max int) []string {
	if len(refs) <= max {
		out := make([]string, len(refs))
		copy(out, refs)
		return out
	}
	out := make([]string, max)
	copy(out, refs[:max])
	return out
}

// strictDecodeArgs decodes json arguments into the target Go struct and
// REJECTS unknown top-level keys + structural mismatches. This is the same
// shape validation the CLI uses (the validateKnownFlags convention) —
// structural only, the typed contract is the JSON Schema side of the tool
// descriptor.
func strictDecodeArgs(raw json.RawMessage, target interface{}) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("arguments must decode to a single JSON object")
	}
	return nil
}

// mcpFindingRecordDescriptor is the typed schema for compound_finding_record.
// Unknown top-level keys are refused by strictDecodeArgs at call time.
func mcpFindingRecordDescriptor() mcpToolDescriptor {
	return mcpToolDescriptor{
		Name: mcpFindingToolName,
		Description: "Records one operational finding under COMPOUND_HOME/observed/findings via the same governed library `compound finding record` uses. " +
			"Idempotent on (topic, body): a replay returns the same id and never overwrites the first record's provenance. " +
			"Returns a body-free typed acknowledgement — the body remains retrievable only through the dedicated `compound finding show` surface.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"additionalProperties": false,
			"required": []string{"topic", "body"},
			"properties": map[string]interface{}{
				"topic":        map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 1024},
				"body":         map[string]interface{}{"type": "string", "minLength": 1, "maxLength": mcpMaxBodyBytes},
				"work_id":      map[string]interface{}{"type": "string", "maxLength": 1024},
				"produced_by":  map[string]interface{}{"type": "string", "maxLength": 256},
				"supersedes":   map[string]interface{}{"type": "string", "pattern": "^find_[a-f0-9]{12}$"},
				"evidence_refs": map[string]interface{}{
					"type":  "array",
					"maxItems": mcpMaxEvidenceRefs,
					"items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 1024},
				},
			},
		},
	}
}

// mcpOrientRecallDescriptor is the typed schema for compound_orient_recall.
// This tool persists sealed runtime artifacts (not read-only), so the schema
// mirrors `compound orient` exactly: one task + bounded harness/repo_kind/mode
// hints. body_budget<=0 keeps the pack body-free.
func mcpOrientRecallDescriptor() mcpToolDescriptor {
	return mcpToolDescriptor{
		Name: mcpOrientToolName,
		Description: "Compiles a sealed EffectiveContract+ContextPack pair via the same `compound orient` runtime materializer and persists the artifacts " +
			"under COMPOUND_HOME. NOT read-only/idempotent — a successful call writes one EffectiveContract (and a `latest.json` pointer) " +
			"plus one ContextPack. Returns bounded task recall (active decisions + active memory) plus the deterministic persisted paths. " +
			"body_budget > 0 enables a body_pack section; 0 keeps the pack body-free.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"additionalProperties": false,
			"required": []string{"task"},
			"properties": map[string]interface{}{
				"task":        map[string]interface{}{"type": "string", "minLength": 1, "maxLength": mcpMaxOrientTaskBytes},
				"harness":     map[string]interface{}{"type": "string", "maxLength": mcpMaxOrientHarnessBytes},
				"cwd":         map[string]interface{}{"type": "string", "maxLength": mcpMaxOrientCwdBytes},
				"repo_kind":   map[string]interface{}{"type": "string", "maxLength": mcpMaxOrientRepoKindBytes},
				"mode":        map[string]interface{}{"type": "string", "maxLength": mcpMaxOrientModeBytes},
				"topic":       map[string]interface{}{"type": "string", "maxLength": mcpMaxOrientTopicBytes},
				"body_budget": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": mcpMaxBodyBytes},
			},
		},
	}
}

// ---------- JSON-RPC response helpers ----------

// respondToID wraps a result OR error into a JSON-RPC response envelope
// preserving the request ID unchanged. nil result → marshaled null.
func respondToID(id json.RawMessage, result interface{}) []byte {
	switch v := result.(type) {
	case *jsonRPCError:
		return encodeResponse(id, v)
	case jsonRPCError:
		return encodeResponse(id, &v)
	case error:
		return encodeResponse(id, &jsonRPCError{Code: rpcInternalError, Message: v.Error()})
	default:
		return encodeResponse(id, nil, result)
	}
}

func encodeResponse(id json.RawMessage, err *jsonRPCError, result ...interface{}) []byte {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      idForResponse(id),
	}
	if err != nil {
		payload["error"] = err
	} else if len(result) > 0 {
		payload["result"] = result[0]
	}
	encoded, mErr := json.Marshal(payload)
	if mErr != nil {
		// Marshal failure is the worst-case transport-fatal state. Return a
		// parse-error response so the host at least sees a syntactically
		// valid envelope on the same ID.
		return mustEncode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      nil,
			"error":   &jsonRPCError{Code: rpcInternalError, Message: "encode failure"},
		})
	}
	return encoded
}

func mustEncode(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Should be impossible for the types passed in here.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"encode failure"}}`)
	}
	return b
}

// idForResponse returns the JSON value to put in the response id slot. A
// missing id in the request becomes an explicit JSON null (notification
// responses are forbidden by JSON-RPC 2.0).
func idForResponse(id json.RawMessage) interface{} {
	if len(id) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(id, &v); err != nil {
		return nil
	}
	return v
}

// writeJSONLine writes one response to the buffered writer and flushes.
func writeJSONLine(w *bufio.Writer, payload []byte) error {
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if err := w.WriteByte('\n'); err != nil {
		return err
	}
	return w.Flush()
}

// bytesTrim trims ASCII whitespace from both ends of a byte slice without
// allocating a new slice for the common no-op case.
func bytesTrim(b []byte) []byte {
	start, end := 0, len(b)
	for start < end {
		c := b[start]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		start++
	}
	for end > start {
		c := b[end-1]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		end--
	}
	return b[start:end]
}

func bytesHasSuffixNewline(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '\n'
}

// cmdMCPTransport is the dispatch seam the cmd/compound/main.go switch calls
// when invoked as `compound mcp-transport`. It rewires ioIn/ioOut to stdio
// (the test seams above let tests swap to pipes) and runs the transport loop
// until EOF.
func cmdMCPTransport(args []string) {
	if hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, "usage: compound mcp-transport")
		return
	}
	ioIn = os.Stdin
	ioOut = os.Stdout
	if err := runMcpTransportLoop(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-transport:", err)
		os.Exit(1)
	}
}
