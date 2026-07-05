package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ollamaClient talks to a local Ollama server (https://ollama.com) to generate
// advanced questions on the user's own GPU. It uses the /api/generate endpoint
// with streaming disabled. If the server is not running, calls simply error and
// the caller falls back to template generation.
type ollamaClient struct {
	baseURL string
	model   string
	hc      *http.Client
}

func newOllamaClient(baseURL, model string) *ollamaClient {
	return &ollamaClient{
		baseURL: baseURL,
		model:   model,
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

type ollamaReq struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

type ollamaResp struct {
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

// question asks the model for a single realistic search question about topic.
// A high temperature is used for varied, "advanced" randomization.
func (c *ollamaClient) question(ctx context.Context, topic string) (string, error) {
	prompt := fmt.Sprintf(
		"Generate ONE realistic, specific search-engine question a curious person "+
			"might type about \"%s\". Reply with only the question, no quotes, no preamble.",
		topic,
	)
	body, _ := json.Marshal(ollamaReq{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Options: map[string]any{
			"temperature": 1.1,
			"top_p":       0.95,
			"num_predict": 40,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	var out ollamaResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama: %s", out.Error)
	}
	return out.Response, nil
}

// Available reports whether the Ollama server responds to a version probe.
func (c *ollamaClient) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return false
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
