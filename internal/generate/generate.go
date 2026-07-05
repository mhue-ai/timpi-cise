// Package generate produces search queries. Queries come either from the
// built-in algorithmic generators (word lists + templates + combinatorial
// randomization) or from a user-supplied CSV list. Built-in question generation
// can optionally be augmented by a local (or remote) model server — Ollama or
// any OpenAI-compatible endpoint.
package generate

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

// Query is a generated search query plus the kind that produced it.
type Query struct {
	Text string
	Kind string // config.GenTerms | GenPhrases | GenQuestions
	// MustContain, if set (from a CSV golden-query column), is a substring that
	// must appear in a result for the query's assertion to pass.
	MustContain string
}

// llmClient is a model backend that completes a prompt. The generator builds
// kind-specific prompts and asks the client to complete them.
type llmClient interface {
	complete(ctx context.Context, prompt string, maxTokens int) (string, error)
}

// Generator produces queries according to a configuration.
type Generator struct {
	cfg    config.Generation
	rng    *rand.Rand
	llm    llmClient
	log    *slog.Logger
	rotate int

	// modelWarned suppresses repeat "model unreachable" warnings after the
	// first failure so a down server doesn't flood the log every minute; it is
	// reset once a call succeeds.
	modelWarned bool

	// CSV source state.
	csv    []Query
	csvIdx int
	csvErr error
}

// ListModels queries a model server for the models it has available. provider is
// config.LLMOllama or config.LLMOpenAI; baseURL/apiKey identify the server.
func ListModels(ctx context.Context, provider, baseURL, apiKey string) ([]string, error) {
	switch provider {
	case config.LLMOpenAI:
		return newOpenAIClient(baseURL, "", apiKey).listModels(ctx)
	default:
		return newOllamaClient(baseURL, "").listModels(ctx)
	}
}

// New builds a Generator. If the CSV source is selected it is loaded eagerly so
// any error is visible immediately (and logged); the optional model client is
// created only when generation is enabled.
func New(cfg config.Generation, log *slog.Logger) *Generator {
	if log == nil {
		log = slog.Default()
	}
	src := rand.NewPCG(rand.Uint64(), rand.Uint64())
	g := &Generator{cfg: cfg, rng: rand.New(src), log: log}

	if cfg.Source == config.SourceCSV {
		q, err := loadCSV(cfg.CSVPath, cfg.Shuffle, g.rng)
		g.csv, g.csvErr = q, err
		if err != nil {
			g.log.Error("could not load CSV term list", "path", cfg.CSVPath, "err", err)
		} else {
			g.log.Info("loaded CSV term list", "path", cfg.CSVPath, "queries", len(q))
		}
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

	if g.cfg.Mode == config.GenRealistic {
		return g.realistic()
	}

	kind := g.cfg.Mode
	if kind == config.GenMixed {
		kinds := []string{config.GenTerms, config.GenPhrases, config.GenQuestions}
		kind = kinds[g.rotate%len(kinds)]
		g.rotate++
	}
	switch kind {
	case config.GenTerms:
		return Query{Text: g.term(ctx), Kind: config.GenTerms}
	case config.GenPhrases:
		return Query{Text: g.phrase(ctx), Kind: config.GenPhrases}
	case config.GenQuestions:
		return Query{Text: g.question(ctx), Kind: config.GenQuestions}
	default:
		return Query{Text: g.term(ctx), Kind: config.GenTerms}
	}
}

// realistic returns a query drawn from the curated corpus with head-weighted
// sampling, so common queries recur (like real search traffic) while the long
// tail stays diverse. The kind is inferred from the text.
func (g *Generator) realistic() Query {
	i := g.zipfIndex(len(realisticQueries))
	text := realisticQueries[i]
	return Query{Text: text, Kind: inferKind(text)}
}

// zipfIndex returns an index in [0,n) biased toward 0 (the head). Taking the
// minimum of three uniform draws yields a cheap, monotonically decreasing
// distribution — head items are sampled several times more often than tail
// items, approximating a Zipfian shape without precomputed weights.
func (g *Generator) zipfIndex(n int) int {
	if n <= 1 {
		return 0
	}
	best := g.rng.IntN(n)
	for k := 0; k < 2; k++ {
		if v := g.rng.IntN(n); v < best {
			best = v
		}
	}
	return best
}

// useModel reports whether the given kind should be produced by the model.
func (g *Generator) useModel(kind string) bool {
	if g.llm == nil {
		return false
	}
	switch kind {
	case config.GenTerms:
		return g.cfg.LLM.Kinds.Terms
	case config.GenPhrases:
		return g.cfg.LLM.Kinds.Phrases
	case config.GenQuestions:
		return g.cfg.LLM.Kinds.Questions
	}
	return false
}

// fromModel builds a kind-specific prompt, calls the model, and returns a clean
// one-line result. It returns "" (caller falls back to CPU) on any error, which
// is logged. To avoid flooding the log when a server is down, the failure is
// warned once and then dropped to debug until a call succeeds again.
func (g *Generator) fromModel(ctx context.Context, kind, topic string) string {
	prompt, maxTok := promptFor(kind, topic)
	out, err := g.llm.complete(ctx, prompt, maxTok)
	if err != nil {
		if g.modelWarned {
			g.log.Debug("model generation failed; using CPU generator", "kind", kind, "provider", g.cfg.LLM.Provider, "err", err)
		} else {
			g.log.Warn("model generation failed; falling back to CPU generator (further failures logged at debug)",
				"kind", kind, "provider", g.cfg.LLM.Provider, "base_url", g.cfg.LLM.BaseURL, "err", err)
			g.modelWarned = true
		}
		return ""
	}
	res := sanitizeOneLine(out)
	if res == "" {
		g.log.Warn("model returned an empty result; using CPU generator", "kind", kind)
		return ""
	}
	if g.modelWarned {
		g.log.Info("model generation recovered", "provider", g.cfg.LLM.Provider)
		g.modelWarned = false
	}
	return res
}

// Token budgets are generous so that *reasoning* models (qwen3, deepseek-r1,
// etc.) have room to finish their hidden "thinking" AND still emit the answer —
// with a small budget they spend it all reasoning and return empty content.
// These are caps: ordinary instruct models stop at their end token well before
// the cap, so a larger budget costs them nothing.
func promptFor(kind, topic string) (prompt string, maxTokens int) {
	switch kind {
	case config.GenTerms:
		return fmt.Sprintf(
			"Give ONE short web search term (1-3 words) related to \"%s\". "+
				"Reply with only the term, lowercase, no punctuation, no quotes.", topic), 1024
	case config.GenPhrases:
		return fmt.Sprintf(
			"Give ONE natural multi-word search phrase (4-8 words) someone might "+
				"type about \"%s\". Reply with only the phrase, no quotes.", topic), 1024
	default: // questions
		return fmt.Sprintf(
			"Generate ONE realistic, specific search-engine question a curious "+
				"person might type about \"%s\". Reply with only the question, no "+
				"quotes, no preamble.", topic), 1536
	}
}

func (g *Generator) pick(s []string) string { return s[g.rng.IntN(len(s))] }

// term returns a short generic query. When the model is routed for terms and
// reachable it is used; otherwise the CPU generator produces a topic, optionally
// narrowed by a noun.
func (g *Generator) term(ctx context.Context) string {
	if g.useModel(config.GenTerms) {
		if t := g.fromModel(ctx, config.GenTerms, g.pick(topics)); t != "" {
			return t
		}
	}
	t := g.pick(topics)
	if g.rng.IntN(2) == 0 {
		return t
	}
	return t + " " + g.pick(nouns)
}

// phrase returns a multi-word phrase, from the model when routed for phrases and
// reachable, else from a CPU template.
func (g *Generator) phrase(ctx context.Context) string {
	if g.useModel(config.GenPhrases) {
		if p := g.fromModel(ctx, config.GenPhrases, g.pick(topics)); p != "" {
			return p
		}
	}
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

// question returns a natural-language question, from the model when routed for
// questions and reachable, else from a CPU template.
func (g *Generator) question(ctx context.Context) string {
	if g.useModel(config.GenQuestions) {
		if q := g.fromModel(ctx, config.GenQuestions, g.pick(topics)); q != "" {
			return q
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
