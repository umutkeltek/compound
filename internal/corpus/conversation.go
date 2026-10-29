package corpus

import (
	"bufio"
	"compound/internal/sourceobs"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// conversationText is a derived retrieval view, never a replacement for custody.
// Keep speaker/time attribution; exclude reasoning, system prompts and tool payloads.
func conversationText(format, body string) (string, error) {
	return conversationTextContext(context.Background(), format, body)
}
func conversationTextContext(ctx context.Context, format, body string) (string, error) {
	if format != "codex" && format != "claude" {
		return "", fmt.Errorf("unknown transcript format")
	}
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1<<30)
	line := 0
	lastUser := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var row struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return "", fmt.Errorf("invalid transcript record %d", line)
		}
		var message struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		var encoded json.RawMessage
		if format == "codex" && row.Type == "response_item" {
			encoded = row.Payload
		}
		if format == "claude" && (row.Type == "user" || row.Type == "assistant") {
			encoded = row.Message
		}
		if format == "codex" && row.Type == "event_msg" {
			var event struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if json.Unmarshal(row.Payload, &event) == nil && event.Type == "user_message" {
				message.Role = "user"
				message.Content, _ = json.Marshal(event.Message)
				encoded, _ = json.Marshal(message)
			}
		}
		if len(encoded) == 0 {
			continue
		}
		if err := json.Unmarshal(encoded, &message); err != nil {
			return "", fmt.Errorf("invalid message record %d", line)
		}
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if len(message.Content) == 0 || string(message.Content) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(message.Content, &text) != nil {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(message.Content, &blocks); err != nil {
				return "", fmt.Errorf("invalid content record %d", line)
			}
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" || b.Type == "input_text" || b.Type == "output_text" {
					parts = append(parts, b.Text)
				}
			}
			text = strings.Join(parts, "\n")
		}
		if message.Role == "user" {
			text = sourceobs.CleanTranscriptUserText(text)
			if sourceobs.IsTranscriptMachineText(text) {
				continue
			}
			if format == "codex" && text == lastUser {
				continue
			}
			lastUser = text
		} else {
			lastUser = ""
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		fmt.Fprintf(&out, "[%s record %d] %s: %s\n\n", row.Timestamp, line, message.Role, text)
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("transcript record exceeds supported size or cannot be read")
	}
	return out.String(), nil
}
