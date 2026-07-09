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
	role := strings.TrimSpace(entry.Role)
	content := entry.Content
	if entry.Message != nil {
		role = firstNonEmpty(entry.Message.Role, role)
		if len(entry.Message.Content) > 0 {
			content = entry.Message.Content
		}
	}
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
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
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

func matchConsumedClaimsFromFile(path string, injectedRefs []string) ([]string, error) {
	if len(injectedRefs) == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	consumedSet := map[string]bool{}
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			for _, ref := range injectedRefs {
				id := strings.TrimPrefix(ref, claimScheme)
				if id != "" && !consumedSet[ref] && bareClaimReference(line, id) {
					consumedSet[ref] = true
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
	for _, ref := range injectedRefs {
		if consumedSet[ref] {
			consumed = append(consumed, ref)
		}
	}
	return consumed, nil
}

func receiptEventRef(rec *envelope.Receipt) string {
	return filepath.Join("events", rec.ID+".json")
}
