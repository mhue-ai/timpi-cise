package generate

import (
	"encoding/csv"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

// loadCSV reads a user query list. The file may be a plain list (one query per
// line) or a CSV whose first column is the query and optional second column is
// the kind (terms/phrases/questions). A leading header row like "query,kind" or
// "term" is skipped automatically. If shuffle is true the order is randomized.
func loadCSV(path string, shuffle bool, rng *rand.Rand) ([]Query, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // allow ragged rows / plain lines
	r.TrimLeadingSpace = true

	var out []Query
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if len(rec) == 0 {
			continue
		}
		text := strings.TrimSpace(rec[0])
		if text == "" {
			continue
		}
		// Skip an obvious header row.
		if first {
			first = false
			low := strings.ToLower(text)
			if low == "query" || low == "term" || low == "queries" || low == "search" {
				continue
			}
		}
		kind := ""
		if len(rec) > 1 {
			kind = normalizeKind(rec[1])
		}
		if kind == "" {
			kind = inferKind(text)
		}
		mustContain := ""
		if len(rec) > 2 {
			mustContain = strings.TrimSpace(rec[2])
		}
		out = append(out, Query{Text: text, Kind: kind, MustContain: mustContain})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%s contained no usable queries", path)
	}
	if shuffle {
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out, nil
}

func normalizeKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case config.GenTerms, "term":
		return config.GenTerms
	case config.GenPhrases, "phrase":
		return config.GenPhrases
	case config.GenQuestions, "question":
		return config.GenQuestions
	default:
		return ""
	}
}

// inferKind guesses a kind from the query text so the dashboard can label it.
func inferKind(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if strings.HasSuffix(t, "?") {
		return config.GenQuestions
	}
	for _, w := range []string{"how ", "what ", "why ", "when ", "where ", "who ", "which ", "is ", "are ", "can ", "do ", "does "} {
		if strings.HasPrefix(t, w) {
			return config.GenQuestions
		}
	}
	if strings.Contains(t, " ") {
		return config.GenPhrases
	}
	return config.GenTerms
}
