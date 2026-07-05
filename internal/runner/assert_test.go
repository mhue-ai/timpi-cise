package runner

import (
	"errors"
	"testing"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/generate"
	"github.com/mhue-ai/timpi-cise/internal/search"
)

func TestEvaluateAssertions(t *testing.T) {
	items := []search.Item{{Title: "Timpi — decentralized search", URL: "https://timpi.io"}}

	// All good.
	a := config.Assertions{Enabled: true, MaxLatencyMS: 500, MinResults: 1}
	res := search.Result{Count: 3, LatencyMS: 120, Items: items}
	if ok, msg := evaluateAssertions(a, generate.Query{}, res, nil); !ok {
		t.Errorf("expected pass, got fail: %s", msg)
	}

	// Too slow.
	if ok, _ := evaluateAssertions(a, generate.Query{}, search.Result{Count: 3, LatencyMS: 999}, nil); ok {
		t.Error("expected fail on latency")
	}

	// Too few results.
	if ok, _ := evaluateAssertions(a, generate.Query{}, search.Result{Count: 0, LatencyMS: 10}, nil); ok {
		t.Error("expected fail on min results")
	}

	// Request error fails.
	if ok, _ := evaluateAssertions(a, generate.Query{}, search.Result{}, errors.New("boom")); ok {
		t.Error("expected fail on request error")
	}

	// Golden must-contain: present vs absent.
	q := generate.Query{MustContain: "timpi.io"}
	none := config.Assertions{}
	if ok, _ := evaluateAssertions(none, q, search.Result{Count: 1, Items: items}, nil); !ok {
		t.Error("must-contain present should pass")
	}
	if ok, _ := evaluateAssertions(none, generate.Query{MustContain: "example.com"}, search.Result{Count: 1, Items: items}, nil); ok {
		t.Error("must-contain absent should fail")
	}
}
