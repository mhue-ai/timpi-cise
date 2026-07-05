// Package rotate provides a size-based rotating file writer, used for the app
// log so it cannot grow without bound. When the file would exceed the size
// limit it is renamed with a timestamp and a fresh file is started.
package rotate

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Writer is an io.WriteCloser that rotates its backing file by size.
type Writer struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

// New opens (appending to) path and rotates once it would exceed maxBytes.
func New(path string, maxBytes int64) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	var size int64
	if info, serr := f.Stat(); serr == nil {
		size = info.Size()
	}
	return &Writer{path: path, max: maxBytes, f: f, size: size}, nil
}

// Write implements io.Writer, rotating first if the write would exceed the size
// limit.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.max > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			// If rotation fails, keep writing to the current file rather than
			// dropping the log line.
			w.size = 0
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate renames the current file with a timestamp and opens a fresh one. The
// caller must hold w.mu.
func (w *Writer) rotate() error {
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
	w.size = 0
	return nil
}

// Close closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
