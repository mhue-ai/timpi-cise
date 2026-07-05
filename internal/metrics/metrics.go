// Package metrics collects execution metrics for the dashboard in a thread-safe
// way. It keeps running counters, a latency window for percentiles, a per-minute
// time series for trend charts, and a small ring buffer of recent results.
package metrics

import (
	"math"
	"sort"
	"sync"
	"time"
)

// ResultSummary is a lightweight, display-oriented view of one executed query.
// It intentionally does not depend on the search package to avoid an import
// cycle; the runner converts search results into this shape.
type ResultSummary struct {
	Time       time.Time     `json:"time"`
	Query      string        `json:"query"`
	Kind       string        `json:"kind"`   // terms | phrases | questions
	Mode       string        `json:"mode"`   // dry-run | public-web | official-api
	Status     int           `json:"status"` // HTTP status, or 0 for dry-run
	Count      int           `json:"count"`  // number of results parsed
	LatencyMS  int64         `json:"latency_ms"`
	OK         bool          `json:"ok"`
	Err        string        `json:"err,omitempty"`
	Note       string        `json:"note,omitempty"`
	AssertPass *bool         `json:"assert_pass,omitempty"` // nil if assertions disabled
	AssertMsg  string        `json:"assert_msg,omitempty"`
	Preview    []PreviewItem `json:"preview,omitempty"`
	TopTitles  []string      `json:"top_titles,omitempty"`
}

// PreviewItem is a compact result item for the miniature results display.
type PreviewItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// bucket is one per-minute aggregate of the time series.
type bucket struct {
	minute int64 // unix seconds truncated to the minute
	sent   int64
	ok     int64
	failed int64
	sumLat int64
}

const (
	latWindow    = 500 // latencies kept for percentile computation
	seriesWindow = 60  // per-minute buckets kept (last hour)
)

// Metrics holds aggregate counters and recent results.
type Metrics struct {
	mu sync.RWMutex

	startedAt   time.Time
	sent        int64
	ok          int64
	failed      int64
	zeroResults int64 // OK responses that returned 0 results
	assertRun   int64 // queries with an assertion evaluated
	assertFail  int64 // assertion failures
	totalLatMS  int64
	lastLatMS   int64
	byStatus    map[int]int64
	lastQuery   string
	lastAt      time.Time
	running     bool

	lat       []int64 // recent latencies (ring)
	series    []bucket
	recent    []ResultSummary
	recentMax int
}

// New returns a Metrics keeping up to recentMax recent results.
func New(recentMax int) *Metrics {
	if recentMax <= 0 {
		recentMax = 25
	}
	return &Metrics{
		startedAt: time.Now(),
		byStatus:  map[int]int64{},
		recentMax: recentMax,
	}
}

// SetRunning records whether the polling loop is active.
func (m *Metrics) SetRunning(v bool) {
	m.mu.Lock()
	m.running = v
	m.mu.Unlock()
}

// Record adds a completed query result to the metrics.
func (m *Metrics) Record(r ResultSummary) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent++
	if r.OK {
		m.ok++
		if r.Count == 0 {
			m.zeroResults++
		}
	} else {
		m.failed++
	}
	if r.AssertPass != nil {
		m.assertRun++
		if !*r.AssertPass {
			m.assertFail++
		}
	}
	m.totalLatMS += r.LatencyMS
	m.lastLatMS = r.LatencyMS
	if r.Status != 0 {
		m.byStatus[r.Status]++
	}
	m.lastQuery = r.Query
	m.lastAt = r.Time

	// Latency window for percentiles.
	m.lat = append(m.lat, r.LatencyMS)
	if len(m.lat) > latWindow {
		m.lat = m.lat[len(m.lat)-latWindow:]
	}

	// Per-minute time series.
	minute := r.Time.Unix() / 60 * 60
	if n := len(m.series); n > 0 && m.series[n-1].minute == minute {
		b := &m.series[n-1]
		b.sent++
		if r.OK {
			b.ok++
		} else {
			b.failed++
		}
		b.sumLat += r.LatencyMS
	} else {
		b := bucket{minute: minute, sent: 1, sumLat: r.LatencyMS}
		if r.OK {
			b.ok = 1
		} else {
			b.failed = 1
		}
		m.series = append(m.series, b)
		if len(m.series) > seriesWindow {
			m.series = m.series[len(m.series)-seriesWindow:]
		}
	}

	m.recent = append(m.recent, r)
	if len(m.recent) > m.recentMax {
		m.recent = m.recent[len(m.recent)-m.recentMax:]
	}
}

// SeriesPoint is one per-minute aggregate for the trend chart.
type SeriesPoint struct {
	TimeUnix     int64 `json:"time_unix"`
	Sent         int64 `json:"sent"`
	OK           int64 `json:"ok"`
	Failed       int64 `json:"failed"`
	AvgLatencyMS int64 `json:"avg_latency_ms"`
}

// Snapshot is a JSON-friendly copy of the current metrics.
type Snapshot struct {
	Running        bool            `json:"running"`
	UptimeSeconds  int64           `json:"uptime_seconds"`
	Sent           int64           `json:"sent"`
	OK             int64           `json:"ok"`
	Failed         int64           `json:"failed"`
	ZeroResults    int64           `json:"zero_results"`
	ZeroResultRate float64         `json:"zero_result_rate"` // 0..1 over OK responses
	AssertRun      int64           `json:"assert_run"`
	AssertFail     int64           `json:"assert_fail"`
	AvgLatencyMS   int64           `json:"avg_latency_ms"`
	LastLatencyMS  int64           `json:"last_latency_ms"`
	P50MS          int64           `json:"p50_ms"`
	P95MS          int64           `json:"p95_ms"`
	P99MS          int64           `json:"p99_ms"`
	ByStatus       map[int]int64   `json:"by_status"`
	LastQuery      string          `json:"last_query"`
	LastAt         *time.Time      `json:"last_at,omitempty"`
	Series         []SeriesPoint   `json:"series"`
	Recent         []ResultSummary `json:"recent"`
}

// Snapshot returns a consistent copy of all metrics for serialization.
func (m *Metrics) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var avg int64
	if m.sent > 0 {
		avg = m.totalLatMS / m.sent
	}
	var zeroRate float64
	if m.ok > 0 {
		zeroRate = float64(m.zeroResults) / float64(m.ok)
	}

	byStatus := make(map[int]int64, len(m.byStatus))
	for k, v := range m.byStatus {
		byStatus[k] = v
	}

	recent := make([]ResultSummary, len(m.recent))
	copy(recent, m.recent)
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i] // newest first
	}

	series := make([]SeriesPoint, 0, len(m.series))
	for _, b := range m.series {
		var a int64
		if b.sent > 0 {
			a = b.sumLat / b.sent
		}
		series = append(series, SeriesPoint{
			TimeUnix: b.minute, Sent: b.sent, OK: b.ok, Failed: b.failed, AvgLatencyMS: a,
		})
	}

	var lastAt *time.Time
	if !m.lastAt.IsZero() {
		t := m.lastAt
		lastAt = &t
	}

	return Snapshot{
		Running:        m.running,
		UptimeSeconds:  int64(time.Since(m.startedAt).Seconds()),
		Sent:           m.sent,
		OK:             m.ok,
		Failed:         m.failed,
		ZeroResults:    m.zeroResults,
		ZeroResultRate: zeroRate,
		AssertRun:      m.assertRun,
		AssertFail:     m.assertFail,
		AvgLatencyMS:   avg,
		LastLatencyMS:  m.lastLatMS,
		P50MS:          percentile(m.lat, 0.50),
		P95MS:          percentile(m.lat, 0.95),
		P99MS:          percentile(m.lat, 0.99),
		ByStatus:       byStatus,
		LastQuery:      m.lastQuery,
		LastAt:         lastAt,
		Series:         series,
		Recent:         recent,
	}
}

// percentile returns the p-quantile (p in 0..1) using the nearest-rank method
// (ceil), which is the conservative choice for latency SLAs. Returns 0 for an
// empty set.
func percentile(values []int64, p float64) int64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	cp := make([]int64, n)
	copy(cp, values)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	rank := int(math.Ceil(p*float64(n))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= n {
		rank = n - 1
	}
	return cp[rank]
}
