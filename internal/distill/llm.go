package distill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// llmClient is a minimal OpenAI-compatible chat client (provider set via env).
// Non-streaming: simpler than a streaming SSE path, which existed only to dodge a
// long-idle edge timeout some providers impose; a standard API is fine non-streamed.
type llmClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func newLLM(model string) (*llmClient, error) {
	key := os.Getenv("DISTILL_API_KEY")
	if key == "" {
		key = os.Getenv("DEEPSEEK_API_KEY")
	}
	if key == "" {
		return nil, fmt.Errorf("set DEEPSEEK_API_KEY (or DISTILL_API_KEY)")
	}
	base := os.Getenv("DISTILL_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com/v1"
	}
	return &llmClient{
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  key,
		model:   model,
		http:    &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

type usage struct {
	prompt, completion int
}

func (c *llmClient) call(ctx context.Context, system, user string) (string, usage, error) {
	body, _ := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	backoffs := []time.Duration{2 * time.Second, 6 * time.Second, 15 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", usage{}, err
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+c.apiKey)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 3 {
				time.Sleep(backoffs[attempt])
				continue
			}
			break
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			lastErr = fmt.Errorf("%s status %d: %s", c.model, resp.StatusCode, snip(data))
			if attempt < 3 {
				time.Sleep(backoffs[attempt])
				continue
			}
			break
		}
		if resp.StatusCode != 200 {
			return "", usage{}, fmt.Errorf("%s status %d: %s", c.model, resp.StatusCode, snip(data))
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", usage{}, err
		}
		if len(out.Choices) == 0 {
			return "", usage{}, fmt.Errorf("%s: no choices", c.model)
		}
		return out.Choices[0].Message.Content, usage{out.Usage.Prompt, out.Usage.Completion}, nil
	}
	return "", usage{}, lastErr
}

func snip(b []byte) string {
	s := string(b)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// LLMComplete is an exported one-shot completion (used by the reconcile pass).
// Defaults to a cheap chat model.
func LLMComplete(ctx context.Context, model, system, user string) (string, error) {
	if model == "" {
		model = "deepseek-chat"
	}
	c, err := newLLM(model)
	if err != nil {
		return "", err
	}
	out, _, err := c.call(ctx, system, user)
	return out, err
}
