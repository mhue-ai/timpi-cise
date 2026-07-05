package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPersistRoundTrip(t *testing.T) {
	m := New(10)
	now := time.Now()
	pass, fail := true, false
	m.Record(ResultSummary{Time: now, OK: true, Count: 5, LatencyMS: 100, Status: 200, AssertPass: &pass})
	m.Record(ResultSummary{Time: now, OK: true, Count: 0, LatencyMS: 200, Status: 200, AssertPass: &fail})
	m.Record(ResultSummary{Time: now, OK: false, Count: 0, LatencyMS: 50})

	path := filepath.Join(t.TempDir(), "metrics.json")
	if err := m.SaveTo(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Restore into a fresh Metrics and compare the durable snapshot.
	m2 := New(10)
	if err := m2.LoadFrom(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	a, b := m.Snapshot(), m2.Snapshot()
	if a.Sent != b.Sent || a.OK != b.OK || a.Failed != b.Failed {
		t.Errorf("counts differ: %+v vs %+v", a, b)
	}
	if a.ZeroResults != b.ZeroResults || a.AssertFail != b.AssertFail || a.AssertRun != b.AssertRun {
		t.Errorf("derived differ: zero %d/%d assertFail %d/%d", a.ZeroResults, b.ZeroResults, a.AssertFail, b.AssertFail)
	}
	if a.P95MS != b.P95MS {
		t.Errorf("p95 differ: %d vs %d", a.P95MS, b.P95MS)
	}
	if len(a.Series) != len(b.Series) || (len(b.Series) > 0 && b.Series[0].Sent != 3) {
		t.Errorf("series not restored: %+v", b.Series)
	}
	if len(b.Recent) != 3 {
		t.Errorf("recent not restored: %d", len(b.Recent))
	}
}

func TestLoadMissingFileIsOK(t *testing.T) {
	m := New(10)
	if err := m.LoadFrom(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Errorf("missing file should not error: %v", err)
	}
}
