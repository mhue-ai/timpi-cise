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
