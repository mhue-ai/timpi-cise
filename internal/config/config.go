// Package config defines the runtime configuration for timpi-cise and handles
// loading/saving it as JSON. It also enforces the hard safety invariants of the
// tool (notably the minimum polling interval) so they cannot be bypassed via a
// hand-edited config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	ModeDryRun      = "dry-run"      // generate queries, never touch the network (default)
	ModePublicWeb   = "public-web"   // exercise the public timpi.com search interface
	ModeOfficialAPI = "official-api" // use an authenticated Timpi Data API endpoint
)

// Generation modes.
const (
	GenTerms     = "terms"     // short generic word terms
	GenPhrases   = "phrases"   // multi-word phrases
	GenQuestions = "questions" // natural-language questions
	GenMixed     = "mixed"     // a rotating mix of the above
)

// Query sources.
const (
	SourceBuiltin = "builtin" // algorithmic generators (default)
	SourceCSV     = "csv"     // a user-supplied CSV/line list of queries
)

// LLM providers for advanced question generation on a local (or remote) server.
const (
	// LLMOllama uses Ollama's native /api/generate endpoint.
	LLMOllama = "ollama"
	// LLMOpenAI uses any OpenAI-compatible /chat/completions endpoint. This
	// covers LM Studio, llama.cpp's server, Jan, LocalAI, vLLM,
	// text-generation-webui, and hosted OpenAI-style APIs.
	LLMOpenAI = "openai"
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

	// Logging controls the app log and the CSV results log.
	Logging Logging `json:"logging"`
}

// Generation controls where queries come from.
type Generation struct {
	// Mode is one of GenTerms, GenPhrases, GenQuestions, GenMixed. It applies
	// when Source is SourceBuiltin.
	Mode string `json:"mode"`

	// Source is SourceBuiltin (algorithmic) or SourceCSV (user file).
	Source string `json:"source"`

	// CSVPath is the path to a user CSV/line list of queries, used when
	// Source == SourceCSV. First column is the query; an optional second column
	// overrides the kind (terms/phrases/questions).
	CSVPath string `json:"csv_path"`

	// Shuffle randomizes the order of CSV queries instead of cycling in order.
	Shuffle bool `json:"shuffle"`

	// LLM optionally augments question generation using a local (or remote)
	// model server. Applies to the questions/mixed builtin modes.
	LLM LLM `json:"llm"`
}

// LLM configures optional model-backed question generation.
type LLM struct {
	// Enabled turns on model-backed question generation. If the server is
	// unreachable the tool silently falls back to the built-in templates.
	Enabled bool `json:"enabled"`

	// Provider is LLMOllama or LLMOpenAI (OpenAI-compatible).
	Provider string `json:"provider"`

	// BaseURL is the server base URL.
	//   ollama: http://localhost:11434
	//   openai: http://localhost:1234/v1  (LM Studio), http://localhost:8080/v1
	//           (llama.cpp), http://localhost:8000/v1 (vLLM), etc.
	BaseURL string `json:"base_url"`

	// Model is the model name to request (e.g. "llama3.2", "qwen2.5", a path).
	Model string `json:"model"`

	// APIKey is an optional bearer token for OpenAI-compatible servers that
	// require one (most local servers ignore it).
	APIKey string `json:"api_key"`

	// Kinds selects which query types are produced by the model server rather
	// than the built-in CPU generator. Any type left off falls back to the CPU
	// generator. If the model server is unreachable, every type falls back.
	Kinds LLMKinds `json:"kinds"`
}

// LLMKinds is a per-type CPU-vs-model switch.
type LLMKinds struct {
	Terms     bool `json:"terms"`     // short terms
	Phrases   bool `json:"phrases"`   // long terms / phrases
	Questions bool `json:"questions"` // natural-language questions
}

// Any reports whether at least one type is routed to the model.
func (k LLMKinds) Any() bool { return k.Terms || k.Phrases || k.Questions }

// Logging controls the app log and the CSV results log.
type Logging struct {
	// Dir is the directory for log files. Empty means a per-user default.
	Dir string `json:"dir"`

	// AppLog enables writing the structured app log to a file (also always to
	// stderr).
	AppLog bool `json:"app_log"`

	// CSVResults enables appending each executed query result to a CSV file.
	CSVResults bool `json:"csv_results"`
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

// DefaultLogDir returns the per-user directory for logs and CSV output.
func DefaultLogDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "timpi-cise", "logs")
	}
	return "logs"
}

// ResultsCSVPath is where the CSV results log is written.
func (c Config) ResultsCSVPath() string {
	return filepath.Join(c.Logging.Dir, "results.csv")
}

// AppLogPath is where the structured app log is written.
func (c Config) AppLogPath() string {
	return filepath.Join(c.Logging.Dir, "timpicise.log")
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
			Mode:   GenMixed,
			Source: SourceBuiltin,
			LLM: LLM{
				Enabled:  false,
				Provider: LLMOllama,
				BaseURL:  "http://localhost:11434",
				Model:    "llama3.2",
				Kinds:    LLMKinds{Questions: true},
			},
		},
		PublicWeb: PublicWeb{
			// Best discoverable HTTP starting point. NOTE: timpi.com's search is
			// a Blazor Server app that runs over a SignalR WebSocket, so this URL
			// currently returns the app's HTML shell rather than JSON results —
			// the app will say so honestly. Override it with a real REST endpoint
			// when one is available, or use official-api mode.
			Endpoint:   "https://timpi.com/api/search?q={query}",
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
		Logging: Logging{
			AppLog:     true,
			CSVResults: true,
		},
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
	switch c.Generation.Source {
	case SourceBuiltin, SourceCSV:
	default:
		c.Generation.Source = SourceBuiltin
	}
	switch c.Generation.LLM.Provider {
	case LLMOllama, LLMOpenAI:
	default:
		c.Generation.LLM.Provider = LLMOllama
	}
	if strings.TrimSpace(c.Generation.LLM.BaseURL) == "" {
		if c.Generation.LLM.Provider == LLMOpenAI {
			c.Generation.LLM.BaseURL = "http://localhost:1234/v1"
		} else {
			c.Generation.LLM.BaseURL = "http://localhost:11434"
		}
	}
	if strings.TrimSpace(c.Generation.LLM.Model) == "" {
		c.Generation.LLM.Model = "llama3.2"
	}
	// If the model is enabled but no type is routed to it, route questions so
	// enabling it always does something.
	if c.Generation.LLM.Enabled && !c.Generation.LLM.Kinds.Any() {
		c.Generation.LLM.Kinds.Questions = true
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		c.Server.Addr = "127.0.0.1:8770"
	}
	if strings.TrimSpace(c.Logging.Dir) == "" {
		c.Logging.Dir = DefaultLogDir()
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
	if c.Generation.Source == SourceCSV {
		p := strings.TrimSpace(c.Generation.CSVPath)
		if p == "" {
			return fmt.Errorf("csv source selected but no csv_path set (upload a file or set a path)")
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("csv file not found: %s", p)
		}
	}
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
