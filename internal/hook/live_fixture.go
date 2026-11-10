package hook

// Live hook input translation: the native PreToolUse / PostToolUse /
// SessionStart / UserPromptSubmit payload a harness writes on stdin becomes a
// Fixture (ActionIntents, source envelopes, coordination delivery). This code
// graduated unchanged from cmd/compound so the synchronous hook and the spool
// drainer share one translator inside the hook package.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/hookplan"
	"compound/internal/policy"
	"compound/internal/work"
)

// LiveFirstPartyCommands returns the catalog of Compound's own CLI commands
// that tool classification uses. cmd/compound owns the command registry and
// installs its catalog here at init; the default knows no commands.
var LiveFirstPartyCommands = func() policy.FirstPartyCommandCatalog {
	return policy.NewFirstPartyCommandCatalogFromSpecs(nil)
}

// liveFirstNonEmpty returns the first value that is not blank, untrimmed
// (the hook package's firstNonEmpty trims; live translation never did).
func liveFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type LiveInput struct {
	HookEventName      string                    `json:"hook_event_name"`
	SessionID          string                    `json:"session_id"`
	Source             string                    `json:"source"`
	Model              string                    `json:"model"`
	Trigger            string                    `json:"trigger"`
	Cwd                string                    `json:"cwd"`
	Prompt             string                    `json:"prompt"`
	ToolName           string                    `json:"tool_name"`
	ToolInput          map[string]any            `json:"tool_input"`
	ToolSchemaRevision string                    `json:"tool_schema_revision"`
	SourceEnvelopes    []envelope.SourceEnvelope `json:"source_envelopes"`
	ClassifierRisk     string                    `json:"classifier_risk"`
	ToolResponse       json.RawMessage           `json:"tool_response"` // post-tool-use: the tool's output body; large bodies are externalized (C3)
	Matcher            string                    `json:"matcher"`
	TranscriptPath     string                    `json:"transcript_path"`
	TurnID             string                    `json:"turn_id"`
	StopHookActive     bool                      `json:"stop_hook_active"`
	LastAssistant      string                    `json:"last_assistant_message"`
	WorkID             string                    `json:"work_id"`
	CompoundWorkID     string                    `json:"compound_work_id"`
	RunAttemptID       string                    `json:"run_attempt_id"`
	Actor              string                    `json:"actor"`
	CompoundActor      string                    `json:"compound_actor"`
	ScopeRefs          []string                  `json:"scope_refs"`
	DependencyRefs     []string                  `json:"dependency_refs"`
	PolicyRefs         []string                  `json:"policy_refs"`
	BaseRevisions      []string                  `json:"base_revisions"`
	ResourceRefs       []string                  `json:"resource_refs"`
}

func BuildLiveFixture(parsed BudgetCommand, data []byte, now time.Time, homePath string, homeAware bool) (Fixture, error) {
	var in LiveInput
	if err := json.Unmarshal(data, &in); err != nil {
		return Fixture{}, fmt.Errorf("parse live hook stdin: %w", err)
	}
	in.ToolName = NormalizeNativeTool(parsed.Harness, in.ToolName)
	var activeSources []envelope.SourceEnvelope
	var sourceEffects []string
	if homeAware {
		var err error
		activeSources, sourceEffects, err = LiveIngressSourcesForHome(homePath, parsed.Harness, parsed.HookName, in, now)
		if err != nil {
			return Fixture{}, err
		}
	}
	in.SourceEnvelopes = mergeLiveSourceEnvelopes(activeSources, in.SourceEnvelopes)
	fx := Fixture{
		Hook:               parsed.HookName,
		Harness:            NormalizeHarness(parsed.Harness),
		Live:               parsed.Live,
		Cwd:                in.Cwd,
		SessionID:          strings.TrimSpace(in.SessionID),
		Prompt:             in.Prompt,
		Task:               liveTask(in),
		Tool:               in.ToolName,
		WorkID:             liveFirstNonEmpty(in.WorkID, in.CompoundWorkID),
		AdapterSideEffects: append([]string(nil), sourceEffects...),
	}
	if fx.Cwd == "" {
		fx.Cwd = os.Getenv("CLAUDE_PROJECT_DIR")
	}
	if homeAware {
		coordination, err := resolveLiveCoordinationDelivery(homePath, fx.Harness, parsed.HookName, in)
		if err != nil {
			return Fixture{}, err
		}
		if coordination != nil {
			fx.CoordinationDelivery = coordination
			fx.WorkID = coordination.Recipient.WorkID
			in.WorkID = coordination.Recipient.WorkID
			in.CompoundWorkID = ""
		}
	}
	switch parsed.HookName {
	case "pre-tool-use":
		var actions []envelope.ActionIntent
		var err error
		if homeAware {
			actions, err = LiveActionIntentsForHome(homePath, fx.Harness, in, now)
		} else {
			actions, err = LiveActionIntents(fx.Harness, in, now)
		}
		if err != nil {
			return Fixture{}, err
		}
		if len(actions) == 1 {
			fx.Action = &actions[0]
		} else {
			fx.Actions = actions
		}
	case "post-tool-use":
		if fx.Tool == "" {
			fx.Tool = "unknown"
		}
		if targetPaths := LiveToolPaths(fx.Tool, in.ToolInput); len(targetPaths) > 0 {
			fx.MutationInputRef = LiveMutationInputReference(fx.Harness, in, targetPaths)
		}
		fx.CapabilityUse = NormalizeCapabilityUse(fx.Harness, fx.Tool, in.ToolInput)
		// C3: carry the tool response body so a large one is externalized at the seam (blob +
		// observed candidate + pointer) instead of flooding context. work_id grounds the candidate.
		fx.ToolOutput = liveToolOutput(in.ToolResponse)
		fx.WorkID = liveFirstNonEmpty(in.WorkID, in.CompoundWorkID)
	case "session-end", "stop":
		fx.WorkID = liveFirstNonEmpty(in.WorkID, in.CompoundWorkID)
		fx.SessionID = strings.TrimSpace(in.SessionID)
		fx.TurnID = strings.TrimSpace(in.TurnID)
		fx.TranscriptPath = strings.TrimSpace(in.TranscriptPath) // scanned for consumed injected claims (claude-code SessionEnd carries it)
		fx.StopHookActive = in.StopHookActive
		fx.LastAssistant = strings.TrimSpace(in.LastAssistant)
		if fx.WorkID == "" && fx.SessionID == "" {
			return Fixture{}, fmt.Errorf("%s live stdin needs work_id, compound_work_id, or session_id", parsed.HookName)
		}
		fx.Summary = "session-end receipt from live hook event " + liveFirstNonEmpty(fx.SessionID, fx.WorkID, "unknown-session")
	}
	return fx, nil
}

func LiveIngressSourcesForHome(
	homePath, harnessName, hookName string,
	in LiveInput,
	now time.Time,
) ([]envelope.SourceEnvelope, []string, error) {
	if strings.TrimSpace(homePath) == "" || strings.TrimSpace(in.SessionID) == "" {
		return nil, nil, nil
	}
	store := policy.NewSessionSourceStore(homePath)
	switch hookName {
	case "user-prompt-submit":
		if strings.TrimSpace(in.Prompt) == "" {
			active, err := store.Active(in.SessionID)
			return active, nil, err
		}
		source := liveOperatorPromptSource(harnessName, in, now)
		ref, err := store.Record(in.SessionID, source, policy.SourceRecordReset, now)
		if err != nil {
			return nil, nil, fmt.Errorf("record operator prompt source: %w", err)
		}
		return []envelope.SourceEnvelope{source}, []string{ref}, nil
	case "post-tool-use":
		active, err := store.Active(in.SessionID)
		if err != nil {
			return nil, nil, fmt.Errorf("load post-tool source lineage: %w", err)
		}
		body := []byte(liveToolOutput(in.ToolResponse))
		if len(body) == 0 {
			return active, nil, nil
		}
		source, err := liveToolResultSource(harnessName, in, body, active, now)
		if err != nil {
			return nil, nil, err
		}
		ref, err := store.Record(in.SessionID, source, policy.SourceRecordAppend, now)
		if err != nil {
			return nil, nil, fmt.Errorf("record tool result source: %w", err)
		}
		return append(active, source), []string{ref}, nil
	case "session-start":
		active, err := store.Active(in.SessionID)
		if err != nil {
			return nil, nil, fmt.Errorf("load session-start source lineage: %w", err)
		}
		if !strings.Contains(strings.ToLower(strings.TrimSpace(in.Source)), "compact") || len(active) == 0 {
			return active, nil, nil
		}
		content := liveCompactionContent(in, active)
		source, err := envelope.DeriveSourceEnvelope(
			liveSourceHeader(in, "compaction", content, now),
			envelope.SourceOrigin{
				Kind:    "compaction",
				Locator: NormalizeHarness(harnessName) + ":session-start",
			},
			envelope.SourceTransformation{
				Kind:     "compaction",
				Producer: NormalizeHarness(harnessName),
				Model:    strings.TrimSpace(in.Model),
			},
			content,
			active...,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("derive compaction source: %w", err)
		}
		ref, err := store.Record(in.SessionID, source, policy.SourceRecordReplace, now)
		if err != nil {
			return nil, nil, fmt.Errorf("record compaction source: %w", err)
		}
		return []envelope.SourceEnvelope{source}, []string{ref}, nil
	default:
		active, err := store.Active(in.SessionID)
		if err != nil {
			return nil, nil, fmt.Errorf("load active source lineage: %w", err)
		}
		return active, nil, nil
	}
}

func liveCompactionContent(in LiveInput, sources []envelope.SourceEnvelope) []byte {
	refs := make([]string, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, source.ID+"@"+source.ContentHash)
	}
	sort.Strings(refs)
	body, _ := json.Marshal(struct {
		SessionID string   `json:"session_id"`
		Model     string   `json:"model,omitempty"`
		Trigger   string   `json:"trigger,omitempty"`
		Parents   []string `json:"parents"`
	}{
		SessionID: strings.TrimSpace(in.SessionID),
		Model:     strings.TrimSpace(in.Model),
		Trigger:   liveFirstNonEmpty(strings.TrimSpace(in.Trigger), strings.TrimSpace(in.Source)),
		Parents:   refs,
	})
	return body
}

func liveOperatorPromptSource(harnessName string, in LiveInput, now time.Time) envelope.SourceEnvelope {
	content := []byte(in.Prompt)
	return envelope.NewSourceEnvelope(
		liveSourceHeader(in, "operator-prompt", content, now),
		envelope.SourceOrigin{
			Kind:          "operator",
			Locator:       NormalizeHarness(harnessName) + ":user-prompt-submit",
			Principal:     "operator:local-harness",
			Authenticated: true,
		},
		content,
		envelope.SourceTrustAuthenticatedOperator,
		envelope.ConfidentialityInternal,
		liveOperatorAllowedSinks(),
	)
}

func liveToolResultSource(
	harnessName string,
	in LiveInput,
	content []byte,
	parents []envelope.SourceEnvelope,
	now time.Time,
) (envelope.SourceEnvelope, error) {
	classification := policy.ClassifyToolAction(policy.ToolActionRequest{
		Tool:               in.ToolName,
		Arguments:          in.ToolInput,
		FirstPartyCommands: LiveFirstPartyCommands(),
	})
	origin, trust, confidentiality, sinks := liveToolSourcePolicy(classification)
	transformationKind := liveToolTransformationKind(in.ToolName)
	if transformationKind == "subagent_result" {
		origin.Kind = "subagent"
	}
	origin.Locator = strings.TrimSpace(in.ToolName)
	header := liveSourceHeader(in, "tool-result", content, now)
	if len(parents) == 0 {
		source := envelope.NewSourceEnvelope(header, origin, content, trust, confidentiality, sinks)
		if err := source.Validate(); err != nil {
			return envelope.SourceEnvelope{}, fmt.Errorf("tool result source: %w", err)
		}
		return source, nil
	}
	source, err := envelope.DeriveSourceEnvelopeWithPolicy(
		header,
		origin,
		envelope.SourceTransformation{
			Kind:     transformationKind,
			Producer: NormalizeHarness(harnessName),
			Tool:     strings.TrimSpace(in.ToolName),
		},
		content,
		envelope.SourceDerivationPolicy{
			MaximumTrust:           trust,
			MinimumConfidentiality: confidentiality,
			AllowedSinks:           sinks,
		},
		parents...,
	)
	if err != nil {
		return envelope.SourceEnvelope{}, fmt.Errorf("derive tool result source: %w", err)
	}
	return source, nil
}

func liveSourceHeader(in LiveInput, kind string, content []byte, now time.Time) envelope.Header {
	return envelope.Header{
		SchemaVersion: envelope.SVSourceEnvelope,
		ID: "src_live_" + ShortHash(
			strings.TrimSpace(in.SessionID),
			kind,
			strings.TrimSpace(in.ToolName),
			ShortHash(string(content)),
			now.UTC().Format(time.RFC3339Nano),
		),
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
		Producer:  envelope.Producer{Kind: "hook", Name: "compound"},
		IDs: envelope.IDs{
			WorkID:    liveFirstNonEmpty(in.WorkID, in.CompoundWorkID),
			SessionID: strings.TrimSpace(in.SessionID),
		},
		Authority:    envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:      "private",
		Risk:         "tier1",
		EvidenceRefs: []string{},
		Uncertainty:  envelope.Uncertainty{Level: "known"},
	}
}

func liveToolSourcePolicy(classification policy.ActionClassification) (
	envelope.SourceOrigin,
	string,
	string,
	[]string,
) {
	switch classification.Domain {
	case policy.ActionDomainNetwork, policy.ActionDomainBrowser:
		return envelope.SourceOrigin{Kind: "web"},
			envelope.SourceTrustUntrusted,
			envelope.ConfidentialityPublic,
			livePublicDataSinks()
	case policy.ActionDomainMessaging:
		return envelope.SourceOrigin{Kind: "message"},
			envelope.SourceTrustUntrusted,
			envelope.ConfidentialityConfidential,
			liveConfidentialDataSinks()
	case policy.ActionDomainConnector:
		return envelope.SourceOrigin{Kind: "connector"},
			envelope.SourceTrustUntrusted,
			envelope.ConfidentialityConfidential,
			liveConfidentialDataSinks()
	case policy.ActionDomainSecrets:
		return envelope.SourceOrigin{Kind: "secret"},
			envelope.SourceTrustMixed,
			envelope.ConfidentialitySecret,
			[]string{"operator_display", "reader", "secrets"}
	case policy.ActionDomainFilesystem:
		return envelope.SourceOrigin{Kind: "file"},
			envelope.SourceTrustMixed,
			envelope.ConfidentialityInternal,
			LiveInternalDataSinks()
	case policy.ActionDomainMemory:
		return envelope.SourceOrigin{Kind: "memory"},
			envelope.SourceTrustMixed,
			envelope.ConfidentialityInternal,
			LiveInternalDataSinks()
	case policy.ActionDomainControl:
		return envelope.SourceOrigin{Kind: "control"},
			envelope.SourceTrustMixed,
			envelope.ConfidentialityInternal,
			LiveInternalDataSinks()
	default:
		return envelope.SourceOrigin{Kind: "tool"},
			envelope.SourceTrustMixed,
			envelope.ConfidentialityInternal,
			LiveInternalDataSinks()
	}
}

func liveOperatorAllowedSinks() []string {
	return []string{
		"browser", "connector", "control", "deploy", "filesystem", "install",
		"memory_authority", "memory_candidate", "messaging", "network",
		"operator_display", "reader", "secrets", "shell",
	}
}

func livePublicDataSinks() []string {
	return []string{
		"browser", "connector", "control", "deploy", "filesystem", "install",
		"memory_candidate", "messaging", "network", "operator_display", "reader", "shell",
	}
}

func LiveInternalDataSinks() []string {
	return []string{
		"browser", "connector", "control", "filesystem", "memory_candidate", "network",
		"operator_display", "reader", "shell",
	}
}

func liveConfidentialDataSinks() []string {
	return []string{
		"browser", "connector", "control", "filesystem", "memory_candidate",
		"messaging", "network", "operator_display", "reader", "shell",
	}
}

func liveToolTransformationKind(toolName string) string {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	if strings.Contains(normalized, "agent") || strings.Contains(normalized, "subagent") ||
		strings.Contains(normalized, "task") {
		return "subagent_result"
	}
	return "tool_result"
}

func mergeLiveSourceEnvelopes(groups ...[]envelope.SourceEnvelope) []envelope.SourceEnvelope {
	seen := map[string]bool{}
	var merged []envelope.SourceEnvelope
	for _, group := range groups {
		for _, source := range group {
			key := source.ID + "@" + source.ContentHash
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, source)
		}
	}
	return merged
}

func resolveLiveCoordinationDelivery(homePath, harnessName, hookName string, in LiveInput) (*CoordinationDeliveryInput, error) {
	homePath = strings.TrimSpace(homePath)
	sessionID := strings.TrimSpace(in.SessionID)
	if homePath == "" || sessionID == "" || strings.TrimSpace(in.Cwd) == "" {
		return nil, nil
	}
	var trigger string
	switch hookName {
	case "session-start", "user-prompt-submit":
		trigger = hookplan.DeliveryTriggerBoundary
	case "pre-tool-use":
		trigger = hookplan.DeliveryTriggerInterrupt
	default:
		return nil, nil
	}
	store, err := work.Open(homePath)
	if err != nil {
		return nil, err
	}
	resolved, ok, err := store.ResolveCollaborationRecipient(work.CollaborationRecipientQuery{
		Harness: harnessName, SessionID: sessionID, Cwd: in.Cwd,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	boundarySource := liveCoordinationBoundarySource(hookName, in.Source)
	cursorKey := strings.Join([]string{
		"harness=" + NormalizeHarness(harnessName),
		"session=" + sessionID,
		"work=" + resolved.WorkID,
	}, "|")
	return &CoordinationDeliveryInput{
		CursorKey: cursorKey,
		Recipient: hookplan.DeliveryRecipient{
			Actor:          resolved.Actor,
			WorkID:         resolved.WorkID,
			RunAttemptID:   resolved.RunAttemptID,
			ScopeRefs:      normalizeLiveRefs(resolved.ScopeRefs),
			DependencyRefs: normalizeLiveRefs(resolved.DependencyRefs),
			PolicyRefs:     normalizeLiveRefs(resolved.PolicyRefs),
			BaseRevisions:  normalizeLiveRefs(resolved.BaseRevisions),
			ResourceRefs:   normalizeLiveRefs(resolved.ResourceRefs),
		},
		Trigger:        trigger,
		BoundarySource: boundarySource,
	}, nil
}

func liveCoordinationBoundarySource(hookName, nativeSource string) string {
	switch hookName {
	case "user-prompt-submit":
		return "prompt"
	case "pre-tool-use":
		return "pre_tool_use"
	case "session-start":
		nativeSource = strings.ToLower(strings.TrimSpace(nativeSource))
		if nativeSource == "" {
			return "startup"
		}
		return nativeSource
	default:
		return strings.ReplaceAll(strings.TrimSpace(hookName), "-", "_")
	}
}

func normalizeLiveRefs(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

// liveToolOutput normalizes a claude-code PostToolUse tool_response into the body C3 externalizes:
// a JSON string (e.g. a Bash tool's stdout) is decoded to its underlying text, so the round-trip is
// the real output rather than an escaped JSON token; a structured response is carried as its raw
// JSON. Absent/null stays empty (the byte-identical legacy path — nothing to externalize).
func liveToolOutput(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return trimmed
}

func liveTask(in LiveInput) string {
	if strings.TrimSpace(in.Prompt) != "" {
		return strings.TrimSpace(in.Prompt)
	}
	if strings.TrimSpace(in.HookEventName) != "" {
		return strings.TrimSpace(in.HookEventName)
	}
	return strings.TrimSpace(in.SessionID)
}

func LiveActionIntents(harnessName string, in LiveInput, now time.Time) ([]envelope.ActionIntent, error) {
	return LiveActionIntentsForHome("", harnessName, in, now)
}

func LiveActionIntentsForHome(homePath, harnessName string, in LiveInput, now time.Time) ([]envelope.ActionIntent, error) {
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		return nil, fmt.Errorf("pre-tool-use live stdin needs tool_name")
	}
	targetPaths := LiveToolPaths(toolName, in.ToolInput)
	workID := liveFirstNonEmpty(in.WorkID, in.CompoundWorkID)
	if workID == "" {
		workID = "work_live_" + ShortHash(in.SessionID, in.Cwd)
	}
	schemaRevision := strings.TrimSpace(in.ToolSchemaRevision)
	if schemaRevision == "" && !strings.HasPrefix(strings.ToLower(toolName), "mcp__") {
		schemaRevision = "builtin:" + strings.ToLower(toolName)
	}
	if schemaRevision == "" {
		schemaRevision = liveCapabilitySchemaRevision(homePath, toolName)
	}
	if len(targetPaths) == 0 && toolName != "apply_patch" {
		monitored := LiveMonitoredAction(harnessName, in, workID, schemaRevision, in.ToolInput, now)
		return []envelope.ActionIntent{monitored.Intent}, nil
	}
	mutationClass := "local_reversible"
	unparsedPatch := false
	if len(targetPaths) == 0 {
		if toolName != "apply_patch" {
			return nil, fmt.Errorf("pre-tool-use live stdin needs a file target or parseable apply_patch command")
		}
		// Unknown patch grammar is still a mutation attempt. Carry it through the
		// normal policy seam as an input-integrity risk, which fails closed to hold.
		targetPaths = []string{liveFirstNonEmpty(strings.TrimSpace(in.Cwd), ".")}
		mutationClass = "security"
		unparsedPatch = true
	}
	action := "use_tool"
	capability := "tool." + strings.ToLower(toolName)
	switch toolName {
	case "Write":
		action = "write_file"
		capability = "filesystem.write"
	case "Edit":
		action = "edit_file"
		capability = "filesystem.write"
	case "MultiEdit":
		action = "multi_edit_file"
		capability = "filesystem.write"
	case "apply_patch":
		action = "edit_file"
		capability = "filesystem.write"
		if len(targetPaths) > 1 {
			action = "multi_edit_file"
		}
		if unparsedPatch {
			action = "unparsed_apply_patch"
		}
	}
	mutationInputRef := LiveMutationInputReference(harnessName, in, targetPaths)
	actions := make([]envelope.ActionIntent, 0, len(targetPaths))
	for _, targetPath := range targetPaths {
		// apply_patch carries its targets inside the patch body rather than a
		// file_path field. Bind each emitted intent to the exact parsed target
		// before monitoring so a valid local edit is not mislabeled as an
		// unresolved-destination action.
		monitorArguments := make(map[string]any, len(in.ToolInput)+1)
		for key, value := range in.ToolInput {
			monitorArguments[key] = value
		}
		monitorArguments["path"] = targetPath
		monitored := LiveMonitoredAction(harnessName, in, workID, schemaRevision, monitorArguments, now)
		id := "act_live_" + ShortHash(toolName, targetPath, in.SessionID, now.UTC().Format(time.RFC3339Nano))
		actions = append(actions, envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent,
				ID:            id,
				CreatedAt:     now.UTC().Format(time.RFC3339),
				Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
				IDs:           envelope.IDs{WorkID: workID},
				Authority:     envelope.Authority{SourceClass: "runtime"},
				Privacy:       "private",
				Risk:          "tier2",
				EvidenceRefs:  []string{mutationInputRef},
				Uncertainty:   envelope.Uncertainty{Level: "known"},
			},
			Actor:                  NormalizeHarness(harnessName),
			Capability:             capability,
			Action:                 action,
			Target:                 envelope.ActionTarget{Kind: "file", Path: targetPath},
			Destination:            targetPath,
			ArgumentsHash:          monitored.Intent.ArgumentsHash,
			InputSources:           append([]envelope.SourceBinding{}, monitored.Intent.InputSources...),
			SchemaRevision:         schemaRevision,
			SecurityFloorOutcome:   monitored.Intent.SecurityFloorOutcome,
			SecurityReasons:        append([]string{}, monitored.Intent.SecurityReasons...),
			CapturesWorkInProgress: monitored.Intent.CapturesWorkInProgress,
			MutationClass:          mutationClass,
			Reversibility:          "easy",
			BlastRadius:            "repo",
		})
	}
	return actions, nil
}

func liveCapabilitySchemaRevision(homePath, toolName string) string {
	if strings.TrimSpace(homePath) == "" || !capgraph.Exists(homePath) {
		return ""
	}
	store, err := capgraph.OpenReadOnly(homePath)
	if err != nil {
		return ""
	}
	nodes, err := store.List()
	if err != nil {
		return ""
	}
	candidates := liveCapabilityToolCandidates(toolName)
	var matched capgraph.Node
	for _, node := range nodes {
		if node.Retired {
			continue
		}
		if node.Kind != "tool" {
			continue
		}
		nodeNameKey := liveCapabilityKey(node.Name)
		nodeIDKey := liveCapabilityKey(strings.TrimPrefix(node.ID, "tool:"))
		for _, candidate := range candidates {
			if nodeNameKey == candidate.toolKey || nodeIDKey == candidate.toolKey {
				matched = node
				break
			}
		}
		if matched.ID != "" {
			break
		}
	}
	if matched.ID == "" {
		for _, node := range nodes {
			if node.Retired || node.Kind != "mcp" {
				continue
			}
			nodeKey := liveCapabilityKey(strings.TrimPrefix(node.ID, "mcp:"))
			for _, candidate := range candidates {
				if nodeKey == candidate.serverKey {
					matched = node
					break
				}
			}
			if matched.ID != "" {
				break
			}
		}
	}
	// Host-native plugin tools may be backed by a topology-owned local CLI
	// family rather than an mcporter server. Falling back to that accepted
	// family node preserves source revision provenance without inventing a
	// schema revision at the hook boundary.
	if matched.ID == "" {
		for _, node := range nodes {
			if node.Retired || node.Kind != "tool" {
				continue
			}
			nodeNameKey := liveCapabilityKey(node.Name)
			nodeIDKey := liveCapabilityKey(strings.TrimPrefix(node.ID, "tool:"))
			for _, candidate := range candidates {
				if nodeNameKey == candidate.serverKey || nodeIDKey == candidate.serverKey {
					matched = node
					break
				}
			}
			if matched.ID != "" {
				break
			}
		}
	}
	if matched.ID == "" {
		return ""
	}
	records, err := capgraph.LoadSourceAuthority(homePath)
	if err != nil {
		return ""
	}
	var sourceRevision string
	for _, record := range records {
		if slices.Contains(record.NodeIDs, matched.ID) && strings.TrimSpace(record.SourceRevision) != "" {
			sourceRevision = strings.TrimSpace(record.SourceRevision)
		}
	}
	if sourceRevision == "" {
		return ""
	}
	return "capgraph:" + matched.ID + "@" + sourceRevision
}

type liveCapabilityToolCandidate struct {
	toolKey   string
	serverKey string
}

func liveCapabilityToolCandidates(toolName string) []liveCapabilityToolCandidate {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	if !strings.HasPrefix(normalized, "mcp__") {
		return []liveCapabilityToolCandidate{{toolKey: liveCapabilityKey(normalized)}}
	}
	parts := strings.Split(strings.TrimPrefix(normalized, "mcp__"), "__")
	serverIndex := 0
	// Codex Apps is an enrolled connector aggregator, not the backing server.
	// Only this exact wrapper may shift the server boundary; arbitrary nested
	// namespaces must not borrow another family's accepted schema revision.
	if len(parts) >= 3 && parts[0] == "codex_apps" {
		serverIndex = 1
	}
	if serverIndex+1 >= len(parts) {
		return []liveCapabilityToolCandidate{{toolKey: liveCapabilityKey(normalized)}}
	}
	serverKey := liveCapabilityKey(parts[serverIndex])
	toolKey := liveCapabilityKey(parts[serverIndex] + "." + strings.Join(parts[serverIndex+1:], "__"))
	if serverKey == "" || toolKey == "" {
		return []liveCapabilityToolCandidate{{toolKey: liveCapabilityKey(normalized)}}
	}
	return []liveCapabilityToolCandidate{{
		toolKey:   toolKey,
		serverKey: serverKey,
	}}
}

func liveCapabilityKey(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func LiveMonitoredAction(
	harnessName string,
	in LiveInput,
	workID string,
	schemaRevision string,
	arguments map[string]any,
	now time.Time,
) policy.MonitoredAction {
	return policy.MonitorToolAction(policy.ToolActionRequest{
		WorkID:             workID,
		Actor:              liveFirstNonEmpty(in.Actor, in.CompoundActor, NormalizeHarness(harnessName)),
		Harness:            NormalizeHarness(harnessName),
		Tool:               strings.TrimSpace(in.ToolName),
		Arguments:          arguments,
		Cwd:                strings.TrimSpace(in.Cwd),
		ToolSchemaRevision: schemaRevision,
		Sources:            append([]envelope.SourceEnvelope{}, in.SourceEnvelopes...),
		ClassifierRisk:     in.ClassifierRisk,
		FirstPartyCommands: LiveFirstPartyCommands(),
		Posture:            harness.DefaultRegistry().Posture(NormalizeHarness(harnessName)),
	}, policy.DefaultConfig(), now)
}

// LiveMutationInputReference stores no patch/body content. Its digest binds the
// real harness session, tool, canonical complete target set, and canonical tool
// input. A different session or partial batch therefore cannot reuse a review.
func LiveMutationInputReference(harnessName string, in LiveInput, targetPaths []string) string {
	targets := make([]string, 0, len(targetPaths))
	for _, target := range targetPaths {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if localPath, local := LocalTargetPath(target, in.Cwd); local {
			targets = append(targets, localPath)
		} else {
			targets = append(targets, "locator:"+target)
		}
	}
	sort.Strings(targets)
	canonicalTargets := targets[:0]
	for _, target := range targets {
		if len(canonicalTargets) == 0 || canonicalTargets[len(canonicalTargets)-1] != target {
			canonicalTargets = append(canonicalTargets, target)
		}
	}
	toolInput, _ := json.Marshal(in.ToolInput)
	payload, _ := json.Marshal(struct {
		SchemaVersion string   `json:"schema_version"`
		Harness       string   `json:"harness"`
		SessionID     string   `json:"session_id"`
		ToolName      string   `json:"tool_name"`
		TargetPaths   []string `json:"target_paths"`
		ToolInput     string   `json:"tool_input_json"`
	}{
		SchemaVersion: "compound.live_mutation_input.v0.1",
		Harness:       NormalizeHarness(harnessName),
		SessionID:     strings.TrimSpace(in.SessionID),
		ToolName:      strings.TrimSpace(in.ToolName),
		TargetPaths:   canonicalTargets,
		ToolInput:     string(toolInput),
	})
	digest := sha256.Sum256(payload)
	prefix := "mutation-input:sha256:"
	if strings.TrimSpace(in.SessionID) == "" {
		prefix = "mutation-input-unbound:sha256:"
	}
	return prefix + hex.EncodeToString(digest[:])
}

// readOnlyBuiltinTools are the built-in tools that carry a file_path/path but do not
// write through it. Without this, LiveToolPaths saw the path key and routed them down
// the mutation branch, where they mapped to the generic "use_tool" action, which has no
// standing grant, which the policy layer holds — and on an --enforce lane a hold becomes
// permissionDecision "ask" (see livePermissionDecision). The result was an approval
// prompt on every file read: the gate fires so often it trains the operator to accept
// without reading, which is worse than not gating at all.
//
// Deliberately a small ALLOW-list of known-safe built-ins, never a deny-list: an
// unrecognized tool — including every mcp__* tool — keeps the old mutation-path
// treatment and stays fail-closed.
var readOnlyBuiltinTools = map[string]bool{
	"Read": true,
	"Glob": true,
	"Grep": true,
}

func LiveToolPaths(toolName string, input map[string]any) []string {
	if readOnlyBuiltinTools[strings.TrimSpace(toolName)] {
		return nil
	}
	for _, key := range []string{"file_path", "path"} {
		if raw, ok := input[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(raw)); s != "" && s != "<nil>" {
				return []string{s}
			}
		}
	}
	if toolName != "apply_patch" {
		return nil
	}
	command, _ := input["command"].(string)
	return applyPatchTargetPaths(command)
}

func applyPatchTargetPaths(command string) []string {
	prefixes := []string{"*** Add File:", "*** Update File:", "*** Delete File:", "*** Move to:"}
	paths := []string(nil)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(command, "\n") {
		for _, prefix := range prefixes {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if path == "" {
				break
			}
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				paths = append(paths, path)
			}
			break
		}
	}
	return paths
}

func ShortHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
