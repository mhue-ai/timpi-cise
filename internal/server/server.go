// Package server exposes the local dashboard: an embedded single-page UI plus a
// small JSON API to view metrics, start/stop polling, edit configuration, upload
// a CSV term list, and download the CSV results log. It binds to loopback by
// default so the dashboard stays on the local machine.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	run *runner.Runner
	met *metrics.Metrics
	log *slog.Logger
	srv *http.Server
}

// New builds a Server listening on the runner's configured address.
func New(run *runner.Runner, met *metrics.Metrics, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{run: run, met: met, log: log}
	mux := http.NewServeMux()

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/terms", s.handleTerms)
	mux.HandleFunc("/api/results.csv", s.handleResultsCSV)

	s.srv = &http.Server{
		Addr:              run.Config().Server.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
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
	data, err := readUpload(r)
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

func readUpload(r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxUploadBytes)
	if file, _, err := r.FormFile("file"); err == nil {
		defer file.Close()
		return io.ReadAll(file)
	}
	return io.ReadAll(r.Body)
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
