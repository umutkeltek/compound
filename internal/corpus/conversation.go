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
	if format != "codex" && format != "claude" && format != "omp" && format != "opencode" && format != "hermes" && format != "codex-history" {
		return "", fmt.Errorf("unknown transcript format")
	}
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1<<30)
	line := 0
	lastUser := ""
	semanticOrdinal := 0
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
			Timestamp json.RawMessage `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
			Payload   json.RawMessage `json:"payload"`
			Context   json.RawMessage `json:"context"`
			Source    json.RawMessage `json:"source"`
		}
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return "", fmt.Errorf("invalid transcript record %d", line)
		}
		if (format == "opencode" || format == "hermes" || format == "codex-history") && row.Type == "native_session" {
			var contextFields map[string]json.RawMessage
			if len(row.Context) > 0 && string(row.Context) != "null" {
				if err := json.Unmarshal(row.Context, &contextFields); err != nil {
					return "", fmt.Errorf("invalid native session context record %d", line)
				}
			}
			var contextParts []string
			for _, field := range []string{"title", "cwd", "model", "source", "parent_session_id"} {
				var value string
				_ = json.Unmarshal(contextFields[field], &value)
				if value != "" {
					contextParts = append(contextParts, field+": "+value)
				}
			}
			if len(contextParts) > 0 {
				fmt.Fprintf(&out, "Session context: %s\n\n", strings.Join(contextParts, "; "))
			}
			continue
		}
		var message struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		var encoded json.RawMessage
		if (format == "omp" || format == "opencode" || format == "hermes" || format == "codex-history") && row.Type == "message" {
			encoded = row.Message
		}
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
		timestamp, err := conversationTimestamp(row.Timestamp)
		if err != nil {
			return "", fmt.Errorf("invalid message timestamp record %d", line)
		}
		semanticOrdinal++
		if format == "opencode" || format == "hermes" || format == "codex-history" {
			var source struct {
				Table     string `json:"table"`
				MessageID string `json:"message_id"`
			}
			if len(row.Source) > 0 && string(row.Source) != "null" {
				if err := json.Unmarshal(row.Source, &source); err != nil {
					return "", fmt.Errorf("invalid native message source record %d", line)
				}
			}
			attribution := fmt.Sprintf("message %d", semanticOrdinal)
			if source.Table != "" && source.MessageID != "" {
				attribution = source.Table + ":" + source.MessageID
			}
			fmt.Fprintf(&out, "[%s %s] %s: %s\n\n", timestamp, attribution, message.Role, text)
		} else {
			fmt.Fprintf(&out, "[%s record %d] %s: %s\n\n", timestamp, line, message.Role, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("transcript record exceeds supported size or cannot be read")
	}
	return out.String(), nil
}

// Metadata belongs to its native record family. A foreign context/source shape
// must not make an otherwise valid visible message unreadable.
func conversationTimestamp(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String(), nil
	}
	return "", fmt.Errorf("timestamp must be a string or number")
}
