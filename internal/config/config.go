// Package config defines the runtime configuration for timpi-cise and handles
// loading/saving it as JSON. It also enforces the hard safety invariants of the
// tool (notably the minimum polling interval) so they cannot be bypassed via a
// hand-edited config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MinPollSeconds is the absolute floor for the polling interval. This is a hard,
// compiled-in safety limit: the tool will never issue more than one query per
// minute, regardless of what the config file or UI asks for. It exists so that
// exercising the Timpi interface stays lighter than a single slow human user and
// cannot be turned into an abusive traffic generator.
const MinPollSeconds = 60

// Connection modes.
const (
	ModeDryRun     = "dry-run"     // generate queries, never touch the network (default)
	ModePublicWeb  = "public-web"  // exercise the public timpi.com search interface
	ModeOfficialAPI = "official-api" // use an authenticated Timpi Data API endpoint
)

// Generation modes.
const (
	GenTerms     = "terms"     // short generic word terms
	GenPhrases   = "phrases"   // multi-word phrases
	GenQuestions = "questions" // natural-language questions
	GenMixed     = "mixed"     // a rotating mix of the above
)

// Config is the full application configuration.
type Config struct {
	// Mode selects how queries are executed. One of ModeDryRun, ModePublicWeb,
	// ModeOfficialAPI.
	Mode string `json:"mode"`

	// PollSeconds is the delay between queries. Clamped to >= MinPollSeconds.
	PollSeconds int `json:"poll_seconds"`

	// JitterSeconds adds up to this many seconds of random extra delay between
	// queries so the traffic pattern is not perfectly periodic.
	JitterSeconds int `json:"jitter_seconds"`

	// UserAgent identifies the tool honestly in requests. Kept configurable but
	// defaults to an identifying string including a contact hint.
	UserAgent string `json:"user_agent"`

	// Generation controls how search queries are produced.
	Generation Generation `json:"generation"`

	// PublicWeb configures the public timpi.com adapter.
	PublicWeb PublicWeb `json:"public_web"`

	// API configures the authenticated Data API adapter.
	API API `json:"api"`

	// Server controls the local dashboard.
	Server Server `json:"server"`
}

// Generation controls query generation.
type Generation struct {
	// Mode is one of GenTerms, GenPhrases, GenQuestions, GenMixed.
	Mode string `json:"mode"`

	// UseOllama enables optional local-GPU question generation via an Ollama
	// server. If Ollama is unreachable the tool silently falls back to the
	// built-in generators.
	UseOllama bool `json:"use_ollama"`

	// OllamaURL is the base URL of a local Ollama server (default
	// http://localhost:11434).
	OllamaURL string `json:"ollama_url"`

	// OllamaModel is the model name to request (e.g. "llama3.2").
	OllamaModel string `json:"ollama_model"`
}

// PublicWeb configures how the tool talks to the public search interface.
//
// The public timpi.com site is a JavaScript app whose search results are loaded
// by a background request. Because that request is not publicly documented, the
// exact endpoint is left as configuration rather than hardcoded. Capture it once
// from your browser's DevTools Network tab (run a search on timpi.com, find the
// request that returns results, copy its URL) and fill it in here.
type PublicWeb struct {
	// Endpoint is the URL template for a search request. The literal token
	// {query} is replaced with the URL-encoded query. If QueryParam is set
	// instead, the query is appended as that parameter.
	Endpoint string `json:"endpoint"`

	// Method is GET or POST.
	Method string `json:"method"`

	// QueryParam, if set, adds the query as this URL/query-string parameter
	// (alternative to using {query} in Endpoint).
	QueryParam string `json:"query_param"`

	// ItemsPath is a dotted JSON path to the array of result items in the
	// response body (e.g. "data.results"). Empty means results are not parsed
	// (only HTTP status and byte count are recorded).
	ItemsPath string `json:"items_path"`

	// TitleKey/URLKey/SnippetKey are the field names within each result item.
	TitleKey   string `json:"title_key"`
	URLKey     string `json:"url_key"`
	SnippetKey string `json:"snippet_key"`
}

// API configures the authenticated Timpi Data API adapter.
type API struct {
	// Endpoint is the URL template for a search request; {query} is replaced, or
	// QueryParam is appended.
	Endpoint string `json:"endpoint"`

	// Method is GET or POST.
	Method string `json:"method"`

	// QueryParam, if set, adds the query as this parameter.
	QueryParam string `json:"query_param"`

	// Key is the API key/token. Sent as a Bearer token in the Authorization
	// header unless KeyHeader is set.
	Key string `json:"key"`

	// KeyHeader overrides the header used for the key (e.g. "X-API-Key"). If
	// empty, "Authorization: Bearer <key>" is used.
	KeyHeader string `json:"key_header"`

	// ItemsPath / *Key mirror PublicWeb for response parsing.
	ItemsPath  string `json:"items_path"`
	TitleKey   string `json:"title_key"`
	URLKey     string `json:"url_key"`
	SnippetKey string `json:"snippet_key"`
}

// Server controls the local dashboard HTTP server.
type Server struct {
	// Addr is the listen address for the dashboard (default "127.0.0.1:8770").
	// Binding to loopback keeps the dashboard local to the machine.
	Addr string `json:"addr"`
}

// Default returns a safe default configuration: dry-run mode, one query per
// minute, mixed generation, dashboard on loopback.
func Default() Config {
	return Config{
		Mode:          ModeDryRun,
		PollSeconds:   MinPollSeconds,
		JitterSeconds: 15,
		UserAgent:     "timpi-cise/0.1 (+https://github.com/mhue-ai/timpi-cise; interface-exerciser)",
		Generation: Generation{
			Mode:        GenMixed,
			UseOllama:   false,
			OllamaURL:   "http://localhost:11434",
			OllamaModel: "llama3.2",
		},
		PublicWeb: PublicWeb{
			Method:     "GET",
			QueryParam: "q",
			TitleKey:   "title",
			URLKey:     "url",
			SnippetKey: "snippet",
		},
		API: API{
			Method:     "GET",
			QueryParam: "q",
			TitleKey:   "title",
			URLKey:     "url",
			SnippetKey: "snippet",
		},
		Server: Server{Addr: "127.0.0.1:8770"},
	}
}

// Load reads config from path. If the file does not exist, the default config is
// written to path and returned.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		c := Default()
		if werr := Save(path, c); werr != nil {
			return c, werr
		}
		return c, nil
	}
	if err != nil {
		return Config{}, err
	}
	c := Default()
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	c.Sanitize()
	return c, nil
}

// Save writes config to path as indented JSON.
func Save(path string, c Config) error {
	c.Sanitize()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Sanitize enforces invariants and fills blanks. It is safe to call repeatedly.
func (c *Config) Sanitize() {
	switch c.Mode {
	case ModeDryRun, ModePublicWeb, ModeOfficialAPI:
	default:
		c.Mode = ModeDryRun
	}
	// The hard safety floor: never below one query per minute.
	if c.PollSeconds < MinPollSeconds {
		c.PollSeconds = MinPollSeconds
	}
	if c.JitterSeconds < 0 {
		c.JitterSeconds = 0
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		c.UserAgent = Default().UserAgent
	}
	switch c.Generation.Mode {
	case GenTerms, GenPhrases, GenQuestions, GenMixed:
	default:
		c.Generation.Mode = GenMixed
	}
	if strings.TrimSpace(c.Generation.OllamaURL) == "" {
		c.Generation.OllamaURL = "http://localhost:11434"
	}
	if strings.TrimSpace(c.Generation.OllamaModel) == "" {
		c.Generation.OllamaModel = "llama3.2"
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		c.Server.Addr = "127.0.0.1:8770"
	}
	if strings.TrimSpace(c.PublicWeb.Method) == "" {
		c.PublicWeb.Method = "GET"
	}
	if strings.TrimSpace(c.API.Method) == "" {
		c.API.Method = "GET"
	}
}

// Validate returns an error if the config cannot be used to run in the selected
// mode (e.g. missing endpoint or key). Dry-run is always valid.
func (c Config) Validate() error {
	switch c.Mode {
	case ModeDryRun:
		return nil
	case ModePublicWeb:
		if strings.TrimSpace(c.PublicWeb.Endpoint) == "" {
			return fmt.Errorf("public-web mode needs public_web.endpoint (capture it from timpi.com DevTools Network tab)")
		}
	case ModeOfficialAPI:
		if strings.TrimSpace(c.API.Endpoint) == "" {
			return fmt.Errorf("official-api mode needs api.endpoint")
		}
		if strings.TrimSpace(c.API.Key) == "" {
			return fmt.Errorf("official-api mode needs api.key")
		}
	}
	return nil
}
