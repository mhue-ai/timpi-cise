package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/mhue-ai/timpi-cise/internal/config"
)

// browserAdapter drives a real search UI in a headless Chrome/Edge/Chromium and
// scrapes the rendered results. It is the faithful way to exercise a search site
// that renders client-side (e.g. timpi.com's Blazor app, which has no REST API).
//
// A single browser is launched lazily and reused across queries. The runner is
// single-flight, so no locking of the browser context is needed for correctness,
// but a mutex guards lazy initialization and Close.
type browserAdapter struct {
	cfg config.Browser
	ua  string

	mu      sync.Mutex
	alloc   context.Context
	cancelA context.CancelFunc
	brCtx   context.Context
	cancelB context.CancelFunc
	started bool
}

func newBrowserAdapter(cfg config.Browser, userAgent string) *browserAdapter {
	return &browserAdapter{cfg: cfg, ua: userAgent}
}

func (a *browserAdapter) Name() string { return "browser" }

// ensure lazily launches the browser on first use.
func (a *browserAdapter) ensure() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", a.cfg.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.WindowSize(1280, 1000),
	)
	if a.ua != "" {
		opts = append(opts, chromedp.UserAgent(a.ua))
	}
	path := a.cfg.ChromePath
	if path == "" {
		path = findBrowser()
	}
	if path != "" {
		opts = append(opts, chromedp.ExecPath(path))
	}
	a.alloc, a.cancelA = chromedp.NewExecAllocator(context.Background(), opts...)
	a.brCtx, a.cancelB = chromedp.NewContext(a.alloc)
	// The browser launches lazily on the first navigate; its tab is owned by
	// a.brCtx (cancelB), so it persists and is reused across queries.
	a.started = true
	return nil
}

func (a *browserAdapter) Search(ctx context.Context, query string) (Result, error) {
	if err := a.ensure(); err != nil {
		return Result{Query: query}, err
	}

	target := strings.ReplaceAll(a.cfg.URL, "{query}", url.QueryEscape(query))

	a.mu.Lock()
	brCtx := a.brCtx
	a.mu.Unlock()

	timeout := time.Duration(a.cfg.TimeoutSeconds) * time.Second
	tctx, cancel := context.WithTimeout(brCtx, timeout)
	defer cancel()
	// Also respect the caller's context (loop shutdown / overall bound).
	tctx, cancel2 := mergeDone(tctx, ctx)
	defer cancel2()

	start := time.Now()

	// Navigate and dismiss any cookie-consent banner.
	if err := chromedp.Run(tctx,
		chromedp.Navigate(target),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(func(c context.Context) error {
			if a.cfg.ConsentSelector != "" {
				_ = chromedp.Run(c, chromedp.Evaluate(consentJS(a.cfg.ConsentSelector), nil))
			}
			return nil
		}),
	); err != nil {
		return Result{Query: query, LatencyMS: time.Since(start).Milliseconds()}, fmt.Errorf("browser: navigate failed: %w", err)
	}

	// Poll for rendered results until the timeout.
	itemSel := a.cfg.ItemSelector
	var count int
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := chromedp.Run(tctx, chromedp.Evaluate(countJS(itemSel), &count)); err != nil {
			return Result{Query: query, LatencyMS: time.Since(start).Milliseconds()}, fmt.Errorf("browser: %w", err)
		}
		if count > 0 {
			break
		}
		select {
		case <-tctx.Done():
			return Result{Query: query, LatencyMS: time.Since(start).Milliseconds()}, tctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	latency := time.Since(start).Milliseconds()

	// Extract structured results.
	var raw string
	if err := chromedp.Run(tctx, chromedp.Evaluate(extractJS(itemSel, a.cfg.TitleSelector, a.cfg.SnippetSelector), &raw)); err != nil {
		return Result{Query: query, LatencyMS: latency}, fmt.Errorf("browser: extract failed: %w", err)
	}
	var items []Item
	if raw != "" && raw != "null" {
		if uerr := json.Unmarshal([]byte(raw), &items); uerr != nil {
			// Malformed extraction (e.g. a bad selector) — surface it distinctly
			// from a genuine zero-result page.
			return Result{Query: query, Status: 200, LatencyMS: latency,
				Note: "could not parse scraped results — check the item/title selectors"}, nil
		}
	}

	res := Result{Query: query, Status: 200, Count: len(items), Items: items, LatencyMS: latency}
	if len(items) == 0 {
		res.Note = "no results rendered in the page — the query may have zero results, or the selectors need updating"
	}
	return res, nil
}

// Close shuts down the browser.
func (a *browserAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started {
		return nil
	}
	if a.cancelB != nil {
		a.cancelB()
	}
	if a.cancelA != nil {
		a.cancelA()
	}
	a.started = false
	return nil
}

// mergeDone returns a context that is cancelled when either parent or other is
// done, so browser work respects both the per-query timeout and loop shutdown.
func mergeDone(parent, other context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := make(chan struct{})
	go func() {
		select {
		case <-other.Done():
			cancel()
		case <-ctx.Done(): // parent timeout fired — drain promptly
		case <-stop:
		}
	}()
	return ctx, func() { close(stop); cancel() }
}

// findBrowser locates an installed Chrome/Edge/Chromium. It returns "" if none
// is found, in which case chromedp's own detection is used as a fallback.
func findBrowser() string {
	// Try PATH first (covers Linux/macOS and Chrome-on-PATH on Windows).
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	// Standard install locations per OS.
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		pf := os.Getenv("ProgramFiles")
		pfx86 := os.Getenv("ProgramFiles(x86)")
		local := os.Getenv("LocalAppData")
		for _, base := range []string{pf, pfx86, local} {
			if base == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
			)
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default:
		candidates = []string{
			"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
			"/snap/bin/chromium", "/usr/bin/microsoft-edge",
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func consentJS(sel string) string {
	return fmt.Sprintf(`(function(){try{var b=document.querySelector(%q);if(b)b.click();}catch(e){}})()`, sel)
}

func countJS(item string) string {
	return fmt.Sprintf(`document.querySelectorAll(%q).length`, item)
}

func extractJS(item, title, snippet string) string {
	return fmt.Sprintf(`(function(){
		var out=[];
		document.querySelectorAll(%q).forEach(function(card){
			var a=card.querySelector(%q);
			var d=%q?card.querySelector(%q):null;
			if(!a) return;
			out.push({
				title:(a.innerText||'').trim().slice(0,300),
				url:a.href||'',
				snippet:d?(d.innerText||'').trim().slice(0,500):''
			});
		});
		return JSON.stringify(out);
	})()`, item, title, snippet, snippet)
}
