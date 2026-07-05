package metrics

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	vals := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentile(vals, 0.50); got != 50 {
		t.Errorf("p50 = %d, want 50", got)
	}
	if got := percentile(vals, 0.95); got != 100 {
		t.Errorf("p95 = %d, want 100", got)
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("empty p50 = %d, want 0", got)
	}
}

func TestZeroResultAndAsserts(t *testing.T) {
	m := New(10)
	now := time.Now()
	pass, fail := true, false
	m.Record(ResultSummary{Time: now, OK: true, Count: 5, LatencyMS: 100, AssertPass: &pass})
	m.Record(ResultSummary{Time: now, OK: true, Count: 0, LatencyMS: 200, AssertPass: &fail}) // zero result + assert fail
	m.Record(ResultSummary{Time: now, OK: false, Count: 0, LatencyMS: 50})                    // failure

	s := m.Snapshot()
	if s.Sent != 3 || s.OK != 2 || s.Failed != 1 {
		t.Fatalf("counts sent=%d ok=%d failed=%d", s.Sent, s.OK, s.Failed)
	}
	if s.ZeroResults != 1 {
		t.Errorf("zero results = %d, want 1", s.ZeroResults)
	}
	if s.ZeroResultRate != 0.5 { // 1 of 2 OK responses
		t.Errorf("zero-result rate = %v, want 0.5", s.ZeroResultRate)
	}
	if s.AssertRun != 2 || s.AssertFail != 1 {
		t.Errorf("assert run=%d fail=%d, want 2/1", s.AssertRun, s.AssertFail)
	}
	if len(s.Series) != 1 || s.Series[0].Sent != 3 {
		t.Errorf("series = %+v, want one bucket with sent=3", s.Series)
	}
}
