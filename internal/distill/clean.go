// Package distill ports the proven reasoning distiller: clean a whole conversation,
// worth-gate it, map-reduce over a reasoning model, and extract CANDIDATE claims.
// It reads the WHOLE conversation (not first+last) — the differentiator.
package distill

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

type Role string

const (
	RoleUser  Role = "USER"
	RoleAgent Role = "AGENT"
)

type Turn struct {
	Role Role
	Text string
}

type CleanResult struct {
	Turns           []Turn
	RawBytes        int64
	UserKept        int
	AgentKept       int
	HarnessDropped  int
	ToolDropped     int
	ThinkingDropped int
}

var (
	reSysReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	reWS          = regexp.MustCompile(`\s+`)
	reProxyNoise  = regexp.MustCompile(`^■ unexpected status \d+|compression_refused|Payload Too Large`)
)

func cleanText(s string) string {
	s = reSysReminder.ReplaceAllString(s, "")
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

var harnessMarkers = []string{
	"<system-reminder>", "<INSTRUCTIONS>", "<command-name>", "<command-message>",
	"<local-command-stdout>", "Caveat:", "This session is being continued from a previous",
	"[Request interrupted",
}

func isHarnessUser(t string) bool {
	for _, m := range harnessMarkers {
		if strings.Contains(t, m) {
			return true
		}
	}
	return reProxyNoise.MatchString(t)
}

type rawEvent struct {
	Type          string          `json:"type"`
	Message       json.RawMessage `json:"message"`
	Payload       json.RawMessage `json:"payload"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// extractText pulls visible text out of a content field (string or []block) and
// counts dropped thinking/tool blocks. Tool results are signalled separately.
func extractText(content json.RawMessage) (text string, hasToolResult bool, thinking, toolUse int) {
	var blocks []block
	if err := json.Unmarshal(content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			switch b.Type {
			case "text", "input_text", "output_text":
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			case "thinking":
				thinking++
			case "tool_use":
				toolUse++
			case "tool_result":
				hasToolResult = true
			}
		}
		return strings.Join(parts, "\n"), hasToolResult, thinking, toolUse
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s, false, 0, 0
	}
	return "", false, 0, 0
}

// normalize folds Claude and Codex schemas into (role, content); drop=true means
// skip this event (tool noise, reasoning, harness/developer injection).
func normalize(ev *rawEvent, res *CleanResult) (role string, content json.RawMessage, drop bool) {
	if len(ev.Message) > 0 { // Claude shape
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(ev.Message, &m) == nil {
			if len(ev.ToolUseResult) > 0 {
				res.ToolDropped++
				return "", nil, true
			}
			return m.Role, m.Content, false
		}
	}
	if ev.Type == "response_item" && len(ev.Payload) > 0 { // Codex shape
		var p struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			switch p.Type {
			case "message":
				if p.Role == "developer" {
					res.HarnessDropped++
					return "", nil, true
				}
				return p.Role, p.Content, false
			case "reasoning":
				res.ThinkingDropped++
			case "function_call", "custom_tool_call", "web_search_call",
				"function_call_output", "custom_tool_call_output":
				res.ToolDropped++
			}
		}
	}
	return "", nil, true
}

// Clean streams a session JSONL into role-tagged user/agent turns, dropping tool,
// reasoning, and harness noise.
func Clean(path string) (*CleanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	res := &CleanResult{RawBytes: st.Size()}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev rawEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		role, content, drop := normalize(&ev, res)
		if drop || content == nil {
			continue
		}
		text, hasTool, think, tool := extractText(content)
		res.ThinkingDropped += think
		res.ToolDropped += tool
		if hasTool {
			res.ToolDropped++
			continue
		}
		cleaned := cleanText(text)
		if cleaned == "" {
			continue
		}
		switch role {
		case "user":
			if isHarnessUser(cleaned) {
				res.HarnessDropped++
				continue
			}
			res.Turns = append(res.Turns, Turn{RoleUser, cleaned})
			res.UserKept++
		case "assistant":
			res.Turns = append(res.Turns, Turn{RoleAgent, cleaned})
			res.AgentKept++
		}
	}
	return res, sc.Err()
}

// Render produces the role-tagged transcript fed to the model.
func Render(turns []Turn) string {
	var b strings.Builder
	for i, t := range turns {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[")
		b.WriteString(string(t.Role))
		b.WriteString("] ")
		b.WriteString(t.Text)
	}
	return b.String()
}
