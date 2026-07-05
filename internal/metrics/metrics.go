// Package metrics collects basic execution metrics for the dashboard in a
// thread-safe way. It keeps running counters plus a small ring buffer of recent
// query results.
package metrics

import (
	"sync"
	"time"
)

// ResultSummary is a lightweight, display-oriented view of one executed query.
// It intentionally does not depend on the search package to avoid an import
// cycle; the runner converts search results into this shape.
type ResultSummary struct {
	Time       time.Time `json:"time"`
	Query      string    `json:"query"`
	Kind       string    `json:"kind"`   // terms | phrases | questions
	Status     int       `json:"status"` // HTTP status, or 0 for dry-run
	Count      int       `json:"count"`  // number of results parsed
	LatencyMS  int64     `json:"latency_ms"`
	OK         bool      `json:"ok"`
	Err        string    `json:"err,omitempty"`
	TopTitles  []string  `json:"top_titles,omitempty"`
}

// Metrics holds aggregate counters and recent results.
type Metrics struct {
	mu sync.RWMutex

	startedAt   time.Time
	sent        int64
	ok          int64
	failed      int64
	totalLatMS  int64
	lastLatMS   int64
	byStatus    map[int]int64
	lastQuery   string
	lastAt      time.Time
	running     bool
	recent      []ResultSummary
	recentMax   int
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
	} else {
		m.failed++
	}
	m.totalLatMS += r.LatencyMS
	m.lastLatMS = r.LatencyMS
	if r.Status != 0 {
		m.byStatus[r.Status]++
	}
	m.lastQuery = r.Query
	m.lastAt = r.Time
	m.recent = append(m.recent, r)
	if len(m.recent) > m.recentMax {
		m.recent = m.recent[len(m.recent)-m.recentMax:]
	}
}

// Snapshot is a JSON-friendly copy of the current metrics.
type Snapshot struct {
	Running       bool            `json:"running"`
	UptimeSeconds int64           `json:"uptime_seconds"`
	Sent          int64           `json:"sent"`
	OK            int64           `json:"ok"`
	Failed        int64           `json:"failed"`
	AvgLatencyMS  int64           `json:"avg_latency_ms"`
	LastLatencyMS int64           `json:"last_latency_ms"`
	ByStatus      map[int]int64   `json:"by_status"`
	LastQuery     string          `json:"last_query"`
	LastAt        *time.Time      `json:"last_at,omitempty"`
	Recent        []ResultSummary `json:"recent"`
}

// Snapshot returns a consistent copy of all metrics for serialization.
func (m *Metrics) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var avg int64
	if m.sent > 0 {
		avg = m.totalLatMS / m.sent
	}
	byStatus := make(map[int]int64, len(m.byStatus))
	for k, v := range m.byStatus {
		byStatus[k] = v
	}
	recent := make([]ResultSummary, len(m.recent))
	copy(recent, m.recent)
	// Return newest-first for display.
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	var lastAt *time.Time
	if !m.lastAt.IsZero() {
		t := m.lastAt
		lastAt = &t
	}
	return Snapshot{
		Running:       m.running,
		UptimeSeconds: int64(time.Since(m.startedAt).Seconds()),
		Sent:          m.sent,
		OK:            m.ok,
		Failed:        m.failed,
		AvgLatencyMS:  avg,
		LastLatencyMS: m.lastLatMS,
		ByStatus:      byStatus,
		LastQuery:     m.lastQuery,
		LastAt:        lastAt,
		Recent:        recent,
	}
}
