package reslog

import (
	"encoding/csv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWriteAndHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	w, err := Open(path, quiet())
	if err != nil {
		t.Fatal(err)
	}
	pass := true
	if err := w.Write(metrics.ResultSummary{
		Time: time.Now(), Mode: "dry-run", Kind: "terms", Query: "hello, world",
		Status: 200, Count: 3, LatencyMS: 42, OK: true, AssertPass: &pass,
	}); err != nil {
		t.Fatal(err)
	}
	w.Close()

	f, _ := os.Open(path)
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d", len(rows))
	}
	if rows[0][0] != "time" || rows[0][8] != "assert" {
		t.Errorf("header wrong: %v", rows[0])
	}
	// The query with a comma must round-trip intact (CSV quoting).
	if rows[1][3] != "hello, world" {
		t.Errorf("query not preserved: %q", rows[1][3])
	}
	if rows[1][8] != "pass" {
		t.Errorf("assert col = %q", rows[1][8])
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.csv")
	w, err := Open(path, quiet())
	if err != nil {
		t.Fatal(err)
	}
	// Force the rotation threshold low so a couple of writes trigger it.
	w.rotateAt = 200
	for i := 0; i < 50; i++ {
		_ = w.Write(metrics.ResultSummary{Time: time.Now(), Query: strings.Repeat("x", 40), OK: true})
	}
	w.Close()

	// A timestamped archive file must exist alongside the fresh current file.
	entries, _ := os.ReadDir(dir)
	var archives int
	for _, e := range entries {
		if e.Name() != "results.csv" && strings.HasPrefix(e.Name(), "results-") {
			archives++
		}
	}
	if archives == 0 {
		t.Fatalf("expected at least one rotated archive, found none in %v", entries)
	}
}

func TestArchivesOldSchemaHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.csv")
	// Simulate a file written by an older version with fewer columns.
	old := "time,mode,kind,query,status,count,latency_ms,ok,error,top_title\n" +
		"2026-01-01T00:00:00Z,dry-run,terms,old,0,1,0,true,,x\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Open(path, quiet())
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	// The current file must now start with the NEW header.
	if got := firstLine(path); got != strings.Join(header, ",") {
		t.Errorf("current file header = %q, want new schema", got)
	}
	// The old file must have been archived (not lost).
	entries, _ := os.ReadDir(dir)
	var archived bool
	for _, e := range entries {
		if e.Name() != "results.csv" && strings.HasPrefix(e.Name(), "results-") {
			archived = true
		}
	}
	if !archived {
		t.Errorf("old-schema file was not archived: %v", entries)
	}
}

func TestUseAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.csv")
	w, _ := Open(path, quiet())
	w.Close()
	w.Close() // idempotent
	if err := w.Write(metrics.ResultSummary{Time: time.Now()}); err != ErrClosed {
		t.Errorf("write after close should return ErrClosed, got %v", err)
	}
}
