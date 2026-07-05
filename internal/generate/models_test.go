package generate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

func TestListModelsOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("ollama path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"},{"name":"qwen2.5:7b"}]}`))
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), config.LLMOllama, srv.URL, "")
	if err != nil {
		t.Fatalf("ollama list: %v", err)
	}
	if len(models) != 2 || models[0] != "llama3.2:latest" || models[1] != "qwen2.5:7b" {
		t.Errorf("models = %v", models)
	}
}

func TestListModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("openai path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer auth: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"local-model"}]}`))
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), config.LLMOpenAI, srv.URL+"/v1", "k")
	if err != nil {
		t.Fatalf("openai list: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-4o" || models[1] != "local-model" {
		t.Errorf("models = %v", models)
	}
}

func TestListModelsSurfacesServerError(t *testing.T) {
	// Servers like LM Studio answer a wrong path with HTTP 200 + an error body.
	// Those must surface as an error, not a silent empty list.
	cases := []struct {
		name, provider, body string
	}{
		{"ollama-wrong-path", config.LLMOllama, `{"error":"Unexpected endpoint or method. (GET /api/tags)"}`},
		{"openai-string-error", config.LLMOpenAI, `{"error":"Unexpected endpoint or method. (GET /models)"}`},
		{"openai-object-error", config.LLMOpenAI, `{"error":{"message":"invalid api key"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			base := srv.URL
			if tc.provider == config.LLMOpenAI {
				base = srv.URL + "/v1"
			}
			if _, err := ListModels(context.Background(), tc.provider, base, ""); err == nil {
				t.Errorf("%s: expected an error, got nil", tc.name)
			}
		})
	}
}

func TestListModelsUnreachable(t *testing.T) {
	// Nothing is listening on this port; should error, not panic.
	if _, err := ListModels(context.Background(), config.LLMOllama, "http://127.0.0.1:1", ""); err == nil {
		t.Error("expected an error for an unreachable server")
	}
}
