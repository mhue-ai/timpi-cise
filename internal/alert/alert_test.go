package alert

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// feed records n results with the given ok/zero pattern into a metrics instance.
func feed(m *metrics.Metrics, ok bool, count int, n int) {
	now := time.Now()
	for i := 0; i < n; i++ {
		m.Record(metrics.ResultSummary{Time: now, OK: ok, Count: count, LatencyMS: 100, Status: 200})
	}
}

func TestAlerterFiresAndRecovers(t *testing.T) {
	cfg := config.Alerts{
		Enabled:         true,
		WindowQueries:   10,
		MaxErrorRate:    0.5, // fire if >50% errors
		CooldownSeconds: 300,
	}
	a := New(cfg, quietLog())

	// Healthy window → no alerts.
	m := metrics.New(50)
	feed(m, true, 5, 10)
	a.Check(m)
	if len(a.Active()) != 0 {
		t.Fatalf("expected no alerts on healthy window, got %v", a.Active())
	}

	// All failures → error-rate alert fires.
	mBad := metrics.New(50)
	feed(mBad, false, 0, 10)
	a.Check(mBad)
	if len(a.Active()) == 0 {
		t.Fatal("expected an error-rate alert")
	}

	// Recovery → alerts clear.
	mGood := metrics.New(50)
	feed(mGood, true, 5, 10)
	a.Check(mGood)
	if len(a.Active()) != 0 {
		t.Fatalf("expected alerts to clear on recovery, got %v", a.Active())
	}
}

func TestAlerterCooldownEdgeTrigger(t *testing.T) {
	cfg := config.Alerts{Enabled: true, WindowQueries: 10, MaxErrorRate: 0.5, CooldownSeconds: 300}
	a := New(cfg, quietLog())

	var notified int
	// Count notifications by observing lastNotified transitions via a fake clock.
	base := time.Unix(1000, 0)
	a.now = func() time.Time { return base }

	mBad := metrics.New(50)
	feed(mBad, false, 0, 10)

	// First check fires (new breach).
	before := len(a.lastNotified)
	a.Check(mBad)
	if len(a.lastNotified) <= before {
		t.Fatal("first breach should record a notification time")
	}
	notified++

	// Immediate re-check within cooldown → no new notification (same timestamp).
	tsBefore := a.lastNotified["error_rate"]
	a.Check(mBad)
	if !a.lastNotified["error_rate"].Equal(tsBefore) {
		t.Error("re-check within cooldown should not re-notify")
	}

	// Advance past cooldown → re-notifies (timestamp advances).
	base = base.Add(6 * time.Minute)
	a.Check(mBad)
	if a.lastNotified["error_rate"].Equal(tsBefore) {
		t.Error("after cooldown the alert should re-notify")
	}
	_ = notified
}

func TestReconfigurePreservesCooldown(t *testing.T) {
	cfg := config.Alerts{Enabled: true, WindowQueries: 10, MaxErrorRate: 0.5, CooldownSeconds: 300}
	a := New(cfg, quietLog())
	base := time.Unix(1000, 0)
	a.now = func() time.Time { return base }

	mBad := metrics.New(50)
	feed(mBad, false, 0, 10)
	a.Check(mBad) // fires; records lastNotified
	first := a.lastNotified["error_rate"]
	if first.IsZero() {
		t.Fatal("expected initial notification")
	}

	// A config save (e.g. changing an unrelated field) must NOT reset cooldown.
	cfg.MaxP95MS = 5000
	a.Reconfigure(cfg)
	a.Check(mBad)
	if !a.lastNotified["error_rate"].Equal(first) {
		t.Error("Reconfigure must preserve cooldown state (should not re-notify)")
	}
}

func TestAlerterDisabled(t *testing.T) {
	a := New(config.Alerts{Enabled: false, WindowQueries: 10, MaxErrorRate: 0.1}, quietLog())
	m := metrics.New(50)
	feed(m, false, 0, 10)
	a.Check(m)
	if len(a.Active()) != 0 {
		t.Error("disabled alerter must not fire")
	}
}

func TestAlerterMinSamples(t *testing.T) {
	a := New(config.Alerts{Enabled: true, WindowQueries: 10, MaxErrorRate: 0.1}, quietLog())
	m := metrics.New(50)
	feed(m, false, 0, 3) // below minSamples
	a.Check(m)
	if len(a.Active()) != 0 {
		t.Error("should not fire below minSamples")
	}
}
