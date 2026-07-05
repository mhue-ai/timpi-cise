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

func TestListModelsUnreachable(t *testing.T) {
	// Nothing is listening on this port; should error, not panic.
	if _, err := ListModels(context.Background(), config.LLMOllama, "http://127.0.0.1:1", ""); err == nil {
		t.Error("expected an error for an unreachable server")
	}
}
