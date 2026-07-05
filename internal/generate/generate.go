// Package generate produces search queries: short terms, multi-word phrases, and
// natural-language questions. Generation is algorithmic by default (word lists +
// templates + combinatorial randomization) and can optionally be augmented by a
// local Ollama model for richer, GPU-accelerated question generation.
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

// Generator produces queries according to a configuration.
type Generator struct {
	cfg    config.Generation
	rng    *rand.Rand
	ollama *ollamaClient
	// rotate cycles through kinds when Mode == mixed.
	rotate int
}

// New builds a Generator. It seeds a PRNG deterministically from the process so
// runs vary; the optional Ollama client is created lazily only when enabled.
func New(cfg config.Generation) *Generator {
	// rand/v2 top-level functions are already seeded randomly; we keep a local
	// source so behavior is self-contained and testable.
	src := rand.NewPCG(rand.Uint64(), rand.Uint64())
	g := &Generator{cfg: cfg, rng: rand.New(src)}
	if cfg.UseOllama {
		g.ollama = newOllamaClient(cfg.OllamaURL, cfg.OllamaModel)
	}
	return g
}

// Next returns the next generated query. ctx bounds any optional Ollama call.
func (g *Generator) Next(ctx context.Context) Query {
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

// question returns a natural-language question. When Ollama is enabled and
// reachable it is used for a richer, more varied question; otherwise a template
// is filled in. Any Ollama error falls back silently to the template.
func (g *Generator) question(ctx context.Context) string {
	if g.ollama != nil {
		topic := g.pick(topics)
		if q, err := g.ollama.question(ctx, topic); err == nil && strings.TrimSpace(q) != "" {
			return sanitizeOneLine(q)
		}
	}
	tmpl := g.pick(questionTemplates)
	return fmt.Sprintf(tmpl, g.pick(topics))
}

// sanitizeOneLine trims an LLM response down to a single clean query line.
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
