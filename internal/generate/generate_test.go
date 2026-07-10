package generate

import (
	"context"
	"strings"
	"testing"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

func gen(mode string) *Generator {
	c := config.Default().Generation
	c.Mode = mode
	c.Source = config.SourceBuiltin
	c.LLM.Enabled = false // exercise the built-in CPU generator only (no model server)
	return New(c, nil)
}

func TestGeneratorsProduceValidQueries(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{config.GenTerms, config.GenPhrases, config.GenQuestions, config.GenRealistic} {
		g := gen(mode)
		for i := 0; i < 30; i++ {
			q := g.Next(ctx)
			if strings.TrimSpace(q.Text) == "" {
				t.Errorf("%s produced empty query", mode)
			}
			switch q.Kind {
			case config.GenTerms, config.GenPhrases, config.GenQuestions:
			default:
				t.Errorf("%s produced invalid kind %q", mode, q.Kind)
			}
		}
	}
}

func TestMixedRotatesKinds(t *testing.T) {
	g := gen(config.GenMixed)
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		seen[g.Next(context.Background()).Kind] = true
	}
	for _, k := range []string{config.GenTerms, config.GenPhrases, config.GenQuestions} {
		if !seen[k] {
			t.Errorf("mixed mode never produced %q", k)
		}
	}
}

func TestRealisticDrawsFromCorpus(t *testing.T) {
	g := gen(config.GenRealistic)
	set := map[string]bool{}
	for _, q := range realisticQueries {
		set[q] = true
	}
	for i := 0; i < 50; i++ {
		if !set[g.Next(context.Background()).Text] {
			t.Fatal("realistic produced a query not in the corpus")
		}
	}
}

func TestZipfHeadBias(t *testing.T) {
	g := gen(config.GenRealistic)
	n := len(realisticQueries)
	var headHalf int
	const trials = 4000
	for i := 0; i < trials; i++ {
		if g.zipfIndex(n) < n/2 {
			headHalf++
		}
	}
	// Head-weighted sampling should put well over half of draws in the first
	// half of the corpus (uniform would be ~50%).
	if headHalf < trials*65/100 {
		t.Errorf("head bias too weak: %d/%d in head half", headHalf, trials)
	}
}

func TestSanitizeOneLine(t *testing.T) {
	cases := map[string]string{
		"  hello world  ":       "hello world",
		"\"quoted\"":            "quoted",
		"1. first line\nsecond": "first line",
		"- bullet":              "bullet",
	}
	for in, want := range cases {
		if got := sanitizeOneLine(in); got != want {
			t.Errorf("sanitizeOneLine(%q) = %q, want %q", in, got, want)
		}
	}
}
