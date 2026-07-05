package search

import (
	"context"
	"hash/fnv"
)

// dryRun is the default adapter. It performs no network activity at all: it
// simply echoes the query back with a deterministic pseudo "result count" so the
// full pipeline and dashboard can be exercised safely without touching Timpi.
type dryRun struct{}

func (dryRun) Name() string { return "dry-run" }

func (dryRun) Search(_ context.Context, query string) (Result, error) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(query))
	count := int(h.Sum32()%9) + 1 // 1..9, stable per query
	return Result{
		Query:  query,
		Status: 0,
		Count:  count,
		Items: []Item{{
			Title:   "[dry-run] " + query,
			URL:     "https://example.invalid/dry-run",
			Snippet: "Dry-run mode: no request was sent to Timpi. Configure a mode to run live.",
		}},
		LatencyMS: 0,
	}, nil
}
