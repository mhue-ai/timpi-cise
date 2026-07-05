// Package generate produces search queries. Queries come either from the
// built-in algorithmic generators (word lists + templates + combinatorial
// randomization) or from a user-supplied CSV list. Built-in question generation
// can optionally be augmented by a local (or remote) model server — Ollama or
// any OpenAI-compatible endpoint.
package generate

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

// Query is a generated search query plus the kind that produced it.
type Query struct {
	Text string
	Kind string // config.GenTerms | GenPhrases | GenQuestions
}

// llmClient is a model backend that can turn a topic into a question.
type llmClient interface {
	question(ctx context.Context, topic string) (string, error)
}

// Generator produces queries according to a configuration.
type Generator struct {
	cfg  config.Generation
	rng  *rand.Rand
	llm  llmClient
	rotate int

	// CSV source state.
	csv    []Query
	csvIdx int
	csvErr error
}

// New builds a Generator. If the CSV source is selected it is loaded eagerly so
// any error is visible immediately; the optional model client is created only
// when generation is enabled.
func New(cfg config.Generation) *Generator {
	src := rand.NewPCG(rand.Uint64(), rand.Uint64())
	g := &Generator{cfg: cfg, rng: rand.New(src)}

	if cfg.Source == config.SourceCSV {
		q, err := loadCSV(cfg.CSVPath, cfg.Shuffle, g.rng)
		g.csv, g.csvErr = q, err
	}

	if cfg.LLM.Enabled {
		switch cfg.LLM.Provider {
		case config.LLMOpenAI:
			g.llm = newOpenAIClient(cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.APIKey)
		default:
			g.llm = newOllamaClient(cfg.LLM.BaseURL, cfg.LLM.Model)
		}
	}
	return g
}

// CSVCount returns how many queries were loaded from the CSV source (0 if not
// using CSV or if loading failed).
func (g *Generator) CSVCount() int { return len(g.csv) }

// CSVError returns any error from loading the CSV source.
func (g *Generator) CSVError() error { return g.csvErr }

// Next returns the next generated query. ctx bounds any optional model call.
func (g *Generator) Next(ctx context.Context) Query {
	// CSV source takes precedence when it has usable content.
	if g.cfg.Source == config.SourceCSV && len(g.csv) > 0 {
		q := g.csv[g.csvIdx%len(g.csv)]
		g.csvIdx++
		return q
	}

	kind := g.cfg.Mode
	if kind == config.GenMixed {
		kinds := []string{config.GenTerms, config.GenPhrases, config.GenQuestions}
		kind = kinds[g.rotate%len(kinds)]
		g.rotate++
	}
	switch kind {
	case config.GenTerms:
		return Query{Text: g.term(), Kind: config.GenTerms}
	case config.GenPhrases:
		return Query{Text: g.phrase(), Kind: config.GenPhrases}
	case config.GenQuestions:
		return Query{Text: g.question(ctx), Kind: config.GenQuestions}
	default:
		return Query{Text: g.term(), Kind: config.GenTerms}
	}
}

func (g *Generator) pick(s []string) string { return s[g.rng.IntN(len(s))] }

// term returns a short generic query: a topic, optionally narrowed by a noun.
func (g *Generator) term() string {
	t := g.pick(topics)
	if g.rng.IntN(2) == 0 {
		return t
	}
	return t + " " + g.pick(nouns)
}

// phrase returns a multi-word phrase from a template.
func (g *Generator) phrase() string {
	tmpl := g.pick(phraseTemplates)
	adj := g.pick(adjectives)
	topic := g.pick(topics)
	conn := g.pick(connectors)
	switch strings.Count(tmpl, "%s") {
	case 3:
		return fmt.Sprintf(tmpl, adj, topic, conn)
	case 2:
		return fmt.Sprintf(tmpl, adj, topic)
	default:
		return fmt.Sprintf(tmpl, topic)
	}
}

// question returns a natural-language question. When a model client is enabled
// and reachable it is used for a richer question; otherwise a template is filled
// in. Any model error falls back silently to the template.
func (g *Generator) question(ctx context.Context) string {
	if g.llm != nil {
		topic := g.pick(topics)
		if q, err := g.llm.question(ctx, topic); err == nil && strings.TrimSpace(q) != "" {
			return sanitizeOneLine(q)
		}
	}
	tmpl := g.pick(questionTemplates)
	return fmt.Sprintf(tmpl, g.pick(topics))
}

// sanitizeOneLine trims a model response down to a single clean query line.
func sanitizeOneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, "\"'` ")
	// Strip a leading list marker like "1." or "- ".
	s = strings.TrimLeft(s, "-*0123456789. ")
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}
