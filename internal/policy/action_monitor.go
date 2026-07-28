package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
)

const (
	ActionDomainUnknown    = "unknown"
	ActionDomainShell      = "shell"
	ActionDomainFilesystem = "filesystem"
	ActionDomainNetwork    = "network"
	ActionDomainBrowser    = "browser"
	ActionDomainMessaging  = "messaging"
	ActionDomainConnector  = "connector"
	ActionDomainSecrets    = "secrets"
	ActionDomainInstall    = "install"
	ActionDomainDeploy     = "deploy"
	ActionDomainMemory     = "memory"
	ActionDomainControl    = "control"

	ClassifierRiskUnknown    = ""
	ClassifierRiskBenign     = "benign"
	ClassifierRiskSuspicious = "suspicious"
	ClassifierRiskMalicious  = "malicious"

	ExactActionApprovalSchema = "compound.exact_action_approval.v0.1"
	ActionBindingSchema       = "compound.action_binding.v0.1"
)

type ToolActionRequest struct {
	WorkID             string
	Actor              string
	Harness            string
	Tool               string
	Arguments          map[string]any
	ToolSchemaRevision string
	Sources            []envelope.SourceEnvelope
	ClassifierRisk     string
	FirstPartyCommands FirstPartyCommandCatalog
}

type ActionClassification struct {
	Known            bool
	Domain           string
	Operation        string
	MutationClass    string
	Reversibility    string
	BlastRadius      string
	TargetKind       string
	Destination      string
	RequiredSink     string
	NeedsSchema      bool
	NeedsDestination bool
}

type ActionBinding struct {
	SchemaVersion      string   `json:"schema_version"`
	ActionIntentID     string   `json:"action_intent_id"`
	Tool               string   `json:"tool"`
	Domain             string   `json:"domain"`
	Operation          string   `json:"operation"`
	ArgumentsHash      string   `json:"arguments_hash"`
	Destination        string   `json:"destination"`
	InputRefs          []string `json:"input_refs"`
	ToolSchemaRevision string   `json:"tool_schema_revision"`
	PolicyRevision     string   `json:"policy_revision"`
}

type MonitoredAction struct {
	Request        ToolActionRequest
	Classification ActionClassification
	Intent         envelope.ActionIntent
	Decision       envelope.PolicyDecision
	Binding        ActionBinding
}

// ClassifyToolAction is deterministic and conservative. Classification can
// increase policy burden, but no classifier result directly grants authority.
func ClassifyToolAction(request ToolActionRequest) ActionClassification {
	tool := strings.ToLower(strings.TrimSpace(request.Tool))
	operation := toolOperation(tool)
	destination := actionDestination(request.Arguments)
	classification := ActionClassification{
		Known:            true,
		Operation:        operation,
		Destination:      destination,
		MutationClass:    "security",
		Reversibility:    "hard",
		BlastRadius:      "account",
		TargetKind:       "tool",
		NeedsDestination: true,
	}

	switch {
	case isMemoryTool(tool):
		classification.Domain = ActionDomainMemory
		classification.TargetKind = "memory"
		classification.RequiredSink = memorySink(operation)
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "manual"
		classification.BlastRadius = "memory"
	case isSecretTool(tool):
		classification.Domain = ActionDomainSecrets
		classification.TargetKind = "secret"
		classification.RequiredSink = "secrets"
	case isDeployTool(tool):
		classification.Domain = ActionDomainDeploy
		classification.TargetKind = "deployment"
		classification.RequiredSink = "deploy"
		classification.MutationClass = "public"
		classification.Reversibility = "manual"
		classification.BlastRadius = "production"
	case isInstallTool(tool):
		classification.Domain = ActionDomainInstall
		classification.TargetKind = "package"
		classification.RequiredSink = "install"
		classification.MutationClass = "security"
		classification.Reversibility = "manual"
		classification.BlastRadius = "host"
	case isControlTool(tool):
		classification = classifyControlAction(classification, tool)
	case isMessagingTool(tool):
		classification.Domain = ActionDomainMessaging
		classification.TargetKind = "recipient"
		classification.RequiredSink = "messaging"
		classification.MutationClass = "public"
		classification.Reversibility = "hard"
		classification.BlastRadius = "public"
	case isBrowserTool(tool):
		classification = classifyBrowserAction(classification, operation)
	case isNetworkTool(tool):
		classification.Domain = ActionDomainNetwork
		classification.TargetKind = "network"
		classification.RequiredSink = "network"
		classification.Operation = networkOperation(tool)
		classification.MutationClass = "read"
		classification.Reversibility = "none"
		classification.BlastRadius = "internet"
	case isConnectorTool(tool):
		classification.Domain = ActionDomainConnector
		classification.TargetKind = "connector"
		classification.RequiredSink = "connector"
		classification.MutationClass = connectorMutationClass(operation)
		classification.Reversibility = connectorReversibility(operation)
		classification.BlastRadius = "account"
		classification.NeedsSchema = true
	case isFilesystemWriteTool(tool):
		classification.Domain = ActionDomainFilesystem
		classification.TargetKind = "file"
		classification.RequiredSink = "filesystem"
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "easy"
		classification.BlastRadius = "local"
	case isFilesystemReadTool(tool):
		classification.Domain = ActionDomainFilesystem
		classification.TargetKind = "file"
		classification.RequiredSink = "reader"
		classification.MutationClass = "read"
		classification.Reversibility = "none"
		classification.BlastRadius = "local"
		classification.NeedsDestination = false
	case isPTYTool(tool):
		classification.Domain = ActionDomainShell
		classification.TargetKind = "session"
		classification.RequiredSink = "shell"
		classification = classifyPTYAction(
			classification,
			stringArgument(request.Arguments, "chars"),
			request.FirstPartyCommands,
		)
	case isShellTool(tool):
		classification.Domain = ActionDomainShell
		classification.TargetKind = "command"
		classification.RequiredSink = "shell"
		classification = classifyShellAction(
			classification,
			stringArgument(request.Arguments, "command", "cmd"),
			request.FirstPartyCommands,
		)
	default:
		classification.Known = false
		classification.Domain = ActionDomainUnknown
		classification.Operation = "unknown"
		classification.RequiredSink = "unknown"
		classification.NeedsSchema = true
	}

	if classification.Destination == "" && classification.Domain == ActionDomainShell {
		if command := stringArgument(request.Arguments, "command", "cmd"); command != "" {
			classification.Destination = "command:" + actionHash([]byte(command))
		}
	}
	return classification
}

func MonitorToolAction(request ToolActionRequest, cfg Config, now time.Time) MonitoredAction {
	classification := ClassifyToolAction(request)
	argumentsHash := hashActionArguments(request.Arguments)
	inputBindings := make([]envelope.SourceBinding, 0, len(request.Sources))
	inputRefs := make([]string, 0, len(request.Sources))
	for i := range request.Sources {
		inputBindings = append(inputBindings, request.Sources[i].Binding())
		inputRefs = append(inputRefs, request.Sources[i].ID+"@"+request.Sources[i].ContentHash)
	}
	sort.Strings(inputRefs)

	workID := strings.TrimSpace(request.WorkID)
	if workID == "" {
		workID = "work_action_" + shortActionHash(request.Harness, request.Actor, request.Tool, argumentsHash)
	}
	intentID := "act_" + shortActionHash(workID, request.Actor, request.Tool, argumentsHash, now.UTC().Format(time.RFC3339Nano))
	targetID := classification.Destination
	if targetID == "" {
		targetID = "unresolved:" + strings.TrimPrefix(argumentsHash, "sha256:")[:12]
	}
	intent := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            intentID,
			CreatedAt:     now.UTC().Format(time.RFC3339Nano),
			Producer:      envelope.Producer{Kind: "reference_monitor", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          actionRisk(classification),
			EvidenceRefs:  append([]string{}, inputRefs...),
			Uncertainty:   envelope.Uncertainty{Level: actionUncertainty(classification)},
		},
		Actor:          firstNonEmptyMonitor(request.Actor, request.Harness, "unknown-actor"),
		Capability:     classification.Domain + "." + classification.Operation,
		Action:         classification.Operation,
		Target:         envelope.ActionTarget{Kind: classification.TargetKind, ID: targetID},
		Destination:    classification.Destination,
		ArgumentsHash:  argumentsHash,
		InputSources:   inputBindings,
		SchemaRevision: strings.TrimSpace(request.ToolSchemaRevision),
		MutationClass:  classification.MutationClass,
		Reversibility:  classification.Reversibility,
		BlastRadius:    classification.BlastRadius,
		RollbackPlan:   defaultActionRollback(classification),
	}

	decision := Evaluate(intent, cfg, now)
	securityFloor := ""
	securityReasons := []string{}
	constrain := func(outcome, reason string) {
		decision = raiseActionDecision(decision, outcome, reason, intent, now)
		if securityFloor == "" || severity[securityFloor] < severity[outcome] {
			securityFloor = outcome
		}
		securityReasons = append(securityReasons, reason)
	}
	if !classification.Known {
		constrain("hold", "unknown tool/action classification fails closed")
	}
	if classification.NeedsSchema && strings.TrimSpace(request.ToolSchemaRevision) == "" {
		constrain("hold", "missing tool schema revision fails closed")
	}
	if classification.NeedsDestination && strings.TrimSpace(classification.Destination) == "" {
		constrain("hold", "missing or unresolved destination fails closed")
	}
	for i := range request.Sources {
		source := &request.Sources[i]
		if err := source.Validate(); err != nil {
			constrain("deny", "invalid source lineage: "+err.Error())
			continue
		}
		if classification.RequiredSink != "" && !sourceAllowsSink(*source, classification.RequiredSink) {
			constrain("deny", fmt.Sprintf("source %s does not allow sink %s", source.ID, classification.RequiredSink))
		}
	}
	if classification.Domain == ActionDomainMemory && classification.RequiredSink == "memory_authority" &&
		leastSourceTrust(request.Sources) < sourceTrustRank(envelope.SourceTrustAdjudicated) {
		constrain("deny", "untrusted or unadjudicated source cannot enter authority memory")
	}
	switch strings.ToLower(strings.TrimSpace(request.ClassifierRisk)) {
	case ClassifierRiskMalicious:
		constrain("deny", "classifier marked content malicious; classifier may quarantine but never grant")
	case ClassifierRiskSuspicious:
		constrain("hold", "classifier marked content suspicious; classifier may raise burden but never grant")
	}
	intent.SecurityFloorOutcome = securityFloor
	intent.SecurityReasons = append([]string{}, securityReasons...)

	binding := ActionBinding{
		SchemaVersion:      ActionBindingSchema,
		ActionIntentID:     intent.ID,
		Tool:               intent.Capability,
		Domain:             classification.Domain,
		Operation:          classification.Operation,
		ArgumentsHash:      argumentsHash,
		Destination:        classification.Destination,
		InputRefs:          inputRefs,
		ToolSchemaRevision: strings.TrimSpace(request.ToolSchemaRevision),
		PolicyRevision:     actionPolicyRevision(cfg),
	}
	return MonitoredAction{
		Request:        request,
		Classification: classification,
		Intent:         intent,
		Decision:       decision,
		Binding:        binding,
	}
}

type ExactActionApproval struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	GrantedBy     string   `json:"granted_by"`
	BindingDigest string   `json:"binding_digest"`
	ExpiresAt     string   `json:"expires_at"`
	MaxUses       int      `json:"max_uses"`
	EvidenceRefs  []string `json:"evidence_refs"`
}

func NewExactActionApproval(id, grantedBy string, binding ActionBinding, expiresAt time.Time) (ExactActionApproval, error) {
	if strings.TrimSpace(id) == "" {
		return ExactActionApproval{}, fmt.Errorf("approval id is required")
	}
	if !HasOperatorProvenance(grantedBy) {
		return ExactActionApproval{}, fmt.Errorf("exact action approval requires operator provenance")
	}
	if expiresAt.IsZero() {
		return ExactActionApproval{}, fmt.Errorf("exact action approval expiry is required")
	}
	return ExactActionApproval{
		SchemaVersion: ExactActionApprovalSchema,
		ID:            id,
		GrantedBy:     grantedBy,
		BindingDigest: ActionBindingDigest(binding),
		ExpiresAt:     expiresAt.UTC().Format(time.RFC3339Nano),
		MaxUses:       1,
		EvidenceRefs:  []string{"action-binding:" + ActionBindingDigest(binding)},
	}, nil
}

func ActionBindingDigest(binding ActionBinding) string {
	copy := binding
	copy.ActionIntentID = ""
	copy.InputRefs = append([]string{}, binding.InputRefs...)
	sort.Strings(copy.InputRefs)
	data, _ := json.Marshal(copy)
	return actionHash(data)
}

type ApprovalConsumer interface {
	Consume(approvalID string, now time.Time) error
}

type ApprovalConsumptionStore struct {
	dir string
}

func NewApprovalConsumptionStore(dir string) *ApprovalConsumptionStore {
	return &ApprovalConsumptionStore{dir: filepath.Clean(dir)}
}

func (s *ApprovalConsumptionStore) Consume(approvalID string, now time.Time) error {
	if s == nil || strings.TrimSpace(s.dir) == "" || s.dir == "." {
		return fmt.Errorf("approval consumption store is required")
	}
	if strings.TrimSpace(approvalID) == "" || strings.ContainsAny(approvalID, `/\`) {
		return fmt.Errorf("invalid approval id")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create approval consumption store: %w", err)
	}
	path := filepath.Join(s.dir, approvalID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("approval %s already consumed", approvalID)
	}
	if err != nil {
		return fmt.Errorf("consume approval %s: %w", approvalID, err)
	}
	payload, marshalErr := json.Marshal(struct {
		ApprovalID string `json:"approval_id"`
		ConsumedAt string `json:"consumed_at"`
	}{approvalID, now.UTC().Format(time.RFC3339Nano)})
	if marshalErr == nil {
		_, marshalErr = file.Write(append(payload, '\n'))
	}
	closeErr := file.Close()
	if marshalErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if marshalErr != nil {
			return fmt.Errorf("record approval consumption: %w", marshalErr)
		}
		return fmt.Errorf("close approval consumption: %w", closeErr)
	}
	return nil
}

func ApplyExactActionApproval(
	monitored MonitoredAction,
	approval ExactActionApproval,
	store ApprovalConsumer,
	now time.Time,
) (envelope.PolicyDecision, error) {
	if approval.SchemaVersion != ExactActionApprovalSchema || approval.MaxUses != 1 {
		return monitored.Decision, fmt.Errorf("invalid exact action approval schema or max_uses")
	}
	if !HasOperatorProvenance(approval.GrantedBy) {
		return monitored.Decision, fmt.Errorf("exact action approval requires operator provenance")
	}
	expiry, err := time.Parse(time.RFC3339Nano, approval.ExpiresAt)
	if err != nil {
		return monitored.Decision, fmt.Errorf("invalid approval expiry: %w", err)
	}
	if !now.Before(expiry) {
		return monitored.Decision, fmt.Errorf("exact action approval expired at %s", approval.ExpiresAt)
	}
	if got, want := ActionBindingDigest(monitored.Binding), approval.BindingDigest; got != want {
		return monitored.Decision, fmt.Errorf("exact action approval binding mismatch")
	}
	if strings.TrimSpace(monitored.Intent.SecurityFloorOutcome) != "" {
		return monitored.Decision, fmt.Errorf("exact action approval cannot override action security floor")
	}
	if monitored.Decision.Outcome == "deny" {
		return monitored.Decision, fmt.Errorf("exact action approval cannot override deny")
	}
	if store == nil {
		return monitored.Decision, fmt.Errorf("approval consumption store is required")
	}
	if err := store.Consume(approval.ID, now); err != nil {
		return monitored.Decision, err
	}
	decision := monitored.Decision
	decision.Outcome = "allow_with_obligations"
	decision.StandingGrantID = ""
	decision.ExpiresAt = approval.ExpiresAt
	decision.Reasons = append(decision.Reasons,
		fmt.Sprintf("exact one-use operator approval %s matches action binding", approval.ID))
	decision.Obligations = mergeStrings(decision.Obligations,
		"emit a Receipt",
		"record exact approval consumption",
	)
	decision.EvidenceRefs = mergeStrings(decision.EvidenceRefs, approval.EvidenceRefs...)
	decision.ID = idFor("pol_", monitored.Intent.ID, "exact-approval", approval.ID, ts(now))
	return decision, nil
}

func raiseActionDecision(
	decision envelope.PolicyDecision,
	outcome, reason string,
	intent envelope.ActionIntent,
	now time.Time,
) envelope.PolicyDecision {
	decision.Reasons = append(decision.Reasons, reason)
	if severity[decision.Outcome] < severity[outcome] {
		decision.Outcome = outcome
		decision.Obligations = nil
		decision.StandingGrantID = ""
		decision.ExpiresAt = ""
		decision.ID = idFor("pol_", intent.ID, outcome, actionHash([]byte(reason)), ts(now))
	}
	return decision
}

func toolOperation(tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return "unknown"
	}
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(strings.TrimPrefix(tool, "mcp__"), "__")
		operationIndex := 1
		// Codex Apps is an enrolled connector aggregator. Its first component
		// names the wrapper and the second names the backing connector, so the
		// actual operation starts at component three.
		if len(parts) >= 3 && parts[0] == "codex_apps" {
			operationIndex = 2
		}
		if operationIndex < len(parts) && strings.TrimSpace(parts[operationIndex]) != "" {
			return strings.Trim(strings.Join(parts[operationIndex:], "__"), "._/:")
		}
	}
	for _, separator := range []string{".", "/", ":"} {
		if index := strings.LastIndex(tool, separator); index >= 0 && index+1 < len(tool) {
			return strings.Trim(tool[index+1:], "._/:")
		}
	}
	return strings.Trim(tool, "._/:")
}

func actionDestination(arguments map[string]any) string {
	for _, key := range []string{
		"recipient", "to", "url", "uri", "file_path", "path", "repo",
		"repository", "repository_full_name", "repo_full_name", "repository_url", "repository_id",
		"project", "deployment", "package", "secret", "claim", "target", "id",
		"session_id", "page", "query", "symbol", "projectPath", "project_path",
		"task_name", "server",
	} {
		if value := stringArgument(arguments, key); value != "" {
			return value
		}
	}
	return ""
}

func stringArgument(arguments map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := arguments[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func isMemoryTool(tool string) bool {
	return strings.Contains(tool, "memory.") || strings.Contains(tool, "memory_") ||
		strings.Contains(tool, "gbrain") || strings.Contains(tool, "candidate.promote")
}

func isSecretTool(tool string) bool {
	return containsAnyMonitor(tool, "secret", "credential", "keychain", "vault", "token.read", "password")
}

func isDeployTool(tool string) bool {
	return containsAnyMonitor(tool, "deploy", "publish", "release", "dns.", "domain.", "production")
}

func isInstallTool(tool string) bool {
	return containsAnyMonitor(tool, ".install", "_install", "npm.install", "package.add", "plugin.add", "plugin.install")
}

func isMessagingTool(tool string) bool {
	return containsAnyMonitor(tool, "send_message", "message.send", "gmail.send", "telegram.send", "slack.send", "mail.send")
}

func isBrowserTool(tool string) bool {
	return strings.Contains(tool, "browser") || strings.Contains(tool, "playwright") ||
		strings.Contains(tool, "computer_use") || strings.Contains(tool, "computer-use")
}

func classifyBrowserAction(classification ActionClassification, operation string) ActionClassification {
	classification.Domain = ActionDomainBrowser
	classification.TargetKind = "browser"
	classification.RequiredSink = "browser"
	classification.BlastRadius = "account"
	switch {
	case actionLooksReadOnly(operation) || containsAnyMonitor(operation, "open", "snapshot", "screenshot", "wait", "hover", "scroll"):
		classification.MutationClass = "read"
		classification.Reversibility = "none"
		classification.BlastRadius = "session"
		classification.NeedsDestination = false
	case containsAnyMonitor(operation, "submit", "send", "upload", "purchase", "checkout", "publish", "delete", "remove"):
		classification.MutationClass = "external_side_effect"
		classification.Reversibility = "hard"
	case containsAnyMonitor(operation, "click", "fill", "type", "press", "select", "focus", "navigate"):
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "easy"
		classification.BlastRadius = "session"
	default:
		classification.Known = false
		classification.MutationClass = "security"
		classification.Reversibility = "hard"
	}
	return classification
}

func isNetworkTool(tool string) bool {
	return containsAnyMonitor(tool, "webfetch", "web_fetch", "websearch", "web_search", "http.", "curl", "fetch_url")
}

func networkOperation(tool string) string {
	if containsAnyMonitor(tool, "search") {
		return "search"
	}
	return "fetch"
}

func isConnectorTool(tool string) bool {
	return strings.HasPrefix(tool, "mcp__") || strings.Contains(tool, "mcporter") ||
		strings.HasPrefix(tool, "connector.") || strings.HasPrefix(tool, "app.")
}

func isFilesystemWriteTool(tool string) bool {
	return tool == "write" || tool == "edit" || tool == "multiedit" || tool == "apply_patch" || tool == "notebookedit" ||
		strings.Contains(tool, "filesystem.write") || strings.Contains(tool, "file.write")
}

func isFilesystemReadTool(tool string) bool {
	return tool == "read" || tool == "grep" || tool == "glob" || tool == "view_image" ||
		strings.Contains(tool, "filesystem.read") || strings.Contains(tool, "file.read")
}

func isShellTool(tool string) bool {
	return containsAnyMonitor(tool, "bash", "shell", "exec_command", "terminal", "process.run", "command.run")
}

func isPTYTool(tool string) bool {
	return tool == "write_stdin"
}

func classifyPTYAction(
	classification ActionClassification,
	chars string,
	firstPartyCommands FirstPartyCommandCatalog,
) ActionClassification {
	switch chars {
	case "":
		classification.Operation = "session_poll"
		classification.MutationClass = "read"
		classification.Reversibility = "none"
		classification.BlastRadius = "local"
	case "\x03", "\x04", "\x1b":
		classification.Operation = "session_control"
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "easy"
		classification.BlastRadius = "local"
	default:
		classification = classifyShellAction(classification, chars, firstPartyCommands)
	}
	return classification
}

func isControlTool(tool string) bool {
	return tool == "update_plan" ||
		tool == "request_user_input" ||
		tool == "get_goal" ||
		tool == "tool_search" ||
		tool == "skill" ||
		isHarnessControlTool(tool) ||
		// Codex flattens the collaboration namespace into names such as
		// "collaborationspawn_agent"; classifyControlAction still validates
		// the operation and leaves unknown neighbors fail-closed.
		strings.HasPrefix(tool, "collaboration") ||
		strings.HasPrefix(tool, "list_mcp_resource") ||
		tool == "read_mcp_resource"
}

// isHarnessControlTool covers harness-native orchestration tools (agent
// fan-out, deferred-tool schema search, task tracking, session and workspace
// control). Scheduling, notification, and remote-trigger tools stay unlisted
// on purpose: they carry host or outbound side effects, so the unknown-tool
// fail-closed default is the correct classification until each gets an
// explicit domain.
func isHarnessControlTool(tool string) bool {
	switch tool {
	case "agent", "toolsearch", "sendmessage", "monitor", "lsp",
		"taskcreate", "taskupdate", "tasklist", "taskget", "taskoutput", "taskstop",
		"enterplanmode", "exitplanmode", "enterworktree", "exitworktree",
		"endconversation":
		return true
	}
	return false
}

func classifyControlAction(classification ActionClassification, tool string) ActionClassification {
	operation := toolOperation(tool)
	classification.Domain = ActionDomainControl
	classification.Operation = operation
	classification.TargetKind = "control"
	classification.RequiredSink = "control"
	classification.BlastRadius = "local"
	switch {
	// A skill invocation loads local instructions. Every action the skill then
	// takes is classified on its own, so holding the invocation adds an operator
	// prompt without adding a boundary.
	case actionLooksReadOnly(operation) || containsAnyMonitor(operation, "wait", "get_goal", "tool_search", "skill", "taskoutput", "monitor", "lsp"):
		classification.MutationClass = "read"
		classification.Reversibility = "none"
		classification.NeedsDestination = false
	case containsAnyMonitor(operation, "update_plan", "request_user_input", "spawn_agent", "followup_task", "send_message", "interrupt_agent", "agent", "taskcreate", "taskupdate", "taskstop", "sendmessage", "planmode", "worktree", "endconversation"):
		classification.MutationClass = "local_reversible"
		classification.Reversibility = "easy"
		classification.NeedsDestination = false
	default:
		classification.Known = false
		classification.MutationClass = "security"
		classification.Reversibility = "hard"
	}
	return classification
}

// classifyShellAction lives in shell_read_lane.go, which owns the verb-level
// decomposition every shell and PTY action is classified by.

func connectorMutationClass(operation string) string {
	if connectorOperationIsReadOnly(operation) {
		return "read"
	}
	return "external_side_effect"
}

func connectorReversibility(operation string) string {
	if connectorOperationIsReadOnly(operation) {
		return "none"
	}
	return "manual"
}

func connectorOperationIsReadOnly(operation string) bool {
	if actionLooksReadOnly(operation) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "codegraph_explore", "codegraph_node", "codegraph_impact",
		"codegraph_files", "codegraph_callers", "codegraph_callees":
		return true
	default:
		return false
	}
}

func actionLooksReadOnly(operation string) bool {
	return containsAnyMonitor(operation, "read", "get", "list", "search", "query", "fetch", "inspect", "status", "view")
}

func memorySink(operation string) string {
	if containsAnyMonitor(operation, "promote", "authority", "commit", "canon") {
		return "memory_authority"
	}
	return "memory_candidate"
}

func containsAnyMonitor(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func sourceAllowsSink(source envelope.SourceEnvelope, sink string) bool {
	for _, allowed := range source.AllowedSinks {
		if allowed == sink {
			return true
		}
	}
	return false
}

func sourceTrustRank(trust string) int {
	for i, candidate := range envelope.SourceTrustClasses {
		if candidate == trust {
			return i
		}
	}
	return -1
}

func leastSourceTrust(sources []envelope.SourceEnvelope) int {
	if len(sources) == 0 {
		return -1
	}
	least := len(envelope.SourceTrustClasses) - 1
	for i := range sources {
		rank := sourceTrustRank(sources[i].TrustClass)
		if rank < least {
			least = rank
		}
	}
	return least
}

func hashActionArguments(arguments map[string]any) string {
	data, err := json.Marshal(arguments)
	if err != nil {
		return actionHash([]byte(fmt.Sprintf("%#v", arguments)))
	}
	return actionHash(data)
}

func actionPolicyRevision(cfg Config) string {
	data, _ := json.Marshal(cfg)
	return actionHash(data)
}

func actionHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func shortActionHash(values ...string) string {
	return strings.TrimPrefix(actionHash([]byte(strings.Join(values, "\x00"))), "sha256:")[:12]
}

func actionRisk(classification ActionClassification) string {
	switch classification.MutationClass {
	case "read":
		return "tier0"
	case "local_reversible":
		return "tier1"
	case "external_side_effect":
		return "tier2"
	default:
		return "tier3"
	}
}

func actionUncertainty(classification ActionClassification) string {
	if classification.Known {
		return "known"
	}
	return "unknown"
}

func defaultActionRollback(classification ActionClassification) string {
	switch classification.Reversibility {
	case "easy":
		return "restore captured pre-image"
	case "manual":
		return "use provider or system-specific rollback with receipt"
	default:
		return ""
	}
}

func firstNonEmptyMonitor(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
