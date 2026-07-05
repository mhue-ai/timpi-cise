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

	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

// ErrClosed is returned by Write after the writer has been closed.
var ErrClosed = errors.New("reslog: writer is closed")

var header = []string{
	"time", "mode", "kind", "query", "status", "count", "latency_ms", "ok", "error", "top_title",
}

// Writer appends result rows to a CSV file.
type Writer struct {
	mu     sync.Mutex
	f      *os.File
	w      *csv.Writer
	path   string
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
	if info.Size() == 0 {
		if err := w.Write(header); err != nil {
			f.Close()
			return nil, err
		}
		w.Flush()
		if err := w.Error(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return &Writer{f: f, w: w, path: path}, nil
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
	top := ""
	if len(r.TopTitles) > 0 {
		top = r.TopTitles[0]
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
		r.Err,
		top,
	}
	if err := w.w.Write(rec); err != nil {
		return err
	}
	w.w.Flush()
	return w.w.Error()
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
