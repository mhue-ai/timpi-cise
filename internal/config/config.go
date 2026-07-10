// Package config defines the runtime configuration for timpi-cise and handles
// loading/saving it as JSON. It also enforces the hard safety invariants of the
// tool (notably the minimum polling interval) so they cannot be bypassed via a
// hand-edited config file.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	ModeDryRun      = "dry-run"      // generate queries, never touch the network
	ModePublicWeb   = "public-web"   // hit a REST search endpoint over HTTP
	ModeBrowser     = "browser"      // drive the real timpi.com UI in a headless browser (default)
	ModeOfficialAPI = "official-api" // use an authenticated Timpi Data API endpoint
)

// Generation modes.
const (
	GenTerms     = "terms"     // short generic word terms
	GenPhrases   = "phrases"   // multi-word phrases
	GenQuestions = "questions" // natural-language questions
	GenMixed     = "mixed"     // a rotating mix of the above
	// GenRealistic draws from a curated corpus of real-world-style queries with
	// head-weighted (Zipfian-like) sampling across search intents, so the
	// traffic shape resembles genuine search demand rather than templates.
	GenRealistic = "realistic"
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

	// Browser configures the headless-browser adapter (drives the real UI).
	Browser Browser `json:"browser"`

	// Server controls the local dashboard.
	Server Server `json:"server"`

	// Logging controls the app log and the CSV results log.
	Logging Logging `json:"logging"`

	// Assertions turn the tool from a traffic generator into a monitor: each
	// query is checked against expectations and reported pass/fail.
	Assertions Assertions `json:"assertions"`

	// Alerts fire when windowed health metrics breach thresholds.
	Alerts Alerts `json:"alerts"`
}

// Alerts define health thresholds evaluated over a sliding window of recent
// queries. On a breach the tool logs an error, surfaces the alert on the
// dashboard, and (optionally) POSTs to a webhook (Slack/Discord/generic).
type Alerts struct {
	// Enabled turns alerting on.
	Enabled bool `json:"enabled"`

	// WebhookURL receives a JSON POST on each alert transition. Compatible with
	// Slack ("text") and Discord ("content") incoming webhooks. Empty = log only.
	WebhookURL string `json:"webhook_url"`

	// WindowQueries is how many recent queries the rates are computed over.
	WindowQueries int `json:"window_queries"`

	// Thresholds (0 = that check is disabled).
	MaxErrorRate      float64 `json:"max_error_rate"`       // 0..1
	MaxZeroResultRate float64 `json:"max_zero_result_rate"` // 0..1
	MaxAssertFailRate float64 `json:"max_assert_fail_rate"` // 0..1
	MaxP95MS          int     `json:"max_p95_ms"`

	// CooldownSeconds is the minimum time between repeat notifications for a
	// still-firing alert.
	CooldownSeconds int `json:"cooldown_seconds"`
}

// Assertions define health expectations evaluated on every executed query.
// Per-query "must contain" checks (golden queries) come from a third CSV column
// and apply even when Enabled is false.
type Assertions struct {
	// Enabled turns on the global checks below.
	Enabled bool `json:"enabled"`

	// MaxLatencyMS fails a query slower than this (0 = no limit).
	MaxLatencyMS int `json:"max_latency_ms"`

	// MinResults fails a query returning fewer than this many results.
	MinResults int `json:"min_results"`
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

	// PersistMetrics writes a live snapshot of metrics to metrics.json (for
	// external scraping/backup). Counters are NOT restored on start — each run
	// begins with fresh counters.
	PersistMetrics bool `json:"persist_metrics"`
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

// Browser configures the headless-browser adapter, which drives a real search
// UI (e.g. timpi.com's Blazor app) in an installed Chrome/Edge/Chromium and
// scrapes the rendered results. This is the faithful way to exercise a search
// site that has no REST endpoint. It requires a browser to be installed.
type Browser struct {
	// URL is the results URL template; {query} is replaced with the URL-encoded
	// query. For timpi.com the search page reads the query straight from the URL.
	URL string `json:"url"`

	// ChromePath optionally points at a specific Chrome/Edge/Chromium binary.
	// Empty means auto-detect from PATH / standard locations.
	ChromePath string `json:"chrome_path"`

	// Headless runs the browser without a visible window (default true).
	Headless bool `json:"headless"`

	// Selectors identify the rendered results. Defaults target timpi.com.
	ItemSelector    string `json:"item_selector"`    // each result container
	TitleSelector   string `json:"title_selector"`   // title link within an item
	SnippetSelector string `json:"snippet_selector"` // snippet within an item
	ConsentSelector string `json:"consent_selector"` // cookie-consent accept button

	// TimeoutSeconds bounds how long to wait for results to render.
	TimeoutSeconds int `json:"timeout_seconds"`
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

// MetricsPath is where persisted metrics are stored.
func (c Config) MetricsPath() string {
	return filepath.Join(c.Logging.Dir, "metrics.json")
}

// Default returns the default configuration: it exercises the real timpi.com
// interface (browser mode) at one query per minute, and by default generates
// questions with a local LM Studio model (OpenAI-compatible on :1234/v1). The AI
// is optional — if the model server is unreachable, generation transparently
// falls back to the built-in CPU generator. Polling still does not begin until
// the user presses Start (or passes --start).
func Default() Config {
	return Config{
		Mode:          ModeBrowser,
		PollSeconds:   MinPollSeconds,
		JitterSeconds: 15,
		UserAgent:     "timpi-cise/0.1 (+https://github.com/mhue-ai/timpi-cise; interface-exerciser)",
		Generation: Generation{
			Mode:   GenMixed,
			Source: SourceBuiltin,
			LLM: LLM{
				Enabled:  true,      // AI on by default; falls back to CPU if unavailable
				Provider: LLMOpenAI, // LM Studio / any OpenAI-compatible server
				BaseURL:  "http://localhost:1234/v1",
				Model:    "", // pick via "Fetch installed models"
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
		Browser: Browser{
			URL:             "https://timpi.com/search?q={query}",
			Headless:        true,
			ItemSelector:    ".all-item-content",
			TitleSelector:   "a.title",
			SnippetSelector: ".description",
			ConsentSelector: ".iubenda-cs-accept-btn",
			TimeoutSeconds:  30,
		},
		Server: Server{Addr: "127.0.0.1:8770"},
		Logging: Logging{
			AppLog:         true,
			CSVResults:     true,
			PersistMetrics: true,
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
	// Ensure the parent directory exists — otherwise WriteFile fails and the
	// config silently never persists (so the next start falls back to defaults).
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, b, 0o600)
}

// Sanitize enforces invariants and fills blanks. It is safe to call repeatedly.
func (c *Config) Sanitize() {
	switch c.Mode {
	case ModeDryRun, ModePublicWeb, ModeBrowser, ModeOfficialAPI:
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
	case GenTerms, GenPhrases, GenQuestions, GenMixed, GenRealistic:
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
	if c.Assertions.MaxLatencyMS < 0 {
		c.Assertions.MaxLatencyMS = 0
	}
	if c.Assertions.MinResults < 0 {
		c.Assertions.MinResults = 0
	}
	if c.Alerts.WindowQueries <= 0 {
		c.Alerts.WindowQueries = 20
	}
	if c.Alerts.CooldownSeconds <= 0 {
		c.Alerts.CooldownSeconds = 300
	}
	for _, r := range []*float64{&c.Alerts.MaxErrorRate, &c.Alerts.MaxZeroResultRate, &c.Alerts.MaxAssertFailRate} {
		if *r < 0 {
			*r = 0
		} else if *r > 1 {
			*r = 1
		}
	}
	if c.Alerts.MaxP95MS < 0 {
		c.Alerts.MaxP95MS = 0
	}
	if strings.TrimSpace(c.PublicWeb.Method) == "" {
		c.PublicWeb.Method = "GET"
	}
	if strings.TrimSpace(c.API.Method) == "" {
		c.API.Method = "GET"
	}
	if strings.TrimSpace(c.Browser.URL) == "" {
		c.Browser.URL = "https://timpi.com/search?q={query}"
	}
	if strings.TrimSpace(c.Browser.ItemSelector) == "" {
		c.Browser.ItemSelector = ".all-item-content"
	}
	if strings.TrimSpace(c.Browser.TitleSelector) == "" {
		c.Browser.TitleSelector = "a.title"
	}
	if c.Browser.TimeoutSeconds <= 0 {
		c.Browser.TimeoutSeconds = 30
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
		if err := validateEndpoint(c.PublicWeb.Endpoint); err != nil {
			return err
		}
	case ModeBrowser:
		if strings.TrimSpace(c.Browser.URL) == "" {
			return fmt.Errorf("browser mode needs browser.url")
		}
		if err := validateEndpoint(c.Browser.URL); err != nil {
			return err
		}
	case ModeOfficialAPI:
		if strings.TrimSpace(c.API.Endpoint) == "" {
			return fmt.Errorf("official-api mode needs api.endpoint")
		}
		if err := validateEndpoint(c.API.Endpoint); err != nil {
			return err
		}
		if strings.TrimSpace(c.API.Key) == "" {
			return fmt.Errorf("official-api mode needs api.key")
		}
	}
	return nil
}

// validateEndpoint ensures an endpoint is a well-formed http(s) URL. The
// {query} placeholder is tolerated in the path/query.
func validateEndpoint(ep string) error {
	probe := strings.ReplaceAll(ep, "{query}", "x")
	u, err := url.Parse(probe)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint must be an http(s) URL, got %q", ep)
	}
	if u.Host == "" {
		return fmt.Errorf("endpoint has no host: %q", ep)
	}
	return nil
}
