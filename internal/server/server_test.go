package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/runner"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Logging.CSVResults = false
	cfg.Logging.AppLog = false
	cfg.Logging.PersistMetrics = false
	cfg.Logging.Dir = t.TempDir()
	met := metrics.New(10)
	run := runner.New(cfg, "", met, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(run.Close)
	return New(run, met, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-1.0", false)
}

func do(s *Server, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:8770"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	return rec
}

func TestHealthzAndMetrics(t *testing.T) {
	s := testServer(t)

	rec := do(s, "GET", "http://127.0.0.1:8770/healthz", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"version":"test-1.0"`) {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(s, "GET", "http://127.0.0.1:8770/metrics", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "timpicise_queries_total") {
		t.Errorf("metrics: %d %s", rec.Code, rec.Body.String())
	}
}

func TestStatusAlertsAlwaysArray(t *testing.T) {
	s := testServer(t)
	rec := do(s, "GET", "http://127.0.0.1:8770/api/status", nil)
	if !strings.Contains(rec.Body.String(), `"alerts":[]`) {
		t.Errorf("alerts should serialize as [] not null: %s", rec.Body.String())
	}
}

func TestGuardRejectsNonLocalHost(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("GET", "http://evil.com/api/status", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-local Host should be 403, got %d", rec.Code)
	}
}

func TestGuardRejectsCrossOriginPost(t *testing.T) {
	s := testServer(t)
	rec := do(s, "POST", "http://127.0.0.1:8770/api/stop", map[string]string{"Origin": "https://evil.com"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST should be 403, got %d", rec.Code)
	}
}

func TestGuardAllowsSameOriginPost(t *testing.T) {
	s := testServer(t)
	rec := do(s, "POST", "http://127.0.0.1:8770/api/stop", map[string]string{"Origin": "http://127.0.0.1:8770"})
	if rec.Code == http.StatusForbidden {
		t.Errorf("same-origin POST should be allowed, got 403")
	}
}

func TestConfigRedactsSecrets(t *testing.T) {
	s := testServer(t)
	rec := do(s, "GET", "http://127.0.0.1:8770/api/config", nil)
	body := rec.Body.String()
	if strings.Contains(body, `"key":"`) && !strings.Contains(body, `"key":""`) {
		t.Errorf("API key should be redacted: %s", body)
	}
}
