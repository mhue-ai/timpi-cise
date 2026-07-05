// Package alert evaluates windowed health metrics against configured thresholds
// and notifies (log + optional webhook) when they are breached. It edge-triggers
// with a cooldown so a persistent problem doesn't flood notifications.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

// minSamples is the fewest windowed results before alerts evaluate, to avoid
// noise on startup.
const minSamples = 5

// Alerter checks metrics against thresholds and notifies on breaches.
type Alerter struct {
	cfg config.Alerts
	log *slog.Logger
	hc  *http.Client
	now func() time.Time // injectable for tests

	mu           sync.Mutex
	lastNotified map[string]time.Time
	active       []string
}

// New builds an Alerter.
func New(cfg config.Alerts, log *slog.Logger) *Alerter {
	if log == nil {
		log = slog.Default()
	}
	return &Alerter{
		cfg:          cfg,
		log:          log,
		hc:           &http.Client{Timeout: 10 * time.Second},
		now:          time.Now,
		lastNotified: map[string]time.Time{},
	}
}

// Active returns the currently-firing alert messages (for the dashboard). It
// always returns a non-nil slice so the JSON field is [] rather than null.
func (a *Alerter) Active() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.active))
	return append(out, a.active...)
}

// Check evaluates the current window and fires/clears alerts.
func (a *Alerter) Check(m *metrics.Metrics) {
	if !a.cfg.Enabled {
		a.mu.Lock()
		a.active = nil
		a.mu.Unlock()
		return
	}
	ws := m.Window(a.cfg.WindowQueries)
	if ws.Count < minSamples {
		return
	}

	breaches := a.evaluate(ws)

	a.mu.Lock()
	wasActive := len(a.active) > 0
	a.active = breachMessages(breaches)
	// Decide which breaches to notify: new ones, or ones past their cooldown.
	var toNotify []string
	now := a.now()
	cooldown := time.Duration(a.cfg.CooldownSeconds) * time.Second
	for key, msg := range breaches {
		last, seen := a.lastNotified[key]
		if !seen || now.Sub(last) >= cooldown {
			a.lastNotified[key] = now
			toNotify = append(toNotify, msg)
		}
	}
	// Clear cooldown state for breaches that have cleared.
	for key := range a.lastNotified {
		if _, still := breaches[key]; !still {
			delete(a.lastNotified, key)
		}
	}
	recovered := wasActive && len(breaches) == 0
	a.mu.Unlock()

	if len(toNotify) > 0 {
		sort.Strings(toNotify)
		a.notify("🔴 timpi-cise alert", toNotify)
	}
	if recovered {
		a.notify("🟢 timpi-cise recovered", []string{"all monitored metrics are back within thresholds"})
	}
}

// evaluate returns a map of breachKey → human message for each threshold hit.
func (a *Alerter) evaluate(ws metrics.WindowStat) map[string]string {
	out := map[string]string{}
	c := a.cfg
	if c.MaxErrorRate > 0 && ws.ErrorRate > c.MaxErrorRate {
		out["error_rate"] = fmt.Sprintf("error rate %.0f%% > %.0f%% (last %d queries)", ws.ErrorRate*100, c.MaxErrorRate*100, ws.Count)
	}
	if c.MaxZeroResultRate > 0 && ws.ZeroResultRate > c.MaxZeroResultRate {
		out["zero_result_rate"] = fmt.Sprintf("zero-result rate %.0f%% > %.0f%%", ws.ZeroResultRate*100, c.MaxZeroResultRate*100)
	}
	if c.MaxAssertFailRate > 0 && ws.AssertFailRate > c.MaxAssertFailRate {
		out["assert_fail_rate"] = fmt.Sprintf("assertion-failure rate %.0f%% > %.0f%%", ws.AssertFailRate*100, c.MaxAssertFailRate*100)
	}
	if c.MaxP95MS > 0 && ws.P95MS > int64(c.MaxP95MS) {
		out["p95"] = fmt.Sprintf("p95 latency %dms > %dms", ws.P95MS, c.MaxP95MS)
	}
	return out
}

func breachMessages(breaches map[string]string) []string {
	if len(breaches) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(breaches))
	for _, m := range breaches {
		msgs = append(msgs, m)
	}
	sort.Strings(msgs)
	return msgs
}

// notify logs the alert and posts to the webhook (if configured), off the caller
// goroutine so a slow webhook never stalls the poll loop.
func (a *Alerter) notify(title string, msgs []string) {
	a.log.Warn("alert", "title", title, "detail", strings.Join(msgs, "; "))
	if a.cfg.WebhookURL == "" {
		return
	}
	text := title + "\n• " + strings.Join(msgs, "\n• ")
	go a.postWebhook(text)
}

func (a *Alerter) postWebhook(text string) {
	// Fields cover Slack ("text") and Discord ("content"); extra keys are
	// ignored by both.
	body, _ := json.Marshal(map[string]any{"text": text, "content": text})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		a.log.Warn("alert webhook: build request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.hc.Do(req)
	if err != nil {
		a.log.Warn("alert webhook: post failed", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		a.log.Warn("alert webhook: non-2xx", "status", resp.StatusCode)
	}
}
