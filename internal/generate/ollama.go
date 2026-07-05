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

// complete runs a prompt through Ollama's native generate endpoint. A high
// temperature is used for varied, "advanced" randomization.
func (c *ollamaClient) complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	body, _ := json.Marshal(ollamaReq{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Options: map[string]any{
			"temperature": 1.1,
			"top_p":       0.95,
			"num_predict": maxTokens,
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

// listModels returns the models installed on the Ollama server (/api/tags).
func (c *ollamaClient) listModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/tags status %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}
