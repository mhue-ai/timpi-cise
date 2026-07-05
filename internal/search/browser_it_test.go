package search

import (
	"context"
	"os"
	"testing"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

// TestBrowserReuseIntegration runs several searches back-to-back through one
// browser adapter to confirm the tab is reused correctly (no "context
// canceled" on the 2nd+ query). It needs a real browser and internet, so it is
// skipped unless BROWSER_IT=1.
func TestBrowserReuseIntegration(t *testing.T) {
	if os.Getenv("BROWSER_IT") == "" {
		t.Skip("set BROWSER_IT=1 to run (needs a browser + internet)")
	}
	cfg := config.Default()
	cfg.Sanitize()
	a := newBrowserAdapter(cfg.Browser, cfg.UserAgent)
	defer a.Close()

	for i, q := range []string{"open source privacy", "best coffee maker", "how to tie a tie", "electric vehicles"} {
		res, err := a.Search(context.Background(), q)
		if err != nil {
			t.Fatalf("query %d (%q) failed: %v", i+1, q, err)
		}
		if res.Count == 0 {
			t.Errorf("query %d (%q) returned 0 results (note: %s)", i+1, q, res.Note)
		}
		t.Logf("query %d %q -> %d results, %dms", i+1, q, res.Count, res.LatencyMS)
	}
}
