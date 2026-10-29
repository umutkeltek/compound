package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/corpus"
)

// This uses the actual setup helper and live hook adapters. No source/index
// command is called between writing a turn and asking the fresh CLI process.
func TestMemoryRecallFreshSetupAutomaticCaptureAcrossHarnesses(t *testing.T) {
	if testing.Short() {
		t.Skip("native subprocess lifecycle proof")
	}
	binary := compoundTestBinary(t)
	hp, root := t.TempDir(), t.TempDir()
	env := append(os.Environ(), "COMPOUND_HOME="+hp)
	run := func(input []byte, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = env
		cmd.Stdin = bytes.NewReader(input)
		body, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, body)
		}
		return body
	}
	run(nil, "home", "init")
	for _, harness := range []string{"codex", "claude-code"} {
		var pack struct {
			Summary string `json:"summary_md"`
		}
		body := run(nil, "orient", "--harness", harness, "--cwd", root, "--task", "Recall our prior archive decision")
		if err := json.Unmarshal(body, &pack); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pack.Summary, "compound memory search") || !strings.Contains(pack.Summary, "operator-authorized scope") {
			t.Fatalf("%s lacks the scoped recall door: %s", harness, pack.Summary)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var texts []string
		if json.Unmarshal(req.Input, &texts) != nil {
			var s string
			_ = json.Unmarshal(req.Input, &s)
			texts = []string{s}
		}
		rows := make([]map[string]any, 0, len(texts))
		for i, text := range texts {
			v := []float64{0, 0, 1}
			if strings.Contains(strings.ToLower(text), "archive") {
				v = []float64{1, 0, 0}
			}
			rows = append(rows, map[string]any{"index": i, "embedding": v})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
	}))
	defer server.Close()
	lane := corpus.LaneConfig{Lane: "dialogue", SourceFamily: "dialogue_fixture", Privacy: "private",
		Embedder: corpus.EmbedderConfig{Provider: "ollama", Model: "fixture", Dims: 3, BaseURL: server.URL},
		Source:   &corpus.SourceConfig{Root: root, Format: "codex"}}
	config := filepath.Join(t.TempDir(), "sources.json")
	raw, _ := json.Marshal(map[string]any{"corpus_lanes": []corpus.LaneConfig{lane}})
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	setup := exec.Command("python3", filepath.Join(repo, "scripts", "memory-service.py"), "setup",
		"--binary", binary, "--home", hp, "--workspace", root, "--config", config)
	setup.Env = env
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	sum := sha256.Sum256([]byte(hp))
	id := "work_memory_refresh_" + hex.EncodeToString(sum[:])[:16]
	transcript := filepath.Join(root, "session.jsonl")
	writeTurn := func(text string) {
		t.Helper()
		row := map[string]any{"type": "event_msg", "payload": map[string]string{"type": "user_message", "message": text}}
		body, _ := json.Marshal(row)
		body = append(body, '\n')
		if err := os.WriteFile(transcript, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(items int) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			var c corpus.Coverage
			b, _ := os.ReadFile(filepath.Join(hp, "indexes", "corpus-dialogue", "refresh.json"))
			_ = json.Unmarshal(b, &c)
			var work struct {
				State string `json:"state"`
			}
			wb, _ := os.ReadFile(filepath.Join(hp, "work", "work", id+".json"))
			_ = json.Unmarshal(wb, &work)
			if c.Status == "ok" && c.Items == items && work.State == "verifying" {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		b, _ := os.ReadFile(filepath.Join(hp, "indexes", "corpus-dialogue", "refresh.json"))
		t.Fatalf("automatic capture did not complete: %s", b)
	}
	question := func(extra ...string) corpus.RecallReport {
		t.Helper()
		args := []string{"memory", "search", "What did we decide for the archive?", "--json"}
		args = append(args, extra...)
		var report corpus.RecallReport
		if err := json.Unmarshal(run(nil, args...), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	writeTurn("We selected Cedar for the archive.")
	input, _ := json.Marshal(map[string]any{"cwd": root, "session_id": "fixture_session", "tool_name": "Read", "tool_response": "fixture"})
	run(input, "hook", "post-tool-use", "--live", "--harness", "codex")
	wait(1)
	hidden := question()
	if len(hidden.Evidence) != 1 || hidden.Evidence[0].Text != "" {
		t.Fatalf("private default: %+v", hidden)
	}
	first := question("--include-private-text")
	if len(first.Evidence) != 1 || !strings.Contains(first.Evidence[0].Text, "Cedar") {
		t.Fatalf("first: %+v", first)
	}
	writeTurn("Correction: Birch replaces Cedar for the archive.")
	input, _ = json.Marshal(map[string]any{"cwd": root, "session_id": "fixture_session", "transcript_path": transcript})
	run(input, "hook", "session-end", "--live", "--harness", "claude_code")
	wait(2)
	latest := question("--include-private-text")
	if len(latest.Evidence) != 1 || !strings.Contains(latest.Evidence[0].Text, "Birch") {
		t.Fatalf("correction: %+v", latest)
	}
	history := question("--include-private-text", "--history")
	if len(history.Evidence) != 2 {
		t.Fatalf("history: %+v", history)
	}
	t.Log(fmt.Sprintf("fresh setup -> Codex PostToolUse -> owned capture -> fresh CLI -> Claude SessionEnd correction -> fresh CLI/history; work=%s", id))
}

func TestPathlessMemorySearchRefusesExternalStoreFallback(t *testing.T) {
	cmd := exec.Command(compoundTestBinary(t), "memory", "search", "prior archive decision", "--json")
	cmd.Env = parityTestEnv(os.Environ(), map[string]string{
		"COMPOUND_HOME":  t.TempDir(),
		"COMPOUND_STORE": "postgre",
		"COMPOUND_DSN":   "",
	})
	body, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(body), "unsupported COMPOUND_STORE") {
		t.Fatalf("file-backed recall under unsupported store: %v %s", err, body)
	}
}
