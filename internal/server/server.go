// Package server exposes the local dashboard: an embedded single-page UI plus a
// small JSON API to view metrics, start/stop polling, and edit configuration.
// It binds to loopback by default so the dashboard stays on the local machine.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/runner"
)

//go:embed web
var webFS embed.FS

// Server wires the HTTP handlers to the runner and metrics.
type Server struct {
	run *runner.Runner
	met *metrics.Metrics
	srv *http.Server
}

// New builds a Server listening on the runner's configured address.
func New(run *runner.Runner, met *metrics.Metrics) *Server {
	s := &Server{run: run, met: met}
	mux := http.NewServeMux()

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/config", s.handleConfig)

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
	_ = json.NewEncoder(w).Encode(v)
}

type statusResp struct {
	Adapter string           `json:"adapter"`
	Mode    string           `json:"mode"`
	Metrics metrics.Snapshot `json:"metrics"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusResp{
		Adapter: s.run.AdapterName(),
		Mode:    s.run.Config().Mode,
		Metrics: s.met.Snapshot(),
	})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if err := s.run.Start(); err != nil {
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

// configView is the config as returned to the UI, with the API key redacted.
type configView struct {
	config.Config
	APIKeySet bool `json:"api_key_set"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c := s.run.Config()
		hadKey := c.API.Key != ""
		c.API.Key = "" // never expose the key to the browser
		writeJSON(w, http.StatusOK, configView{Config: c, APIKeySet: hadKey})
	case http.MethodPost:
		var incoming config.Config
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		// Preserve the existing key if the UI submitted an empty one.
		cur := s.run.Config()
		if incoming.API.Key == "" {
			incoming.API.Key = cur.API.Key
		}
		if err := s.run.UpdateConfig(incoming); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		c := s.run.Config()
		hadKey := c.API.Key != ""
		c.API.Key = ""
		writeJSON(w, http.StatusOK, configView{Config: c, APIKeySet: hadKey})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
	}
}
