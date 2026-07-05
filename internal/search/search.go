// Package search executes generated queries against a search backend. It defines
// a small Adapter interface with three implementations: a safe dry-run adapter
// (no network), the public timpi.com web adapter, and an authenticated Data API
// adapter. Adapters are deliberately configuration-driven so no undocumented
// endpoint is hardcoded.
package search

import (
	"context"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

// Item is a single search result.
type Item struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Result is the outcome of executing one query.
type Result struct {
	Query      string
	Status     int // HTTP status; 0 for dry-run
	Count      int
	Items      []Item
	LatencyMS  int64
	RetryAfter time.Duration // parsed from a 429/503 Retry-After, if any
}

// Adapter executes a single query.
type Adapter interface {
	// Search runs one query. A non-nil error means the query failed; Result may
	// still carry a Status (e.g. 429) that the caller uses for backoff.
	Search(ctx context.Context, query string) (Result, error)
	// Name identifies the adapter for the UI/logs.
	Name() string
}

// Build returns the adapter for the configured mode. The caller is expected to
// have validated the config first.
func Build(c config.Config) Adapter {
	switch c.Mode {
	case config.ModePublicWeb:
		return newHTTPAdapter("public-web", httpAdapterOpts{
			endpoint:   c.PublicWeb.Endpoint,
			method:     c.PublicWeb.Method,
			queryParam: c.PublicWeb.QueryParam,
			userAgent:  c.UserAgent,
			itemsPath:  c.PublicWeb.ItemsPath,
			titleKey:   c.PublicWeb.TitleKey,
			urlKey:     c.PublicWeb.URLKey,
			snippetKey: c.PublicWeb.SnippetKey,
			browserish: true,
		})
	case config.ModeOfficialAPI:
		return newHTTPAdapter("official-api", httpAdapterOpts{
			endpoint:   c.API.Endpoint,
			method:     c.API.Method,
			queryParam: c.API.QueryParam,
			userAgent:  c.UserAgent,
			apiKey:     c.API.Key,
			keyHeader:  c.API.KeyHeader,
			itemsPath:  c.API.ItemsPath,
			titleKey:   c.API.TitleKey,
			urlKey:     c.API.URLKey,
			snippetKey: c.API.SnippetKey,
		})
	default:
		return dryRun{}
	}
}
