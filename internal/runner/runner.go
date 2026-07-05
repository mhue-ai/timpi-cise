// Package runner drives the polling loop: it pulls a query from the generator,
// executes it through the active search adapter, records metrics, and waits the
// configured interval before the next one. It owns and enforces the tool's pace
// so that the compiled-in safety floor cannot be circumvented.
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/alert"
	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/generate"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/reslog"
	"github.com/mhue-ai/timpi-cise/internal/search"
)

// maxBackoff caps how long the loop will wait after repeated failures.
const maxBackoff = 15 * time.Minute

// Runner coordinates generation + execution on a rate-limited loop.
type Runner struct {
	mu      sync.Mutex
	cfg     config.Config
	cfgPath string
	log     *slog.Logger
	gen     *generate.Generator
	adapter search.Adapter
	met     *metrics.Metrics
	alerter *alert.Alerter
	res     *reslog.Writer
	running bool
	stopCh  chan struct{}
	fails   int
}

// New creates a Runner from an initial config. It does not start polling.
func New(cfg config.Config, cfgPath string, met *metrics.Metrics, log *slog.Logger) *Runner {
	cfg.Sanitize()
	if log == nil {
		log = slog.Default()
	}
	r := &Runner{
		cfg:     cfg,
		cfgPath: cfgPath,
		log:     log,
		gen:     generate.New(cfg.Generation, log),
		adapter: search.Build(cfg),
		met:     met,
		alerter: alert.New(cfg.Alerts, log),
	}
	r.applyResultsLog(cfg)
	return r
}

// ActiveAlerts returns the currently-firing alert messages.
func (r *Runner) ActiveAlerts() []string {
	r.mu.Lock()
	a := r.alerter
	r.mu.Unlock()
	return a.Active()
}

// Config returns a copy of the current config.
func (r *Runner) Config() config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

// AdapterName returns the active adapter name.
func (r *Runner) AdapterName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adapter.Name()
}

// Running reports whether the loop is active.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// CSVInfo reports the CSV-source term count and any load error, for the UI.
func (r *Runner) CSVInfo() (count int, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count = r.gen.CSVCount()
	if err := r.gen.CSVError(); err != nil {
		errMsg = err.Error()
	}
	return
}

// ResultsCSVPath returns the current CSV results-log path ("" if disabled).
func (r *Runner) ResultsCSVPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.res == nil {
		return ""
	}
	return r.res.Path()
}

// UpdateConfig validates and applies a new config, rebuilding the generator,
// adapter, and results log. It persists the config to disk. The change takes
// effect on the next loop iteration if polling is active.
func (r *Runner) UpdateConfig(c config.Config) error {
	c.Sanitize()
	if err := c.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	oldAdapter := r.adapter
	r.cfg = c
	r.gen = generate.New(c.Generation, r.log)
	r.adapter = search.Build(c)
	r.alerter.Reconfigure(c.Alerts) // preserve cooldown/active state across saves
	r.fails = 0
	path := r.cfgPath
	r.mu.Unlock()

	closeAdapter(oldAdapter) // releases the browser if the old adapter held one
	r.applyResultsLog(c)
	r.log.Info("config updated", "mode", c.Mode, "source", c.Generation.Source, "poll_seconds", c.PollSeconds)
	if path != "" {
		return config.Save(path, c)
	}
	return nil
}

// applyResultsLog opens or closes the CSV results writer to match the config.
func (r *Runner) applyResultsLog(c config.Config) {
	r.mu.Lock()
	cur := r.res
	curPath := ""
	if cur != nil {
		curPath = cur.Path()
	}
	want := c.Logging.CSVResults
	wantPath := c.ResultsCSVPath()
	r.mu.Unlock()

	if !want {
		if cur != nil {
			if err := cur.Close(); err != nil {
				r.log.Warn("closing results CSV failed", "path", curPath, "err", err)
			}
			r.mu.Lock()
			r.res = nil
			r.mu.Unlock()
		}
		return
	}
	if cur != nil && curPath == wantPath {
		return // already open at the right path
	}
	if cur != nil {
		if err := cur.Close(); err != nil {
			r.log.Warn("closing results CSV failed", "path", curPath, "err", err)
		}
	}
	w, err := reslog.Open(wantPath, r.log)
	if err != nil {
		r.log.Error("could not open results CSV", "path", wantPath, "err", err)
		r.mu.Lock()
		r.res = nil
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.res = w
	r.mu.Unlock()
	r.log.Info("results CSV log", "path", wantPath)
}

// Start begins polling. It is a no-op if already running or if the current
// config is invalid.
func (r *Runner) Start() error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	if err := r.cfg.Validate(); err != nil {
		r.mu.Unlock()
		return err
	}
	r.running = true
	r.fails = 0
	stop := make(chan struct{})
	r.stopCh = stop
	mode := r.cfg.Mode
	poll := r.cfg.PollSeconds
	r.mu.Unlock()

	r.met.SetRunning(true)
	r.log.Info("polling started", "mode", mode, "poll_seconds", poll)
	go r.loop(stop)
	return nil
}

// Stop halts polling. It is a no-op if not running.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	close(r.stopCh)
	r.stopCh = nil
	r.mu.Unlock()
	r.met.SetRunning(false)
	r.log.Info("polling stopped")
}

func (r *Runner) loop(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}

		wait := r.safeStep(stop)

		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// safeStep runs one step with panic recovery so an unexpected failure in an
// adapter or generator is logged rather than silently killing the loop. On a
// panic it waits one base interval before retrying.
func (r *Runner) safeStep(stop <-chan struct{}) (wait time.Duration) {
	defer func() {
		if rec := recover(); rec != nil {
			r.log.Error("recovered from panic in query step", "panic", rec, "stack", string(debug.Stack()))
			// Treat a panic as a failure so backoff applies (avoids hammering on
			// a persistent panic); nextWait handles the fail counter and floor.
			wait = r.nextWait(r.Config(), false, 0)
		}
	}()
	return r.step(stop)
}

// step runs exactly one query and returns how long to wait before the next.
func (r *Runner) step(stop <-chan struct{}) time.Duration {
	r.mu.Lock()
	gen := r.gen
	adapter := r.adapter
	cfg := r.cfg
	res := r.res
	alerter := r.alerter
	r.mu.Unlock()

	// Bound the whole query (including any optional model generation) so a hung
	// backend cannot stall the loop.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	q := gen.Next(ctx)
	result, err := adapter.Search(ctx, q.Text)

	summary := metrics.ResultSummary{
		Time:      time.Now(),
		Query:     q.Text,
		Kind:      q.Kind,
		Mode:      cfg.Mode,
		Status:    result.Status,
		Count:     result.Count,
		LatencyMS: result.LatencyMS,
		OK:        err == nil,
	}
	if err != nil {
		summary.Err = err.Error()
	}
	summary.Note = result.Note
	for i, it := range result.Items {
		if i >= 3 {
			break
		}
		summary.Preview = append(summary.Preview, metrics.PreviewItem{
			Title:   it.Title,
			URL:     it.URL,
			Snippet: it.Snippet,
		})
		if it.Title != "" {
			summary.TopTitles = append(summary.TopTitles, it.Title)
		}
	}

	// Assertions: evaluate when globally enabled or when this query carries a
	// per-query "must contain" (golden) expectation.
	if cfg.Assertions.Enabled || q.MustContain != "" {
		pass, msg := evaluateAssertions(cfg.Assertions, q, result, err)
		summary.AssertPass = &pass
		summary.AssertMsg = msg
	}

	r.met.Record(summary)
	if res != nil {
		if werr := res.Write(summary); werr != nil {
			r.log.Error("results CSV write failed", "err", werr)
		}
	}
	if err != nil {
		r.log.Warn("query failed", "query", q.Text, "status", result.Status, "err", err)
	} else {
		r.log.Debug("query ok", "query", q.Text, "kind", q.Kind, "count", result.Count, "latency_ms", result.LatencyMS)
	}
	if summary.AssertPass != nil && !*summary.AssertPass {
		r.log.Warn("assertion failed", "query", q.Text, "detail", summary.AssertMsg)
	}
	alerter.Check(r.met)

	return r.nextWait(cfg, err == nil, result.RetryAfter)
}

// evaluateAssertions checks a result against the global assertions plus any
// per-query "must contain" expectation, returning pass/fail and a reason string.
func evaluateAssertions(a config.Assertions, q generate.Query, res search.Result, err error) (bool, string) {
	var fails []string
	if err != nil {
		fails = append(fails, "request failed: "+err.Error())
	}
	if a.MinResults > 0 && res.Count < a.MinResults {
		fails = append(fails, fmt.Sprintf("got %d results, want >= %d", res.Count, a.MinResults))
	}
	if a.MaxLatencyMS > 0 && res.LatencyMS > int64(a.MaxLatencyMS) {
		fails = append(fails, fmt.Sprintf("latency %dms > %dms", res.LatencyMS, a.MaxLatencyMS))
	}
	if q.MustContain != "" && !resultsContain(res.Items, q.MustContain) {
		fails = append(fails, fmt.Sprintf("no result contains %q", q.MustContain))
	}
	if len(fails) == 0 {
		return true, ""
	}
	return false, strings.Join(fails, "; ")
}

// resultsContain reports whether any result item contains needle (case-
// insensitive) in its title, URL, or snippet.
func resultsContain(items []search.Item, needle string) bool {
	n := strings.ToLower(needle)
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Title), n) ||
			strings.Contains(strings.ToLower(it.URL), n) ||
			strings.Contains(strings.ToLower(it.Snippet), n) {
			return true
		}
	}
	return false
}

// nextWait computes the delay before the next query, honoring the safety floor,
// jitter, server-requested Retry-After, and exponential backoff on failures.
func (r *Runner) nextWait(cfg config.Config, ok bool, retryAfter time.Duration) time.Duration {
	base := time.Duration(cfg.PollSeconds) * time.Second
	if base < config.MinPollSeconds*time.Second {
		base = config.MinPollSeconds * time.Second
	}

	r.mu.Lock()
	if ok {
		r.fails = 0
	} else {
		r.fails++
	}
	fails := r.fails
	r.mu.Unlock()

	wait := base
	if !ok {
		// Exponential backoff: base * 2^min(fails,4), capped.
		mult := 1 << min(fails, 4)
		wait = base * time.Duration(mult)
	}
	if retryAfter > wait {
		wait = retryAfter
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}

	// Add jitter (only on the happy path; backoff already spreads load).
	if ok && cfg.JitterSeconds > 0 {
		wait += time.Duration(rand.IntN(cfg.JitterSeconds+1)) * time.Second
	}
	return wait
}

// Close stops polling (if running) and releases resources (results log, browser).
func (r *Runner) Close() {
	r.Stop() // ensure the loop goroutine exits before we close its writer
	r.mu.Lock()
	res := r.res
	r.res = nil
	adapter := r.adapter
	r.mu.Unlock()
	closeAdapter(adapter)
	if res != nil {
		if err := res.Close(); err != nil {
			r.log.Warn("closing results CSV failed", "err", err)
		}
	}
}

// closeAdapter closes an adapter that holds resources (e.g. the browser).
func closeAdapter(a search.Adapter) {
	if c, ok := a.(interface{ Close() error }); ok && c != nil {
		_ = c.Close()
	}
}
