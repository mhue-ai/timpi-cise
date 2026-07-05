package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openaiClient talks to any OpenAI-compatible chat-completions endpoint. This
// covers most local model servers — LM Studio, llama.cpp's server, Jan,
// LocalAI, vLLM, text-generation-webui — as well as hosted OpenAI-style APIs.
// The BaseURL is expected to include the version segment, e.g.
// "http://localhost:1234/v1".
type openaiClient struct {
	baseURL string
	model   string
	apiKey  string
	hc      *http.Client
}

func newOpenAIClient(baseURL, model, apiKey string) *openaiClient {
	return &openaiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 25 * time.Second},
	}
}

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaReq struct {
	Model       string      `json:"model"`
	Messages    []oaMessage `json:"messages"`
	Temperature float64     `json:"temperature"`
	TopP        float64     `json:"top_p"`
	MaxTokens   int         `json:"max_tokens"`
	Stream      bool        `json:"stream"`
}

type oaResp struct {
	Choices []struct {
		Message oaMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *openaiClient) complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	body, _ := json.Marshal(oaReq{
		Model: c.model,
		Messages: []oaMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 1.1,
		TopP:        0.95,
		MaxTokens:   maxTokens,
		Stream:      false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai-compatible server status %d", resp.StatusCode)
	}
	var out oaResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("llm: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

// listModels returns the models the server advertises (GET {base}/models, the
// OpenAI list-models format used by LM Studio, llama.cpp, vLLM, etc.).
// errorMessage extracts a human message from an OpenAI-style error field, which
// may be absent, a plain string (LM Studio), or an object {"message": ...}.
func errorMessage(v any) string {
	switch e := v.(type) {
	case string:
		return e
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			return m
		}
		return "server returned an error"
	default:
		return ""
	}
}

func (c *openaiClient) listModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/models status %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Error any `json:"error"` // may be a string (LM Studio) or an object (OpenAI)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	// A wrong path (e.g. missing /v1) often returns 200 + an error body. Surface
	// it instead of silently reporting zero models.
	if msg := errorMessage(out.Error); msg != "" {
		return nil, fmt.Errorf("%s (check the base URL — LM Studio needs .../v1)", msg)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}
