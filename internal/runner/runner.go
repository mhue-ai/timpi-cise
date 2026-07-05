// Package runner drives the polling loop: it pulls a query from the generator,
// executes it through the active search adapter, records metrics, and waits the
// configured interval before the next one. It owns and enforces the tool's pace
// so that the compiled-in safety floor cannot be circumvented.
package runner

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/generate"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/search"
)

// maxBackoff caps how long the loop will wait after repeated failures.
const maxBackoff = 15 * time.Minute

// Runner coordinates generation + execution on a rate-limited loop.
type Runner struct {
	mu       sync.Mutex
	cfg      config.Config
	cfgPath  string
	gen      *generate.Generator
	adapter  search.Adapter
	met      *metrics.Metrics
	running  bool
	stopCh   chan struct{}
	fails    int
}

// New creates a Runner from an initial config. It does not start polling.
func New(cfg config.Config, cfgPath string, met *metrics.Metrics) *Runner {
	cfg.Sanitize()
	return &Runner{
		cfg:     cfg,
		cfgPath: cfgPath,
		gen:     generate.New(cfg.Generation),
		adapter: search.Build(cfg),
		met:     met,
	}
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

// UpdateConfig validates and applies a new config, rebuilding the generator and
// adapter. It persists the config to disk. The change takes effect on the next
// loop iteration if polling is active.
func (r *Runner) UpdateConfig(c config.Config) error {
	c.Sanitize()
	if err := c.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	r.cfg = c
	r.gen = generate.New(c.Generation)
	r.adapter = search.Build(c)
	r.fails = 0
	path := r.cfgPath
	r.mu.Unlock()
	if path != "" {
		return config.Save(path, c)
	}
	return nil
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
	r.mu.Unlock()

	r.met.SetRunning(true)
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
}

func (r *Runner) loop(stop <-chan struct{}) {
	for {
		// Execute one query immediately, then wait. Stop is checked before and
		// during the wait so shutdown is prompt.
		select {
		case <-stop:
			return
		default:
		}

		wait := r.step(stop)

		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// step runs exactly one query and returns how long to wait before the next.
func (r *Runner) step(stop <-chan struct{}) time.Duration {
	r.mu.Lock()
	gen := r.gen
	adapter := r.adapter
	cfg := r.cfg
	r.mu.Unlock()

	// Bound the whole query (including any optional Ollama generation) so a
	// hung backend cannot stall the loop.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	q := gen.Next(ctx)
	res, err := adapter.Search(ctx, q.Text)

	summary := metrics.ResultSummary{
		Time:      time.Now(),
		Query:     q.Text,
		Kind:      q.Kind,
		Status:    res.Status,
		Count:     res.Count,
		LatencyMS: res.LatencyMS,
		OK:        err == nil,
	}
	if err != nil {
		summary.Err = err.Error()
	}
	for i, it := range res.Items {
		if i >= 3 {
			break
		}
		if it.Title != "" {
			summary.TopTitles = append(summary.TopTitles, it.Title)
		}
	}
	r.met.Record(summary)

	return r.nextWait(cfg, err == nil, res.RetryAfter)
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
