// Package reslog appends each executed query result to a CSV file so runs can be
// audited or analyzed later. It is safe for concurrent use, flushes after every
// row, and rotates by size so the file cannot grow without bound. Rotation never
// destroys existing data: if the rotated-file rename fails (e.g. the file is
// held open on Windows), it keeps appending to the current file instead.
package reslog

import (
	"bufio"
	"encoding/csv"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

// firstLine returns the first line of a file, or "" on any error.
func firstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		return strings.TrimRight(sc.Text(), "\r\n")
	}
	return ""
}

// ErrClosed is returned by Write after the writer has been closed.
var ErrClosed = errors.New("reslog: writer is closed")

// maxBytes is the size at which the CSV is rotated to a timestamped file.
const maxBytes = 10 << 20 // 10 MiB

var header = []string{
	"time", "mode", "kind", "query", "status", "count", "latency_ms", "ok", "assert", "assert_detail", "note", "error", "top_title",
}

// Writer appends result rows to a CSV file.
type Writer struct {
	mu       sync.Mutex
	log      *slog.Logger
	f        *os.File
	w        *csv.Writer
	path     string
	size     int64 // current on-disk size (kept accurate via Stat)
	rotateAt int64 // rotate once size reaches this
	closed   bool
}

// Open creates (or appends to) the CSV file at path, writing a header row if the
// file is new or empty. Parent directories are created as needed.
func Open(path string, log *slog.Logger) (*Writer, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// If an existing file has a different (older) header, archive it so new rows
	// aren't appended under a mismatched schema.
	if info, serr := os.Stat(path); serr == nil && info.Size() > 0 {
		if existing := firstLine(path); existing != "" && existing != strings.Join(header, ",") {
			ts := time.Now().Format("20060102-150405")
			ext := filepath.Ext(path)
			archived := path[:len(path)-len(ext)] + "-" + ts + ext
			if rerr := os.Rename(path, archived); rerr != nil {
				log.Warn("results CSV: could not archive old-schema file", "err", rerr)
			} else {
				log.Info("results CSV: schema changed since last run, archived old file", "archived", archived)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
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
	wr := &Writer{log: log, f: f, w: w, path: path, rotateAt: maxBytes}
	wr.refreshSize()
	return wr, nil
}

// refreshSize reads the true file size. The caller must hold w.mu.
func (w *Writer) refreshSize() {
	if s, err := w.f.Stat(); err == nil {
		w.size = s.Size()
	}
}

// Path returns the CSV file path.
func (w *Writer) Path() string { return w.path }

// rotate renames the current file with a timestamp and starts a fresh one. If
// the rename fails (commonly on Windows when the file is held open elsewhere),
// it does NOT truncate — it keeps appending to the current file and defers the
// next rotation attempt. The caller must hold w.mu.
func (w *Writer) rotate() {
	w.w.Flush()
	if err := w.f.Close(); err != nil {
		w.log.Warn("results CSV: close before rotate failed", "err", err)
	}

	ts := time.Now().Format("20060102-150405")
	ext := filepath.Ext(w.path)
	base := w.path[:len(w.path)-len(ext)]
	rotated := base + "-" + ts + ext

	if err := os.Rename(w.path, rotated); err != nil {
		// Preserve data: reopen the SAME file for append (no truncation) and try
		// again after it grows another maxBytes.
		w.log.Warn("results CSV: rotation rename failed; continuing on current file", "err", err)
		f, oerr := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if oerr != nil {
			w.log.Error("results CSV: reopen after failed rotate failed", "err", oerr)
			return
		}
		w.f = f
		w.w = csv.NewWriter(f)
		w.refreshSize()
		w.rotateAt = w.size + maxBytes
		return
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		w.log.Error("results CSV: open fresh file after rotate failed", "err", err)
		return
	}
	w.f = f
	w.w = csv.NewWriter(f)
	if err := w.w.Write(header); err != nil {
		w.log.Warn("results CSV: header write after rotate failed", "err", err)
	}
	w.w.Flush()
	w.refreshSize()
	w.rotateAt = maxBytes
	w.log.Info("results CSV rotated", "archived", rotated)
}

// Write appends one result row and flushes.
func (w *Writer) Write(r metrics.ResultSummary) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if w.size >= w.rotateAt {
		w.rotate()
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
		r.Mode, r.Kind, r.Query,
		strconv.Itoa(r.Status), strconv.Itoa(r.Count), strconv.FormatInt(r.LatencyMS, 10),
		strconv.FormatBool(r.OK), assert, r.AssertMsg, r.Note, r.Err, top,
	}
	if err := w.w.Write(rec); err != nil {
		return err
	}
	w.w.Flush()
	if err := w.w.Error(); err != nil {
		return err
	}
	w.refreshSize() // accurate size (accounts for CSV quoting), not an estimate
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
