package generate

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/mhue-ai/timpi-cise/internal/config"
)

func newRNG() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestInferKind(t *testing.T) {
	cases := map[string]string{
		"privacy":                             config.GenTerms,
		"best privacy tools":                  config.GenPhrases,
		"how does decentralized search work?": config.GenQuestions,
		"what is a node":                      config.GenQuestions, // question word, no '?'
		"electric vehicles":                   config.GenPhrases,
	}
	for in, want := range cases {
		if got := inferKind(in); got != want {
			t.Errorf("inferKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadCSV(t *testing.T) {
	dir := t.TempDir()

	// Header row skipped; kind column honored; kind inferred otherwise.
	p := filepath.Join(dir, "terms.csv")
	content := "query,kind\n" +
		"solar panels\n" +
		"how to compost at home?\n" +
		"lithium batteries,phrases\n" +
		"\n" + // blank line ignored
		"  spaced term  \n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCSV(p, false, newRNG())
	if err != nil {
		t.Fatalf("loadCSV: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d queries, want 4: %+v", len(got), got)
	}
	if got[0].Text != "solar panels" || got[0].Kind != config.GenPhrases {
		t.Errorf("row0 = %+v", got[0])
	}
	if got[1].Kind != config.GenQuestions {
		t.Errorf("row1 kind = %q, want questions", got[1].Kind)
	}
	if got[2].Kind != config.GenPhrases { // explicit column
		t.Errorf("row2 kind = %q, want phrases", got[2].Kind)
	}
	if got[3].Text != "spaced term" {
		t.Errorf("row3 not trimmed: %q", got[3].Text)
	}
}

func TestLoadCSVEmptyErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.csv")
	if err := os.WriteFile(p, []byte("\n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCSV(p, false, newRNG()); err == nil {
		t.Fatal("expected error for empty CSV, got nil")
	}
	if _, err := loadCSV(filepath.Join(dir, "missing.csv"), false, newRNG()); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
