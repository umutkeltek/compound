package hook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
)

var (
	skillPathPattern = regexp.MustCompile(`(?i)[/.]skills/([a-z0-9][a-z0-9-]*)/SKILL(?:\.md)?`)
	claimRefPattern  = regexp.MustCompile(`claim://[A-Za-z0-9_-]+`)
)

var mutatingTranscriptTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

type sessionTranscriptSummary struct {
	FirstUser      string
	LastAssistant  string
	FirstTimestamp string
	LastTimestamp  string
	FilesChanged   []string
	SkillsFired    []string
	ToolCalls      int
	BashCalls      int
	ConsumedRefs   []string
	UsedDeclared   bool
}

type transcriptEntry struct {
	Timestamp string             `json:"timestamp"`
	TS        string             `json:"ts"`
	Type      string             `json:"type"`
	Role      string             `json:"role"`
	Content   json.RawMessage    `json:"content"`
	Message   *transcriptMessage `json:"message"`
	Payload   *transcriptMessage `json:"payload"`
}

type transcriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type transcriptContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func buildWorklessSessionReceipt(name string, fx Fixture, now time.Time) (*envelope.Receipt, error) {
	path := strings.TrimSpace(fx.TranscriptPath)
	if path == "" {
		return nil, nil
	}
	summary, err := scanSessionTranscript(path)
	if err != nil {
		return nil, err
	}
	used := mergeUniqueStrings(summary.SkillsFired, summary.ConsumedRefs)
	rec := &envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            idFor("rec_", fx.Harness, name, fx.SessionID, hashStr(path), ts(now)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{SessionID: fx.SessionID},
			Scope:         envelope.Scope{Surface: fx.Cwd},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:  "completed",
		MoveType: "audit_only",
		StateDelta: envelope.StateDelta{
			Changed:      len(summary.FilesChanged) > 0,
			Summary:      renderSessionReceiptSummary(summary, path),
			FilesChanged: summary.FilesChanged,
			Artifacts:    []string{},
			Verification: []string{},
		},
		Cost: envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{
			Used:         used,
			Missed:       []string{},
			UsedDeclared: summary.UsedDeclared,
			Candidate:    []string{},
		},
		Memory:    envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:      envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs: []string{"transcript:" + path},
	}
	if err := rec.Validate(); err != nil {
		return nil, err
	}
	return rec, nil
}

// OperatorUtterance is one attributed operator (human) message pulled from a
// session transcript: verbatim text plus provenance (1-based line number in the
// .jsonl and the entry timestamp). It is the utterance-attribution surface the
// ruling harvester consumes so it never needs a parallel transcript parser
// (compound.session_distill_source is binding: consume the existing reader, add
// attribution, do not re-extract).
type OperatorUtterance struct {
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
	LineNo    int    `json:"line_no"`
}

// BlockingAsk is the last assistant question in a transcript plus the nearest
// preceding operator utterance as minimal context. It shares this reader so
// ask-triage does not grow a second transcript/extraction organ.
type BlockingAsk struct {
	Text, Context, Timestamp string
	LineNo                   int
}

func LastBlockingAsk(path string) (BlockingAsk, error) {
	f, err := os.Open(path)
	if err != nil {
		return BlockingAsk{}, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	context := ""
	var last BlockingAsk
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNo++
			entry, parseErr := parseTranscriptEntry([]byte(strings.TrimSpace(string(line))))
			if parseErr != nil {
				return BlockingAsk{}, parseErr
			}
			role, content := transcriptRoleContent(entry)
			text := strings.TrimSpace(textOfContent(content))
			if role == "user" {
				cleaned := cleanTranscriptUserText(text)
				if isOperatorUtterance(cleaned) {
					context = cleaned
				}
			}
			if role == "assistant" && text != "" {
				last = BlockingAsk{}
				if question := BlockingQuestion(text); question != "" {
					last = BlockingAsk{Text: question, Context: context, Timestamp: firstNonEmpty(entry.Timestamp, entry.TS), LineNo: lineNo}
				}
			}
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			break
		}
		return BlockingAsk{}, readErr
	}
	return last, nil
}

// BlockingQuestion returns a question only when the latest assistant message
// genuinely stops on it. Questions answered later in the same message and
// optional follow-up offers are completions, not operator gates.
func BlockingQuestion(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimRight(text, "\"'`*_)]}")
	if !strings.HasSuffix(text, "?") {
		return ""
	}
	end := len(text) - 1
	start := strings.LastIndexAny(text[:end], "\n.!?")
	question := strings.TrimSpace(text[start+1:])
	lower := strings.ToLower(question)
	for _, prefix := range []string{
		"would you like me to ",
		"do you want me to ",
		"if you'd like",
		"if you would like",
		"should i also ",
		"shall i also ",
		"want me to ",
		"let me know if ",
	} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	return question
}

// OperatorUtterances streams a session transcript and returns every genuine
// operator (human) utterance with attribution. It reuses the SAME streaming,
// entry parsing (parseTranscriptEntry), content extraction (textOfContent) and
// user-text cleaning (cleanTranscriptUserText) as scanSessionTranscript — it only
// adds per-utterance line/timestamp attribution and drops user-role entries that
// are not the operator speaking: tool results, slash-command echoes, local-command
// output, task notifications, and injected resume/interrupt markers. It is the
// one thin addition allowed by the harvest packet; it does not classify or judge.
func OperatorUtterances(path string) ([]OperatorUtterance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []OperatorUtterance
	reader := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNo++
			trimmed := strings.TrimSpace(string(line))
			if trimmed != "" {
				entry, parseErr := parseTranscriptEntry([]byte(trimmed))
				if parseErr != nil {
					return nil, parseErr
				}
				if u, ok := operatorUtteranceFromEntry(entry, lineNo); ok {
					out = append(out, u)
				}
			}
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			break
		}
		return nil, readErr
	}
	return out, nil
}

func operatorUtteranceFromEntry(entry transcriptEntry, lineNo int) (OperatorUtterance, bool) {
	role, content := transcriptRoleContent(entry)
	if role == "" {
		role = strings.TrimSpace(entry.Type)
	}
	if role != "user" {
		return OperatorUtterance{}, false
	}
	cleaned := cleanTranscriptUserText(textOfContent(content))
	if !isOperatorUtterance(cleaned) {
		return OperatorUtterance{}, false
	}
	return OperatorUtterance{
		Text:      cleaned,
		Timestamp: strings.TrimSpace(firstNonEmpty(entry.Timestamp, entry.TS)),
		LineNo:    lineNo,
	}, true
}

// operatorUtteranceRejectMarkers are substrings that mark a user-role entry as
// harness/tool machinery rather than the operator speaking. Kept conservative:
// the classifier is the real precision gate, this only drops obvious non-operator
// content so it never reaches the gate.
var operatorUtteranceRejectMarkers = []string{
	"<command-name>", "<command-message>", "<command-args>",
	"<local-command", "</local-command",
	"<task-notification>", "<task-id>",
	"[request interrupted",
	"messages below were generated by the user while running local commands",
	"resume the paused workflow by calling",
	"caveat: the messages below",
	// Tool/skill injections that arrive with role=user but are machinery, not the
	// operator speaking (found leaking through in the live 2026-07-11 dry-run).
	"base directory for this skill",
	"contents of the skill file",
	"here is useful information about the environment",
	"the following mcp servers",
}

func isOperatorUtterance(cleaned string) bool {
	if len([]rune(cleaned)) < 15 {
		return false
	}
	lower := strings.ToLower(cleaned)
	for _, marker := range operatorUtteranceRejectMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func scanSessionTranscript(path string) (sessionTranscriptSummary, error) {
	var out sessionTranscriptSummary
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()

	files := map[string]bool{}
	skills := map[string]bool{}
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(string(line))
			if trimmed != "" {
				for _, skill := range skillsFromLine(trimmed) {
					skills[skill] = true
				}
				entry, parseErr := parseTranscriptEntry([]byte(trimmed))
				if parseErr != nil {
					return sessionTranscriptSummary{}, parseErr
				}
				out.applyEntry(entry, files, skills)
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return sessionTranscriptSummary{}, err
	}
	out.FilesChanged = sortedKeys(files)
	out.SkillsFired = sortedKeys(skills)
	consumed, declared, err := detectTranscriptConsumption(path, nil)
	if err != nil {
		return sessionTranscriptSummary{}, err
	}
	out.ConsumedRefs = consumed
	out.UsedDeclared = declared
	return out, nil
}

func parseTranscriptEntry(line []byte) (transcriptEntry, error) {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return entry, err
	}
	return entry, nil
}

func (s *sessionTranscriptSummary) applyEntry(entry transcriptEntry, files, skills map[string]bool) {
	timestamp := strings.TrimSpace(firstNonEmpty(entry.Timestamp, entry.TS))
	if timestamp != "" {
		if s.FirstTimestamp == "" {
			s.FirstTimestamp = timestamp
		}
		s.LastTimestamp = timestamp
	}
	role, content := transcriptRoleContent(entry)
	if role == "" {
		role = strings.TrimSpace(entry.Type)
	}
	text := textOfContent(content)
	if role == "user" && s.FirstUser == "" {
		cleaned := cleanTranscriptUserText(text)
		if len(cleaned) > 10 {
			s.FirstUser = cleaned
		}
	}
	if role != "assistant" {
		return
	}
	if strings.TrimSpace(text) != "" {
		s.LastAssistant = strings.TrimSpace(text)
	}
	for _, block := range toolBlocks(content) {
		s.ToolCalls++
		if block.Name == "Bash" {
			s.BashCalls++
		}
		if block.Name == "Skill" {
			if skill := skillFromInput(block.Input); skill != "" {
				skills[skill] = true
			}
		}
		if mutatingTranscriptTools[block.Name] {
			if path := toolInputPath(block.Input); path != "" {
				files[path] = true
			}
		}
	}
}

func textOfContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []transcriptContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if (block.Type == "text" || block.Type == "input_text" || block.Type == "output_text") && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func transcriptRoleContent(entry transcriptEntry) (string, json.RawMessage) {
	role := strings.TrimSpace(entry.Role)
	content := entry.Content
	for _, nested := range []*transcriptMessage{entry.Message, entry.Payload} {
		if nested == nil {
			continue
		}
		role = firstNonEmpty(nested.Role, role)
		if len(nested.Content) > 0 {
			content = nested.Content
		}
	}
	return role, content
}

func toolBlocks(raw json.RawMessage) []transcriptContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []transcriptContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	out := make([]transcriptContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

func toolInputPath(input map[string]any) string {
	for _, key := range []string{"file_path", "path"} {
		if raw, ok := input[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(raw)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func skillFromInput(input map[string]any) string {
	for _, key := range []string{"skill", "command"} {
		if raw, ok := input[key]; ok {
			if ref := normalizeSkillRef(fmt.Sprint(raw)); ref != "" {
				return ref
			}
		}
	}
	return ""
}

func skillsFromLine(line string) []string {
	matches := skillPathPattern.FindAllStringSubmatch(line, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if ref := normalizeSkillRef(match[1]); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func normalizeSkillRef(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "skill:")
	if s == "" || s == "skills" || s == "name" || s == "example" {
		return ""
	}
	if strings.HasPrefix(s, "capability://skill:") {
		return s
	}
	return "capability://skill:" + s
}

func cleanTranscriptUserText(s string) string {
	s = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?s)<local-command-.*?>`).ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func renderSessionReceiptSummary(summary sessionTranscriptSummary, transcriptPath string) string {
	return strings.Join([]string{
		"task: " + clipTranscriptText(firstNonEmpty(summary.FirstUser, "(no plain-text user ask captured)"), 400),
		"outcome_hint: " + clipTranscriptText(firstNonEmpty(summary.LastAssistant, "(none)"), 700),
		fmt.Sprintf("tool_calls: %d (bash: %d)", summary.ToolCalls, summary.BashCalls),
		"window: " + firstNonEmpty(summary.FirstTimestamp, "?") + " -> " + firstNonEmpty(summary.LastTimestamp, "?"),
		"transcript: " + transcriptPath,
	}, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func clipTranscriptText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mergeUniqueStrings(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func transcriptInjectedClaimRefs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	seen := map[string]bool{}
	var refs []string
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			for _, ref := range claimRefPattern.FindAllString(string(line), -1) {
				if !seen[ref] {
					seen[ref] = true
					refs = append(refs, ref)
				}
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, err
	}
	return refs, nil
}

// matchConsumedClaimsFromFile is the production streaming path (bounded memory regardless of
// transcript size — matches scanSessionTranscript's own streaming discipline). It shares
// buildClaimMatchTargets/matchTargetHitsLine with matchConsumedClaims (consumption.go) so the
// streamed and whole-string entry points can never drift: v1 exact-id OR v2 content-key, per line.
func matchConsumedClaimsFromFile(path string, injected []injectedClaim) ([]string, error) {
	if len(injected) == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	targets := buildClaimMatchTargets(injected)
	consumedSet := map[string]bool{}
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			for i := range targets {
				if !consumedSet[targets[i].Ref] && matchTargetHitsLine(targets[i], line) {
					consumedSet[targets[i].Ref] = true
				}
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, err
	}
	consumed := make([]string, 0, len(consumedSet))
	for _, t := range targets {
		if consumedSet[t.Ref] {
			consumed = append(consumed, t.Ref)
		}
	}
	return consumed, nil
}

func receiptEventRef(rec *envelope.Receipt) string {
	return filepath.Join("events", rec.ID+".json")
}
