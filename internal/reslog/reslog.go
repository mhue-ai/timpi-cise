// Package reslog appends each executed query result to a CSV file so runs can be
// audited or analyzed later. It is safe for concurrent use and flushes after
// every row so nothing is lost if the process is killed.
package reslog

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

// ErrClosed is returned by Write after the writer has been closed.
var ErrClosed = errors.New("reslog: writer is closed")

// maxBytes is the size at which the CSV is rotated to a timestamped file and a
// fresh one is started, so the results log cannot grow without bound.
const maxBytes = 10 << 20 // 10 MiB

var header = []string{
	"time", "mode", "kind", "query", "status", "count", "latency_ms", "ok", "assert", "assert_detail", "note", "error", "top_title",
}

// Writer appends result rows to a CSV file.
type Writer struct {
	mu     sync.Mutex
	f      *os.File
	w      *csv.Writer
	path   string
	size   int64
	closed bool
}

// Open creates (or appends to) the CSV file at path, writing a header row if the
// file is new or empty. Parent directories are created as needed.
func Open(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	w := csv.NewWriter(f)
	size := info.Size()
	if size == 0 {
		if err := w.Write(header); err != nil {
			f.Close()
			return nil, err
		}
		w.Flush()
		if err := w.Error(); err != nil {
			f.Close()
			return nil, err
		}
		if s, serr := f.Stat(); serr == nil {
			size = s.Size()
		}
	}
	return &Writer{f: f, w: w, path: path, size: size}, nil
}

// rotate closes the current file, renames it with a timestamp, and opens a fresh
// file (with header). The caller must hold w.mu.
func (w *Writer) rotate() error {
	w.w.Flush()
	if err := w.f.Close(); err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	ext := filepath.Ext(w.path)
	base := w.path[:len(w.path)-len(ext)]
	_ = os.Rename(w.path, base+"-"+ts+ext)

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.f = f
	w.w = csv.NewWriter(f)
	if err := w.w.Write(header); err != nil {
		return err
	}
	w.w.Flush()
	if s, serr := f.Stat(); serr == nil {
		w.size = s.Size()
	} else {
		w.size = 0
	}
	return w.w.Error()
}

// Path returns the CSV file path.
func (w *Writer) Path() string { return w.path }

// Write appends one result row and flushes.
func (w *Writer) Write(r metrics.ResultSummary) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if w.size >= maxBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	top := ""
	if len(r.TopTitles) > 0 {
		top = r.TopTitles[0]
	}
	assert := ""
	if r.AssertPass != nil {
		if *r.AssertPass {
			assert = "pass"
		} else {
			assert = "fail"
		}
	}
	rec := []string{
		r.Time.Format("2006-01-02T15:04:05Z07:00"),
		r.Mode,
		r.Kind,
		r.Query,
		strconv.Itoa(r.Status),
		strconv.Itoa(r.Count),
		strconv.FormatInt(r.LatencyMS, 10),
		strconv.FormatBool(r.OK),
		assert,
		r.AssertMsg,
		r.Note,
		r.Err,
		top,
	}
	if err := w.w.Write(rec); err != nil {
		return err
	}
	w.w.Flush()
	if err := w.w.Error(); err != nil {
		return err
	}
	// Approximate the on-disk growth for the rotation check.
	for _, f := range rec {
		w.size += int64(len(f)) + 1
	}
	w.size++ // newline
	return nil
}

// Close flushes and closes the file. It is idempotent.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.w != nil {
		w.w.Flush()
	}
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}
