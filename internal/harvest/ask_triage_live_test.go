package harvest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStopBlockingAskUsesCurrentPayloadOverLaggingTranscript(t *testing.T) {
	transcript := writeAskTriageTranscript(t,
		"The operator asked for the implementation to be inspected.",
		"Should I inspect the existing test first?",
	)

	ask, err := stopBlockingAsk(CodexStopInput{
		TranscriptPath:       transcript,
		LastAssistantMessage: "I inspected the existing test; it passes.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ask.Text != "" {
		t.Fatalf("current completion retained a lagging transcript ask: %+v", ask)
	}

	current := "May I continue with the reversible edit inside the envelope?"
	ask, err = stopBlockingAsk(CodexStopInput{
		TranscriptPath:       transcript,
		LastAssistantMessage: current,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ask.Text != current || ask.LineNo != 0 {
		t.Fatalf("current payload question did not replace lagging transcript: %+v", ask)
	}
}

func writeAskTriageTranscript(t *testing.T, userText, assistantText string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	entries := []map[string]any{
		{"timestamp": "2026-07-11T09:14:00Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": userText}}}},
		{"timestamp": "2026-07-11T09:15:00Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": assistantText}}}},
	}
	var body []byte
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, line...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
