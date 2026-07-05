// Package server exposes the local dashboard: an embedded single-page UI plus a
// small JSON API to view metrics, start/stop polling, edit configuration, upload
// a CSV term list, and download the CSV results log. It binds to loopback by
// default so the dashboard stays on the local machine.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/runner"
)

//go:embed web
var webFS embed.FS

// maxUploadBytes caps the size of an uploaded CSV term list.
const maxUploadBytes = 8 << 20 // 8 MiB

// Server wires the HTTP handlers to the runner and metrics.
type Server struct {
	run      *runner.Runner
	met      *metrics.Metrics
	log      *slog.Logger
	version  string
	allowLAN bool // when false, requests with a non-local Host header are rejected
	srv      *http.Server
}

// New builds a Server listening on the runner's configured address. allowLAN
// relaxes the anti-DNS-rebinding Host check for intentional non-loopback binds.
func New(run *runner.Runner, met *metrics.Metrics, log *slog.Logger, version string, allowLAN bool) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{run: run, met: met, log: log, version: version, allowLAN: allowLAN}
	mux := http.NewServeMux()

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/terms", s.handleTerms)
	mux.HandleFunc("/api/results.csv", s.handleResultsCSV)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.handleMetrics)

	s.srv = &http.Server{
		Addr:              run.Config().Server.Addr,
		Handler:           s.guard(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// guard defends the local dashboard against DNS-rebinding and cross-site
// request forgery: it rejects requests whose Host header is not local (unless
// LAN access was explicitly enabled) and cross-origin state-changing requests.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowLAN && !hostIsLocal(r.Host) {
			s.log.Warn("rejected non-local Host header", "host", r.Host, "path", r.URL.Path)
			http.Error(w, "forbidden: non-local Host header (DNS-rebinding guard)", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			if o := r.Header.Get("Origin"); o != "" && !originMatchesHost(o, r.Host) {
				s.log.Warn("rejected cross-origin request", "origin", o, "host", r.Host, "path", r.URL.Path)
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// hostIsLocal reports whether a Host header refers to the loopback interface.
func hostIsLocal(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]") // strip IPv6 brackets
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// originMatchesHost reports whether a request Origin is exactly same-origin with
// the request Host. A loopback fallback is deliberately NOT allowed: a page
// served by a *different* local port (e.g. http://127.0.0.1:9999) must not be
// able to forge state-changing requests to the dashboard.
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == host
}

// Addr returns the listen address.
func (s *Server) Addr() string { return s.srv.Addr }

// Start begins serving (blocking). Returns when the server is shut down.
func (s *Server) Start() error {
	err := s.srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The default logger is configured at startup via slog.SetDefault.
		slog.Debug("failed to encode JSON response", "err", err)
	}
}

type statusResp struct {
	Adapter        string           `json:"adapter"`
	Mode           string           `json:"mode"`
	Source         string           `json:"source"`
	CSVCount       int              `json:"csv_count"`
	CSVError       string           `json:"csv_error,omitempty"`
	ResultsCSVPath string           `json:"results_csv_path"`
	LogDir         string           `json:"log_dir"`
	Metrics        metrics.Snapshot `json:"metrics"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := s.run.Config()
	count, errMsg := s.run.CSVInfo()
	writeJSON(w, http.StatusOK, statusResp{
		Adapter:        s.run.AdapterName(),
		Mode:           cfg.Mode,
		Source:         cfg.Generation.Source,
		CSVCount:       count,
		CSVError:       errMsg,
		ResultsCSVPath: s.run.ResultsCSVPath(),
		LogDir:         cfg.Logging.Dir,
		Metrics:        s.met.Snapshot(),
	})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if err := s.run.Start(); err != nil {
		s.log.Warn("start rejected", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	s.run.Stop()
	writeJSON(w, http.StatusOK, map[string]bool{"running": false})
}

// configView is the config as returned to the UI, with secrets redacted.
type configView struct {
	config.Config
	APIKeySet bool `json:"api_key_set"`
	LLMKeySet bool `json:"llm_key_set"`
}

func redact(c config.Config) configView {
	v := configView{
		Config:    c,
		APIKeySet: c.API.Key != "",
		LLMKeySet: c.Generation.LLM.APIKey != "",
	}
	v.Config.API.Key = ""
	v.Config.Generation.LLM.APIKey = ""
	return v
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, redact(s.run.Config()))
	case http.MethodPost:
		var incoming config.Config
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			s.log.Warn("config update: invalid JSON", "err", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		// Preserve existing secrets when the UI submits empty ones.
		cur := s.run.Config()
		if incoming.API.Key == "" {
			incoming.API.Key = cur.API.Key
		}
		if incoming.Generation.LLM.APIKey == "" {
			incoming.Generation.LLM.APIKey = cur.Generation.LLM.APIKey
		}
		// The CSV path is managed via the upload endpoint; keep the current one
		// if the UI didn't send a new one.
		if incoming.Generation.CSVPath == "" {
			incoming.Generation.CSVPath = cur.Generation.CSVPath
		}
		if err := s.run.UpdateConfig(incoming); err != nil {
			s.log.Warn("config update rejected", "err", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, redact(s.run.Config()))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
	}
}

// handleTerms accepts an uploaded CSV/line list, saves it under the log dir, and
// switches the generator to the CSV source. The body may be raw text/csv or a
// multipart form with a "file" field.
func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	data, err := readUpload(w, r)
	if err != nil {
		s.log.Warn("terms upload: read failed", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(data) == 0 {
		s.log.Warn("terms upload: empty body")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty upload"})
		return
	}

	cfg := s.run.Config()
	dest := filepath.Join(cfg.Logging.Dir, "terms.csv")
	if err := os.MkdirAll(cfg.Logging.Dir, 0o755); err != nil {
		s.log.Error("terms upload: mkdir failed", "dir", cfg.Logging.Dir, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		s.log.Error("terms upload: write failed", "path", dest, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg.Generation.Source = config.SourceCSV
	cfg.Generation.CSVPath = dest
	if err := s.run.UpdateConfig(cfg); err != nil {
		// Saved the file, but it didn't parse into usable queries.
		s.log.Warn("terms upload: file saved but not usable", "path", dest, "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	count, errMsg := s.run.CSVInfo()
	s.log.Info("terms uploaded", "path", dest, "count", count)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dest,
		"csv_count": count,
		"csv_error": errMsg,
	})
}

func readUpload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if file, _, err := r.FormFile("file"); err == nil {
		defer file.Close()
		return io.ReadAll(file)
	}
	return io.ReadAll(r.Body)
}

// handleHealthz reports basic liveness for supervisors and uptime checks.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	snap := s.met.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        s.version,
		"uptime_seconds": snap.UptimeSeconds,
		"running":        snap.Running,
	})
}

// handleMetrics exposes counters in Prometheus text exposition format so the
// tool can be scraped into Grafana/alerting.
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	snap := s.met.Snapshot()
	running := 0
	if snap.Running {
		running = 1
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder
	metric := func(name, typ, help string, value any) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s %v\n", name, help, name, typ, name, value)
	}
	metric("timpicise_up", "gauge", "1 if the process is serving.", 1)
	metric("timpicise_running", "gauge", "1 if the polling loop is active.", running)
	metric("timpicise_uptime_seconds", "gauge", "Process uptime in seconds.", snap.UptimeSeconds)
	metric("timpicise_queries_total", "counter", "Total queries executed.", snap.Sent)
	metric("timpicise_queries_ok_total", "counter", "Queries that succeeded.", snap.OK)
	metric("timpicise_queries_failed_total", "counter", "Queries that failed.", snap.Failed)
	metric("timpicise_zero_results_total", "counter", "Successful queries returning zero results.", snap.ZeroResults)
	metric("timpicise_assert_failures_total", "counter", "Assertion failures.", snap.AssertFail)
	metric("timpicise_latency_ms_avg", "gauge", "Average query latency (ms).", snap.AvgLatencyMS)
	metric("timpicise_latency_ms_p50", "gauge", "p50 query latency (ms).", snap.P50MS)
	metric("timpicise_latency_ms_p95", "gauge", "p95 query latency (ms).", snap.P95MS)
	metric("timpicise_latency_ms_p99", "gauge", "p99 query latency (ms).", snap.P99MS)
	if _, err := w.Write([]byte(b.String())); err != nil {
		s.log.Debug("metrics write failed", "err", err)
	}
}

// handleResultsCSV streams the current CSV results log for download.
func (s *Server) handleResultsCSV(w http.ResponseWriter, r *http.Request) {
	path := s.run.ResultsCSVPath()
	if path == "" {
		path = s.run.Config().ResultsCSVPath()
	}
	f, err := os.Open(path)
	if err != nil {
		s.log.Warn("results CSV download: not available", "path", path, "err", err)
		http.Error(w, "no results log yet", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="timpi-cise-results.csv"`)
	if _, err := io.Copy(w, f); err != nil {
		s.log.Warn("results CSV download: copy failed", "err", err)
	}
}
